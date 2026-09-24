package capture

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/filelock"
)

// RunSession is a session a tend run started on this machine.
type RunSession struct {
	Run      string
	Provider string
	Dir      string
	Title    string
	Open     bool // its supervisor is alive and has not recorded an end
}

// RunSessions are the sessions of this machine's runs (<home>/node/runs, written by internal/node), by session id.
// Their CLIs mark them one-shot, but tend started them for a task: they are listed and guarded like any session.
func RunSessions() map[string]RunSession {
	root := filepath.Join(fav.Home(), "node", "runs")
	ents, _ := os.ReadDir(root)
	out := map[string]RunSession{}
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
		}
		readJSONFile(filepath.Join(dir, "spec.json"), &spec)
		ended := readJSONFile(filepath.Join(dir, "state.json"), &st) &&
			(st.State == "exited" || st.State == "stopped" || st.State == "failed")
		sid := st.Session
		if sid == "" {
			sid = spec.Session
		}
		if sid == "" {
			continue
		}
		out[sid] = RunSession{Run: e.Name(), Provider: spec.Provider, Dir: spec.Dir, Title: spec.Title,
			Open: !ended && filelock.Held(filepath.Join(dir, "lock"))}
	}
	return out
}

func readJSONFile(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
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
