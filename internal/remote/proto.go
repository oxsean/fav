// Package remote lets tend on one machine read the sessions of another: `tend rpc` answers line-delimited JSON on
// stdin/stdout, a Client reaches it over ssh (or as a local process), Hosts keeps the configured machines' lists and
// Source reads one record's transcript wherever it lives.
package remote

import (
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/envcheck"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/memory"
	"github.com/oxsean/fav/internal/migrate"
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

// What a handoff or a migration asks of the machines at its two ends, for each machine's owner alone (the coordinator's
// ownerReads and writeMethods).
const (
	MHandoffFacts  = "handoff.facts"
	MHandoffPut    = "handoff.put"
	MMemoryList    = "memory.ls"
	MMemoryRead    = "memory.read"
	MMemoryTrash   = "memory.trash"
	MMemoryRestore = "memory.restore"
	MMemoryPut     = "memory.put"
	MEnv           = "env"
	MEnvFile       = "env.file"
	MExportPlan    = "export.plan"
	MExportRead    = "export.read"
	MExportDone    = "export.done"
	MCopies        = "copies"
	MImportBegin   = "import.begin"
	MImportChunk   = "import.chunk"
	MImportCommit  = "import.commit"
	MImportAbort   = "import.abort"
)

// FeatureMigrate is in a coordinator's hello when its node.call forwards those methods.
const FeatureMigrate = "migrate"

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
	Copies        []Copy         `json:"copies,omitempty"` // its migrations to and from other machines, never Changed
}

func SessionOf(r *tend.Rec) Session {
	return Session{ID: r.ID, Provider: r.Provider, SessionID: r.SessionID, Title: r.Title, Label: r.Label,
		Summary: r.Summary, Project: r.Project, WorkType: r.WorkType, Tags: r.Tags, Status: r.Status, Cwd: r.Cwd,
		GitBranch: r.GitBranch, GitRemote: r.GitRemote, Repo: r.Repo, Transcript: r.TranscriptPath, Pinned: r.PinnedPath,
		StartedAt: r.SessionStartedAt, FavoritedAt: r.FavoritedAt, ArchivedAt: r.ArchivedAt, UpdatedAt: r.UpdatedAt,
		ResumedAt: r.LastResumedAt, Resumes: r.ResumeCount, LastAt: r.LastAt, Turns: r.Turns, Msgs: r.Msgs, App: r.App,
		CodexArchived: r.CodexArchived, Recap: r.Recap, Files: r.Files, Copies: copiesOf(r.Copies)}
}

