// Package index scans Claude ~/.claude/projects/*/*.jsonl and Codex sessions/YYYY/MM/DD/rollout-*.jsonl incrementally
// by recorded offset; cached in ~/.agent/fav/sessions.jsonl, one line per file, last line wins.
package index

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/paths"
)

// File is one transcript as scanned so far (a Codex session may span several rollouts, merged in Sessions); counters are cumulative.
type File struct {
	Path      string    `json:"path"`
	Provider  string    `json:"provider"`
	SessionID string    `json:"session_id"`
	Size      int64     `json:"size"` // offset after the last complete line
	ModTime   time.Time `json:"mtime"`
	Skip      bool      `json:"skip,omitempty"` // one-shot -p / SDK / exec session, not resumable

	Cwd       string    `json:"cwd,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	Title     string    `json:"title,omitempty"`  // Claude: /rename custom-title beats ai-title; Codex: thread_name from session_index
	Custom    bool      `json:"custom,omitempty"` // Title came from /rename; later ai-titles do not overwrite it
	StartedAt time.Time `json:"started_at"`
	Turns     int       `json:"turns"`
	Replies   int       `json:"replies,omitempty"` // AI replies, counted by line
	Ver       int       `json:"ver,omitempty"`     // scanner logic version, distinct from scanVer forced rescans
	First     string    `json:"first,omitempty"`   // first decent prompt, the title when there is none
	Prompts   string    `json:"prompts,omitempty"` // concatenated prompts capped at promptsCap, search only
	// Claude moved the conversation to this session id when the context ran out; the chain is one session (Sessions)
	ContinuedIn string `json:"continued_in,omitempty"`
	App         bool   `json:"app,omitempty"` // Codex: started in the desktop app
	// Recap: Claude's latest away_summary (goal · done · next), Codex's latest task_complete reply, first paragraph; capped
	Recap string `json:"recap,omitempty"`
	// WtRepo: Claude entered a git worktree (worktree-state) and this is the checkout it came from; cleared when it left
	WtRepo string `json:"wt_repo,omitempty"`
	Remote string `json:"remote,omitempty"` // Codex: session_meta git.repository_url
	// Files: absolute path → times the AI wrote it (Claude Edit / Write / MultiEdit / NotebookEdit, Codex apply_patch); filesCap paths
	Files map[string]int `json:"files,omitempty"`

	line int // bytes of its line in the cache
}

type Session struct {
	Provider  string
	SessionID string   // Claude continuation chains: the newest id, the only one that resumes
	Aliases   []string // older ids of the chain; store records may still carry one
	Path      string   // the earliest file: resume, pin and doctor use it (a chain: the newest file)
	Cwd       string
	Branch    string
	Title     string
	First     string
	StartedAt time.Time
	LastAt    time.Time
	Turns     int
	Replies   int
	Prompts   string
	App       bool   // started in a desktop app (Codex: from the file; Claude: set by Attach)
	Recap     string // the newest file's Recap
	// Repo: the main checkout when the session ran in a linked git worktree (worktrees.go)
	Repo   string
	Files  map[string]int
	Size   int64 // bytes of all its transcripts
	wtRepo string
	remote string
}

// CodexArchived: the rollout sits in ~/.codex/archived_sessions (archived in Codex or the desktop app).
func (s *Session) CodexArchived() bool {
	return s.Provider == fav.ProviderCodex && paths.Under(s.Path, capture.CodexArchivedDir())
}

func (s *Session) Key() string { return fav.SessionKey(s.Provider, s.SessionID) }

func (s *Session) DisplayTitle() string {
	if s.Title != "" {
		return s.Title
	}
	first := truncate(strings.Join(strings.Fields(s.First), " "), 60)
	if s.Cwd != "" {
		return filepath.Base(s.Cwd) + ": " + first
	}
	return first
}

func (s *Session) Rec() *fav.Rec {
	started := s.StartedAt
	r := &fav.Rec{
		Provider: s.Provider, SessionID: s.SessionID, Cwd: s.Cwd, GitBranch: s.Branch,
		Title: s.DisplayTitle(), Summary: strings.Join(strings.Fields(s.First), " "), Project: projectOf(s.Cwd),
		Recap: s.Recap != "", Repo: s.Repo, Files: s.Files,
		TranscriptPath: s.Path, SessionStartedAt: &started, UpdatedAt: s.LastAt, // Status "": not marked until the user does
	}
	if s.Repo != "" {
		r.Project = filepath.Base(s.Repo)
	}
	if s.Recap != "" {
		r.Summary = s.Recap
	}
	r.Attach(s.Turns, s.Turns+s.Replies, s.LastAt, s.Prompts)
	r.App, r.CodexArchived = s.App, s.CodexArchived()
	return r
}

const (
	scanVer    = 8
	promptsCap = 8 * 1024 // max prompt bytes kept per file
	promptCap  = 300      // max chars stored per prompt
	titleMin   = 12       // prompts shorter than this are not titles
	scanBuf    = 256 * 1024
	filesCap   = 200 // paths kept per file
)

type line struct {
	Type        string    `json:"type"`
	Timestamp   time.Time `json:"timestamp"`
	Cwd         string    `json:"cwd"`
	GitBranch   string    `json:"gitBranch"`
	Entrypoint  string    `json:"entrypoint"`
	IsMeta      bool      `json:"isMeta"` // injected by Claude (skill expansion, caveats)
	CustomTitle string    `json:"customTitle"`
	AITitle     string    `json:"aiTitle"`
	ContinuedIn string    `json:"continuedInSessionId"`
	Worktree    *struct {
		OriginalCwd string `json:"originalCwd"`
	} `json:"worktreeSession"`
	Subtype string          `json:"subtype"`
	Content json.RawMessage `json:"content"` // Claude system lines (away_summary): a string
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Payload struct {
		Type           string  `json:"type"`
		Role           string  `json:"role"`
		SessionID      string  `json:"session_id"`
		Cwd            string  `json:"cwd"`
		Originator     string  `json:"originator"`
		ParentThreadID *string `json:"parent_thread_id"`
		LastAgentMsg   string  `json:"last_agent_message"` // Codex task_complete
		Git            struct {
			RepositoryURL string `json:"repository_url"`
		} `json:"git"`
		Content json.RawMessage `json:"content"`
	} `json:"payload"`
}

func (l *line) userText() string {
	var text string
	switch {
	case l.Type == "user" && !l.IsMeta:
		text = capture.ContentText(l.Message.Content)
	case l.Type == "response_item" && l.Payload.Type == "message" && l.Payload.Role == "user":
		text = capture.ContentText(l.Payload.Content)
	}
	if text = capture.Unwrap(text); capture.Injected(text) {
		return ""
	}
	return text
}

// interesting is a cheap pre-filter before JSON parsing.
var wanted = [][]byte{[]byte(`"type":"user"`), []byte(`"role":"user"`), []byte(`-title"`), []byte(`"session_meta"`), []byte(`"continued-in"`),
	[]byte(`"away_summary"`), []byte(`"task_complete"`), []byte(`"worktree-state"`)}

