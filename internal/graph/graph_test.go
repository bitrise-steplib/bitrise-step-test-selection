package graph

import (
	"reflect"
	"sort"
	"testing"
)

func build() *Graph {
	g := New()
	g.Add(&Node{ID: "core", Kind: KindSource, Dirs: []string{"Modules/Core"}})
	g.Add(&Node{ID: "feature", Kind: KindSource, Dirs: []string{"Modules/Feature"}, Deps: []string{"core"}})
	g.Add(&Node{ID: "app", Kind: KindSource, Dirs: []string{"App"}, Deps: []string{"feature"}, Files: map[string]bool{"Shared/Config.swift": true}})
	g.Add(&Node{ID: "coreTests", Kind: KindTest, Dirs: []string{"Modules/Core/Tests"}, Deps: []string{"core"}})
	g.Add(&Node{ID: "featureTests", Kind: KindTest, Dirs: []string{"Modules/Feature/Tests"}, Deps: []string{"feature"}})
	g.Add(&Node{ID: "appTests", Kind: KindTest, Dirs: []string{"AppTests"}, Deps: []string{"app"}})
	g.Add(&Node{ID: "manual", Kind: KindSource, Globs: []string{"Scripts/**/*.rb"}})
	g.Add(&Node{ID: "scriptTests", Kind: KindTest, Deps: []string{"manual"}})
	g.Finalize()
	return g
}

func sorted(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestOwnersLongestPrefixWins(t *testing.T) {
	g := build()
	if got := g.Owners("Modules/Core/Tests/FooTests.swift"); !reflect.DeepEqual(got, []string{"coreTests"}) {
		t.Fatalf("got %v", got)
	}
	if got := g.Owners("Modules/Core/Sources/Foo.swift"); !reflect.DeepEqual(got, []string{"core"}) {
		t.Fatalf("got %v", got)
	}
}

func TestOwnersExplicitFileWins(t *testing.T) {
	g := build()
	if got := g.Owners("./Shared/Config.swift"); !reflect.DeepEqual(got, []string{"app"}) {
		t.Fatalf("got %v", got)
	}
}

func TestOwnersGlob(t *testing.T) {
	g := build()
	if got := g.Owners("Scripts/ci/run.rb"); !reflect.DeepEqual(got, []string{"manual"}) {
		t.Fatalf("got %v", got)
	}
}

func TestOwnersUnknown(t *testing.T) {
	g := build()
	if got := g.Owners("README.md"); len(got) != 0 {
		t.Fatalf("expected no owner, got %v", got)
	}
}

func TestDependentsTransitive(t *testing.T) {
	g := build()
	got := sorted(g.Dependents([]string{"core"}))
	want := []string{"app", "appTests", "core", "coreTests", "feature", "featureTests"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	got = sorted(g.Dependents([]string{"feature"}))
	want = []string{"app", "appTests", "feature", "featureTests"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAddMergesSameID(t *testing.T) {
	g := New()
	g.Add(&Node{ID: "a", Kind: KindSource, Dirs: []string{"x"}})
	g.Add(&Node{ID: "a", Kind: KindTest, Dirs: []string{"y"}, Deps: []string{"b"}})
	g.Finalize()
	n := g.Node("a")
	if n.Kind != KindTest || len(n.Dirs) != 2 || len(n.Deps) != 1 {
		t.Fatalf("merge failed: %+v", n)
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{"./a/b/": "a/b", "a\\b": "a/b", "": ".", "./": "."}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q)=%q want %q", in, got, want)
		}
	}
}
