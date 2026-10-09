package xcode

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/bitrise-steplib/bitrise-step-test-selection/internal/graph"
)

const pbxproj = `// !$*UTF8*$!
{
	archiveVersion = 1;
	classes = {
	};
	objectVersion = 77;
	objects = {

/* Begin PBXBuildFile section */
		BF1 /* Main.swift in Sources */ = {isa = PBXBuildFile; fileRef = F1 /* Main.swift */; };
		BF2 = {isa = PBXBuildFile; fileRef = F2; };
		BF3 = {isa = PBXBuildFile; fileRef = F3; };
		BFLINK = {isa = PBXBuildFile; fileRef = PCORE; };
		BFPKG = {isa = PBXBuildFile; productRef = PKGDEP; };
/* End PBXBuildFile section */

		F1 = {isa = PBXFileReference; lastKnownFileType = sourcecode.swift; path = Main.swift; sourceTree = "<group>"; };
		F2 = {isa = PBXFileReference; path = Core.swift; sourceTree = "<group>"; };
		F3 = {isa = PBXFileReference; path = AppTests.swift; sourceTree = "<group>"; };
		FROOT = {isa = PBXFileReference; path = Shared/Config.swift; sourceTree = SOURCE_ROOT; };
		FOLDERPKG = {isa = PBXFileReference; lastKnownFileType = folder; name = Legacy; path = Packages/Legacy; sourceTree = "<group>"; };
		FOLDERPLAIN = {isa = PBXFileReference; lastKnownFileType = folder; path = Assets; sourceTree = "<group>"; };
		PAPP = {isa = PBXFileReference; explicitFileType = wrapper.application; path = App.app; sourceTree = BUILT_PRODUCTS_DIR; };
		PCORE = {isa = PBXFileReference; explicitFileType = wrapper.framework; path = Core.framework; sourceTree = BUILT_PRODUCTS_DIR; };
		PTESTS = {isa = PBXFileReference; explicitFileType = wrapper.cfbundle; path = AppTests.xctest; sourceTree = BUILT_PRODUCTS_DIR; };
		PUI = {isa = PBXFileReference; explicitFileType = wrapper.cfbundle; path = AppUITests.xctest; sourceTree = BUILT_PRODUCTS_DIR; };

		MAIN = {isa = PBXGroup; children = (APPGRP, COREGRP, TESTSGRP, SYNCGRP, FROOT, FOLDERPKG, FOLDERPLAIN, PRODUCTS); sourceTree = "<group>"; };
		APPGRP = {isa = PBXGroup; children = (F1); path = App; sourceTree = "<group>"; };
		COREGRP = {isa = PBXGroup; children = (F2); path = Core; sourceTree = "<group>"; };
		TESTSGRP = {isa = PBXGroup; children = (F3); path = AppTests; sourceTree = "<group>"; };
		SYNCGRP = {isa = PBXFileSystemSynchronizedRootGroup; path = Features; sourceTree = "<group>"; };
		PRODUCTS = {isa = PBXGroup; children = (PAPP, PCORE, PTESTS, PUI); name = Products; sourceTree = "<group>"; };

		SRCAPP = {isa = PBXSourcesBuildPhase; files = (BF1); };
		FWAPP = {isa = PBXFrameworksBuildPhase; files = (BFLINK, BFPKG); };
		SRCCORE = {isa = PBXSourcesBuildPhase; files = (BF2); };
		SRCTESTS = {isa = PBXSourcesBuildPhase; files = (BF3); };

		DEPCORE = {isa = PBXTargetDependency; target = CORE; };
		DEPAPP = {isa = PBXTargetDependency; targetProxy = PROXYAPP; };
		PROXYAPP = {isa = PBXContainerItemProxy; containerPortal = PROJ; proxyType = 1; remoteGlobalIDString = APP; };

		APP = {isa = PBXNativeTarget; name = App; productType = "com.apple.product-type.application"; productReference = PAPP; buildPhases = (SRCAPP, FWAPP); dependencies = (DEPCORE); fileSystemSynchronizedGroups = (SYNCGRP); packageProductDependencies = (PKGDEP); buildConfigurationList = CLAPP; };
		CORE = {isa = PBXNativeTarget; name = Core; productType = "com.apple.product-type.framework"; productReference = PCORE; buildPhases = (SRCCORE); buildConfigurationList = CLCORE; };
		APPTESTS = {isa = PBXNativeTarget; name = AppTests; productType = "com.apple.product-type.bundle.unit-test"; productReference = PTESTS; buildPhases = (SRCTESTS); dependencies = (DEPAPP); buildConfigurationList = CLTESTS; };
		UITESTS = {isa = PBXNativeTarget; name = AppUITests; productType = "com.apple.product-type.bundle.ui-testing"; productReference = PUI; buildPhases = (); buildConfigurationList = CLUI; };

		CLAPP = {isa = XCConfigurationList; buildConfigurations = (CFGAPP); };
		CLCORE = {isa = XCConfigurationList; buildConfigurations = (CFGCORE); };
		CLTESTS = {isa = XCConfigurationList; buildConfigurations = (CFGTESTS); };
		CLUI = {isa = XCConfigurationList; buildConfigurations = (CFGUI); };
		CFGAPP = {isa = XCBuildConfiguration; name = Debug; buildSettings = { PRODUCT_NAME = App; INFOPLIST_FILE = App/Info.plist; CODE_SIGN_ENTITLEMENTS = "$(SRCROOT)/App/App.entitlements"; }; };
		CFGCORE = {isa = XCBuildConfiguration; name = Debug; buildSettings = { }; };
		CFGTESTS = {isa = XCBuildConfiguration; name = Debug; buildSettings = { TEST_HOST = "$(BUILT_PRODUCTS_DIR)/App.app/App"; }; };
		CFGUI = {isa = XCBuildConfiguration; name = Debug; buildSettings = { TEST_TARGET_NAME = App; }; };

		LOCALPKG = {isa = XCLocalSwiftPackageReference; relativePath = Packages/Networking; };
		PKGDEP = {isa = XCSwiftPackageProductDependency; productName = Networking; };

		PROJ = {isa = PBXProject; mainGroup = MAIN; projectDirPath = ""; targets = (APP, CORE, APPTESTS, UITESTS); packageReferences = (LOCALPKG); };
	};
	rootObject = PROJ;
}
`

