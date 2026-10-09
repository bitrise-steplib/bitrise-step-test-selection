// Package spmdump loads a package description through `swift package dump-package`,
// the authoritative view of a manifest, for validating the step's own scanner.
package spmdump

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Package is the subset of the dump the validation needs.
type Package struct {
	Dir      string
	Name     string
	Targets  []Target
	Products []Product
}

// Target is one target as SwiftPM sees it.
type Target struct {
	Name string
	// Type is regular, test, executable, system, binary, plugin or macro.
	Type string
	// Path is the absolute source directory when the manifest sets one, else "".
	Path        string
	ByName      []string
	TargetDeps  []string
	ProductDeps []string
}

// Product maps a product name to its targets.
type Product struct {
	Name    string
	Targets []string
}

type rawPackage struct {
	Name    string `json:"name"`
	Targets []struct {
		Name         string                     `json:"name"`
		Type         string                     `json:"type"`
		Path         *string                    `json:"path"`
		Dependencies []map[string][]interface{} `json:"dependencies"`
	} `json:"targets"`
	Products []struct {
		Name    string   `json:"name"`
		Targets []string `json:"targets"`
	} `json:"products"`
}

// Load runs dump-package in dir with the given swift binary.
func Load(swift, dir string) (*Package, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(swift, "package", "dump-package")
	cmd.Dir = abs
	out, err := cmd.Output()
	if err != nil {
		msg := ""
		if ee, ok := err.(*exec.ExitError); ok {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("dump-package in %s: %w\n%s", dir, err, msg)
	}
	var raw rawPackage
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse dump-package output: %w", err)
	}
	pkg := &Package{Dir: abs, Name: raw.Name}
	for _, t := range raw.Targets {
		tg := Target{Name: t.Name, Type: t.Type}
		if t.Path != nil && *t.Path != "" {
			tg.Path = filepath.Join(abs, *t.Path)
		}
		for _, dep := range t.Dependencies {
			for kind, vals := range dep {
				if len(vals) == 0 {
					continue
				}
				name, _ := vals[0].(string)
				switch kind {
				case "byName":
					tg.ByName = append(tg.ByName, name)
				case "target":
					tg.TargetDeps = append(tg.TargetDeps, name)
				case "product":
					tg.ProductDeps = append(tg.ProductDeps, name)
				}
			}
		}
		pkg.Targets = append(pkg.Targets, tg)
	}
	for _, p := range raw.Products {
		pkg.Products = append(pkg.Products, Product{Name: p.Name, Targets: p.Targets})
	}
	return pkg, nil
}

// DefaultPath returns the directory SwiftPM would use for a target without an
// explicit path, following its lookup order.
func DefaultPath(pkgDir string, t Target, exists func(string) bool) string {
	roots := []string{"Sources", "Source", "src", "srcs"}
	if t.Type == "test" {
		roots = []string{"Tests", "Sources", "Source", "src", "srcs"}
	}
	if t.Type == "plugin" {
		roots = []string{"Plugins", "Sources", "Source", "src", "srcs"}
	}
	for _, r := range roots {
		p := filepath.Join(pkgDir, r, t.Name)
		if exists(p) {
			return p
		}
	}
	return filepath.Join(pkgDir, roots[0], t.Name)
}
