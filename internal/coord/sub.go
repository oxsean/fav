package coord

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// WatchParams opens state.watch: without AfterSeq (or when the stretch after it cannot be replayed) the state comes as
// a snapshot first.
type WatchParams struct {
	AfterSeq int64 `json:"after_seq,omitzero"`
	NoBriefs bool  `json:"no_briefs,omitempty"`
}

// Snapshot is one batch of one table of the state, by id; a table may come in several batches.
type Snapshot struct {
	Part  string                     `json:"part"`
	Items map[string]json.RawMessage `json:"items"`
}

// Live: the snapshot is complete up to Seq; envelopes after it follow.
type Live struct {
	Seq int64 `json:"seq"`
}

type sub struct {
	p      Principal
	ch     chan published
	closed bool              // the coordinator is closing; under mu
	again  chan struct{}     // Reaffirm: count the affordances again
	aff    map[string]string // the affordances sent, by key (see affordances); only the feed touches it
}

// published is a committed envelope with the tasks it touched, counted before it applied (touched).
type published struct {
	env journal.Envelope
	ids []string
}

// watchState opens a state stream: the live feed is registered under mu with the journal's end, then the snapshot or
// the replay is sent outside it and the live envelopes past what was sent follow.
func (c *Coord) watchState(p Principal, r *wire.Request) (any, error) {
	var wp WatchParams
	if err := r.Decode(&wp); err != nil {
		return nil, err
	}
	s, err := r.Stream(wire.StreamOptions{Class: wire.ClassState, Full: wire.FullLag})
	if err != nil {
		return nil, err
	}
	sb := &sub{p: p, ch: make(chan published, stateQueue), again: make(chan struct{}, 1)}
	c.mu.Lock()
	c.subs[s] = sb
	end := c.log.Seq()
	c.mu.Unlock()
	go c.feed(s, sb, wp, end)
	return nil, nil
}

func (c *Coord) feed(s *wire.Stream, sb *sub, wp WatchParams, end int64) {
	defer func() {
		c.mu.Lock()
		delete(c.subs, s)
		c.mu.Unlock()
	}()
	ctx := s.Context()
	var err error
	if c.replayable(wp.AfterSeq, end) {
		if err = s.PushWait(ctx, wire.PushOpen, wire.Open{Mode: wire.ModeResume}); err == nil {
			if err = c.replay(ctx, s, sb.p, wp.AfterSeq, end); err == nil {
				sb.aff = map[string]string{} // what the copy holds of them is not known: all of them, nulls too
				err = c.pushAfford(ctx, s, sb, nil)
			}
		}
	} else if err = s.PushWait(ctx, wire.PushOpen, wire.Open{Mode: wire.ModeSnapshot}); err == nil {
		end, err = c.snapshot(ctx, s, sb, wp.NoBriefs)
	}
	for err == nil {
		select {
		case <-ctx.Done():
			return
		case <-sb.again:
			err = c.pushAfford(ctx, s, sb, nil)
		case pub, ok := <-sb.ch:
			env := pub.env
			if !ok {
				c.mu.Lock()
				why := &wire.Error{Code: wire.CodeLagged}
				if sb.closed {
					why = &wire.Error{Code: wire.CodeGone}
				}
				c.mu.Unlock()
				s.End(nil, why)
				return
			}
			if env.Seq <= end {
				continue
			}
			if !reshapes(env) {
				if err = c.pushEnv(ctx, s, sb.p, env); err == nil && len(pub.ids) > 0 {
					err = c.pushAfford(ctx, s, sb, pub.ids)
				}
			} else if err = s.PushWait(ctx, PushReset, struct{}{}); err == nil {
				// the envelope itself only as part of the new snapshot: a copy at its seq is always one made after it
				end, err = c.snapshot(ctx, s, sb, wp.NoBriefs)
			}
		}
	}
	s.End(nil, err) // nothing when the stream has already ended
}

// replayable: the envelopes (after, end] can be replayed to bring a copy of the state at after up to date. They cannot
// when there is no copy, when the copy is not of this journal, when they are too many, or when one of them changed who
// sees what: what the copy holds may no longer be the viewer's.
func (c *Coord) replayable(after, end int64) bool {
	if after <= 0 || after > end || end-after > maxReplay {
		return false
	}
	ok := true
	if err := c.log.ReadAfter(after, end, func(env journal.Envelope) bool {
		ok = !reshapes(env)
		return ok
	}); err != nil {
		return false
	}
	return ok
}

