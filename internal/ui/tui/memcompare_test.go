package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/memory"
	"github.com/oxsean/fav/internal/projects"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

func memSHA(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// cmpNode is a machine's Claude memories of shop in memDir, by file name, with the index MEMORY.md lists them in, and a
// Codex block applying to dir.
func cmpNode(memDir, dir string, files map[string]string, index, codex string) *memNode {
	n := &memNode{texts: map[string]string{}, trashed: map[string]memory.Item{}}
	s := memory.Set{Kind: memory.KindClaude, Dir: memDir, Index: memDir + "/MEMORY.md", Items: []memory.Item{},
		Lines: strings.Count(index, "\n"), Bytes: int64(len(index))}
	n.texts[s.Index] = index
	at := time.Now().Add(-72 * time.Hour)
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range files {
			if !yield(k) {
				return
			}
		}
	}) {
		text := files[name]
		it := memory.Item{File: memDir + "/" + name, Title: strings.TrimSuffix(name, ".md"), At: at, Size: int64(len(text)),
			SHA: memSHA(text), Norm: memSHA(text), InIndex: strings.Contains(index, "("+name+")")}
		n.texts[it.File] = text
		s.Items = append(s.Items, it)
	}
	c := memory.Set{Kind: memory.KindCodexGlobal, Dir: dir, Index: memDir + "/codex.md", Items: []memory.Item{}}
	if codex != "" {
		c.Items = append(c.Items, memory.Item{File: c.Index, Title: codex, Description: "the login callback", At: at, Line: 1,
			SHA: memSHA(codex), Norm: memSHA(codex)})
		n.texts[c.Index] = "# Task Group: " + codex + "\nscope: the login callback\n"
	}
	n.sets = []memory.Set{s, c}
	return n
}

// put is memory.put as a node answers it: the expected hash or stale; new written and indexed, the same left, another
// under .incoming/.
func (n *memNode) put(params json.RawMessage) (any, error) {
	var p remote.MemoryPutParams
	json.Unmarshal(params, &p)
	n.puts = append(n.puts, p)
	if p.Kind != memory.KindClaude {
		return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "kind"}
	}
	s := &n.sets[0]
	file := s.Dir + "/" + p.Name
	i := slices.IndexFunc(s.Items, func(it memory.Item) bool { return it.File == file })
	cur := ""
	if i >= 0 {
		cur = s.Items[i].SHA
	}
	if p.Expect != cur {
		return nil, &wire.Error{Code: wire.CodeStale, Detail: p.Name}
	}
	it := memory.Item{File: file, Title: strings.TrimSuffix(p.Name, ".md"), At: time.Now(), SHA: memSHA(p.Text), Norm: memSHA(p.Text)}
	res := remote.MemoryPut{File: file}
	switch {
	case i < 0:
		it.InIndex = p.Line != ""
		s.Items = append(s.Items, it)
		n.texts[file] = p.Text
		if p.Line != "" {
			n.texts[s.Index] += p.Line + "\n"
			s.Lines++
		}
	case cur != it.SHA:
		it.File = s.Dir + "/.incoming/" + p.Name
		s.Incoming = append(s.Incoming, it)
		n.texts[it.File] = p.Text
		res.File, res.Incoming = it.File, true
	}
	res.Lines, res.Bytes, res.Over = s.Lines, s.Bytes, n.over
	return res, nil
}

func (n *memNode) putsOf() []remote.MemoryPutParams {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.puts)
}

var cmpMethods = append(slices.Clone(memMethods), remote.MMemoryPut)

const mbaMem = "/home/u/.claude/projects/-home-u-dev-shop/memory"

