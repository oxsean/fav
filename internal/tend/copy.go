package tend

import "time"

// Copy is a migration of a session between its machine and another, as its machine recorded it: what a list shows.
type Copy struct {
	Migration string
	Role      string // CopyTo | CopyFrom
	State     string // CopyPending | CopyDone | CopyAborted
	Peer      string // the other machine as the migration named it
	Endpoint  string // its hello.endpoint
	At        time.Time
}

// A copy's role: its machine is the migration's source (to) or its target (from).
const (
	CopyTo   = "to"
	CopyFrom = "from"
)

// A copy's state: the source records the intent (pending) and the end (done, aborted); the target only its commit.
const (
	CopyPending = "pending"
	CopyDone    = "done"
	CopyAborted = "aborted"
)

// MigratedHere: one of cs brought the session to its machine.
func MigratedHere(cs []Copy) bool {
	for _, c := range cs {
		if c.Role == CopyFrom && c.State == CopyDone {
			return true
		}
	}
	return false
}
