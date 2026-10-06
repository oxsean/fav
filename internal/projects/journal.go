package projects

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
)

// Journal reads mode 1's projects from the coordinator's log without its lock, while it appends: only the project
// and member events are folded, each read goes on from where the last one stopped.
type Journal struct {
	path string
	off  int64 // where the next line starts
	st   *task.State
}

func NewJournal(path string) *Journal { return &Journal{path: path, st: task.New()} }

// teamTypes are the events a project is folded from; a line holding none of them is skipped unread.
var teamTypes = [][]byte{[]byte(`"type":"` + task.EProjectCreated + `"`), []byte(`"type":"` + task.EProjectEdited + `"`),
	[]byte(`"type":"` + task.EMemberSet + `"`)}

// ⚠️ the journal's own bound on a line: a longer one is skipped
const maxLine = 4 << 20

// Read folds what was appended since the last read; changed: a project changed. A last line written in part waits for
// the next read, and so does one whose sum does not hold (it may be the coordinator's write in progress): the read
// stops before it, never with an error. A log shorter than what was read was rewritten and is read again.
func (j *Journal) Read() (changed bool, err error) {
	fi, err := os.Stat(j.path)
	if errors.Is(err, os.ErrNotExist) {
		if j.off > 0 {
			*j = *NewJournal(j.path)
			changed = true
		}
		return changed, nil
	}
	if err != nil {
		return false, err
	}
	if fi.Size() < j.off {
		*j = *NewJournal(j.path)
		changed = true
	}
	if fi.Size() == j.off {
		return changed, nil
	}
	_, err = fileio.Lines(context.Background(), j.path, j.off, maxLine, func(at int64, line []byte) bool {
		if !slices.ContainsFunc(teamTypes, func(t []byte) bool { return bytes.Contains(line, t) }) {
			j.off = at + int64(len(line))
			return true
		}
		env, err := journal.Parse(line)
		if err != nil {
			return false
		}
		team := env
		team.Events = nil
		for _, e := range env.Events {
			if e.Type == task.EProjectCreated || e.Type == task.EProjectEdited || e.Type == task.EMemberSet {
				team.Events = append(team.Events, e)
			}
		}
		j.off = at + int64(len(line))
		if j.st.Apply(team) == nil { // ⚠️ a whole line the coordinator wrote folded there; one that does not here is passed over
			changed = true
		}
		return true
	})
	return changed, err
}

// Snapshot is mode 1's snapshot of what has been read.
func (j *Journal) Snapshot() Snapshot { return Mine(j.st.Projects) }
