package xcode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bitrise-steplib/bitrise-step-test-selection/internal/spmdump"
)

// firstError picks the line of swift's output that says what went wrong; a manifest
// compile failure prints the whole swiftc command line first.
func firstError(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	line := lines[0]
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "error:") {
			line = l
			break
		}
	}
	line = strings.TrimSpace(line)
	if len(line) > 200 {
		line = line[:200] + "..."
	}
	return line
}

// dumpPackage evaluates dir/Package.swift with `swift package dump-package`.
func dumpPackage(dir string, opts Options) (*Package, error) {
	// Without a scratch path SwiftPM creates .build inside the user's package.
	scratch, err := os.MkdirTemp("", "test-selection-spm")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	out, err := opts.Cmd.Run(dir, opts.Swift, "package", "--scratch-path", scratch, "dump-package")
	if err != nil {
		return nil, fmt.Errorf("swift package dump-package failed: %v: %s", err, firstError(out))
	}
	dump, err := spmdump.Parse(dir, []byte(out))
	if err != nil {
		return nil, err
	}

	pkg := &Package{Dir: dump.Dir, Name: dump.Name, Products: map[string][]string{}, LocalDependencies: dump.LocalDependencies}
	for _, t := range dump.Targets {
		target := PackageTarget{
			Name:        t.Name,
			IsTest:      t.Type == "test",
			Path:        t.Path,
			TargetDeps:  append(append([]string{}, t.ByName...), t.TargetDeps...),
			ProductDeps: t.ProductDeps,
		}
		// A remote binary has no files in the repository.
		if target.Path == "" && t.Type != "binary" {
			target.Path = filepath.Join(dump.Dir, defaultTargetDir(dump.Dir, t.Name, target.IsTest, t.Type == "plugin"))
		}
		pkg.Targets = append(pkg.Targets, target)
	}
	for _, p := range dump.Products {
		pkg.Products[p.Name] = append(pkg.Products[p.Name], p.Targets...)
	}
	return pkg, nil
}

// loadPackage reads dir/Package.swift through SwiftPM when it is available, because
// manifests build their targets in Swift code (helper functions, constants, arrays
// joined with +) that the scanner cannot follow, and through the scanner otherwise.
func loadPackage(dir, root string, opts Options) (*Package, error) {
	reason := "swift is not available"
	if opts.Swift != "" && opts.Cmd != nil {
		pkg, err := dumpPackage(dir, opts)
		if err == nil {
			return pkg, nil
		}
		reason = err.Error()
	}
	if opts.Logf != nil {
		name, ok := relTo(root, filepath.Join(dir, "Package.swift"))
		if !ok {
			name = filepath.Join(dir, "Package.swift")
		}
		opts.Logf("%s: %s; reading it with the built-in manifest scanner, which can miss targets declared through helpers", name, reason)
	}
	return ParsePackage(dir)
}
