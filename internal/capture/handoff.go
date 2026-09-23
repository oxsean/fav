package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
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

// BuildFork: a new session that starts with r's history (Claude --fork-session, codex fork); r itself is left as it was.
func BuildFork(r *fav.Rec) (CommandSpec, error) {
	if r.SessionID == "" {
		return CommandSpec{}, i18n.E("resume.check.no_session")
	}
	switch r.Provider {
	case fav.ProviderClaude:
		return CommandSpec{Exec: "claude", Args: []string{"--resume", r.SessionID, "--fork-session"}, Cwd: r.Cwd}, nil
	case fav.ProviderCodex:
		return CommandSpec{Exec: "codex", Args: []string{"fork", r.SessionID}, Cwd: r.Cwd}, nil
	}
	return CommandSpec{}, i18n.E("resume.unknown_provider", r.Provider)
}

// BuildStart: a new session of provider in cwd whose first message is prompt ("" = none).
func BuildStart(provider, cwd, prompt string) (CommandSpec, error) {
	var args []string
	if prompt != "" {
		args = []string{prompt}
	}
	switch provider {
	case fav.ProviderClaude:
		return CommandSpec{Exec: "claude", Args: args, Cwd: cwd}, nil
	case fav.ProviderCodex:
		return CommandSpec{Exec: "codex", Args: args, Cwd: cwd}, nil
	}
	return CommandSpec{}, i18n.E("resume.unknown_provider", provider)
}

func Installed(provider string) bool {
	_, err := lookPath(provider)
	return err == nil
}

// HandoffPrompt is the new session's first message; the pack itself stays in the file so it never reaches argv or ps.
func HandoffPrompt(path string) string { return i18n.F("handoff.prompt", path) }

func handoffDir() string { return filepath.Join(fav.Home(), "handoff") }

// WriteHandoff writes r's handoff pack under the fav home and returns its path; packs older than handoffKeep are removed.
func WriteHandoff(r *fav.Rec) (string, error) {
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
	return path, os.WriteFile(path, []byte(Handoff(r)), 0o600)
}

func pruneHandoffs(dir string, now time.Time) {
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if info, err := e.Info(); err == nil && strings.HasSuffix(e.Name(), ".md") && now.Sub(info.ModTime()) > handoffKeep {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// Handoff is r's handoff pack in Markdown: where it ran, its summary, the latest requests, the last reply, the files it
// changed and what git has uncommitted. Tool output is left out.
func Handoff(r *fav.Rec) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }
	section := func(key string) { line(""); line("## " + i18n.T(key)); line("") }

	line("# " + i18n.F("handoff.title", r.Title))
	line("")
	src := i18n.F("handoff.source", providerLabel(r.Provider), r.SessionID, orDash(r.Cwd))
	if r.GitBranch != "" {
		src += i18n.F("handoff.branch", r.GitBranch)
	}
	var msgs []Message // newest first
	if r.TranscriptPath != "" {
		msgs = RecentMessages(r.TranscriptPath, handoffScan)
	}
	at := r.ActiveAt()
	if len(msgs) > 0 && msgs[0].At.After(at) {
		at = msgs[0].At
	}
	if !at.IsZero() {
		src += i18n.F("handoff.last_active", at.Local().Format("2006-01-02 15:04"))
	}
	line(src)
	if r.TranscriptPath != "" {
		line(i18n.F("handoff.transcript", r.TranscriptPath))
	}
	line(i18n.T("handoff.caveat"))

	if s := strings.TrimSpace(r.Summary); s != "" {
		section("handoff.summary")
		line(s)
	}
	if reqs := lastRequests(r.TranscriptPath, msgs, handoffRequests); len(reqs) > 0 {
		section("handoff.requests")
		for i, q := range reqs {
			line(fmt.Sprintf("%d. %s", i+1, indentRest(clip(q, handoffReqCap))))
		}
	}
	if reply := lastReply(r.TranscriptPath, msgs); reply != "" {
		section("handoff.stopped")
		line(quote(clip(reply, handoffReplyCap)))
	}
	if files := changedFiles(msgs, r.Cwd, handoffFilesCap); len(files) > 0 {
		section("handoff.files")
		for _, f := range files {
			line("- " + f)
		}
	}
	if r.Cwd != "" && dirExists(r.Cwd) {
		if st := GitOut(r.Cwd, "status", "--short", "--branch"); strings.Contains(st, "\n") { // --branch leads with a "## branch" line, so the trim keeps the status columns of the rest
			section("handoff.uncommitted")
			line("```")
			line(head(st, handoffGitCap))
			line("```")
		}
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
			out = append([]string{RawText(path, m.Off, m.Text)}, out...)
		}
	}
	return out
}

func lastReply(path string, msgs []Message) string {
	for _, m := range msgs {
		if m.Role == "assistant" {
			return RawText(path, m.Off, m.Text)
		}
	}
	return ""
}

var editTools = map[string]bool{"Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true}

// changedFiles: files the AI wrote or patched in msgs (newest first), most recent first, relative to cwd when under it.
func changedFiles(msgs []Message, cwd string, n int) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		if cwd != "" {
			if rel, err := filepath.Rel(cwd, p); err == nil && filepath.IsAbs(p) && !strings.HasPrefix(rel, "..") {
				p = rel
			}
		}
		out = append(out, p)
	}
	for _, m := range msgs {
		steps := m.Steps
		for j := len(steps) - 1; j >= 0 && len(out) < n; j-- {
			s := steps[j]
			switch {
			case s.Result:
			case editTools[s.Tool]:
				add(strings.TrimSpace(firstLine(s.Text)))
			case s.Tool == "apply_patch":
				for _, f := range strings.Fields(firstLine(s.Text)) {
					add(f)
				}
			}
		}
	}
	return out
}
