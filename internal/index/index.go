// Package index scans Claude ~/.claude/projects/*/*.jsonl and Codex sessions/YYYY/MM/DD/rollout-*.jsonl incrementally
// by recorded offset; cached in ~/.agent/fav/sessions.jsonl, one line per file, last line wins.
package index

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
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
}

type Session struct {
	Provider  string
	SessionID string
	Path      string // the earliest file: resume, pin and doctor use it
	Cwd       string
	Branch    string
	Title     string
	First     string
	StartedAt time.Time
	LastAt    time.Time
	Turns     int
	Replies   int
	Prompts   string
}

func (s *Session) Key() string { return s.Provider + ":" + s.SessionID }

func (s *Session) DisplayTitle() string {
	if s.Title != "" {
		return s.Title
	}
	return truncate(strings.Join(strings.Fields(s.First), " "), 60)
}

func (s *Session) Rec() *fav.Rec {
	started := s.StartedAt
	r := &fav.Rec{
		Provider: s.Provider, SessionID: s.SessionID, Cwd: s.Cwd, GitBranch: s.Branch,
		Title: s.DisplayTitle(), Summary: strings.Join(strings.Fields(s.First), " "), Project: filepath.Base(s.Cwd),
		TranscriptPath: s.Path, SessionStartedAt: &started, UpdatedAt: s.LastAt,
		Status: fav.StatusDoing,
	}
	if s.Cwd == "" {
		r.Project = ""
	}
	r.Attach(s.Turns, s.Turns+s.Replies, s.LastAt, s.Prompts)
	return r
}

const (
	scanVer    = 2
	promptsCap = 8 * 1024 // max prompt bytes kept per file
	promptCap  = 300      // max chars stored per prompt
	titleMin   = 12       // prompts shorter than this are not titles
	scanBuf    = 256 * 1024
)

// noise is what Claude Code injects into user messages.
var noise = []string{"<", "This session is being continued", "[Image:", "Base directory for this skill",
	"Launching skill", "Stop hook feedback", "A session-scoped Stop hook", "[Request interrupted"}

func isNoise(s string) bool {
	for _, n := range noise {
		if strings.HasPrefix(s, n) {
			return true
		}
	}
	return false
}

type line struct {
	Type        string    `json:"type"`
	Timestamp   time.Time `json:"timestamp"`
	Cwd         string    `json:"cwd"`
	GitBranch   string    `json:"gitBranch"`
	Entrypoint  string    `json:"entrypoint"`
	IsMeta      bool      `json:"isMeta"` // injected by Claude (skill expansion, caveats)
	CustomTitle string    `json:"customTitle"`
	AITitle     string    `json:"aiTitle"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Payload struct {
		Type           string  `json:"type"`
		Role           string  `json:"role"`
		SessionID      string  `json:"session_id"`
		Cwd            string  `json:"cwd"`
		Originator     string  `json:"originator"`
		ParentThreadID *string `json:"parent_thread_id"`
		Content        []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"payload"`
}

func (l *line) userText() string {
	var text string
	switch {
	case l.Type == "user":
		if len(l.Message.Content) > 0 && l.Message.Content[0] == '"' {
			json.Unmarshal(l.Message.Content, &text)
		} else {
			var blocks []struct{ Type, Text string }
			json.Unmarshal(l.Message.Content, &blocks)
			var parts []string
			for _, b := range blocks {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					parts = append(parts, b.Text)
				}
			}
			text = strings.Join(parts, " ")
		}
	case l.Type == "response_item" && l.Payload.Type == "message" && l.Payload.Role == "user":
		for _, c := range l.Payload.Content {
			text += c.Text + " "
		}
	}
	text = capture.Unwrap(text)
	if l.IsMeta || isNoise(text) {
		return ""
	}
	return text
}

// interesting is a cheap pre-filter before JSON parsing.
var wanted = [][]byte{[]byte(`"type":"user"`), []byte(`"role":"user"`), []byte(`-title"`), []byte(`"session_meta"`)}

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

// scan reads from f.Size to EOF; a trailing partial line is not counted.
func (f *File) scan() {
	fh, err := os.Open(f.Path)
	if err != nil {
		return
	}
	defer fh.Close()
	if _, err := fh.Seek(f.Size, 0); err != nil {
		return
	}
	br := bufio.NewReaderSize(fh, scanBuf)
	off := f.Size
	for {
		b, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			// lines longer than the buffer are skipped
			n := len(b)
			for err == bufio.ErrBufferFull {
				b, err = br.ReadSlice('\n')
				n += len(b)
			}
			if err != nil {
				break
			}
			off += int64(n)
			continue
		}
		if err != nil {
			break
		}
		off += int64(len(b))
		if isReply(b) {
			f.Replies++
			continue
		}
		if !interesting(b) {
			continue
		}
		var l line
		if json.Unmarshal(b, &l) != nil {
			continue
		}
		f.take(&l)
	}
	f.Size = off
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
		return
	case "custom-title":
		f.Title, f.Custom = l.CustomTitle, true
		return
	case "ai-title":
		if !f.Custom {
			f.Title = l.AITitle
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
	raw   int
}

func Path() string { return filepath.Join(fav.Home(), "sessions.jsonl") }

