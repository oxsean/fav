package fav

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"sort"
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
	return slices.Contains(Statuses, s)
}

const (
	ProviderClaude = "claude"
	ProviderCodex  = "codex"
)

func ProviderName(p string) string {
	switch p {
	case ProviderClaude:
		return "Claude"
	case ProviderCodex:
		return "Codex"
	}
	return p
}

func ProviderLabel(p string) string {
	switch p {
	case ProviderClaude:
		return "Claude Code"
	case ProviderCodex:
		return "Codex CLI"
	}
	return p
}

func SessionKey(provider, sid string) string { return provider + ":" + sid }

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
	// Recap: Summary is the agent's own recap (unfavorited sessions), not a /fav summary or the first prompt.
	Recap bool `json:"-"`
	// Repo: the main checkout when the session ran in a linked git worktree.
	Repo string `json:"-"`
	// Files: absolute path → times the AI wrote it in this session.
	Files map[string]int `json:"-"`
	extra string

	hay string
}

func (r *Rec) Attach(turns, msgs int, lastAt time.Time, prompts string) {
	r.Turns, r.Msgs, r.LastAt, r.extra = turns, msgs, lastAt, prompts
	r.buildHay()
}

func (r *Rec) Archived() bool { return r.ArchivedAt != nil }
func (r *Rec) Done() bool     { return r.Status == StatusDone }
func (r *Rec) Favorite() bool { return r.ID != "" && r.FavoritedAt != nil }

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

func (r *Rec) Key() string { return SessionKey(r.Provider, r.SessionID) }

func (r *Rec) Transcripts() []string {
	out := make([]string, 0, 2)
	for _, p := range []string{r.PinnedPath, r.TranscriptPath} {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Broken: the cwd is gone, or every recorded transcript is (none recorded: not broken); callers skip running sessions.
func (r *Rec) Broken(exists func(string) bool) (dirGone, transcriptGone bool) {
	dirGone = r.Cwd != "" && !exists(r.Cwd)
	ts := r.Transcripts()
	return dirGone, len(ts) > 0 && !slices.ContainsFunc(ts, exists)
}

// ⚠️ Relink hard-links src, never copies, or a pin doubles a transcript's disk use; on failure r is left unpinned.
func (r *Rec) Relink(src string) error {
	if err := os.MkdirAll(filepath.Dir(r.PinnedPath), 0o700); err != nil {
		r.PinnedPath = ""
		return err
	}
	os.Remove(r.PinnedPath)
	if err := os.Link(src, r.PinnedPath); err != nil {
		r.PinnedPath = ""
		return err
	}
	return nil
}

func (r *Rec) ToggleFavorite(now time.Time) {
	if r.Favorite() {
		r.FavoritedAt = nil
		return
	}
	r.FavoritedAt = &now
}

func (r *Rec) ToggleArchived(now time.Time) {
	if r.Archived() {
		r.ArchivedAt = nil
		return
	}
	r.ArchivedAt = &now
}

func (r *Rec) ToggleStatus(target string) {
	if r.Status == target {
		r.Status = StatusDoing
		return
	}
	r.Status = target
}

// ActiveAt: the last activity the index saw, else When.
func (r *Rec) ActiveAt() time.Time {
	if r.LastAt.After(r.When()) {
		return r.LastAt
	}
	return r.When()
}

// SortByStart: newest start first; a session with no time yet (not indexed) counts as newest.
func SortByStart(recs []*Rec) {
	sort.SliceStable(recs, func(i, j int) bool {
		a, b := recs[i].When(), recs[j].When()
		if a.IsZero() != b.IsZero() {
			return a.IsZero()
		}
		return a.After(b)
	})
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

func (r *Rec) wrote(sub string) bool {
	for p := range r.Files {
		if strings.Contains(strings.ToLower(p), sub) {
			return true
		}
	}
	return false
}

type FileCount struct {
	Path string
	N    int
}

// TopFiles: the n most written files, most first, then by path.
func TopFiles(files map[string]int, n int) []FileCount {
	out := make([]FileCount, 0, len(files))
	for p, c := range files {
		out = append(out, FileCount{p, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Path < out[j].Path
	})
	return out[:min(n, len(out))]
}
