package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/memory"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// memoryHomes points Claude and Codex at fresh homes and writes dir's memories there: a Claude index with two entries
// (one with front matter) and a third file it does not list, and Codex's global memory with a block applying inside
// dir and one naming no directory.
func memoryHomes(t *testing.T, dir string) (mem string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	mem = filepath.Join(index.ClaudeProjectDir(dir), "memory")
	files := map[string]string{
		filepath.Join(mem, "MEMORY.md"): "- [Deploy steps](deploy.md) — how staging is deployed\n- [OAuth state](oauth.md) — decoded once\n",
		filepath.Join(mem, "deploy.md"): "---\nname: deploy-steps\ndescription: Staging deploys from main\n---\n\nRun the release script; never from a branch.\n",
		filepath.Join(mem, "oauth.md"):  "The callback decodes state once.\n",
		filepath.Join(mem, "stray.md"):  "Not in the index.\n",
		filepath.Join(root, "codex", "memories", "MEMORY.md"): "# Task Group: callback\nscope: the login callback\napplies_to: cwd=" +
			filepath.Join(dir, "src") + "; reuse_rule=x\n\n# Task Group: releases\nscope: how releases are cut\napplies_to: cwd=any checkout\n",
	}
	for p, body := range files {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return mem
}

// memoryModel: the projects view on the fixture, its sessions' directory holding memoryHomes' memories, the cursor on
// a group heading and what the project block reads for it read.
func memoryModel(t *testing.T) (*Model, string) {
	t.Helper()
	m := sized(t, 140, 40)
	dir := m.current().Cwd
	mem := memoryHomes(t, dir)
	m.setView(viewProjects)
	m.foldAll(nil)
	m.cursor = slices.IndexFunc(m.rows, func(r row) bool { return r.group != "" })
	pump(m, m.memoryHere())
	waitFor(t, m, func() bool { v := m.mems[m.groupUnderCursor()]; return v != nil && !v.busy() })
	return m, mem
}

func TestProjectBlockCountsMemories(t *testing.T) {
	m, _ := memoryModel(t)
	s := screenText(m)
	want := i18n.F("memory.counts", 3, 1)
	if !strings.Contains(s, want) || !strings.Contains(s, i18n.T("memory.label")) {
		t.Fatalf("the project block counts 3 Claude memories and the 1 Codex block for the directory (%q):\n%s", want, s)
	}
	if strings.Contains(s, i18n.T("memory.over")) {
		t.Errorf("an index under the load limit is not flagged:\n%s", s)
	}
	if !strings.Contains(s, footKeyOf(inList, actMemory)+" "+i18n.T("footer.memory")) {
		t.Errorf("the footer offers the memory key on a group heading:\n%s", s)
	}
	checkWidth(t, m)
}

func TestProjectBlockFlagsAnIndexOverTheLoadLimit(t *testing.T) {
	m := sized(t, 140, 40)
	dir := m.current().Cwd
	mem := memoryHomes(t, dir)
	os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte(strings.Repeat("- a line\n", 201)), 0o644)
	m.setView(viewProjects)
	m.foldAll(nil)
	m.cursor = slices.IndexFunc(m.rows, func(r row) bool { return r.group != "" })
	pump(m, m.memoryHere())
	waitFor(t, m, func() bool { v := m.mems[m.groupUnderCursor()]; return v != nil && !v.busy() })
	if s := screenText(m); !strings.Contains(s, i18n.T("memory.over")) {
		t.Fatalf("201 lines are over what Claude loads:\n%s", s)
	}
}