func Open() (*Index, error) { return OpenAt(Path()) }

func OpenAt(path string) (*Index, error) {
	idx := &Index{path: path, files: map[string]*File{}}
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
		idx.raw++
		var f File
		if json.Unmarshal(sc.Bytes(), &f) == nil && f.Path != "" {
			idx.files[f.Path] = &f
		}
	}
	return idx, sc.Err()
}

func (idx *Index) Len() int { return len(idx.files) }

func (idx *Index) Refresh() (*Index, bool) { return idx.Rescan(nil) }

// Rescan is Refresh, but force files skip the incremental path and start over: they were rewritten in place (cwd changed by a move), size and mtime lie.
func (idx *Index) Rescan(force map[string]bool) (*Index, bool) {
	next := &Index{path: idx.path, files: make(map[string]*File, len(idx.files)), raw: idx.raw}
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
	return next, len(next.dirty) > 0 || seen != len(idx.files)
}

// Save appends this Refresh's changes; rewrites the whole cache once it exceeds twice the entry count.
func (idx *Index) Save() error {
	if len(idx.dirty) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(idx.path), 0o755); err != nil {
		return err
	}
	if idx.raw+len(idx.dirty) > 2*len(idx.files)+64 {
		return idx.rewrite()
	}
	fh, err := os.OpenFile(idx.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer fh.Close()
	w := bufio.NewWriter(fh)
	for _, f := range idx.dirty {
		if err := json.NewEncoder(w).Encode(f); err != nil {
			return err
		}
		idx.raw++
	}
	idx.dirty = nil
	return w.Flush()
}

func (idx *Index) rewrite() error {
	tmp := idx.path + ".tmp"
	fh, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(fh)
	for _, f := range idx.files {
		if err := json.NewEncoder(w).Encode(f); err != nil {
			fh.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		fh.Close()
		return err
	}
	if err := fh.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, idx.path); err != nil {
		return err
	}
	idx.raw, idx.dirty = len(idx.files), nil
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
	for _, f := range order {
		k := f.Provider + ":" + f.SessionID
		s := byKey[k]
		if s == nil {
			s = &Session{Provider: f.Provider, SessionID: f.SessionID, Path: f.Path, Cwd: f.Cwd,
				StartedAt: f.StartedAt, First: f.First}
			byKey[k] = s
		}
		s.Turns += f.Turns
		s.Replies += f.Replies
		if f.Branch != "" {
			s.Branch = f.Branch
		}
		if f.Title != "" && (s.Title == "" || !f.ModTime.Before(s.LastAt)) {
			s.Title = f.Title
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
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out
}

type candidate struct{ path, provider, sessionID string }

func candidates() []candidate {
	var out []candidate
	hits, _ := filepath.Glob(filepath.Join(claudeHome(), "projects", "*", "*.jsonl"))
	for _, p := range hits {
		out = append(out, candidate{p, fav.ProviderClaude, strings.TrimSuffix(filepath.Base(p), ".jsonl")})
	}
	// Codex only nests YYYY/MM/DD
	root := codexSessionsDir()
	years, _ := os.ReadDir(root)
	for _, y := range years {
		if !y.IsDir() || len(y.Name()) != 4 {
			continue
		}
		hits, _ := filepath.Glob(filepath.Join(root, y.Name(), "[0-9][0-9]", "[0-9][0-9]", "rollout-*.jsonl"))
		for _, p := range hits {
			out = append(out, candidate{p, fav.ProviderCodex, ""})
		}
	}
	return out
}

func claudeHome() string {
	if h := os.Getenv("CLAUDE_CONFIG_DIR"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func codexSessionsDir() string { return filepath.Join(codexHome(), "sessions") }

// session_index.jsonl: {"id":…,"thread_name":…}
func codexThreadNames() map[string]string {
	out := map[string]string{}
	fh, err := os.Open(filepath.Join(codexHome(), "session_index.jsonl"))
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
		keep[r.Provider+":"+r.SessionID] = r
	}
	var out []*fav.Rec
	for _, s := range idx.Sessions() {
		if r := store.BySession(s.Provider, s.SessionID); r != nil {
			if r.TranscriptPath == "" { // favorited from the Agents page before the index saw it: fill in the file location
				r.TranscriptPath, r.SessionStartedAt = s.Path, &s.StartedAt
				if r.Cwd == "" {
					r.Cwd = s.Cwd
				}
			}
			r.Attach(s.Turns, s.Turns+s.Replies, s.LastAt, s.Prompts)
			continue
		}
		r := s.Rec()
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
	var out []string
	add := func(pattern string) {
		hits, _ := filepath.Glob(pattern)
		out = append(out, hits...)
	}
	switch provider {
	case fav.ProviderClaude:
		h := claudeHome()
		add(filepath.Join(h, "projects", "*", sessionID+".jsonl"))
		add(filepath.Join(h, "projects", "*", sessionID))
		add(filepath.Join(h, "todos", sessionID+"*"))
		add(filepath.Join(h, "file-history", sessionID))
	case fav.ProviderCodex:
		add(filepath.Join(codexSessionsDir(), "*", "[0-9][0-9]", "[0-9][0-9]", "rollout-*-"+sessionID+".jsonl"))
	}
	return out
}
