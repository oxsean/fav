package main

import (
	"path/filepath"
	"testing"

	"github.com/oxsean/fav/internal/tend"
)

func TestPickBySessionPrefix(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TEND_HOME", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(dir, "codex"))
	s, err := tend.OpenAt(filepath.Join(dir, "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	a := &tend.Rec{ID: tend.NewID(), Provider: tend.ProviderClaude, SessionID: "0123abcd-1", Title: "a"}
	b := &tend.Rec{ID: tend.NewID(), Provider: tend.ProviderClaude, SessionID: "0123ffff-2", Title: "b"}
	for _, r := range []*tend.Rec{a, b} {
		if err := s.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	if r, err := pick(s, a.ID); err != nil || r.Title != "a" {
		t.Fatalf("by record id: %v %v", r, err)
	}
	if r, err := pick(s, "0123ab"); err != nil || r.Title != "a" {
		t.Fatalf("by session prefix: %v %v", r, err)
	}
	if _, err := pick(s, "0123"); err == nil {
		t.Fatal("ambiguous prefix should fail")
	}
	if _, err := pick(s, "zzzz"); err == nil {
		t.Fatal("unknown id should fail")
	}
}
