// replay runs the selection over a repository's history: for every commit it takes
// the commit's own diff as the change set and records what the step would have
// decided. With -swift it also checks the SPM propagation against the target graph
// that `swift package dump-package` reports.
//
//	go run ./tools/replay -repo ~/src/app -project App/App.xcodeproj -scheme App -n 200 -swift swift
//
// The project graph is built from the checked-out tree, not from each historical
// commit, so the numbers are an approximation of what a live run would have done.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bitrise-steplib/bitrise-step-test-selection/internal/selection"
	"github.com/bitrise-steplib/bitrise-step-test-selection/internal/step"
	"github.com/bitrise-steplib/bitrise-step-test-selection/tools/internal/spmdump"
)

type record struct {
	sha        string
	files      int
	mode       selection.Mode
	reason     string
	candidates int
	selected   int
}

func main() {
	repo := flag.String("repo", ".", "repository root")
	project := flag.String("project", "", "xcworkspace or xcodeproj, relative to the repo")
	scheme := flag.String("scheme", "", "scheme to restrict candidates")
	gradleDir := flag.String("gradle-dir", "", "gradle project dir, relative to the repo")
	flutterDir := flag.String("flutter-dir", "", "flutter project dir, relative to the repo")
	n := flag.Int("n", 200, "number of first-parent commits to replay")
	swift := flag.String("swift", "", "swift binary; enables the dump-package oracle for the root package")
	verbose := flag.Bool("v", false, "print one line per commit")
	flag.Parse()

	root, err := filepath.Abs(*repo)
	if err != nil {
		fatal(err)
	}
	shas := lines(git(root, "rev-list", "--first-parent", "-n", fmt.Sprint(*n), "HEAD"))

	var oracle *oracleGraph
	if *swift != "" {
		if _, err := os.Stat(filepath.Join(root, "Package.swift")); err == nil {
			pkg, err := spmdump.Load(*swift, root)
			if err != nil {
				fatal(err)
			}
			oracle = newOracle(root, pkg)
			fmt.Printf("oracle: %s with %d targets\n", pkg.Name, len(pkg.Targets))
		}
	}

	var records []record
	fullReasons := map[string]int{}
	agree, disagree := 0, 0
	var disagreements []string
	sumCandidates, sumRun := 0, 0

	for _, sha := range shas {
		files := parseNameStatus(gitAllowFail(root, "diff", "--name-status", "-M", "--no-color", sha+"^", sha))
		if len(files) == 0 {
			continue
		}
		cfg := step.Config{
			ChangedFiles:         strings.Join(files, "\n"),
			ChangedFilesComplete: "true",
			ProjectType:          "auto",
			ProjectPath:          *project,
			Scheme:               *scheme,
			GradleProjectDir:     *gradleDir,
			GradlewPath:          "gradlew",
			FlutterProjectDir:    *flutterDir,
			OnUnknownFile:        "full",
			Mode:                 "enforce",
			OnError:              "full",
			RepoDir:              root,
		}
		res, err := step.Run(cfg, step.Deps{})
		if err != nil {
			fatal(err)
		}
		r := record{sha: sha[:8], files: len(files), mode: res.Mode, reason: res.Reason, candidates: res.Stats.Candidates, selected: res.Stats.Selected}
		records = append(records, r)
		sumCandidates += r.candidates
		if r.mode == selection.ModeFull {
			sumRun += r.candidates
			fullReasons[category(res)]++
		} else {
			sumRun += r.selected
		}
		if *verbose {
			fmt.Printf("%s  %3d files  %-8s  %3d/%-3d  %s\n", r.sha, r.files, r.mode, r.selected, r.candidates, truncate(r.reason, 70))
		}

		if oracle != nil && res.Mode == selection.ModeSelected {
			want := oracle.affectedTests(files, candidateNames(res))
			got := selectedSPM(res)
			if equal(want, got) {
				agree++
			} else {
				disagree++
				if len(disagreements) < 8 {
					disagreements = append(disagreements, fmt.Sprintf("%s: step %v\n            oracle %v", r.sha, got, want))
				}
			}
		}
	}

	full := 0
	for _, r := range records {
		if r.mode == selection.ModeFull {
			full++
		}
	}
	fmt.Printf("\ncommits replayed: %d (of %d, empty diffs skipped)\n", len(records), len(shas))
	fmt.Printf("full runs: %d (%.0f%%)\n", full, pct(full, len(records)))
	for _, k := range sortedKeys(fullReasons) {
		fmt.Printf("  %-28s %d\n", k, fullReasons[k])
	}
	fmt.Printf("selected runs: %d\n", len(records)-full)
	if sumCandidates > 0 {
		fmt.Printf("test units run: %d of %d (%.0f%% skipped overall)\n", sumRun, sumCandidates, 100-pct(sumRun, sumCandidates))
	}
	if oracle != nil {
		fmt.Printf("oracle agreement on selected runs: %d agree, %d disagree\n", agree, disagree)
		for _, d := range disagreements {
			fmt.Printf("  %s\n", d)
		}
	}
	if disagree > 0 {
		os.Exit(1)
	}
}

