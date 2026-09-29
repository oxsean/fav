package coord

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

func gitDo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// checkout is a repository on main with one commit.
func checkout(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := filepath.Join(t.TempDir(), "app")
	os.MkdirAll(dir, 0o755)
	gitDo(t, dir, "init", "--quiet", "--initial-branch=main")
	gitDo(t, dir, "config", "user.email", "dev@example.com")
	gitDo(t, dir, "config", "user.name", "Dev")
	os.WriteFile(filepath.Join(dir, "README"), []byte("app\n"), 0o644)
	gitDo(t, dir, "add", "README")
	gitDo(t, dir, "commit", "--quiet", "-m", "start")
	return dir
}

func writing(name, file string) tend.AgentProfile {
	return tend.AgentProfile{Name: name, Provider: agent.ProviderFake, Args: []string{"--steps", "2", "--every", "600ms", "--write", file}}
}

func TestSubtasksWorkOnTheirBranchesAndMergeIntoTheirParent(t *testing.T) {
	dir := checkout(t)
	e := newEnv(t, tend.Config{Agents: []tend.AgentProfile{writing("shared", "shared.txt"), writing("own", "own.txt")},
		Machines: map[string]tend.MachineConfig{Local: {Slots: 4}}})
	e.start()
	e.must(MProjectCreate, ProjectCreate{ID: "p1", Name: "One"}, nil)
	e.must(MProjectEdit, task.ProjectEdit{ID: "p1", Repos: &[]task.Repo{{Name: "app", Base: "main", Dirs: map[string]string{Local: dir},
		Worktrees: true}}}, nil)
	create := func(p TaskCreate) *task.Task { // no directory: they work in the project's repository
		var x task.Task
		p.Status = task.StatusBacklog
		e.must(MTaskCreate, p, &x)
		return &x
	}
	parent := create(TaskCreate{Title: "export", Project: "p1"})
	a := create(TaskCreate{Title: "csv", Parent: parent.ID, Agent: "shared"})
	b := create(TaskCreate{Title: "tsv", Parent: parent.ID, Agent: "own"})
	c := create(TaskCreate{Title: "json", Parent: parent.ID, Agent: "shared"})
	e.must(MTaskStart, TaskRef{ID: parent.ID}, nil)

	var conflicted *task.Task
	st := e.until("two merged, one in conflict", func(s *task.State) bool {
		done, waiting := 0, 0
		for _, id := range []string{a.ID, b.ID, c.ID} {
			switch sit := s.Situation(s.Tasks[id]); {
			case sit.Kind == task.SitDone:
				done++
			case sit.Reason == task.WhyMergeConflict:
				waiting, conflicted = waiting+1, s.Tasks[id]
			}
		}
		return done == 2 && waiting == 1
	})
	if conflicted.ID == b.ID || st.Tasks[b.ID].Branch != "tend/"+b.ID || !st.Tasks[b.ID].Merged || st.Tasks[parent.ID].Branch != "tend/"+parent.ID {
		t.Fatalf("csv and json both write shared.txt: %+v", st.Tasks)
	}
	started := map[string]bool{}
	for _, r := range st.Runs {
		if r.Stage != task.StageMerge {
			started[r.Task] = r.Work != nil && r.Work.Branch == "tend/"+r.Task && r.Work.Chain[0] == "tend/"+parent.ID
		}
	}
	if len(started) != 3 || !started[a.ID] || !started[b.ID] || !started[c.ID] {
		t.Fatalf("each works on its own branch cut from its parent's: %v", started)
	}
	first := func(id string) *task.Run { return stageRuns(st, id)[0] }
	if ra, rc := first(a.ID), first(c.ID); ra.StartedAt == nil || rc.StartedAt == nil || !rc.StartedAt.Before(*ra.EndedAt) || !ra.StartedAt.Before(*rc.EndedAt) {
		t.Fatal("siblings in one checkout run at the same time")
	}
	if sit := st.Situation(st.Tasks[parent.ID]); sit.Reason != task.WhyChildren {
		t.Fatalf("the parent waits for the one in conflict: %+v", sit)
	}
	if err := e.call(MTaskStatus, task.TaskStatus{ID: conflicted.ID, Status: task.StatusDone}, nil); err != nil {
		t.Fatalf("marking it done tries the merge again: %v", err)
	}
	e.until("still in conflict", func(s *task.State) bool {
		return s.Situation(s.Tasks[conflicted.ID]).Reason == task.WhyMergeConflict && s.OpenRun(conflicted.ID) == nil
	})

	integration := filepath.Join(dir+"-wt", parent.ID)
	c.Branch = "tend/" + conflicted.ID
	exec.Command("git", "-C", integration, "merge", "--no-edit", c.Branch).Run()
	gitDo(t, integration, "checkout", "--theirs", "shared.txt")
	gitDo(t, integration, "commit", "--quiet", "-am", "take "+c.Branch)
	e.must(MTaskMerge, TaskRef{ID: conflicted.ID}, nil)
	st = e.until("resolved by hand, it merges", status(conflicted.ID, task.StatusDone))
	if sit := st.Situation(st.Tasks[parent.ID]); sit.Reason != task.WhyAccept {
		t.Fatalf("the parent waits to be accepted on its branch: %+v", sit)
	}
	if err := e.call(MTaskMerge, TaskRef{ID: b.ID}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("a merged task has nothing to merge: %v", err)
	}
	e.must(MTaskStatus, task.TaskStatus{ID: parent.ID, Status: task.StatusDone}, nil)
	st = e.until("the parent is done", status(parent.ID, task.StatusDone))
	log := gitDo(t, integration, "log", "--format=%s")
	for _, x := range []*task.Task{a, b, c} {
		if !strings.Contains(log, "tend/"+x.ID) {
			t.Fatalf("its branch holds every subtask:\n%s", log)
		}
	}
	if gitDo(t, dir, "branch", "--show-current") != "main" || gitDo(t, dir, "status", "--porcelain") != "" {
		t.Fatal("the checkout is left alone")
	}
	if st.Tasks[parent.ID].Head != gitDo(t, dir, "rev-parse", "tend/"+parent.ID) {
		t.Fatalf("the parent's head is known: %s", st.Tasks[parent.ID].Head)
	}
}

