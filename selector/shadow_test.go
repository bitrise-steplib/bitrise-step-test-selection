package selector

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bitrise-io/go-utils/v2/analytics"
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
	if _, ok := StartShadow(ShadowOptions{Getenv: env(nil)}); ok {
		t.Fatal("ran without configuration")
	}
	zero := `{"mode":"shadow","sample_rate":0}`
	if _, ok := StartShadow(ShadowOptions{Getenv: env(map[string]string{ConfigEnvKey: zero})}); ok {
		t.Fatal("ran outside the sample")
	}
}

func TestSampleRateFor(t *testing.T) {
	bc, _ := ShadowConfig(env(map[string]string{ConfigEnvKey: `{"mode":"shadow","sample_rate":0.5,"sample_rates":{"android-unit-test":0}}`}))
	if r := bc.SampleRateFor("android-unit-test"); r == nil || *r != 0 {
		t.Errorf("android: %v", r)
	}
	if r := bc.SampleRateFor("xcode-test"); r == nil || *r != 0.5 {
		t.Errorf("xcode: %v", r)
	}
	_, ok := StartShadow(ShadowOptions{Step: "android-unit-test", Getenv: env(map[string]string{ConfigEnvKey: `{"mode":"shadow","sample_rates":{"android-unit-test":0}}`})})
	if ok {
		t.Error("android ran at a zero sample rate")
	}
}

func TestRunShadowNoBase(t *testing.T) {
	sh, ok := StartShadow(ShadowOptions{Step: "flutter-test", Getenv: env(map[string]string{ConfigEnvKey: `{"mode":"shadow"}`})})
	if !ok || sh.Report.Decision != "full" || sh.Report.ReasonKind != ReasonNoBase {
		t.Fatalf("%+v", sh.Report)
	}
}

func TestRunShadowNoXcodeProject(t *testing.T) {
	sh, ok := StartShadow(ShadowOptions{
		Config: Config{ProjectType: "xcode"},
		Getenv: env(map[string]string{ConfigEnvKey: shadowJSON}),
	})
	rep := sh.Report
	if !ok || rep.Decision != DecisionError || rep.ReasonKind != ReasonNoProject {
		t.Fatalf("%+v", rep)
	}
}

func TestRunShadowSelects(t *testing.T) {
	root := flutterRepo(t)
	var logged []string
	sh, ok := StartShadow(ShadowOptions{
		Step:    "flutter-test",
		Config:  Config{ProjectType: "flutter"},
		WorkDir: filepath.Join(root, "packages", "ui"),
		Getenv:  env(map[string]string{ConfigEnvKey: shadowJSON}),
		Logf:    func(f string, v ...interface{}) { logged = append(logged, fmt.Sprintf(f, v...)) },
	})
	rep := sh.Report
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
	sh, _ := StartShadow(ShadowOptions{
		Config: Config{ProjectType: "flutter", RepoDir: root},
		Getenv: env(map[string]string{ConfigEnvKey: shadowJSON}),
		Keep: func(t Test, repoDir string) bool {
			rel, ok := RelPath(repoDir, filepath.Join(root, "packages", "core"))
			return ok && t.Ident.PackageDir == rel
		},
	})
	rep := sh.Report
	if rep.Candidates != 1 || len(rep.Selected) != 0 || !reflect.DeepEqual(rep.Skipped, []string{"core tests"}) {
		t.Fatalf("%+v", rep)
	}
}

func TestRunShadowAbsoluteProjectDir(t *testing.T) {
	root := flutterRepo(t)
	sh, _ := StartShadow(ShadowOptions{
		Config:  Config{ProjectType: "flutter", FlutterProjectDir: filepath.Join(root, "packages")},
		WorkDir: root,
		Getenv:  env(map[string]string{ConfigEnvKey: shadowJSON}),
	})
	rep := sh.Report
	if rep.Decision != "selected" || !reflect.DeepEqual(rep.Selected, []string{"ui tests"}) {
		t.Fatalf("%+v", rep)
	}
	sh, _ = StartShadow(ShadowOptions{
		Config:  Config{ProjectType: "flutter", FlutterProjectDir: t.TempDir()},
		WorkDir: root,
		Getenv:  env(map[string]string{ConfigEnvKey: shadowJSON}),
	})
	rep = sh.Report
	if rep.ReasonKind != ReasonNoProject {
		t.Fatalf("%+v", rep)
	}
}

func TestRunShadowBaseFromEnv(t *testing.T) {
	root := flutterRepo(t)
	sh, _ := StartShadow(ShadowOptions{
		Config: Config{ProjectType: "flutter", RepoDir: root},
		Getenv: env(map[string]string{ConfigEnvKey: `{"mode":"shadow"}`, "BITRISEIO_GIT_BRANCH_DEST": "main"}),
	})
	rep := sh.Report
	if rep.Decision != "selected" || len(rep.Selected) != 2 {
		t.Fatalf("%+v", rep)
	}
}