func category(res selection.Result) string {
	switch {
	case strings.Contains(res.Reason, "(rule "):
		return "global file rule"
	case strings.Contains(res.Reason, "no known build unit"):
		return "unknown owner"
	case strings.Contains(res.Reason, "analysis failed"):
		return "analysis error"
	case strings.Contains(res.Reason, "incomplete"), strings.Contains(res.Reason, "no changed file"):
		return "no change list"
	}
	return "other"
}

func candidateNames(res selection.Result) map[string]bool {
	out := map[string]bool{}
	for _, t := range res.Tests {
		if t.Ecosystem == "spm" {
			out[t.Name] = true
		}
	}
	return out
}

func selectedSPM(res selection.Result) []string {
	var out []string
	for _, t := range res.Tests {
		if t.Ecosystem == "spm" && t.Selected {
			out = append(out, t.Name)
		}
	}
	sort.Strings(out)
	return out
}

// oracleGraph is an independent implementation of the propagation, built from
// dump-package instead of the scanner, over one package.
type oracleGraph struct {
	root    string
	paths   map[string]string // target → repo-relative dir
	isTest  map[string]bool
	reverse map[string][]string
}

func newOracle(root string, pkg *spmdump.Package) *oracleGraph {
	o := &oracleGraph{root: root, paths: map[string]string{}, isTest: map[string]bool{}, reverse: map[string][]string{}}
	exists := func(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }
	names := map[string]bool{}
	for _, t := range pkg.Targets {
		names[t.Name] = true
	}
	products := map[string][]string{}
	for _, p := range pkg.Products {
		products[p.Name] = p.Targets
	}
	for _, t := range pkg.Targets {
		p := t.Path
		if p == "" {
			p = spmdump.DefaultPath(pkg.Dir, t, exists)
		}
		rel, _ := filepath.Rel(root, p)
		o.paths[t.Name] = filepath.ToSlash(rel)
		o.isTest[t.Name] = t.Type == "test"
		var deps []string
		for _, d := range t.ByName {
			if names[d] {
				deps = append(deps, d)
			} else {
				deps = append(deps, products[d]...)
			}
		}
		deps = append(deps, t.TargetDeps...)
		for _, d := range t.ProductDeps {
			deps = append(deps, products[d]...)
		}
		for _, d := range deps {
			o.reverse[d] = append(o.reverse[d], t.Name)
		}
	}
	return o
}

func (o *oracleGraph) owner(file string) string {
	best, bestLen := "", -1
	for name, dir := range o.paths {
		if (file == dir || strings.HasPrefix(file, dir+"/")) && len(dir) > bestLen {
			best, bestLen = name, len(dir)
		}
	}
	return best
}

func (o *oracleGraph) affectedTests(files []string, candidates map[string]bool) []string {
	seen := map[string]bool{}
	var queue []string
	for _, f := range files {
		// The step treats the manifest as part of every target; the oracle agrees.
		if f == "Package.swift" {
			for name := range o.paths {
				if !seen[name] {
					seen[name] = true
					queue = append(queue, name)
				}
			}
			continue
		}
		if owner := o.owner(f); owner != "" && !seen[owner] {
			seen[owner] = true
			queue = append(queue, owner)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, up := range o.reverse[cur] {
			if !seen[up] {
				seen[up] = true
				queue = append(queue, up)
			}
		}
	}
	var out []string
	for name := range seen {
		if o.isTest[name] && candidates[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func git(dir string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		fatal(fmt.Errorf("git %v: %v\n%s", args, err, out))
	}
	return string(out)
}

func gitAllowFail(dir string, args ...string) string {
	out, _ := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return string(out)
}

func parseNameStatus(out string) []string {
	var files []string
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(parts) >= 2 {
			files = append(files, parts[1:]...)
		}
	}
	return files
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func equal(a, b []string) bool {
	return strings.Join(a, ",") == strings.Join(b, ",")
}

func sortedKeys(m map[string]int) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
