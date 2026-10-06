package tui

import (
	"context"
	"maps"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
)

// people are the names the coordinator gives for user ids (people.names), kept per connection: the ids shown that are
// not known yet are asked in one batch; a new connection or a reset of the state stream (who sees what changed) starts
// over. A coordinator without the method leaves ids shown as they are, and so do the ids it does not answer for.
type people struct {
	cl       *coord.Client
	names    map[string]string
	disabled map[string]bool
	asked    map[string]bool
	gen      int // which start the answers belong to
}

const namesBatch = 500 // ⚠️ the most people.names takes at once

// nameOf is the name of user id, the id while it is not known.
func (m *Model) nameOf(id string) string {
	n, ok := m.people.names[id]
	switch {
	case !ok || m.people.cl != m.tasks.cl:
		return id
	case m.people.disabled[id]:
		return i18n.F("people.disabled", n)
	}
	return n
}

// ownerName is the name of whoever owns machine name.
func (m *Model) ownerName(name string) string { return m.nameOf(m.ownerOf(name)) }

func (m *Model) forgetNames() { m.people = people{gen: m.people.gen + 1} }

type namesMsg struct {
	cl  *coord.Client
	gen int
	ids []string
	got coord.People
	err error
}

// askNames asks the names of the machines' and the projects' owners not asked on this connection yet.
func (m *Model) askNames() tea.Cmd {
	cl := m.tasks.cl
	if cl == nil || m.proj.hello == nil || !slices.Contains(m.proj.hello.Methods, coord.MPeopleNames) {
		return nil
	}
	if m.people.cl != cl {
		m.people = people{cl: cl, names: map[string]string{}, disabled: map[string]bool{}, asked: map[string]bool{}, gen: m.people.gen + 1}
	}
	gen := m.people.gen
	var ids []string
	add := func(id string) {
		if id != "" && !m.people.asked[id] {
			m.people.asked[id] = true
			ids = append(ids, id)
		}
	}
	for _, mc := range m.tasks.machines {
		add(mc.Owner)
	}
	if st := m.tasks.st; st != nil {
		for _, id := range slices.Sorted(maps.Keys(st.Projects)) {
			add(st.Projects[id].Owner)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
		defer cancel()
		msg := namesMsg{cl: cl, gen: gen, ids: ids, got: coord.People{Names: map[string]string{}}}
		for batch := range slices.Chunk(ids, namesBatch) {
			var got coord.People
			if msg.err = cl.Call(ctx, coord.MPeopleNames, coord.PeopleParams{IDs: batch}, &got); msg.err != nil {
				break
			}
			maps.Copy(msg.got.Names, got.Names)
			msg.got.Disabled = append(msg.got.Disabled, got.Disabled...)
		}
		return msg
	}
}

func (msg namesMsg) apply(m *Model) tea.Cmd {
	if m.people.cl != msg.cl || m.people.gen != msg.gen {
		return nil
	}
	if msg.err != nil { // asked again with the next change
		tracef("people: names: %v", msg.err)
		for _, id := range msg.ids {
			delete(m.people.asked, id)
		}
		return nil
	}
	maps.Copy(m.people.names, msg.got.Names)
	for _, id := range msg.got.Disabled {
		m.people.disabled[id] = true
	}
	return nil
}
