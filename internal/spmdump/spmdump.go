// Package spmdump reads the package description `swift package dump-package` prints,
// the authoritative view of a manifest.
package spmdump

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Package is the subset of the dump the selection needs.
type Package struct {
	Dir      string
	Name     string
	Targets  []Target
	Products []Product
	// LocalDependencies are absolute directories of `.package(path:)` entries.
	LocalDependencies []string
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
	Name         string `json:"name"`
	Dependencies []struct {
		FileSystem []struct {
			Path string `json:"path"`
		} `json:"fileSystem"`
	} `json:"dependencies"`
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

// underDir re-roots path, which SwiftPM prints with symlinks resolved (/private/var on
// macOS), onto dir as the caller spelled it, so it compares with the caller's paths.
func underDir(dir, path string) string {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(real, path)
	if err != nil {
		return path
	}
	joined := filepath.Join(dir, rel)
	if resolved, err := filepath.EvalSymlinks(joined); err != nil || resolved != path {
		return path
	}
	return joined
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
	return Parse(abs, out)
}

// Parse reads dump-package output for the package in dir. Diagnostics printed before
// or after the JSON are skipped, so the output may include stderr.
func Parse(dir string, out []byte) (*Package, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	start := -1
	for i := 0; i < len(out); {
		if out[i] == '{' {
			start = i
			break
		}
		nl := bytes.IndexByte(out[i:], '\n')
		if nl < 0 {
			break
		}
		i += nl + 1
	}
	if start < 0 {
		return nil, fmt.Errorf("parse dump-package output: no JSON object")
	}
	var raw rawPackage
	if err := json.NewDecoder(bytes.NewReader(out[start:])).Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse dump-package output: %w", err)
	}
	pkg := &Package{Dir: abs, Name: raw.Name}
	for _, dep := range raw.Dependencies {
		for _, fs := range dep.FileSystem {
			pkg.LocalDependencies = append(pkg.LocalDependencies, underDir(abs, fs.Path))
		}
	}
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
