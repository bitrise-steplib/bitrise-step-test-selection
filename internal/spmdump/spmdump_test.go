package spmdump

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"Modules", "Tools"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// SwiftPM prints local package paths with symlinks resolved (/private/var on macOS).
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	out := `warning: 'modules': a diagnostic on stderr
{
  "name" : "Modules",
  "dependencies" : [
    { "sourceControl" : [ { "identity" : "alamofire" } ] },
    { "fileSystem" : [ { "identity" : "tools", "path" : "` + real + `/Tools" } ] }
  ],
  "products" : [ { "name" : "Lib", "targets" : [ "Lib" ] } ],
  "targets" : [
    {
      "name" : "Lib",
      "type" : "regular",
      "path" : "Sources/Custom",
      "dependencies" : [
        { "byName" : [ "Core", null ] },
        { "target" : [ "Util", null ] },
        { "product" : [ "Alamofire", "Alamofire", null, null ] }
      ]
    },
    { "name" : "LibTests", "type" : "test", "dependencies" : [ { "byName" : [ "Lib", null ] } ] }
  ]
}
warning: a diagnostic after the JSON
`
	pkg, err := Parse(filepath.Join(root, "Modules"), []byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := &Package{
		Dir:  filepath.Join(root, "Modules"),
		Name: "Modules",
		Targets: []Target{
			{Name: "Lib", Type: "regular", Path: filepath.Join(root, "Modules", "Sources", "Custom"), ByName: []string{"Core"}, TargetDeps: []string{"Util"}, ProductDeps: []string{"Alamofire"}},
			{Name: "LibTests", Type: "test", ByName: []string{"Lib"}},
		},
		Products:          []Product{{Name: "Lib", Targets: []string{"Lib"}}},
		LocalDependencies: []string{filepath.Join(root, "Tools")},
	}
	if !reflect.DeepEqual(pkg, want) {
		t.Errorf("got  %+v\nwant %+v", pkg, want)
	}
}

func TestParseWithoutJSON(t *testing.T) {
	if _, err := Parse(t.TempDir(), []byte("error: 'modules': invalid manifest\n")); err == nil {
		t.Error("want an error")
	}
}