var replyClaude, replyText, replyCodex = []byte(`"type":"assistant"`), []byte(`"type":"text"`), []byte(`"type":"output_text"`)

func isReply(b []byte) bool {
	return (bytes.Contains(b, replyClaude) && bytes.Contains(b, replyText)) || bytes.Contains(b, replyCodex)
}

func interesting(b []byte) bool {
	for _, w := range wanted {
		if bytes.Contains(b, w) {
			return true
		}
	}
	return false
}

// scan reads from f.Size to EOF; a trailing partial line is not counted, lines longer than scanBuf are skipped.
func (f *File) scan() {
	f.Size, _ = fileio.Lines(context.Background(), f.Path, f.Size, scanBuf, func(_ int64, b []byte) bool {
		if isEdit(b) {
			f.takeEdits(b)
		}
		if isReply(b) {
			f.Replies++
			return true
		}
		if !interesting(b) {
			return true
		}
		var l line
		if json.Unmarshal(b, &l) == nil {
			f.take(&l)
		}
		return true
	})
}

func (f *File) take(l *line) {
	if f.StartedAt.IsZero() && !l.Timestamp.IsZero() {
		f.StartedAt = l.Timestamp.Local()
	}
	switch l.Type {
	case "session_meta":
		f.SessionID, f.Cwd = l.Payload.SessionID, l.Payload.Cwd
		if capture.CodexOneOff(l.Payload.Originator, l.Payload.ParentThreadID) {
			f.Skip = true
		}
		f.App = capture.CodexFromApp(l.Payload.Originator)
		f.Remote = l.Payload.Git.RepositoryURL
		return
	case "worktree-state":
		f.WtRepo = ""
		if l.Worktree != nil {
			f.WtRepo = l.Worktree.OriginalCwd
		}
		return
	case "custom-title":
		f.Title, f.Custom = l.CustomTitle, true
		return
	case "ai-title":
		if !f.Custom {
			f.Title = l.AITitle
		}
		return
	case "continued-in":
		f.ContinuedIn = l.ContinuedIn
		return
	case "system":
		if l.Subtype == "away_summary" {
			var text string
			if json.Unmarshal(l.Content, &text) == nil {
				f.Recap = recap(text)
			}
		}
		return
	case "event_msg":
		if l.Payload.Type == "task_complete" && l.Payload.LastAgentMsg != "" {
			f.Recap = recap(l.Payload.LastAgentMsg)
		}
		return
	}
	if l.Entrypoint != "" && l.Entrypoint != "cli" {
		f.Skip = true
	}
	if f.Cwd == "" {
		f.Cwd = l.Cwd
	}
	if l.GitBranch != "" && l.GitBranch != "HEAD" {
		f.Branch = l.GitBranch
	}
	text := l.userText()
	if text == "" {
		return
	}
	f.Turns++
	if f.First == "" || (f.Turns <= 3 && utf8.RuneCountInString(f.First) < titleMin && utf8.RuneCountInString(text) >= titleMin) {
		f.First = truncate(text, promptCap)
	}
	if len(f.Prompts) < promptsCap {
		f.Prompts += truncate(text, promptCap) + "\n"
	}
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Index is an immutable snapshot; Refresh returns a new one.
type Index struct {
	path  string
	files map[string]*File
	dirty []*File // changed by this Refresh; Save writes only these
	size  int     // bytes of the cache file, stale lines included
	torn  bool    // the cache has a line it could not read: the next Save rewrites it
	wt    worktrees
}

func Path() string { return filepath.Join(fav.Home(), "sessions.jsonl") }

func Open() (*Index, error) { return OpenAt(Path()) }

func OpenAt(path string) (*Index, error) {
	idx := &Index{path: path, files: map[string]*File{}, wt: loadWorktrees(worktreesPath(path))}
	fh, err := os.Open(path)
	if os.IsNotExist(err) {
		return idx, nil
	}
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		n := len(sc.Bytes()) + 1
		idx.size += n
		var f File
		if json.Unmarshal(sc.Bytes(), &f) == nil && f.Path != "" {
			f.line = n
			idx.files[f.Path] = &f
		}
	}
	idx.torn = sc.Err() != nil // ⚠️ a cache: an unreadable tail is rescanned, never fatal
	return idx, nil
}

