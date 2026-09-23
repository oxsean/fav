// Package herdr wraps the herdr CLI: exec + JSON.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
)

type Pane struct {
	PaneID       string `json:"pane_id"`
	TabID        string `json:"tab_id"`
	WorkspaceID  string `json:"workspace_id"`
	Agent        string `json:"agent"`
	AgentSession *Agent `json:"agent_session"`
	AgentStatus  string `json:"agent_status"` // working | idle | blocked | done | unknown
	Title        string `json:"terminal_title_stripped"`
	StateSeq     int    `json:"state_change_seq"` // bumps on every status change
	Cwd          string `json:"cwd"`
	Focused      bool   `json:"focused"`
}

type Agent struct {
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Value  string `json:"value"`
}

type Workspace struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Number      int    `json:"number"`
	Focused     bool   `json:"focused"`
	TabCount    int    `json:"tab_count"`
}

type Tab struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Number      int    `json:"number"`
}

// Active: this process runs inside a Herdr pane (Reachable says whether Herdr can be driven).
func Active() bool { return os.Getenv("HERDR_ENV") != "" }

// Reachable: is the herdr service up; independent of HERDR_* env vars, cached.
var reachable struct {
	sync.Once
	ok bool
}

func Reachable() bool {
	reachable.Do(func() {
		_, err := Workspaces()
		reachable.ok = err == nil
	})
	return reachable.ok
}

func output(args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "herdr", args...).Output()
	if err != nil {
		what := strings.Join(args[:min(2, len(args))], " ")
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(bytes.TrimSpace(ee.Stderr)) > 0 {
			return nil, fmt.Errorf("herdr %s: %s", what, bytes.TrimSpace(ee.Stderr))
		}
		return nil, fmt.Errorf("herdr %s: %w", what, err)
	}
	return b, nil
}

// run decodes the result of herdr's JSON envelope into out (nil: ignore it).
func run(args []string, out any) error {
	b, err := output(args)
	if err != nil || out == nil {
		return err
	}
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return i18n.E("herdr.bad_json", strings.Join(args, " "), err)
	}
	return json.Unmarshal(env.Result, out)
}

func CurrentPane() (*Pane, error) {
	var r struct {
		Pane Pane `json:"pane"`
	}
	if err := run([]string{"pane", "current"}, &r); err != nil {
		return nil, err
	}
	return &r.Pane, nil
}

func Workspaces() ([]Workspace, error) {
	var r struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	err := run([]string{"workspace", "list"}, &r)
	return r.Workspaces, err
}

func FindWorkspace(label string) (*Workspace, error) {
	ws, err := Workspaces()
	if err != nil {
		return nil, err
	}
	for i := range ws {
		if strings.EqualFold(ws[i].Label, label) {
			return &ws[i], nil
		}
	}
	return nil, nil
}

// WorkspacesFor: the workspace named label; else those with a pane in cwd's subtree — only those with a pane exactly in cwd
// when there are any. ⚠️ More than one: the caller asks the user, never picks.
func WorkspacesFor(label, cwd string) ([]Workspace, error) {
	if label != "" {
		w, err := FindWorkspace(label)
		if w == nil {
			return nil, err
		}
		return []Workspace{*w}, err
	}
	if cwd == "" {
		return nil, nil
	}
	panes, err := Panes()
	if err != nil {
		return nil, err
	}
	ws, err := Workspaces()
	if err != nil {
		return nil, err
	}
	return matchWorkspaces(panes, ws, cwd), nil
}

func matchWorkspaces(panes []Pane, ws []Workspace, cwd string) []Workspace {
	exact, near := map[string]bool{}, map[string]bool{}
	for _, p := range panes {
		switch {
		case paths.Same(p.Cwd, cwd):
			exact[p.WorkspaceID] = true
		case paths.Nested(p.Cwd, cwd):
			near[p.WorkspaceID] = true
		}
	}
	if len(exact) > 0 {
		near = exact
	}
	var out []Workspace
	for _, w := range ws {
		if near[w.WorkspaceID] {
			out = append(out, w)
		}
	}
	return out
}

func FocusTab(tabID string) error { return run([]string{"tab", "focus", tabID}, nil) }

// CloseTab kills the agent inside.
func CloseTab(tabID string) error { return run([]string{"tab", "close", tabID}, nil) }

func Tabs() ([]Tab, error) {
	var r struct {
		Tabs []Tab `json:"tabs"`
	}
	err := run([]string{"tab", "list"}, &r)
	return r.Tabs, err
}

func Agents() ([]Pane, error) {
	var r struct {
		Agents []Pane `json:"agents"`
	}
	err := run([]string{"agent", "list"}, &r)
	return r.Agents, err
}

func Panes() ([]Pane, error) {
	var r struct {
		Panes []Pane `json:"panes"`
	}
	err := run([]string{"pane", "list"}, &r)
	return r.Panes, err
}

func CreateTab(workspaceID, cwd, label string) (*Pane, error) {
	args := []string{"tab", "create", "--workspace", workspaceID, "--focus"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if label != "" {
		args = append(args, "--label", label)
	}
	var r struct {
		Pane Pane `json:"root_pane"`
		Tab  Tab  `json:"tab"`
	}
	if err := run(args, &r); err != nil {
		return nil, err
	}
	if r.Pane.PaneID == "" {
		return nil, errors.New(i18n.T("herdr.no_pane"))
	}
	r.Pane.TabID = r.Tab.TabID
	return &r.Pane, nil
}

func Rename(tabID, label string) error {
	return run([]string{"tab", "rename", tabID, label}, nil)
}

func TabLabel(tabID string) (string, error) {
	var r struct {
		Tab Tab `json:"tab"`
	}
	err := run([]string{"tab", "get", tabID}, &r)
	return r.Tab.Label, err
}

// Herdr overwrites the tab title from the command line: wait for that, then set it back.
func KeepLabel(tabID, label string) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		if cur, err := TabLabel(tabID); err == nil && cur != label {
			break
		}
	}
	return Rename(tabID, label)
}

// label and note both, because the hook resets label to note
func ReportLabel(paneID, label string) error {
	return run([]string{"pane", "report-metadata", paneID, "--source", "fav",
		"--token", "note=" + label, "--token", "label=" + label}, nil)
}

// Run types a line + Enter into the pane. ⚠️ Goes through a shell: line must already be quoted (CommandSpec.ShellLine).
func Run(paneID, line string) error {
	return run([]string{"pane", "run", paneID, line}, nil)
}

// ReadAgent is the last lines of an agent pane's terminal, as plain text.
func ReadAgent(paneID string, lines int) (string, error) {
	b, err := output([]string{"agent", "read", paneID, "--source", "recent", "--lines", strconv.Itoa(lines), "--format", "text"})
	return string(b), err
}

// PromptAgent submits text as the agent's next prompt; Herdr refuses it while the agent is blocked on a question.
func PromptAgent(paneID, prompt string) error {
	_, err := output([]string{"agent", "prompt", paneID, prompt})
	return err
}

// SendKeys presses keys in an agent pane (answering a prompt such as a permission question).
func SendKeys(paneID string, keys ...string) error {
	_, err := output(append([]string{"agent", "send-keys", paneID}, keys...))
	return err
}
