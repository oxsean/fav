package fileio

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func collect(t *testing.T, path string, from int64, max int) ([]string, []int64, int64) {
	t.Helper()
	var lines []string
	var offs []int64
	end, err := Lines(context.Background(), path, from, max, func(off int64, line []byte) bool {
		lines, offs = append(lines, string(line)), append(offs, off)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	return lines, offs, end
}

func TestLinesLeavesAPartialLineAndSkipsLongOnes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.jsonl")
	body := "one\n" + strings.Repeat("x", 100) + "\ntwo\nhalf"
	os.WriteFile(p, []byte(body), 0o644)
	lines, offs, end := collect(t, p, 0, 64)
	if strings.Join(lines, "|") != "one\n|two\n" {
		t.Fatalf("lines %q", lines)
	}
	if offs[0] != 0 || offs[1] != int64(4+101) {
		t.Fatalf("offsets %v", offs)
	}
	if want := int64(len(body) - len("half")); end != want {
		t.Fatalf("end %d, want %d (before the partial line)", end, want)
	}
	os.WriteFile(p, []byte(body+"\n"), 0o644)
	lines, _, end = collect(t, p, end, 64)
	if strings.Join(lines, "|") != "half\n" || end != int64(len(body)+1) {
		t.Fatalf("resume: %q end %d", lines, end)
	}
}

func TestLinesStopsWhenAsked(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.jsonl")
	os.WriteFile(p, []byte("a\nb\nc\n"), 0o644)
	n := 0
	end, err := Lines(context.Background(), p, 0, 64, func(int64, []byte) bool { n++; return n < 2 })
	if err != nil || n != 2 || end != 4 {
		t.Fatalf("n %d end %d err %v", n, end, err)
	}
}

func TestWriteAtomicReplacesAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	os.WriteFile(p, []byte("old"), 0o644)
	if err := WriteFile(p, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Fatalf("content %q", b)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("temp file left behind: %v", ents)
	}
	boom := os.ErrInvalid
	if err := WriteAtomic(p, 0o600, func(w io.Writer) error { io.WriteString(w, "half"); return boom }); err != boom {
		t.Fatalf("err %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Fatalf("a failed write changed the file: %q", b)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("temp file left behind after a failure: %v", ents)
	}
}

func TestWriteAtomicKeepsASymlink(t *testing.T) {
	dir := t.TempDir()
	real, link := filepath.Join(dir, "real.json"), filepath.Join(dir, "link.json")
	os.WriteFile(real, []byte("old"), 0o644)
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here")
	}
	if err := WriteFile(link, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Lstat(link); st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced by a file")
	}
	if b, _ := os.ReadFile(real); string(b) != "new" {
		t.Fatalf("target %q", b)
	}
}

func TestIDChangesWhenAFileIsRenamedOver(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	os.WriteFile(p, []byte("a\n"), 0o644)
	id := ID(p)
	if id == "" {
		t.Fatal("no id")
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("b\n")
	f.Close()
	if ID(p) != id {
		t.Fatal("appending keeps the file")
	}
	if err := WriteAtomic(p, 0o644, func(w io.Writer) error { _, err := w.Write([]byte("a\nb\n")); return err }); err != nil {
		t.Fatal(err)
	}
	if got := ID(p); got == id || got == "" {
		t.Fatalf("a rewrite is another file: %q %q", id, got)
	}
	if ID(filepath.Join(dir, "none")) != "" {
		t.Fatal("a missing file has no id")
	}
}

func TestAnOpenFileKeepsItsIDWhateverItsPathNamesNow(t *testing.T) {
	dir := t.TempDir()
	p, q := filepath.Join(dir, "output.log"), filepath.Join(dir, "other.log")
	os.WriteFile(p, []byte("a\n"), 0o644)
	os.WriteFile(q, []byte("b\n"), 0o644)
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g, err := os.Open(q)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	id := IDOf(f)
	if id == "" || id != ID(p) || IDOf(g) != ID(q) || IDOf(g) == id {
		t.Fatalf("%q %q %q", id, ID(p), IDOf(g))
	}
	if runtime.GOOS == "windows" { // ⚠️ an open file cannot be renamed over there
		return
	}
	if err := os.Rename(q, p); err != nil {
		t.Fatal(err)
	}
	if IDOf(f) != id || ID(p) == id {
		t.Fatalf("the open file is the one it opened: %q, the path names another: %q", IDOf(f), ID(p))
	}
}

func TestMoveFallsBackToCopyAcrossDevices(t *testing.T) {
	rename = func(string, string) error { return errors.New("invalid cross-device link") }
	t.Cleanup(func() { rename = Rename })
	src, dst := t.TempDir(), t.TempDir()
	tree := filepath.Join(src, "s1")
	os.MkdirAll(filepath.Join(tree, "sub"), 0o755)
	os.WriteFile(filepath.Join(tree, "sub", "x"), []byte("x"), 0o640)
	file := filepath.Join(src, "s1.jsonl")
	os.WriteFile(file, []byte("{}\n"), 0o600)
	then := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	os.Chtimes(file, then, then)

	if err := Move(tree, filepath.Join(dst, "s1")); err != nil {
		t.Fatal(err)
	}
	if err := Move(file, filepath.Join(dst, "s1.jsonl")); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "s1", "sub", "x")); err != nil || string(b) != "x" {
		t.Fatalf("nested file not copied: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "s1.jsonl")); err != nil || string(b) != "{}\n" {
		t.Fatalf("file not copied: %v", err)
	}
	if st, _ := os.Stat(filepath.Join(dst, "s1.jsonl")); !st.ModTime().Equal(then) {
		t.Errorf("time not kept: %v", st.ModTime())
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(filepath.Join(dst, "s1", "sub", "x")); st.Mode().Perm() != 0o640 {
			t.Errorf("mode not kept: %v", st.Mode())
		}
	}
	for _, p := range []string{tree, file} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s: the source stays after the copy", p)
		}
	}
}
