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
