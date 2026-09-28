package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/task"
)

// finishEdit is the editor closing with text in its file.
func finishEdit(t *testing.T, m *Model, text string) {
	t.Helper()
	if m.tasks.editing == nil {
		t.Fatal("no editor is open")
	}
	if err := os.WriteFile(m.tasks.editing.path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	_, cmd := m.Update(editedMsg{})
	pump(m, cmd)
}

func editorText(t *testing.T, m *Model) string {
	t.Helper()
	if m.tasks.editing == nil {
		t.Fatal("no editor is open")
	}
	b, err := os.ReadFile(m.tasks.editing.path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestADraftIsReviewedEditedAndApplied(t *testing.T) {
	t.Setenv("VISUAL", "true")
	m, _ := tasksModel(t)
	key(m, "5")
	var tk task.Task
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "big", Dir: t.TempDir()}, &tk); err != nil {
		t.Fatal(err)
	}
	plan := &task.Plan{Tasks: []task.PlanTask{{Key: "a", Title: "design it"}, {Key: "b", Title: "build it", After: []string{"a"}}},
		Questions: []string{"Postgres or SQLite?"}}
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskPlanSave, "c2", coord.PlanSave{ID: tk.ID, Plan: plan}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 && hasDraft(m.tasks.list[0]) })

	key(m, "enter")
	if s := screenText(m); !strings.Contains(s, "草稿（2）") && !strings.Contains(s, "Draft (2)") {
		t.Fatalf("the task dialog offers its draft:\n%s", s)
	}
	key(m, "enter")
	if m.ov.kind != ovTaskDraft {
		t.Fatalf("Enter opens the draft: %v", m.ov.kind)
	}
	s := screenText(m)
	for _, want := range []string{"design it", "build it", "Postgres or SQLite?"} {
		if !strings.Contains(s, want) {
			t.Fatalf("the draft lacks %q:\n%s", want, s)
		}
	}
	for i, l := range strings.Split(m.screen(), "\n") {
		if w := ansi.StringWidth(l); w != m.w {
			t.Fatalf("line %d is %d wide, not %d", i, w, m.w)
		}
	}

	key(m, "e")
	if !strings.Contains(editorText(t, m), `"design it"`) {
		t.Fatalf("the editor has the draft as JSON:\n%s", editorText(t, m))
	}
	finishEdit(t, m, "{bad")
	if string(m.tasks.unsaved["plan:"+tk.ID]) != "{bad" || !strings.Contains(m.notice, "{") && m.notice == "" {
		t.Fatalf("a bad edit is kept and said: %q %q", m.tasks.unsaved, m.notice)
	}
	key(m, "e")
	if editorText(t, m) != "{bad" {
		t.Fatalf("the next edit goes on from it: %q", editorText(t, m))
	}
	finishEdit(t, m, `{"tasks":[{"key":"a","title":"design it well"},{"key":"b","title":"build it","after":["a"]}]}`)
	waitFor(t, m, func() bool {
		x := m.tasks.st.Tasks[tk.ID]
		return hasDraft(x) && x.Draft.Plan.Tasks[0].Title == "design it well"
	})
	if _, left := m.tasks.unsaved["plan:"+tk.ID]; left {
		t.Fatal("a saved edit leaves nothing unsaved")
	}

	if m.ov.kind != ovTaskDraft {
		t.Fatalf("the draft stays open after an edit: %v", m.ov.kind)
	}
	key(m, "enter")
	waitFor(t, m, func() bool { return len(m.tasks.st.Children(tk.ID)) == 2 })
	for _, k := range m.tasks.st.Children(tk.ID) {
		if k.Status != task.StatusBacklog {
			t.Fatalf("applied subtasks wait to be started: %+v", k)
		}
	}
}

