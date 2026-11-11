package flutter

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func write(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
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

func TestBuild(t *testing.T) {
	root := t.TempDir()
	write(t, root, "pubspec.yaml", "name: app\ndependencies:\n  flutter:\n    sdk: flutter\n  core:\n    path: packages/core\n  ui:\n    path: packages/ui\ndev_dependencies:\n  test_support:\n    path: packages/test_support\n")
	write(t, root, "lib/main.dart", "")
	write(t, root, "test/widget_test.dart", "")
	write(t, root, "integration_test/app_test.dart", "")
	write(t, root, "packages/core/pubspec.yaml", "name: core\n")
	write(t, root, "packages/core/test/core_test.dart", "")
	write(t, root, "packages/ui/pubspec.yaml", "name: ui\ndependencies:\n  core:\n    path: ../core\n")
	write(t, root, "packages/test_support/pubspec.yaml", "name: test_support\ndependencies:\n  core:\n    path: ../core\n")
	// Platform folders and build output never hold first-party packages.
	write(t, root, "ios/Flutter/pubspec.yaml", "name: bogus\n")
	write(t, root, "build/pubspec.yaml", "name: bogus2\n")

	g, err := Build(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, n := range g.Nodes() {
		ids = append(ids, n.ID)
	}
	want := []string{"flutter:app", "flutter:app:integration_test", "flutter:app:test", "flutter:core", "flutter:core:test", "flutter:test_support", "flutter:ui"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("nodes %v", ids)
	}

	got := keys(g.Dependents(g.Owners("packages/core/lib/core.dart")))
	want = []string{"flutter:app", "flutter:app:integration_test", "flutter:app:test", "flutter:core", "flutter:core:test", "flutter:test_support", "flutter:ui"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dependents of core: %v", got)
	}
	// A dev dependency only reaches the tests.
	got = keys(g.Dependents(g.Owners("packages/test_support/lib/support.dart")))
	if !reflect.DeepEqual(got, []string{"flutter:app:integration_test", "flutter:app:test", "flutter:test_support"}) {
		t.Errorf("dependents of test_support: %v", got)
	}
	if owners := g.Owners("integration_test/app_test.dart"); !reflect.DeepEqual(owners, []string{"flutter:app:integration_test"}) {
		t.Errorf("integration test owner %v", owners)
	}
	if owners := g.Owners("test/widget_test.dart"); !reflect.DeepEqual(owners, []string{"flutter:app:test"}) {
		t.Errorf("unit test owner %v", owners)
	}
	if n := g.Node("flutter:app:integration_test"); n.Meta["kind"] != "ui" || n.Meta["package_dir"] != "." {
		t.Errorf("integration test meta %v", n.Meta)
	}
	if owners := g.Owners("lib/main.dart"); !reflect.DeepEqual(owners, []string{"flutter:app"}) {
		t.Errorf("lib owner %v", owners)
	}
	// A root package must not swallow unrelated files.
	if owners := g.Owners("scripts/deploy.sh"); len(owners) != 0 {
		t.Errorf("root package should not own scripts, got %v", owners)
	}
	if n := g.Node("flutter:core:test"); n.Meta["package_dir"] != "packages/core" {
		t.Errorf("package dir %q", n.Meta["package_dir"])
	}
}

func TestBuildPubspecOverrides(t *testing.T) {
	root := t.TempDir()
	write(t, root, "pubspec.yaml", "name: app\ndependencies:\n  core: ^1.0.0\n")
	write(t, root, "pubspec_overrides.yaml", "dependency_overrides:\n  core:\n    path: packages/core\n")
	write(t, root, "lib/main.dart", "")
	write(t, root, "packages/core/pubspec.yaml", "name: core\n")

	g, err := Build(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	got := keys(g.Dependents(g.Owners("packages/core/lib/core.dart")))
	if !reflect.DeepEqual(got, []string{"flutter:app", "flutter:core"}) {
		t.Errorf("dependents of core: %v", got)
	}
	if owners := g.Owners("pubspec_overrides.yaml"); !reflect.DeepEqual(owners, []string{"flutter:app"}) {
		t.Errorf("pubspec_overrides.yaml owner %v", owners)
	}
}

func TestBuildWorkspace(t *testing.T) {
	root := t.TempDir()
	write(t, root, "pubspec.yaml", "name: repo\nworkspace:\n  - packages/a\n  - packages/b\n  - packages/c\n")
	write(t, root, "packages/a/pubspec.yaml", "name: a\nresolution: workspace\ndependencies:\n  b: ^2.0.0\ndev_dependencies:\n  c: any\n")
	write(t, root, "packages/a/test/a_test.dart", "")
	write(t, root, "packages/b/pubspec.yaml", "name: b\nresolution: workspace\n")
	write(t, root, "packages/c/pubspec.yaml", "name: c\nresolution: workspace\n")

	g, err := Build(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	got := keys(g.Dependents(g.Owners("packages/b/lib/b.dart")))
	if !reflect.DeepEqual(got, []string{"flutter:a", "flutter:a:test", "flutter:b"}) {
		t.Errorf("dependents of b: %v", got)
	}
	// A dev dependency only reaches the tests.
	got = keys(g.Dependents(g.Owners("packages/c/lib/c.dart")))
	if !reflect.DeepEqual(got, []string{"flutter:a:test", "flutter:c"}) {
		t.Errorf("dependents of c: %v", got)
	}
}

func TestBuildDuplicatePackageNames(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"packages/quick_actions_android", "packages/quick_actions_ios"} {
		write(t, root, p+"/pubspec.yaml", "name: "+filepath.Base(p)+"\n")
		write(t, root, p+"/example/pubspec.yaml", "name: quick_actions_example\ndependencies:\n  "+filepath.Base(p)+":\n    path: ../\n")
		write(t, root, p+"/example/integration_test/app_test.dart", "")
	}

	g, err := Build(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	android := "flutter:quick_actions_example@packages/quick_actions_android/example"
	ios := "flutter:quick_actions_example@packages/quick_actions_ios/example"
	for id, dir := range map[string]string{android: "packages/quick_actions_android/example", ios: "packages/quick_actions_ios/example"} {
		if n := g.Node(id + ":integration_test"); n == nil || n.Meta["package_dir"] != dir {
			t.Errorf("%s: want a unit for %s, got %+v", id, dir, n)
		}
	}
	got := keys(g.Dependents(g.Owners("packages/quick_actions_android/lib/quick_actions_android.dart")))
	want := []string{"flutter:quick_actions_android", android, android + ":integration_test"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dependents of quick_actions_android: %v, want %v", got, want)
	}
}

func TestBuildHostedLocalDependency(t *testing.T) {
	root := t.TempDir()
	write(t, root, "packages/api/pubspec.yaml", "name: api\ndependencies:\n  flutter:\n    sdk: flutter\n  api_dart: \">=0.5.20 <0.6.0\"\n  forked:\n    git:\n      url: https://example.com/forked.git\n  hosted_map:\n    hosted: https://pub.example.com\n    version: ^1.0.0\n  bare:\n")
	write(t, root, "packages/api/test/api_test.dart", "")
	write(t, root, "packages/api_dart/pubspec.yaml", "name: api_dart\n")
	write(t, root, "packages/forked/pubspec.yaml", "name: forked\n")
	write(t, root, "packages/hosted_map/pubspec.yaml", "name: hosted_map\n")
	write(t, root, "packages/bare/pubspec.yaml", "name: bare\n")
	write(t, root, "packages/flutter/pubspec.yaml", "name: flutter\n")

	g, err := Build(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range []string{"api_dart", "hosted_map", "bare"} {
		got := keys(g.Dependents(g.Owners("packages/" + dep + "/lib/x.dart")))
		want := []string{"flutter:api", "flutter:api:test", "flutter:" + dep}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("dependents of %s: %v", dep, got)
		}
	}
	// git and sdk dependencies never resolve to the working tree.
	for _, dep := range []string{"forked", "flutter"} {
		got := keys(g.Dependents(g.Owners("packages/" + dep + "/lib/x.dart")))
		if !reflect.DeepEqual(got, []string{"flutter:" + dep}) {
			t.Errorf("dependents of %s: %v", dep, got)
		}
	}
}
