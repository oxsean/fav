package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/tend"
)

// HookCall is what a Claude Code hook (tend install-hook) hands tend on stdin, as far as tend keeps it.
type HookCall struct {
	Session, Transcript string
	Event               string          // hook_event_name
	Kind                string          // Notification: notification_type (permission_prompt, elicitation_dialog, …)
	Message             string          // Notification: message
	Tool                string          // PermissionRequest: tool_name
	Input               json.RawMessage // PermissionRequest: tool_input
}

// hookEvent: a session's latest hook event and the transcript size at that moment.
type hookEvent struct {
	Event   string    `json:"event"`
	Kind    string    `json:"kind,omitempty"`
	Tool    string    `json:"tool,omitempty"`
	Summary string    `json:"summary,omitempty"` // the tool's input in a line, else the notification's message
	At      time.Time `json:"at"`
	Size    int64     `json:"size"`
}

const (
	hookPermission   = "PermissionRequest"
	hookNotification = "Notification"
	kindPermission   = "permission_prompt"
	hookSummaryCap   = 200
)

func hookEventPath(sessionID string) string {
	return filepath.Join(tend.Home(), "events", filepath.Base(sessionID)+".json")
}

func readHookEvent(sessionID string) (hookEvent, bool) {
	var e hookEvent
	b, err := os.ReadFile(hookEventPath(sessionID))
	return e, err == nil && json.Unmarshal(b, &e) == nil
}

// RecordHookEvent overwrites the session's event file; errors are dropped, a hook must never get in Claude's way.
// The notification of a permission prompt keeps the tool its PermissionRequest named at the same transcript size.
func RecordHookEvent(c HookCall) {
	if c.Session == "" || c.Event == "" {
		return
	}
	e := hookEvent{Event: c.Event, Kind: c.Kind, Tool: c.Tool, At: time.Now()}
	if st, err := os.Stat(c.Transcript); err == nil {
		e.Size = st.Size()
	}
	switch {
	case c.Tool != "":
		e.Summary = clip(strings.Join(strings.Fields(toolArg(c.Tool, c.Input, 1)), " "), hookSummaryCap)
	case c.Event == hookNotification:
		e.Summary = clip(strings.Join(strings.Fields(c.Message), " "), hookSummaryCap)
	}
	if c.Event == hookNotification && c.Kind == kindPermission {
		if was, ok := readHookEvent(c.Session); ok && was.Event == hookPermission && was.Size == e.Size && was.Tool != "" {
			e.Tool, e.Summary = was.Tool, was.Summary
		}
	}
	b, _ := json.Marshal(e)
	fileio.WriteFile(hookEventPath(c.Session), b, 0o644)
}

// HookWait is what a session's agent waits on the user for, as its latest hook event tells.
type HookWait struct {
	Permission bool   // a tool waits for permission; otherwise the hook cannot tell what it waits on
	Tool       string // the tool, when the hook named it
	Summary    string // the tool's input in a line, or the notification's own words
}

// HookWaiting: the session's latest hook event is a prompt for the user (a permission prompt, a dialog) and the
// transcript has not grown since — once it is answered the tool runs and writes, which clears it. A permission
// request for a tool that asks the user (AskUserQuestion, ExitPlanMode) is no permission.
func HookWaiting(sessionID string, size int64) (HookWait, bool) {
	e, ok := readHookEvent(sessionID)
	if !ok || (e.Event != hookNotification && e.Event != hookPermission) || size > e.Size {
		return HookWait{}, false
	}
	if askTools[e.Tool] {
		return HookWait{}, true
	}
	w := HookWait{Permission: e.Event == hookPermission || e.Kind == kindPermission, Tool: e.Tool, Summary: e.Summary}
	if !w.Permission {
		w.Summary = ""
	}
	return w, true
}
