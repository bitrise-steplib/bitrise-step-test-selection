// spmcheck compares the step's Package.swift scanner with `swift package dump-package`.
//
//	go run ./tools/spmcheck -swift /path/to/swift <package dir>...
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bitrise-steplib/bitrise-step-test-selection/internal/xcode"
	"github.com/bitrise-steplib/bitrise-step-test-selection/tools/internal/spmdump"
)

func main() {
	swift := flag.String("swift", "swift", "swift binary")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: spmcheck -swift <bin> <package dir>...")
		os.Exit(2)
	}
	failed := false
	for _, dir := range flag.Args() {
		if !check(*swift, dir) {
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func check(swift, dir string) bool {
	fmt.Printf("== %s\n", dir)
	truth, err := spmdump.Load(swift, dir)
	if err != nil {
		fmt.Printf("   dump-package failed: %v\n", err)
		return false
	}
	ours, err := xcode.ParsePackage(dir)
	if err != nil {
		fmt.Printf("   scanner failed: %v\n", err)
		return false
	}
	var problems []string
	if ours.Name != truth.Name {
		problems = append(problems, fmt.Sprintf("name: ours %q truth %q", ours.Name, truth.Name))
	}

	oursByName := map[string]xcode.PackageTarget{}
	for _, t := range ours.Targets {
		oursByName[t.Name] = t
	}
	truthByName := map[string]spmdump.Target{}
	for _, t := range truth.Targets {
		truthByName[t.Name] = t
	}
	for name := range truthByName {
		if _, ok := oursByName[name]; !ok {
			problems = append(problems, "missing target "+name)
		}
	}
	for name := range oursByName {
		if _, ok := truthByName[name]; !ok {
			problems = append(problems, "extra target "+name)
		}
	}
	exists := func(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }
	for name, tt := range truthByName {
		ot, ok := oursByName[name]
		if !ok {
			continue
		}
		if ot.IsTest != (tt.Type == "test") {
			problems = append(problems, fmt.Sprintf("%s: test flag ours=%v truth=%s", name, ot.IsTest, tt.Type))
		}
		want := tt.Path
		if want == "" && tt.Type != "binary" {
			want = spmdump.DefaultPath(truth.Dir, tt, exists)
		}
		if filepath.Clean(ot.Path) != filepath.Clean(want) {
			problems = append(problems, fmt.Sprintf("%s: path ours=%s truth=%s", name, rel(dir, ot.Path), rel(dir, want)))
		}
		wantTargets := sorted(append(append([]string{}, tt.ByName...), tt.TargetDeps...))
		gotTargets := sorted(ot.TargetDeps)
		if strings.Join(wantTargets, ",") != strings.Join(gotTargets, ",") {
			problems = append(problems, fmt.Sprintf("%s: target deps ours=%v truth=%v", name, gotTargets, wantTargets))
		}
		wantProducts := sorted(tt.ProductDeps)
		gotProducts := sorted(ot.ProductDeps)
		if strings.Join(wantProducts, ",") != strings.Join(gotProducts, ",") {
			problems = append(problems, fmt.Sprintf("%s: product deps ours=%v truth=%v", name, gotProducts, wantProducts))
		}
	}
	for _, p := range truth.Products {
		got := sorted(ours.Products[p.Name])
		want := sorted(p.Targets)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			problems = append(problems, fmt.Sprintf("product %s: ours=%v truth=%v", p.Name, got, want))
		}
	}

	fmt.Printf("   %d targets, %d products, %d problems\n", len(truth.Targets), len(truth.Products), len(problems))
	for _, p := range problems {
		fmt.Printf("   - %s\n", p)
	}
	return len(problems) == 0
}

func sorted(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func rel(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil {
		return r
	}
	return p
}