func TestADraftIsDiscardedAfterAConfirm(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	var tk task.Task
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "big", Dir: t.TempDir()}, &tk); err != nil {
		t.Fatal(err)
	}
	plan := &task.Plan{Tasks: []task.PlanTask{{Key: "a", Title: "design it"}}}
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskPlanSave, "c2", coord.PlanSave{ID: tk.ID, Plan: plan}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 && hasDraft(m.tasks.list[0]) })
	m.openDraft()
	m.screen()
	for _, b := range m.ov.btns {
		if b.label == "丢弃" || b.label == "Discard" {
			b.act(m)
		}
	}
	if m.ov.kind != ovConfirm {
		t.Fatalf("discarding asks first: %v", m.ov.kind)
	}
	key(m, "esc")
	if m.ov.kind != ovTaskDraft {
		t.Fatalf("backing out returns to the draft: %v", m.ov.kind)
	}
	m.screen()
	for _, b := range m.ov.btns {
		if b.label == "丢弃" || b.label == "Discard" {
			b.act(m)
		}
	}
	key(m, "y")
	waitFor(t, m, func() bool { return m.tasks.st.Tasks[tk.ID].Draft == nil })
}

func TestATasksProjectIsEditedAsJSON(t *testing.T) {
	t.Setenv("VISUAL", "true")
	m, _ := tasksModel(t)
	key(m, "5")
	var pr task.Project
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MProjectCreate, "p1", coord.ProjectCreate{Name: "shop"}, &pr); err != nil {
		t.Fatal(err)
	}
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "one", Dir: t.TempDir(), Project: pr.ID}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 && m.taskProject() != nil })
	key(m, "enter")
	if s := screenText(m); !strings.Contains(s, "项目设置") && !strings.Contains(s, "Project settings") {
		t.Fatalf("the task dialog offers its project's settings:\n%s", s)
	}
	pump(m, m.editProject())
	text := editorText(t, m)
	if !strings.Contains(text, `"name": "shop"`) || !strings.Contains(text, `"context": ""`) {
		t.Fatalf("the editor has the project:\n%s", text)
	}
	finishEdit(t, m, strings.Replace(text, `"context": ""`, `"context": "ship on Fridays"`, 1))
	waitFor(t, m, func() bool { return m.tasks.st.Projects[pr.ID].Context == "ship on Fridays" })
	if p := m.tasks.st.Projects[pr.ID]; p.Name != "shop" {
		t.Fatalf("only what changed is sent: %+v", p)
	}

	pump(m, m.editProject())
	finishEdit(t, m, `{"name": "shop", "nonsense": 1}`)
	if _, kept := m.tasks.unsaved["project:"+pr.ID]; !kept {
		t.Fatal("an unknown field is refused and the text kept")
	}
}

func TestAgentDefinitionsAreMadeAndEditedFromTheRunDialog(t *testing.T) {
	t.Setenv("VISUAL", "true")
	m, _ := tasksModel(t)
	key(m, "5")
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "one", Dir: t.TempDir()}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 })
	m.openRunDialog()
	s := screenText(m)
	if !strings.Contains(s, "新建 agent") && !strings.Contains(s, "New agent") {
		t.Fatalf("the run dialog offers a new agent:\n%s", s)
	}

	pump(m, m.newAgent())
	finishEdit(t, m, strings.Replace(agentTemplate, "role: implement", "role: nonsense", 1))
	if _, kept := m.tasks.unsaved["agent:"]; !kept {
		t.Fatalf("a definition that fails its checks is kept: %q", m.notice)
	}
	pump(m, m.newAgent())
	finishEdit(t, m, strings.Replace(agentTemplate, "my-agent", "tester", 1))
	var list coord.AgentDefList
	waitFor(t, m, func() bool {
		m.tasks.cl.Call(t.Context(), coord.MAgentDefList, struct{}{}, &list)
		return len(list.Defs) == 1 && list.Defs[0].Name == "tester"
	})

	pump(m, m.editAgent("tester"))
	if !strings.Contains(editorText(t, m), "name: tester") {
		t.Fatalf("the editor has the definition:\n%s", editorText(t, m))
	}
	finishEdit(t, m, editorText(t, m))
	if m.notice != "没有改动" && m.notice != "Nothing changed" {
		t.Fatalf("an untouched definition is not sent: %q", m.notice)
	}

	pump(m, m.editAgent("fake"))
	if m.tasks.editing != nil || !strings.Contains(m.notice, "config.json") {
		t.Fatalf("a configured profile is not a definition: %q", m.notice)
	}
}