// TestMemoryOverlayOnThisMachine: i lists the group's memories by kind, Enter reads one in full through the reader,
// D confirmed puts it in the trash with its index line, u puts both back.
func TestMemoryOverlayOnThisMachine(t *testing.T) {
	m, mem := memoryModel(t)
	key(m, "i")
	waitFor(t, m, func() bool { return m.ov.kind == ovMemory && !m.mems[m.ov.mem.key].busy() })
	s := screenText(m)
	for _, want := range []string{i18n.T("project.this_machine"), i18n.F("memory.set_claude", 3, 2), "deploy-steps", "Staging deploys from main",
		"OAuth state", "decoded once", i18n.T("memory.not_indexed"), i18n.F("memory.set_codex", 1), "callback", i18n.F("memory.set_loose", 1),
		"releases", "Enter " + i18n.T("memory.btn_read"), "D " + i18n.T("memory.btn_trash")} {
		if !strings.Contains(s, want) {
			t.Errorf("the overlay lacks %q:\n%s", want, s)
		}
	}
	checkWidth(t, m)

	for m.ov.mem.item().Title != "OAuth state" {
		key(m, "j")
	}
	key(m, "enter")
	waitFor(t, m, func() bool { return m.ov.mem.read != nil && !m.ov.mem.read.loading })
	if s := screenText(m); !strings.Contains(s, "The callback decodes state once.") || !strings.Contains(s, "oauth.md") {
		t.Fatalf("Enter reads the memory in full:\n%s", s)
	}
	checkWidth(t, m)
	key(m, "tab")
	waitFor(t, m, func() bool { return m.ov.mem.read != nil && !m.ov.mem.read.loading })
	if m.ov.mem.read.item.Title == "OAuth state" {
		t.Errorf("Tab reads the next memory")
	}
	key(m, "shift+tab")
	waitFor(t, m, func() bool { return m.ov.mem.read != nil && !m.ov.mem.read.loading })
	key(m, "esc")
	if m.ov.kind != ovMemory || m.ov.mem.read != nil {
		t.Fatalf("Esc goes back to the list: %d", m.ov.kind)
	}

	key(m, "D")
	if m.ov.kind != ovConfirm {
		t.Fatalf("D asks first: %d", m.ov.kind)
	}
	key(m, "esc")
	if m.ov.kind != ovMemory {
		t.Fatalf("cancelling goes back to the list: %d", m.ov.kind)
	}
	key(m, "D")
	key(m, "y")
	waitFor(t, m, func() bool {
		return m.ov.kind == ovMemory && !m.mems[m.ov.mem.key].busy() && strings.Contains(m.notice, "OAuth state")
	})
	if _, err := os.Stat(filepath.Join(mem, "oauth.md")); !os.IsNotExist(err) {
		t.Fatalf("the memory went to the trash: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(mem, "MEMORY.md")); strings.Contains(string(b), "oauth.md") {
		t.Errorf("its index line went with it:\n%s", b)
	}
	s = screenText(m)
	if strings.Contains(s, "OAuth state —") || !strings.Contains(s, i18n.F("memory.set_claude", 2, 1)) {
		t.Errorf("the list is read again without it:\n%s", s)
	}
	if !strings.Contains(s, i18n.F("undo.offer", i18n.F("memory.trashed", "OAuth state"), keyOf(inList, actUndo))) {
		t.Errorf("the notice offers the undo:\n%s", s)
	}
	checkWidth(t, m)

	key(m, "u")
	waitFor(t, m, func() bool {
		return strings.Contains(m.notice, i18n.F("undo.done", "OAuth state")) && !m.mems[m.ov.mem.key].busy()
	})
	if _, err := os.Stat(filepath.Join(mem, "oauth.md")); err != nil {
		t.Fatalf("u puts the memory back: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(mem, "MEMORY.md")); !strings.Contains(string(b), "(oauth.md)") {
		t.Errorf("and its index line:\n%s", b)
	}
	if s := screenText(m); !strings.Contains(s, "OAuth state") {
		t.Errorf("the list shows it again:\n%s", s)
	}
}

func TestCodexMemoriesAreReadOnly(t *testing.T) {
	m, _ := memoryModel(t)
	key(m, "i")
	waitFor(t, m, func() bool { return m.ov.kind == ovMemory && !m.mems[m.ov.mem.key].busy() })
	for m.ov.mem.item().Kind != memory.KindCodexGlobal {
		key(m, "j")
	}
	key(m, "D")
	if m.ov.kind != ovMemory || m.notice != i18n.T("memory.codex_read_only") {
		t.Fatalf("D on a Codex block says it is read only: %d %q", m.ov.kind, m.notice)
	}
	key(m, "enter")
	waitFor(t, m, func() bool { return m.ov.mem.read != nil && !m.ov.mem.read.loading })
	if s := screenText(m); !strings.Contains(s, "scope: the login callback") || strings.Contains(s, "how releases are cut") {
		t.Fatalf("a Codex block is read alone, cut out of its file:\n%s", s)
	}
}

// The palette and the IME route reach the overlay without the letter.
func TestMemoryKeyIsInThePalette(t *testing.T) {
	m, _ := memoryModel(t)
	key(m, ":")
	typeText(m, "记忆")
	key(m, "enter")
	waitFor(t, m, func() bool { return m.ov.kind == ovMemory })
	if b := bindingOf(inList, actMemory); b == nil || b.ime != actPalette {
		t.Errorf("the memory key's IME route is the palette: %+v", b)
	}
}

func TestMemoryLineIsClickable(t *testing.T) {
	m, _ := memoryModel(t)
	clickText(t, m, i18n.F("memory.counts", 3, 1))
	if m.ov.kind != ovMemory {
		t.Fatalf("a click on the memory line opens the overlay:\n%s", screenText(m))
	}
}

// memNode answers the memory methods as another machine's tend does, over its own directories.
type memNode struct {
	mu       sync.Mutex
	sets     []memory.Set
	texts    map[string]string
	calls    []string
	trashed  map[string]memory.Item // entry → the memory
	listDirs [][]string
	puts     []remote.MemoryPutParams
	over     bool // MEMORY.md is over the load limit after a put
}

const shopDir = "/home/u/dev/shop"

func newMemNode() *memNode {
	at := time.Now().Add(-72 * time.Hour)
	return &memNode{
		sets: []memory.Set{{Kind: memory.KindClaude, Dir: "/home/u/.claude/projects/-home-u-dev-shop/memory", Lines: 1, Bytes: 40,
			Items: []memory.Item{{File: "/home/u/.claude/projects/-home-u-dev-shop/memory/cart.md", Title: "cart totals", Description: "rounded once, at the end",
				At: at, Size: 30, InIndex: true}}},
			{Kind: memory.KindCodexGlobal, Dir: shopDir, Items: []memory.Item{}}},
		texts:   map[string]string{"/home/u/.claude/projects/-home-u-dev-shop/memory/cart.md": "Round the cart total once.\n"},
		trashed: map[string]memory.Item{},
	}
}

func (n *memNode) handle(method string, params json.RawMessage) (any, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, method)
	switch method {
	case remote.MMemoryList:
		var p remote.MemoryListParams
		json.Unmarshal(params, &p)
		n.listDirs = append(n.listDirs, p.Dirs)
		return remote.MemoryList{Sets: n.sets}, nil
	case remote.MMemoryRead:
		var p remote.MemoryFile
		json.Unmarshal(params, &p)
		text, ok := n.texts[p.File]
		if !ok {
			return nil, &wire.Error{Code: wire.CodeUnauthorized, Detail: "memory"}
		}
		return remote.MemoryText{Text: text}, nil
	case remote.MMemoryTrash:
		var p remote.MemoryFile
		json.Unmarshal(params, &p)
		items := n.sets[0].Items
		i := slices.IndexFunc(items, func(it memory.Item) bool { return it.File == p.File })
		if i < 0 {
			return nil, &wire.Error{Code: wire.CodeUnauthorized, Detail: "memory"}
		}
		n.trashed["e1"] = items[i]
		n.sets[0].Items = slices.Delete(slices.Clone(items), i, i+1)
		return remote.MemoryEntry{Entry: "e1"}, nil
	case remote.MMemoryRestore:
		var p remote.MemoryEntry
		json.Unmarshal(params, &p)
		it, ok := n.trashed[p.Entry]
		if !ok {
			return nil, &wire.Error{Code: wire.CodeConflict}
		}
		delete(n.trashed, p.Entry)
		n.sets[0].Items = append(n.sets[0].Items, it)
		return remote.MemoryFile{File: it.File}, nil
	case remote.MMemoryPut:
		return n.put(params)
	}
	return nil, &wire.Error{Code: wire.CodeUnknownMethod}
}

func (n *memNode) called() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.calls)
}

