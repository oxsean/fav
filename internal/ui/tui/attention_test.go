package tui

import (
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
)

func TestAttentionQueue(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	m := sized(t, 140, 40)
	r := m.current()
	id := r.SessionID
	m.live = map[string]capture.Live{id: {Status: "idle"}}
	m.applyPulses(pulseMsg{id: {Size: 100, Finished: true}})
	if m.need(id) != needNone || m.notice != "" {
		t.Fatal("first seen: its old output is not news")
	}
	m.applyPulses(pulseMsg{id: {Size: 200, Finished: true}})
	if m.need(id) != needUnseen || !strings.Contains(m.notice, r.Title[:3]) || m.needCount() != 1 {
		t.Fatalf("new output, finished: unseen and announced once: need=%d notice=%q", m.need(id), m.notice)
	}
	if !strings.Contains(agentsTab(len(m.live), m.needCount()), "!1") {
		t.Fatal("the tab says one needs you")
	}
	m.notice = ""
	m.applyPulses(pulseMsg{id: {Size: 200, Finished: true}})
	if m.notice != "" {
		t.Fatal("announced once, not every poll")
	}
	m.Update(press("."))
	if m.need(id) != needNone {
		t.Fatal(". marks it handled")
	}
	m.applyPulses(pulseMsg{id: {Size: 300, Finished: true}})
	if m.need(id) != needUnseen {
		t.Fatal("new output after handled brings it back")
	}

	reloaded := loadAttn()
	if reloaded[id].Seen != 200 || reloaded[id].Quiet != 200 {
		t.Fatalf("what the user took in survives a restart: %+v", reloaded[id])
	}

	m.Update(press("H"))
	if m.need(id) != needNone {
		t.Fatal("H snoozes it")
	}
}

func TestAttentionAskingAndGroups(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	m := sized(t, 140, 40)
	recs := m.store.All()
	a, b, c := recs[0].SessionID, recs[1].SessionID, recs[2].SessionID
	m.live = map[string]capture.Live{a: {Status: "working"}, b: {Status: "idle"}, c: {Status: "working"}}
	m.applyPulses(pulseMsg{a: {Size: 10}, b: {Size: 10, Finished: true}, c: {Size: 10, Asking: true}})
	if m.need(c) != needWait {
		t.Fatal("an open question needs the user even when first seen")
	}
	if text, _ := m.needLabel(c, m.live[c]); !strings.Contains(text, i18n.T("attn.asking")) {
		t.Fatalf("the card says it is asking: %q", text)
	}
	m.cfg.LiveSort = liveSortGroup
	rows := m.liveRows([]*fav.Rec{recs[0], recs[1], recs[2]})
	if rows[0].group != i18n.T("live.waiting") || rows[1].rec.SessionID != c {
		t.Fatalf("the waiting group comes first: %+v", rows)
	}
}
