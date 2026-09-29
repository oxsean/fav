package node

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fileState struct {
	data  []byte
	mtime time.Time
	mode  fs.FileMode
}

// filesOf is every file under dir, .git's index included, by path.
func filesOf(t *testing.T, dir string) map[string]fileState {
	t.Helper()
	out := map[string]fileState{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			if rel == ".git" {
				fi, _ := os.Stat(filepath.Join(p, "index"))
				b, _ := os.ReadFile(filepath.Join(p, "index"))
				out[".git/index"] = fileState{b, fi.ModTime(), fi.Mode()}
				return filepath.SkipDir
			}
			return nil
		}
		fi, _ := d.Info()
		b, _ := os.ReadFile(p)
		out[rel] = fileState{b, fi.ModTime(), fi.Mode()}
		return nil
	})
	return out
}

// A tree of the workspace is taken through a copy of the index: untracked files are in it, ignored ones are not, and
// the user's index and working tree stay as they were, byte for byte and time for time.
func TestTreesLeaveTheUserAlone(t *testing.T) {
	needGit(t)
	dir := repo(t)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.o\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged\n"), 0o644)
	git(t, dir, "add", ".gitignore", "staged.txt")
	os.WriteFile(filepath.Join(dir, "README"), []byte("app, changed and not staged\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("untracked\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "build.o"), []byte("ignored\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "deep.txt"), []byte("deep\n"), 0o644)
	before := filesOf(t, dir)
	time.Sleep(20 * time.Millisecond) // a write in between would show in the mtimes

	runDir := t.TempDir()
	tr, err := takeTrees(dir)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := takeTree(dir, filepath.Join(runDir, baseIndex), "")
	if err != nil {
		t.Fatal(err)
	}
	after := filesOf(t, dir)
	if len(after) != len(before) {
		t.Fatalf("files %d then %d", len(before), len(after))
	}
	for p, b := range before {
		a := after[p]
		if !bytes.Equal(a.data, b.data) || !a.mtime.Equal(b.mtime) || a.mode != b.mode {
			t.Errorf("%s changed", p)
		}
	}
	if out := git(t, dir, "status", "--porcelain"); !strings.Contains(out, " M README") || !strings.Contains(out, "A  staged.txt") || !strings.Contains(out, "?? new.txt") {
		t.Fatalf("status %q", out)
	}
	listed := git(t, tr.GitDir, "--git-dir="+tr.GitDir, "ls-tree", "-r", "--name-only", tree)
	for _, want := range []string{"README", "new.txt", "staged.txt", "sub/deep.txt"} {
		if !strings.Contains(listed, want) {
			t.Errorf("%s not in the tree: %s", want, listed)
		}
	}
	if strings.Contains(listed, "build.o") {
		t.Error("ignored files stay out")
	}
	if got := git(t, dir, "cat-file", "-p", tree+":README"); got != "app, changed and not staged" {
		t.Fatalf("README in the tree: %q", got)
	}

	tr.Base = tree
	if err := tr.ref("r_1", "base", tree); err != nil {
		t.Fatal(err)
	}
	if got := git(t, dir, "rev-parse", refPrefix+"r_1/base"); got != tree {
		t.Fatalf("ref %s", got)
	}
	tr.unref("r_1")
	if _, err := (gitIn{dir: dir}).run("rev-parse", "--verify", "--quiet", refPrefix+"r_1/base"); err == nil {
		t.Fatal("the ref goes with the run")
	}
}

// A run in a subdirectory of a repository records where it is in it.
func TestTreesOfASubdirectory(t *testing.T) {
	needGit(t)
	dir := repo(t)
	sub := filepath.Join(dir, "pkg")
	os.MkdirAll(sub, 0o755)
	tr, err := takeTrees(sub)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Prefix != "pkg/" || !tr.Git {
		t.Fatalf("%+v", tr)
	}
	if _, err := takeTrees(t.TempDir()); err == nil {
		t.Fatal("outside git there is no repository")
	}
}