func copiesOf(cs []tend.Copy) []Copy {
	if len(cs) == 0 {
		return nil
	}
	out := make([]Copy, len(cs))
	for i, c := range cs {
		out[i] = Copy{Migration: c.Migration, Role: c.Role, State: c.State, Peer: PeerRef{Name: c.Peer, Endpoint: c.Endpoint}, At: c.At}
	}
	return out
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
	for _, c := range s.Copies {
		r.Copies = append(r.Copies, tend.Copy{Migration: c.Migration, Role: c.Role, State: c.State, Peer: c.Peer.Name,
			Endpoint: c.Peer.Endpoint, At: c.At.Local()})
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

// End is one machine as a path mapping needs it, from its hello (pathmap.End on the wire).
type End = migrate.End

// PeerRef names the other machine of a handoff or migration as the initiator saw it; a migration record keeps it.
type PeerRef = migrate.Peer

// DirPair is a source directory and its counterpart on the target, given explicitly.
type DirPair = migrate.Pair

// HandoffPutParams writes a handoff pack on the target for `tend handoff --open`.
type HandoffPutParams struct {
	Ref      Ref     `json:"ref"` // the session handed off, on From
	Text     string  `json:"text"`
	Dir      string  `json:"dir"`
	Provider string  `json:"provider"` // the CLI the new session starts with
	From     PeerRef `json:"from"`
}

type HandoffPut struct {
	ID   string `json:"id"` // [A-Za-z0-9._-], the only value `tend handoff --open` takes
	Path string `json:"path"`
}

type MemoryListParams struct {
	Dirs   []string `json:"dirs,omitempty"`
	Global bool     `json:"global,omitempty"` // Codex's global memories too
}

type MemoryList struct {
	Sets []memory.Set `json:"sets"`
}

// MemoryFile names a file under a memory root: memory.read and memory.trash take it, memory.restore answers it.
type MemoryFile struct {
	File string `json:"file"`
}

type MemoryText struct {
	Text string    `json:"text"`
	At   time.Time `json:"at"`
	SHA  string    `json:"sha"`
}

// MemoryEntry is a memory's trash entry: memory.trash answers it, memory.restore takes it.
type MemoryEntry struct {
	Entry string `json:"entry"`
}

// MemoryPutParams writes one memory and its MEMORY.md line, never over another: Expect "" when the file should not exist.
type MemoryPutParams struct {
	Dir    string `json:"dir"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Text   string `json:"text"`
	Line   string `json:"line,omitempty"`
	Expect string `json:"expect,omitempty"`
}

type MemoryPut struct {
	File     string `json:"file"`
	Incoming bool   `json:"incoming,omitempty"` // a different one was there: written under .incoming/, not indexed
	Lines    int    `json:"lines,omitzero"`     // of MEMORY.md after
	Bytes    int64  `json:"bytes,omitzero"`
	Over     bool   `json:"over,omitempty"`
}

// EnvParams asks for envcheck.Print of dir; with Ref, on the source, the answer also has what the session saw.
type EnvParams struct {
	Dir string `json:"dir"`
	Ref *Ref   `json:"ref,omitempty"`
}

// EnvFileParams asks for one file of a Print, answered as Text.
type EnvFileParams struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

type ExportPlanParams struct {
	Ref
	Migration string   `json:"migration,omitempty"` // set: record the intent (pending) for To
	To        *PeerRef `json:"to,omitempty"`
}

type ExportPlan struct {
	Manifest migrate.Manifest `json:"manifest"`
	Live     string           `json:"live"`          // yes | no | unknown
	Why      string           `json:"why,omitempty"` // why unknown, a stable code
	Cwd      string           `json:"cwd"`
	Repo     string           `json:"repo,omitempty"`
	Git      envcheck.Git     `json:"git"`
}

type ExportReadParams struct {
	Ref
	Migration string `json:"migration"`
	File      string `json:"file"` // a manifest path
	Off       int64  `json:"off"`
	N         int    `json:"n"`  // ≤ 4 MiB
	ID        string `json:"id"` // fileio.ID of the file as planned: another one answers stale
}

type ExportChunk struct {
	Data []byte `json:"data"`
	EOF  bool   `json:"eof,omitempty"`
}

type ExportDoneParams struct {
	Ref
	Migration string `json:"migration"`
	State     string `json:"state"`          // done | aborted
	Move      bool   `json:"move,omitempty"` // done and move: the original goes to this machine's trash (index.TrashSession)
	// Committed is, with done, the main transcript as the target committed it: recorded instead of the last plan's.
	Committed *migrate.Sum `json:"committed,omitempty"`
}

type ExportDone struct {
	Trashed int `json:"trashed,omitzero"` // files moved into this machine's trash
}

type ImportBeginParams struct {
	Migration string  `json:"migration"`
	From      PeerRef `json:"from"`
	Ref
	Title    string           `json:"title,omitempty"` // the session's title on the source: the trash entry of a copy it replaces
	Cwd      string           `json:"cwd"`             // the session's cwd on the source
	Dir      string           `json:"dir"`             // the directory it gets here
	Pairs    []DirPair        `json:"pairs,omitempty"` // explicit mappings the rewrite may use, Cwd → Dir first
	Manifest migrate.Manifest `json:"manifest"`
}

type ImportBegin struct {
	Staged    map[string]int64 `json:"staged,omitempty"`    // file → bytes held, matching the manifest's sha so far
	Committed *Row             `json:"committed,omitempty"` // already committed: the row, nothing more to do
	Source    *migrate.Sum     `json:"source,omitempty"`    // committed: the source's main transcript it carried
	Clash     string           `json:"clash,omitempty"`     // "" | forward (an unchanged copy, replaced on commit) | diverged | exists
}

type ImportChunkParams struct {
	Migration string `json:"migration"`
	File      string `json:"file"`
	Off       int64  `json:"off"` // the bytes staged so far: another offset is refused
	Data      []byte `json:"data"`
}

type ImportChunk struct {
	Off int64 `json:"off"`
}

type ImportCommitParams struct {
	Migration string `json:"migration"`
	Note      string `json:"note,omitempty"` // the migration note the first resume here starts with
}

type ImportCommit struct {
	Row      Row          `json:"row"`
	Files    int          `json:"files"`
	Source   *migrate.Sum `json:"source,omitempty"`   // the source's main transcript it carried
	Unmapped []string     `json:"unmapped,omitempty"` // cwd values no pair or home mapped, left as they were
}

type ImportAbortParams struct {
	Migration string `json:"migration"`
}

// Copies are a session's migrations this machine recorded.
type Copies struct {
	Copies []Copy `json:"copies"`
}

type Copy struct {
	Migration string    `json:"migration"`
	Role      string    `json:"role"`  // to: this machine is the source; from: the target
	State     string    `json:"state"` // pending | done | aborted
	Peer      PeerRef   `json:"peer"`
	At        time.Time `json:"at"`
	Changed   *bool     `json:"changed,omitempty"` // copies only: this side's transcript moved on since; nil unknown
	Moved     bool      `json:"moved,omitempty"`   // copies only: the source's original went to its trash
}
