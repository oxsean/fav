// Package capture identifies the current session and the environment it runs in.
package capture

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/herdr"
	"github.com/oxsean/fav/internal/i18n"
)

type Context struct {
	Provider       string
	SessionID      string
	Cwd            string
	GitRoot        string
	GitRemote      string
	GitBranch      string
	Hostname       string
	TranscriptPath string
	HerdrWorkspace string
	HerdrTab       string
}

// ErrAmbiguous: several live sessions share the cwd; never guess, ask for --session-id.
type ErrAmbiguous struct{ Candidates []Candidate }

func (e *ErrAmbiguous) Error() string {
	var b strings.Builder
	b.WriteString(i18n.T("capture.codex_ambiguous"))
	for _, c := range e.Candidates {
		b.WriteString(i18n.F("capture.candidate", c.SessionID, c.ModTime.Format("15:04:05")))
	}
	return b.String()
}

// Detect: herdr pane current → CLAUDE_CODE_SESSION_ID → Codex rollout lookup.
func Detect() (*Context, error) {
	c := &Context{}
	c.Cwd, _ = os.Getwd()
	c.Hostname, _ = os.Hostname()
	c.fillGit()
	c.fillHerdr()

	if c.Provider == "" {
		if id := os.Getenv("CLAUDE_CODE_SESSION_ID"); id != "" {
			c.Provider, c.SessionID = fav.ProviderClaude, id
		}
	}
	if c.Provider == "" {
		sess, err := detectCodex(c.Cwd)
		if err != nil {
			return c, err
		}
		if sess != nil {
			c.Provider, c.SessionID = fav.ProviderCodex, sess.SessionID
			c.TranscriptPath = sess.Path
		}
	}
	if c.Provider == "" {
		return c, errors.New(i18n.T("capture.no_provider"))
	}
	if c.TranscriptPath == "" {
		c.TranscriptPath = TranscriptPath(c.Provider, c.SessionID)
	}
	return c, nil
}

func (c *Context) fillHerdr() {
	if !herdr.Active() {
		return
	}
	pane, err := herdr.CurrentPane()
	if err != nil {
		return
	}
	if pane.AgentSession != nil && pane.AgentSession.Kind == "id" && pane.AgentSession.Value != "" {
		if p := normalizeAgent(pane.Agent); p != "" {
			c.Provider, c.SessionID = p, pane.AgentSession.Value
		}
	}
	if ws, err := herdr.Workspaces(); err == nil {
		for _, w := range ws {
			if w.WorkspaceID == pane.WorkspaceID {
				c.HerdrWorkspace = w.Label
				break
			}
		}
	}
	if tabs, err := herdr.Tabs(); err == nil {
		for _, t := range tabs {
			if t.TabID == pane.TabID {
				c.HerdrTab = t.Label
				break
			}
		}
	}
}

func normalizeAgent(a string) string {
	switch strings.ToLower(a) {
	case "claude", "claude-code":
		return fav.ProviderClaude
	case "codex", "codex-cli":
		return fav.ProviderCodex
	}
	return ""
}

func (c *Context) fillGit() {
	c.GitRoot = GitOut(c.Cwd, "rev-parse", "--show-toplevel")
	if c.GitRoot == "" {
		return
	}
	c.GitBranch = GitOut(c.Cwd, "rev-parse", "--abbrev-ref", "HEAD")
	c.GitRemote = GitOut(c.Cwd, "remote", "get-url", "origin")
}

func GitOut(dir string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func ClaudeHome() string {
	if h := os.Getenv("CLAUDE_CONFIG_DIR"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func CodexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func codexSessionsDir() string { return filepath.Join(CodexHome(), "sessions") }

// CodexArchivedDir: archiving a thread moves its rollout here, flat.
func CodexArchivedDir() string { return filepath.Join(CodexHome(), "archived_sessions") }

// ClaudeTranscripts are the transcripts of session id ("" = every session): projects/<encoded cwd>/<id>.jsonl.
func ClaudeTranscripts(id string) []string {
	if id == "" {
		id = "*"
	}
	hits, _ := filepath.Glob(filepath.Join(ClaudeHome(), "projects", "*", id+".jsonl"))
	return hits
}

// CodexRollouts: sessions/YYYY/MM/DD/rollout-<time>-<id>.jsonl, then the archived ones (id "" = every session).
func CodexRollouts(id string) []string {
	name := "rollout-*.jsonl"
	if id != "" {
		name = "rollout-*-" + id + ".jsonl"
	}
	hits, _ := filepath.Glob(filepath.Join(codexSessionsDir(), "[0-9][0-9][0-9][0-9]", "[0-9][0-9]", "[0-9][0-9]", name))
	archived, _ := filepath.Glob(filepath.Join(CodexArchivedDir(), name))
	return append(hits, archived...)
}

// TranscriptPath finds a session's transcript by its id, never by re-encoding the cwd.
func TranscriptPath(provider, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	var hits []string
	switch provider {
	case fav.ProviderClaude:
		hits = ClaudeTranscripts(sessionID)
	case fav.ProviderCodex:
		hits = CodexRollouts(sessionID)
	}
	if len(hits) == 0 {
		return ""
	}
	return hits[0]
}