const networkingManifest = `// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "Networking",
    platforms: [.iOS(.v15)],
    products: [
        .library(name: "Networking", targets: ["Networking"]),
    ],
    dependencies: [
        .package(path: "../Logging"), // a URL in a comment: https://example.com
        .package(url: "https://github.com/example/dep.git", from: "1.0.0"), /* block */
    ],
    targets: [
        .target(
            name: "Networking",
            dependencies: [
                .product(name: "Logging", package: "Logging"),
                "NetworkingCore",
            ]
        ),
        .target(
            name: "NetworkingCore",
            path: "Sources/Core",
            plugins: [.plugin(name: "SwiftLintPlugin", package: "SwiftLint")]
        ),
        .testTarget(name: "NetworkingTests", dependencies: [.target(name: "Networking")]),
        .binaryTarget(name: "Analytics", url: "https://example.com/Analytics.xcframework.zip", checksum: "abc"),
    ]
)
`

const loggingManifest = `// swift-tools-version:5.9
import PackageDescription
let package = Package(
    name: "Logging",
    products: [.library(name: "Logging", targets: ["Logging"])],
    targets: [
        .target(name: "Logging"),
        .testTarget(name: "LoggingTests", dependencies: [.target(name: "Logging")]),
    ]
)
`

const scheme = `<?xml version="1.0" encoding="UTF-8"?>
<Scheme LastUpgradeVersion = "1600" version = "1.7">
   <TestAction buildConfiguration = "Debug">
      <Testables>
         <TestableReference skipped = "NO">
            <BuildableReference BuildableIdentifier = "primary" BlueprintIdentifier = "APPTESTS" BuildableName = "AppTests.xctest" BlueprintName = "AppTests" ReferencedContainer = "container:App.xcodeproj"></BuildableReference>
         </TestableReference>
         <TestableReference skipped = "YES">
            <BuildableReference BuildableIdentifier = "primary" BlueprintIdentifier = "UITESTS" BuildableName = "AppUITests.xctest" BlueprintName = "AppUITests" ReferencedContainer = "container:App.xcodeproj"></BuildableReference>
         </TestableReference>
      </Testables>
   </TestAction>
</Scheme>
`

const planScheme = `<?xml version="1.0" encoding="UTF-8"?>
<Scheme version = "1.7">
   <TestAction buildConfiguration = "Debug">
      <TestPlans>
         <TestPlanReference reference = "container:Plans/Unit.xctestplan" default = "YES"></TestPlanReference>
      </TestPlans>
   </TestAction>
</Scheme>
`

