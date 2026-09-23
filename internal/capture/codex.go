package capture

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/oxsean/fav/internal/paths"
)

// Codex has no session id env var: the first rollout line (session_meta) carries session_id and cwd, mtime is recency.
const codexActiveWindow = 5 * time.Minute

type Candidate struct {
	SessionID string
	Cwd       string
	Path      string
	ModTime   time.Time
}

func detectCodex(cwd string) (*Candidate, error) {
	cands := activeCodexSessions(cwd, time.Now())
	switch len(cands) {
	case 0:
		return nil, nil
	case 1:
		return &cands[0], nil
	default:
		return nil, &ErrAmbiguous{Candidates: cands}
	}
}

// only today's and yesterday's date directories
func activeCodexSessions(cwd string, now time.Time) []Candidate {
	root := codexSessionsDir()
	if root == "" {
		return nil
	}
	var out []Candidate
	for _, day := range []time.Time{now, now.AddDate(0, 0, -1)} {
		dir := filepath.Join(root, day.Format("2006"), day.Format("01"), day.Format("02"))
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
				continue
			}
			info, err := e.Info()
			if err != nil || now.Sub(info.ModTime()) > codexActiveWindow {
				continue
			}
			path := filepath.Join(dir, e.Name())
			meta, err := readSessionMeta(path)
			if err != nil || !paths.Same(meta.Cwd, cwd) {
				continue
			}
			out = append(out, Candidate{
				SessionID: meta.SessionID, Cwd: meta.Cwd,
				Path: path, ModTime: info.ModTime(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out
}

type sessionMeta struct {
	SessionID      string  `json:"session_id"`
	Cwd            string  `json:"cwd"`
	Originator     string  `json:"originator"`
	ParentThreadID *string `json:"parent_thread_id"`
}

// ⚠️ readSessionMeta reads only the first line (session_meta): rollouts reach hundreds of MB.
func readSessionMeta(path string) (*sessionMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 64*1024)
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	var env struct {
		Payload sessionMeta `json:"payload"`
	}
	if err := json.Unmarshal(line, &env); err != nil {
		return nil, err
	}
	return &env.Payload, nil
}
