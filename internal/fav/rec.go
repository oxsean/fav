package fav

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

const Schema = 1

// Three independent dimensions: favorite (FavoritedAt set), board status, archived (ArchivedAt set).
const (
	StatusTodo    = "todo"
	StatusDoing   = "doing"
	StatusDone    = "done"
	StatusDefault = StatusDone
)

var Statuses = []string{StatusTodo, StatusDoing, StatusDone}

func ValidStatus(s string) bool {
	for _, v := range Statuses {
		if v == s {
			return true
		}
	}
	return false
}

const (
	ProviderClaude = "claude"
	ProviderCodex  = "codex"
)

type Rec struct {
	ID        string   `json:"id"`
	Schema    int      `json:"schema_version"`
	Provider  string   `json:"provider"`
	SessionID string   `json:"session_id"`
	Title     string   `json:"title"`
	Label     string   `json:"label,omitempty"` // short title for the Herdr tab
	Summary   string   `json:"summary"`
	Project   string   `json:"project,omitempty"`
	WorkType  string   `json:"work_type,omitempty"`
	Tags      []string `json:"tags,omitempty"`

	Status     string     `json:"status"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`

	Cwd       string `json:"cwd,omitempty"`
	GitRoot   string `json:"git_root,omitempty"`
	GitRemote string `json:"git_remote,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	Hostname  string `json:"hostname,omitempty"`

	TranscriptPath string `json:"transcript_path,omitempty"`
	PinnedPath     string `json:"pinned_path,omitempty"`

	HerdrWorkspace string `json:"herdr_workspace,omitempty"`
	HerdrTab       string `json:"herdr_tab,omitempty"`

	SessionStartedAt *time.Time `json:"session_started_at,omitempty"` // first timestamp in the transcript; may be unreadable
	FavoritedAt      *time.Time `json:"favorited_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
	LastResumedAt    *time.Time `json:"last_resumed_at,omitempty"`
	ResumeCount      int        `json:"resume_count"`

	Supersedes string `json:"supersedes,omitempty"`
	Deleted    bool   `json:"deleted,omitempty"`

	// From the index, not persisted; re-attached on every refresh.
	Turns  int       `json:"-"`
	Msgs   int       `json:"-"`
	LastAt time.Time `json:"-"`
	App    bool      `json:"-"` // started in a desktop app (Claude or ChatGPT): opening it there is the default when set so
	// CodexArchived: the rollout is in ~/.codex/archived_sessions.
	CodexArchived bool `json:"-"`
	extra         string

	hay string
}

func (r *Rec) Attach(turns, msgs int, lastAt time.Time, prompts string) {
	r.Turns, r.Msgs, r.LastAt, r.extra = turns, msgs, lastAt, prompts
	r.buildHay()
}

func (r *Rec) Archived() bool { return r.ArchivedAt != nil }
func (r *Rec) Done() bool     { return r.Status == StatusDone }
func (r *Rec) Favorite() bool { return r.ID != "" && r.FavoritedAt != nil }

func (r *Rec) Favorited() time.Time {
	if r.FavoritedAt != nil {
		return *r.FavoritedAt
	}
	return time.Time{}
}

func (r *Rec) Visible() bool {
	return !r.Deleted && !r.Archived() && r.Favorite()
}

func (r *Rec) Prepare() { r.buildHay() }

func (r *Rec) buildHay() {
	var b strings.Builder
	for _, s := range []string{r.Title, r.Summary, r.Project, r.WorkType, r.GitBranch, r.GitRemote, r.Cwd} {
		b.WriteString(s)
		b.WriteByte('\n')
	}
	for _, t := range r.Tags {
		b.WriteString(t)
		b.WriteByte('\n')
	}
	b.WriteString(r.extra)
	r.hay = strings.ToLower(b.String())
}

func NewID() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func Normalize(tags []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(t, "#")))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// ActiveAt: the last activity the index saw, else When.
func (r *Rec) ActiveAt() time.Time {
	if r.LastAt.After(r.When()) {
		return r.LastAt
	}
	return r.When()
}

func (r *Rec) When() time.Time {
	if r.SessionStartedAt != nil {
		return *r.SessionStartedAt
	}
	if r.FavoritedAt != nil {
		return *r.FavoritedAt
	}
	return r.UpdatedAt
}
