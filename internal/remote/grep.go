package remote

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Grepped is one machine's answer in a message search across machines.
type Grepped struct {
	Machine string
	Result  GrepResult
	Err     error // wire.CodeUnknownMethod: its tend has no grep
}

// Grep runs a message search on name. A tend whose hello lists no grep is not sent it: unknown_method, with its
// version as the detail.
func (h *Hosts) Grep(ctx context.Context, name string, p GrepParams) (GrepResult, error) {
	var res GrepResult
	err := h.callIfKnown(ctx, name, MGrep, p, &res)
	return res, err
}

// Hits lists the messages of one session on name matching p's keywords, as Grep asks only a tend that has hits.
func (h *Hosts) Hits(ctx context.Context, name string, p HitsParams) (HitsResult, error) {
	var res HitsResult
	err := h.callIfKnown(ctx, name, MHits, p, &res)
	return res, err
}

// callIfKnown calls method on name when name's hello lists it and every method of also; an older tend is not sent it
// and answers unknown_method here, with its version as the detail.
func (h *Hosts) callIfKnown(ctx context.Context, name, method string, params, out any, also ...string) error {
	hello, err := h.t.Hello(ctx, name)
	if err != nil {
		return err
	}
	for _, m := range append([]string{method}, also...) {
		if !slices.Contains(hello.Methods, m) {
			return &wire.Error{Code: wire.CodeUnknownMethod, Detail: hello.Version}
		}
	}
	return h.Call(ctx, name, method, params, out)
}

// GrepWithin is Grep on name answered within wait; a machine still searching then is told as timed out.
func (h *Hosts) GrepWithin(ctx context.Context, name string, p GrepParams, wait time.Duration) Grepped {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	res, err := h.Grep(ctx, name, p)
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		err = &wire.Error{Code: wire.CodeTimeout}
	}
	return Grepped{Machine: name, Result: res, Err: err}
}

// GrepAll asks every one of names in parallel with its params, each within wait, and answers in names' order.
func (h *Hosts) GrepAll(ctx context.Context, names []string, params func(name string) GrepParams, wait time.Duration) []Grepped {
	out := make([]Grepped, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() { out[i] = h.GrepWithin(ctx, name, params(name), wait) })
	}
	wg.Wait()
	return out
}

// File is the fileio.ID of r's transcript as its machine last answered a read of it from the end; "" before one.
func (h *Hosts) File(r *tend.Rec) string {
	if h == nil || r.Host == "" {
		return ""
	}
	if k := h.file(r.Host+"\x00"+r.Key(), ""); k != "-" {
		return k
	}
	return ""
}

// ProjectDirsOn are the projects with a directory on machine and those directories, by project id: the context a
// query or grep gives that machine's node.
func ProjectDirsOn(projects map[string]*task.Project, machine string) []ProjectDirs {
	var out []ProjectDirs
	for _, id := range slices.Sorted(maps.Keys(projects)) {
		pr := projects[id]
		var dirs []string
		for _, r := range pr.Repos {
			if d := r.Dirs[machine]; d != "" {
				dirs = append(dirs, d)
			}
		}
		if len(dirs) > 0 {
			out = append(out, ProjectDirs{ID: pr.ID, Name: pr.Name, Dirs: dirs})
		}
	}
	return out
}

// Gap is why g does not cover its machine in full, as a short phrase for the UI; "" when it does.
func (g Grepped) Gap() string {
	switch {
	case wire.Code(g.Err) == wire.CodeUnknownMethod:
		return i18n.T("msg.far_old")
	case g.Err != nil:
		return Reason(g.Err)
	case g.Result.Building != nil:
		return i18n.F("msg.far_building", g.Result.Building.Done, g.Result.Building.Total)
	}
	return ""
}