func TestRunShadowForcedFullRunSkipsTheAnalysis(t *testing.T) {
	defer func() { run = Run }()
	run = func(Config, Deps) (Result, error) { panic("the analysis must not run") }

	sh, ok := StartShadow(ShadowOptions{
		Config: Config{ProjectType: "flutter", RepoDir: flutterRepo(t)},
		Getenv: env(map[string]string{ConfigEnvKey: `{"mode":"shadow","base_ref":"main","force_full_run":true,"force_full_run_reason":"builds of the default branch run every test"}`}),
	})
	rep := sh.Report
	if !ok || rep.Decision != "full" || rep.ReasonKind != ReasonKind("forced") || rep.Candidates != 0 {
		t.Fatalf("%+v", rep)
	}

	newTracker = func(func(string) string) analytics.Tracker { return nil }
	defer func() { newTracker = defaultNewTracker }()
	sh.Finish([]UnitOutcome{{Key: "packages/core", Tests: 2, Failed: 1, Duration: time.Second}}, 5*time.Second)
	if sh.Report.EstimatedSavingMS != 0 || sh.Report.MissedFailure || !sh.Report.OutcomesKnown {
		t.Fatalf("a forced run saves nothing and misses nothing: %+v", sh.Report)
	}
}

func TestRunShadowTimeoutAndPanic(t *testing.T) {
	root := flutterRepo(t)
	defer func() { run = Run }()

	run = func(Config, Deps) (Result, error) { time.Sleep(3 * time.Second); return Result{}, nil }
	start := time.Now()
	sh, _ := StartShadow(ShadowOptions{
		Config: Config{RepoDir: root},
		Getenv: env(map[string]string{ConfigEnvKey: `{"mode":"shadow","base_ref":"main","timeout_seconds":1}`}),
	})
	rep := sh.Report
	if rep.Decision != DecisionError || rep.ReasonKind != ReasonTimeout || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout: %+v after %s", rep, time.Since(start))
	}

	run = func(Config, Deps) (Result, error) { panic("boom") }
	sh, _ = StartShadow(ShadowOptions{
		Config: Config{RepoDir: root},
		Getenv: env(map[string]string{ConfigEnvKey: shadowJSON}),
	})
	rep = sh.Report
	if rep.Decision != DecisionError || rep.ReasonKind != ReasonPanic {
		t.Fatalf("panic: %+v", rep)
	}
}

type fakeTracker struct {
	events []string
	props  []map[string]interface{}
}

func (f *fakeTracker) Enqueue(name string, properties ...analytics.Properties) {
	f.events = append(f.events, name)
	merged := map[string]interface{}{}
	for _, p := range properties {
		for k, v := range p {
			merged[k] = v
		}
	}
	f.props = append(f.props, merged)
}
func (f *fakeTracker) Wait()            {}
func (f *fakeTracker) IsTracking() bool { return true }

func selectedShadow(t *testing.T) *Shadow {
	t.Helper()
	root := flutterRepo(t)
	sh, ok := StartShadow(ShadowOptions{
		Step:   "flutter-test",
		Config: Config{ProjectType: "flutter", RepoDir: root},
		Getenv: env(map[string]string{ConfigEnvKey: `{"mode":"shadow","base_ref":"main","base_build_slug":"green-build"}`, "BITRISE_BUILD_SLUG": "b1", "BITRISE_APP_SLUG": "a1", "BITRISE_WORKSPACE_ID": "w1"}),
	})
	if !ok || sh.Report.Decision != "selected" {
		t.Fatalf("%+v", sh.Report)
	}
	return sh
}

