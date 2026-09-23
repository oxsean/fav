package fav

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTrashRoundTrip(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	src := t.TempDir()
	f := filepath.Join(src, "s1.jsonl")
	d := filepath.Join(src, "s1")
	os.WriteFile(f, []byte("{}\n"), 0o644)
	os.MkdirAll(filepath.Join(d, "sub"), 0o755)
	os.WriteFile(filepath.Join(d, "sub", "x"), []byte("x"), 0o644)

	e, err := MoveToTrash(TrashEntry{Provider: ProviderClaude, SessionID: "s1", Title: "t", Record: &Rec{ID: "r1", Title: "t"}}, []string{f, d, filepath.Join(src, "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("原文件应已挪走")
	}
	if len(e.Files) != 2 || e.Transcript() == "" {
		t.Fatalf("应登记两项且能找到记录文件：%+v", e.Files)
	}
	got, _ := LoadTrash()
	if len(got) != 1 || got[0].Record == nil || got[0].Record.ID != "r1" {
		t.Fatalf("manifest 不对：%+v", got)
	}

	os.RemoveAll(src) // restore must work when the original directory is gone
	r, err := RestoreTrash(ProviderClaude, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(d, "sub", "x")); err != nil || string(b) != "x" {
		t.Fatalf("目录应原样回来：%v", err)
	}
	if _, err := os.Stat(f); err != nil || r.Title != "t" {
		t.Fatal("记录文件应回来")
	}
	if got, _ := LoadTrash(); len(got) != 0 {
		t.Fatal("还原后不应还在回收站")
	}
	if _, err := RestoreTrash(ProviderClaude, "s1"); err == nil {
		t.Fatal("再还原应报错")
	}
}

func TestPurgeTrash(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	src := t.TempDir()
	for _, id := range []string{"old", "new"} {
		p := filepath.Join(src, id+".jsonl")
		os.WriteFile(p, []byte("{}\n"), 0o644)
		if _, err := MoveToTrash(TrashEntry{Provider: ProviderCodex, SessionID: id}, []string{p}); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := LoadTrash()
	var oldDir string
	for i := range entries {
		if entries[i].SessionID == "old" {
			entries[i].DeletedAt = time.Now().AddDate(0, 0, -40)
			oldDir = entries[i].Dir
		}
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("the entry's directory should exist before the purge: %v", err)
	}
	saveTrash(entries)
	n, err := PurgeTrash(30)
	if err != nil || n != 1 {
		t.Fatalf("应只清掉过期的一条：n=%d err=%v", n, err)
	}
	left, _ := LoadTrash()
	if len(left) != 1 || left[0].SessionID != "new" {
		t.Fatalf("剩下的不对：%+v", left)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatal("过期条目的目录应已删除")
	}
	if n, _ := PurgeTrash(0); n != 1 {
		t.Fatalf("days=0 应全清：%d", n)
	}
}

func trashDirs(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, p := range []string{ProviderClaude, ProviderCodex} {
		ds, _ := os.ReadDir(filepath.Join(TrashDir(), p))
		for _, d := range ds {
			out = append(out, d.Name())
		}
	}
	return out
}

func TestTrashingASessionAgainKeepsTheEarlierFilesUntilPurge(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	src := t.TempDir()
	p := filepath.Join(src, "s1.jsonl")
	trash := func(body string) TrashEntry {
		t.Helper()
		if body != "" {
			os.WriteFile(p, []byte(body), 0o644)
		}
		e, err := MoveToTrash(TrashEntry{Provider: ProviderClaude, SessionID: "s1"}, []string{p})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	first := trash("first\n")
	second := trash("second\n")
	if got, _ := LoadTrash(); len(got) != 1 || got[0].Dir != second.Dir {
		t.Fatalf("one entry per session, the newest: %+v", got)
	}
	if !slices.ContainsFunc(trashFiles(t), func(f string) bool { return strings.HasSuffix(f, "first\n") }) {
		t.Fatalf("the earlier files are gone before their purge: %v", trashFiles(t))
	}
	if again := trash(""); again.Dir != second.Dir || len(again.Files) != 1 {
		t.Fatalf("trashing with nothing left to move replaced the entry: %+v (first was %s)", again, first.Dir)
	}
	if _, err := RestoreTrash(ProviderClaude, "s1"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "second\n" {
		t.Fatalf("restored %q", b)
	}
	if n, err := PurgeTrash(1); err != nil || n != 0 || len(trashDirs(t)) != 1 {
		t.Fatalf("a restored entry's earlier files went before their time: %d %v %v", n, err, trashDirs(t))
	}
	if _, err := PurgeTrash(0); err != nil {
		t.Fatal(err)
	}
	if left := trashDirs(t); len(left) != 0 {
		t.Fatalf("purge left files behind: %v", left)
	}
}

func trashFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(TrashDir(), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Base(path) != "manifest.jsonl" && !strings.HasSuffix(path, ".lock") {
			b, _ := os.ReadFile(path)
			out = append(out, path+"\n"+string(b))
		}
		return nil
	})
	return out
}

func TestConcurrentTrashWritersKeepEveryEntry(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	src := t.TempDir()
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		id := fmt.Sprintf("s%02d", i)
		p := filepath.Join(src, id+".jsonl")
		os.WriteFile(p, []byte("{}\n"), 0o644)
		wg.Go(func() {
			_, err := MoveToTrash(TrashEntry{Provider: ProviderCodex, SessionID: id}, []string{p})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := LoadTrash(); len(got) != n {
		t.Fatalf("entries lost to a concurrent writer: %d of %d", len(got), n)
	}
}

func TestMoveFallsBackToCopyAcrossDevices(t *testing.T) {
	rename = func(string, string) error { return errors.New("invalid cross-device link") }
	t.Cleanup(func() { rename = os.Rename })
	src, dst := t.TempDir(), t.TempDir()
	tree := filepath.Join(src, "s1")
	os.MkdirAll(filepath.Join(tree, "sub"), 0o755)
	os.WriteFile(filepath.Join(tree, "sub", "x"), []byte("x"), 0o640)
	file := filepath.Join(src, "s1.jsonl")
	os.WriteFile(file, []byte("{}\n"), 0o600)

	if err := moveAny(tree, filepath.Join(dst, "s1")); err != nil {
		t.Fatal(err)
	}
	if err := moveAny(file, filepath.Join(dst, "s1.jsonl")); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "s1", "sub", "x")); err != nil || string(b) != "x" {
		t.Fatalf("nested file not copied: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "s1.jsonl")); err != nil || string(b) != "{}\n" {
		t.Fatalf("file not copied: %v", err)
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

func TestAFailedMoveToTrashPutsTheFilesBack(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	src := t.TempDir()
	ok, broken := filepath.Join(src, "s1.jsonl"), filepath.Join(src, "s1-link")
	os.WriteFile(ok, []byte("{}\n"), 0o644)
	if err := os.Symlink(filepath.Join(src, "nowhere"), broken); err != nil {
		t.Skip("no symlinks here")
	}
	rename = func(from, to string) error {
		if from == broken {
			return errors.New("cross-device")
		}
		return os.Rename(from, to)
	}
	t.Cleanup(func() { rename = os.Rename })
	if _, err := MoveToTrash(TrashEntry{Provider: ProviderClaude, SessionID: "s1"}, []string{ok, broken}); err == nil {
		t.Fatal("the dangling link cannot be copied")
	}
	if _, err := os.Stat(ok); err != nil {
		t.Fatalf("the file moved before the failure is not back: %v", err)
	}
	if left := trashDirs(t); len(left) != 0 {
		t.Fatalf("an unowned directory stays in the trash: %v", left)
	}
}

func TestPurgeWithNothingDueLeavesTheManifestAlone(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	p := filepath.Join(t.TempDir(), "s1.jsonl")
	os.WriteFile(p, []byte("{}\n"), 0o644)
	if _, err := MoveToTrash(TrashEntry{Provider: ProviderClaude, SessionID: "s1"}, []string{p}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(trashManifest())
	if n, err := PurgeTrash(30); err != nil || n != 0 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if after, _ := os.Stat(trashManifest()); !os.SameFile(before, after) {
		t.Fatal("a purge with nothing due rewrote the manifest")
	}
}
