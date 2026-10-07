package migrate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// A record's role and state (tend.Copy's).
const (
	RoleTo       = tend.CopyTo
	RoleFrom     = tend.CopyFrom
	StatePending = tend.CopyPending
	StateDone    = tend.CopyDone
	StateAborted = tend.CopyAborted
)

// Record is one line of migrations.jsonl: a migration of a session between this machine and Peer. The last line of a
// migration id wins.
type Record struct {
	Migration string    `json:"migration"`
	Role      string    `json:"role"`
	Provider  string    `json:"provider"`
	SessionID string    `json:"session_id"`
	Peer      Peer      `json:"peer"`
	State     string    `json:"state"`
	At        time.Time `json:"at"`
	Files     int       `json:"files"`
	// Size and SHA are this side's main transcript then: the source's original as sent, the target's rewritten copy.
	Size  int64      `json:"size"`
	SHA   string     `json:"sha"`
	Dir   string     `json:"dir,omitempty"`   // from: the session's directory here
	Note  string     `json:"note,omitempty"`  // from: the migration note the first resume here starts with
	Noted *time.Time `json:"noted,omitzero"`  // from: when a resume sent it
	Moved bool       `json:"moved,omitempty"` // to: the original went to this machine's trash
	// Source is, on the target, the source's main transcript its commit carried: what the source records done.
	Source *Sum `json:"source,omitempty"`
}

// Sum is a main transcript as a migration saw it.
type Sum struct {
	Size int64  `json:"size"`
	SHA  string `json:"sha"`
}

// Copy is r as a list shows it.
func (r Record) Copy() tend.Copy {
	return tend.Copy{Migration: r.Migration, Role: r.Role, State: r.State, Peer: r.Peer.Name, Endpoint: r.Peer.Endpoint, At: r.At}
}

func recordsPath() string { return filepath.Join(tend.Home(), "migrations.jsonl") }

// NewID is a fresh migration id: when it started, and a random part.
func NewID(now time.Time) string {
	return now.UTC().Format("20060102T150405") + "-" + tend.NewID()[:8]
}

func validID(m string) error {
	if !capture.SafeID(m) {
		return &wire.Error{Code: wire.CodeBadRequest, Detail: "migration"}
	}
	return nil
}

// Records are the migrations this machine recorded, the last line of each, oldest first.
func Records() ([]Record, error) {
	b, err := os.ReadFile(recordsPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var order []string
	last := map[string]Record{}
	for line := range bytes.SplitSeq(b, []byte("\n")) {
		var r Record
		if json.Unmarshal(line, &r) != nil || r.Migration == "" {
			continue // ⚠️ a torn last line from a crash is skipped, never fatal
		}
		if _, ok := last[r.Migration]; !ok {
			order = append(order, r.Migration)
		}
		last[r.Migration] = r
	}
	out := make([]Record, len(order))
	for i, m := range order {
		out[i] = last[m]
	}
	return out, nil
}

// Of are the records of provider's session sid, newest first.
func Of(provider, sid string) ([]Record, error) {
	all, err := Records()
	var out []Record
	for _, r := range slices.Backward(all) {
		if r.Provider == provider && r.SessionID == sid {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b Record) int { return b.At.Compare(a.At) })
	return out, err
}

// find is migration m's record, false when there is none.
func find(m string) (Record, bool, error) {
	all, err := Records()
	i := slices.IndexFunc(all, func(r Record) bool { return r.Migration == m })
	if i < 0 {
		return Record{}, false, err
	}
	return all[i], true, err
}

// change appends what next makes of migration m's record (ok: there is one), under the records' lock; next answering
// nil writes nothing.
func change(m string, next func(cur Record, ok bool) (*Record, error)) (Record, error) {
	path := recordsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Record{}, err
	}
	unlock, err := filelock.Lock(path + ".lock")
	if err != nil {
		return Record{}, err
	}
	defer unlock()
	cur, ok, err := find(m)
	if err != nil {
		return Record{}, err
	}
	r, err := next(cur, ok)
	if err != nil || r == nil {
		return cur, err
	}
	line, err := json.Marshal(r)
	if err != nil {
		return Record{}, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return Record{}, err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return Record{}, err
	}
	return *r, f.Close()
}

// Marks is index.Rows.Copies over the records on this machine now: each session's migrations, newest first.
func Marks() func(*tend.Rec) []tend.Copy {
	all, _ := Records()
	by := map[string][]tend.Copy{}
	for _, r := range slices.Backward(all) {
		k := tend.SessionKey(r.Provider, r.SessionID)
		by[k] = append(by[k], r.Copy())
	}
	if len(by) == 0 {
		return nil
	}
	return func(r *tend.Rec) []tend.Copy { return by[r.Key()] }
}
