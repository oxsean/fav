package remote

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// MRepos finds a repository's checkouts on a node (ReposParams), answered by internal/node.
const MRepos = "node.repos"

type ReposParams struct {
	Remote string `json:"remote"`
}

type Repos struct {
	Dirs []RepoDir `json:"dirs"`
}

// RepoDir is a checkout of the remote asked for.
type RepoDir struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	From   string `json:"from"` // where it was found: claude (~/.claude.json) | index | scan; Dirs adds same | project
}

// handoff answers handoff.facts and handoff.put.
func (h *localHandler) handoff(_ context.Context, method string, params json.RawMessage) (any, error) {
	if method == MHandoffFacts {
		var ref Ref
		if err := decode(params, &ref); err != nil {
			return nil, err
		}
		if ref.Provider == "" || ref.SessionID == "" {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "ref"}
		}
		h.mu.Lock()
		r, err := h.lookup(ref, 0)
		h.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return capture.HandoffFactsOf(r), nil
	}
	var p HandoffPutParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	switch {
	case strings.TrimSpace(p.Text) == "":
		return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "text"}
	case p.Provider != tend.ProviderClaude && p.Provider != tend.ProviderCodex:
		return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "provider"}
	case !filepath.IsAbs(p.Dir):
		return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "dir"}
	}
	m := capture.HandoffMeta{Dir: filepath.Clean(p.Dir), Provider: p.Provider, From: p.From.Name, Endpoint: p.From.Endpoint}
	if p.Ref.SessionID != "" {
		m.Session = p.Ref.Provider + ":" + p.Ref.SessionID
	}
	if i := strings.Index(p.Text, "\n"); strings.HasPrefix(p.Text, "# ") && i > 0 {
		m.Title = strings.TrimSpace(p.Text[2:i])
	}
	id, path, err := capture.PutHandoff(p.Text, m, p.Ref.SessionID)
	if err != nil {
		return nil, err
	}
	return HandoffPut{ID: id, Path: path}, nil
}

// Handover is a session on From handed to a new session on To: the facts read where it is, the pack written where
// the new one starts.
type Handover struct {
	From, To Peer
	Ref      Ref
	Facts    capture.HandoffFacts
	Env      *capture.HandoffEnv // how To's environment differs, once Diagnose compared them
}

// Handoff reads ref's facts on from for a new session on to.
func Handoff(ctx context.Context, from, to Peer, ref Ref) (*Handover, error) {
	x := &Handover{From: from, To: to, Ref: ref}
	if err := from.Call(ctx, MHandoffFacts, ref, &x.Facts); err != nil {
		return nil, err
	}
	return x, nil
}

// Target is where the pack is read: on To, the session's own machine reading it as its own.
func (x *Handover) Target(dir string) capture.HandoffTarget {
	if x.From.Same(x.To) {
		return capture.HandoffTarget{}
	}
	return capture.HandoffTarget{Source: x.FromName(), SourceHome: x.From.Hello.Home, Dir: dir, Home: x.To.Hello.Home, Env: x.Env}
}

// FromName is the session's machine as the caller names it.
func (x *Handover) FromName() string { return x.From.Label() }

// Text is the pack for a new session in dir on To.
func (x *Handover) Text(dir string) string { return capture.RenderHandoff(x.Facts, x.Target(dir)) }

// Dirs are where the session can go on To, in order: its own directory when To is its own machine, the session's
// directory moved into each pair's (a project's directory on both machines), else the checkouts To finds of the
// session's remote (the facts' origin, then remotes). Nothing found is no error: the caller asks for a directory.
func (x *Handover) Dirs(ctx context.Context, pairs []DirPair, remotes ...string) ([]RepoDir, error) {
	if x.From.Same(x.To) {
		return []RepoDir{{Path: x.Facts.Cwd, From: "same"}}, nil
	}
	for _, p := range pairs {
		if d, ok := pathmap.Rebase(x.Facts.Cwd, p.From, p.To, x.From.End(), x.To.End()); ok {
			return []RepoDir{{Path: d, From: "project"}}, nil
		}
	}
	remote := ""
	for _, r := range append([]string{x.Facts.Git.Remote}, remotes...) {
		if task.RemoteKey(r) != "" {
			remote = r
			break
		}
	}
	if remote == "" {
		return nil, nil
	}
	var res Repos
	if err := x.To.Call(ctx, MRepos, ReposParams{Remote: remote}, &res); err != nil {
		return nil, err
	}
	return slices.Clip(res.Dirs), nil
}

// Put writes text on To for a new session in dir with provider's CLI: `tend handoff --open <id>` there starts it.
func (x *Handover) Put(ctx context.Context, text, dir, provider string) (HandoffPut, error) {
	var res HandoffPut
	err := x.To.Call(ctx, MHandoffPut, HandoffPutParams{Ref: x.Ref, Text: text, Dir: dir, Provider: provider, From: x.From.Ref()}, &res)
	return res, err
}

// ShareAll is the node.share_sessions value under which a node answers every session (node.ShareAll).
const ShareAll = "all"

// HandoffRefusal says why p, called host, did not answer method: its tend is too old, or its node shares only what
// its runs left (a handoff reads and writes beyond those).
func HandoffRefusal(p Peer, host, method string, err error) string {
	if wire.Code(err) == wire.CodeUnknownMethod {
		return TooOld(host, method)
	}
	if share := ShareLimit(p, err); share != "" {
		return i18n.F("cli.handoff.share", host, share)
	}
	return i18n.F("cli.handoff.refused", host, Reason(err))
}

// ShareLimit is p's node.share_sessions when err is p refusing what that setting keeps from the caller, else "".
func ShareLimit(p Peer, err error) string {
	if share := p.Hello.Share; wire.Code(err) == wire.CodeUnauthorized && share != "" && share != ShareAll {
		return share
	}
	return ""
}

// ServerRefusal is why a server (mode 2) does not carry work between the viewer's own machines to machine name, ""
// when it does: down is why it cannot be reached (nil when it can), mine whether name is the viewer's, features what
// it understands; notMine and serverOld are the keys of what is said for another person's machine and for a server
// too old for the work.
func ServerRefusal(name string, down error, mine bool, features []string, notMine, serverOld string) string {
	switch {
	case down != nil:
		return i18n.F("remote.put_server_down", Reason(down))
	case !mine:
		return i18n.F(notMine, name)
	case !slices.Contains(features, FeatureMigrate):
		return i18n.T(serverOld)
	}
	return ""
}

func handoffOpenArgs(id string) []string { return []string{"handoff", "--open", id} }

// HandoffOpenLine is the command that opens the pack put as id, run on the machine it was put on.
func HandoffOpenLine(id string) string { return "tend " + strings.Join(handoffOpenArgs(id), " ") }

// HandoffThere opens the pack put as id on h over ssh, in this terminal.
func HandoffThere(h tend.Host, id string) agent.CommandSpec {
	cmd := Command(h, true, append(handoffOpenArgs(id), "--no-herdr")...)
	return agent.CommandSpec{Exec: cmd.Args[0], Args: cmd.Args[1:]}
}

// Reach is machine name as a Peer, its failure said for the UI.
func (h *Hosts) Reach(ctx context.Context, name string) (Peer, error) {
	p, err := h.Peer(ctx, name)
	if err != nil {
		return Peer{}, errors.New(i18n.F("remote.unreachable", name, Reason(err)))
	}
	return p, nil
}
