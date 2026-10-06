// Package remote lets tend on one machine read the sessions of another: `tend rpc` answers line-delimited JSON on
// stdin/stdout, a Client reaches it over ssh (or as a local process), Hosts keeps the configured machines' lists and
// Source reads one record's transcript wherever it lives.
package remote

import (
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/tend"
)

const (
	MHello    = "hello"
	MList     = "list"
	MMessages = "messages"
	MText     = "text"
	MSteps    = "steps"
	MPulse    = "pulse"
	MChecks   = "checks"
	MLive     = "live"
	MEcho     = "echo"
)

type HelloParams struct {
	Proto int    `json:"proto"`
	Role  string `json:"role,omitempty"` // client | coordinator | node
	Lang  string `json:"lang,omitempty"` // the caller's language: check texts come back in it
	// Features: what the caller understands beyond the methods it calls (events, fields), as the other end's Features.
	Features []string `json:"features,omitempty"`
}

type Hello struct {
	Proto    int             `json:"proto"`
	Version  string          `json:"version"`
	OS       string          `json:"os,omitempty"` // GOOS
	Arch     string          `json:"arch,omitempty"`
	Endpoint string          `json:"endpoint,omitempty"` // stable id of this machine + OS + WSL distro + config dirs
	Hostname string          `json:"hostname,omitempty"`
	WSL      string          `json:"wsl,omitempty"` // WSL distro name
	Home     string          `json:"home,omitempty"`
	Sep      string          `json:"sep,omitempty"`
	Claude   string          `json:"claude_home,omitempty"`
	Codex    string          `json:"codex_home,omitempty"`
	CLIs     map[string]bool `json:"clis,omitempty"` // claude / codex found on PATH
	Methods  []string        `json:"methods"`
	// Features: what this end understands beyond its method names, such as a field of run.start; an end that needs one
	// the other lacks refuses rather than lets it be ignored.
	Features []string `json:"features,omitempty"`
	NodeID   string   `json:"node_id,omitempty"` // a node's lasting identity (node.ID), which a server binds its token to
	// Share: which of its sessions a node answers (node.share_sessions as it applies them: all | runs | none).
	Share string `json:"share_sessions,omitempty"`
	Role  string `json:"role,omitempty"`
	// Build: tend-server's Web UI, a hash of its files; a page that loaded other files reloads.
	Build string `json:"build,omitempty"`
	// Caller: a coordinator tells a client who it answers as; a node leaves it out.
	Caller *Caller `json:"caller,omitempty"`
}

// Caller is who a coordinator answers a client as.
type Caller struct {
	User  string `json:"user"`
	Admin bool   `json:"admin,omitempty"`
}

// Ref names a session on the machine that answers.
type Ref struct {
	Provider  string `json:"provider"`
	SessionID string `json:"session_id"`
}

type List struct {
	Sessions []Session `json:"sessions"`
}

// Session is the list fields of a record: what a card, the filters and the detail header need, and all that the
// local cache keeps. No conversation text beyond the title, summary and recap.
type Session struct {
	ID            string         `json:"id,omitempty"`
	Provider      string         `json:"provider"`
	SessionID     string         `json:"session_id"`
	Title         string         `json:"title"`
	Label         string         `json:"label,omitempty"`
	Summary       string         `json:"summary,omitempty"`
	Project       string         `json:"project,omitempty"`
	WorkType      string         `json:"work_type,omitempty"`
	Tags          []string       `json:"tags,omitempty"`
	Status        string         `json:"status,omitempty"`
	Cwd           string         `json:"cwd,omitempty"`
	GitBranch     string         `json:"git_branch,omitempty"`
	GitRemote     string         `json:"git_remote,omitempty"`
	Repo          string         `json:"repo,omitempty"`
	Transcript    string         `json:"transcript,omitempty"`
	Pinned        string         `json:"pinned,omitempty"`
	StartedAt     *time.Time     `json:"started_at,omitzero"`
	FavoritedAt   *time.Time     `json:"favorited_at,omitzero"`
	ArchivedAt    *time.Time     `json:"archived_at,omitzero"`
	UpdatedAt     time.Time      `json:"updated_at"`
	ResumedAt     *time.Time     `json:"resumed_at,omitzero"`
	Resumes       int            `json:"resumes,omitempty"`
	LastAt        time.Time      `json:"last_at"`
	Turns         int            `json:"turns"`
	Msgs          int            `json:"msgs"`
	App           bool           `json:"app,omitempty"`
	CodexArchived bool           `json:"codex_archived,omitempty"`
	Recap         bool           `json:"recap,omitempty"`
	Files         map[string]int `json:"files,omitempty"`
}

