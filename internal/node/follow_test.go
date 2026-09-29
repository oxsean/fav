package node

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/wire"
)

// followRun is a run directory written by hand: its supervisor is the test, holding the lock while it "runs".
type followRun struct {
	t      *testing.T
	n      *Node
	id     string
	dir    string
	unlock func()
}

func newFollowRun(t *testing.T) *followRun {
	t.Helper()
	n := New(t.TempDir())
	r := &followRun{t: t, n: n, id: "r_0123abcd"}
	r.dir = n.runDir(r.id)
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	unlock, err := filelock.TryLock(filepath.Join(r.dir, "lock"))
	if err != nil {
		t.Fatal(err)
	}
	r.unlock = unlock
	t.Cleanup(func() { r.end() })
	if err := writeJSON(filepath.Join(r.dir, "state.json"), State{Rev: 1, State: StateRunning}); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *followRun) write(s string) {
	r.t.Helper()
	f, err := os.OpenFile(filepath.Join(r.dir, "output.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		r.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		r.t.Fatal(err)
	}
}

func (r *followRun) log() string { return fileio.ID(filepath.Join(r.dir, "output.log")) }

func (r *followRun) mark(m Mark) {
	r.t.Helper()
	m.Type, m.At = "tend", time.Now()
	if err := appendLine(filepath.Join(r.dir, marksFile), m); err != nil {
		r.t.Fatal(err)
	}
}

func (r *followRun) roll() {
	r.t.Helper()
	if err := fileio.Rename(filepath.Join(r.dir, "output.log"), filepath.Join(r.dir, "output.log.1")); err != nil {
		r.t.Fatal(err)
	}
}

func (r *followRun) end() {
	if r.unlock != nil {
		writeJSON(filepath.Join(r.dir, "state.json"), State{Rev: 2, State: StateExited})
		r.unlock()
		r.unlock = nil
	}
}

func (r *followRun) follow(from Cursor) *wire.Watch {
	r.t.Helper()
	a, b := wire.Pipe(wire.Options{}, wire.Options{Handler: r.n.Handler(nil)})
	r.t.Cleanup(func() { a.Close(); b.Close() })
	return a.Watch(context.Background(), MRunFollow, FollowParams{Run: r.id, From: from})
}

// next is the next push of w, decoded into v; it fails the test after a few seconds.
func next(t *testing.T, w *wire.Watch, v any) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := w.Next(ctx)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if err := p.Decode(v); err != nil {
		t.Fatal(err)
	}
	return p.Method
}

// lines gathers followed lines until want of them came, with the pushes' last cursor.
func lines(t *testing.T, w *wire.Watch, want int) ([]TailLine, []Mark, Cursor) {
	t.Helper()
	var got []TailLine
	var ms []Mark
	var at Cursor
	for len(got) < want {
		var fl Follow
		if m := next(t, w, &fl); m != PushFollow {
			t.Fatalf("push %s", m)
		}
		got, ms, at = append(got, fl.Lines...), append(ms, fl.Marks...), fl.Cursor
	}
	return got, ms, at
}

func TestFollowSendsWholeLinesAsTheyComeAndEndsWithTheRun(t *testing.T) {
	r := newFollowRun(t)
	r.write("one\ntwo\n")
	r.mark(Mark{Event: markStart, File: r.log(), Off: 0})
	w := r.follow(Cursor{})
	var open wire.Open
	if m := next(t, w, &open); m != wire.PushOpen || open.Mode != wire.ModeResume {
		t.Fatalf("%s %+v", m, open)
	}
	got, ms, at := lines(t, w, 2)
	if got[0].Text != "one\n" || got[1].Off != 4 || got[1].At == "" || at.Off != 8 || at.File != r.log() {
		t.Fatalf("%+v at %+v", got, at)
	}
	if len(ms) != 1 || ms[0].Event != markStart {
		t.Fatalf("marks %+v", ms)
	}
	r.write("thr")
	var fl Follow
	next(t, w, &fl)
	if fl.Part == nil || fl.Part.Text != "thr" || fl.Part.Off != 8 || fl.Cursor.Off != 8 {
		t.Fatalf("a line still written goes out once it waited, not past the cursor: %+v", fl)
	}
	r.write("ee\n")
	r.mark(Mark{Event: markExit, File: r.log(), Off: 14})
	got, _, at = lines(t, w, 1)
	if got[0].Text != "three\n" || got[0].Off != 8 || at.Off != 14 {
		t.Fatalf("%+v at %+v", got, at)
	}
	r.end()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		p, err := w.Next(ctx)
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("a follow of an ended run finishes: %v", err)
		}
		var fl Follow
		p.Decode(&fl)
		if len(fl.Lines) > 0 {
			t.Fatalf("nothing more was written: %+v", fl)
		}
	}
}

func TestFollowReadsTheOldLogToItsEndAcrossARoll(t *testing.T) {
	r := newFollowRun(t)
	r.write("a\nb\n")
	first := r.log()
	w := r.follow(Cursor{File: first, Off: 2})
	var open wire.Open
	next(t, w, &open)
	got, _, _ := lines(t, w, 1)
	if got[0].Text != "b\n" {
		t.Fatalf("%+v", got)
	}
	r.write("c\n")
	r.roll()
	r.write("d\n")
	got, _, at := lines(t, w, 2)
	if got[0].Text != "c\n" || got[1].Text != "d\n" || got[1].Off != 0 || at.File == first || at.File != r.log() || at.Off != 2 {
		t.Fatalf("the rest of the old log, then the new one from its start: %+v at %+v", got, at)
	}
}

func TestFollowResumesInTheLogBeforeAndGapsWhereNoneIsKept(t *testing.T) {
	r := newFollowRun(t)
	r.write("a\nb\n")
	first := r.log()
	r.mark(Mark{Event: markHook, Phase: phaseBegin, File: first, Off: 2})
	r.roll()
	r.write("c\n")
	w := r.follow(Cursor{File: first, Off: 2})
	var open wire.Open
	next(t, w, &open)
	got, ms, _ := lines(t, w, 2)
	if got[0].Text != "b\n" || got[1].Text != "c\n" {
		t.Fatalf("%+v", got)
	}
	if len(ms) != 1 || ms[0].Event != markHook || ms[0].Pos != 0 {
		t.Fatalf("the marks from the place it opened at: %+v", ms)
	}

	w = r.follow(Cursor{File: "gone:1", Off: 9, Marks: 1 << 20})
	if next(t, w, &open); open.Mode != wire.ModeGap {
		t.Fatalf("a log no longer kept: %+v", open)
	}
	b, _ := json.Marshal(open.Cursor)
	if !strings.Contains(string(b), `"off":0`) || !strings.Contains(string(b), first) {
		t.Fatalf("it goes on from the oldest log kept: %s", b)
	}
}
