package changes

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type execRunner struct{}

func (execRunner) Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := execRunner{}.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupRepos creates an "origin" with main and a feature branch, then a shallow clone
// of the feature branch, mirroring what the git-clone step leaves behind.
func setupRepos(t *testing.T) (origin, clone string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	origin = t.TempDir()
	run(t, origin, "init", "-q", "-b", "main")
	run(t, origin, "config", "user.email", "t@example.com")
	run(t, origin, "config", "user.name", "t")
	run(t, origin, "config", "uploadpack.allowFilter", "true")
	run(t, origin, "config", "uploadpack.allowAnySHA1InWant", "true")
	write(t, origin, "App/Main.swift", "a")
	write(t, origin, "Modules/Core/Core.swift", "c")
	run(t, origin, "add", ".")
	run(t, origin, "commit", "-q", "-m", "base")
	run(t, origin, "checkout", "-q", "-b", "feature")
	for i := 0; i < 5; i++ {
		write(t, origin, "Modules/Core/Core.swift", "c"+strings.Repeat("x", i+1))
		run(t, origin, "commit", "-q", "-am", "feature work")
	}
	write(t, origin, "Modules/Core/Old.swift", "old")
	run(t, origin, "add", ".")
	run(t, origin, "commit", "-q", "-m", "add old")
	run(t, origin, "mv", "Modules/Core/Old.swift", "Modules/Core/New.swift")
	run(t, origin, "commit", "-q", "-m", "rename")
	run(t, origin, "checkout", "-q", "main")
	write(t, origin, "App/Main.swift", "a2")
	run(t, origin, "commit", "-q", "-am", "main moved on")

	clone = filepath.Join(t.TempDir(), "clone")
	run(t, filepath.Dir(clone), "clone", "-q", "--depth=1", "--branch", "feature", "file://"+origin, "clone")
	return origin, clone
}

func TestFromGitShallowClone(t *testing.T) {
	_, clone := setupRepos(t)
	res, err := FromGit(execRunner{}, clone, "main")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Modules/Core/Core.swift", "Modules/Core/New.swift"}
	if !reflect.DeepEqual(res.Files, want) {
		t.Fatalf("files %v want %v", res.Files, want)
	}
	if !res.Complete || res.Origin != "git" {
		t.Fatalf("unexpected result %+v", res)
	}
}

func TestFromGitMissingBase(t *testing.T) {
	_, clone := setupRepos(t)
	if _, err := FromGit(execRunner{}, clone, "no-such-branch"); err == nil {
		t.Fatal("expected an error for a missing base ref")
	}
}

func TestParseNameStatus(t *testing.T) {
	out := "M\ta.swift\nR100\told.swift\tnew.swift\nA\tb/c.kt\n\n"
	got := parseNameStatus(out)
	want := []string{"a.swift", "b/c.kt", "new.swift", "old.swift"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestFromList(t *testing.T) {
	res := FromList("b.swift\n./a.swift\n\na.swift\n", true)
	if !reflect.DeepEqual(res.Files, []string{"a.swift", "b.swift"}) || !res.Complete {
		t.Fatalf("got %+v", res)
	}
}
