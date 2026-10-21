package selector

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// ConfigEnvKey carries the backend's selection configuration into the build.
	ConfigEnvKey = "BITRISE_TEST_SELECTION_CONFIG_JSON"
	// BaseRefEnvKey is the commit or branch the backend wants the change diffed against.
	BaseRefEnvKey = "BITRISE_TEST_SELECTION_BASE_REF"
	// ReportFileName is written into the step's test result directory, next to the
	// CLI's step-info.json, so that it covers the step run rather than one test run.
	ReportFileName = "test-selection.json"
	// ReportVersion is the schema version of Report.
	ReportVersion = 1

	defaultShadowTimeout = 60 * time.Second
)

// Decisions a shadow report can carry besides the selection modes.
const (
	DecisionError = "error"
)

// Reason kinds that only the shadow runner produces.
const (
	ReasonNoBase    ReasonKind = "no_base"
	ReasonTimeout   ReasonKind = "timeout"
	ReasonPanic     ReasonKind = "panic"
	ReasonNoProject ReasonKind = "no_project"
	ReasonFailed    ReasonKind = "failed"
)

// BackendConfig is the part of BITRISE_TEST_SELECTION_CONFIG_JSON the test steps read.
// The whole JSON is also handed to Run, so its rule lists and force_full_run apply.
type BackendConfig struct {
	Mode           string   `json:"mode"`
	BaseRef        string   `json:"base_ref"`
	SampleRate     *float64 `json:"sample_rate"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

// ShadowConfig returns the backend configuration when it asks for a shadow run.
func ShadowConfig(getenv func(string) string) (BackendConfig, bool) {
	raw := strings.TrimSpace(getenv(ConfigEnvKey))
	if raw == "" {
		return BackendConfig{}, false
	}
	var bc BackendConfig
	if err := json.Unmarshal([]byte(raw), &bc); err != nil {
		return BackendConfig{}, false
	}
	return bc, bc.Mode == "shadow"
}

// Sampled reports whether this build falls into the sample. The build slug makes the
// decision stable across the steps of one build.
func Sampled(rate *float64, buildSlug string) bool {
	if rate == nil || *rate >= 1 {
		return true
	}
	if *rate <= 0 {
		return false
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(buildSlug))
	return float64(h.Sum32())/float64(1<<32) < *rate
}

// Report is what a test step records about a shadow selection. It names test units
// but never files, because it leaves the customer's build.
type Report struct {
	Version      int        `json:"version"`
	Step         string     `json:"step"`
	Decision     string     `json:"decision"`
	ReasonKind   ReasonKind `json:"reason_kind"`
	Base         string     `json:"base,omitempty"`
	ChangedFiles int        `json:"changed_files"`
	DurationMS   int64      `json:"duration_ms"`
	Candidates   int        `json:"candidates"`
	Selected     []string   `json:"selected"`
	Skipped      []string   `json:"skipped"`
}

// ShadowOptions describe the consuming step's project.
type ShadowOptions struct {
	// Step names the consuming step in the report, for example "xcode-test".
	Step string
	// Config holds the project inputs (ProjectType, ProjectPath, Scheme,
	// GradleProjectDir, FlutterProjectDir...). The runner fills in the base ref, the
	// backend configuration and the safe defaults.
	Config Config
	// WorkDir is used to find the repository root when Config.RepoDir is empty.
	WorkDir string
	// Keep limits the report to the test units the step runs. Nil keeps every unit.
	Keep func(Test) bool
	// Getenv defaults to os.Getenv.
	Getenv func(string) string
	// Logf prints the one-line summary; Debugf gets the analysis details.
	Logf, Debugf func(format string, v ...interface{})
}

// run is replaced in tests.
var run = Run

// RunShadow computes the selection a test step would have made, without changing
// what it runs. ok is false when the backend did not ask for a shadow run or the build
// is not sampled. It never panics and never takes longer than the configured timeout.
func RunShadow(opts ShadowOptions) (rep Report, ok bool) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	debugf := opts.Debugf
	if debugf == nil {
		debugf = func(string, ...interface{}) {}
	}

	bc, enabled := ShadowConfig(getenv)
	if !enabled {
		return Report{}, false
	}
	if !Sampled(bc.SampleRate, getenv("BITRISE_BUILD_SLUG")) {
		debugf("Test selection shadow run: this build is not in the sample")
		return Report{}, false
	}

	start := time.Now()
	rep = Report{Version: ReportVersion, Step: opts.Step, Selected: []string{}, Skipped: []string{}}
	finish := func(decision string, kind ReasonKind, detail string) (Report, bool) {
		rep.Decision = decision
		rep.ReasonKind = kind
		rep.DurationMS = time.Since(start).Milliseconds()
		logf("Test selection (shadow, nothing was skipped): %s", detail)
		return rep, true
	}

	baseRef := firstNonEmpty(bc.BaseRef, getenv(BaseRefEnvKey), getenv("BITRISEIO_GIT_BRANCH_DEST"))
	if baseRef == "" {
		return finish(string(ModeFull), ReasonNoBase, "no base commit or target branch to compare with")
	}

	cfg := opts.Config
	cfg.BaseRef = baseRef
	cfg.BackendConfig = getenv(ConfigEnvKey)
	cfg.Mode = "shadow"
	// The env list carries no completeness flag yet, so the diff always comes from git.
	cfg.ChangedFiles = ""
	cfg.ChangedFilesComplete = ""
	cfg.OnError = "full"
	if cfg.OnUnknownFile == "" {
		cfg.OnUnknownFile = "full"
	}
	if cfg.ProjectType == "" {
		cfg.ProjectType = "auto"
	}

	timeout := defaultShadowTimeout
	if bc.TimeoutSeconds > 0 {
		timeout = time.Duration(bc.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if cfg.RepoDir == "" {
		root, err := (execGit{ctx}).Run(firstNonEmpty(opts.WorkDir, getenv("BITRISE_SOURCE_DIR"), "."), "rev-parse", "--show-toplevel")
		if err != nil || root == "" {
			debugf("cannot find the repository root: %v", err)
			return finish(DecisionError, ReasonNoProject, "the working directory is not a git repository")
		}
		cfg.RepoDir = root
	}

	type outcome struct {
		res      Result
		err      error
		panicked interface{}
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- outcome{panicked: p}
			}
		}()
		res, err := run(cfg, Deps{Git: execGit{ctx}, Cmd: execCmd{ctx}, Logf: debugf, Debugf: debugf})
		done <- outcome{res: res, err: err}
	}()

	var o outcome
	select {
	case o = <-done:
	case <-ctx.Done():
		return finish(DecisionError, ReasonTimeout, fmt.Sprintf("the analysis did not finish in %s", timeout))
	}
	if o.panicked != nil {
		debugf("test selection panicked: %v", o.panicked)
		return finish(DecisionError, ReasonPanic, "the analysis failed unexpectedly")
	}
	if o.err != nil {
		debugf("test selection failed: %v", o.err)
		return finish(DecisionError, ReasonFailed, "the analysis failed: "+o.err.Error())
	}

	res := o.res
	rep.Base = res.ChangedFiles.Base
	rep.ChangedFiles = res.ChangedFiles.Count
	for _, t := range res.Tests {
		if opts.Keep != nil && !opts.Keep(t) {
			continue
		}
		rep.Candidates++
		if t.Selected {
			rep.Selected = append(rep.Selected, t.Name)
		} else {
			rep.Skipped = append(rep.Skipped, t.Name)
		}
	}
	detail := fmt.Sprintf("would have run %d of %d test units (%s)", len(rep.Selected), rep.Candidates, res.Reason)
	if len(rep.Skipped) > 0 {
		detail += "; would have skipped: " + strings.Join(rep.Skipped, ", ")
	}
	return finish(string(res.Mode), res.ReasonKind, detail)
}

// WriteReport stores the report in the step's test result directory. An empty dir
// means the step runs outside the Bitrise CLI and nothing is written.
func WriteReport(dir string, rep Report) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, ReportFileName)
	return p, os.WriteFile(p, data, 0o644)
}

// execGit and execCmd run commands bound to the shadow timeout, so a slow git fetch
// or Gradle configuration is killed rather than left running next to the tests.
type execGit struct{ ctx context.Context }

func (g execGit) Run(dir string, args ...string) (string, error) {
	return runCommand(g.ctx, dir, "git", args...)
}

type execCmd struct{ ctx context.Context }

func (c execCmd) Run(dir, name string, args ...string) (string, error) {
	return runCommand(c.ctx, dir, name, args...)
}

func runCommand(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
