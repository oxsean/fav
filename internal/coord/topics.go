package coord

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/oxsean/fav/internal/wire"
)

// The snapshot topics: open, then the whole list on open and each time it changed.
const (
	MMachinesWatch = "machines.watch" // the machines the viewer may see (TopicParams): PushMachines pushes
	MInboxWatch    = "inbox.watch"    // what waits for the viewer (TopicParams): PushInbox pushes
	PushMachines   = "machines"       // MachineList
	PushInbox      = "inbox"          // Inbox
)

type TopicParams struct{}

// MachineList is one push of machines.watch: every machine the viewer may see, as Machines lists them.
type MachineList struct {
	Items []Machine `json:"items"`
}

// topic is one subscriber of a snapshot topic; a kick says what it shows may have changed.
type topic struct {
	p    Principal
	kind string // PushMachines or PushInbox
	kick chan struct{}
}

func (c *Coord) watchTopic(p Principal, r *wire.Request, kind string) (any, error) {
	var tp TopicParams
	if err := r.Decode(&tp); err != nil {
		return nil, err
	}
	s, err := r.Stream(wire.StreamOptions{Class: wire.ClassStream, Full: wire.FullLag})
	if err != nil {
		return nil, err
	}
	sb := &topic{p: p, kind: kind, kick: make(chan struct{}, 1)}
	c.mu.Lock()
	c.topics[s] = sb
	c.mu.Unlock()
	go c.serveTopic(s, sb)
	return nil, nil
}

// serveTopic pushes sb's list whenever it differs from what was last pushed: counted again once the kicks of a
// moment (topicWait) came, and for the machines every machinesEvery too, in case a change had no kick.
func (c *Coord) serveTopic(s *wire.Stream, sb *topic) {
	defer func() {
		c.mu.Lock()
		delete(c.topics, s)
		c.mu.Unlock()
	}()
	ctx := s.Context()
	wait := inboxWait
	var every <-chan time.Time
	if sb.kind == PushMachines {
		wait = machinesWait
		t := time.NewTicker(machinesEvery)
		defer t.Stop()
		every = t.C
	}
	err := s.PushWait(ctx, wire.PushOpen, wire.Open{Mode: wire.ModeSnapshot})
	var sent []byte
	for err == nil {
		var b []byte
		if b, err = c.topicView(sb); err == nil && !bytes.Equal(b, sent) {
			sent = b
			err = s.PushWait(ctx, sb.kind, json.RawMessage(b))
		}
		if err != nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-every:
		case <-sb.kick:
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			select {
			case <-sb.kick:
			default:
			}
		}
	}
	s.End(nil, err)
}

// topicView is sb's list now, as JSON.
func (c *Coord) topicView(sb *topic) ([]byte, error) {
	if sb.kind == PushInbox {
		return json.Marshal(c.inbox(sb.p))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return json.Marshal(MachineList{Items: c.machinesFor(sb.p, c.machineList())})
}

// kick wakes the subscribers of kind whose viewer who picks (nil: all of them); the caller holds mu.
func (c *Coord) kick(kind string, who func(Principal) bool) {
	for _, sb := range c.topics {
		if sb.kind == kind && (who == nil || who(sb.p)) {
			select {
			case sb.kick <- struct{}{}:
			default:
			}
		}
	}
}

// machinesMoved: how a machine stands may have changed; the caller holds mu.
func (c *Coord) machinesMoved() { c.kick(PushMachines, nil) }

// watching: someone subscribes to kind; the caller holds mu.
func (c *Coord) watching(kind string) bool {
	for _, sb := range c.topics {
		if sb.kind == kind {
			return true
		}
	}
	return false
}

// concerned are the people the tasks ids, as they stand now, may wait for; the caller holds mu.
func (c *Coord) concerned(ids []string, into map[string]bool) {
	for _, id := range ids {
		if t := c.st.Tasks[id]; t != nil {
			for _, u := range concerns(c.st, t, c.st.Situation(t)) {
				into[u] = true
			}
		}
	}
}

// endTopics ends every topic stream as the coordinator closes; the caller holds mu.
func (c *Coord) endTopics() {
	for s := range c.topics {
		delete(c.topics, s)
		s.End(nil, &wire.Error{Code: wire.CodeGone})
	}
}
