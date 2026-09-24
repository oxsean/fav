package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
)

// attnEntry: what the user has taken in of a running session, by transcript size. Seen moves when they switch to it or
// mark it handled; Quiet (set by marking handled) silences a waiting session until it writes something new.
type attnEntry struct {
	Seen   int64     `json:"seen"`
	Quiet  int64     `json:"quiet,omitempty"`
	Snooze time.Time `json:"snooze,omitzero"`
}

const (
	needNone   = iota
	needWait   // Herdr blocked, or an open question to the user
	needUnseen // finished with output the user has not seen
)

const snoozeFor = time.Hour

func attnPath() string { return filepath.Join(fav.Home(), "attention.json") }

func loadAttn() map[string]attnEntry {
	out := map[string]attnEntry{}
	if b, err := os.ReadFile(attnPath()); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

// saveAttn keeps only the running sessions' entries.
func (m *Model) saveAttn() {
	keep := make(map[string]attnEntry, len(m.live))
	for id := range m.live {
		if e, ok := m.attn[id]; ok {
			keep[id] = e
		}
	}
	if b, err := json.Marshal(keep); err == nil {
		fileio.WriteFile(attnPath(), b, 0o644)
	}
}

// need: how much a running session needs the user now.
func (m *Model) need(id string) int {
	l, ok := m.live[id]
	if !ok {
		return needNone
	}
	p, havePulse := m.pulse[id]
	e := m.attn[id]
	switch {
	case m.now.Before(e.Snooze):
		return needNone
	case e.Quiet > 0 && havePulse && p.Size <= e.Quiet:
		return needNone
	case l.Status == "blocked" || havePulse && p.Asking:
		return needWait
	case havePulse && p.Finished && l.Status != "working" && p.Size > e.Seen:
		return needUnseen
	}
	return needNone
}

func (m *Model) needCount() int {
	n := 0
	for id := range m.live {
		if m.need(id) != needNone {
			n++
		}
	}
	return n
}

// applyPulses takes a pulse round; a session seen for the first time starts as seen, so old output does not flood the queue.
func (m *Model) applyPulses(ps pulseMsg) {
	m.now = time.Now()
	m.pulse = ps
	changed := false
	for id, p := range ps {
		if _, ok := m.attn[id]; !ok {
			m.attn[id], changed = attnEntry{Seen: p.Size}, true
		}
	}
	if changed {
		m.saveAttn()
	}
	m.checkNeeds()
}

// checkNeeds announces, once, a session that has come to need the user (after a live poll or a pulse round).
func (m *Model) checkNeeds() {
	next := make(map[string]int, len(m.live))
	for id := range m.live {
		n := m.need(id)
		next[id] = n
		if n != needNone && m.lastNeed != nil && m.lastNeed[id] == needNone {
			m.announce(id, n)
		}
	}
	m.lastNeed = next
}

func (m *Model) announce(id string, n int) {
	title := m.live[id].Title
	if r := m.bySession(id); r != nil {
		title = r.Title
	}
	key := "live.waiting_flash"
	if n == needUnseen {
		key = "live.finished_flash"
	}
	m.flash(render.GlyphWarn + i18n.F(key, render.Truncate(title, 40)))
	if m.cfg.Notify == fav.NotifyBell {
		fmt.Fprint(os.Stderr, "\a")
	}
}

// markSeen: the user switched to the session or marked it handled (quiet: also silence it while it keeps waiting).
func (m *Model) markSeen(id string, quiet bool) {
	p, ok := m.pulse[id]
	if !ok {
		if path := m.idx.Transcript(id); path != "" {
			p, ok = m.hosts.Source(&fav.Rec{TranscriptPath: path}).Pulse()
		}
	}
	if !ok {
		return
	}
	e := m.attn[id]
	e.Seen, e.Snooze = p.Size, time.Time{}
	if quiet {
		e.Quiet = p.Size
	}
	m.attn[id] = e
	m.saveAttn()
}

func (m *Model) handleAttn(r *fav.Rec, snooze bool) {
	if r == nil {
		return
	}
	if _, ok := m.live[r.SessionID]; !ok {
		m.flash(i18n.T("attn.not_running"))
		return
	}
	if snooze {
		e := m.attn[r.SessionID]
		e.Snooze = time.Now().Add(snoozeFor)
		m.attn[r.SessionID] = e
		m.saveAttn()
		m.flash(i18n.F("attn.snoozed", render.Truncate(r.Title, 40)))
		return
	}
	m.markSeen(r.SessionID, true)
	m.flash(i18n.F("attn.handled", render.Truncate(r.Title, 40)))
}

// needLabel overrides the live label when the session needs the user.
func (m *Model) needLabel(id string, l capture.Live) (string, int) {
	text, tone := liveLabel(l, m.now)
	switch m.need(id) {
	case needWait:
		if l.Status != "blocked" {
			return render.GlyphWarn + i18n.T("attn.asking"), toneBlocked
		}
	case needUnseen:
		return render.GlyphDone + i18n.T("attn.unseen"), toneBlocked
	}
	return text, tone
}
