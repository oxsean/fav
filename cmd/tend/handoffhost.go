package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/envcheck"
	"github.com/oxsean/fav/internal/herdr"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// handoffTo is how `tend handoff --host` opens the new session: in a terminal with a provider, as a task, printed
// only, or (none of them) written there for `tend handoff --open`.
type handoffTo struct {
	host, dir, provider string
	task, print         bool
	agent, project      string
	dryRun, noHerdr     bool
	workspace           string
}

// handoffHost hands r to a new session on another machine: its facts read where it is, the pack written there.
func handoffHost(s *tend.Store, ref string, o handoffTo) error {
	r, err := pick(s, ref)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	defer cancel()
	from, err := handoffPeer(ctx, r.Host)
	if err != nil {
		return err
	}
	to, err := handoffPeer(ctx, o.host)
	if err != nil {
		return err
	}
	x, err := remote.Handoff(ctx, from, to, remote.Ref{Provider: r.Provider, SessionID: r.SessionID})
	if err != nil {
		return handoffRefused(from, cmp.Or(r.Host, tend.HostLocal), remote.MHandoffFacts, err)
	}
	dir := o.dir
	if dir == "" {
		if dir, err = handoffDir(ctx, x, r, o.host, handoffDirTexts); err != nil {
			return err
		}
	}
	if dir != "" && !pathmap.Abs(dir) {
		return i18n.E("cli.handoff.dir_abs", dir)
	}
	if !x.From.Same(x.To) {
		handoffEnv(ctx, x, dir, o.host)
	}
	text := x.Text(dir)
	switch {
	case o.print:
		fmt.Print(text)
		return nil
	case o.task:
		return handoffTask(r, x, text, dir, o)
	}
	provider := cmp.Or(o.provider, r.Provider)
	put, err := x.Put(ctx, text, dir, provider)
	if err != nil {
		return handoffRefused(x.To, o.host, remote.MHandoffPut, err)
	}
	fmt.Fprint(os.Stderr, i18n.F("cli.handoff.put", o.host, put.Path))
	if o.provider == "" {
		fmt.Println(handoffOpenLine(put.ID))
		return nil
	}
	if to.Name == "" {
		return handoffOpen(s, put.ID, o.dryRun, o.noHerdr, o.workspace)
	}
	return handoffThere(o.host, put.ID, r.Title, o)
}

func handoffOpenLine(id string) string { return "tend handoff --open " + id }

// handoffPeer is the machine name: this one for "" or local, else a configured host (mode 1) or one of the viewer's
// own machines the server knows (mode 2), whose server forwards the handoff methods.
func handoffPeer(ctx context.Context, name string) (remote.Peer, error) {
	return ownPeer(ctx, name, "cli.handoff.not_mine", "cli.handoff.server_old")
}

// ownPeer is handoffPeer for any work between the viewer's own machines; notMine and serverOld are the keys of what
// it says when the machine is shared with the viewer or the server is too old to forward that work.
func ownPeer(ctx context.Context, name, notMine, serverOld string) (remote.Peer, error) {
	if name == "" || name == tend.HostLocal {
		return herePeer(), nil
	}
	h, far := remoteHosts(), farHosts()
	if hostName(h, name) == "" {
		far.reach()
	}
	n := hostName(h, name)
	if n == "" {
		return remote.Peer{}, i18n.E("cli.host.unknown", name)
	}
	if far.Server != nil {
		if err := far.reach(); err != nil {
			return remote.Peer{}, i18n.E("remote.put_server_down", remote.Reason(err))
		}
		if !far.mine(n) {
			return remote.Peer{}, i18n.E(notMine, n)
		}
		if !slices.Contains(far.features, remote.FeatureMigrate) {
			return remote.Peer{}, errors.New(i18n.T(serverOld))
		}
	}
	p, err := h.Peer(ctx, n)
	if err != nil {
		return remote.Peer{}, errors.New(i18n.F("remote.unreachable", n, remote.Reason(err)))
	}
	return p, nil
}

// herePeer is this machine in this process: the session methods as `tend node` answers them past its share gate, and
// node.repos.
func herePeer() remote.Peer {
	n := node.New(tend.Home())
	n.Limits = loadConfig().Node
	hello := remote.LocalHello(version)
	hello.Methods = append(slices.Clone(hello.Methods), remote.MRepos)
	return remote.PeerOf("", hello, remote.InProcess(hereHandler{n, remote.NewLocal(version)}))
}

type hereHandler struct {
	n        *node.Node
	sessions remote.Handler
}

func (h hereHandler) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method != remote.MRepos {
		return h.sessions.Handle(ctx, method, params)
	}
	var p remote.ReposParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: err.Error()}
	}
	return h.n.Repos(ctx, p)
}

// handoffRefused says why p, called host, did not answer method: its tend is too old, or its node shares only what
// its runs left (a handoff reads and writes beyond those).
func handoffRefused(p remote.Peer, host, method string, err error) error {
	switch wire.Code(err) {
	case wire.CodeUnknownMethod:
		return errors.New(remote.TooOld(host, method))
	case wire.CodeUnauthorized:
		if share := p.Hello.Share; share != "" && share != node.ShareAll {
			return i18n.E("cli.handoff.share", host, share)
		}
	}
	return i18n.E("cli.handoff.refused", host, remote.Reason(err))
}

// dirTexts are what handoffDir says: nothing found, the one taken, several to choose from.
type dirTexts struct{ none, one, several string }

var handoffDirTexts = dirTexts{"cli.handoff.no_dir", "cli.handoff.dir", "cli.handoff.dirs"}

