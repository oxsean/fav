package workflow

import (
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/task"
)

func TestTheBuiltinsAndACustomWorkflowRead(t *testing.T) {
	b := Builtins()
	if f := b["feature"]; len(f.Stages) != 3 || f.Stages[1].Output != task.OutputVerdict || f.Stages[1].OnRework != "implement" ||
		f.Stages[2].Gate != task.GateHuman || f.MaxLoops != 2 || !f.Stages[0].Check {
		t.Fatalf("%+v", f)
	}
	for _, name := range []string{"feature", "fix", "docs"} {
		if f := b[name]; f.Back("accept") != "implement" {
			t.Errorf("%s: a gate sends the work back to %q, not to who implements it", name, f.Back("accept"))
		}
	}
	if b["fix"].Stages[1].Role != "test" || len(b["docs"].Stages) != 2 {
		t.Fatalf("%+v", b)
	}
	custom := "---\nname: tiny\nstages:\n  - {name: build, role: implement}\n  - {name: check, role: review, output: verdict}\nmax_loops: 0\n---\n## build\nDo {{task.title}}.\n{{#rework}}Fix: {{rework.notes}}{{/rework}}\n"
	f, err := Resolve("tiny", map[string]string{"tiny": custom})
	if err != nil || f.MaxLoops != 0 || f.Stages[0].Prompt == "" || f.Back("check") != "build" {
		t.Fatalf("%+v %v", f, err)
	}
	for _, bad := range []string{"---\nname: x\nstages: []\n---\n", "---\nname: x\nstages:\n  - {name: a, role: boss}\n---\n",
		"---\nname: x\nstages:\n  - {name: a, role: review, on_rework: z}\n---\n", "---\nname: x\nstages:\n  - {name: a}\n---\n"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := Resolve("tiny", map[string]string{"tiny": strings.Replace(custom, "name: tiny", "name: other", 1)}); err == nil {
		t.Fatal("a workflow is named what it says it is")
	}
}

func TestAStageBriefCarriesTheReworkAndTheWorkpad(t *testing.T) {
	f, _ := Resolve("feature", nil)
	st := task.New()
	at := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	x := &task.Task{ID: "t_1", Title: "Export", Brief: "Export CSV.", Accept: []string{"rows are quoted"}, Flow: &f, Stage: "implement"}
	st.Tasks[x.ID] = x
	b := Brief(st, x)
	if !strings.Contains(b, "Export CSV.") || !strings.Contains(b, "- rows are quoted") || strings.Contains(b, "sent back") || strings.Contains(b, "Workpad") {
		t.Fatalf("a first round: %q", b)
	}
	code := 0
	st.Runs["r_1"] = &task.Run{ID: "r_1", Task: x.ID, Stage: "review", State: task.Exited, ExitCode: &code, QueuedAt: at,
		Verdict: &agent.Verdict{Verdict: agent.VerdictRework, Summary: "quote the commas"}}
	x.Notes = append(x.Notes, task.Note{At: at.Add(time.Minute), Stage: "review", Kind: task.NoteRework, Text: "quote the commas"})
	x.Loops = 1
	b = Brief(st, x)
	if !strings.Contains(b, "round 2") || !strings.Contains(b, "Address this") || !strings.Contains(b, "verdict rework: quote the commas") {
		t.Fatalf("a rework round: %q", b)
	}
	x.Stage = "review"
	if b = Brief(st, x); !strings.Contains(b, "do not change any file") || !strings.Contains(b, "Workpad") {
		t.Fatalf("the reviewer: %q", b)
	}
}
