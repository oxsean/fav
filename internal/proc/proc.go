// Package proc holds the process rules that differ by platform: starting a process that outlives its parent's
// session, running a process as a tree that can be ended as a whole, and telling whether a pid still runs. Start,
// signal and probe processes only through here.
package proc

import "os/exec"

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