func (idx *Index) Len() int { return len(idx.files) }

// Transcript is the newest file of a session, Skip files included (an SDK-launched agent still has a readable transcript).
func (idx *Index) Transcript(sessionID string) string {
	if f, _ := idx.FileByPrefix(sessionID); f != nil {
		return f.Path
	}
	return ""
}

// FileByPrefix: the newest file whose session id starts with ref, Skip and silent files included, and how many
// sessions match.
func (idx *Index) FileByPrefix(ref string) (*File, int) {
	var hit *File
	ids := map[string]bool{}
	for _, f := range idx.files {
		if strings.HasPrefix(f.SessionID, ref) {
			ids[fav.SessionKey(f.Provider, f.SessionID)] = true
			if hit == nil || f.ModTime.After(hit.ModTime) {
				hit = f
			}
		}
	}
	return hit, len(ids)
}

// Paths is every transcript the index knows, one-shot runs included: the full-text store follows this list.
func (idx *Index) Paths() []string {
	out := make([]string, 0, len(idx.files))
	for p := range idx.files {
		out = append(out, p)
	}
	return out
}

// PathsBySession maps provider:session id to its transcripts; a Claude continuation chain sits under its newest id,
// the id its record carries after Attach.
func (idx *Index) PathsBySession() map[string][]string {
	canon := map[string]string{}
	for _, s := range idx.Sessions() {
		for _, a := range s.Aliases {
			canon[fav.SessionKey(s.Provider, a)] = s.Key()
		}
	}
	out := map[string][]string{}
	for _, f := range idx.files {
		if f.SessionID == "" {
			continue
		}
		k := fav.SessionKey(f.Provider, f.SessionID)
		if c, ok := canon[k]; ok {
			k = c
		}
		out[k] = append(out[k], f.Path)
	}
	return out
}