func (c *Coord) replay(ctx context.Context, s *wire.Stream, p Principal, after, end int64) error {
	var perr error
	if err := c.log.ReadAfter(after, end, func(env journal.Envelope) bool {
		perr = c.pushEnv(ctx, s, p, env)
		return perr == nil
	}); err != nil {
		return err
	}
	return perr
}

// pushEnv sends p their part of env: every seq reaches them, empty when nothing in it is theirs.
func (c *Coord) pushEnv(ctx context.Context, s *wire.Stream, p Principal, env journal.Envelope) error {
	c.mu.Lock()
	v := c.visibleEnv(p, env)
	c.mu.Unlock()
	return s.PushWait(ctx, PushJournal, v)
}

// pushAfford pushes what changed in sb's viewer's affordances of tasks ids and their runs (nil: all they may see).
func (c *Coord) pushAfford(ctx context.Context, s *wire.Stream, sb *sub, ids []string) error {
	c.mu.Lock()
	now := c.affordances(sb.p, ids)
	c.mu.Unlock()
	if push, ok := affordDiff(sb.aff, now); ok {
		return s.PushWait(ctx, PushAffordances, push)
	}
	return nil
}

// snapshot sends what sb's viewer may see of the state now, table by table in batches of at most snapshotBatch bytes,
// each table at least once, then their affordances, then live; it returns the seq the snapshot is at.
func (c *Coord) snapshot(ctx context.Context, s *wire.Stream, sb *sub, noBriefs bool) (int64, error) {
	p := sb.p
	st := c.visibleState(p, c.state(!noBriefs))
	parts := []struct {
		name  string
		items map[string]any
	}{
		{task.PartProjects, anyMap(st.Projects)}, {task.PartTasks, anyMap(st.Tasks)}, {task.PartRuns, anyMap(st.Runs)},
		{task.PartShares, anyMap(st.Shares)}, {task.PartAgentDefs, anyMap(st.AgentDefs)},
	}
	for _, part := range parts {
		batch, size := map[string]json.RawMessage{}, 0
		flush := func() error {
			err := s.PushWait(ctx, PushSnapshot, Snapshot{Part: part.name, Items: batch})
			batch, size = map[string]json.RawMessage{}, 0
			return err
		}
		sent := false
		for _, id := range slices.Sorted(maps.Keys(part.items)) {
			b, err := json.Marshal(part.items[id])
			if err != nil {
				return 0, err
			}
			if size > 0 && size+len(b) > snapshotBatch {
				if err := flush(); err != nil {
					return 0, err
				}
				sent = true
			}
			batch[id] = b
			size += len(id) + len(b)
		}
		if size > 0 || !sent {
			if err := flush(); err != nil {
				return 0, err
			}
		}
	}
	c.mu.Lock()
	sb.aff = c.affordances(p, nil) // counted after the copy: never older than it
	c.mu.Unlock()
	items := map[string]json.RawMessage{}
	for k, v := range affordPart(sb.aff) {
		b, _ := json.Marshal(v)
		items[k] = b
	}
	if err := s.PushWait(ctx, PushSnapshot, Snapshot{Part: task.PartAffordances, Items: items}); err != nil {
		return 0, err
	}
	return st.Seq, s.PushWait(ctx, PushLive, Live{Seq: st.Seq})
}

// affordPart is the affordances by key as the snapshot part's items: runs and tasks, those with nothing to do left out.
func affordPart(aff map[string]string) map[string]map[string]json.RawMessage {
	out := map[string]map[string]json.RawMessage{"runs": {}, "tasks": {}}
	for k, v := range aff {
		if v != "" {
			out[map[byte]string{'t': "tasks", 'r': "runs"}[k[0]]][k[2:]] = json.RawMessage(v)
		}
	}
	return out
}