const testPlan = `{
  "configurations" : [ { "id" : "1", "name" : "Configuration 1", "options" : {} } ],
  "defaultOptions" : {},
  "testTargets" : [
    { "target" : { "containerPath" : "container:App.xcodeproj", "identifier" : "APPTESTS", "name" : "AppTests" } },
    { "target" : { "containerPath" : "container:Packages/Networking", "identifier" : "NetworkingTests", "name" : "NetworkingTests" } }
  ],
  "version" : 1
}
`

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "App.xcodeproj", "project.pbxproj"), pbxproj)
	writeFile(t, filepath.Join(root, "App.xcodeproj", "xcshareddata", "xcschemes", "App.xcscheme"), scheme)
	writeFile(t, filepath.Join(root, "App.xcodeproj", "xcshareddata", "xcschemes", "Plan.xcscheme"), planScheme)
	writeFile(t, filepath.Join(root, "Plans", "Unit.xctestplan"), testPlan)
	writeFile(t, filepath.Join(root, "Packages", "Networking", "Package.swift"), networkingManifest)
	writeFile(t, filepath.Join(root, "Packages", "Logging", "Package.swift"), loggingManifest)
	writeFile(t, filepath.Join(root, "Packages", "Legacy", "Package.swift"), `// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "Legacy", targets: [.target(name: "Legacy"), .testTarget(name: "LegacyTests", dependencies: ["Legacy"])])
`)
	if err := os.MkdirAll(filepath.Join(root, "Assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "App.xcworkspace", "contents.xcworkspacedata"), `<?xml version="1.0" encoding="UTF-8"?>
<Workspace version = "1.0">
   <FileRef location = "group:App.xcodeproj"></FileRef>
   <Group location = "container:Packages" name = "Packages">
      <FileRef location = "group:Logging"></FileRef>
   </Group>
</Workspace>
`)
	return root
}

func TestParseProject(t *testing.T) {
	root := fixture(t)
	p, err := ParseProject(filepath.Join(root, "App.xcodeproj"))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*Target{}
	for _, tg := range p.Targets {
		byName[tg.Name] = tg
	}
	app := byName["App"]
	if app == nil || app.IsTest {
		t.Fatalf("App target missing or wrongly a test: %+v", app)
	}
	wantFiles := []string{filepath.Join(root, "App", "Main.swift"), filepath.Join(root, "App", "Info.plist"), filepath.Join(root, "App", "App.entitlements")}
	if !reflect.DeepEqual(app.Files, wantFiles) {
		t.Errorf("App files: %v", app.Files)
	}
	if !reflect.DeepEqual(app.Dirs, []string{filepath.Join(root, "Features")}) {
		t.Errorf("App synced dirs: %v", app.Dirs)
	}
	if !reflect.DeepEqual(app.DependencyIDs, []string{"CORE"}) || !reflect.DeepEqual(app.LinkedProducts, []string{"Core.framework"}) {
		t.Errorf("App deps: %v linked: %v", app.DependencyIDs, app.LinkedProducts)
	}
	if !reflect.DeepEqual(app.PackageProducts, []string{"Networking", "Networking"}) {
		t.Errorf("App package products: %v", app.PackageProducts)
	}
	tests := byName["AppTests"]
	if !tests.IsTest || tests.TestHostProduct != "App" || !reflect.DeepEqual(tests.DependencyIDs, []string{"APP"}) {
		t.Errorf("AppTests: %+v", tests)
	}
	if ui := byName["AppUITests"]; !ui.IsTest || ui.TestTargetName != "App" {
		t.Errorf("AppUITests: %+v", ui)
	}
	// XCLocalSwiftPackageReference plus the legacy folder reference; the plain Assets
	// folder holds no manifest and is not a package.
	sort.Strings(p.LocalPackages)
	if !reflect.DeepEqual(p.LocalPackages, []string{filepath.Join(root, "Packages", "Legacy"), filepath.Join(root, "Packages", "Networking")}) {
		t.Errorf("local packages: %v", p.LocalPackages)
	}
}

func TestParsePackage(t *testing.T) {
	root := fixture(t)
	pkg, err := ParsePackage(filepath.Join(root, "Packages", "Networking"))
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "Networking" {
		t.Errorf("name %q", pkg.Name)
	}
	if !reflect.DeepEqual(pkg.LocalDependencies, []string{filepath.Join(root, "Packages", "Logging")}) {
		t.Errorf("local deps %v", pkg.LocalDependencies)
	}
	if !reflect.DeepEqual(pkg.Products["Networking"], []string{"Networking"}) {
		t.Errorf("products %v", pkg.Products)
	}
	// Three declarations plus the binary target; the plugin usage and the
	// `.target(name:)` dependency item must not count as declarations.
	if len(pkg.Targets) != 4 {
		t.Fatalf("targets %+v", pkg.Targets)
	}
	if bin := pkg.Targets[3]; bin.Name != "Analytics" || bin.Path != "" {
		t.Errorf("binary target: %+v", bin)
	}
	net := pkg.Targets[0]
	if net.Name != "Networking" || net.Path != filepath.Join(pkg.Dir, "Sources", "Networking") {
		t.Errorf("target 0: %+v", net)
	}
	if !reflect.DeepEqual(net.ProductDeps, []string{"Logging"}) || !reflect.DeepEqual(net.TargetDeps, []string{"NetworkingCore"}) {
		t.Errorf("deps: products %v targets %v", net.ProductDeps, net.TargetDeps)
	}
	if core := pkg.Targets[1]; core.Path != filepath.Join(pkg.Dir, "Sources", "Core") {
		t.Errorf("custom path: %s", core.Path)
	}
	if tt := pkg.Targets[2]; !tt.IsTest || tt.Path != filepath.Join(pkg.Dir, "Tests", "NetworkingTests") || !reflect.DeepEqual(tt.TargetDeps, []string{"Networking"}) {
		t.Errorf("test target: %+v", tt)
	}
}

func TestTestables(t *testing.T) {
	root := fixture(t)
	got, err := Testables("App", []string{filepath.Join(root, "App.xcodeproj")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"AppTests"}) {
		t.Errorf("testables %v", got)
	}
	got, err = Testables("Plan", []string{filepath.Join(root, "App.xcodeproj")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"AppTests", "NetworkingTests"}) {
		t.Errorf("plan testables %v", got)
	}
	if got, _ := Testables("Missing", []string{filepath.Join(root, "App.xcodeproj")}); got != nil {
		t.Errorf("missing scheme should give nil, got %v", got)
	}
}

func TestParseWorkspace(t *testing.T) {
	root := fixture(t)
	ws, err := ParseWorkspace(filepath.Join(root, "App.xcworkspace"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ws.Projects, []string{filepath.Join(root, "App.xcodeproj")}) {
		t.Errorf("projects %v", ws.Projects)
	}
	if !reflect.DeepEqual(ws.PackageDirs, []string{filepath.Join(root, "Packages", "Logging")}) {
		t.Errorf("packages %v", ws.PackageDirs)
	}
}

func TestBuildGraph(t *testing.T) {
	root := fixture(t)
	g, candidates, err := Build(Options{Root: root, ProjectPath: "App.xcworkspace", Scheme: "App"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(candidates, []string{"AppTests"}) {
		t.Errorf("candidates %v", candidates)
	}
	var ids []string
	for _, n := range g.Nodes() {
		ids = append(ids, n.ID)
	}
	want := []string{
		"spm:Legacy/Legacy", "spm:Legacy/LegacyTests",
		"spm:Logging/Logging", "spm:Logging/LoggingTests",
		"spm:Networking/Analytics", "spm:Networking/Networking", "spm:Networking/NetworkingCore", "spm:Networking/NetworkingTests",
		"xcode:App/App", "xcode:App/AppTests", "xcode:App/AppUITests", "xcode:App/Core",
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("nodes %v", ids)
	}

	// A change in the Logging package reaches the app and its tests through
	// Networking → App, and the SPM test targets on the way.
	got := keys(g.Dependents(g.Owners("Packages/Logging/Sources/Logging/Log.swift")))
	want = []string{"spm:Logging/Logging", "spm:Logging/LoggingTests", "spm:Networking/Networking", "spm:Networking/NetworkingTests", "xcode:App/App", "xcode:App/AppTests", "xcode:App/AppUITests"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dependents of Logging: %v", got)
	}

	// The synced folder belongs to the app.
	if owners := g.Owners("Features/Home/HomeView.swift"); !reflect.DeepEqual(owners, []string{"xcode:App/App"}) {
		t.Errorf("synced folder owner %v", owners)
	}
	// A SOURCE_ROOT-relative file resolves against the project directory.
	if owners := g.Owners("Shared/Config.swift"); len(owners) != 0 {
		// FROOT is in the main group but in no build phase, so no target owns it.
		t.Errorf("unreferenced file should have no owner, got %v", owners)
	}
	// Core changes do not touch the package tests.
	got = keys(g.Dependents(g.Owners("Core/Core.swift")))
	want = []string{"xcode:App/App", "xcode:App/AppTests", "xcode:App/AppUITests", "xcode:App/Core"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dependents of Core: %v", got)
	}
	// The project file belongs to every target of its project, and to nothing else.
	got = g.Owners("App.xcodeproj/project.pbxproj")
	want = []string{"xcode:App/App", "xcode:App/AppTests", "xcode:App/AppUITests", "xcode:App/Core"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pbxproj owners: %v", got)
	}
	if owners := g.Owners("App/Info.plist"); !reflect.DeepEqual(owners, []string{"xcode:App/App"}) {
		t.Errorf("Info.plist owner %v", owners)
	}
	if n := g.Node("xcode:App/AppTests"); n.Kind != graph.KindTest {
		t.Error("AppTests should be a test node")
	}
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