func TestAReviewLooksAtACopyOfTheBranch(t *testing.T) {
	dir := checkout(t)
	e := newEnv(t, tend.Config{Agents: []tend.AgentProfile{writing("coder", "code.txt"),
		{Name: "reviewer", Provider: agent.ProviderFake, Args: []string{"--steps", "1", "--every", "50ms", "--verdicts", "pass", "--write", "scratch.txt"}}}})
	e.start()
	e.must(MProjectCreate, ProjectCreate{ID: "p1", Name: "One"}, nil)
	e.must(MProjectEdit, task.ProjectEdit{ID: "p1", Repos: &[]task.Repo{{Name: "app", Base: "main", Dirs: map[string]string{Local: dir}, Worktrees: true}},
		Defaults: &task.Defaults{Workflow: "feature", Roles: map[string]string{"implement": "coder", "review": "reviewer"}}}, nil)
	x := e.create(TaskCreate{Title: "code", Project: "p1"})
	e.must(MTaskStart, TaskRef{ID: x.ID}, nil)
	st := e.until("reviewed, it waits at the gate", func(s *task.State) bool { return s.Situation(s.Tasks[x.ID]).Reason == task.WhyAccept })
	runs := stageRuns(st, x.ID)
	impl, review := runs[0], runs[1]
	if impl.Work.ReadOnly || !review.Work.ReadOnly || review.Worked == nil || review.Worked.Head != impl.Worked.Head ||
		review.Worked.Discarded != 1 || st.Tasks[x.ID].Head != impl.Worked.Head || impl.Worked.Commits != 1 {
		t.Fatalf("the review saw what was implemented and its own change was thrown away: %+v %+v", impl.Worked, review.Worked)
	}
	e.must(MTaskGate, TaskGate{ID: x.ID, Pass: true}, nil)
	st = e.until("done", status(x.ID, task.StatusDone))
	if st.Tasks[x.ID].Merged || st.Tasks[x.ID].Branch != "tend/"+x.ID {
		t.Fatalf("a task without a parent keeps its branch for someone to merge: %+v", st.Tasks[x.ID])
	}
}

func TestAReviewerOnACopyMayBuildButNeverEdits(t *testing.T) {
	for _, c := range []struct {
		provider, permission string
		copy                 bool
		want                 string
		deny                 bool
	}{
		{tend.ProviderCodex, "danger-full-access", true, "workspace-write", false},
		{tend.ProviderCodex, "danger-full-access", false, "read-only", false},
		{tend.ProviderCodex, "", true, "workspace-write", false},
		{tend.ProviderClaude, "acceptEdits", true, "acceptEdits", true},
	} {
		p := tend.AgentProfile{Provider: c.provider, Permission: c.permission}
		reviewer(&p, c.copy)
		if p.Permission != c.want || (c.deny && !slices.Contains(p.Deny, "Edit")) {
			t.Errorf("%+v: %+v", c, p)
		}
	}
}