// compareRig: this machine and mba both hold the project shop's directory and memories of it: deploy only here, cart
// only on mba, notes the same, oauth different, the Codex block callback only here; mba has one under .incoming/
// already. The memory overlay is open on mba's session.
func compareRig(t *testing.T, tr string, set func(*handoffRig)) (g *handoffRig, here, mba *memNode) {
	t.Helper()
	var hereDir string
	g, _ = memoryRig(t, tr, func(g *handoffRig) {
		hereDir = filepath.Join(g.here.hello.Home, "dev", "shop")
		here = cmpNode(filepath.Join(g.here.hello.Home, ".claude", "projects", "shop", "memory"), hereDir, map[string]string{
			"deploy.md": "Deploy from main.\n", "notes.md": "Shared notes.\n", "oauth.md": "Decode the state once.\n"},
			"- [Deploy](deploy.md) — staging\n- [Notes](notes.md)\n- [OAuth](oauth.md)\n", "callback")
		g.here.mem = here
		g.here.hello.Methods = append(g.here.hello.Methods, cmpMethods...)
		g.mba.mem = cmpNode(mbaMem, shopDir, map[string]string{
			"cart.md": "Round the cart total once.\n", "notes.md": "Shared notes.\n", "oauth.md": "Decode the state twice.\n"},
			"- [Cart](cart.md)\n- [Notes](notes.md)\n- [OAuth](oauth.md)\n", "")
		g.mba.mem.sets[0].Incoming = []memory.Item{{File: mbaMem + "/.incoming/notes.md", Title: "notes (theirs)", At: time.Now().Add(-time.Hour)}}
		g.mba.mem.texts[mbaMem+"/.incoming/notes.md"] = "Notes from elsewhere.\n"
		g.mba.hello.Methods = append(g.mba.hello.Methods, remote.MMemoryPut)
		if set != nil {
			set(g)
		}
	})
	m, mba := g.m, g.mba.mem
	p := &task.Project{ID: "p_shop", Name: "shop", Repos: []task.Repo{{Name: "shop", Dirs: map[string]string{coord.Local: hereDir, "mba": shopDir}}}}
	m.setProjects(projects.Mine(map[string]*task.Project{p.ID: p}))
	m.refresh()
	m.cursor = slices.IndexFunc(m.rows, func(r row) bool { return r.rec != nil && r.rec.Host == "mba" })
	key(m, "i")
	waitFor(t, m, func() bool { return m.ov.kind == ovMemory && !m.ov.mem.view.busy() })
	if len(m.ov.mem.view.machines) != 2 {
		t.Fatalf("the overlay reads the project on both machines:\n%s", screenText(m))
	}
	return g, here, mba
}

// startCompare presses 「对比…」 and picks mba.
func startCompare(t *testing.T, m *Model) {
	t.Helper()
	clickText(t, m, i18n.T("memory.btn_compare"))
	if m.ov.kind != ovPicker {
		t.Fatalf("compare asks for the other machine:\n%s", screenText(m))
	}
	key(m, "enter")
	waitCompared(t, m)
}

func waitCompared(t *testing.T, m *Model) {
	t.Helper()
	waitFor(t, m, func() bool { return m.ov.kind == ovMemCompare && !m.ov.mcmp.loading && !m.ov.mcmp.copying })
}

// cmpGoTo puts the compare list's cursor on the row named name in group g.
func cmpGoTo(t *testing.T, m *Model, g int, name string) {
	t.Helper()
	rows := m.ov.mcmp.rows()
	i := slices.IndexFunc(rows, func(r cmpRow) bool { return r.group == g && r.name() == name })
	if i < 0 {
		t.Fatalf("no %q in group %d:\n%s", name, g, screenText(m))
	}
	for m.ov.mcmp.cursor < i {
		key(m, "j")
	}
	for m.ov.mcmp.cursor > i {
		key(m, "k")
	}
}

