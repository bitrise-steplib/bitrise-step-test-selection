package selector

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

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

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// flutterRepo commits app ← ui, app ← core on main and changes ui on a branch.
func flutterRepo(t *testing.T) string {
	root := t.TempDir()
	write(t, root, "pubspec.yaml", "name: app\ndependencies:\n  core:\n    path: packages/core\n  ui:\n    path: packages/ui\n")
	write(t, root, "lib/main.dart", "")
	write(t, root, "test/app_test.dart", "")
	write(t, root, "packages/core/pubspec.yaml", "name: core\n")
	write(t, root, "packages/core/lib/core.dart", "")
	write(t, root, "packages/core/test/core_test.dart", "")
	write(t, root, "packages/ui/pubspec.yaml", "name: ui\n")
	write(t, root, "packages/ui/lib/button.dart", "")
	write(t, root, "packages/ui/test/ui_test.dart", "")
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init")
	git(t, root, "checkout", "-q", "-b", "feature")
	write(t, root, "packages/ui/lib/button.dart", "// changed\n")
	git(t, root, "commit", "-q", "-am", "change ui")
	return root
}

const shadowJSON = `{"mode":"shadow","base_ref":"main"}`

func TestShadowConfig(t *testing.T) {
	for raw, want := range map[string]bool{
		"":                   false,
		"not json":           false,
		`{"mode":"enforce"}`: false,
		`{"mode":"shadow"}`:  true,
		`{"mode":"shadow","sample_rate":0.1,"timeout_seconds":30}`: true,
	} {
		if _, got := ShadowConfig(env(map[string]string{ConfigEnvKey: raw})); got != want {
			t.Errorf("%q: got %v, want %v", raw, got, want)
		}
	}
}

func TestSampled(t *testing.T) {
	rate := func(v float64) *float64 { return &v }
	if !Sampled(nil, "a") || !Sampled(rate(1), "a") || Sampled(rate(0), "a") {
		t.Fatal("bounds")
	}
	if Sampled(rate(0.3), "slug-1") != Sampled(rate(0.3), "slug-1") {
		t.Fatal("not deterministic")
	}
	in := 0
	for i := 0; i < 2000; i++ {
		if Sampled(rate(0.25), fmt.Sprintf("build-%d", i)) {
			in++
		}
	}
	if in < 400 || in > 600 {
		t.Errorf("sampled %d of 2000 at 0.25", in)
	}
}

func TestRunShadowDisabled(t *testing.T) {
	if _, ok := RunShadow(ShadowOptions{Getenv: env(nil)}); ok {
		t.Fatal("ran without configuration")
	}
	zero := `{"mode":"shadow","sample_rate":0}`
	if _, ok := RunShadow(ShadowOptions{Getenv: env(map[string]string{ConfigEnvKey: zero})}); ok {
		t.Fatal("ran outside the sample")
	}
}

func TestRunShadowNoBase(t *testing.T) {
	rep, ok := RunShadow(ShadowOptions{Step: "flutter-test", Getenv: env(map[string]string{ConfigEnvKey: `{"mode":"shadow"}`})})
	if !ok || rep.Decision != "full" || rep.ReasonKind != ReasonNoBase {
		t.Fatalf("%+v", rep)
	}
}

func TestRunShadowSelects(t *testing.T) {
	root := flutterRepo(t)
	var logged []string
	rep, ok := RunShadow(ShadowOptions{
		Step:    "flutter-test",
		Config:  Config{ProjectType: "flutter"},
		WorkDir: filepath.Join(root, "packages", "ui"),
		Getenv:  env(map[string]string{ConfigEnvKey: shadowJSON}),
		Logf:    func(f string, v ...interface{}) { logged = append(logged, fmt.Sprintf(f, v...)) },
	})
	if !ok {
		t.Fatal("not run")
	}
	if rep.Decision != "selected" || rep.ReasonKind != "graph" || rep.ChangedFiles != 1 || rep.Base == "" {
		t.Fatalf("%+v", rep)
	}
	if !reflect.DeepEqual(rep.Selected, []string{"app tests", "ui tests"}) || !reflect.DeepEqual(rep.Skipped, []string{"core tests"}) {
		t.Errorf("selected %v skipped %v", rep.Selected, rep.Skipped)
	}
	if len(logged) != 1 {
		t.Errorf("summary lines: %v", logged)
	}
}

func TestRunShadowKeep(t *testing.T) {
	root := flutterRepo(t)
	rep, _ := RunShadow(ShadowOptions{
		Config: Config{ProjectType: "flutter", RepoDir: root},
		Getenv: env(map[string]string{ConfigEnvKey: shadowJSON}),
		Keep:   func(t Test) bool { return t.Ident.PackageDir == "packages/core" },
	})
	if rep.Candidates != 1 || len(rep.Selected) != 0 || !reflect.DeepEqual(rep.Skipped, []string{"core tests"}) {
		t.Fatalf("%+v", rep)
	}
}

func TestRunShadowBaseFromEnv(t *testing.T) {
	root := flutterRepo(t)
	rep, _ := RunShadow(ShadowOptions{
		Config: Config{ProjectType: "flutter", RepoDir: root},
		Getenv: env(map[string]string{ConfigEnvKey: `{"mode":"shadow"}`, "BITRISEIO_GIT_BRANCH_DEST": "main"}),
	})
	if rep.Decision != "selected" || len(rep.Selected) != 2 {
		t.Fatalf("%+v", rep)
	}
}

func TestRunShadowForcedFullRun(t *testing.T) {
	root := flutterRepo(t)
	rep, _ := RunShadow(ShadowOptions{
		Config: Config{ProjectType: "flutter", RepoDir: root},
		Getenv: env(map[string]string{ConfigEnvKey: `{"mode":"shadow","base_ref":"main","force_full_run":true}`}),
	})
	if rep.Decision != "full" || rep.ReasonKind != "forced" || len(rep.Selected) != 3 {
		t.Fatalf("%+v", rep)
	}
}

func TestRunShadowTimeoutAndPanic(t *testing.T) {
	root := flutterRepo(t)
	defer func() { run = Run }()

	run = func(Config, Deps) (Result, error) { time.Sleep(3 * time.Second); return Result{}, nil }
	start := time.Now()
	rep, _ := RunShadow(ShadowOptions{
		Config: Config{RepoDir: root},
		Getenv: env(map[string]string{ConfigEnvKey: `{"mode":"shadow","base_ref":"main","timeout_seconds":1}`}),
	})
	if rep.Decision != DecisionError || rep.ReasonKind != ReasonTimeout || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout: %+v after %s", rep, time.Since(start))
	}

	run = func(Config, Deps) (Result, error) { panic("boom") }
	rep, _ = RunShadow(ShadowOptions{
		Config: Config{RepoDir: root},
		Getenv: env(map[string]string{ConfigEnvKey: shadowJSON}),
	})
	if rep.Decision != DecisionError || rep.ReasonKind != ReasonPanic {
		t.Fatalf("panic: %+v", rep)
	}
}

func TestWriteReport(t *testing.T) {
	if p, err := WriteReport("", Report{}); p != "" || err != nil {
		t.Fatalf("%q %v", p, err)
	}
	dir := filepath.Join(t.TempDir(), "step")
	in := Report{Version: 1, Step: "xcode-test", Decision: "selected", ReasonKind: "graph", Selected: []string{"A"}, Skipped: []string{}}
	p, err := WriteReport(dir, in)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	var out Report
	if err := json.Unmarshal(data, &out); err != nil || !reflect.DeepEqual(in, out) {
		t.Fatalf("%v %+v", err, out)
	}
}
