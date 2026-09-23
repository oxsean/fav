package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
)

// askMove: r's session, or with r nil on a group header the whole project (subdirs included); live sessions block it.
func (m *Model) askMove(r *fav.Rec) {
	if m.view == viewLive || m.inTrash() {
		return
	}
	old := ""
	var only *fav.Rec
	switch {
	case r != nil:
		old, only = r.Cwd, r
	case m.view == viewProjects:
		if recs := m.groups[m.groupUnderCursor()]; len(recs) > 0 {
			if d := topN(recs, func(r *fav.Rec) string { return r.Cwd }, 1); len(d) > 0 {
				old = d[0].key
			}
		}
	}
	if old == "" {
		m.flash(i18n.T("move.no_dir"))
		return
	}
	if running := m.runningUnder(old, only); len(running) > 0 {
		m.flash(i18n.F("move.refused", len(running), render.Truncate(running[0], 40)))
		return
	}
	start := old
	if !paths.IsDir(old) { // cwd is gone: prefill the single guess, else start from the parent
		start = filepath.Dir(old) + string(filepath.Separator)
		for _, miss := range m.idx.FindMissing(m.store, old) {
			if miss.Dir == old && len(miss.Found) == 1 {
				start = miss.Found[0]
			}
		}
	}
	m.pickMoveTarget(old, start, only)
}

func (m *Model) pickMoveTarget(old, start string, only *fav.Rec) {
	m.openDirPicker(i18n.F("move.title", paths.Tilde(old)), start, func(m *Model, dst string) { m.confirmMove(old, dst, only) })
}

func (m *Model) runningUnder(dir string, only *fav.Rec) []string {
	var out []string
	seen := map[string]bool{}
	check := func(r *fav.Rec) {
		if r.SessionID == "" || seen[r.SessionID] || !m.isLive(r.SessionID) {
			return
		}
		if only != nil && r != only {
			return
		}
		if paths.Under(r.Cwd, dir) {
			seen[r.SessionID] = true
			out = append(out, r.Title)
		}
	}
	for _, r := range m.store.All() {
		check(r)
	}
	for _, r := range m.unfav {
		check(r)
	}
	return out
}

func (m *Model) confirmMove(old, dst string, only *fav.Rec) {
	dst = filepath.Clean(dst)
	plan, err := m.idx.PlanMove(m.store, m.live, old, dst)
	if err != nil {
		m.flash(i18n.F("move.failed", err))
		return
	}
	if only != nil {
		plan.Only(only.Provider, only.SessionID)
	}
	if len(plan.Live) > 0 {
		m.flash(i18n.F("move.refused", len(plan.Live), render.Truncate(plan.Live[0].Title, 40)))
		return
	}
	if len(plan.Sessions) == 0 && len(plan.Records) == 0 {
		m.flash(i18n.T("move.nothing"))
		return
	}
	claude, codex := 0, 0
	for _, s := range plan.Sessions {
		if s.Provider == fav.ProviderClaude {
			claude++
		} else {
			codex++
		}
	}
	lines := []string{
		i18n.F("move.from", paths.Tilde(old)),
		i18n.F("move.to", paths.Tilde(dst)),
		"",
		i18n.F("move.summary", len(plan.Sessions), claude, codex, plan.Files(), len(plan.Records)),
	}
	if plan.Settings {
		lines = append(lines, i18n.T("move.settings"))
	}
	lines = append(lines, "", i18n.T("move.note"), i18n.T("move.restart_hint"), "", i18n.T("move.confirm_hint"))
	m.openConfirm(i18n.T("move.confirm_title"), i18n.T("move.btn_move"), lines, func(m *Model) { m.doMove(plan) },
		func(m *Model) { m.pickMoveTarget(old, dst+string(filepath.Separator), only) })
}

func (m *Model) doMove(plan *index.MovePlan) {
	// a session may have started while the dialog was open: replan with the latest live set
	fresh, err := plan.Replan(m.idx, m.store, capture.MergeLive(m.live, capture.LiveSessions()))
	if err != nil {
		m.flash(i18n.F("move.failed", err))
		return
	}
	if len(fresh.Live) > 0 {
		m.flash(i18n.F("move.refused", len(fresh.Live), render.Truncate(fresh.Live[0].Title, 40)))
		return
	}
	rep, err := fresh.Apply(m.store)
	if err != nil {
		m.flash(i18n.F("move.failed", err))
	}
	m.rescan(rep.Touched)
	if err == nil {
		m.flash(i18n.F("move.done", rep.Sessions, rep.Files, rep.Records))
	}
}

// rescan updates the index now after files were moved, trashed, restored or rewritten (force: read from scratch).
func (m *Model) rescan(force map[string]bool) {
	next, err := m.idx.RescanSave(force)
	m.adopt(next)
	if err != nil {
		m.flash(i18n.F("flash.index_not_written", err))
	}
}

// adopt takes an index rescanned on the main loop; refreshes started before it are stale.
func (m *Model) adopt(next *index.Index) {
	m.probes, m.probeWant = nil, nil
	m.heldIdx, m.idxGen = nil, m.idxGen+1
	m.applyIndex(next)
}

// openDirPicker: the list follows the path in the input, existing dirs only; Enter picks the highlighted one, Tab / → descends.
func (m *Model) openDirPicker(title, start string, apply func(*Model, string)) {
	m.openPicker(title, "", nil, false, nil, func(m *Model, chosen []string) {
		if len(chosen) > 0 {
			apply(m, chosen[0])
		}
	})
	m.ov.browse = listDirs
	m.ov.filter.CharLimit = 512
	m.ov.filter.Placeholder = i18n.T("dir.placeholder")
	m.ov.filter.SetValue(start)
	m.ov.filter.CursorEnd()
}

// descendDir puts the highlighted dir into the input and lists its children; the highlight lands on the first child, so repeated Enter only descends.
func (m *Model) descendDir() {
	vis := m.ov.visible()
	if m.ov.cursor >= len(vis) {
		return
	}
	m.ov.filter.SetValue(vis[m.ov.cursor].name + string(filepath.Separator))
	m.ov.filter.CursorEnd()
	m.ov.cursor = 0
	if len(m.ov.visible()) > 1 {
		m.ov.cursor = 1
	}
}

// pickDir selects the directory currently listed, not the highlighted child.
func (m *Model) pickDir() {
	vis := m.ov.visible()
	if len(vis) == 0 || vis[0].count != -1 {
		return
	}
	m.finishPicker([]string{vis[0].name})
}

// listDirs: a directory lists its children (itself first); otherwise the parent filtered by the typed prefix.
func listDirs(text string) []item {
	text = paths.Expand(strings.TrimSpace(text))
	dir, prefix := text, ""
	if !paths.IsDir(text) {
		dir, prefix = filepath.Dir(text), filepath.Base(text)
	}
	if !paths.IsDir(dir) {
		return nil
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		if !e.IsDir() || (strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(prefix, ".")) {
			continue
		}
		if prefix != "" && !strings.Contains(strings.ToLower(e.Name()), strings.ToLower(prefix)) {
			continue
		}
		names = append(names, e.Name())
	}
	if prefix != "" && len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	// the first row is always the listed directory (count=-1); the last path segment filters its children
	out := []item{{name: filepath.Clean(dir), label: i18n.T("dir.this") + " " + paths.Tilde(filepath.Clean(dir)), count: -1}}
	for _, n := range names {
		out = append(out, item{name: filepath.Join(dir, n), label: n + string(filepath.Separator)})
	}
	return out
}
