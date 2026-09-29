package node

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/output"
)

// Where a run keeps what slimming moved out of its log.
const (
	blobsDir  = "blobs"      // blobs/<sha256>: a field's content, once per content
	blobsGone = "blobs.gone" // the node removed blobs/ to stay under its cap
	diffsDir  = "diffs"      // diffs/turn-<id>.patch: codex's last diff of a turn
)

// slimPatterns mark the lines slimming might change; a quote inside a JSON string is always escaped, so text that
// only mentions these never matches.
var slimPatterns = [][]byte{[]byte(`"tool_use_result"`), []byte(`"type":"tool_use"`), []byte(`"type":"fileChange"`),
	[]byte(`"method":"turn/diff/updated"`), []byte(`"method":"item/fileChange/patchUpdated"`), []byte(`"method":"turn/completed"`)}

// slimmer takes the copies of whole files out of a run's output before it is logged: into blobs, or left out when
// git has them (the base tree, the end tree). One supervisor goroutine calls it.
type slimmer struct {
	dir   string
	git   bool
	used  int64             // bytes in blobs/
	diffs map[string]string // codex: each open turn's latest diff
}

func newSlimmer(dir string, git bool) *slimmer {
	sl := &slimmer{dir: dir, git: git, diffs: map[string]string{}}
	sl.used, _ = dirBytes(filepath.Join(dir, blobsDir))
	return sl
}

// dirBytes is the size of the files in dir and how many there are.
func dirBytes(dir string) (int64, int) {
	ents, _ := os.ReadDir(dir)
	var n int64
	count := 0
	for _, e := range ents {
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() {
			n += fi.Size()
			count++
		}
	}
	return n, count
}

// line is line as the log keeps it, keep false to leave it out, and a mark to add where it is logged.
func (sl *slimmer) line(line []byte) (logged []byte, keep bool, mark *Mark) {
	if !containsAny(line, slimPatterns) {
		return line, true, nil
	}
	body := bytes.TrimRight(line, "\r\n")
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return line, true, nil
	}
	changed := false
	if method := str(m["method"]); method != "" {
		var drop bool
		drop, changed, mark = sl.codex(method, m)
		if drop {
			return nil, false, nil
		}
	} else {
		changed = sl.claude(m)
	}
	if !changed {
		return line, true, mark
	}
	return append(encode(m), line[len(body):]...), true, mark
}

func containsAny(b []byte, ps [][]byte) bool {
	for _, p := range ps {
		if bytes.Contains(b, p) {
			return true
		}
	}
	return false
}

// claude slims a claude stream-json line: what a tool call writes, and what its result copies of the files.
func (sl *slimmer) claude(m map[string]json.RawMessage) bool {
	changed := false
	if _, ok := m["tool_use_result"]; ok {
		changed = edit(m, "tool_use_result", func(res map[string]json.RawMessage) bool {
			c := false
			if sl.git {
				c = sl.omit(res, "originalFile", 0)
			} else {
				c = sl.slim(res, "originalFile", slimMin)
			}
			if str(res["type"]) == "update" && sl.git {
				c = sl.omit(res, "content", slimMin) || c
			} else {
				c = sl.slim(res, "content", slimMin) || c
			}
			if p := res["structuredPatch"]; len(p) >= slimPatch {
				if ref := sl.store("structuredPatch", p, 0); ref != nil {
					res["structuredPatch"], c = ref, true
				}
			}
			return edit(res, "file", func(f map[string]json.RawMessage) bool { return sl.slim(f, "content", slimMin) }) || c
		})
	}
	if _, ok := m["message"]; ok {
		changed = edit(m, "message", func(msg map[string]json.RawMessage) bool {
			return eachOf(msg, "content", func(c map[string]json.RawMessage) bool {
				if str(c["type"]) != "tool_use" {
					return false
				}
				return edit(c, "input", sl.callInput)
			})
		}) || changed
	}
	return changed
}

// callInput slims what an edit tool call carries.
func (sl *slimmer) callInput(in map[string]json.RawMessage) bool {
	c := false
	for _, k := range []string{"content", "old_string", "new_string", "new_source"} {
		c = sl.slim(in, k, slimMin) || c
	}
	return eachOf(in, "edits", func(e map[string]json.RawMessage) bool {
		o := sl.slim(e, "old_string", slimMin)
		return sl.slim(e, "new_string", slimMin) || o
	}) || c
}

