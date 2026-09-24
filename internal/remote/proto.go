// Package remote lets fav on one machine read the sessions of another: `fav rpc` answers line-delimited JSON on
// stdin/stdout, a Client reaches it over ssh (or as a local process), Hosts keeps the configured machines' lists and
// Source reads one record's transcript wherever it lives.
package remote

import (
	"encoding/json"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
)

// Proto changes whenever a request or result changes shape; both ends must agree.
const Proto = 1

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

// Request and Response are one line of JSON each.
type Request struct {
	Proto  int             `json:"proto"`
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	ID     int64           `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error carries a stable code, never localized text: the two ends may use different languages. Detail is for logs.
type Error struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

// Error codes. The first group comes from the remote fav, the second from reaching it.
const (
	CodeBadRequest    = "bad_request"
	CodeUnknownMethod = "unknown_method"
	CodeProto         = "proto" // the other end speaks another Proto: update fav there
	CodeNotFound      = "not_found"
	CodeStale         = "stale" // the transcript was rewritten since the offsets asked about were read
	CodeInternal      = "internal"

	CodeOffline = "offline" // ssh could not connect
	CodeAuth    = "auth"    // ssh refused the key
	CodeHostKey = "hostkey" // the host key changed or is unknown
	CodeTimeout = "timeout"
	CodeClosed  = "closed" // the remote process ended (crashed, killed)
	CodeNoFav   = "no_fav" // the remote shell could not find the fav command
)

type HelloParams struct {
	Lang string `json:"lang,omitempty"` // the caller's language: check texts come back in it
}

type Hello struct {
	Proto    int             `json:"proto"`
	Version  string          `json:"version"`
	OS       string          `json:"os"` // GOOS
	Arch     string          `json:"arch"`
	Endpoint string          `json:"endpoint"` // stable id of this machine + OS + WSL distro + config dirs
	Hostname string          `json:"hostname"`
	WSL      string          `json:"wsl,omitempty"` // WSL distro name
	Home     string          `json:"home"`
	Sep      string          `json:"sep"`
	Claude   string          `json:"claude_home"`
	Codex    string          `json:"codex_home"`
	CLIs     map[string]bool `json:"clis"` // claude / codex found on PATH
	Methods  []string        `json:"methods"`
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
	StartedAt     *time.Time     `json:"started_at,omitempty"`
	FavoritedAt   *time.Time     `json:"favorited_at,omitempty"`
	ArchivedAt    *time.Time     `json:"archived_at,omitempty"`
	UpdatedAt     time.Time      `json:"updated_at"`
	ResumedAt     *time.Time     `json:"resumed_at,omitempty"`
	Resumes       int            `json:"resumes,omitempty"`
	LastAt        time.Time      `json:"last_at"`
	Turns         int            `json:"turns"`
	Msgs          int            `json:"msgs"`
	App           bool           `json:"app,omitempty"`
	CodexArchived bool           `json:"codex_archived,omitempty"`
	Recap         bool           `json:"recap,omitempty"`
	Files         map[string]int `json:"files,omitempty"`
}

func SessionOf(r *fav.Rec) Session {
	return Session{ID: r.ID, Provider: r.Provider, SessionID: r.SessionID, Title: r.Title, Label: r.Label,
		Summary: r.Summary, Project: r.Project, WorkType: r.WorkType, Tags: r.Tags, Status: r.Status, Cwd: r.Cwd,
		GitBranch: r.GitBranch, GitRemote: r.GitRemote, Repo: r.Repo, Transcript: r.TranscriptPath, Pinned: r.PinnedPath,
		StartedAt: r.SessionStartedAt, FavoritedAt: r.FavoritedAt, ArchivedAt: r.ArchivedAt, UpdatedAt: r.UpdatedAt,
		ResumedAt: r.LastResumedAt, Resumes: r.ResumeCount, LastAt: r.LastAt, Turns: r.Turns, Msgs: r.Msgs, App: r.App,
		CodexArchived: r.CodexArchived, Recap: r.Recap, Files: r.Files}
}

// Rec is s as a record of host.
func (s Session) Rec(host string) *fav.Rec {
	r := &fav.Rec{ID: s.ID, Provider: s.Provider, SessionID: s.SessionID, Title: s.Title, Label: s.Label,
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

// File, in the params that carry offsets, is the Page.File they came from: another file answers CodeStale.
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
