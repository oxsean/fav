package node

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/shell"
)

// touchedFile lists what the agent touched: the files its tools edited and the paths its commands named. A directory
// run lists only these; outside git the edits' first bases are what the changes are counted from.
const touchedFile = "touched.jsonl"

// touch is a line of touched.jsonl, the first time a path is touched one way.
type touch struct {
	Path string `json:"path"`           // absolute
	Via  string `json:"via"`            // edit | cmd
	Base string `json:"base,omitempty"` // outside git: the blob of the file before the agent's first edit of it
	New  bool   `json:"new,omitempty"`  // outside git: the file was not there before
	Lost bool   `json:"lost,omitempty"` // outside git: what it was before is not known (the cap, or no copy of it)
}

const (
	viaEdit, viaCmd = "edit", "cmd"
	maxCmdPaths     = 100 // paths read from one command
)

// touchPatterns mark the lines touch reads besides the ones slimming reads.
var touchPatterns = [][]byte{[]byte(`"name":"Bash"`), []byte(`"type":"commandExecution"`)}

// touch records what line m says the agent touched.
func (sl *slimmer) touch(m map[string]json.RawMessage) {
	if sl.cwd == "" {
		return
	}
	var line struct {
		Method string `json:"method"`
		Result *struct {
			Type         string          `json:"type"`
			FilePath     string          `json:"filePath"`
			OriginalFile json.RawMessage `json:"originalFile"`
		} `json:"tool_use_result"`
		Message struct {
			Content []struct {
				Type  string `json:"type"`
				Name  string `json:"name"`
				Input struct {
					Command string `json:"command"`
				} `json:"input"`
			} `json:"content"`
		} `json:"message"`
		Params struct {
			Item struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Cwd     string `json:"cwd"`
				Changes []struct {
					Path string `json:"path"`
					Kind struct {
						Type     string `json:"type"`
						MovePath string `json:"move_path"`
					} `json:"kind"`
				} `json:"changes"`
			} `json:"item"`
		} `json:"params"`
	}
	b, _ := json.Marshal(m)
	if json.Unmarshal(b, &line) != nil {
		return
	}
	if r := line.Result; r != nil && r.FilePath != "" {
		sl.edited(r.FilePath, func() touch {
			if s, ok := text(r.OriginalFile); ok && r.Type != "create" {
				return sl.base([]byte(s))
			}
			if r.Type == "create" || string(r.OriginalFile) == "null" {
				return touch{New: true}
			}
			return touch{Lost: true}
		})
	}
	for _, c := range line.Message.Content {
		if c.Type == "tool_use" && c.Name == "Bash" {
			sl.named(c.Input.Command, sl.cwd)
		}
	}
	it := line.Params.Item
	switch {
	case it.Type == "commandExecution" && line.Method == "item/started":
		sl.named(it.Command, cmp.Or(it.Cwd, sl.cwd))
	case it.Type == "fileChange" && (line.Method == "item/started" || line.Method == "item/completed"):
		started := line.Method == "item/started"
		for _, ch := range it.Changes {
			path := ch.Path
			sl.edited(path, func() touch {
				switch {
				case ch.Kind.Type == "add":
					return touch{New: true}
				case !started:
					return touch{Lost: true}
				}
				b, err := os.ReadFile(paths.From(sl.cwd, path))
				if err != nil {
					return touch{Lost: true}
				}
				return sl.base(b)
			})
			if ch.Kind.MovePath != "" {
				sl.edited(ch.Kind.MovePath, func() touch { return touch{New: true} })
			}
		}
	}
}

// edited records the agent's tools editing path; outside git what it was before comes from before, once.
func (sl *slimmer) edited(path string, before func() touch) {
	p := paths.From(sl.cwd, path)
	if sl.seen[viaEdit+"\x00"+p] {
		return
	}
	sl.seen[viaEdit+"\x00"+p] = true
	t := touch{}
	if !sl.git {
		t = before()
	}
	t.Path, t.Via = p, viaEdit
	appendLine(filepath.Join(sl.dir, touchedFile), t)
}

// base keeps b as a file's content before the agent's first edit.
func (sl *slimmer) base(b []byte) touch {
	sum, ok := sl.keep(b)
	if !ok {
		return touch{Lost: true}
	}
	return touch{Base: sum}
}

// named records the paths under the run's directory that command, run in cwd, names.
func (sl *slimmer) named(command, cwd string) {
	words, ok := shell.POSIX.Split(command)
	if !ok {
		words = strings.Fields(command)
	}
	n := 0
	program := true // the word is a program, not what it works on
	for _, w := range words {
		if w == "&&" || w == "||" || w == "|" || w == ";" {
			program = true
			continue
		}
		if program {
			program = false
			continue
		}
		w = strings.TrimLeft(w, "<>&|0123456789")
		if i := strings.IndexByte(w, '='); i >= 0 {
			w = w[i+1:]
		}
		if w == "" || w[0] == '-' || strings.Contains(w, "://") || strings.ContainsAny(w, "*?$`\n") {
			continue
		}
		p := paths.From(cwd, w)
		if !paths.Under(p, sl.cwd) || p == sl.cwd || sl.seen[viaCmd+"\x00"+p] {
			continue
		}
		sl.seen[viaCmd+"\x00"+p] = true
		appendLine(filepath.Join(sl.dir, touchedFile), touch{Path: p, Via: viaCmd})
		if n++; n == maxCmdPaths {
			return
		}
	}
}

// readTouched is what touched.jsonl holds, the first line of each path and way.
func readTouched(dir string) []touch {
	ts, _ := linesFrom(filepath.Join(dir, touchedFile), 0, func(t touch) bool { return t.Path != "" })
	return ts
}
