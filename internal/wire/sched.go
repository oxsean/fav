package wire

import (
	"sync"
	"time"
)

// Class orders what a Conn writes: every waiting frame of a lower class goes before any of a higher one, and within a
// class the lanes (one per stream, one per bulk answer, one for everything else) take turns.
type Class int

const (
	ClassControl Class = iota // answers, requests, pings, cancels, pushes outside streams
	ClassState                // the state stream
	ClassStream               // output and snapshot streams
	ClassBulk                 // answers of Options.Bulk methods
	classes
)

type pending struct {
	b      []byte
	method string
	mark   any
	oob    bool       // an answer nobody waits for (ping, busy)
	done   chan error // nil: nobody waits for the write
}

func (o *pending) finish(err error) {
	if o.done != nil {
		o.done <- err
	}
}

// lane is one FIFO of frames.
type lane struct {
	class  Class
	q      []*pending
	bytes  int
	oob    int
	queued bool // in its class's ring
}

// dropMethod removes the queued frames of method and returns the mark of the first one.
func (l *lane) dropMethod(method string) (mark any, found bool) {
	kept := l.q[:0]
	for _, o := range l.q {
		if o.method != method {
			kept = append(kept, o)
			continue
		}
		if !found {
			mark, found = o.mark, true
		}
		l.bytes -= len(o.b)
	}
	clear(l.q[len(kept):])
	l.q = kept
	return mark, found
}

func (l *lane) dropAll() {
	clear(l.q)
	l.q, l.bytes = l.q[:0], 0
}

type sched struct {
	wmx    sync.Mutex
	rings  [classes][]*lane
	ctl    *lane
	maxOOB int
	kick   chan struct{}
	wstart int64 // unix nanos the write in progress began, 0 between writes; under wmx
}

func (s *sched) init(maxOOB int) {
	s.ctl, s.maxOOB, s.kick = &lane{class: ClassControl}, maxOOB, make(chan struct{}, 1)
}

// post queues o on l; false once the connection has ended.
func (c *Conn) post(l *lane, o *pending) bool {
	c.wmx.Lock()
	ok := c.putLocked(l, o)
	c.wmx.Unlock()
	if ok {
		c.wake()
	}
	return ok
}

func (c *Conn) putLocked(l *lane, o *pending) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	if o.oob {
		if l.oob >= c.maxOOB {
			return true
		}
		l.oob++
	}
	l.q = append(l.q, o)
	l.bytes += len(o.b)
	if !l.queued {
		l.queued = true
		c.rings[l.class] = append(c.rings[l.class], l)
	}
	return true
}

func (c *Conn) wake() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// pop takes the next frame to write, nil when nothing waits.
func (c *Conn) pop() *pending {
	c.wmx.Lock()
	defer c.wmx.Unlock()
	for cl := range c.rings {
		for len(c.rings[cl]) > 0 {
			l := c.rings[cl][0]
			c.rings[cl][0] = nil
			c.rings[cl] = c.rings[cl][1:]
			if len(l.q) == 0 { // emptied by a drop
				l.queued = false
				continue
			}
			o := l.q[0]
			l.q[0] = nil
			l.q = l.q[1:]
			l.bytes -= len(o.b)
			if o.oob {
				l.oob--
			}
			if len(l.q) > 0 {
				c.rings[cl] = append(c.rings[cl], l)
			} else {
				l.queued = false
			}
			c.wstart = time.Now().UnixNano()
			return o
		}
	}
	return nil
}

// writer is the one goroutine writing to the connection.
func (c *Conn) writer() {
	for {
		o := c.pop()
		if o == nil {
			select {
			case <-c.kick:
				continue
			case <-c.done:
				return
			}
		}
		_, err := c.rw.Write(o.b)
		c.wmx.Lock()
		c.wstart = 0
		c.wmx.Unlock()
		if err != nil {
			c.fail(&Error{Code: CodeClosed, Detail: err.Error()})
			o.finish(c.Err())
			return
		}
		o.finish(nil)
	}
}

// watchdog ends the connection when one write takes longer than WriteTimeout: the other end stopped reading, and
// half a line would break the stream.
func (c *Conn) watchdog() {
	t := time.NewTicker(max(c.opt.WriteTimeout/4, time.Millisecond))
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
		}
		c.wmx.Lock()
		began := c.wstart
		c.wmx.Unlock()
		if began != 0 && time.Since(time.Unix(0, began)) > c.opt.WriteTimeout {
			c.fail(&Error{Code: CodeTimeout, Detail: "write"})
			return
		}
	}
}
