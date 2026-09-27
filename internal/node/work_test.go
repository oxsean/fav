package node

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/paths"
)

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := (gitIn{dir: dir}).run(args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// repo is a checkout on branch main with one commit.
func repo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "--quiet", "--initial-branch=main")
	git(t, dir, "config", "user.email", "dev@example.com")
	git(t, dir, "config", "user.name", "Dev")
	os.WriteFile(filepath.Join(dir, "README"), []byte("app\n"), 0o644)
	git(t, dir, "add", "README")
	git(t, dir, "commit", "--quiet", "-m", "start")
	return dir
}

func finished(t *testing.T, n *Node, id string) Snapshot {
	t.Helper()
	return wait(t, n, id, func(s Snapshot) bool { return Terminal(s.State.State) })
}

func TestATaskWorksOnItsBranchInItsOwnWorktree(t *testing.T) {
	needGit(t)
	n := New(t.TempDir())
	n.Limits.AllowHooks = true
	checkout, setup := repo(t), filepath.Join(t.TempDir(), "setup")
	w := &Workspace{Checkout: checkout, Branch: "tend/t_1", Base: "main", Setup: []string{os.Args[0], "_touch", setup}}
	s := start(t, n, StartParams{Task: "t_1", Title: "Add notes", Profile: fake("--steps", "1", "--every", "10ms", "--write", "notes.txt"),
		Brief: "write notes", Work: w})
	end := finished(t, n, s.Run)
	wt := filepath.Join(checkout+"-wt", "t_1")
	if end.State.State != StateExited || *end.ExitCode != 0 || end.Work == nil || end.Work.Branch != "tend/t_1" || end.Work.Commits != 1 ||
		end.Work.Head != git(t, checkout, "rev-parse", "tend/t_1") || !samePlace(end.Work.Dir, wt) || end.Work.Diffstat == "" {
		t.Fatalf("%+v %+v", end.State, end.Work)
	}
	if !slices.ContainsFunc(end.Work.Warnings, func(w string) bool { return strings.Contains(w, "committed 1 files") }) {
		t.Fatalf("what the agent left is committed: %q", end.Work.Warnings)
	}
	if where, _ := os.ReadFile(setup); !samePlace(string(where), wt) {
		t.Fatalf("the setup hook runs in the new worktree: %q", where)
	}
	if git(t, checkout, "status", "--porcelain") != "" || git(t, checkout, "branch", "--show-current") != "main" {
		t.Fatal("the checkout is left alone")
	}
	brief, _ := os.ReadFile(filepath.Join(n.runDir(s.Run), "prompt.md"))
	if !strings.Contains(string(brief), "branch tend/t_1") {
		t.Fatalf("the agent is told where it works:\n%s", brief)
	}

	os.Remove(setup)
	again := finished(t, n, start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "10ms", "--write", "notes.txt"),
		Brief: "more", Work: w}).Run)
	if again.Work.Commits != 2 || paths.Exists(setup) {
		t.Fatalf("the next run goes on in the same worktree, set up once: %+v", again.Work)
	}

	head := again.Work.Head
	review := finished(t, n, start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "10ms", "--write", "junk.txt"),
		Brief: "review", Work: &Workspace{Checkout: checkout, Branch: "tend/t_1", Base: "main", ReadOnly: true}}).Run)
	if review.Work == nil || review.Work.Head != head || review.Work.Discarded != 1 || git(t, checkout, "rev-parse", "tend/t_1") != head {
		t.Fatalf("a review sees the head and what it changes is thrown away: %+v", review.Work)
	}
	if ents, _ := os.ReadDir(filepath.Join(checkout+"-wt", ".ro")); len(ents) != 0 {
		t.Fatalf("its copy is removed: %v", ents)
	}
}

