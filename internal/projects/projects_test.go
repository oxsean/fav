package projects

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/coordtest"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

func appendEvents(t *testing.T, l *journal.Log, events ...journal.Event) {
	t.Helper()
	if _, err := l.Append(journal.System, nil, events); err != nil {
		t.Fatal(err)
	}
}

func readOK(t *testing.T, j *Journal) bool {
	t.Helper()
	changed, err := j.Read()
	if err != nil {
		t.Fatal(err)
	}
	return changed
}

// TestJournalFollowsAnotherWriter: what the coordinator (another process, holding the log open) appends shows on the
// next read; tasks and runs are passed over; a line written in part, or whose sum does not hold, stops the read before
// it without an error and is read once it is whole; a log rewritten shorter is read again from its start.
func TestJournalFollowsAnotherWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord", "events.jsonl")
	l, err := journal.Open(path, func(journal.Envelope) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	j := NewJournal(path)
	if readOK(t, j) || len(j.Snapshot().Projects) != 0 {
		t.Fatal("an empty log has projects")
	}
	appendEvents(t, l, journal.NewEvent(task.ETaskCreated, task.Task{ID: "t1", Title: "x"}))
	appendEvents(t, l, journal.NewEvent(task.EProjectCreated, task.Project{ID: "p1", Name: "Shop", Owner: "local"}))
	if !readOK(t, j) || j.Snapshot().Projects["p1"].Name != "Shop" {
		t.Fatalf("the created project is not read: %+v", j.Snapshot().Projects)
	}
	if readOK(t, j) {
		t.Fatal("nothing new, still changed")
	}

	repos := []task.Repo{{Name: "web", Dirs: map[string]string{"local": "/src/shop"}}}
	line, err := journal.Line(journal.Envelope{Seq: 3, At: time.Now(), Events: []journal.Event{
		journal.NewEvent(task.EProjectEdited, task.ProjectEdit{ID: "p1", Repos: &repos})}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Write(line[:len(line)/2])
	if readOK(t, j) || len(j.Snapshot().Projects["p1"].Repos) != 0 {
		t.Fatal("half a line was folded")
	}
	f.Write(line[len(line)/2:])
	if !readOK(t, j) || len(j.Snapshot().Projects["p1"].Repos) != 1 {
		t.Fatal("the line made whole is not read")
	}

	bad, _ := journal.Line(journal.Envelope{Seq: 4, At: time.Now(), Events: []journal.Event{
		journal.NewEvent(task.EProjectEdited, task.ProjectEdit{ID: "p1", Name: new("Shop 2")})}})
	bad[len(bad)-5] ^= 1
	f.Write(bad)
	if readOK(t, j) || j.Snapshot().Projects["p1"].Name != "Shop" {
		t.Fatal("a line whose sum does not hold was folded")
	}
	if readOK(t, j) {
		t.Fatal("the bad line changed something on a second read")
	}

	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !readOK(t, j) || len(j.Snapshot().Projects) != 0 {
		t.Fatal("a log rewritten shorter keeps the old projects")
	}
}

// TestSnapshotPlacesSessions: a session belongs to the project holding its main checkout (worktree) or its cwd on its
// machine; mode 2 finds this machine by its node id, and a server without caller or project.attach is old.
func TestSnapshotPlacesSessions(t *testing.T) {
	ps := map[string]*task.Project{
		"p1": {ID: "p1", Name: "Shop", Owner: "ann", Members: map[string]string{"bob": task.RoleParticipant},
			Repos: []task.Repo{{Name: "web", Dirs: map[string]string{"mac": "/src/shop", "win": `D:\src\shop`}}}},
	}
	machines := []coord.Machine{{Name: "mac", NodeID: "n_mac", OS: "darwin", Owner: "ann"}, {Name: "win", OS: "windows", Owner: "bob"}}
	hello := remote.Hello{Methods: []string{coord.MProjectAttach}, Caller: &remote.Caller{User: "bob"}}
	s := Served(hello, ps, machines, "n_mac")
	if s.Here != "mac" || !s.Ready() {
		t.Fatalf("this machine is %q (%s)", s.Here, s.State)
	}
	for _, c := range []struct {
		r    tend.Rec
		want string
	}{
		{tend.Rec{Cwd: "/src/shop/web"}, "p1"},
		{tend.Rec{Cwd: "/src/shop-wt/feature", Repo: "/src/shop"}, "p1"},
		{tend.Rec{Cwd: "/src/other"}, ""},
		{tend.Rec{Host: "win", Cwd: `d:\SRC\shop\api`}, "p1"},
		{tend.Rec{Host: "linux", Cwd: "/src/shop"}, ""},
	} {
		if id, _ := s.Belong(&c.r); id != c.want {
			t.Errorf("%+v belongs to %q, want %q", c.r, id, c.want)
		}
	}
	if !s.MayAttach(ps["p1"], "win") || s.MayAttach(ps["p1"], "mac") || len(s.Attachable("mac")) != 0 {
		t.Error("a participant changes directories on their own machine only")
	}
	if s := Served(hello, ps, machines, "n_other"); s.Here != "" {
		t.Errorf("a machine that is no node of the server is %q", s.Here)
	} else if id, _ := s.Belong(&tend.Rec{Cwd: "/src/shop"}); id != "" {
		t.Error("this machine's sessions belong to a project although it is none of the server's machines")
	}
	for _, h := range []remote.Hello{{Methods: []string{coord.MProjectAttach}}, {Caller: &remote.Caller{User: "bob"}}} {
		if s := Served(h, ps, machines, "n_mac"); s.State != Outdated {
			t.Errorf("hello %+v: %s, want old", h, s.State)
		}
	}
	mine := Mine(ps)
	if id, _ := mine.Belong(&tend.Rec{Host: "mac", Cwd: "/src/shop"}); id != "p1" || !mine.MayAttach(ps["p1"], "anything") {
		t.Error("mode 1: the owner may change every project on every machine")
	}
}

// TestTableLastsThirtySeconds: the table is used while it is fresh, never after; a failed fetch is not tried again
// within the same time.
func TestTableLastsThirtySeconds(t *testing.T) {
	path := TablePath(t.TempDir())
	now := time.Now()
	if _, fresh, failed := LoadTable(path, now); fresh || failed {
		t.Fatal("no table is fresh")
	}
	s := Mine(map[string]*task.Project{"p1": {ID: "p1", Name: "Shop"}})
	s.At = now
	if err := SaveTable(path, s, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Errorf("the table is %v", fi.Mode().Perm())
	}
	if got, fresh, _ := LoadTable(path, now.Add(29*time.Second)); !fresh || got.Projects["p1"].Name != "Shop" {
		t.Fatal("a table of 29 seconds is not used")
	}
	if _, fresh, _ := LoadTable(path, now.Add(31*time.Second)); fresh {
		t.Fatal("a table of 31 seconds is used")
	}
	SaveTable(path, Snapshot{}, now)
	if _, fresh, failed := LoadTable(path, now.Add(10*time.Second)); fresh || !failed {
		t.Fatal("a failed fetch is tried again at once")
	}
	if _, _, failed := LoadTable(path, now.Add(40*time.Second)); failed {
		t.Fatal("a failed fetch holds off the next one for good")
	}
}

// TestFetchFromATeamCoordinator: mode 2's snapshot comes from the server as its caller sees it: the projects, this
// machine found by its node id, who the caller is.
func TestFetchFromATeamCoordinator(t *testing.T) {
	ann := coord.Principal{User: "u_ann"}
	tm := coordtest.NewTeam(t, coord.User{ID: ann.User, Name: "Ann"})
	n := node.New(t.TempDir())
	tm.Attach("mac", ann.User, n)
	cl := tm.Client(ann, wire.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := cl.CallCommand(ctx, coord.MProjectCreate, "c1", coord.ProjectCreate{ID: "p1", Name: "Shop"}, nil); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := cl.CallCommand(ctx, coord.MProjectAttach, "c2", coord.ProjectAttach{Project: "p1", Machine: "mac", Dir: dir}, nil); err != nil {
		t.Fatal(err)
	}
	s, err := Fetch(ctx, cl, n.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !s.Ready() || s.Here != "mac" || s.Viewer.User != ann.User || s.Viewer.Admin {
		t.Fatalf("snapshot %+v", s)
	}
	if id, name := s.Belong(&tend.Rec{Cwd: filepath.Join(dir, "sub")}); id != "p1" || name != "Shop" {
		t.Fatalf("a session under the attached directory is in %q", id)
	}
	if got := s.Attachable("mac"); len(got) != 1 {
		t.Fatalf("the owner may attach on her machine: %v", got)
	}
}