var memMethods = []string{remote.MMemoryList, remote.MMemoryRead, remote.MMemoryTrash, remote.MMemoryRestore}

// memoryRig is handoffRig with mba answering memory.*, the cursor on mba's session in the sessions view.
func memoryRig(t *testing.T, tr string, set func(*handoffRig)) (*handoffRig, *memNode) {
	t.Helper()
	n := newMemNode()
	g := newHandoffRig(t, tr, func(g *handoffRig) {
		g.mba.mem = n
		g.mba.hello.Methods = append(g.mba.hello.Methods, memMethods...)
		if set != nil {
			set(g)
		}
	})
	m := g.m
	if !g.offline {
		fetchNow(m, "mba")
	}
	m.search.SetValue("host:mba")
	m.refresh()
	m.cursor = slices.IndexFunc(m.rows, func(r row) bool { return r.rec != nil && r.rec.Host == "mba" })
	if m.cursor < 0 {
		m.cursor = 0
		m.remote["mba"].merge([]*tend.Rec{g.mba.sessions[0].Rec("mba")})
		m.refresh()
		m.cursor = slices.IndexFunc(m.rows, func(r row) bool { return r.rec != nil && r.rec.Host == "mba" })
	}
	return g, n
}

// TestMemoryOverlayOnAnotherMachine: over ssh and over the server's node.call, a session of mba lists mba's memories
// of its directory through memory.ls there, reads one through memory.read, and trashes and restores it there.
func TestMemoryOverlayOnAnotherMachine(t *testing.T) {
	for _, tr := range handoffTransports {
		t.Run(tr, func(t *testing.T) {
			g, n := memoryRig(t, tr, nil)
			m := g.m
			key(m, "i")
			waitFor(t, m, func() bool { return m.ov.kind == ovMemory && !m.mems[m.ov.mem.key].busy() })
			s := screenText(m)
			for _, want := range []string{"mba", shopDir, "cart totals", "rounded once, at the end", i18n.F("memory.set_claude", 1, 1)} {
				if !strings.Contains(s, want) {
					t.Errorf("the overlay lacks %q:\n%s", want, s)
				}
			}
			if len(n.listDirs) == 0 || !slices.Equal(n.listDirs[len(n.listDirs)-1], []string{shopDir}) {
				t.Errorf("memory.ls asked mba for the session's directory: %v", n.listDirs)
			}
			checkWidth(t, m)
			key(m, "enter")
			waitFor(t, m, func() bool { return m.ov.mem.read != nil && !m.ov.mem.read.loading })
			if s := screenText(m); !strings.Contains(s, "Round the cart total once.") {
				t.Fatalf("mba's memory is read there:\n%s", s)
			}
			key(m, "q")
			key(m, "D")
			key(m, "y")
			waitFor(t, m, func() bool { return strings.Contains(m.notice, i18n.F("memory.trashed", "cart totals")) })
			waitFor(t, m, func() bool { return !m.mems[m.ov.mem.key].busy() })
			if s := screenText(m); strings.Contains(s, "cart totals —") {
				t.Errorf("the trashed memory left the list:\n%s", s)
			}
			key(m, "u")
			waitFor(t, m, func() bool { return strings.Contains(m.notice, i18n.F("undo.done", "cart totals")) })
			if c := n.called(); !slices.Contains(c, remote.MMemoryTrash) || !slices.Contains(c, remote.MMemoryRestore) {
				t.Errorf("trash and restore went to mba: %v", c)
			}
		})
	}
}

