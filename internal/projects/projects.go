// Package projects is where the session lists learn which project each session's directory belongs to: the projects
// its viewer sees and the machines' names, from this machine's journal (mode 1) or from the server (mode 2).
package projects

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// What a snapshot can say about the projects.
const (
	Ready    = "ok"
	Offline  = "offline" // the server cannot be reached (or is still being dialed)
	Outdated = "old"     // the server lacks project.attach or does not say who it answers as
)

// Viewer is who the projects are seen by: the coordinator's caller.
type Viewer struct {
	User  string `json:"user"`
	Admin bool   `json:"admin,omitempty"`
}

// Snapshot is what a source knows. Sessions belong to projects only when State is Ready; otherwise every session
// stays in its automatic group.
type Snapshot struct {
	State    string                   `json:"state"`
	At       time.Time                `json:"at"`
	Here     string                   `json:"here,omitempty"`   // this machine's name to the coordinator; "": none of its machines
	OS       map[string]string        `json:"os,omitempty"`     // machine → GOOS, where known
	Owners   map[string]string        `json:"owners,omitempty"` // machine → its owner; nil: the viewer owns every machine (mode 1)
	Viewer   Viewer                   `json:"viewer"`
	Projects map[string]*task.Project `json:"projects,omitempty"`

	memo map[[2]string]*task.Project
}

// Mine is mode 1's snapshot of projects: this machine is local and its owner sees and owns everything.
func Mine(ps map[string]*task.Project) Snapshot {
	return Snapshot{State: Ready, At: time.Now(), Here: coord.Local, OS: map[string]string{coord.Local: runtime.GOOS},
		Viewer: Viewer{User: coord.Owner.User, Admin: coord.Owner.Admin}, Projects: ps}
}

// Served is mode 2's snapshot: the projects state.watch (or state.get) gave, the machines, and this machine found by
// its node id. A server whose hello lacks project.attach or caller is old.
func Served(hello remote.Hello, ps map[string]*task.Project, machines []coord.Machine, nodeID string) Snapshot {
	s := Snapshot{State: Ready, At: time.Now(), OS: map[string]string{}, Owners: map[string]string{}, Projects: ps}
	if hello.Caller == nil || !slices.Contains(hello.Methods, coord.MProjectAttach) {
		return Snapshot{State: Outdated, At: s.At}
	}
	s.Viewer = Viewer{User: hello.Caller.User, Admin: hello.Caller.Admin}
	for _, m := range machines {
		if m.OS != "" {
			s.OS[m.Name] = m.OS
		}
		s.Owners[m.Name] = m.Owner
		if nodeID != "" && m.NodeID == nodeID {
			s.Here = m.Name
		}
	}
	return s
}

// Down is a snapshot that knows no projects, for why (Offline, Outdated).
func Down(why string) Snapshot { return Snapshot{State: why, At: time.Now()} }

func (s *Snapshot) Ready() bool { return s.State == Ready }

// Machine is the coordinator's name for the machine r is on; "" when this machine is none of its machines.
func (s *Snapshot) Machine(r *tend.Rec) string { return cmp.Or(r.Host, s.Here) }

// GOOS is machine's system: as the coordinator knows it, this one's for this machine, else guessed from dir's form.
func (s *Snapshot) GOOS(machine, dir string) string {
	if g := s.OS[machine]; g != "" {
		return g
	}
	if machine != "" && machine == s.Here {
		return runtime.GOOS
	}
	if pathmap.Drive(dir) || strings.HasPrefix(dir, `\\`) {
		return "windows"
	}
	return "linux"
}

// Dir is the directory that decides r's project: its main checkout when it ran in a linked worktree, else its cwd.
func Dir(r *tend.Rec) string { return cmp.Or(r.Repo, r.Cwd) }

// Of is the project r belongs to, nil for none.
func (s *Snapshot) Of(r *tend.Rec) *task.Project {
	return s.Holding(s.Machine(r), Dir(r))
}

// DirPairs are r's project's directories on r's machine and on machine to ("" this one), the pairs a session's
// directory is moved between when it is handed from one to the other.
func (s *Snapshot) DirPairs(r *tend.Rec, to string) []remote.DirPair {
	return Pairs(s.Of(r), s.Machine(r), cmp.Or(to, s.Here))
}

// Pairs are p's repositories' directories on machines from and to, those found on both.
func Pairs(p *task.Project, from, to string) []remote.DirPair {
	if p == nil {
		return nil
	}
	var out []remote.DirPair
	for _, rp := range p.Repos {
		if a, b := rp.Dirs[from], rp.Dirs[to]; a != "" && b != "" {
			out = append(out, remote.DirPair{From: a, To: b})
		}
	}
	return out
}

