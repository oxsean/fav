package fav

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tmpStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func reopen(t *testing.T, s *Store) *Store {
	t.Helper()
	s2, err := OpenAt(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	return s2
}

func TestPutOverwritesSameID(t *testing.T) {
	s := tmpStore(t)
	r := rec("原标题", nil)
	if err := s.Put(r); err != nil {
		t.Fatal(err)
	}
	r.Title = "改过的标题"
	if err := s.Put(r); err != nil {
		t.Fatal(err)
	}

	s2 := reopen(t, s)
	if n := len(s2.All()); n != 1 {
		t.Fatalf("want 1 record, got %d", n)
	}
	if got := s2.All()[0].Title; got != "改过的标题" {
		t.Fatalf("want latest title, got %q", got)
	}
	if s2.RawLines() != 2 {
		t.Fatalf("两次 Put 应留下两行物理记录，got %d", s2.RawLines())
	}
}

func TestTombstone(t *testing.T) {
	s := tmpStore(t)
	r := rec("要删的", nil)
	s.Put(r)
	r.Deleted = true
	s.Put(r)

	if n := len(reopen(t, s).All()); n != 0 {
		t.Fatalf("墓碑后不应再出现，got %d", n)
	}
}

func TestCompactKeepsLatestDropsHistory(t *testing.T) {
	s := tmpStore(t)
	keep := rec("留下", nil)
	drop := rec("删掉", nil)
	s.Put(keep)
	for i := 0; i < 5; i++ {
		keep.Title = "留下"
		s.Put(keep)
	}
	s.Put(drop)
	drop.Deleted = true
	s.Put(drop)

	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	s2 := reopen(t, s)
	if n := len(s2.All()); n != 1 || s2.All()[0].Title != "留下" {
		t.Fatalf("compact 后应只剩一条有效记录，got %d 条", n)
	}
	if s2.RawLines() != 1 {
		t.Fatalf("compact 应丢掉历史版本与墓碑，物理行数 got %d", s2.RawLines())
	}
}

func TestBySessionIdempotency(t *testing.T) {
	s := tmpStore(t)
	r := rec("排障", func(x *Rec) { x.SessionID = "abc-123" })
	s.Put(r)

	if got := s.BySession(ProviderClaude, "abc-123"); got == nil || got.ID != r.ID {
		t.Fatal("同 provider + session id 应命中已有记录，否则重复 /fav 会产生重复收藏")
	}
	if s.BySession(ProviderCodex, "abc-123") != nil {
		t.Fatal("provider 不同不应命中")
	}
}

func TestGetByPrefix(t *testing.T) {
	s := tmpStore(t)
	r := rec("排障", nil)
	s.Put(r)
	if got := s.Get(r.ID[:6]); got == nil || got.ID != r.ID {
		t.Fatal("应支持用 id 前缀引用")
	}
	if s.Get("没有这条") != nil {
		t.Fatal("不存在的 id 应返回 nil")
	}
}

func TestSortedByFavoritedAtDesc(t *testing.T) {
	s := tmpStore(t)
	old := rec("旧", func(r *Rec) { r.FavoritedAt = ptr(time.Now().Add(-48 * time.Hour)) })
	fresh := rec("新", func(r *Rec) { r.FavoritedAt = ptr(time.Now()) })
	s.Put(old)
	s.Put(fresh)

	all := reopen(t, s).All()
	if all[0].Title != "新" {
		t.Fatalf("默认应按收藏时间倒序，got %q 在前", all[0].Title)
	}
}

func TestCorruptLineIsSkipped(t *testing.T) {
	s := tmpStore(t)
	s.Put(rec("好记录", nil))
	f, _ := os.OpenFile(s.Path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("{这不是 json\n")
	f.Close()

	s2, err := OpenAt(s.Path)
	if err != nil {
		t.Fatalf("坏行不应让 Open 失败: %v", err)
	}
	if len(s2.All()) != 1 {
		t.Fatalf("好记录应还在，got %d", len(s2.All()))
	}
}

func TestMissingFileIsEmptyNotError(t *testing.T) {
	s, err := OpenAt(filepath.Join(t.TempDir(), "nope", "records.jsonl"))
	if err != nil {
		t.Fatalf("首次使用时文件还不存在，不应报错: %v", err)
	}
	if len(s.All()) != 0 {
		t.Fatal("want empty")
	}
}

func TestChangedOnlyByOthers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	s, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(&Rec{ID: "a", Title: "a"}); err != nil {
		t.Fatal(err)
	}
	if s.Changed() {
		t.Fatal("自己 Put 之后不该报 Changed")
	}
	other, _ := OpenAt(path)
	time.Sleep(20 * time.Millisecond) // bump mtime
	if err := other.Put(&Rec{ID: "b", Title: "b"}); err != nil {
		t.Fatal(err)
	}
	if !s.Changed() {
		t.Fatal("别的进程写过应报 Changed")
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if s.Changed() || s.Get("b") == nil || len(s.All()) != 2 {
		t.Fatalf("Reload 之后：changed=%v n=%d", s.Changed(), len(s.All()))
	}
}

func TestUnknownStatusFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "records.jsonl")
	if err := os.WriteFile(path, []byte(`{"id":"x","title":"t","status":"whatever"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.All(); len(got) != 1 || got[0].Status != StatusDefault {
		t.Fatalf("got %+v", got)
	}
}

// Compact reloads first: rows another process appended after our Put must survive.
func TestCompactKeepsOtherWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	s, _ := OpenAt(path)
	other, _ := OpenAt(path)
	if err := other.Put(&Rec{ID: "b", Title: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(&Rec{ID: "a", Title: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	again, _ := OpenAt(path)
	if len(again.All()) != 2 {
		t.Fatalf("压实后应两条都在：%d", len(again.All()))
	}
}