func TestMemoryOverlayOnAnOfflineMachine(t *testing.T) {
	g, n := memoryRig(t, "ssh", func(g *handoffRig) { g.offline = true })
	m := g.m
	m.remote["mba"].err = &wire.Error{Code: wire.CodeOffline}
	key(m, "i")
	waitFor(t, m, func() bool { return m.ov.kind == ovMemory && !m.mems[m.ov.mem.key].busy() })
	if s := screenText(m); !strings.Contains(s, i18n.F("remote.unreachable", "mba", remote.Reason(&wire.Error{Code: wire.CodeOffline}))) {
		t.Fatalf("an offline machine says so in the overlay:\n%s", s)
	}
	if c := n.called(); len(c) > 0 {
		t.Errorf("nothing was sent: %v", c)
	}
	checkWidth(t, m)
}

// A tend without memory.ls is not asked, and says it is too old, as `tend memory host:` says.
func TestMemoryOverlayOnAnOldMachine(t *testing.T) {
	for _, tr := range handoffTransports {
		t.Run(tr, func(t *testing.T) {
			g, n := memoryRig(t, tr, func(g *handoffRig) {
				g.mba.hello.Methods = slices.DeleteFunc(g.mba.hello.Methods, func(s string) bool { return strings.HasPrefix(s, "memory.") })
			})
			m := g.m
			key(m, "i")
			waitFor(t, m, func() bool { return m.ov.kind == ovMemory && !m.mems[m.ov.mem.key].busy() })
			if s := screenText(m); !strings.Contains(stripped(s), stripped(remote.MemoryRefused("mba", &wire.Error{Code: wire.CodeUnknownMethod}))) {
				t.Fatalf("an old tend is named with its update command:\n%s", s)
			}
			if c := n.called(); len(c) > 0 {
				t.Errorf("an old tend is not sent memory methods: %v", c)
			}
		})
	}
}

