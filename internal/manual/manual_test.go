package manual

import (
	"reflect"
	"sort"
	"testing"
)

func TestParse(t *testing.T) {
	g, err := Parse("# comment\nScripts/**/*.rb => ScriptTests, ToolingTests\n\nFastlane/** => ToolingTests\n")
	if err != nil {
		t.Fatal(err)
	}
	var tests []string
	for _, n := range g.TestNodes() {
		tests = append(tests, n.Name)
	}
	if !reflect.DeepEqual(tests, []string{"ScriptTests", "ToolingTests"}) {
		t.Fatalf("tests %v", tests)
	}
	deps := g.Dependents(g.Owners("Scripts/ci/run.rb"))
	var got []string
	for id := range deps {
		got = append(got, id)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"manual:ScriptTests", "manual:ToolingTests", "manual:rule1"}) {
		t.Errorf("dependents %v", got)
	}
	if owners := g.Owners("Fastlane/Fastfile"); !reflect.DeepEqual(owners, []string{"manual:rule2"}) {
		t.Errorf("owners %v", owners)
	}
	if _, err := Parse("no arrow here"); err == nil {
		t.Error("expected an error for a malformed rule")
	}
}
