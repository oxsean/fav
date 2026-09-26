package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/wire"
)

func TestReadingASnapshotWritesNothing(t *testing.T) {
	n := New(t.TempDir())
	n.Launch = noLaunch
	s := start(t, n, StartParams{Task: "t_1", Profile: fake()})
	age(t, n, s.Run)
	dir := n.runDir(s.Run)
	for range 3 {
		if got, _ := n.Snapshot(s.Run); got.State.State != StateFailed || got.Reason != "not_launched" {
			t.Fatalf("%+v", got)
		}
	}
	if paths.Exists(filepath.Join(dir, "state.json")) || paths.Exists(filepath.Join(dir, "claim")) {
		t.Fatal("a snapshot only reads")
	}
	if err := Supervise(dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := n.Snapshot(s.Run); got.State.State != StateFailed || got.Reason != "not_launched" || got.Pid != 0 || !paths.Exists(filepath.Join(dir, "state.json")) {
		t.Fatalf("a supervisor coming too late records the run not launched and starts nothing: %+v", got)
	}
}

func TestARunDirectoryAppearsWhole(t *testing.T) {
	n := New(t.TempDir())
	var seen []string
	n.Launch = func(dir string, _ Spec) (string, error) {
		for _, f := range []string{"spec.json", "prompt.md"} {
			if paths.Exists(filepath.Join(dir, f)) {
				seen = append(seen, f)
			}
		}
		return "", nil
	}
	s := start(t, n, StartParams{Task: "t_1", Profile: fake(), Brief: "b"})
	if len(seen) != 2 {
		t.Fatalf("the supervisor starts on a whole directory: %v", seen)
	}
	ents, _ := os.ReadDir(filepath.Join(n.Dir, "runs"))
	if len(ents) != 1 || ents[0].Name() != s.Run {
		t.Fatalf("nothing half-made is left beside it: %v", ents)
	}
	leftover := filepath.Join(n.Dir, "runs", ".new-r_0123456789ab-x")
	os.MkdirAll(leftover, 0o700)
	if runs, _ := n.List("c1", nil); len(runs) != 1 {
		t.Fatalf("a directory still being made is not a run: %+v", runs)
	}
}

func TestTheNodeRefusesASecondRunInABusyDirectory(t *testing.T) {
	n := New(t.TempDir())
	dir := t.TempDir()
	a := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "3", "--every", "300ms"), Dir: dir, Coordinator: "other"})
	wait(t, n, a.Run, func(s Snapshot) bool { return s.State.State == StateRunning })
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skip(err)
	}
	for _, d := range []string{dir, link} {
		_, err := n.Start(StartParams{Run: NewRunID(), Task: "t_2", Profile: fake(), Dir: d, Coordinator: "c1", Runner: RunnerBackground})
		if wire.Code(err) != wire.CodeConflict || !strings.Contains(err.Error(), "dir_busy "+a.Run) {
			t.Fatalf("%s: %v", d, err)
		}
	}
	other := start(t, n, StartParams{Task: "t_3", Profile: fake("--steps", "1", "--every", "10ms")})
	if other.State.State == StateFailed {
		t.Fatalf("another directory is free: %+v", other)
	}
	wait(t, n, a.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if s := start(t, n, StartParams{Task: "t_2", Profile: fake("--steps", "1", "--every", "10ms"), Dir: dir}); s.State.State == StateFailed {
		t.Fatalf("the directory is free once the run ended: %+v", s)
	}
}

func TestNodeSlotsBoundItsRuns(t *testing.T) {
	n := New(t.TempDir())
	n.Limits.Slots = 1
	a := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "2", "--every", "300ms")})
	wait(t, n, a.Run, func(s Snapshot) bool { return s.State.State == StateRunning })
	_, err := n.Start(StartParams{Run: NewRunID(), Task: "t_2", Profile: fake(), Dir: t.TempDir(), Coordinator: "c1", Runner: RunnerBackground})
	if wire.Code(err) != wire.CodeConflict || !strings.Contains(err.Error(), "slots 1/1") {
		t.Fatal(err)
	}
}

func TestAListOfSomeRunsReadsOnlyThose(t *testing.T) {
	n := New(t.TempDir())
	n.Launch = noLaunch
	a := start(t, n, StartParams{Task: "t_1", Profile: fake()})
	start(t, n, StartParams{Task: "t_2", Profile: fake()})
	n.swept = time.Now()
	runs, err := n.List("c1", nil, a.Run, "r_000000000000")
	if err != nil || len(runs) != 1 || runs[0].Run != a.Run {
		t.Fatalf("%+v %v", runs, err)
	}
	if runs, _ := n.List("other", nil, a.Run); len(runs) != 0 {
		t.Fatal("another coordinator's run is not listed")
	}
}