// codex slims a codex app-server line: drop tells to leave it out.
func (sl *slimmer) codex(method string, m map[string]json.RawMessage) (drop, changed bool, mark *Mark) {
	var p struct {
		TurnID string `json:"turnId"`
		Diff   string `json:"diff"`
		Turn   struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	json.Unmarshal(m["params"], &p)
	switch method {
	case "turn/diff/updated":
		if p.TurnID != "" {
			sl.diffs[p.TurnID] = p.Diff
		}
		return true, false, nil
	case "item/fileChange/patchUpdated":
		return true, false, nil
	case "turn/completed":
		d, ok := sl.diffs[p.Turn.ID]
		if !ok {
			return false, false, nil
		}
		delete(sl.diffs, p.Turn.ID)
		files, add, del := diffStat(d)
		if files == 0 && add == 0 && del == 0 {
			return false, false, nil
		}
		name := filepath.Join(sl.dir, diffsDir, "turn-"+fileSafe(p.Turn.ID)+".patch")
		if fileio.WriteFile(name, []byte(d), 0o600) != nil {
			return false, false, nil
		}
		return false, false, &Mark{Event: markDiff, ID: p.Turn.ID, Files: files, Add: add, Del: del}
	case "item/started", "item/completed":
		changed = edit(m, "params", func(pm map[string]json.RawMessage) bool {
			return edit(pm, "item", func(it map[string]json.RawMessage) bool {
				if str(it["type"]) != "fileChange" {
					return false
				}
				return eachOf(it, "changes", func(ch map[string]json.RawMessage) bool { return sl.slim(ch, "diff", slimPatch) })
			})
		})
	}
	return false, changed, nil
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// fileSafe is id as a file name: itself when it is one, its hash otherwise.
func fileSafe(id string) string {
	if safeName.MatchString(id) {
		return id
	}
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:16])
}

// diffStat counts a unified diff's files and lines added and removed.
func diffStat(d string) (files, add, del int) {
	plus := 0
	for l := range strings.Lines(d) {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			files++
		case strings.HasPrefix(l, "+++ "):
			plus++
		case strings.HasPrefix(l, "--- "):
		case strings.HasPrefix(l, "+"):
			add++
		case strings.HasPrefix(l, "-"):
			del++
		}
	}
	if files == 0 {
		files = plus
	}
	return files, add, del
}

// slim moves string field k of m into a blob when it is at least min bytes; past the run's cap it is left out.
func (sl *slimmer) slim(m map[string]json.RawMessage, k string, min int) bool {
	s, ok := text(m[k])
	if !ok || len(s) < min {
		return false
	}
	ref := sl.store(k, []byte(s), output.Lines(s))
	if ref == nil {
		return false // the blob could not be written: the content stays where it is
	}
	m[k] = ref
	return true
}

// omit leaves string field k of m out when it is at least min bytes: its content is in git.
func (sl *slimmer) omit(m map[string]json.RawMessage, k string, min int) bool {
	s, ok := text(m[k])
	if !ok || len(s) < min || len(s) == 0 {
		return false
	}
	m[k] = encode(output.Ref{Omit: k, Bytes: len(s), Lines: output.Lines(s)})
	return true
}

// store keeps b, field k's content, in a blob (once per content) and answers the ref standing in for it; past the
// run's cap the ref only says k was left out. nil: the blob could not be written.
func (sl *slimmer) store(k string, b []byte, lines int) json.RawMessage {
	h := sha256.Sum256(b)
	sum := hex.EncodeToString(h[:])
	path := filepath.Join(sl.dir, blobsDir, sum)
	if _, err := os.Stat(path); err != nil {
		if sl.used+int64(len(b)) > maxRunBlobs || fileExists(filepath.Join(sl.dir, blobsGone)) {
			return encode(output.Ref{Omit: k, Bytes: len(b), Lines: lines, Cap: true})
		}
		// ⚠️ the blob is whole before the line naming it is logged: temp file, then rename
		if err := fileio.WriteAtomic(path, 0o600, func(w io.Writer) error { _, err := w.Write(b); return err }); err != nil {
			return nil
		}
		sl.used += int64(len(b))
	}
	return encode(output.Ref{Blob: sum, Bytes: len(b), Lines: lines})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// edit applies f to the object at m[k], writing it back when f changed it.
func edit(m map[string]json.RawMessage, k string, f func(map[string]json.RawMessage) bool) bool {
	var inner map[string]json.RawMessage
	if json.Unmarshal(m[k], &inner) != nil || inner == nil || !f(inner) {
		return false
	}
	m[k] = encode(inner)
	return true
}

// eachOf applies f to each object of the array at m[k], writing it back when f changed one.
func eachOf(m map[string]json.RawMessage, k string, f func(map[string]json.RawMessage) bool) bool {
	var items []json.RawMessage
	if json.Unmarshal(m[k], &items) != nil {
		return false
	}
	changed := false
	for i, it := range items {
		var o map[string]json.RawMessage
		if json.Unmarshal(it, &o) != nil || o == nil || !f(o) {
			continue
		}
		items[i], changed = encode(o), true
	}
	if changed {
		m[k] = encode(items)
	}
	return changed
}

func text(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	return s, json.Unmarshal(raw, &s) == nil
}

func str(raw json.RawMessage) string {
	s, _ := text(raw)
	return s
}

// encode is v as JSON without escaping <>&, as the agents write it.
func encode(v any) json.RawMessage {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}
