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
	want := []string{"flutter:app", "flutter:app:test", "flutter:core", "flutter:core:test", "flutter:test_support", "flutter:ui"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("nodes %v", ids)
	}

	got := keys(g.Dependents(g.Owners("packages/core/lib/core.dart")))
	want = []string{"flutter:app", "flutter:app:test", "flutter:core", "flutter:core:test", "flutter:test_support", "flutter:ui"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dependents of core: %v", got)
	}
	// A dev dependency only reaches the tests.
	got = keys(g.Dependents(g.Owners("packages/test_support/lib/support.dart")))
	if !reflect.DeepEqual(got, []string{"flutter:app:test", "flutter:test_support"}) {
		t.Errorf("dependents of test_support: %v", got)
	}
	if owners := g.Owners("integration_test/app_test.dart"); !reflect.DeepEqual(owners, []string{"flutter:app:test"}) {
		t.Errorf("integration test owner %v", owners)
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
