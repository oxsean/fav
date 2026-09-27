package coord

import (
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/wire"
)

type SubscribeParams struct {
	AfterSeq int64 `json:"after_seq"`
}

// Subscribed: journal pushes follow for every envelope after the one asked for; Seq is the journal's end when the
// subscription began.
type Subscribed struct {
	Seq int64 `json:"seq"`
}

type sub struct {
	p  Principal
	ch chan journal.Envelope
}

// subscribe registers the live feed under mu, then replays (after, end] from the file outside it and moves on to the
// live envelopes past end. A client too slow to keep subscribeQueue envelopes is disconnected; it subscribes again
// from the last seq it has.
func (c *Coord) subscribe(p Principal, r *wire.Request) (any, error) {
	var sp SubscribeParams
	if err := r.Decode(&sp); err != nil {
		return nil, err
	}
	conn := r.Conn
	sp.AfterSeq = max(sp.AfterSeq, 0)
	c.mu.Lock()
	if _, ok := c.subs[conn]; ok {
		c.mu.Unlock()
		return nil, conflict("subscribed")
	}
	s := &sub{p: p, ch: make(chan journal.Envelope, subscribeQueue)}
	c.subs[conn] = s
	end := c.log.Seq()
	c.mu.Unlock()
	go c.feed(conn, s, sp.AfterSeq, end)
	return Subscribed{Seq: end}, nil
}

func (c *Coord) feed(conn *wire.Conn, s *sub, after, end int64) {
	defer func() {
		c.mu.Lock()
		if c.subs[conn] == s {
			delete(c.subs, conn)
		}
		c.mu.Unlock()
	}()
	if after < end {
		var perr error
		if err := c.log.ReadAfter(after, end, func(env journal.Envelope) bool {
			perr = c.push(conn, s.p, env)
			return perr == nil
		}); err != nil || perr != nil {
			conn.Close()
			return
		}
	}
	for {
		select {
		case <-conn.Done():
			return
		case env, ok := <-s.ch:
			if !ok {
				conn.Close()
				return
			}
			if env.Seq <= end {
				continue
			}
			if c.push(conn, s.p, env) != nil {
				return
			}
		}
	}
}

// push sends p their part of env, and asks them to fetch the state again when env changed what they may see.
func (c *Coord) push(conn *wire.Conn, p Principal, env journal.Envelope) error {
	c.mu.Lock()
	v := c.visibleEnv(p, env)
	c.mu.Unlock()
	if err := conn.Push(PushJournal, v); err != nil {
		return err
	}
	if reshapes(env) {
		return conn.Push(PushRefetch, nil)
	}
	return nil
}

// publish hands env to every subscriber; the caller holds mu.
func (c *Coord) publish(env journal.Envelope) {
	for conn, s := range c.subs {
		select {
		case s.ch <- env:
		default:
			delete(c.subs, conn)
			close(s.ch)
		}
	}
}
