package output

import (
	"encoding/json"
	"strings"
)

// Ref stands in a line of the log for a field the node moved out of it (slimming): Blob is the sha256 of the content
// kept in the run's blobs/, Omit names a field left out (its content is in git, or a cap was reached: Cap).
type Ref struct {
	Blob  string `json:"$blob,omitempty"`
	Omit  string `json:"$omit,omitempty"`
	Bytes int    `json:"bytes"`
	Lines int    `json:"lines,omitempty"`
	Cap   bool   `json:"cap,omitempty"`
}

// Text is a string field of a line, or the Ref standing in for it.
type Text struct {
	S   string
	Ref *Ref
}

func (t *Text) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '{' {
		var r Ref
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		t.Ref = &r
		return nil
	}
	return json.Unmarshal(b, &t.S)
}

func (t Text) empty() bool { return t.S == "" && t.Ref == nil }

func (t Text) lines() int {
	if t.Ref != nil {
		return t.Ref.Lines
	}
	return Lines(t.S)
}

// Lines counts s's lines, a last one without its newline included.
func Lines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}