func TestSiblingsWorkAtOnceAndMergeIntoTheirParent(t *testing.T) {
	needGit(t)
	n := New(t.TempDir())
	n.Limits.AllowHooks = true
	checkout, cleaned := repo(t), filepath.Join(t.TempDir(), "cleaned")
	child := func(task, file string) Snapshot {
		return start(t, n, StartParams{Task: task, Profile: fake("--steps", "3", "--every", "100ms", "--write", file), Brief: task,
			Work: &Workspace{Checkout: checkout, Branch: "tend/" + task, Chain: []string{"tend/p"}, Base: "main"}})
	}
	a, b, c := child("a", "shared.txt"), child("b", "shared.txt"), child("c", "c.txt")
	for _, s := range []Snapshot{a, b, c} {
		if end := finished(t, n, s.Run); *end.ExitCode != 0 {
			t.Fatalf("siblings in their own worktrees run side by side: %+v", end.State)
		}
	}
	merge := func(task string) Snapshot {
		return finished(t, n, start(t, n, StartParams{Task: task, Title: "task " + task, Work: &Workspace{Checkout: checkout, Branch: "tend/p",
			Base: "main", Merge: "tend/" + task, Cleanup: []string{os.Args[0], "_touch", cleaned}}}).Run)
	}
	if m := merge("a"); m.State.State != StateExited || *m.ExitCode != 0 || !m.Work.Merged || paths.Exists(filepath.Join(checkout+"-wt", "a")) {
		t.Fatalf("a merges and its worktree goes: %+v %+v", m.State, m.Work)
	}
	if where, _ := os.ReadFile(cleaned); !samePlace(string(where), filepath.Join(checkout+"-wt", "a")) {
		t.Fatalf("the cleanup hook runs in the merged worktree first: %q", where)
	}
	if m := merge("c"); *m.ExitCode != 0 || !m.Work.Merged {
		t.Fatalf("c merges: %+v", m.Work)
	}
	integration := filepath.Join(checkout+"-wt", "p")
	m := merge("b")
	if *m.ExitCode != 1 || m.State.Reason != ReasonMergeConflict || m.Work.Merged || !slices.Equal(m.Work.Conflict, []string{"shared.txt"}) {
		t.Fatalf("b conflicts with a: %+v %+v", m.State, m.Work)
	}
	if git(t, integration, "status", "--porcelain") != "" || !paths.Exists(filepath.Join(checkout+"-wt", "b")) {
		t.Fatal("the merge is undone and b's worktree stays")
	}
	if log := git(t, integration, "log", "--format=%s", "-3"); !strings.Contains(log, "Merge tend/c: task c") || !strings.Contains(log, "Merge tend/a") {
		t.Fatalf("the integration branch holds a and c:\n%s", log)
	}
	if again := merge("a"); *again.ExitCode != 0 || !again.Work.Merged {
		t.Fatalf("merging a merged branch again is done at once: %+v", again.Work)
	}
}

func TestARemoteCarriesTheBranchToAnotherMachine(t *testing.T) {
	needGit(t)
	first := repo(t)
	remote := filepath.Join(t.TempDir(), "app.git")
	git(t, first, "init", "--quiet", "--bare", "--initial-branch=main", remote)
	git(t, first, "push", "--quiet", remote, "main")
	second := filepath.Join(t.TempDir(), "app")
	git(t, filepath.Dir(second), "clone", "--quiet", remote, second)

	n1, n2 := New(t.TempDir()), New(t.TempDir())
	w := Workspace{Checkout: first, Branch: "tend/t_1", Base: "main", Remote: remote}
	made := finished(t, n1, start(t, n1, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "10ms", "--write", "notes.txt"),
		Brief: "x", Work: &w}).Run)
	if len(made.Work.Warnings) != 1 || git(t, first, "ls-remote", remote, "tend/t_1") == "" {
		t.Fatalf("the branch is pushed: %+v", made.Work)
	}
	w.Checkout, w.ReadOnly = second, true
	seen := finished(t, n2, start(t, n2, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "10ms"), Brief: "review", Work: &w}).Run)
	if seen.Work == nil || seen.Work.Head != made.Work.Head {
		t.Fatalf("another machine reviews the same commit: %+v %+v", seen.State, seen.Work)
	}
}

func TestAWorkspaceIsChecked(t *testing.T) {
	n := New(t.TempDir())
	checkout := t.TempDir()
	for _, w := range []Workspace{{Checkout: checkout, Branch: "main"}, {Checkout: checkout, Branch: "tend/x", Base: "--upload-pack=x"},
		{Checkout: "rel", Branch: "tend/x"}, {Checkout: checkout, Branch: "tend/x", Remote: "-oProxy"}, {Checkout: checkout, Branch: "tend/x", Chain: []string{"tend/../x"}}} {
		if _, err := n.Start(StartParams{Run: NewRunID(), Task: "t", Coordinator: "c", Profile: fake(), Work: &w}); err == nil {
			t.Errorf("accepted %+v", w)
		}
	}
	if _, err := n.Start(StartParams{Run: NewRunID(), Task: "t", Coordinator: "c", Profile: fake(),
		Work: &Workspace{Checkout: checkout, Branch: "tend/x", Setup: []string{"make"}}}); err == nil {
		t.Error("a hook needs node.allow_hooks")
	}
}
