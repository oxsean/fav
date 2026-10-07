// Package migrate moves a Claude session to another machine: its manifest, the staging and commit on the target, the
// cwd rewrite and the migration records on both ends.
package migrate

import (
	"path"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/wire"
)

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
	// Left is what the Claude home holds of the session outside the directories a migration copies (session-env/ may
	// hold environment variables): named, never copied.
	Left []string `json:"left,omitempty"`
}

type File struct {
	Path    string    `json:"path"` // relative to the Claude home, / separated
	Kind    string    `json:"kind"`
	Size    int64     `json:"size"`
	SHA     string    `json:"sha"`
	Lines   int       `json:"lines,omitzero"`
	ID      string    `json:"id"`             // fileio.ID when planned
	ModTime time.Time `json:"mtime,omitzero"` // kept on the copy: the index orders by it
}

// checkAliases: each alias of session sid is another session id, once: they name files and are matched as patterns.
func checkAliases(sid string, aliases []string) error {
	seen := map[string]bool{sid: true}
	for _, a := range aliases {
		if !capture.SafeID(a) || seen[a] {
			return &wire.Error{Code: wire.CodeBadRequest, Detail: "aliases"}
		}
		seen[a] = true
	}
	return nil
}

// Size is the bytes of every file.
func (m Manifest) Size() int64 {
	var n int64
	for _, f := range m.Files {
		n += f.Size
	}
	return n
}

// File is the manifest's file at p.
func (m Manifest) File(p string) (File, bool) {
	for _, f := range m.Files {
		if f.Path == p {
			return f, true
		}
	}
	return File{}, false
}

// Main is the transcript of sid itself (not of its chain): what the records compare.
func (m Manifest) Main(sid string) (File, bool) {
	for _, f := range m.Files {
		if f.Kind == KindTranscript && path.Base(f.Path) == sid+".jsonl" {
			return f, true
		}
	}
	return File{}, false
}

// The directories of the Claude home a migration copies from: projects/<project>/<id>.jsonl and <id>/…,
// file-history/<id>/…, todos/<id>….
const (
	dirProjects    = "projects"
	dirFileHistory = "file-history"
	dirTodos       = "todos"
)

// kindOf is how a manifest path of one of ids is copied; false when a migration does not copy such a path: outside
// the three directories, another session's, or not a plain relative path.
func kindOf(p string, ids []string) (string, bool) {
	if p == "" || strings.Contains(p, `\`) || path.IsAbs(p) || path.Clean(p) != p || strings.HasPrefix(p, "../") || p == ".." {
		return "", false
	}
	segs := strings.Split(p, "/")
	ofSession := func(name string, exact bool) bool {
		for _, id := range ids {
			if exact && name == id || !exact && strings.HasPrefix(name, id) {
				return true
			}
		}
		return false
	}
	switch segs[0] {
	case dirProjects:
		switch {
		case len(segs) == 3 && strings.HasSuffix(segs[2], ".jsonl") && ofSession(strings.TrimSuffix(segs[2], ".jsonl"), true):
			return KindTranscript, true
		case len(segs) > 3 && ofSession(segs[2], true):
			if strings.HasSuffix(p, ".jsonl") {
				return KindSide, true
			}
			return KindCopy, true
		}
	case dirFileHistory:
		if len(segs) > 2 && ofSession(segs[1], true) {
			return KindCopy, true
		}
	case dirTodos:
		if len(segs) >= 2 && ofSession(segs[1], false) {
			return KindCopy, true
		}
	}
	return "", false
}
