package gradle

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

const dump = `{
  "rootDir": "/repo/android",
  "projects": [
    {"path": ":", "dir": "", "buildFile": "build.gradle.kts", "android": false, "mainDirs": [], "testDirs": [], "deps": []},
    {"path": ":app", "dir": "app", "buildFile": "app/build.gradle.kts", "android": true,
     "mainDirs": ["app/src/main/java", "app/src/main/kotlin", "app/src/main/res", "app/src/main/AndroidManifest.xml"],
     "testDirs": ["app/src/test/java", "app/src/test/kotlin", "app/src/androidTest/java"],
     "deps": [
       {"path": ":core", "configuration": "implementation"},
       {"path": ":feature:home", "configuration": "implementation"},
       {"path": ":core:testing", "configuration": "testImplementation"},
       {"path": ":core:testing", "configuration": "androidTestImplementation"}
     ]},
    {"path": ":core", "dir": "core", "buildFile": "core/build.gradle.kts", "android": false,
     "mainDirs": ["core/src/main/kotlin", "core/src/testFixtures/kotlin"], "testDirs": ["core/src/test/kotlin"], "deps": []},
    {"path": ":core:testing", "dir": "core/testing", "buildFile": "core/testing/build.gradle.kts", "android": false,
     "mainDirs": ["core/testing/src/main/kotlin"], "testDirs": [], "deps": [{"path": ":core", "configuration": "api"}]},
    {"path": ":feature:home", "dir": "feature/home", "buildFile": "feature/home/build.gradle.kts", "android": true,
     "mainDirs": ["feature/home/src/main/kotlin"], "testDirs": ["feature/home/src/test/kotlin"],
     "deps": [{"path": ":core", "configuration": "implementation"}]}
  ]
}`

func fixture(t *testing.T) (root, projectDir string) {
	t.Helper()
	root = t.TempDir()
	projectDir = filepath.Join(root, "android")
	for _, d := range []string{"app/src/test/kotlin", "app/src/androidTest/java", "core/src/test/kotlin", "feature/home/src/test/kotlin"} {
		if err := os.MkdirAll(filepath.Join(projectDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, projectDir
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestBuildFromDump(t *testing.T) {
	root, projectDir := fixture(t)
	g, err := BuildFromDump(root, projectDir, []byte(dump))
	if err != nil {
		t.Fatal(err)
	}

	var tests []string
	for _, n := range g.TestNodes() {
		tests = append(tests, n.ID)
	}
	want := []string{"gradle::app:androidTest", "gradle::app:unitTest", "gradle::core:unitTest", "gradle::feature:home:unitTest"}
	if !reflect.DeepEqual(tests, want) {
		t.Fatalf("test nodes %v", tests)
	}

	// A core change reaches every module and test that depends on it.
	got := keys(g.Dependents(g.Owners("android/core/src/main/kotlin/Core.kt")))
	want = []string{"gradle::app", "gradle::app:androidTest", "gradle::app:unitTest", "gradle::core", "gradle::core:testing", "gradle::core:unitTest", "gradle::feature:home", "gradle::feature:home:unitTest"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dependents of core: %v", got)
	}

	// Test fixtures are part of the module, so dependents see them.
	if owners := g.Owners("android/core/src/testFixtures/kotlin/Fake.kt"); !reflect.DeepEqual(owners, []string{"gradle::core"}) {
		t.Errorf("testFixtures owner %v", owners)
	}

	// A shared test helper module only affects the tests that declare it.
	got = keys(g.Dependents(g.Owners("android/core/testing/src/main/kotlin/TestRule.kt")))
	want = []string{"gradle::app:androidTest", "gradle::app:unitTest", "gradle::core:testing"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dependents of core:testing: %v", got)
	}

	// A module's own test source only affects that test node.
	got = keys(g.Dependents(g.Owners("android/feature/home/src/test/kotlin/HomeTest.kt")))
	if !reflect.DeepEqual(got, []string{"gradle::feature:home:unitTest"}) {
		t.Errorf("dependents of a test file: %v", got)
	}

	// A module build file belongs to the module, not to the root.
	if owners := g.Owners("android/app/build.gradle.kts"); !reflect.DeepEqual(owners, []string{"gradle::app"}) {
		t.Errorf("build file owner %v", owners)
	}
	// Root-level files have no owner (the rules layer turns them into a full run).
	if owners := g.Owners("android/build.gradle.kts"); len(owners) != 0 {
		t.Errorf("root build file owner %v", owners)
	}
	if owners := g.Owners("android/app/proguard-rules.pro"); !reflect.DeepEqual(owners, []string{"gradle::app"}) {
		t.Errorf("module-level file owner %v", owners)
	}

	if n := g.Node("gradle::core:unitTest"); n.Meta["task"] != ":core:test" {
		t.Errorf("jvm task %q", n.Meta["task"])
	}
	if n := g.Node("gradle::app:unitTest"); n.Meta["task"] != "" {
		t.Errorf("android task should be left to the step, got %q", n.Meta["task"])
	}
}