// TestMemoryCompare: over ssh and the server's node.call, 「对比…」 with mba lists what is only here, only there and
// different, the same counted apart, mba's .incoming/ to merge; Enter reads one with a tab per side.
func TestMemoryCompare(t *testing.T) {
	for _, tr := range handoffTransports {
		t.Run(tr, func(t *testing.T) {
			g, _, mba := compareRig(t, tr, nil)
			m := g.m
			startCompare(t, m)
			c := m.ov.mcmp
			if c.why != "" || len(c.pairs) != 1 || c.pairs[0].Dirs.To != shopDir {
				t.Fatalf("compared the project's directories on both: %q %+v", c.why, c.pairs)
			}
			s := screenText(m)
			here := i18n.T("project.this_machine")
			for _, want := range []string{i18n.F("memory.cmp_only", here, 2), "deploy", "callback", i18n.T("memory.cmp_codex"),
				i18n.F("memory.cmp_only", "mba", 1), "cart", i18n.F("memory.cmp_differ", 1), "oauth",
				i18n.F("memory.cmp_incoming", 1), "notes (theirs)", i18n.F("memory.cmp_same_bare", 1),
				i18n.F("memory.btn_copy_to", "mba"), i18n.T("memory.btn_copy_here")} {
				if !strings.Contains(s, want) {
					t.Errorf("the comparison lacks %q:\n%s", want, s)
				}
			}
			checkWidth(t, m)

			cmpGoTo(t, m, cmpDiffer, "oauth.md")
			key(m, "enter")
			waitFor(t, m, func() bool { r := m.ov.mcmp.read; return r != nil && !r.loading[0] && !r.loading[1] })
			if s := screenText(m); !strings.Contains(s, "Decode the state once.") || !strings.Contains(s, "mba") {
				t.Fatalf("the reader opens on this side, with a tab for mba:\n%s", s)
			}
			checkWidth(t, m)
			key(m, "tab")
			if s := screenText(m); !strings.Contains(s, "Decode the state twice.") {
				t.Fatalf("Tab shows mba's:\n%s", s)
			}
			key(m, "esc")
			if m.ov.kind != ovMemCompare || m.ov.mcmp.read != nil {
				t.Fatalf("Esc goes back to the comparison: %d", m.ov.kind)
			}

			cmpGoTo(t, m, cmpIncoming, "notes (theirs)")
			key(m, "enter")
			waitFor(t, m, func() bool { r := m.ov.mcmp.read; return r != nil && !r.loading[1] })
			if s := screenText(m); !strings.Contains(s, "Notes from elsewhere.") {
				t.Fatalf("one to merge reads from its machine:\n%s", s)
			}
			key(m, "esc")
			key(m, "esc")
			if m.ov.kind != ovMemory {
				t.Fatalf("Esc goes back to the memory overlay: %d", m.ov.kind)
			}
			if c := mba.called(); slices.Contains(c, remote.MMemoryPut) {
				t.Errorf("comparing writes nothing: %v", c)
			}
		})
	}
}

// TestMemoryCopyEachWay: x ticks what is only here and 「复制到 mba」 writes it there with its index line; the row
// under the cursor, unticked, goes the other way with 「复制到这边」; the comparison is read again.
func TestMemoryCopyEachWay(t *testing.T) {
	for _, tr := range handoffTransports {
		t.Run(tr, func(t *testing.T) {
			g, here, mba := compareRig(t, tr, nil)
			m := g.m
			startCompare(t, m)
			cmpGoTo(t, m, cmpOnlyHere, "deploy.md")
			key(m, "x")
			if s := screenText(m); !strings.Contains(s, "[+]") {
				t.Fatalf("x ticks the row:\n%s", s)
			}
			clickText(t, m, i18n.F("memory.btn_copy_to", "mba"))
			waitCompared(t, m)
			ps := mba.putsOf()
			if len(ps) != 1 || ps[0].Name != "deploy.md" || ps[0].Dir != shopDir || ps[0].Expect != "" || ps[0].Text != "Deploy from main.\n" ||
				ps[0].Line != "- [Deploy](deploy.md) — staging" {
				t.Fatalf("deploy.md went to mba's directory with its index line: %+v", ps)
			}
			s := screenText(m)
			if !strings.Contains(s, i18n.F("memory.cmp_copied", "mba", 1)) || !strings.Contains(s, i18n.F("memory.cmp_same_bare", 2)) {
				t.Errorf("the copy is reported and the comparison read again:\n%s", s)
			}
			if strings.Contains(s, "[+]") {
				t.Errorf("the ticks are cleared:\n%s", s)
			}

			cmpGoTo(t, m, cmpOnlyThere, "cart.md")
			clickText(t, m, i18n.T("memory.btn_copy_here"))
			waitCompared(t, m)
			ps = here.putsOf()
			if len(ps) != 1 || ps[0].Name != "cart.md" || ps[0].Dir != filepath.Join(g.here.hello.Home, "dev", "shop") || ps[0].Text != "Round the cart total once.\n" ||
				ps[0].Line != "- [Cart](cart.md)" {
				t.Fatalf("cart.md came here with its index line: %+v", ps)
			}
			if s := screenText(m); !strings.Contains(s, i18n.F("memory.cmp_copied", i18n.T("project.this_machine"), 1)) {
				t.Errorf("the copy here is reported:\n%s", s)
			}
			checkWidth(t, m)
		})
	}
}

