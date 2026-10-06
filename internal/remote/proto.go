// Package remote lets tend on one machine read the sessions of another: `tend rpc` answers line-delimited JSON on
// stdin/stdout, a Client reaches it over ssh (or as a local process), Hosts keeps the configured machines' lists and
// Source reads one record's transcript wherever it lives.
package remote

import (
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/index"
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
	MQuery    = "query"
	MPut      = "put"
	MGrep     = "grep"
	MHits     = "hits"
	MTrash    = "trash"
	MRestore  = "restore"
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
	Find   string `json:"find,omitempty"` // message-search keywords: each message carries the spans they match
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

// ProjectDirs is a project the viewer sees and its directories on the machine that answers.
type ProjectDirs struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Dirs []string `json:"dirs"`
}

// Cursor is where a row falls in an order (tend.Cursor): a page resumes after one.
type Cursor = tend.Cursor

// QueryParams is a query of this machine's sessions, read with tend.Parse as this machine's own (host: is the
// caller's to apply; this machine ignores it).
type QueryParams struct {
	Q        string            `json:"q"`
	All      bool              `json:"all,omitempty"`  // not only favorites
	Sort     string            `json:"sort,omitempty"` // active (default) | started | favorited | turns
	Limit    int               `json:"limit"`          // 1..500
	After    *Cursor           `json:"after,omitempty"`
	Projects []ProjectDirs     `json:"projects,omitempty"`
	Also     map[string]string `json:"also,omitempty"`  // session id → its task's text, which keywords match too
	Fresh    bool              `json:"fresh,omitempty"` // refresh the index first
}

// Row is a listed session.
type Row struct {
	Session
	Project   string        `json:"project_id,omitempty"` // the project it belongs to, of those given
	Live      *capture.Live `json:"live,omitempty"`
	DeletedAt *time.Time    `json:"deleted_at,omitzero"` // status:trash: when it was moved into the trash
}

// Facets count the rows in the query's scope by what each picker offers (index.Facets).
type Facets = index.Facets

type QueryResult struct {
	Rows    []Row        `json:"rows"`
	Next    *Cursor      `json:"next,omitempty"`
	Total   int          `json:"total"`   // rows in scope
	Matched int          `json:"matched"` // rows q picks
	Running int          `json:"running"` // of those, running now
	Facets  Facets       `json:"facets"`
	Tokens  []tend.Token `json:"tokens"`
	Status  string       `json:"status,omitempty"` // agent: rows not listed here (the TUI's only)
	// TrashDays: this machine's trash_days, after which a trashed session is purged (0: never).
	TrashDays int `json:"trash_days,omitempty"`
}

// PutParams writes a patch to a session's record; a session without one gets one (not a favorite).
type PutParams struct {
	Ref
	Patch  tend.Patch `json:"patch"`
	Expect *time.Time `json:"expect,omitempty"` // the record's updated_at the editor saw; another answers wire.CodeStale
}

// TrashParams moves a session's files into this machine's trash, as the TUI's delete does; a running session answers
// wire.CodeBusy.
type TrashParams struct{ Ref }

type TrashResult struct {
	Title string `json:"title"`
	Files int    `json:"files"` // moved into the trash
}

// RestoreParams puts a session in this machine's trash back.
type RestoreParams struct{ Ref }

type RestoreResult struct {
	Title string `json:"title"`
	Files int    `json:"files"` // moved back
}

type GrepParams struct {
	Q        string            `json:"q"` // the text after >
	All      bool              `json:"all,omitempty"`
	Limit    int               `json:"limit"`
	BudgetMS int               `json:"budget_ms"`
	Projects []ProjectDirs     `json:"projects,omitempty"`
	Also     map[string]string `json:"also,omitempty"`
}

type GrepHit struct {
	Row      Row       `json:"row"`
	Hits     int       `json:"hits"`
	AllInOne bool      `json:"all_in_one"`
	Snippet  string    `json:"snippet"`
	Spans    [][2]int  `json:"spans,omitempty"` // byte ranges of the snippet to highlight
	Off      int64     `json:"off"`
	File     string    `json:"file"` // fileio.ID of the transcript the hit is in
	At       time.Time `json:"at"`
	Latest   time.Time `json:"latest"`
}

// Progress is how far the message-search store is built: Done of Total transcripts read.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

type GrepResult struct {
	Hits     []GrepHit `json:"hits"` // ranked on this machine
	Building *Progress `json:"building,omitempty"`
	Busy     bool      `json:"busy,omitempty"`
	TooLong  bool      `json:"too_long,omitempty"`
	Fixes    []string  `json:"fixes,omitempty"` // the spellings searched besides the keywords
}

type HitsParams struct {
	Ref
	Q     string `json:"q"`
	Limit int    `json:"limit"`
}

// Hit is a message of one session matching the keywords.
type Hit struct {
	Off   int64     `json:"off"`
	Role  string    `json:"role"` // user | assistant | tool
	At    time.Time `json:"at"`
	Text  string    `json:"text"`
	Spans [][2]int  `json:"spans,omitempty"`
	File  string    `json:"file"`
}

type HitsResult struct {
	Hits  []Hit `json:"hits"`
	Total int   `json:"total"`
}
