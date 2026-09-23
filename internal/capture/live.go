package capture

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/herdr"
)

type Live struct {
	PaneID, TabID string
	Status        string // from Herdr: working | idle | blocked | done | unknown
	Agent, Title  string // fills the card when the index does not know the session yet
	Cwd           string
	Seq           int
	Since         time.Time
	BackgroundID  string // short id for claude attach
}

// IdleAfter: a running agent whose transcript has not been written for this long counts as idle (cleanup suggestions).
const IdleAfter = 4 * time.Hour

// LiveSessions merges Claude sessions/*.json, Codex thread locks and Herdr by session id; an unavailable source counts as empty.
func LiveSessions() map[string]Live {
	local := LocalLive()
	return MergeLive(local, HerdrLive(local, nil))
}

// prev is the previous result: unchanged status and seq keep their Since.
// Herdr re-reads a pane's Claude session id only on a state change, so an id without a sessions/*.json is stale and dropped.
func HerdrLive(local, prev map[string]Live) map[string]Live {
	agents, err := herdr.Agents()
	if err != nil {
		return nil
	}
	now := time.Now()
	_, tracked := os.Stat(filepath.Join(ClaudeHome(), "sessions"))
	out := map[string]Live{}
	for _, a := range agents {
		if a.AgentSession == nil || a.AgentSession.Value == "" {
			continue
		}
		if a.Agent == fav.ProviderClaude && tracked == nil {
			if _, ok := local[a.AgentSession.Value]; !ok {
				continue
			}
		}
		l := Live{PaneID: a.PaneID, TabID: a.TabID, Status: a.AgentStatus, Seq: a.StateSeq, Since: now, Agent: a.Agent, Title: a.Title, Cwd: a.Cwd}
		if p, ok := prev[a.AgentSession.Value]; ok && p.Status == l.Status && p.Seq == l.Seq {
			l.Since = p.Since
		}
		out[a.AgentSession.Value] = l
	}
	return out
}

// ClaudeLive reads ~/.claude/sessions/<pid>.json (session id, cwd, busy/idle, status time; kind=bg carries the jobId for attach); dead pids are skipped.
func ClaudeLive() map[string]Live {
	out := map[string]Live{}
	files, _ := filepath.Glob(filepath.Join(ClaudeHome(), "sessions", "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s struct {
			PID             int    `json:"pid"`
			SessionID       string `json:"sessionId"`
			Cwd             string `json:"cwd"`
			Kind            string `json:"kind"`
			Name            string `json:"name"`
			Status          string `json:"status"`
			StatusUpdatedAt int64  `json:"statusUpdatedAt"`
			JobID           string `json:"jobId"`
			ParkedJobID     string `json:"parkedJobId"` // the conversation moved on to a background worker; this process is only the terminal
		}
		if json.Unmarshal(b, &s) != nil || s.SessionID == "" || s.ParkedJobID != "" || !alive(s.PID) {
			continue
		}
		l := Live{Agent: fav.ProviderClaude, Title: s.Name, Cwd: s.Cwd, Since: time.UnixMilli(s.StatusUpdatedAt)}
		switch s.Status {
		case "busy":
			l.Status = "working"
		case "idle":
			l.Status = "idle"
		}
		if s.Kind == "bg" {
			l.BackgroundID = s.JobID
		}
		out[s.SessionID] = l
	}
	return out
}

// Non-interactive Codex originators: the Claude Code plugin, codex exec, the SDK.
var codexSkipOriginators = map[string]bool{"Claude Code": true, "codex_exec": true, "multica-agent-sdk": true}

// CodexOneOff: not a human thread — a non-interactive originator, or a sub-agent (parent_thread_id).
func CodexOneOff(originator string, parent *string) bool {
	return codexSkipOriginators[originator] || (parent != nil && *parent != "")
}

// Locks are probed every 3 s but the rollout head is read once per id; the TUI poller and the move flow query concurrently, hence the mutex.
var (
	codexOneOffByID = map[string]bool{}
	codexOneOffMu   sync.Mutex
)

// No rollout yet: a fresh thread writes one within seconds; a lock older than a minute with no rollout is an app-server companion task.
func codexOneOff(id, lock string) bool {
	codexOneOffMu.Lock()
	defer codexOneOffMu.Unlock()
	if v, ok := codexOneOffByID[id]; ok {
		return v
	}
	hits, _ := filepath.Glob(filepath.Join(codexSessionsDir(), "*", "[0-9][0-9]", "[0-9][0-9]", "rollout-*-"+id+".jsonl"))
	if len(hits) == 0 {
		fi, err := os.Stat(lock)
		return err == nil && time.Since(fi.ModTime()) > time.Minute
	}
	f, err := os.Open(hits[0])
	if err != nil {
		return false
	}
	defer f.Close()
	var meta struct {
		Payload struct {
			Originator     string  `json:"originator"`
			ParentThreadID *string `json:"parent_thread_id"`
		} `json:"payload"`
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	if sc.Scan() {
		json.Unmarshal(sc.Bytes(), &meta)
	}
	codexOneOffByID[id] = CodexOneOff(meta.Payload.Originator, meta.Payload.ParentThreadID)
	return codexOneOffByID[id]
}

// CodexLive: a held flock means the thread is open. The Claude Code plugin's app-server keeps holding locks of threads it ran; those do not count.
func CodexLive() map[string]Live {
	out := map[string]Live{}
	locks, _ := filepath.Glob(filepath.Join(filepath.Dir(codexSessionsDir()), "thread-writer-locks", "*.lock"))
	for _, f := range locks {
		id := strings.TrimSuffix(filepath.Base(f), ".lock")
		if strings.HasPrefix(id, ".") || !filelock.Held(f) || codexOneOff(id, f) {
			continue
		}
		out[id] = Live{Agent: fav.ProviderCodex}
	}
	return out
}

// ClaudeHome is Claude Code's data directory: CLAUDE_CONFIG_DIR, else ~/.claude.
func ClaudeHome() string {
	if h := os.Getenv("CLAUDE_CONFIG_DIR"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// CodexHome is Codex's data directory: CODEX_HOME, else ~/.codex.
func CodexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func LocalLive() map[string]Live { return MergeLive(CodexLive(), ClaudeLive()) }

// later non-empty fields win
func MergeLive(maps ...map[string]Live) map[string]Live {
	out := map[string]Live{}
	for _, m := range maps {
		for k, v := range m {
			l := out[k]
			if v.PaneID != "" {
				l.PaneID = v.PaneID
			}
			if v.TabID != "" {
				l.TabID = v.TabID
			}
			if v.Status != "" {
				l.Status, l.Seq, l.Since = v.Status, v.Seq, v.Since
			}
			if v.Agent != "" {
				l.Agent = v.Agent
			}
			if v.Title != "" {
				l.Title = v.Title
			}
			if v.Cwd != "" {
				l.Cwd = v.Cwd
			}
			if v.BackgroundID != "" {
				l.BackgroundID = v.BackgroundID
			}
			out[k] = l
		}
	}
	return out
}

// background sessions cannot --resume
func BuildAttach(r *fav.Rec, backgroundID string) CommandSpec {
	return CommandSpec{Exec: "claude", Args: []string{"attach", backgroundID}, Cwd: r.Cwd}
}
