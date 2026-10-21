# Test Selection step

Works out which test units a change affects and writes the list for the test steps.

```
changed files ──► file rules ──► project graph ──► propagation ──► selection JSON
   env / git      full-run,       Xcode, SPM,        reverse deps     + per-ecosystem
                  ignore          Gradle, Flutter,   + test host       list files
                                  manual rules
```

## How it decides

1. **Changed files** come from `BITRISE_GIT_CHANGED_FILES`. When the list is missing or
   marked incomplete, the step diffs `HEAD` against the merge base with `base_ref`,
   deepening the shallow clone the git-clone step leaves behind.
2. **File rules** run first. A file on the full-run list (lockfiles, xcconfig, root Gradle
   files, `bitrise.yml`, …) forces a full run. Ignored files (docs, markdown, GitHub
   metadata) are dropped.
3. **The graph** is built per ecosystem. Xcode: `project.pbxproj` build phases, synced
   folders, target dependencies, linked products, `TEST_HOST`, plus every local Swift
   package (`Package.swift` targets, products and `.package(path:)`). Gradle: an init
   script dumps projects, source sets and project dependencies. Flutter: `pubspec.yaml`
   path dependencies. Manual: `glob => Test` rules.
4. **Ownership** of each changed file: explicit membership, then globs, then the longest
   matching directory. A file nobody owns forces a full run (`on_unknown_file`).
5. **Propagation** follows reverse dependencies transitively, with no depth limit. A test
   unit is selected when it or anything it depends on changed, or when it matches a
   must-run pattern.

Any analysis error is a full run with the reason in the log and the JSON, never a guess.

## Output

`test-selection.json`:

```json
{
  "version": 1,
  "mode": "selected",
  "shadow": false,
  "reason": "3 changed files mapped onto the project graph",
  "reason_kind": "graph",
  "changed_files": {"origin": "git", "complete": true, "base": "…", "count": 3, "files": ["…"]},
  "owners": [{"file": "Modules/Core/Sources/Cache.swift", "nodes": ["spm:Core/Core"]}],
  "affected_nodes": ["spm:Core/Core", "spm:Core/CoreTests", "xcode:App/App", "xcode:App/AppTests"],
  "tests": [
    {"id": "xcode:App/AppTests", "name": "AppTests", "ecosystem": "xcode", "selected": true,
     "reason": "via Core ← Modules/Core/Sources/Cache.swift", "identifiers": {"only_testing": "AppTests"}},
    {"id": "xcode:App/AppUITests", "name": "AppUITests", "ecosystem": "xcode", "selected": false,
     "reason": "no changed dependency", "identifiers": {"only_testing": "AppUITests"}}
  ],
  "stats": {"candidates": 2, "selected": 1, "skipped": 1, "must_run": 0}
}
```

Next to it, in `selected` mode only: `xcode-only-testing.txt`, `gradle-modules.txt`, `gradle-tasks.txt`,
`flutter-packages.txt`. In `full` mode no list file is written, because an empty list
means "everything" to the consuming steps.

`reason_kind` classifies the decision without file names: `graph`, `forced`, `no_changes`,
`incomplete_changes`, `global_file`, `unknown_file` or `analysis_failed`.

## Using it from a test step (shadow mode)

The `selector` package is the importable form of this step. A test step calls
`selector.StartShadow` before its test run: when the backend injects
`BITRISE_TEST_SELECTION_CONFIG_JSON` with `"mode": "shadow"`, it computes the selection the
step would have made and logs one summary line. The step still runs every test. Afterwards
it reports what each test unit did with `Finish`, which works out the time a selection
would have saved and whether it would have skipped a failing unit, and publishes both on
the step analytics channel.

```go
sh, ok := selector.StartShadow(selector.ShadowOptions{
	Step:    "xcode-test",
	Config:  selector.Config{ProjectType: "xcode", ProjectPath: cfg.ProjectPath, Scheme: cfg.Scheme},
	WorkDir: filepath.Dir(cfg.ProjectPath),
	Keep:    func(t selector.Test, repoDir string) bool { return runs(t.Name) },
	Logf:    logger.Printf,
	Debugf:  logger.Debugf,
})
started := time.Now()
runTests()
if ok {
	sh.Finish(outcomesPerTarget(), time.Since(started)) // nil outcomes: results unreadable
}
```

`selector.UnitKey` names a unit the way `UnitOutcome.Key` must: the Xcode test target, the
Gradle project path (`:feature:home`) or the Flutter package directory.
`selector.JUnitOutcome` builds an outcome from JUnit XML files.

The backend configuration accepts the step's `config` fields plus:

| Field | Meaning |
|---|---|
| `mode` | Only `shadow` enables the runner. |
| `base_ref` | Commit or branch to diff against. Falls back to `BITRISE_TEST_SELECTION_BASE_REF`, then to the PR target branch. |
| `base_build_slug` | The build `base_ref` was taken from, passed through to the events so a failure can be checked against it. |
| `sample_rate` | Fraction of builds that run the analysis, decided from the build slug. Defaults to every build. |
| `sample_rates` | Per-step override of `sample_rate`, keyed by step (`xcode-test`, `xcode-test-without-building`, `android-unit-test`, `flutter-test`). |
| `timeout_seconds` | Analysis time limit, 60 by default. Git and Gradle are killed when it passes. |

The runner never panics or fails the step. A timeout, a panic or an analysis error becomes
`"decision": "error"` with a `reason_kind` (`timeout`, `panic`, `failed`, `no_project`).
Without a base it reports `"decision": "full"` with `reason_kind: "no_base"` and analyses nothing.

### Events

Both events carry `step_id`, `build_slug`, `app_slug`, `workspace_id`, `decision`,
`reason_kind`, `base` and `base_build_slug`. They name test units, never files.

- **`test_selection_shadow`**, once per step run: `selected` and `skipped` (comma-separated),
  `units` (JSON: name, selected, mapped, ran, tests, failed, flaky, duration_ms), `duration_ms`
  of the analysis, `test_run_ms`, `estimated_saving_ms`, `missed_failure`, `missed_units`,
  `flaky_in_skipped`, `outcomes_known`.
- **`test_selection_missed_failure`**, once per unit that the selection would have skipped
  although a test in it failed in its final attempt: `unit`, `mapped`, `tests`, `failed`, `flaky`.

The rules behind the numbers, only for `"decision": "selected"`:

- **Saving:** the summed duration of the units that ran but would have been skipped. When no
  selected unit ran, the step itself would have been skipped, so the saving is the whole
  test run's wall time.
- **Missed failure:** a skipped unit with a test whose final attempt failed. A test that only
  failed before passing on a retry counts as flaky, not missed.
- **Unmapped units:** a unit that ran but the analysis did not know (`"mapped": false`) counts as
  skipped, because a selection would not have run it either.

Set `ANALYTICS_DISABLED=true` to send nothing. The report is printed at debug level, or in
the normal log together with the analytics client's delivery errors when
`BITRISE_TEST_SELECTION_SHADOW_VERBOSE=true`.

## Trying it on a repository

```bash
export BITRISE_SOURCE_DIR=/path/to/repo
export changed_files="$(git -C $BITRISE_SOURCE_DIR diff --name-only origin/main...HEAD)"
export changed_files_complete=true
export project_path=App.xcworkspace scheme=App
export project_type=auto on_unknown_file=full mode=enforce on_error=full verbose=true
export repo_dir=$BITRISE_SOURCE_DIR output_dir=/tmp/test-selection
go run .
```

Or let the step diff itself: leave `changed_files` empty and set `base_ref=main`.

## Development

```bash
go test ./...
go vet ./...
```
