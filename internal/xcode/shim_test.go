package xcode

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/bitrise-steplib/bitrise-step-test-selection/internal/graph"
)

// The app's targets link one product per Xcode target, declared through manifest
// helpers instead of literal `.target(name:)` calls, the way woocommerce-ios
// (name constant plus a name transform) and WordPress-iOS (literal name, dependencies
// in a variable) do it.
const shimPbxproj = `// !$*UTF8*$!
{
	objects = {
		PAPP = {isa = PBXFileReference; explicitFileType = wrapper.application; path = App.app; sourceTree = BUILT_PRODUCTS_DIR; };
		PTESTS = {isa = PBXFileReference; explicitFileType = wrapper.cfbundle; path = AppTests.xctest; sourceTree = BUILT_PRODUCTS_DIR; };
		PWIDGET = {isa = PBXFileReference; explicitFileType = wrapper.app-extension; path = Widget.appex; sourceTree = BUILT_PRODUCTS_DIR; };
		PWTESTS = {isa = PBXFileReference; explicitFileType = wrapper.cfbundle; path = WidgetTests.xctest; sourceTree = BUILT_PRODUCTS_DIR; };
		MAIN = {isa = PBXGroup; children = (); sourceTree = "<group>"; };

		APP = {isa = PBXNativeTarget; name = App; productType = "com.apple.product-type.application"; productReference = PAPP; buildPhases = (); packageProductDependencies = (DEPAPP); buildConfigurationList = CLAPP; };
		APPTESTS = {isa = PBXNativeTarget; name = AppTests; productType = "com.apple.product-type.bundle.unit-test"; productReference = PTESTS; buildPhases = (); packageProductDependencies = (DEPAPPTESTS); buildConfigurationList = CLTESTS; };
		WIDGET = {isa = PBXNativeTarget; name = Widget; productType = "com.apple.product-type.app-extension"; productReference = PWIDGET; buildPhases = (); packageProductDependencies = (DEPWIDGET); buildConfigurationList = CLAPP; };
		WIDGETTESTS = {isa = PBXNativeTarget; name = WidgetTests; productType = "com.apple.product-type.bundle.unit-test"; productReference = PWTESTS; buildPhases = (); dependencies = (TDWIDGET); buildConfigurationList = CLAPP; };
		TDWIDGET = {isa = PBXTargetDependency; target = WIDGET; };

		CLAPP = {isa = XCConfigurationList; buildConfigurations = (CFGAPP); };
		CLTESTS = {isa = XCConfigurationList; buildConfigurations = (CFGTESTS); };
		CFGAPP = {isa = XCBuildConfiguration; name = Debug; buildSettings = { }; };
		CFGTESTS = {isa = XCBuildConfiguration; name = Debug; buildSettings = { TEST_HOST = "$(BUILT_PRODUCTS_DIR)/App.app/App"; }; };

		MODULES = {isa = XCLocalSwiftPackageReference; relativePath = Modules; };
		DEPAPP = {isa = XCSwiftPackageProductDependency; package = MODULES; productName = XcodeTarget_App; };
		DEPAPPTESTS = {isa = XCSwiftPackageProductDependency; package = MODULES; productName = XcodeTarget_AppTests; };
		DEPWIDGET = {isa = XCSwiftPackageProductDependency; package = MODULES; productName = XcodeTarget_Widget; };

		PROJ = {isa = PBXProject; mainGroup = MAIN; projectDirPath = ""; targets = (APP, APPTESTS, WIDGET, WIDGETTESTS); packageReferences = (MODULES); };
	};
	rootObject = PROJ;
}
`

const shimManifest = `// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "Modules",
    products: XcodeSupport.products + [
        .library(name: "Feature", targets: ["Feature"]),
        .library(name: "XcodeTarget_Widget", targets: ["XcodeTarget_Widget"]),
    ],
    dependencies: [
        .package(url: "https://github.com/Alamofire/Alamofire", from: "5.0.0"),
        .package(path: "../Tools"),
    ],
    targets: XcodeSupport.targets + [
        .target(name: "Charts"),
        .target(name: "Feature"),
        .target(name: "Mocks"),
        .target(name: "Shared"),
        .target(name: "Unlinked"),
        .testTarget(name: "FeatureTests", dependencies: ["Feature"]),
    ]
)

enum XcodeTargetNames {
    static let app = "App"
    static let appTests = "AppTests"
}

enum XcodeSupport {
    static var products: [Product] {
        [XcodeTargetNames.app, XcodeTargetNames.appTests].map { .supportingProduct(forXcodeTarget: $0) }
    }

    static var targets: [Target] {
        let widgetDependencies: [Target.Dependency] = [
            .target(name: "Shared"),
            .product(name: "Alamofire", package: "Alamofire"),
        ]

        return [
            .xcodeTarget(
                XcodeTargetNames.app,
                dependencies: [
                    "Feature",
                    .product(name: "Alamofire", package: "Alamofire"),
                ]
            ),
            .xcodeTarget(
                XcodeTargetNames.appTests,
                dependencies: [
                    "Mocks",
                    XcodeTargetNames.app.asDependency
                ]
            ),
            .shimTarget("XcodeTarget_Widget", dependencies: widgetDependencies + ["Charts"]),
        ]
    }
}

extension Product {
    static func supportingProduct(forXcodeTarget targetName: String) -> Product {
        .library(
            name: "XcodeTarget_\(targetName)",
            targets: ["XcodeTarget_\(targetName)"]
        )
    }
}

extension Target {
    static func xcodeTarget(_ name: String, dependencies: [Dependency]) -> Target {
        .target(
            name: name.supportingName,
            dependencies: dependencies,
            path: "Sources/XcodeSupport/\(name.supportingName)"
        )
    }

    static func shimTarget(_ name: String, dependencies: [Dependency]) -> Target {
        .target(name: name, dependencies: dependencies, path: "Sources/XcodeSupport/\(name)")
    }
}

extension String {
    var supportingName: String {
        "XcodeTarget_\(self)"
    }

    var asDependency: Target.Dependency {
        .target(name: self.supportingName)
    }
}
`