// AgentSessions: the one-shot sessions Sessions() drops — SDK / exec / sub-agent runs — and the sessions in agent tools' scratch
// directories (AgentScratch; Attach leaves them out unless favorited); newest activity first.
// They carry no title of their own more often than not, so DisplayTitle falls back to the first prompt.
func (idx *Index) AgentSessions() []*Session {
	byKey := map[string]*Session{}
	for _, f := range idx.files {
		if !f.Skip && !AgentScratch(f.Cwd) || f.SessionID == "" {
			continue
		}
		s := byKey[fav.SessionKey(f.Provider, f.SessionID)]
		if s == nil {
			s = &Session{Provider: f.Provider, SessionID: f.SessionID, Path: f.Path, Cwd: f.Cwd, Branch: f.Branch,
				StartedAt: f.StartedAt, First: f.First}
			byKey[s.Key()] = s
		}
		if f.Title != "" && (s.Title == "" || !f.ModTime.Before(s.LastAt)) {
			s.Title = f.Title
		}
		if f.ModTime.After(s.LastAt) {
			s.LastAt, s.Path = f.ModTime, f.Path
		}
		s.Turns += f.Turns
		s.Replies += f.Replies
	}
	out := make([]*Session, 0, len(byKey))
	for _, s := range byKey {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out
}

// Rec is a bare record for a file outside Sessions() (Skip): enough to preview and resume it.
func (f *File) Rec() *fav.Rec {
	r := &fav.Rec{Provider: f.Provider, SessionID: f.SessionID, Cwd: f.Cwd, GitBranch: f.Branch, Title: f.Title,
		Summary: strings.Join(strings.Fields(f.First), " "), Project: projectOf(f.Cwd), TranscriptPath: f.Path,
		SessionStartedAt: &f.StartedAt, UpdatedAt: f.ModTime}
	if r.Title == "" {
		r.Title = r.Summary
	}
	return r
}

func (idx *Index) Refresh() (*Index, bool) { return idx.Rescan(nil) }

// Rescan is Refresh, but force files skip the incremental path and start over: they were rewritten in place (cwd changed by a move), size and mtime lie.
func (idx *Index) Rescan(force map[string]bool) (*Index, bool) {
	next := &Index{path: idx.path, files: make(map[string]*File, len(idx.files)), size: idx.size, torn: idx.torn, wt: idx.wt}
	seen := 0
	threads := codexThreadNames()
	for _, c := range candidates() {
		st, err := os.Stat(c.path)
		if err != nil {
			continue
		}
		seen++
		old := idx.files[c.path]
		if force[c.path] {
			old = nil
		}
		if old != nil && old.Ver == scanVer && old.Size == st.Size() && old.ModTime.Equal(st.ModTime()) {
			next.files[c.path] = old
			continue
		}
		f := &File{Path: c.path, Provider: c.provider, SessionID: c.sessionID, Ver: scanVer}
		if old != nil && old.Ver == scanVer && st.Size() >= old.Size {
			cp := *old
			f = &cp
		}
		f.ModTime = st.ModTime()
		f.scan()
		if f.Provider == fav.ProviderCodex {
			if t := threads[f.SessionID]; t != "" {
				f.Title = t
			}
		}
		next.files[c.path] = f
		next.dirty = append(next.dirty, f)
	}
	next.wt = idx.wt.learn(next.files, worktreesPath(idx.path))
	return next, len(next.dirty) > 0 || seen != len(idx.files)
}

// Save appends this Refresh's changes; rewrites the whole cache once stale lines outweigh the current ones.
func (idx *Index) Save() error {
	if len(idx.dirty) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(idx.path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, f := range idx.dirty {
		n := buf.Len()
		if err := json.NewEncoder(&buf).Encode(f); err != nil {
			return err
		}
		f.line = buf.Len() - n
	}
	live := 0
	for _, f := range idx.files {
		live += f.line
	}
	if idx.torn || idx.size+buf.Len() > 2*live+64<<10 {
		return idx.rewrite()
	}
	fh, err := os.OpenFile(idx.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer fh.Close()
	if _, err := fh.Write(buf.Bytes()); err != nil {
		return err
	}
	idx.size += buf.Len()
	idx.dirty = nil
	return nil
}

func (idx *Index) rewrite() error {
	size := 0
	err := fileio.WriteAtomic(idx.path, 0o644, func(w io.Writer) error {
		var line bytes.Buffer
		for _, f := range idx.files {
			line.Reset()
			if err := json.NewEncoder(&line).Encode(f); err != nil {
				return err
			}
			if _, err := w.Write(line.Bytes()); err != nil {
				return err
			}
			f.line = line.Len()
			size += line.Len()
		}
		return nil
	})
	if err != nil {
		return err
	}
	idx.size, idx.dirty, idx.torn = size, nil, false
	return nil
}

// Sessions merges files into sessions (same id: turns added, newest title, earliest file is canonical), newest activity first; silent and Skip files are dropped.
func (idx *Index) Sessions() []*Session {
	byKey := map[string]*Session{}
	var order []*File
	for _, f := range idx.files {
		if f.Skip || f.Turns == 0 || f.SessionID == "" {
			continue
		}
		order = append(order, f)
	}
	sort.Slice(order, func(i, j int) bool {
		if !order[i].StartedAt.Equal(order[j].StartedAt) {
			return order[i].StartedAt.Before(order[j].StartedAt)
		}
		return order[i].Path < order[j].Path
	})
	// a Claude continuation chain folds into its newest id: turns add up, the newest file is the transcript
	next := map[string]string{}
	for _, f := range idx.files {
		if f.ContinuedIn != "" && f.Provider == fav.ProviderClaude {
			next[f.SessionID] = f.ContinuedIn
		}
	}
	newest := func(id string) string {
		for range 16 { // ponytail: 16 hops is far more than any real chain
			n, ok := next[id]
			if !ok || n == id {
				break
			}
			id = n
		}
		return id
	}
	for _, f := range order {
		id := f.SessionID
		if f.Provider == fav.ProviderClaude {
			id = newest(id)
		}
		k := fav.SessionKey(f.Provider, id)
		s := byKey[k]
		if s == nil {
			s = &Session{Provider: f.Provider, SessionID: id, Path: f.Path, Cwd: f.Cwd,
				StartedAt: f.StartedAt, First: f.First}
			byKey[k] = s
		}
		if f.SessionID != id {
			s.Aliases = append(s.Aliases, f.SessionID)
		} else if f.Provider == fav.ProviderClaude {
			s.Path = f.Path
		}
		s.App = s.App || f.App
		s.Turns += f.Turns
		s.Replies += f.Replies
		s.Size += f.Size
		if f.Branch != "" {
			s.Branch = f.Branch
		}
		if f.Title != "" && (s.Title == "" || !f.ModTime.Before(s.LastAt)) {
			s.Title = f.Title
		}
		if f.Recap != "" && (s.Recap == "" || !f.ModTime.Before(s.LastAt)) {
			s.Recap = f.Recap
		}
		if !f.ModTime.Before(s.LastAt) {
			s.wtRepo = f.WtRepo
		}
		if f.Remote != "" {
			s.remote = f.Remote
		}
		for p, n := range f.Files {
			if s.Files == nil {
				s.Files = map[string]int{}
			}
			s.Files[p] += n
		}
		if f.ModTime.After(s.LastAt) {
			s.LastAt = f.ModTime
		}
		if len(s.Prompts) < promptsCap {
			s.Prompts += f.Prompts
		}
	}
	out := make([]*Session, 0, len(byKey))
	for _, s := range byKey {
		s.Repo = idx.wt.repoOf(s.Cwd, s.wtRepo, s.remote)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out
}

type candidate struct{ path, provider, sessionID string }

func candidates() []candidate {
	var out []candidate
	for _, p := range capture.ClaudeTranscripts("") {
		out = append(out, candidate{p, fav.ProviderClaude, strings.TrimSuffix(filepath.Base(p), ".jsonl")})
	}
	for _, p := range capture.CodexRollouts("") {
		out = append(out, candidate{p, fav.ProviderCodex, ""})
	}
	return out
}

// session_index.jsonl: {"id":…,"thread_name":…}
func codexThreadNames() map[string]string {
	out := map[string]string{}
	fh, err := os.Open(filepath.Join(capture.CodexHome(), "session_index.jsonl"))
	if err != nil {
		return out
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		var e struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.ID != "" && e.Name != "" {
			out[e.ID] = e.Name
		}
	}
	return out
}

// Attach: store records get index data attached, unknown sessions become records with an empty ID; the same object is reused from prev.
func (idx *Index) Attach(store *fav.Store, prev []*fav.Rec) []*fav.Rec {
	keep := make(map[string]*fav.Rec, len(prev))
	for _, r := range prev {
		keep[r.Key()] = r
	}
	var out []*fav.Rec
	desktop := capture.ClaudeDesktopIDs()
	for _, s := range idx.Sessions() {
		if s.Provider == fav.ProviderClaude && !s.App {
			s.App = desktop[s.SessionID]
			for _, a := range s.Aliases {
				s.App = s.App || desktop[a]
			}
		}
		r := store.BySession(s.Provider, s.SessionID)
		for _, a := range s.Aliases {
			if r != nil {
				break
			}
			if r = store.BySession(s.Provider, a); r != nil { // the record follows the chain: only the newest id resumes
				r.SessionID, r.TranscriptPath = s.SessionID, s.Path
			}
		}
		if r == nil && AgentScratch(s.Cwd) {
			continue // a scratch run, not a project: status:agent lists it
		}
		if r != nil {
			if r.TranscriptPath == "" { // favorited from the Agents page before the index saw it: fill in the file location
				r.TranscriptPath, r.SessionStartedAt = s.Path, &s.StartedAt
				if r.Cwd == "" {
					r.Cwd = s.Cwd
				}
			}
			if r.TranscriptPath != s.Path && !paths.Exists(r.TranscriptPath) { // moved: Codex archive / unarchive
				r.TranscriptPath = s.Path
			}
			r.Attach(s.Turns, s.Turns+s.Replies, s.LastAt, s.Prompts)
			r.App, r.CodexArchived, r.Repo, r.Files = s.App, s.CodexArchived(), s.Repo, s.Files
			continue
		}
		r = s.Rec()
		if p := keep[s.Key()]; p != nil {
			*p = *r
			r = p
		}
		out = append(out, r)
	}
	return out
}

// SessionFiles: Claude's transcript, sub-agent dir, todo and file-history; Codex's every rollout.
func SessionFiles(provider, sessionID string) []string {
	if sessionID == "" {
		return nil
	}
	switch provider {
	case fav.ProviderClaude:
		out, h := capture.ClaudeTranscripts(sessionID), capture.ClaudeHome()
		for _, pattern := range []string{filepath.Join(h, "projects", "*", sessionID), filepath.Join(h, "todos", sessionID+"*"),
			filepath.Join(h, "file-history", sessionID)} {
			hits, _ := filepath.Glob(pattern)
			out = append(out, hits...)
		}
		return out
	case fav.ProviderCodex:
		return capture.CodexRollouts(sessionID)
	}
	return nil
}

// AgentScratch: cwd is a tool's scratch directory — inside a temp directory (paths.InTemp) under a path element named
// claude-… (Claude Code's scratchpad claude-<uid>/…, review runs claude-review-…). A session started in /tmp itself is not one.
func AgentScratch(cwd string) bool {
	rel, ok := paths.InTemp(cwd)
	if !ok {
		return false
	}
	for el := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if strings.HasPrefix(el, "claude-") {
			return true
		}
	}
	return false
}

const recapCap = 300 // chars kept of a recap

// recap: the first paragraph, without Claude's "(disable recaps in /config)" tail, capped.
func recap(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.Index(text, "\n\n"); i > 0 {
		text = text[:i]
	}
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "(disable recaps in /config)"))
	return truncate(strings.Join(strings.Fields(text), " "), recapCap)
}
