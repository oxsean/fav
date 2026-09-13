package fav

import (
	"os"
	"path/filepath"
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
	for i := range entries {
		if entries[i].SessionID == "old" {
			entries[i].DeletedAt = time.Now().AddDate(0, 0, -40)
		}
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
	if _, err := os.Stat(filepath.Join(TrashDir(), ProviderCodex, "old")); !os.IsNotExist(err) {
		t.Fatal("过期条目的目录应已删除")
	}
	if n, _ := PurgeTrash(0); n != 1 {
		t.Fatalf("days=0 应全清：%d", n)
	}
}
