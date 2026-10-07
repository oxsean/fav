package remote

import (
	"cmp"
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"github.com/oxsean/fav/internal/capture"
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
	From   string `json:"from"` // where it was found: claude (~/.claude.json) | index | scan
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
	return capture.HandoffTarget{Source: x.FromName(), SourceHome: x.From.Hello.Home, Dir: dir, Home: x.To.Hello.Home}
}

// FromName is the session's machine as the caller names it.
func (x *Handover) FromName() string {
	return cmp.Or(x.From.Name, x.From.Hello.Hostname, x.From.Hello.Endpoint)
}

// Text is the pack for a new session in dir on To.
func (x *Handover) Text(dir string) string { return capture.RenderHandoff(x.Facts, x.Target(dir)) }

// Dirs are where the session can go on To, in order: the session's directory moved into each pair's (a project's
// directory on both machines), else the checkouts To finds of the session's remote (the facts' origin, then
// remotes). Nothing found is no error: the caller asks for a directory.
func (x *Handover) Dirs(ctx context.Context, pairs []DirPair, remotes ...string) ([]RepoDir, error) {
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
