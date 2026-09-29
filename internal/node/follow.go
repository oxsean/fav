package node

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/wire"
)

// MRunFollow streams a run's output as it is written (FollowParams): open, then PushFollow pushes (Follow), until the
// run has ended and all of it went out (wire.EndDone).
const (
	MRunFollow = "run.follow.watch"
	PushFollow = "run.follow"
)

// Cursor is where a follow of a run's output stands: the log file and offset the next line starts at, and where the
// next read of marks.jsonl starts (0 with a File or an Off: the marks placed from there on).
type Cursor struct {
	File  string `json:"file"`
	Off   int64  `json:"off"`
	Marks int64  `json:"marks,omitempty"`
}

type FollowParams struct {
	Run  string `json:"run"`
	From Cursor `json:"from"` // File "": the current log, from Off
}

// Follow is one push of a follow: what came in one log file since the last push.
type Follow struct {
	File   string     `json:"file"`
	Gap    *Gap       `json:"gap,omitempty"`   // before the lines: a stretch no longer kept
	Lines  []TailLine `json:"lines,omitempty"` // whole lines, each with At
	Part   *TailLine  `json:"part,omitempty"`  // the line still being written, once it waited halfLineWait; Cursor is before it
	Marks  []Mark     `json:"marks,omitempty"` // new in marks.jsonl, wherever they are placed
	Cursor Cursor     `json:"cursor"`
}

type Gap struct {
	From Cursor `json:"from"`
	To   Cursor `json:"to"`
}

// follow reads one run's output for a stream: every followEvery it opens the log, reads what is new and closes it
// again, so a Windows roll never finds it held.
type follow struct {
	n        *Node
	run, dir string
	s        *wire.Stream
	at       Cursor
	since    *Cursor // the first read of the marks keeps those placed from here on
	part     struct {
		off   int64
		since time.Time
		sent  string
	}
}

// Follow opens a follow of p.Run's output on r; it pushes after the handler returned.
func (n *Node) Follow(r *wire.Request, p FollowParams) error {
	if !runID.MatchString(p.Run) || p.From.Off < 0 || p.From.Marks < 0 {
		return &wire.Error{Code: wire.CodeBadRequest, Detail: "follow"}
	}
	dir := n.runDir(p.Run)
	if _, err := os.Stat(dir); err != nil {
		return &wire.Error{Code: wire.CodeNotFound, Detail: p.Run}
	}
	s, err := r.Stream(wire.StreamOptions{Class: wire.ClassStream, Full: wire.FullLag})
	if err != nil {
		return err
	}
	f := &follow{n: n, run: p.Run, dir: dir, s: s, at: p.From}
	if p.From.Marks == 0 && (p.From.File != "" || p.From.Off > 0) {
		since := p.From
		f.since = &since
	}
	go f.loop()
	return nil
}

func (f *follow) logPath() string { return filepath.Join(f.dir, "output.log") }

