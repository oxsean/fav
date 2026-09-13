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

// Claude transcripts are found by globbing the session id, not by re-encoding the cwd.
func TranscriptPath(provider, sessionID string) string {
	home, err := os.UserHomeDir()
	if err != nil || sessionID == "" {
		return ""
	}
	switch provider {
	case fav.ProviderClaude:
		hits, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", sessionID+".jsonl"))
		if len(hits) > 0 {
			return hits[0]
		}
	case fav.ProviderCodex:
		return findCodexRollout(sessionID)
	}
	return ""
}

func TranscriptAlive(r *fav.Rec) bool {
	for _, p := range []string{r.PinnedPath, r.TranscriptPath} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