func TestFinishSavingAndMissedFailure(t *testing.T) {
	fake := &fakeTracker{}
	defer func(orig func(func(string) string) analytics.Tracker) { newTracker = orig }(newTracker)
	newTracker = func(func(string) string) analytics.Tracker { return fake }

	sh := selectedShadow(t)
	sh.Finish([]UnitOutcome{
		{Key: ".", Tests: 3, Duration: 2 * time.Second},
		{Key: "packages/ui", Tests: 2, Duration: time.Second},
		{Key: "packages/core", Tests: 4, Failed: 1, Flaky: 1, Duration: 5 * time.Second},
		{Key: "packages/unknown", Tests: 1, Duration: 500 * time.Millisecond},
	}, 30*time.Second)

	rep := sh.Report
	if rep.EstimatedSavingMS != 5500 || !rep.MissedFailure || !reflect.DeepEqual(rep.MissedUnits, []string{"core tests"}) || rep.FlakyInSkipped != 1 {
		t.Fatalf("%+v", rep)
	}
	if rep.EstimatedSavingByUnitsMS != 15000 {
		t.Errorf("2 of the 4 units that ran would be skipped: %d", rep.EstimatedSavingByUnitsMS)
	}
	if !rep.OutcomesKnown || rep.TestRunMS != 30000 || rep.BaseBuildSlug != "green-build" {
		t.Errorf("%+v", rep)
	}
	var unknown *UnitReport
	for i := range rep.Units {
		if rep.Units[i].Name == "packages/unknown" {
			unknown = &rep.Units[i]
		}
	}
	if unknown == nil || unknown.Mapped || unknown.Selected || !unknown.Ran {
		t.Errorf("an unmapped unit that ran: %+v", unknown)
	}

	if !reflect.DeepEqual(fake.events, []string{ShadowEvent, MissedFailureEvent}) {
		t.Fatalf("events %v", fake.events)
	}
	shadow, missed := fake.props[0], fake.props[1]
	if shadow["estimated_saving_ms"] != int64(5500) || shadow["missed_failure"] != true || shadow["workspace_id"] != "w1" || shadow["build_slug"] != "b1" {
		t.Errorf("shadow event %v", shadow)
	}
	if missed["unit"] != "core tests" || missed["failed"] != 1 || missed["base_build_slug"] != "green-build" {
		t.Errorf("missed failure event %v", missed)
	}
	if units, _ := shadow["units"].(string); !strings.Contains(units, `"name":"core tests"`) || strings.Contains(units, "key") {
		t.Errorf("units %s", units)
	}
}

func TestFinishEmptySelectionSavesTheWholeRun(t *testing.T) {
	newTracker = func(func(string) string) analytics.Tracker { return nil }
	defer func() { newTracker = defaultNewTracker }()

	sh := selectedShadow(t)
	sh.Finish([]UnitOutcome{{Key: "packages/core", Tests: 4, Duration: 3 * time.Second}}, 40*time.Second)
	if sh.Report.EstimatedSavingMS != 40000 || sh.Report.EstimatedSavingByUnitsMS != 40000 || sh.Report.MissedFailure {
		t.Fatalf("%+v", sh.Report)
	}
}

func TestFinishWithoutOutcomesOrSelection(t *testing.T) {
	newTracker = func(func(string) string) analytics.Tracker { return nil }
	defer func() { newTracker = defaultNewTracker }()

	sh := selectedShadow(t)
	sh.Finish(nil, 10*time.Second)
	if sh.Report.OutcomesKnown || sh.Report.EstimatedSavingMS != 0 || sh.Report.MissedFailure {
		t.Fatalf("unknown outcomes: %+v", sh.Report)
	}

	root := flutterRepo(t)
	full, _ := StartShadow(ShadowOptions{
		Config: Config{ProjectType: "flutter", RepoDir: root},
		Getenv: env(map[string]string{ConfigEnvKey: `{"mode":"shadow","base_ref":"main","force_full_run":true}`}),
	})
	full.Finish([]UnitOutcome{{Key: "packages/core", Tests: 1, Failed: 1, Duration: time.Second}}, time.Second)
	if full.Report.EstimatedSavingMS != 0 || full.Report.MissedFailure {
		t.Fatalf("a full run neither saves nor misses: %+v", full.Report)
	}

	var nilShadow *Shadow
	nilShadow.Finish(nil, 0)
}

func TestJUnitOutcome(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.xml", `<?xml version="1.0"?>
<testsuite name="A" tests="4">
  <testcase name="ok" time="0.5"/>
  <testcase name="bad" time="1.25"><failure message="x"/></testcase>
  <testcase name="crash" time="0.25"><error/></testcase>
  <testcase name="off"><skipped/></testcase>
</testsuite>`)
	write(t, dir, "b.xml", `<testsuites><testsuite><testcase name="retried" time="1"><flakyFailure/></testcase></testsuite></testsuites>`)
	got, err := JUnitOutcome(":core", []string{filepath.Join(dir, "a.xml"), filepath.Join(dir, "b.xml")})
	if err != nil {
		t.Fatal(err)
	}
	want := UnitOutcome{Key: ":core", Tests: 4, Failed: 2, Flaky: 1, Duration: 3 * time.Second}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if _, err := JUnitOutcome("x", []string{filepath.Join(dir, "missing.xml")}); err == nil {
		t.Error("a missing file must fail")
	}
}

func TestRelPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		repo, path, want string
		ok               bool
	}{
		{root, root, ".", true},
		{root, filepath.Join(root, "a", "b"), "a/b", true},
		{link, filepath.Join(root, "a"), "a", true},
		{filepath.Join(root, "a"), root, "", false},
	} {
		got, ok := RelPath(c.repo, c.path)
		if got != c.want || ok != c.ok {
			t.Errorf("RelPath(%s, %s) = %q %v, want %q %v", c.repo, c.path, got, ok, c.want, c.ok)
		}
	}
}
