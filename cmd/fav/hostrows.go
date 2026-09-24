package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/herdr"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
)

// remoteHosts: the configured machines, reached on first use (tests swap it for a fake).
var remoteHosts = sync.OnceValue(func() *remote.Hosts {
	cfg := fav.LoadConfig()
	return remote.NewHosts(cfg.Hosts, i18n.Resolve(cfg.Lang))
})

// hostTimeout bounds reaching a host and fetching its list.
const hostTimeout = 20 * time.Second

// cachedFor: a host list fetched this recently is used as it is (fzf lists again on every keystroke); 0 always fetches.
var cachedFor time.Duration

// hostName is the configured host called name (any case), "" when there is none.
func hostName(h *remote.Hosts, name string) string {
	for _, n := range h.Names() {
		if strings.EqualFold(n, name) {
			return n
		}
	}
	return ""
}

// hostsOf: the configured hosts q selects; none without host:, every one for host:all.
func hostsOf(h *remote.Hosts, q fav.Query) []string {
	switch q.Host {
	case "", fav.HostLocal:
		return nil
	case fav.HostAll:
		return h.Names()
	}
	if n := hostName(h, q.Host); n != "" {
		return []string{n}
	}
	fmt.Fprintln(os.Stderr, i18n.F("cli.hosts.unknown", q.Host, strings.Join(h.Names(), " ")))
	return nil
}

// hostRows are the rows q selects on other machines, fetched in parallel; a host that cannot answer gives its cached
// rows and one line on stderr. live: who runs on each host, by host name (status:live only).
func hostRows(q fav.Query) ([]*fav.Rec, map[string]map[string]capture.Live) {
	if q.Status == fav.StatusTrash || q.Status == fav.StatusAgent {
		return nil, nil
	}
	h := remoteHosts()
	names := hostsOf(h, q)
	type answer struct {
		recs    []*fav.Rec
		st      remote.State
		live    map[string]capture.Live
		liveErr error
	}
	answers := make([]answer, len(names))
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() {
			a := &answers[i]
			a.recs, a.st = h.Cached(name)
			tried, failed := h.Failed(name)
			switch {
			case cachedFor > 0 && failed != nil && time.Since(tried) < cachedFor: // down a moment ago: not again per keystroke
				a.st.Err = failed
			case cachedFor == 0 || a.st.At.IsZero() || time.Since(a.st.At) > cachedFor:
				a.recs, a.st = h.Sessions(ctx, name)
			}
			if q.Status == fav.StatusLive && a.st.Err == nil {
				var at time.Time
				if a.live, at = h.CachedLive(name); cachedFor == 0 || at.IsZero() || time.Since(at) > cachedFor {
					a.live, a.liveErr = h.Live(ctx, name)
				}
			}
		})
	}
	wg.Wait()
	var out []*fav.Rec
	live := make(map[string]map[string]capture.Live, len(names))
	for i, name := range names {
		a := answers[i]
		switch {
		case a.st.Err != nil && !a.st.At.IsZero():
			fmt.Fprintln(os.Stderr, i18n.F("cli.host.cached", name, remote.Reason(a.st.Err), render.WhenFull(a.st.At)))
		case cmp.Or(a.st.Err, a.liveErr) != nil:
			fmt.Fprintln(os.Stderr, i18n.F("remote.unreachable", name, remote.Reason(cmp.Or(a.st.Err, a.liveErr))))
		}
		hq := q
		hq.Live = func(id string) bool { _, ok := a.live[id]; return ok }
		for _, r := range a.recs {
			if hq.Match(r) {
				out = append(out, r)
			}
		}
		live[name] = a.live
	}
	return out, live
}

// pickHost resolves host:ref (a configured host, then a record id or session id prefix there); ok is false when ref
// names no configured host. A full key found in the host's cached list is taken from there without reaching it.
func pickHost(ref string) (r *fav.Rec, ok bool, err error) {
	name, sub, found := strings.Cut(ref, ":")
	if !found {
		return nil, false, nil
	}
	h := remoteHosts()
	if name = hostName(h, name); name == "" {
		return nil, false, nil
	}
	if sub == "" {
		return nil, true, errors.New(i18n.T("cli.missing_id"))
	}
	cached, _ := h.Cached(name)
	if r, n := matchRef(cached, sub, recKeys); n == 1 && (r.SessionID == sub || r.ID == sub) {
		return r, true, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	defer cancel()
	recs, st := h.Sessions(ctx, name)
	if r, n := matchRef(recs, sub, recKeys); n > 0 {
		return r, true, refErr(ref, n)
	}
	if st.Err != nil {
		return nil, true, errors.New(i18n.F("remote.unreachable", name, remote.Reason(st.Err)))
	}
	return nil, true, refErr(ref, 0)
}

// pickLocal is pick for commands that write: another machine's records are read-only here.
func pickLocal(s *fav.Store, ref string) (*fav.Rec, error) {
	r, err := pick(s, ref)
	if err == nil && r.Host != "" {
		return nil, readOnly(r)
	}
	return r, err
}

func readOnly(r *fav.Rec) error { return i18n.E("cli.remote.read_only", r.Host) }

// remoteResume is `ssh -t <host> fav resume …` for r, as a command spec.
func remoteResume(r *fav.Rec) (capture.CommandSpec, error) {
	cmd, ok := remoteHosts().ResumeCommand(r)
	if !ok {
		return capture.CommandSpec{}, i18n.E("cli.host.unknown", r.Host)
	}
	return capture.CommandSpec{Exec: cmd.Args[0], Args: cmd.Args[1:]}, nil
}

// resumeRemote resumes r on its host over ssh: in a new tab of this Herdr workspace when fav runs inside Herdr
// (workspace: another one, by label), else in this terminal.
func resumeRemote(r *fav.Rec, dryRun, noHerdr bool, workspace string) error {
	spec, err := remoteResume(r)
	if err != nil {
		return err
	}
	plan := capture.Plan{Spec: spec}
	if !noHerdr && herdr.Active() {
		plan.Ws = herdrWorkspace(workspace)
	}
	if dryRun {
		plan.Checks = remoteHosts().Source(r).Checks()
		printPlan(r, plan, true)
		return nil
	}
	if plan.Ws != nil {
		msg, warn, err := plan.RunInHerdr(r)
		if err == nil {
			if warn != nil {
				fmt.Fprint(os.Stderr, i18n.F("cli.resume.warn", warn))
			}
			fmt.Println(msg)
			return nil
		}
		fmt.Fprint(os.Stderr, i18n.F("cli.resume.herdr_failed", err))
	}
	return resumeHere(spec)
}

// herdrWorkspace is the Herdr workspace labelled label, or the one this pane is in.
func herdrWorkspace(label string) *herdr.Workspace {
	if label != "" {
		ws, _ := herdr.FindWorkspace(label)
		return ws
	}
	pane, err := herdr.CurrentPane()
	if err != nil {
		return nil
	}
	ws, _ := herdr.Workspaces()
	for i := range ws {
		if ws[i].WorkspaceID == pane.WorkspaceID {
			return &ws[i]
		}
	}
	return nil
}
