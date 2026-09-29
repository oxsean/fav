package node

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"sync"
)

// spool carries pieces of output from one goroutine to another and holds up to max bytes of them: its writer waits
// only when it is full, never on how slowly its reader goes.
type spool struct {
	mu     sync.Mutex
	cond   sync.Cond
	items  []piece
	n, max int
	closed bool
}

// piece is a line of output, or part of one too long to hold whole (more: the line goes on in the next).
type piece struct {
	b    []byte
	more bool
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

// readLines reads r into s a line at a time: whole up to maxLine, a longer one in pieces as they come.
func readLines(r io.Reader, s *spool) {
	defer s.Close()
	br := bufio.NewReaderSize(r, 64<<10)
	var line []byte
	long := false
	for {
		part, err := br.ReadSlice('\n')
		full := errors.Is(err, bufio.ErrBufferFull)
		switch {
		case long:
			s.put(bytes.Clone(part), full)
		case full && len(line)+len(part) < maxLine:
			line = append(line, part...)
		case full:
			s.put(append(line, part...), true)
			line, long = nil, true
		default:
			if l := append(line, part...); len(l) > 0 {
				s.put(l, false)
			}
			line = nil
		}
		if !full {
			long = false
			if err != nil {
				return
			}
		}
	}
}

func (s *spool) put(b []byte, more bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.n > 0 && s.n+len(b) > s.max {
		s.cond.Wait()
	}
	s.items = append(s.items, piece{b, more})
	s.n += len(b)
	s.cond.Broadcast()
}

// take is the next piece; ok false once the spool ended and is empty.
func (s *spool) take() (b []byte, more, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.items) == 0 && !s.closed {
		s.cond.Wait()
	}
	if len(s.items) == 0 {
		return nil, false, false
	}
	p := s.items[0]
	s.items = s.items[1:]
	s.n -= len(p.b)
	s.cond.Broadcast()
	return p.b, p.more, true
}

func (s *spool) Write(p []byte) (int, error) {
	s.put(bytes.Clone(p), false)
	return len(p), nil
}

// Close ends the spool: its reader gets what it holds, then the end.
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
	for len(s.items) == 0 && !s.closed {
		s.cond.Wait()
	}
	if len(s.items) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.items[0].b)
	if s.items[0].b = s.items[0].b[n:]; len(s.items[0].b) == 0 {
		s.items = s.items[1:]
	}
	s.n -= n
	s.cond.Broadcast()
	return n, nil
}
