package fileio

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
