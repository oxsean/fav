package remote

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	for _, m := range []string{MMemoryList, MMemoryRead, MMemoryTrash, MMemoryRestore, MMemoryPut} {
		if !slices.Contains(hello.Methods, m) {
			t.Errorf("hello lacks %s", m)
		}
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

// memory.put writes a new memory with its index line, leaves the same text, puts a different one under .incoming/,
// and refuses when what is there is not what the caller saw, Codex's memories, and names outside a memory directory.
func TestMemoryPutOnTheFixture(t *testing.T) {
	d, c := localMachine(t)
	ctx := context.Background()
	webapp := filepath.Join(d.Work, "webapp")
	mem := memory.ClaudeDir(webapp)
	deploy, err := os.ReadFile(filepath.Join(mem, "deploy.md"))
	if err != nil {
		t.Fatal(err)
	}
	put := func(p MemoryPutParams) (MemoryPut, error) {
		var res MemoryPut
		err := c.Call(ctx, MMemoryPut, p, &res)
		return res, err
	}

	res, err := put(MemoryPutParams{Dir: webapp, Kind: memory.KindClaude, Name: "new.md", Text: "new\n", Line: "- [New](new.md) — added"})
	if err != nil || res.File != filepath.Join(mem, "new.md") || res.Incoming || res.Lines != 3 || res.Over {
		t.Fatalf("new: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(mem, "MEMORY.md")); !strings.HasSuffix(string(b), "- [New](new.md) — added\n") {
		t.Errorf("index: %s", b)
	}
	var l MemoryList
	if err := c.Call(ctx, MMemoryList, MemoryListParams{Dirs: []string{webapp}}, &l); err != nil {
		t.Fatal(err)
	}
	var deploySHA string
	for _, it := range l.Sets[0].Items {
		if filepath.Base(it.File) == "deploy.md" {
			deploySHA = it.SHA
		}
	}
	if res, err := put(MemoryPutParams{Dir: webapp, Kind: memory.KindClaude, Name: "deploy.md", Text: string(deploy), Expect: deploySHA}); err != nil || res.Incoming {
		t.Errorf("the same text: %+v %v", res, err)
	}
	res, err = put(MemoryPutParams{Dir: webapp, Kind: memory.KindClaude, Name: "deploy.md", Text: "theirs\n", Line: "- [Deploy](deploy.md) — theirs", Expect: deploySHA})
	if err != nil || !res.Incoming || res.File != filepath.Join(mem, ".incoming", "deploy.md") {
		t.Fatalf("different: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(mem, "deploy.md")); string(b) != string(deploy) {
		t.Error("overwrote")
	}
	for _, p := range []MemoryPutParams{
		{Dir: webapp, Kind: memory.KindClaude, Name: "deploy.md", Text: "x\n"},
		{Dir: webapp, Kind: memory.KindClaude, Name: "gone.md", Text: "x\n", Expect: deploySHA},
	} {
		if _, err := put(p); code(err) != wire.CodeStale {
			t.Errorf("%s expecting %q: %v", p.Name, p.Expect, err)
		}
	}
	for _, p := range []MemoryPutParams{
		{Dir: webapp, Kind: memory.KindCodexGlobal, Name: "x.md", Text: "x\n"},
		{Dir: webapp, Kind: "codex", Name: "x.md", Text: "x\n"},
		{Dir: webapp, Kind: memory.KindClaude, Name: "../x.md", Text: "x\n"},
		{Dir: "webapp", Kind: memory.KindClaude, Name: "x.md", Text: "x\n"},
		{Dir: webapp, Kind: memory.KindClaude, Name: "x.md", Text: "x\n", Line: "- [X](x.md)\n- [Y](y.md)"},
	} {
		if _, err := put(p); code(err) != wire.CodeBadRequest {
			t.Errorf("%+v: %v", p, err)
		}
	}
}

// Two ends compared through their Peers: one project directory each, the second end empty; what a copy writes there
// and the guard against a change in between.
func TestCompareAndCopyMemoriesBetweenPeers(t *testing.T) {
	d, _ := localMachine(t)
	ctx := context.Background()
	h := NewLocal("test")
	a, b := PeerOf("a", LocalHello("test"), InProcess(h)), PeerOf("b", LocalHello("test"), InProcess(h))
	webapp, notes := filepath.Join(d.Work, "webapp"), filepath.Join(d.Work, "notes-api")
	pair := DirPair{From: webapp, To: notes}

	got, err := CompareMemories(ctx, a, b, []DirPair{pair})
	if err != nil || len(got) != 1 {
		t.Fatalf("compare: %+v %v", got, err)
	}
	c := got[0].Diff
	var only []string
	for _, e := range c.OnlyHere {
		only = append(only, e.Kind+":"+e.Name)
	}
	if !slices.Contains(only, "claude:deploy.md") || !slices.Contains(only, "claude:oauth-state.md") || len(c.OnlyThere) != 0 || len(c.Differ) != 0 {
		t.Fatalf("compare: %+v", c)
	}
	if !slices.ContainsFunc(c.Same, func(e memory.Entry) bool { return e.Kind == memory.KindCodexGlobal && e.Name == "release workflow" }) {
		t.Errorf("Codex's block naming no directory is on both ends: %+v", c.Same)
	}

	i := slices.IndexFunc(c.OnlyHere, func(e memory.Entry) bool { return e.Name == "deploy.md" })
	res, err := CopyMemory(ctx, a, b, got[0], c.OnlyHere[i])
	if err != nil || res.Incoming || res.File != filepath.Join(memory.ClaudeDir(notes), "deploy.md") || res.Lines != 1 {
		t.Fatalf("copy: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(memory.ClaudeDir(notes), "MEMORY.md")); string(b) != "- [Deploy steps](deploy.md) — how staging is deployed\n" {
		t.Errorf("the line comes along: %q", b)
	}
	if _, err := CopyMemory(ctx, a, b, got[0], c.OnlyHere[i]); code(err) != wire.CodeStale {
		t.Errorf("copied again from a stale comparison: %v", err)
	}
	if j := slices.IndexFunc(c.Same, func(e memory.Entry) bool { return e.Kind == memory.KindCodexGlobal }); j >= 0 {
		if _, err := CopyMemory(ctx, a, b, got[0], c.Same[j]); code(err) != wire.CodeBadRequest {
			t.Errorf("Codex's memories are not copied: %v", err)
		}
	}

	old := PeerOf("old", Hello{Version: "v0.0.1", Methods: []string{MMemoryList, MMemoryRead}}, InProcess(h))
	if _, err := CopyMemory(ctx, a, old, got[0], c.OnlyHere[i]); code(err) != wire.CodeUnknownMethod {
		t.Errorf("an older end is not sent memory.put: %v", err)
	}
}
