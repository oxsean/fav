// Package proc holds the process rules that differ by platform: starting a process that outlives its parent's
// session, running a process as a tree that can be ended as a whole, and telling whether a pid still runs. Start,
// signal and probe processes only through here.
package proc

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

// Tree is a started process and whatever it starts.
type Tree struct {
	Cmd *exec.Cmd
	tree
}

// StartTree starts c as a tree. With foreground, c takes over the terminal it shares with this process (an
// interactive agent); otherwise c gets no terminal of its own.
func StartTree(c *exec.Cmd, foreground bool) (*Tree, error) {
	t := &Tree{Cmd: c}
	if err := t.start(foreground); err != nil {
		return nil, err
	}
	return t, nil
}

// Stop asks the tree to end (SIGTERM to its group; on Windows there is no gentle way, so it ends it).
func (t *Tree) Stop() error { return t.stop(false) }

// Kill ends the tree now.
func (t *Tree) Kill() error { return t.stop(true) }

// ErrBatchArgs: a .cmd / .bat target would have cmd.exe read an argument as a command.
var ErrBatchArgs = errors.New("a batch file cannot take this argument safely")

// batchUnsafe: exe is a batch file and one of args holds what cmd.exe interprets.
func batchUnsafe(exe string, args []string) bool {
	switch strings.ToLower(filepath.Ext(exe)) {
	case ".cmd", ".bat":
	default:
		return false
	}
	for _, a := range args {
		if strings.ContainsAny(a, "&|<>^%!\"\r\n") {
			return true
		}
	}
	return false
}
