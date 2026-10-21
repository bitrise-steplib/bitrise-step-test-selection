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
`selector.RunShadow` before its test run: when the backend injects
`BITRISE_TEST_SELECTION_CONFIG_JSON` with `"mode": "shadow"`, it computes the selection the
step would have made, logs one summary line and returns a report. The step still runs every
test, and writes the report with `selector.WriteReport` into `$BITRISE_TEST_RESULT_DIR`.

```go
rep, ok := selector.RunShadow(selector.ShadowOptions{
	Step:    "xcode-test",
	Config:  selector.Config{ProjectType: "xcode", ProjectPath: cfg.ProjectPath, Scheme: cfg.Scheme},
	WorkDir: filepath.Dir(cfg.ProjectPath),
	Logf:    logger.Printf,
	Debugf:  logger.Debugf,
})
if ok {
	_, _ = selector.WriteReport(os.Getenv("BITRISE_TEST_RESULT_DIR"), rep)
}
```

The backend configuration accepts the step's `config` fields plus:

| Field | Meaning |
|---|---|
| `mode` | Only `shadow` enables the runner. |
| `base_ref` | Commit or branch to diff against. Falls back to `BITRISE_TEST_SELECTION_BASE_REF`, then to the PR target branch. |
| `sample_rate` | Fraction of builds that run the analysis, decided from the build slug. Defaults to every build. |
| `timeout_seconds` | Analysis time limit, 60 by default. Git and Gradle are killed when it passes. |

The runner never panics or fails the step. A timeout, a panic or an analysis error becomes a
report with `"decision": "error"` and a `reason_kind` (`timeout`, `panic`, `failed`, `no_project`).
Without a base it reports `"decision": "full"` with `reason_kind: "no_base"` and analyses nothing.

`test-selection.json` in the step's test result directory names test units, never files:

```json
{
  "version": 1,
  "step": "xcode-test",
  "decision": "selected",
  "reason_kind": "graph",
  "base": "4ae226e2759122ddc4212f8768378fb742cae9e9",
  "changed_files": 1,
  "duration_ms": 412,
  "candidates": 4,
  "selected": ["FeatureTests", "FixtureAppTests", "NetworkingTests"],
  "skipped": ["CoreTests"]
}
```

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