// A different one copied over lands in .incoming/ there, listed to merge, never over the other.
func TestMemoryCopyConflictGoesToIncoming(t *testing.T) {
	g, _, mba := compareRig(t, "ssh", nil)
	m := g.m
	startCompare(t, m)
	cmpGoTo(t, m, cmpDiffer, "oauth.md")
	clickText(t, m, i18n.F("memory.btn_copy_to", "mba"))
	waitCompared(t, m)
	ps := mba.putsOf()
	if len(ps) != 1 || ps[0].Expect != memSHA("Decode the state twice.\n") {
		t.Fatalf("the put expects what was compared there: %+v", ps)
	}
	if mba.texts[mbaMem+"/oauth.md"] != "Decode the state twice.\n" {
		t.Errorf("mba's own oauth.md is left as it was")
	}
	s := screenText(m)
	if !strings.Contains(s, i18n.F("memory.cmp_incoming_note", 1, "mba")) || !strings.Contains(s, i18n.F("memory.cmp_incoming", 2)) {
		t.Fatalf("the conflict is said and listed to merge:\n%s", s)
	}
}

// What changed there after the comparison is not copied: stale, said, and compared again.
func TestMemoryCopyStale(t *testing.T) {
	g, _, mba := compareRig(t, "node.call", nil)
	m := g.m
	startCompare(t, m)
	mba.mu.Lock()
	i := slices.IndexFunc(mba.sets[0].Items, func(it memory.Item) bool { return strings.HasSuffix(it.File, "/oauth.md") })
	mba.sets[0].Items[i].SHA = memSHA("changed meanwhile")
	mba.mu.Unlock()
	cmpGoTo(t, m, cmpDiffer, "oauth.md")
	clickText(t, m, i18n.F("memory.btn_copy_to", "mba"))
	waitCompared(t, m)
	if s := screenText(m); !strings.Contains(s, i18n.F("memory.cmp_stale", "oauth.md", "mba")) {
		t.Fatalf("stale is said:\n%s", s)
	}
	if len(mba.sets[0].Incoming) != 1 {
		t.Errorf("nothing was written: %+v", mba.sets[0].Incoming)
	}
	if n := slices.Index(mba.called(), remote.MMemoryPut); n < 0 || !slices.Contains(mba.called()[n:], remote.MMemoryList) {
		t.Errorf("the comparison is read again after: %v", mba.called())
	}
}

// After a copy that leaves MEMORY.md over what Claude loads, the overlay says so.
func TestMemoryCopyOverTheLoadLimit(t *testing.T) {
	g, _, mba := compareRig(t, "ssh", func(g *handoffRig) { g.mba.mem.over = true })
	m := g.m
	startCompare(t, m)
	cmpGoTo(t, m, cmpOnlyHere, "deploy.md")
	clickText(t, m, i18n.F("memory.btn_copy_to", "mba"))
	waitCompared(t, m)
	if len(mba.putsOf()) != 1 {
		t.Fatalf("copied: %+v", mba.putsOf())
	}
	if s := screenText(m); !strings.Contains(s, i18n.F("memory.cmp_over", "mba")) {
		t.Fatalf("the load limit is said after the copy:\n%s", s)
	}
}

// Codex blocks are compared, never ticked or copied.
func TestMemoryCompareCodexIsCompareOnly(t *testing.T) {
	g, _, mba := compareRig(t, "ssh", nil)
	m := g.m
	startCompare(t, m)
	cmpGoTo(t, m, cmpOnlyHere, "callback")
	key(m, "x")
	if m.notice != i18n.T("memory.codex_compare_only") || len(m.ov.mcmp.ticks) != 0 {
		t.Fatalf("x on a Codex block says it is compare only: %q %v", m.notice, m.ov.mcmp.ticks)
	}
	m.notice = ""
	clickText(t, m, i18n.F("memory.btn_copy_to", "mba"))
	if m.notice != i18n.T("memory.codex_compare_only") || m.ov.mcmp.copying {
		t.Fatalf("copying a Codex block is refused: %q", m.notice)
	}
	if len(mba.putsOf()) != 0 {
		t.Errorf("nothing was sent: %+v", mba.putsOf())
	}
}

