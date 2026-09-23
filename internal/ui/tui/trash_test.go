package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/testkit"
)

func TestStatusPickerAndTrash(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	m := sized(t, 140, 40)
	m.pickStatus()
	if !m.ov.active() || m.ov.visible()[m.ov.cursor].name != fav.StatusOpen {
		t.Fatalf("选择器应打开并停在当前值：%+v", m.ov.cursor)
	}
	m.ov.filter.SetValue("回收")
	m.Update(press("enter"))
	if m.search.Value() != "status:trash" || !m.inTrash() {
		t.Fatalf("选回收站应写进查询串：%q", m.search.Value())
	}
	if len(m.rows) != 0 {
		t.Fatalf("回收站应是空的：%d", len(m.rows))
	}
	m.search.SetValue("")
	m.refresh()
	r := m.current()
	if r == nil {
		t.Fatal("要有一条当前记录")
	}
	transcript := filepath.Join(t.TempDir(), r.SessionID+".jsonl")
	os.WriteFile(transcript, []byte("{}\n"), 0o644)
	r.PinnedPath = transcript
	m.store.Put(r)
	key := func(k string) { m.Update(press(k)) }
	key("D")
	if m.ov.kind != ovConfirm {
		t.Fatal("D 应先确认")
	}
	m.Update(press("esc"))
	if m.ov.active() || m.store.Get(r.ID) == nil {
		t.Fatal("Esc 应取消，不删")
	}
	key("D")
	m.Update(press("enter"))
	if m.ov.active() || m.store.Get(r.ID) == nil {
		t.Fatal("删除确认的焦点在取消：Enter 不删")
	}
	key("D")
	key("y")
	if m.store.Get(r.ID) != nil {
		t.Fatal("确认后记录应墓碑")
	}
	if _, err := os.Stat(transcript); !os.IsNotExist(err) {
		t.Fatal("钉住的文件应已挪走")
	}
	m.search.SetValue("status:trash")
	m.refresh()
	if m.countRecs() != 1 || m.current() == nil || m.current().SessionID != r.SessionID {
		t.Fatalf("回收站应列出刚删的：%d", m.countRecs())
	}
	key("f")
	if m.store.Get(r.ID) != nil {
		t.Fatal("回收站里 f 不该复活记录")
	}
	key("D")
	if got := m.store.Get(r.ID); got == nil || got.Deleted {
		t.Fatal("还原后记录应回来")
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Fatal("文件应回到原处")
	}
	if len(m.rows) != 0 {
		t.Fatalf("还原后回收站应空：%d", len(m.rows))
	}
}

// a deleted session leaves the list, tab and picker counts even after re-attaching the index; restoring brings it back
func TestDeletedSessionLeavesEveryCountUntilRestored(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cwd := t.TempDir()
	pdir := index.ClaudeProjectDir(cwd)
	os.MkdirAll(pdir, 0o755)
	var b strings.Builder
	for i := range 3 {
		b.WriteString(`{"type":"user","timestamp":"2026-09-10T01:00:0` + strconv.Itoa(i) + `Z","cwd":` + testkit.JSONString(cwd) + `,"message":{"content":"提示 ` + strconv.Itoa(i) + ` 做点什么事情"}}` + "\n")
	}
	os.WriteFile(filepath.Join(pdir, "gone-1.jsonl"), []byte(b.String()), 0o644)
	idx, _ := index.OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	idx, _ = idx.Refresh()
	m := newModel(t, fixture(t), 140, 40)
	m.applyIndex(idx)
	m.setView(viewSessions)
	listed := func() bool {
		for _, row := range m.rows {
			if row.rec != nil && row.rec.SessionID == "gone-1" {
				return true
			}
		}
		return false
	}
	projects := func() int { return len(projectsOf(m.visibleRecs())) }
	nAll, nProj := m.nAll, projects()
	for i, row := range m.rows {
		if row.rec != nil && row.rec.SessionID == "gone-1" {
			m.cursor = i
		}
	}
	if m.current() == nil || m.current().SessionID != "gone-1" {
		t.Fatal("the indexed session is listed")
	}
	m.Update(press("D"))
	m.Update(press("y"))
	m.applyIndex(m.idx)
	if listed() || m.nAll != nAll-1 || projects() != nProj-1 {
		t.Fatalf("deleted: gone from the list and every count: listed=%v nAll %d→%d projects %d→%d", listed(), nAll, m.nAll, nProj, projects())
	}
	m.search.SetValue("status:trash")
	m.refresh()
	_, cmd := m.Update(press("D"))
	for msg := range drain(cmd) {
		if im, ok := msg.(indexMsg); ok {
			m.Update(im)
			break
		}
	}
	m.search.SetValue("")
	m.refresh()
	if !listed() || m.nAll != nAll {
		t.Fatalf("restored: back in the list after the rescan: listed=%v nAll=%d", listed(), m.nAll)
	}
}

func TestTrashBlocksEveryAlias(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	m := sized(t, 140, 40)
	r := m.current()
	transcript := filepath.Join(t.TempDir(), r.SessionID+".jsonl")
	os.WriteFile(transcript, []byte("{}\n"), 0o644)
	r.PinnedPath = transcript
	m.store.Put(r)
	m.Update(press("D"))
	m.Update(press("y"))
	m.search.SetValue("status:trash")
	m.refresh()
	if m.current() == nil {
		t.Fatal("the deleted session is in the trash")
	}
	m.pane = paneChat
	for _, k := range []tea.KeyPressMsg{press("*"), press("ctrl+x"), press("a"), press("space")} {
		m.Update(k)
		if m.store.Get(r.ID) != nil || m.ov.active() || m.quitting {
			t.Fatalf("in the trash %q is blocked like f, also from the right pane", k.String())
		}
	}
}

// a rewrite that keeps size and mtime is only seen when forced; a second restore before the first rescan lands
// must not drop the first one's forced files
func TestRescansKeepEveryForcedFileUntilApplied(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	before, after := filepath.Join(root, "aaaa"), filepath.Join(root, "bbbb")
	pdir := index.ClaudeProjectDir(before)
	os.MkdirAll(pdir, 0o755)
	path := filepath.Join(pdir, "s1.jsonl")
	line := func(dir string) string {
		var b strings.Builder
		for i := range 3 {
			b.WriteString(`{"type":"user","timestamp":"2026-09-10T01:00:0` + strconv.Itoa(i) + `Z","cwd":` + testkit.JSONString(dir) + `,"message":{"content":"第 ` + strconv.Itoa(i) + ` 句话"}}` + "\n")
		}
		return b.String()
	}
	os.WriteFile(path, []byte(line(before)), 0o644)
	st, _ := os.Stat(path)
	idx, _ := index.OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	idx, _ = idx.Refresh()
	m := newModel(t, fixture(t), 140, 40)
	m.applyIndex(idx)

	os.WriteFile(path, []byte(line(after)), 0o644)
	os.Chtimes(path, st.ModTime(), st.ModTime())
	m.reindex(map[string]bool{path: true})
	second := m.reindex(map[string]bool{filepath.Join(pdir, "other.jsonl"): true})
	m.Update(second())
	cwdOf := func() string {
		for _, s := range m.idx.Sessions() {
			if s.SessionID == "s1" {
				return s.Cwd
			}
		}
		return ""
	}
	if got := cwdOf(); got != after {
		t.Fatalf("the first restore's forced file was not re-read: cwd %q", got)
	}
	if m.forced != nil {
		t.Errorf("forced files outlive the rescan that read them: %v", m.forced)
	}
}
