package remote

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/memory"
	"github.com/oxsean/fav/internal/wire"
)

func TestMemoryMethodsOnTheFixture(t *testing.T) {
	d, c := localMachine(t)
	ctx := context.Background()
	webapp := filepath.Join(d.Work, "webapp")
	var hello Hello
	if err := c.Call(ctx, MHello, HelloParams{}, &hello); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{MMemoryList, MMemoryRead, MMemoryTrash, MMemoryRestore} {
		if !slices.Contains(hello.Methods, m) {
			t.Errorf("hello lacks %s", m)
		}
	}
	if slices.Contains(hello.Methods, MMemoryPut) {
		t.Error("memory.put is not answered yet")
	}

	var l MemoryList
	if err := c.Call(ctx, MMemoryList, MemoryListParams{Dirs: []string{webapp}, Global: true}, &l); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(l.Sets, func(s memory.Set) bool { return s.Kind == memory.KindClaude })
	if i < 0 || len(l.Sets[i].Items) != 2 || !slices.ContainsFunc(l.Sets, func(s memory.Set) bool { return s.Kind == memory.KindCodexGlobal }) {
		t.Fatalf("sets: %+v", l.Sets)
	}
	claude := l.Sets[i]
	file := claude.Items[0].File

	var text MemoryText
	if err := c.Call(ctx, MMemoryRead, MemoryFile{File: file}, &text); err != nil || text.Text == "" || text.SHA != claude.Items[0].SHA {
		t.Fatalf("read: %+v %v", text, err)
	}
	for _, p := range []string{filepath.Join(d.Claude, ".claude.json"), filepath.Join(d.Home, "records.jsonl"), d.Get("oauth").Path,
		filepath.Join(claude.Dir, "nothing.md")} {
		if err := c.Call(ctx, MMemoryRead, MemoryFile{File: p}, &text); code(err) != wire.CodeUnauthorized {
			t.Errorf("read %s: %v", p, err)
		}
	}

	var e MemoryEntry
	if err := c.Call(ctx, MMemoryTrash, MemoryFile{File: file}, &e); err != nil || e.Entry == "" {
		t.Fatalf("trash: %+v %v", e, err)
	}
	if err := c.Call(ctx, MMemoryList, MemoryListParams{Dirs: []string{webapp}}, &l); err != nil || len(l.Sets) != 1 || len(l.Sets[0].Items) != 1 {
		t.Errorf("after trash: %+v %v", l.Sets, err)
	}
	var back MemoryFile
	if err := c.Call(ctx, MMemoryRestore, e, &back); err != nil || back.File != file {
		t.Fatalf("restore: %+v %v", back, err)
	}
	if err := c.Call(ctx, MMemoryRestore, e, &back); code(err) != wire.CodeConflict {
		t.Errorf("restore twice: %v", err)
	}
	if err := c.Call(ctx, MMemoryTrash, MemoryFile{File: filepath.Join(d.Codex, "memories", "MEMORY.md")}, &e); code(err) != wire.CodeUnauthorized {
		t.Errorf("Codex memories stay: %v", err)
	}
}
