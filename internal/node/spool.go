package node

import (
	"bytes"
	"io"
	"sync"
)

// spool carries bytes from one goroutine to another and holds up to max of them: its writer waits only when it is
// full, never on how slowly its reader goes.
type spool struct {
	mu     sync.Mutex
	cond   sync.Cond
	bufs   [][]byte
	n, max int
	closed bool
}

func newSpool(max int) *spool {
	s := &spool{max: max}
	s.cond.L = &s.mu
	return s
}

// spooled is r read into a spool as fast as r gives it; the spool ends when r does.
func spooled(pipes *sync.WaitGroup, r io.Reader, max int) io.Reader {
	s := newSpool(max)
	pipes.Add(1)
	go func() {
		defer pipes.Done()
		defer s.Close()
		io.Copy(s, r)
	}()
	return s
}

func (s *spool) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.n > 0 && s.n+len(p) > s.max {
		s.cond.Wait()
	}
	s.bufs = append(s.bufs, bytes.Clone(p))
	s.n += len(p)
	s.cond.Broadcast()
	return len(p), nil
}

// Close ends the spool: its reader gets what it holds, then io.EOF.
func (s *spool) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.cond.Broadcast()
	return nil
}

func (s *spool) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.bufs) == 0 && !s.closed {
		s.cond.Wait()
	}
	if len(s.bufs) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.bufs[0])
	if s.bufs[0] = s.bufs[0][n:]; len(s.bufs[0]) == 0 {
		s.bufs = s.bufs[1:]
	}
	s.n -= n
	s.cond.Broadcast()
	return n, nil
}
