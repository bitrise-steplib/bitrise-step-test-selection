package step

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitrise-steplib/bitrise-step-test-selection/internal/selection"
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

// flutterRepo is the smallest repository the auto-detection can analyse without a
// toolchain: an app package plus two library packages.
func flutterRepo(t *testing.T) string {
	root := t.TempDir()
	write(t, root, "pubspec.yaml", "name: app\ndependencies:\n  core:\n    path: packages/core\n  ui:\n    path: packages/ui\n")
	write(t, root, "test/app_test.dart", "")
	write(t, root, "packages/core/pubspec.yaml", "name: core\n")
	write(t, root, "packages/core/test/core_test.dart", "")
	write(t, root, "packages/ui/pubspec.yaml", "name: ui\n")
	write(t, root, "packages/ui/test/ui_test.dart", "")
	return root
}

func base(root string) Config {
	return Config{ProjectType: "auto", ChangedFilesComplete: true, OnUnknownFile: "full", Mode: "enforce", OnError: "full", RepoDir: root}
}

func selected(res selection.Result) map[string]bool {
	out := map[string]bool{}
	for _, t := range res.Tests {
		out[t.ID] = t.Selected
	}
	return out
}

func TestSelectsDependents(t *testing.T) {
	root := flutterRepo(t)
	cfg := base(root)
	cfg.ChangedFiles = "packages/ui/lib/button.dart\n"
	res, err := Run(cfg, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != selection.ModeSelected {
		t.Fatalf("mode %s reason %s errors %v", res.Mode, res.Reason, res.Errors)
	}
	got := selected(res)
	if !got["flutter:ui:test"] || !got["flutter:app:test"] || got["flutter:core:test"] {
		t.Errorf("selection %v", got)
	}
	if res.Stats.Selected != 2 || res.Stats.Skipped != 1 {
		t.Errorf("stats %+v", res.Stats)
	}
	if !strings.Contains(res.Tests[0].Reason, "via ui") && !strings.Contains(res.Tests[2].Reason, "via ui") {
		t.Errorf("reason should name the owner: %+v", res.Tests)
	}
}

func TestFullRunOnGlobalFile(t *testing.T) {
	root := flutterRepo(t)
	cfg := base(root)
	cfg.ChangedFiles = "packages/ui/lib/button.dart\npubspec.lock\n"
	res, err := Run(cfg, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != selection.ModeFull || !strings.Contains(res.Reason, "pubspec.lock") {
		t.Fatalf("mode %s reason %s", res.Mode, res.Reason)
	}
	for _, tc := range res.Tests {
		if !tc.Selected {
			t.Errorf("%s should be selected in a full run", tc.ID)
		}
	}
}

func TestUnknownFilePolicy(t *testing.T) {
	root := flutterRepo(t)
	cfg := base(root)
	cfg.ChangedFiles = "scripts/deploy.sh\n"
	res, err := Run(cfg, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != selection.ModeFull {
		t.Errorf("unknown file should force a full run, got %s", res.Mode)
	}
	cfg.OnUnknownFile = "ignore"
	res, err = Run(cfg, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != selection.ModeSelected || res.Stats.Selected != 0 {
		t.Errorf("ignored unknown file should select nothing, got %s %+v", res.Mode, res.Stats)
	}
}

func TestIgnoredAndMustRun(t *testing.T) {
	root := flutterRepo(t)
	cfg := base(root)
	cfg.ChangedFiles = "README.md\n"
	cfg.MustRun = "core tests"
	res, err := Run(cfg, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	got := selected(res)
	if res.Mode != selection.ModeSelected || !got["flutter:core:test"] || got["flutter:app:test"] {
		t.Errorf("mode %s selection %v", res.Mode, got)
	}
	if res.Stats.MustRun != 1 {
		t.Errorf("stats %+v", res.Stats)
	}
}

func TestBackendConfigOverrides(t *testing.T) {
	root := flutterRepo(t)
	cfg := base(root)
	cfg.ChangedFiles = "packages/ui/lib/button.dart\n"
	cfg.BackendConfig = `{"mode":"shadow","force_full_run":true,"force_full_run_reason":"main branch"}`
	res, err := Run(cfg, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Shadow || res.Mode != selection.ModeFull || res.Reason != "main branch" {
		t.Errorf("got shadow=%v mode=%s reason=%q", res.Shadow, res.Mode, res.Reason)
	}
}

func TestIncompleteListWithoutGitFallsBackToFull(t *testing.T) {
	root := flutterRepo(t)
	cfg := base(root)
	cfg.ChangedFiles = "packages/ui/lib/button.dart\n"
	cfg.ChangedFilesComplete = false
	res, err := Run(cfg, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != selection.ModeFull || !strings.Contains(res.Reason, "incomplete") {
		t.Errorf("mode %s reason %s", res.Mode, res.Reason)
	}
}

func TestNoProjectDetected(t *testing.T) {
	root := t.TempDir()
	cfg := base(root)
	cfg.ChangedFiles = "a.txt"
	res, err := Run(cfg, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != selection.ModeFull || len(res.Errors) == 0 {
		t.Errorf("expected a full run with an error, got %s %v", res.Mode, res.Errors)
	}
	cfg.OnError = "fail"
	if _, err := Run(cfg, Deps{}); err == nil {
		t.Error("on_error=fail should return the error")
	}
}

func TestManualRules(t *testing.T) {
	root := t.TempDir()
	cfg := base(root)
	cfg.ManualRules = "Scripts/** => ScriptTests\nFastlane/** => LaneTests"
	cfg.ChangedFiles = "Scripts/x.rb"
	res, err := Run(cfg, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	got := selected(res)
	if res.Mode != selection.ModeSelected || !got["manual:ScriptTests"] || got["manual:LaneTests"] {
		t.Errorf("mode %s selection %v", res.Mode, got)
	}
	dir := t.TempDir()
	files, err := selection.Write(dir, res)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(files.XcodeOnlyTesting)
	if strings.TrimSpace(string(data)) != "ScriptTests" {
		t.Errorf("only-testing file: %q", data)
	}
}
