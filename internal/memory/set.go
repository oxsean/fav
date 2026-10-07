// Package memory reads and writes the agents' memories on this machine: Claude's project memory and Codex's global
// memories.
package memory

import "time"

// Kinds of a Set.
const (
	KindClaude      = "claude"
	KindCodexGlobal = "codex_global"
)

// Set is one memory directory's entries and its MEMORY.md; Codex's global memories are the blocks of one MEMORY.md
// that apply to Dir, or to no directory when Dir is "".
type Set struct {
	Kind  string `json:"kind"`
	Dir   string `json:"dir"`
	Index string `json:"index,omitempty"` // its MEMORY.md
	Items []Item `json:"items"`
	// Incoming are Claude's memories under .incoming/: written there because another held the name, never indexed,
	// waiting to be merged by hand.
	Incoming []Item `json:"incoming,omitempty"`
	Lines    int    `json:"lines,omitzero"` // of MEMORY.md
	Bytes    int64  `json:"bytes,omitzero"`
	Over     bool   `json:"over,omitempty"` // MEMORY.md passes what Claude loads: 200 lines or 25 KB
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
	Line        int       `json:"line,omitzero"` // a Codex block: the line of File it starts on
}