// Holding is the project holding dir on machine, nil for none.
func (s *Snapshot) Holding(machine, dir string) *task.Project {
	if !s.Ready() || machine == "" || dir == "" || len(s.Projects) == 0 {
		return nil
	}
	k := [2]string{machine, dir}
	if p, ok := s.memo[k]; ok {
		return p
	}
	p := task.ProjectOf(s.Projects, machine, s.GOOS(machine, dir), dir)
	if s.memo == nil {
		s.memo = map[[2]string]*task.Project{}
	}
	s.memo[k] = p
	return p
}

// Belong is index.Rows.Belong over s.
func (s *Snapshot) Belong(r *tend.Rec) (id, name string) {
	if p := s.Of(r); p != nil {
		return p.ID, p.Name
	}
	return "", ""
}

// Owner is who owns machine.
func (s *Snapshot) Owner(machine string) string {
	if s.Owners == nil {
		return s.Viewer.User
	}
	return s.Owners[machine]
}

// MayAttach: the viewer may change p's directories on machine.
func (s *Snapshot) MayAttach(p *task.Project, machine string) bool {
	return s.Ready() && task.MayAttach(p, s.Viewer.User, s.Viewer.Admin, s.Owner(machine))
}

// Attachable are the projects the viewer may add a directory on machine to, by name.
func (s *Snapshot) Attachable(machine string) []*task.Project {
	var out []*task.Project
	for _, id := range slices.Sorted(maps.Keys(s.Projects)) {
		if p := s.Projects[id]; s.MayAttach(p, machine) {
			out = append(out, p)
		}
	}
	slices.SortStableFunc(out, func(a, b *task.Project) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out
}

// Personal: p has no member besides its owner.
func Personal(p *task.Project) bool {
	for u, role := range p.Members {
		if u != p.Owner && role != "" {
			return false
		}
	}
	return true
}

// Participants counts p's owner and participants.
func Participants(p *task.Project) int {
	n := 0
	if p.Owner != "" {
		n++
	}
	for u, role := range p.Members {
		if u != p.Owner && role == task.RoleParticipant {
			n++
		}
	}
	return n
}

// NodeID is this machine's node identity as kept under home, "" when it has none yet; never made here.
func NodeID(home string) string {
	b, err := os.ReadFile(filepath.Join(home, "node", "id"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Fetch asks a mode 2 coordinator for the snapshot: its hello, the state and the machines.
func Fetch(ctx context.Context, cl *coord.Client, nodeID string) (Snapshot, error) {
	var h remote.Hello
	if err := cl.Call(ctx, remote.MHello, remote.HelloParams{Role: "client"}, &h); err != nil {
		return Snapshot{}, err
	}
	if s := Served(h, nil, nil, ""); !s.Ready() {
		return s, nil
	}
	var st task.State
	if err := cl.Call(ctx, coord.MStateGet, coord.StateParams{NoBriefs: true}, &st); err != nil {
		return Snapshot{}, err
	}
	var ms coord.Machines
	if err := cl.Call(ctx, coord.MMachineList, coord.MachinesParams{}, &ms); err != nil {
		return Snapshot{}, err
	}
	return Served(h, st.Projects, ms.Machines, nodeID), nil
}

// TableFor is how long the project table stays good: fzf lists again on every keystroke, each a new process.
const TableFor = 30 * time.Second

// TablePath is mode 2's project table under home.
func TablePath(home string) string { return filepath.Join(home, "projects.json") }

type table struct {
	Snapshot
	Failed time.Time `json:"failed_at,omitzero"` // the last fetch failed then: not tried again within TableFor
}

// LoadTable is the snapshot at path while it is fresh; failed: a fetch failed within TableFor, so none is tried.
func LoadTable(path string, now time.Time) (s Snapshot, fresh, failed bool) {
	var t table
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &t) != nil {
		return Snapshot{}, false, false
	}
	if !t.Failed.IsZero() && now.Sub(t.Failed) < TableFor && now.Sub(t.Failed) >= 0 {
		return Snapshot{}, false, true
	}
	if t.State == "" || now.Sub(t.At) >= TableFor || now.Before(t.At) {
		return Snapshot{}, false, false
	}
	return t.Snapshot, true, false
}

// SaveTable keeps s at path for TableFor; a zero s with failed records that a fetch failed now.
func SaveTable(path string, s Snapshot, failed time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(table{Snapshot: s, Failed: failed})
	if err != nil {
		return err
	}
	return fileio.WriteFile(path, b, 0o600)
}
