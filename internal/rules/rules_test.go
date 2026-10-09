package rules

import "testing"

func TestDefaultsFullRun(t *testing.T) {
	r := Merge(nil, nil, nil)
	for _, f := range []string{"bitrise.yml", "ios/Podfile.lock", "App.xcodeproj/../Config/Debug.xcconfig", "gradle/libs.versions.toml", "buildSrc/src/main/kotlin/Conventions.kt", "settings.gradle.kts"} {
		if _, ok := r.MatchFullRun(f); !ok {
			t.Errorf("%s should force a full run", f)
		}
	}
	// A module build file is owned by the module; only the root one is global. Root
	// files of a Gradle build in a subdirectory are left to the unknown-owner rule.
	for _, f := range []string{"app/build.gradle.kts", "android/settings.gradle.kts", "feature/home/gradle.properties"} {
		if p, ok := r.MatchFullRun(f); ok {
			t.Errorf("%s should not force a full run (matched %s)", f, p)
		}
	}
	// Anchored user patterns work the same way.
	r = Merge([]string{"/Config/**"}, nil, nil)
	if _, ok := r.MatchFullRun("Config/Base.plist"); !ok {
		t.Error("anchored user pattern should match at the root")
	}
	if _, ok := r.MatchFullRun("App/Config/Base.plist"); ok {
		t.Error("anchored user pattern should not match below the root")
	}
}

func TestIgnore(t *testing.T) {
	r := Merge(nil, []string{"design/**"}, nil)
	for _, f := range []string{"README.md", "docs/guide/x.png", ".github/workflows/ci.yml", "design/logo.sketch", ".circleci/config.yml", ".swiftlint.yml", ".ci/targets/x.yaml", "Dangerfile.swift"} {
		if !r.MatchIgnore(f) {
			t.Errorf("%s should be ignored", f)
		}
	}
	if r.MatchIgnore("App/Sources/Main.swift") {
		t.Error("source should not be ignored")
	}
}

func TestMustRun(t *testing.T) {
	r := Merge(nil, nil, []string{"*SmokeTests", "gradle::app:*"})
	if _, ok := r.MatchMustRun("xcode:App/AppSmokeTests", "AppSmokeTests"); !ok {
		t.Error("name glob should match")
	}
	if _, ok := r.MatchMustRun("gradle::app:unitTest", ":app"); !ok {
		t.Error("id glob should match")
	}
	if _, ok := r.MatchMustRun("xcode:App/AppTests", "AppTests"); ok {
		t.Error("unexpected match")
	}
}

func TestSplitLines(t *testing.T) {
	got := SplitLines(" a\n\nb, c ")
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("got %v", got)
	}
}