// recordedSwift answers dump-package with testdata/shim-dump-package.json, recorded by
// running `swift package dump-package` on shimManifest with @ROOT@ in place of the
// fixture root. A warning line goes first because swift's diagnostics share the stream.
type recordedSwift struct{ root string }

func (r recordedSwift) Run(dir, name string, args ...string) (string, error) {
	data, err := os.ReadFile(filepath.Join("testdata", "shim-dump-package.json"))
	if err != nil {
		return "", err
	}
	// SwiftPM prints paths with symlinks resolved.
	real, err := filepath.EvalSymlinks(r.root)
	if err != nil {
		return "", err
	}
	return "warning: 'modules': a diagnostic\n" + strings.ReplaceAll(string(data), "@ROOT@", real), nil
}

type failingSwift struct{}

func (failingSwift) Run(dir, name string, args ...string) (string, error) {
	return "error: 'modules': invalid manifest", errors.New("exit status 1")
}

type execSwift struct{}

func (execSwift) Run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeShimFixture(t *testing.T) string {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "App.xcodeproj", "project.pbxproj"), shimPbxproj)
	writeFile(t, filepath.Join(root, "Modules", "Package.swift"), shimManifest)
	return root
}

func xcodeDependents(g *graph.Graph, file string) []string {
	var out []string
	for _, id := range keys(g.Dependents(g.Owners(file))) {
		if strings.HasPrefix(id, "xcode:") {
			out = append(out, id)
		}
	}
	return out
}

func TestBuildGraphXcodeTargetShims(t *testing.T) {
	root := writeShimFixture(t)
	var logs []string
	logf := func(format string, v ...interface{}) { logs = append(logs, fmt.Sprintf(format, v...)) }
	g, _, err := Build(Options{Root: root, ProjectPath: "App.xcodeproj", Swift: "swift", Cmd: recordedSwift{root}, Logf: logf})
	if err != nil {
		t.Fatal(err)
	}
	assertShimGraph(t, g)
	if len(logs) != 0 {
		t.Errorf("unexpected fallback: %v", logs)
	}
}

func TestBuildGraphXcodeTargetShimsWithSwift(t *testing.T) {
	swift, err := exec.LookPath("swift")
	if err != nil {
		t.Skip("swift is not on PATH")
	}
	root := writeShimFixture(t)
	var logs []string
	logf := func(format string, v ...interface{}) { logs = append(logs, fmt.Sprintf(format, v...)) }
	g, _, err := Build(Options{Root: root, ProjectPath: "App.xcodeproj", Swift: swift, Cmd: execSwift{}, Logf: logf})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 0 {
		t.Fatalf("dump-package did not run: %v", logs)
	}
	assertShimGraph(t, g)
	if _, err := os.Stat(filepath.Join(root, "Modules", ".build")); err == nil {
		t.Error("dump-package left .build in the package")
	}
}

func TestBuildGraphManifestScannerFallback(t *testing.T) {
	cases := map[string]Options{
		"no swift":             {},
		"dump-package failure": {Swift: "swift", Cmd: failingSwift{}},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			root := writeShimFixture(t)
			var logs []string
			opts.Root = root
			opts.ProjectPath = "App.xcodeproj"
			opts.Logf = func(format string, v ...interface{}) { logs = append(logs, fmt.Sprintf(format, v...)) }
			g, _, err := Build(opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(logs) != 1 || !strings.HasPrefix(logs[0], "Modules/Package.swift: ") {
				t.Errorf("logs %q, want one line for Modules/Package.swift", logs)
			}
			// Literal declarations still resolve without SwiftPM.
			got := keys(g.Dependents(g.Owners("Modules/Sources/Feature/Feature.swift")))
			if !slices.Contains(got, "spm:Modules/FeatureTests") {
				t.Errorf("Feature dependents %v, want spm:Modules/FeatureTests", got)
			}
		})
	}
}

func assertShimGraph(t *testing.T, g *graph.Graph) {
	t.Helper()
	var names []string
	for _, n := range g.Nodes() {
		if n.Ecosystem == "spm" {
			names = append(names, n.Name)
		}
	}
	sort.Strings(names)
	// The `.target(name:)` item in the dependency variable is not a declaration.
	want := []string{"Charts", "Feature", "FeatureTests", "Mocks", "Shared", "Unlinked", "XcodeTarget_App", "XcodeTarget_AppTests", "XcodeTarget_Widget"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("targets %v, want %v", names, want)
	}

	cases := []struct {
		file string
		want []string
	}{
		// The app's shim depends on Feature; AppTests is hosted by the app.
		{"Modules/Sources/Feature/Feature.swift", []string{"xcode:App/App", "xcode:App/AppTests"}},
		// Only the test shim links Mocks.
		{"Modules/Sources/Mocks/Mock.swift", []string{"xcode:App/AppTests"}},
		// The widget shim's dependencies come from a variable plus a literal array.
		{"Modules/Sources/Shared/Shared.swift", []string{"xcode:App/Widget", "xcode:App/WidgetTests"}},
		{"Modules/Sources/Charts/Chart.swift", []string{"xcode:App/Widget", "xcode:App/WidgetTests"}},
		{"Modules/Sources/Unlinked/Unlinked.swift", nil},
	}
	for _, c := range cases {
		if got := xcodeDependents(g, c.file); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: dependents %v, want %v", c.file, got, c.want)
		}
	}
}
