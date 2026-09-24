package remote

import (
	"context"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
)

// Source reads one record's transcript and checks, from this machine's files or through the record's host. Every
// reader of a transcript on behalf of a record (right pane, full message, steps, pulse, resume checks) goes through it.
type Source interface {
	Messages(before int64, n int) capture.Page
	TextFull(off int64, fallback string) string
	StepsFull(steps []capture.Step) []string
	Pulse() (capture.Pulse, bool)
	Checks() []capture.Check
}

// Local reads r's own files; it keeps a copy of r, so it can run off the UI goroutine.
func Local(r *fav.Rec) Source {
	cp := *r
	return local{&cp}
}

type local struct{ r *fav.Rec }

func (l local) path() string {
	if ts := l.r.Transcripts(); len(ts) > 0 {
		return ts[0]
	}
	return ""
}

func (l local) Messages(before int64, n int) capture.Page {
	if p := l.path(); p != "" {
		return capture.Messages(p, before, n)
	}
	return capture.Page{Done: true}
}

func (l local) TextFull(off int64, fallback string) string {
	return capture.TextFull(l.path(), off, fallback)
}

func (l local) StepsFull(steps []capture.Step) []string { return capture.StepsFull(l.path(), steps) }

func (l local) Pulse() (capture.Pulse, bool) {
	if p := l.path(); p != "" {
		return capture.ReadPulse(p)
	}
	return capture.Pulse{}, false
}

func (l local) Checks() []capture.Check { return capture.Checks(l.r) }

// callTimeout bounds one read of a remote transcript.
const callTimeout = 20 * time.Second

type far struct {
	h   *Hosts
	ref Ref
	on  string
}

func (f far) call(method string, params, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return f.h.Call(ctx, f.on, method, params, out)
}

// Messages from the end learns which file the transcript is; the offsets of later pages, full texts and steps are
// asked about that file only. A rewritten transcript answers CodeStale once: read it again from the end.
func (f far) Messages(before int64, n int) capture.Page {
	key := f.on + "\x00" + fav.SessionKey(f.ref.Provider, f.ref.SessionID)
	known := f.h.file(key, "")
	params := MessagesParams{Ref: f.ref, Before: before, N: n}
	if before >= 0 {
		params.File = known
	}
	var p capture.Page
	if err := f.call(MMessages, params, &p); err != nil {
		if Stale(err) {
			f.h.file(key, "-")
		}
		return capture.Page{From: before, Err: err}
	}
	if before < 0 {
		f.h.file(key, p.File)
		if known != "" && known != "-" && known != p.File {
			return capture.Page{From: before, Err: &Error{Code: CodeStale}}
		}
	}
	for i := range p.Msgs {
		p.Msgs[i].At = p.Msgs[i].At.Local()
	}
	return p
}

func (f far) known() string {
	if k := f.h.file(f.on+"\x00"+fav.SessionKey(f.ref.Provider, f.ref.SessionID), ""); k != "-" {
		return k
	}
	return ""
}

func (f far) TextFull(off int64, fallback string) string {
	var t Text
	if f.call(MText, TextParams{Ref: f.ref, Off: off, Fallback: fallback, File: f.known()}, &t) != nil {
		return fallback
	}
	return t.Text
}

func (f far) StepsFull(steps []capture.Step) []string {
	var s Steps
	if f.call(MSteps, StepsParams{Ref: f.ref, Steps: steps, File: f.known()}, &s) != nil {
		out := make([]string, len(steps))
		for i, st := range steps {
			out[i] = st.Text
		}
		return out
	}
	return s.Texts
}

func (f far) Pulse() (capture.Pulse, bool) {
	var p PulseResult
	if f.call(MPulse, f.ref, &p) != nil {
		return capture.Pulse{}, false
	}
	p.Pulse.TurnAt, p.Pulse.ModTime = p.Pulse.TurnAt.Local(), p.Pulse.ModTime.Local()
	return p.Pulse, p.OK
}

func (f far) Checks() []capture.Check {
	var c Checks
	if err := f.call(MChecks, f.ref, &c); err != nil {
		return []capture.Check{{Text: i18n.F("remote.unreachable", f.on, Reason(err))}}
	}
	return c.Checks
}
