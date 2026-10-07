package capture

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/tend"
)

const (
	handoffScan     = 80 // recent messages read for requests and changed files
	handoffRequests = 5
	handoffReqCap   = 600 // runes per request
	handoffReplyCap = 2000
	handoffFilesCap = 30
	handoffGitCap   = 30 // git status lines
	handoffKeep     = 30 * 24 * time.Hour
)

// HandoffPrompt is the new session's first message; the pack itself stays in the file so it never reaches argv or ps.
func HandoffPrompt(path string) string { return i18n.F("handoff.prompt", path) }

func handoffDir() string { return filepath.Join(tend.Home(), "handoff") }

// WriteHandoff writes r's handoff pack under the tend home and returns its path; packs older than handoffKeep are removed.
func WriteHandoff(r *tend.Rec) (string, error) {
	dir := handoffDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	pruneHandoffs(dir, time.Now())
	sid := r.SessionID
	if len(sid) > 8 {
		sid = sid[:8]
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.md", sid, time.Now().Format("20060102-150405")))
	return path, os.WriteFile(path, []byte(RenderHandoff(HandoffFactsOf(r), HandoffTarget{})), 0o600)
}

func pruneHandoffs(dir string, now time.Time) {
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		ext := filepath.Ext(e.Name())
		if info, err := e.Info(); err == nil && (ext == ".md" || ext == ".json") && now.Sub(info.ModTime()) > handoffKeep {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// HandoffMeta is what `tend handoff --open` needs besides the pack, kept beside it as <id>.json.
type HandoffMeta struct {
	Dir      string    `json:"dir"`
	Provider string    `json:"provider"` // the CLI the new session starts with
	Title    string    `json:"title,omitempty"`
	From     string    `json:"from,omitempty"`     // the session's machine, as the sender named it
	Endpoint string    `json:"endpoint,omitempty"` // its hello.endpoint
	Session  string    `json:"session,omitempty"`  // provider:session id there
	At       time.Time `json:"at"`
}

var handoffID = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`)

// PutHandoff keeps a pack another machine wrote for a new session here; the id is all `tend handoff --open` takes.
func PutHandoff(text string, m HandoffMeta, sessionID string) (id, path string, err error) {
	dir := handoffDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	pruneHandoffs(dir, time.Now())
	var rnd [3]byte
	rand.Read(rnd[:])
	sid := unsafeID.ReplaceAllString(sessionID, "_")
	if len(sid) > 8 {
		sid = sid[:8]
	}
	id = fmt.Sprintf("%s-%s-%s", cmp.Or(sid, "s"), time.Now().Format("20060102-150405"), hex.EncodeToString(rnd[:]))
	m.At = time.Now()
	b, err := json.Marshal(m)
	if err != nil {
		return "", "", err
	}
	path = filepath.Join(dir, id+".md")
	if err := fileio.WriteFile(path, []byte(text), 0o600); err != nil {
		return "", "", err
	}
	return id, path, fileio.WriteFile(filepath.Join(dir, id+".json"), b, 0o600)
}

var unsafeID = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// OpenHandoff is the pack PutHandoff kept as id: its meta and the path of its text.
func OpenHandoff(id string) (HandoffMeta, string, error) {
	var m HandoffMeta
	if !handoffID.MatchString(id) {
		return m, "", i18n.E("handoff.bad_id", id)
	}
	path := filepath.Join(handoffDir(), id+".md")
	b, err := os.ReadFile(filepath.Join(handoffDir(), id+".json"))
	if err == nil {
		err = json.Unmarshal(b, &m)
	}
	if err == nil {
		_, err = os.Stat(path)
	}
	if err != nil {
		return m, "", i18n.E("handoff.no_pack", id)
	}
	return m, path, nil
}

// HandoffFacts are what a handoff pack says of a session, read on the machine it is on: the reader's machine writes
// the pack from them in its own language. Each part is capped as the pack shows it.
type HandoffFacts struct {
	Provider   string     `json:"provider"`
	SessionID  string     `json:"session_id"`
	Title      string     `json:"title"`
	Cwd        string     `json:"cwd,omitempty"`
	Branch     string     `json:"branch,omitempty"`
	LastAt     time.Time  `json:"last_at,omitzero"`
	Transcript string     `json:"transcript,omitempty"` // its path there
	Summary    string     `json:"summary,omitempty"`
	Requests   []string   `json:"requests,omitempty"` // the latest, oldest first
	Reply      string     `json:"reply,omitempty"`    // the last one
	Files      []string   `json:"files,omitempty"`    // the files it changed, newest first, relative under Cwd
	Git        HandoffGit `json:"git"`
}

// HandoffGit is the checkout Cwd is in, as git says now.
type HandoffGit struct {
	Status string `json:"status,omitempty"` // git status --short --branch, when anything is uncommitted
	Remote string `json:"remote,omitempty"` // origin's URL
}

// HandoffFactsOf reads r's facts on this machine: its record, the tail of its transcript and git in its directory.
// Tool output is left out.
func HandoffFactsOf(r *tend.Rec) HandoffFacts {
	f := HandoffFacts{Provider: r.Provider, SessionID: r.SessionID, Title: r.Title, Cwd: r.Cwd, Branch: r.GitBranch,
		Transcript: r.TranscriptPath, Summary: strings.TrimSpace(r.Summary)}
	var msgs []Message // newest first
	if r.TranscriptPath != "" {
		msgs = recentMessages(r.TranscriptPath, handoffScan)
	}
	f.LastAt = r.ActiveAt()
	if len(msgs) > 0 && msgs[0].At.After(f.LastAt) {
		f.LastAt = msgs[0].At
	}
	for _, q := range lastRequests(r.TranscriptPath, msgs, handoffRequests) {
		f.Requests = append(f.Requests, clip(q, handoffReqCap))
	}
	f.Reply = clip(lastReply(r.TranscriptPath, msgs), handoffReplyCap)
	f.Files = changedFiles(msgs, r.Cwd, handoffFilesCap)
	if r.Cwd != "" && paths.IsDir(r.Cwd) {
		if st := GitOut(r.Cwd, "status", "--short", "--branch"); strings.Contains(st, "\n") { // --branch leads with a "## branch" line, so the trim keeps the status columns of the rest
			f.Git.Status = head(st, handoffGitCap)
		}
		f.Git.Remote = GitOut(r.Cwd, "remote", "get-url", "origin")
	}
	return f
}

// HandoffTarget is where a pack is read; the zero value is the session's own machine.
type HandoffTarget struct {
	Source     string // the session's machine as the reader names it; set when the pack is read on another one
	SourceHome string
	Dir        string // where the new session starts
	Home       string
}

// RenderHandoff is the handoff pack of f in Markdown: where it ran, its summary, the latest requests, the last reply,
// the files it changed and what git has uncommitted. Read on another machine, it names the session's machine instead
// of its transcript, and how the directories correspond.
func RenderHandoff(f HandoffFacts, to HandoffTarget) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }
	section := func(key string) { line(""); line("## " + i18n.T(key)); line("") }

	line("# " + i18n.F("handoff.title", f.Title))
	line("")
	src := i18n.F("handoff.source", tend.ProviderLabel(f.Provider), f.SessionID, orDash(f.Cwd))
	if to.Source != "" {
		src = i18n.F("handoff.source_on", tend.ProviderLabel(f.Provider), f.SessionID, to.Source, orDash(f.Cwd))
	}
	if f.Branch != "" {
		src += i18n.F("handoff.branch", f.Branch)
	}
	if !f.LastAt.IsZero() {
		src += i18n.F("handoff.last_active", f.LastAt.Local().Format("2006-01-02 15:04"))
	}
	line(src)
	switch {
	case to.Source != "":
		line(i18n.F("handoff.elsewhere", to.Source))
	case f.Transcript != "":
		line(i18n.F("handoff.transcript", f.Transcript))
	}
	line(i18n.T("handoff.caveat"))

	if to.Source != "" {
		section("handoff.dirs")
		line(i18n.F("handoff.dirs.dir", orDash(f.Cwd), orDash(to.Dir)))
		if to.SourceHome != "" && to.Home != "" {
			line(i18n.F("handoff.dirs.home", to.SourceHome, to.Home))
		}
	}
	if f.Summary != "" {
		section("handoff.summary")
		line(f.Summary)
	}
	if len(f.Requests) > 0 {
		section("handoff.requests")
		for i, q := range f.Requests {
			line(fmt.Sprintf("%d. %s", i+1, indentRest(q)))
		}
	}
	if f.Reply != "" {
		section("handoff.stopped")
		line(quote(f.Reply))
	}
	if len(f.Files) > 0 {
		section("handoff.files")
		for _, x := range f.Files {
			line("- " + x)
		}
	}
	if f.Git.Status != "" {
		section("handoff.uncommitted")
		line("```")
		line(f.Git.Status)
		line("```")
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// quote keeps the reply's own headings from reading as the pack's sections.
func quote(s string) string { return "> " + strings.ReplaceAll(s, "\n", "\n> ") }

// indentRest keeps a multi-line request inside its list item.
func indentRest(s string) string { return strings.ReplaceAll(s, "\n", "\n   ") }

// lastRequests: the newest n user messages of msgs (newest first), oldest first, with their line breaks.
func lastRequests(path string, msgs []Message, n int) []string {
	var out []string
	for _, m := range msgs {
		if len(out) == n {
			break
		}
		if m.Role == "user" {
			out = append([]string{rawText(path, m.Off, m.Text)}, out...)
		}
	}
	return out
}

func lastReply(path string, msgs []Message) string {
	for _, m := range msgs {
		if m.Role == "assistant" {
			return rawText(path, m.Off, m.Text)
		}
	}
	return ""
}

// changedFiles: files the AI wrote or patched in msgs (newest first), most recent first, relative to cwd when under it.
func changedFiles(msgs []Message, cwd string, n int) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		if rel, ok := paths.Inside(cwd, p); ok && filepath.IsAbs(p) {
			p = rel
		}
		out = append(out, p)
	}
	for _, m := range msgs {
		steps := m.Steps
		for j := len(steps) - 1; j >= 0 && len(out) < n; j-- {
			for _, f := range steps[j].Files {
				add(f)
			}
		}
	}
	return out
}