// handoffDir is where the new session starts on host (3.1): the session's own directory on its own machine, the
// project's directory there, else the one checkout host finds of the session's remote (git's origin where it ran, else
// the one its record or Codex recorded).
func handoffDir(ctx context.Context, x *remote.Handover, r *tend.Rec, host string, say dirTexts) (string, error) {
	if x.From.Same(x.To) {
		return x.Facts.Cwd, nil
	}
	snap := sessionProjects()
	there := host
	if x.To.Name == "" {
		there = snap.Here
	}
	var pairs []remote.DirPair
	if p := snap.Of(r); p != nil {
		for _, rp := range p.Repos {
			if a, b := rp.Dirs[snap.Machine(r)], rp.Dirs[there]; a != "" && b != "" {
				pairs = append(pairs, remote.DirPair{From: a, To: b})
			}
		}
	}
	dirs, err := x.Dirs(ctx, pairs, r.GitRemote, indexedRemote(r))
	if err != nil {
		fmt.Fprintln(os.Stderr, handoffRefused(x.To, host, remote.MRepos, err))
	}
	switch len(dirs) {
	case 0:
		return "", i18n.E(say.none, host)
	case 1:
		fmt.Fprint(os.Stderr, i18n.F(say.one, host, dirs[0].Path))
		return dirs[0].Path, nil
	}
	var b strings.Builder
	for _, d := range dirs {
		b.WriteString("\n  " + strings.TrimSpace(d.Path+"  "+d.Branch))
	}
	return "", i18n.E(say.several, len(dirs), host, b.String())
}

// handoffEnv compares the session's environment with dir's on host for the pack, and says on stderr how they differ
// and what blocks; a block stops nothing, the new session reads it in the pack.
func handoffEnv(ctx context.Context, x *remote.Handover, dir, host string) {
	rep, err := x.Diagnose(ctx, dir)
	if err != nil {
		who := host
		if !x.From.Has(remote.MEnv) {
			who = x.FromName()
		}
		fmt.Fprint(os.Stderr, i18n.F("cli.handoff.env_unread", envErr(who, err)))
		return
	}
	fmt.Fprint(os.Stderr, i18n.F("cli.handoff.env", host, rep.Summary()))
	for _, it := range rep.Items {
		if it.Level == envcheck.LevelBlock {
			fmt.Fprintln(os.Stderr, "  "+envcheck.LevelText(it.Level)+": "+it.What)
		}
	}
}

// indexedRemote is the origin Codex recorded for r, a session on this machine, as the index has it.
func indexedRemote(r *tend.Rec) string {
	if r.Host != "" {
		return ""
	}
	idx, err := index.Open()
	if err != nil {
		return ""
	}
	for _, s := range idx.Sessions() {
		if s.Provider == r.Provider && s.SessionID == r.SessionID {
			return s.GitRemote()
		}
	}
	return ""
}

// handoffTask makes the pack a task on host and dispatches it: the coordinator's rules for the machine, the agent and
// the project apply.
func handoffTask(r *tend.Rec, x *remote.Handover, text, dir string, o handoffTo) error {
	machine := o.host
	if x.To.Name == "" {
		machine = cmp.Or(sessionProjects().Here, coord.Local)
	}
	project := o.project
	if p := sessionProjects().Of(r); project == "" && p != nil {
		project = p.ID
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		c := coord.TaskCreate{Title: i18n.F("handoff.title", r.Title), Brief: text + "\n" + i18n.T("handoff.task_ask") + "\n", Dir: dir,
			Machine: machine, Agent: cmp.Or(o.agent, r.Provider), Project: project}
		var t task.Task
		if err := write(cl, coord.MTaskCreate, c, &t); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.task.added", t.ID, t.Title))
		d := coord.Dispatch{Task: t.ID}
		if err := preview(cl, d, false); err != nil {
			return err
		}
		var run task.Run
		if err := write(cl, coord.MRunDispatch, d, &run); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.run.queued", run.ID, run.Machine, run.Agent))
		return nil
	})
}

// handoffThere runs `tend handoff --open <id>` on host over ssh: in a new tab of this Herdr workspace inside Herdr,
// else in this terminal. Mode 2 reaches over ssh only a host of the same name that is the server's machine.
func handoffThere(name, id, title string, o handoffTo) error {
	h, far := remoteHosts(), farHosts()
	if far.Server != nil {
		if !far.sshSame(name) {
			fmt.Println(handoffOpenLine(id))
			return i18n.E("cli.handoff.no_ssh", name)
		}
		h = far.SSH
	}
	host, ok := h.Host(name)
	if !ok {
		return i18n.E("cli.host.unknown", name)
	}
	cmd := remote.Command(host, true, "handoff", "--open", id, "--no-herdr")
	plan := capture.Plan{Spec: agent.CommandSpec{Exec: cmd.Args[0], Args: cmd.Args[1:]}}
	if !o.noHerdr && herdr.Active() {
		plan.Ws = herdrWorkspace(o.workspace)
	}
	r := &tend.Rec{Title: i18n.F("handoff.title", title), Host: name}
	if o.dryRun {
		printPlan(r, plan, false)
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
	return resumeHere(plan.Spec)
}

// handoffOpen starts the new session a pack another machine wrote here asks for, in its directory with its CLI.
func handoffOpen(s *tend.Store, id string, dryRun, noHerdr bool, workspace string) error {
	m, path, err := capture.OpenHandoff(id)
	if err != nil {
		return err
	}
	if !paths.IsDir(m.Dir) {
		return i18n.E("cli.handoff.dir_missing", m.Dir)
	}
	r := &tend.Rec{Provider: m.Provider, Cwd: m.Dir, Title: cmp.Or(m.Title, id)}
	plan, err := capture.PlanStart(r, m.Provider, capture.HandoffPrompt(path), noHerdr)
	if err != nil {
		return err
	}
	return runPlan(s, r, plan, dryRun, workspace, false)
}
