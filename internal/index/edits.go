package index

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
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

// patchHeaders are apply_patch's file lines ("*** Move to:" names the new path of an update).
var patchHeaders = []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "}

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
		for line := range strings.SplitSeq(l.Payload.Input, "\n") {
			for _, h := range patchHeaders {
				if p, ok := strings.CutPrefix(line, h); ok {
					f.edited(strings.TrimSpace(p))
				}
			}
		}
		return
	}
	var blocks []struct {
		Type  string `json:"type"`
		Name  string `json:"name"`
		Input struct {
			FilePath     string `json:"file_path"`
			NotebookPath string `json:"notebook_path"`
		} `json:"input"`
	}
	if len(l.Message.Content) == 0 || l.Message.Content[0] != '[' || json.Unmarshal(l.Message.Content, &blocks) != nil {
		return
	}
	for _, bl := range blocks {
		switch {
		case bl.Type != "tool_use":
		case bl.Name == "Edit" || bl.Name == "Write" || bl.Name == "MultiEdit":
			f.edited(bl.Input.FilePath)
		case bl.Name == "NotebookEdit":
			f.edited(bl.Input.NotebookPath)
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
