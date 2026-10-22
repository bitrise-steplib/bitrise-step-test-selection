package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bitrise-io/go-steputils/v2/export"
	"github.com/bitrise-io/go-steputils/v2/stepconf"
	"github.com/bitrise-io/go-utils/v2/command"
	"github.com/bitrise-io/go-utils/v2/env"
	"github.com/bitrise-io/go-utils/v2/log"

	"github.com/bitrise-steplib/bitrise-step-test-selection/internal/selection"
	"github.com/bitrise-steplib/bitrise-step-test-selection/internal/step"
)

type gitRunner struct{ factory command.Factory }

func (r gitRunner) Run(dir string, args ...string) (string, error) {
	return r.factory.Create("git", args, &command.Opts{Dir: dir}).RunAndReturnTrimmedCombinedOutput()
}

type cmdRunner struct{ factory command.Factory }

func (r cmdRunner) Run(dir, name string, args ...string) (string, error) {
	return r.factory.Create(name, args, &command.Opts{Dir: dir}).RunAndReturnTrimmedCombinedOutput()
}

func main() {
	os.Exit(run())
}

func run() int {
	logger := log.NewLogger()
	envRepo := env.NewRepository()
	cmdFactory := command.NewFactory(envRepo)

	var cfg step.Config
	if err := stepconf.NewInputParser(envRepo).Parse(&cfg); err != nil {
		logger.Errorf("Invalid inputs: %s", err)
		return 1
	}
	stepconf.Print(cfg)
	logger.EnableDebugLog(cfg.Verbose)
	logger.Println()

	var res selection.Result
	var err error
	if strings.TrimSpace(cfg.BackendConfig) == "" {
		res = step.NotEnabled()
		logger.Warnf("Test selection is not enabled for this workspace, every test runs.")
	} else {
		res, err = step.Run(cfg, step.Deps{
			Git:    gitRunner{cmdFactory},
			Cmd:    cmdRunner{cmdFactory},
			Logf:   logger.Printf,
			Debugf: logger.Debugf,
		})
		if err != nil {
			logger.Errorf("Test selection failed: %s", err)
			return 1
		}
	}

	outputDir := cfg.OutputDir
	if outputDir == "" {
		outputDir, err = os.MkdirTemp("", "test-selection")
		if err != nil {
			logger.Errorf("Cannot create output directory: %s", err)
			return 1
		}
	}
	files, err := selection.Write(outputDir, res)
	if err != nil {
		logger.Errorf("Cannot write the selection: %s", err)
		return 1
	}

	fmt.Println(res.Summary())

	exporter := export.NewDefaultExporter(cmdFactory)
	_, envmanErr := exec.LookPath("envman")
	if envmanErr != nil {
		logger.Infof("envman is not available (not running under the Bitrise CLI); outputs are printed instead of exported")
	}
	outputs := map[string]string{
		"BITRISE_TEST_SELECTION_PATH":                  files.JSON,
		"BITRISE_TEST_SELECTION_MODE":                  string(res.Mode),
		"BITRISE_TEST_SELECTION_SHADOW":                fmt.Sprint(res.Shadow),
		"BITRISE_TEST_SELECTION_XCODE_ONLY_TESTING_PATH": files.XcodeOnlyTesting,
		"BITRISE_TEST_SELECTION_GRADLE_MODULES_PATH":   files.GradleModules,
		"BITRISE_TEST_SELECTION_GRADLE_TASKS_PATH":     files.GradleTasks,
		"BITRISE_TEST_SELECTION_FLUTTER_PACKAGES_PATH": files.FlutterPackages,
		"BITRISE_TEST_SELECTION_SUMMARY":               fmt.Sprintf("%s: %d of %d test units selected", res.Mode, res.Stats.Selected, res.Stats.Candidates),
	}
	for key, value := range outputs {
		if value == "" {
			continue
		}
		if envmanErr != nil {
			logger.Printf("%s=%s", key, value)
			continue
		}
		if err := exporter.ExportOutput(key, value); err != nil {
			logger.Warnf("Cannot export %s: %s", key, err)
		}
	}
	logger.Donef("Selection written to %s", filepath.Clean(files.JSON))
	return 0
}
