package index

import (
	"bytes"
	"encoding/json"
	"path/filepath"

	"github.com/oxsean/fav/internal/capture"
)

var editNames = [][]byte{[]byte(`"name":"Edit"`), []byte(`"name":"Write"`), []byte(`"name":"MultiEdit"`), []byte(`"name":"NotebookEdit"`)}

// isEdit is a cheap pre-filter for lines where the AI wrote a file.
func isEdit(b []byte) bool {
	if bytes.Contains(b, []byte(`"custom_tool_call"`)) {
		return bytes.Contains(b, []byte(`"apply_patch"`))
	}
	if !bytes.Contains(b, []byte(`"tool_use"`)) {
		return false
	}
	for _, n := range editNames {
		if bytes.Contains(b, n) {
			return true
		}
	}
	return false
}

func (f *File) takeEdits(b []byte) {
	var l struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		Payload struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input string `json:"input"`
		} `json:"payload"`
	}
	if json.Unmarshal(b, &l) != nil {
		return
	}
	if l.Payload.Type == "custom_tool_call" && l.Payload.Name == "apply_patch" {
		for _, p := range capture.PatchFiles(l.Payload.Input) {
			f.edited(p)
		}
		return
	}
	var blocks []struct {
		Type  string          `json:"type"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if len(l.Message.Content) == 0 || l.Message.Content[0] != '[' || json.Unmarshal(l.Message.Content, &blocks) != nil {
		return
	}
	for _, bl := range blocks {
		if bl.Type == "tool_use" {
			f.edited(capture.EditedPath(bl.Name, bl.Input))
		}
	}
}

// edited counts one write of p; a relative path (apply_patch) is resolved against the session's cwd; agent scratch files are not counted.
func (f *File) edited(p string) {
	if p == "" {
		return
	}
	if !filepath.IsAbs(p) {
		if f.Cwd == "" {
			return
		}
		p = filepath.Join(f.Cwd, p)
	}
	p = filepath.Clean(p)
	if AgentScratch(p) {
		return
	}
	if f.Files == nil {
		f.Files = map[string]int{}
	}
	if _, ok := f.Files[p]; ok || len(f.Files) < filesCap {
		f.Files[p]++
	}
}