// A machine that cannot be compared with says why in the picker and when picked, and is sent nothing.
func TestMemoryCompareRefusals(t *testing.T) {
	offline := remote.Reason(&wire.Error{Code: wire.CodeOffline})
	for _, c := range []struct {
		name, tr string
		set      func(*handoffRig)
		after    func(*Model)
		why      string
	}{
		{"offline", "ssh", nil, func(m *Model) { m.remote["mba"].err = &wire.Error{Code: wire.CodeOffline} },
			i18n.F("remote.unreachable", "mba", offline)},
		{"too old", "node.call", nil, func(m *Model) { m.lackOn("mba", remote.MMemoryList) }, remote.TooOld("mba", remote.MMemoryList)},
		{"not yours", "node.call", nil, func(m *Model) { m.setMachines([]remote.Machine{{Name: "mba"}, {Name: "bobs"}}) },
			i18n.F("memory.not_mine", "mba")},
	} {
		t.Run(c.name, func(t *testing.T) {
			g, _, mba := compareRig(t, c.tr, c.set)
			m := g.m
			c.after(m)
			before := len(mba.called())
			clickText(t, m, i18n.T("memory.btn_compare"))
			if s, head := screenText(m), []rune(stripped(c.why)); m.ov.kind != ovPicker || !strings.Contains(stripped(s), "mba  ·  "+string(head[:min(12, len(head))])) {
				t.Fatalf("the picker says why (%q):\n%s", c.why, s)
			}
			key(m, "enter")
			if m.ov.kind != ovMemory || stripped(m.notice) != stripped(c.why) {
				t.Fatalf("picking it says why and stays: %d %q", m.ov.kind, m.notice)
			}
			if got := mba.called()[before:]; len(got) > 0 {
				t.Errorf("nothing was sent: %v", got)
			}
		})
	}
}

// A tend that compares (memory.ls) but cannot be written to (no memory.put) takes no copy; copies from it still work.
func TestMemoryCopyToATendWithoutPut(t *testing.T) {
	g, here, mba := compareRig(t, "ssh", func(g *handoffRig) {
		g.mba.hello.Methods = slices.DeleteFunc(g.mba.hello.Methods, func(s string) bool { return s == remote.MMemoryPut })
	})
	m := g.m
	startCompare(t, m)
	cmpGoTo(t, m, cmpOnlyHere, "deploy.md")
	clickText(t, m, i18n.F("memory.btn_copy_to", "mba"))
	waitCompared(t, m)
	if stripped(m.notice) != stripped(remote.TooOld("mba", remote.MMemoryPut)) || len(mba.putsOf()) != 0 {
		t.Fatalf("an old tend is named with its update command: %q %+v", m.notice, mba.putsOf())
	}
	cmpGoTo(t, m, cmpOnlyThere, "cart.md")
	clickText(t, m, i18n.T("memory.btn_copy_here"))
	waitCompared(t, m)
	if len(here.putsOf()) != 1 {
		t.Fatalf("copying here from it works: %+v", here.putsOf())
	}
}

// A group that is no project has nothing to pair with.
func TestMemoryCompareNeedsAProject(t *testing.T) {
	m, _ := memoryModel(t)
	key(m, "i")
	waitFor(t, m, func() bool { return m.ov.kind == ovMemory && !m.ov.mem.view.busy() })
	clickText(t, m, i18n.T("memory.btn_compare"))
	if m.ov.kind != ovMemory || m.notice != i18n.F("memory.cmp_no_project", m.ov.mem.view.label) {
		t.Fatalf("no project, no comparison: %d %q", m.ov.kind, m.notice)
	}
}

// The comparison's keys: x ticks as in the other dialogs, Enter reads, Tab reaches the buttons, Esc goes back; none needs a letter.
func TestMemoryCompareKeys(t *testing.T) {
	for k, a := range map[string]act{"space": actNone, "x": actTick, "ctrl+x": actTick, "enter": actEnter, "tab": actFocusNext,
		"shift+tab": actFocusPrev, "esc": actClose, "j": actDown, "down": actDown, "k": actUp, "up": actUp} {
		if got := keyAct(inMemCompare, k); got != a {
			t.Errorf("%q does %d, want %d", k, got, a)
		}
	}
	for _, b := range bindings {
		if b.in&inMemCompare != 0 && isLower(b.keys[0]) && !hasNonLetter(b.keys) {
			t.Errorf("%q has no key an input method lets through", b.keys[0])
		}
	}
}