// Mode 2: another person's machine keeps its memories to its owner; the TUI does not ask.
func TestMemoryOverlayOnAnotherPersonsMachine(t *testing.T) {
	g, _ := memoryRig(t, "node.call", nil)
	m := g.m
	m.setMachines([]remote.Machine{{Name: "mba"}, {Name: "bobs"}})
	key(m, "i")
	waitFor(t, m, func() bool { return m.ov.kind == ovMemory && !m.mems[m.ov.mem.key].busy() })
	if s := screenText(m); !strings.Contains(s, i18n.F("memory.not_mine", "mba")) {
		t.Fatalf("a machine shared with the viewer keeps its memories:\n%s", s)
	}
}

// dumpT is TestDumpFrame's test, for the frames that write files.
var dumpT *testing.T

// dumpMemory: the demo project on this machine, mba (answering memory.*) and win (offline), the cursor on its heading;
// then the overlay, a memory read, or one deleted with the undo offered; or the comparison with mba, one of its
// memories read on both sides, or two copied to mba (one new, one into .incoming/).
func dumpMemory(m *Model, kind string) {
	t := dumpT
	n := newHandoffNode("mba", "linux", "/home/u")
	n.mem = newMemNode()
	n.hello.Methods = append(n.hello.Methods, memMethods...)
	compare := strings.HasPrefix(kind, "memory-co")
	if compare {
		n.hello.Methods = append(n.hello.Methods, remote.MMemoryPut)
	}
	m.useHosts(remote.NewHostsDial([]tend.Host{{Name: "mba"}, {Name: "win"}}, i18n.ZH, func(h tend.Host) (*remote.Client, error) {
		if h.Name == "mba" {
			return remote.Pipe(n), nil
		}
		return nil, &wire.Error{Code: wire.CodeOffline}
	}))
	fetchNow(m, "mba")
	m.remote["win"].err = &wire.Error{Code: wire.CodeOffline}
	demoProject(m)
	memoryHomes(t, m.proj.snap.Projects["p_demo"].Repos[0].Dirs["local"])
	if compare {
		dir := m.proj.snap.Projects["p_demo"].Repos[0].Dirs["mba"]
		mem := "/home/u/.claude/projects/" + strings.ReplaceAll(dir, "/", "-") + "/memory"
		n.mem = cmpNode(mem, dir, map[string]string{
			"deploy.md": "---\nname: deploy-steps\ndescription: Staging deploys from main\n---\n\nRun the release script; never from a branch.\n",
			"oauth.md":  "The callback decodes state twice.\n", "cart.md": "Round the cart total once.\n"},
			"- [Deploy steps](deploy.md) — how staging is deployed\n- [OAuth state](oauth.md)\n- [Cart totals](cart.md)\n", "")
		n.mem.sets[0].Incoming = []memory.Item{{File: mem + "/.incoming/notes.md", Title: "release notes (from win)", At: time.Now().Add(-26 * time.Hour)}}
		n.mem.texts[mem+"/.incoming/notes.md"] = "Release notes are written by hand.\n"
	}
	m.cursor = slices.IndexFunc(m.rows, func(r row) bool { return groupProject(r.group) == "p_demo" })
	pump(m, m.memoryHere())
	waitFor(t, m, func() bool { return !m.mems[m.groupUnderCursor()].busy() })
	if kind == "memory-block" {
		return
	}
	key(m, "i")
	waitFor(t, m, func() bool { return m.ov.kind == ovMemory && !m.ov.mem.view.busy() })
	switch kind {
	case "memory-read":
		key(m, "enter")
		waitFor(t, m, func() bool { return !m.ov.mem.read.loading })
	case "memory-trashed":
		key(m, "j")
		key(m, "D")
		key(m, "y")
		waitFor(t, m, func() bool { return m.undo != nil && !m.ov.mem.view.busy() })
	case "memory-compare", "memory-compare-read", "memory-copied":
		m.askCompare()
		key(m, "enter")
		waitCompared(t, m)
		cmpGoTo(t, m, cmpDiffer, "oauth.md")
		switch kind {
		case "memory-compare-read":
			key(m, "enter")
			waitFor(t, m, func() bool { r := m.ov.mcmp.read; return !r.loading[0] && !r.loading[1] })
			key(m, "tab")
		case "memory-copied":
			key(m, "x")
			cmpGoTo(t, m, cmpOnlyHere, "stray.md")
			key(m, "x")
			clickText(t, m, i18n.F("memory.btn_copy_to", "mba"))
			waitCompared(t, m)
		default:
			cmpGoTo(t, m, cmpOnlyHere, "stray.md")
			key(m, "x")
		}
	}
}

// stripped is s without the backticks a message may wrap a command in, and without styling.
func stripped(s string) string { return strings.ReplaceAll(s, "`", "") }
