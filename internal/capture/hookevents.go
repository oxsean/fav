package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fileio"
)

// hookEvent: a session's latest Claude Code hook event (fav install-hook) and the transcript size at that moment.
type hookEvent struct {
	Event string    `json:"event"`
	At    time.Time `json:"at"`
	Size  int64     `json:"size"`
}

func hookEventPath(sessionID string) string {
	return filepath.Join(fav.Home(), "events", filepath.Base(sessionID)+".json")
}

// RecordHookEvent overwrites the session's event file; errors are dropped, a hook must never get in Claude's way.
func RecordHookEvent(sessionID, event, transcript string) {
	if sessionID == "" || event == "" {
		return
	}
	e := hookEvent{Event: event, At: time.Now()}
	if st, err := os.Stat(transcript); err == nil {
		e.Size = st.Size()
	}
	b, _ := json.Marshal(e)
	fileio.WriteFile(hookEventPath(sessionID), b, 0o644)
}

// HookWaiting: the session's latest hook event is a question for the user (a permission prompt, a dialog) and the
// transcript has not grown since — once it is answered the tool runs and writes, which clears it.
func HookWaiting(sessionID string, size int64) bool {
	b, err := os.ReadFile(hookEventPath(sessionID))
	if err != nil {
		return false
	}
	var e hookEvent
	if json.Unmarshal(b, &e) != nil {
		return false
	}
	return (e.Event == "Notification" || e.Event == "PermissionRequest") && size <= e.Size
}
