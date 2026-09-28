package node

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/oxsean/fav/internal/testkit"
	"github.com/oxsean/fav/internal/wire"
)

func TestDirsListsOnlyInsideTheAllowedRoots(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	outside := t.TempDir()
	for _, d := range []string{"api", "web/.git", ".cache", "Web2"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "notes.txt"), nil, 0o600)
	n := New(t.TempDir())
	n.Limits.AllowDirs = []string{root}

	top, err := n.Dirs(DirsParams{})
	if err != nil || len(top.Dirs) != 1 || top.Dirs[0].Path != root {
		t.Fatalf("no path lists the roots: %+v %v", top, err)
	}
	got, err := n.Dirs(DirsParams{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range got.Dirs {
		names = append(names, d.Name)
		if d.Name == "web" && !d.Git {
			t.Error("a checkout is marked")
		}
	}
	if len(names) != 3 || names[0] != "api" || names[1] != "web" || names[2] != "Web2" {
		t.Fatalf("subdirectories only, hidden ones left out, by name: %v", names)
	}
	if got.Parent != "" {
		t.Fatalf("nothing above a root: %q", got.Parent)
	}
	if in, _ := n.Dirs(DirsParams{Path: filepath.Join(root, "api")}); in.Parent != root {
		t.Fatalf("one level up stays inside: %q", in.Parent)
	}
	for _, p := range []string{outside, "relative/path", filepath.Join(root, "..")} {
		if _, err := n.Dirs(DirsParams{Path: p}); wire.Code(err) != wire.CodeUnauthorized {
			t.Errorf("%s is refused: %v", p, err)
		}
	}
}

func TestDirsRefusesALinkOutOfTheRoots(t *testing.T) {
	testkit.PosixOnly(t)
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	n := New(t.TempDir())
	n.Limits.AllowDirs = []string{root}
	if _, err := n.Dirs(DirsParams{Path: filepath.Join(root, "escape")}); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a link out is refused: %v", err)
	}
}
