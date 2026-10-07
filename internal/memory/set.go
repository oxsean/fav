// Package memory reads and writes the agents' memories on this machine: Claude's project memory, Codex's project and
// global memories.
package memory

import "time"

// Kinds of a Set.
const (
	KindClaude      = "claude"
	KindCodex       = "codex"
	KindCodexGlobal = "codex_global"
)

// Set is one memory directory's entries and its MEMORY.md.
type Set struct {
	Kind  string `json:"kind"`
	Dir   string `json:"dir"`
	Index string `json:"index,omitempty"` // its MEMORY.md
	Items []Item `json:"items"`
	Lines int    `json:"lines,omitzero"` // of MEMORY.md
	Bytes int64  `json:"bytes,omitzero"`
	Over  bool   `json:"over,omitempty"` // MEMORY.md passes what Claude loads: 200 lines or 25 KB
}

type Item struct {
	File        string    `json:"file"`
	Title       string    `json:"title"`                 // the front matter's name, else its MEMORY.md link text
	Description string    `json:"description,omitempty"` // the front matter's description, else what follows the link's dash
	At          time.Time `json:"at"`
	Size        int64     `json:"size"`
	SHA         string    `json:"sha"`  // of its bytes
	Norm        string    `json:"norm"` // of its text with LF line ends
	InIndex     bool      `json:"in_index,omitempty"`
}
