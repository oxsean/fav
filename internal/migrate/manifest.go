// Package migrate moves a Claude session to another machine: its manifest, the staging and commit on the target, the
// cwd rewrite and the migration records on both ends.
package migrate

// Kinds of a manifest file.
const (
	KindTranscript = "transcript" // the session's .jsonl and its chain's: cwd is rewritten
	KindSide       = "side"       // a .jsonl under the session's own directory: cwd is rewritten
	KindCopy       = "copy"       // copied as it is
)

// Manifest is what a migration copies of one session.
type Manifest struct {
	Files   []File   `json:"files"`
	Aliases []string `json:"aliases,omitempty"` // older ids of its continuation chain (index.Session.Aliases)
}

type File struct {
	Path  string `json:"path"` // relative to the Claude home, / separated
	Kind  string `json:"kind"`
	Size  int64  `json:"size"`
	SHA   string `json:"sha"`
	Lines int    `json:"lines,omitzero"`
	ID    string `json:"id"` // fileio.ID when planned
}
