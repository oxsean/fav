package capture

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/proc"
	"github.com/oxsean/fav/internal/tend"
)

// RunSession is a session a tend run started on this machine.
type RunSession struct {
	Run      string `json:"run"`
	Provider string `json:"provider,omitempty"`
	Dir      string `json:"dir,omitempty"`
	Title    string `json:"title,omitempty"`
	Open     bool   `json:"-"` // it has not recorded an end, and its supervisor or its agent is alive
}

// keptSessions lists, one JSON line each, the sessions of runs whose directories are gone.
const keptSessions = "sessions.jsonl"

// KeepRunSession records session sid of a run whose directory under nodeDir is about to go.
func KeepRunSession(nodeDir, sid string, r RunSession) error {
	b, err := json.Marshal(struct {
		Session string `json:"session"`
		RunSession
	}{sid, r})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(nodeDir, keptSessions), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// RunSessions are the sessions of this machine's runs (<home>/node/runs, written by internal/node, and the ones kept
// after their directories went), by session id. Their CLIs mark them one-shot, but tend started them for a task:
// they are listed and guarded like any session.
func RunSessions() map[string]RunSession {
	nodeDir := filepath.Join(tend.Home(), "node")
	out := map[string]RunSession{}
	fileio.Lines(context.Background(), filepath.Join(nodeDir, keptSessions), 0, 64<<10, func(_ int64, line []byte) bool {
		var k struct {
			Session string `json:"session"`
			RunSession
		}
		if json.Unmarshal(line, &k) == nil && k.Session != "" {
			out[k.Session] = k.RunSession
		}
		return true
	})
	root := filepath.Join(nodeDir, "runs")
	ents, _ := os.ReadDir(root)
	for _, e := range ents {
		dir := filepath.Join(root, e.Name())
		var spec struct {
			Provider string `json:"provider"`
			Session  string `json:"session"`
			Dir      string `json:"dir"`
			Title    string `json:"title"`
		}
		var st struct {
			State   string `json:"state"`
			Session string `json:"session"`
			Pid     int    `json:"pid"`
		}
		fileio.ReadJSON(filepath.Join(dir, "spec.json"), &spec)
		ended := fileio.ReadJSON(filepath.Join(dir, "state.json"), &st) == nil &&
			(st.State == "exited" || st.State == "stopped" || st.State == "failed")
		sid := st.Session
		if sid == "" {
			sid = spec.Session
		}
		if sid == "" {
			continue
		}
		out[sid] = RunSession{Run: e.Name(), Provider: spec.Provider, Dir: spec.Dir, Title: spec.Title,
			Open: !ended && (filelock.Held(filepath.Join(dir, "lock")) || proc.Alive(st.Pid))}
	}
	return out
}

// RunLive: the open runs as live sessions.
func RunLive() map[string]Live {
	out := map[string]Live{}
	for sid, r := range RunSessions() {
		if r.Open {
			out[sid] = Live{Agent: r.Provider, Title: r.Title, Cwd: r.Dir, Run: r.Run}
		}
	}
	return out
}
