// Package flutter builds the package graph of a Dart/Flutter repository from the
// pubspec.yaml files it contains and the dependencies between them.
package flutter

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bitrise-steplib/bitrise-step-test-selection/internal/graph"
)

// Directories that never contain first-party packages.
var skipDirs = map[string]bool{
	".git": true, ".dart_tool": true, "build": true, ".symlinks": true, "Pods": true,
	"node_modules": true, ".pub-cache": true, ".fvm": true, "ios": true, "android": true,
	"macos": true, "linux": true, "windows": true, "web": true,
}

type pubspec struct {
	Name         string                 `yaml:"name"`
	Dependencies map[string]interface{} `yaml:"dependencies"`
	DevDeps      map[string]interface{} `yaml:"dev_dependencies"`
	Overrides    map[string]interface{} `yaml:"dependency_overrides"`
}

const overridesFile = "pubspec_overrides.yaml"

// testUnits are the test folders of a package. Integration tests run on a device, so
// they are a unit of their own that a UI test run can select without the unit tests.
var testUnits = []struct{ dir, idSuffix, nameSuffix, kind string }{
	{"test", ":test", " tests", "unit"},
	{"integration_test", ":integration_test", " integration tests", "ui"},
}

type pkg struct {
	name    string
	dir     string // absolute
	deps    []string
	devDeps []string
	// Hosted dependencies, by name.
	hosted    []string
	devHosted []string
}

// Build scans dir (relative to root) for packages and returns their graph.
func Build(root, dir string) (*graph.Graph, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	start := filepath.Join(root, dir)
	var pkgs []*pkg
	err = filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != start && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "pubspec.yaml" {
			return nil
		}
		pk, err := parse(p)
		if err != nil {
			return err
		}
		pkgs = append(pkgs, pk)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no pubspec.yaml found under %s", dir)
	}

	// Package names are not unique in a monorepo (flutter/packages has nine packages
	// named `example`). A duplicated name gets its directory in the ID; otherwise the
	// packages would merge into one unit that runs only one folder's tests.
	nameCount := map[string]int{}
	for _, pk := range pkgs {
		nameCount[pk.name]++
	}
	idOf := func(pk *pkg) string {
		id := "flutter:" + pk.name
		if nameCount[pk.name] > 1 {
			if relDir, ok := rel(root, pk.dir); ok {
				id += "@" + relDir
			}
		}
		return id
	}

	byDir := map[string]string{}
	byName := map[string][]string{}
	g := graph.New()
	for _, pk := range pkgs {
		id := idOf(pk)
		byDir[pk.dir] = id
		byName[pk.name] = append(byName[pk.name], id)
		relDir, ok := rel(root, pk.dir)
		if !ok {
			continue
		}
		// A package at the repository root must not own every file in the repository,
		// so it gets the conventional package directories only.
		dirs := []string{relDir}
		if relDir == "." {
			dirs = nil
			for _, d := range []string{"lib", "bin", "assets", "fonts", "images"} {
				if st, err := os.Stat(filepath.Join(pk.dir, d)); err == nil && st.IsDir() {
					dirs = append(dirs, d)
				}
			}
		}
		g.Add(&graph.Node{
			ID: id, Name: pk.name, Kind: graph.KindSource, Ecosystem: "flutter",
			Dirs:  dirs,
			Files: map[string]bool{path.Join(relDir, "pubspec.yaml"): true, path.Join(relDir, overridesFile): true},
			Meta:  map[string]string{"package_dir": relDir},
		})
		for _, unit := range testUnits {
			if st, err := os.Stat(filepath.Join(pk.dir, unit.dir)); err != nil || !st.IsDir() {
				continue
			}
			g.Add(&graph.Node{
				ID: id + unit.idSuffix, Name: pk.name + unit.nameSuffix, Kind: graph.KindTest, Ecosystem: "flutter",
				Dirs: []string{path.Join(relDir, unit.dir)}, Deps: []string{id},
				Meta: map[string]string{"package_dir": relDir, "kind": unit.kind},
			})
		}
	}
	link := func(from string, dirs, names []string) {
		for _, d := range dirs {
			if to, ok := byDir[d]; ok {
				g.AddDep(from, to)
			}
		}
		// A hosted dependency on a duplicated name links to every package of that name.
		for _, n := range names {
			for _, to := range byName[n] {
				g.AddDep(from, to)
			}
		}
	}
	for _, pk := range pkgs {
		id := idOf(pk)
		link(id, pk.deps, pk.hosted)
		for _, unit := range testUnits {
			link(id+unit.idSuffix, pk.devDeps, pk.devHosted)
		}
	}
	g.Finalize()
	return g, nil
}

// readOverrides returns the dependency_overrides of the pubspec_overrides.yaml in dir,
// which melos and aft generate to link the repository's packages to each other.
func readOverrides(dir string) (map[string]interface{}, error) {
	p := filepath.Join(dir, overridesFile)
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ps pubspec
	if err := yaml.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return ps.Overrides, nil
}

// hostedDeps returns the names of dependencies resolved by version rather than from
// a path, git or the SDK.
func hostedDeps(deps map[string]interface{}) []string {
	var out []string
	for name, v := range deps {
		if m, ok := v.(map[string]interface{}); ok && (m["path"] != nil || m["git"] != nil || m["sdk"] != nil) {
			continue
		}
		out = append(out, name)
	}
	return out
}

func parse(path string) (*pkg, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ps pubspec
	if err := yaml.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	overrides, err := readOverrides(dir)
	if err != nil {
		return nil, err
	}
	pk := &pkg{name: ps.Name, dir: dir}
	if pk.name == "" {
		pk.name = filepath.Base(dir)
	}
	pk.deps = append(pathDeps(dir, ps.Dependencies), pathDeps(dir, ps.Overrides)...)
	pk.deps = append(pk.deps, pathDeps(dir, overrides)...)
	pk.devDeps = pathDeps(dir, ps.DevDeps)
	// A hosted dependency on a package in the repository is an edge too. Pub workspaces
	// resolve it to the local package, and melos, aft and flutter/packages' CI rewrite
	// it to a path dependency before running the tests.
	pk.hosted = hostedDeps(ps.Dependencies)
	pk.devHosted = hostedDeps(ps.DevDeps)
	return pk, nil
}

// pathDeps returns the absolute directories of `path:` dependencies.
func pathDeps(dir string, deps map[string]interface{}) []string {
	var out []string
	for _, v := range deps {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if p, ok := m["path"].(string); ok && p != "" {
			out = append(out, filepath.Clean(filepath.Join(dir, p)))
		}
	}
	return out
}

func rel(root, p string) (string, bool) {
	r, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(r, "..") {
		return "", false
	}
	return filepath.ToSlash(r), true
}