func SessionOf(r *tend.Rec) Session {
	return Session{ID: r.ID, Provider: r.Provider, SessionID: r.SessionID, Title: r.Title, Label: r.Label,
		Summary: r.Summary, Project: r.Project, WorkType: r.WorkType, Tags: r.Tags, Status: r.Status, Cwd: r.Cwd,
		GitBranch: r.GitBranch, GitRemote: r.GitRemote, Repo: r.Repo, Transcript: r.TranscriptPath, Pinned: r.PinnedPath,
		StartedAt: r.SessionStartedAt, FavoritedAt: r.FavoritedAt, ArchivedAt: r.ArchivedAt, UpdatedAt: r.UpdatedAt,
		ResumedAt: r.LastResumedAt, Resumes: r.ResumeCount, LastAt: r.LastAt, Turns: r.Turns, Msgs: r.Msgs, App: r.App,
		CodexArchived: r.CodexArchived, Recap: r.Recap, Files: r.Files}
}

// Rec is s as a record of host.
func (s Session) Rec(host string) *tend.Rec {
	r := &tend.Rec{ID: s.ID, Provider: s.Provider, SessionID: s.SessionID, Title: s.Title, Label: s.Label,
		Summary: s.Summary, Project: s.Project, WorkType: s.WorkType, Tags: s.Tags, Status: s.Status, Cwd: s.Cwd,
		GitBranch: s.GitBranch, GitRemote: s.GitRemote, Repo: s.Repo, TranscriptPath: s.Transcript, PinnedPath: s.Pinned,
		SessionStartedAt: s.StartedAt, FavoritedAt: s.FavoritedAt, ArchivedAt: s.ArchivedAt, UpdatedAt: s.UpdatedAt,
		LastResumedAt: s.ResumedAt, ResumeCount: s.Resumes, LastAt: s.LastAt, Turns: s.Turns, Msgs: s.Msgs, App: s.App,
		CodexArchived: s.CodexArchived, Recap: s.Recap, Files: s.Files, Host: host}
	for _, t := range []*time.Time{&r.UpdatedAt, &r.LastAt, r.SessionStartedAt, r.FavoritedAt, r.ArchivedAt, r.LastResumedAt} {
		if t != nil {
			*t = t.Local()
		}
	}
	r.Prepare()
	return r
}

// File, in the params that carry offsets, is the Page.File they came from: another file answers wire.CodeStale.
type MessagesParams struct {
	Ref
	Before int64  `json:"before"` // < 0: from the end
	N      int    `json:"n"`
	File   string `json:"file,omitempty"`
}

type TextParams struct {
	Ref
	Off      int64  `json:"off"`
	Fallback string `json:"fallback,omitempty"`
	File     string `json:"file,omitempty"`
}

type Text struct {
	Text string `json:"text"`
}

type StepsParams struct {
	Ref
	Steps []capture.Step `json:"steps"`
	File  string         `json:"file,omitempty"`
}

type Steps struct {
	Texts []string `json:"texts"`
}

type PulseResult struct {
	Pulse capture.Pulse `json:"pulse"`
	OK    bool          `json:"ok"`
}

type Checks struct {
	Checks []capture.Check `json:"checks"`
}

type Live struct {
	Live map[string]capture.Live `json:"live"`
}