func anyMap[V any](m map[string]V) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// publish hands env and the tasks it touched to every state stream; one too slow to keep stateQueue envelopes ends as
// lagged. The caller holds mu.
func (c *Coord) publish(env journal.Envelope, ids []string) {
	for s, sb := range c.subs {
		select {
		case sb.ch <- published{env, ids}:
		default:
			delete(c.subs, s)
			close(sb.ch)
		}
	}
}

// StateFold is a client's copy of the state, folded from what state.watch pushes.
type StateFold struct {
	St      *task.State // nil until the first snapshot is live
	Aff     Affordances // what the viewer may do, as last pushed
	next    *task.State // the snapshot being received
	nextAff Affordances
}

// take merges an affordances push or part into a: a null entry takes one out.
func (a *Affordances) take(runs, tasks json.RawMessage) error {
	if a.Runs == nil {
		a.Runs, a.Tasks = map[string][]string{}, map[string]*TaskAffordance{}
	}
	var rs map[string][]string
	var ts map[string]*TaskAffordance
	if len(runs) > 0 {
		if err := json.Unmarshal(runs, &rs); err != nil {
			return err
		}
	}
	if len(tasks) > 0 {
		if err := json.Unmarshal(tasks, &ts); err != nil {
			return err
		}
	}
	for id, v := range rs {
		if v == nil {
			delete(a.Runs, id)
		} else {
			a.Runs[id] = v
		}
	}
	for id, v := range ts {
		if v == nil {
			delete(a.Tasks, id)
		} else {
			a.Tasks[id] = v
		}
	}
	return nil
}

// Params opens (or opens again) the stream from what the fold holds.
func (f *StateFold) Params(noBriefs bool) WatchParams {
	wp := WatchParams{NoBriefs: noBriefs}
	if f.St != nil {
		wp.AfterSeq = f.St.Seq
	}
	return wp
}

// Apply folds one push; changed when St is new or moved on. An error means the copy cannot follow: open the stream
// again without AfterSeq (Reset first).
func (f *StateFold) Apply(p wire.Push) (changed bool, err error) {
	switch p.Method {
	case wire.PushOpen:
		var o wire.Open
		if err := p.Decode(&o); err != nil {
			return false, err
		}
		f.next = nil
		if o.Mode != wire.ModeResume || f.St == nil {
			f.next, f.nextAff = task.New(), Affordances{}
		}
		return false, nil
	case PushReset:
		f.next, f.nextAff = task.New(), Affordances{}
		return false, nil
	case PushAffordances:
		var a struct{ Runs, Tasks json.RawMessage }
		if err := p.Decode(&a); err != nil {
			return false, err
		}
		if f.next != nil {
			return false, f.nextAff.take(a.Runs, a.Tasks)
		}
		return true, f.Aff.take(a.Runs, a.Tasks)
	case PushSnapshot:
		var sn Snapshot
		if err := p.Decode(&sn); err != nil {
			return false, err
		}
		if f.next == nil {
			return false, &wire.Error{Code: wire.CodeBadRequest, Detail: "snapshot outside a snapshot"}
		}
		if sn.Part == task.PartAffordances {
			return false, f.nextAff.take(sn.Items["runs"], sn.Items["tasks"])
		}
		return false, f.next.Take(sn.Part, sn.Items)
	case PushLive:
		var l Live
		if err := p.Decode(&l); err != nil {
			return false, err
		}
		if f.next == nil {
			return false, &wire.Error{Code: wire.CodeBadRequest, Detail: "live outside a snapshot"}
		}
		f.St, f.next = f.next, nil
		f.Aff, f.nextAff = f.nextAff, Affordances{}
		f.St.Seq = l.Seq
		return true, nil
	case PushJournal:
		var env journal.Envelope
		if err := p.Decode(&env); err != nil {
			return false, err
		}
		if f.St == nil || f.next != nil {
			return false, &wire.Error{Code: wire.CodeBadRequest, Detail: "journal before live"}
		}
		if env.Seq <= f.St.Seq {
			return false, nil
		}
		if env.Seq != f.St.Seq+1 {
			return false, &wire.Error{Code: wire.CodeBadRequest, Detail: "journal gap"}
		}
		return true, f.St.Apply(env)
	}
	return false, nil
}

// Reset forgets the copy: the next opening asks for a snapshot.
func (f *StateFold) Reset() { f.St, f.next, f.Aff = nil, nil, Affordances{} }