func (f *follow) loop() {
	ctx := f.s.Context()
	open := wire.Open{Mode: wire.ModeResume}
	if cur, old := fileio.ID(f.logPath()), fileio.ID(f.logPath()+".1"); f.at.File != "" && f.at.File != cur && f.at.File != old {
		to := Cursor{File: cmp.Or(old, cur), Marks: f.at.Marks}
		open = wire.Open{Mode: wire.ModeGap, From: f.at, To: Cursor{File: to.File}}
		f.at = to
	}
	open.Cursor = f.at
	if err := f.s.PushWait(ctx, wire.PushOpen, open); err != nil {
		return
	}
	t := time.NewTicker(followEvery)
	defer t.Stop()
	for ended := false; ; {
		if err := f.pass(ctx, ended); err != nil {
			f.s.End(nil, err)
			return
		}
		if ended {
			f.s.End(wire.EndDone, nil)
			return
		}
		snap, err := f.n.Snapshot(f.run)
		if err != nil {
			f.s.End(nil, &wire.Error{Code: wire.CodeGone, Detail: f.run})
			return
		}
		if ended = Terminal(snap.State.State) || snap.State.State == StateUnknown; ended {
			continue // one more pass takes what came last
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// pass pushes all that is new: the rest of a log that rolled, the current log's whole lines in pushes of about
// maxFollowPush bytes, the line being written once it waited (at once when the run ended), and new marks.
func (f *follow) pass(ctx context.Context, ended bool) error {
	for {
		cur, old := fileio.ID(f.logPath()), fileio.ID(f.logPath()+".1")
		if cur == "" { // not written yet
			if _, err := os.Stat(f.dir); err != nil {
				return &wire.Error{Code: wire.CodeGone, Detail: f.run}
			}
			return f.push(ctx, Follow{File: f.at.File, Marks: f.newMarks()})
		}
		if f.at.File == "" {
			f.at.File = cur
		}
		var name string
		switch f.at.File {
		case cur:
			name = f.logPath()
		case old:
			name = f.logPath() + ".1"
		default: // it rolled twice since the last look: what it held is gone
			gap := &Gap{From: f.at, To: Cursor{File: cur}}
			f.at.File, f.at.Off, f.part.since = cur, 0, time.Time{}
			if err := f.push(ctx, Follow{File: cur, Gap: gap}); err != nil {
				return err
			}
			continue
		}
		fl, more, err := f.read(name, f.at.File == cur, ended)
		switch {
		case errors.Is(err, errMoved): // rolled between the look and the open
			continue
		case errors.Is(err, errCut): // shorter than read: it starts again
			gap := &Gap{From: f.at, To: Cursor{File: cur}}
			f.at.Off, f.part.since = 0, time.Time{}
			if err := f.push(ctx, Follow{File: cur, Gap: gap}); err != nil {
				return err
			}
			continue
		case err != nil:
			return err
		}
		if err := f.push(ctx, fl); err != nil {
			return err
		}
		if more {
			continue
		}
		if f.at.File == cur {
			return nil
		}
		f.at.File, f.at.Off, f.part.since = cur, 0, time.Time{} // the old log read to its end
	}
}

var (
	errMoved = errors.New("moved")
	errCut   = errors.New("cut")
)

// read reads the log file name from f.at: whole lines up to about maxFollowPush bytes (more: it stopped there), the
// line being written when current, the new marks.
func (f *follow) read(name string, current, ended bool) (fl Follow, more bool, err error) {
	fh, err := os.Open(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fl, false, errMoved
		}
		return fl, false, err
	}
	defer fh.Close()
	if fileio.IDOf(fh) != f.at.File {
		return fl, false, errMoved
	}
	fi, err := fh.Stat()
	if err != nil {
		return fl, false, err
	}
	if fi.Size() < f.at.Off {
		return fl, false, errCut
	}
	if _, err := fh.Seek(f.at.Off, io.SeekStart); err != nil {
		return fl, false, err
	}
	now := time.Now().Format(time.RFC3339Nano)
	br := bufio.NewReaderSize(fh, 64<<10)
	sent := 0
	var partial []byte
	for sent < maxFollowPush {
		l, text, whole, err := readLine(br, f.at.Off)
		if err != nil {
			return fl, false, err
		}
		if !whole {
			partial = text
			break
		}
		l.At = now
		fl.Lines = append(fl.Lines, l)
		sent += len(l.Text)
		f.at.Off += lineSize(l)
	}
	more = sent >= maxFollowPush
	if len(fl.Lines) > 0 {
		f.part.since = time.Time{}
	}
	if current && !more {
		fl.Part = f.halfLine(f.at.Off, partial, ended)
	}
	fl.File, fl.Marks = f.at.File, f.newMarks()
	return fl, more, nil
}

// lineSize is how much of the log l takes.
func lineSize(l TailLine) int64 {
	if l.Size > 0 {
		return l.Size
	}
	return int64(len(l.Text))
}

// readLine reads the line at off whole, as a TailLine cut the way a page's lines are (clipLine; the longest sent as
// only their head). whole false: it has no newline yet, and text is what there is of it (nil past maxSentField).
func readLine(br *bufio.Reader, off int64) (l TailLine, text []byte, whole bool, err error) {
	var b []byte
	var size int64
	for {
		part, err := br.ReadSlice('\n')
		size += int64(len(part))
		if len(b) <= maxSentLine {
			b = append(b, part...)
		}
		if err == nil {
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err == io.EOF {
			if len(b) > maxSentField {
				b = nil
			}
			return TailLine{}, b, false, nil
		}
		return TailLine{}, nil, false, err
	}
	l = TailLine{Off: off}
	if size > maxSentLine+1 {
		l.Text, l.Size, l.Head = string(runeHead(b, maxSentField)), size, true
		return l, nil, true, nil
	}
	sent, head := clipLine(bytes.TrimSuffix(b, []byte("\n")))
	switch {
	case head:
		l.Text, l.Size, l.Head = string(sent), size, true
	case int64(len(sent)+1) != size:
		l.Text, l.Size = string(sent)+"\n", size
	default:
		l.Text = string(b)
	}
	return l, nil, true, nil
}

// halfLine is the line being written at off, once it waited halfLineWait (at once when the run ended) and changed
// since it was last sent.
func (f *follow) halfLine(off int64, text []byte, ended bool) *TailLine {
	if len(bytes.TrimSpace(text)) == 0 {
		f.part.since = time.Time{}
		return nil
	}
	if f.part.since.IsZero() || f.part.off != off {
		f.part.off, f.part.since, f.part.sent = off, time.Now(), ""
	}
	if !ended && time.Since(f.part.since) < halfLineWait || string(text) == f.part.sent {
		return nil
	}
	f.part.sent = string(text)
	return &TailLine{Off: off, Text: string(text)}
}

// newMarks reads the marks written since the last read. The first read of a follow opened at a place without its
// marks' own keeps those placed from there on: in its file from its offset, and all in the files after it.
func (f *follow) newMarks() []Mark {
	ms, next := readMarks(f.dir, f.at.Marks)
	f.at.Marks = next
	since := f.since
	if since == nil {
		return ms
	}
	f.since = nil
	seen := false
	for i, m := range ms {
		if m.File == since.File {
			seen = true
			if m.Off >= since.Off {
				return ms[i:]
			}
		} else if seen || m.File == f.at.File && since.File != f.at.File {
			return ms[i:]
		}
	}
	return nil
}

func (f *follow) push(ctx context.Context, fl Follow) error {
	if fl.Gap == nil && len(fl.Lines) == 0 && fl.Part == nil && len(fl.Marks) == 0 {
		return nil
	}
	fl.Cursor = f.at
	return f.s.PushWait(ctx, PushFollow, fl)
}
