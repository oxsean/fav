package tui

import (
	"maps"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/tend"
)

// While something runs here, its transcripts are stat-ed every second and a changed one is read on through
// index.RefreshPaths: turns, new sessions and the right pane follow without waiting for the 10 s refresh.

const (
	followEvery = time.Second
	followRetry = 3 * time.Second // a running session whose transcript was not found is looked for again after this
)

type follow struct {
	on    bool                 // a tick or a stat is pending
	found map[string]string    // session id → transcript of running sessions no record carries yet
	tried map[string]time.Time // running sessions whose transcript was not found, and when
	text  bool                 // changes taken in since the full-text store last followed the index
}

type followTickMsg struct{}

type followMsg struct {
	base, next *index.Index
	gen        int
	changed    []string
	found      map[string]string
	missed     []string
}

func followTick() tea.Cmd {
	return tea.Tick(followEvery, func(time.Time) tea.Msg { return followTickMsg{} })
}

// startFollow starts the ticks when something runs here and they are not running yet.
func (m *Model) startFollow() tea.Cmd {
	if m.follow.on || len(m.live) == 0 {
		return nil
	}
	m.follow.on = true
	return followTick()
}

// newestIdx is the snapshot the next one builds on: one held back while scrolling is newer than the applied one.
func (m *Model) newestIdx() *index.Index {
	if m.heldIdx != nil {
		return m.heldIdx
	}
	return m.idx
}

// followLive stats the running sessions' transcripts off the main loop; it ends the ticks once nothing runs.
func (m *Model) followLive() tea.Cmd {
	if len(m.live) == 0 {
		m.follow = follow{text: m.follow.text}
		return nil
	}
	now := time.Now()
	var paths []string
	look := map[string]string{} // session id → provider
	for id, l := range m.live {
		if r := m.bySession(id); r != nil && r.Host == "" && r.TranscriptPath != "" {
			paths = append(paths, r.TranscriptPath)
			continue
		}
		if p := m.follow.found[id]; p != "" {
			paths = append(paths, p)
			continue
		}
		if l.Agent != tend.ProviderClaude && l.Agent != tend.ProviderCodex || now.Sub(m.follow.tried[id]) < followRetry {
			continue
		}
		look[id] = l.Agent
	}
	maps.DeleteFunc(m.follow.found, func(id, _ string) bool { _, ok := m.live[id]; return !ok })
	maps.DeleteFunc(m.follow.tried, func(id string, _ time.Time) bool { _, ok := m.live[id]; return !ok })
	if len(paths) == 0 && len(look) == 0 {
		return followTick()
	}
	base, gen := m.newestIdx(), m.idxGen
	return func() tea.Msg {
		msg := followMsg{base: base, gen: gen, found: map[string]string{}}
		for id, provider := range look {
			if p := capture.TranscriptPath(provider, id); p != "" {
				msg.found[id] = p
				paths = append(paths, p)
			} else {
				msg.missed = append(msg.missed, id)
			}
		}
		msg.next, msg.changed = base.RefreshPaths(paths)
		return msg
	}
}

// applyFollow takes in a snapshot built on the newest one, saved as the periodic refresh does; the right pane re-reads
// when the session under the cursor changed. A result over a snapshot replaced meanwhile is dropped: the next tick redoes it.
func (m *Model) applyFollow(msg followMsg) tea.Cmd {
	if m.follow.found == nil {
		m.follow.found, m.follow.tried = map[string]string{}, map[string]time.Time{}
	}
	maps.Copy(m.follow.found, msg.found)
	for _, id := range msg.missed {
		m.follow.tried[id] = time.Now()
	}
	var cmd tea.Cmd
	if len(msg.changed) > 0 && msg.gen == m.idxGen && msg.base == m.newestIdx() {
		msg.next.Save() // a failed write only slows the next start
		m.heldIdx, m.follow.text = msg.next, true
		tracef("follow %d changed", len(msg.changed))
		if r := m.current(); r != nil && r.Host == "" && slices.Contains(msg.changed, r.TranscriptPath) {
			cmd = m.refreshChat()
		}
	}
	if len(m.live) == 0 {
		m.follow = follow{text: m.follow.text}
		return cmd
	}
	return tea.Batch(cmd, followTick())
}
