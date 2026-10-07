package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// linkedState: sessions s-old and s-new were both used by runs, s-new last by tk-new's; tk-many ran twice on s-many;
// tk-none's run has no session yet.
func linkedState(newTitle, newSession string) *task.State {
	st := task.New()
	at := time.Now().Add(-time.Hour)
	add := func(x *task.Task) { st.Tasks[x.ID] = x }
	run := func(id, tk, session string, seq int64) {
		st.Runs[id] = &task.Run{ID: id, Task: tk, Session: session, Seq: seq, QueuedAt: at.Add(time.Duration(seq) * time.Minute), State: task.Exited}
	}
	add(&task.Task{ID: "tk-old", Title: "Old attempt", Status: task.StatusTodo})
	add(&task.Task{ID: "tk-new", Title: newTitle, Stage: "review", Status: task.StatusTodo})
	add(&task.Task{ID: "tk-many", Title: "Many runs", Status: task.StatusDone})
	add(&task.Task{ID: "tk-none", Title: "No session", Status: task.StatusTodo})
	run("r1", "tk-old", newSession, 1)
	run("r2", "tk-new", newSession, 5)
	run("r3", "tk-old", "", 6)
	run("r4", "tk-many", "s-many", 2)
	run("r5", "tk-many", "s-many", 3)
	run("r6", "tk-none", "", 4)
	return st
}

func TestASessionLinksToTheTaskOfItsNewestRun(t *testing.T) {
	links := task.SessionTasks(linkedState("New attempt", "s-new"))
	if x := links["s-new"]; x == nil || x.ID != "tk-new" {
		t.Errorf("two tasks' runs on one session: the newest run's task, got %v", x)
	}
	if x := links["s-many"]; x == nil || x.ID != "tk-many" {
		t.Errorf("several runs of one task on one session: that task, got %v", x)
	}
	if _, ok := links[""]; ok {
		t.Error("a run without a session links nothing")
	}
	if x := links["s-elsewhere"]; x != nil {
		t.Errorf("a session no run used has no task, got %v", x)
	}
	if len(links) != 2 {
		t.Errorf("links %v", links)
	}
}

func TestTaskLabelIsSanitizedOnOneLine(t *testing.T) {
	x := &task.Task{ID: "tk-new", Title: "Ship \x1b[31mred\x1b[0m\nnow\a", Stage: "review"}
	got := taskLabel(x)
	if got != "tk-new Ship red now · review" {
		t.Errorf("label %q", got)
	}
	if got := taskLabel(&task.Task{ID: "tk-1", Title: "Plain"}); got != "tk-1 Plain" {
		t.Errorf("no stage: %q", got)
	}
}

// linkedModel: the fixture's WebSocket session was used by tk-new's run; the TUI holds the coordinator's state.
func linkedModel(t *testing.T, title string) (*Model, *tend.Rec) {
	t.Helper()
	m := sized(t, 140, 40)
	var linked *tend.Rec
	for _, r := range m.store.Query(tend.Parse("status:all")) {
		if strings.Contains(r.Title, "WebSocket") {
			linked = r
		}
	}
	m.tasks.st = linkedState(title, linked.SessionID)
	m.refresh()
	return m, linked
}

func TestTheSessionsViewLabelsALinkedSession(t *testing.T) {
	m, linked := linkedModel(t, "Ship \x1b[31mred\x1b[0m\nnow\a")
	for m.current() != linked {
		m.navKey(press("j"))
	}
	lines := viewLines(m)
	screen := strings.Join(lines, "\n")
	if !strings.Contains(ansi.Strip(screen), "tk-new Ship red now · review") {
		t.Errorf("no label in the detail pane:\n%s", ansi.Strip(screen))
	}
	if !strings.Contains(ansi.Strip(screen), i18n.F("card.task", "tk-new Ship")) {
		t.Errorf("no label on the card:\n%s", ansi.Strip(screen))
	}
	if strings.Contains(screen, "\x1b[31mred") || strings.Contains(screen, "\a") {
		t.Error("the title's escapes reached the frame")
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 140 {
			t.Errorf("line %d is %d wide", i, w)
		}
	}

	if row := ansi.Strip(m.recLine(linked, false, 60)); !strings.Contains(row, i18n.F("card.task", "tk-new")) || ansi.StringWidth(row) != 60 {
		t.Errorf("a one-line row names the task: %q", row)
	}

	m.tasks.st = nil
	if strings.Contains(ansi.Strip(m.screen()), "tk-new") {
		t.Error("without the coordinator's state the view shows no label")
	}
}

func TestTheSessionsSearchFindsTheLinkedTask(t *testing.T) {
	m, linked := linkedModel(t, "Refund emails")
	for _, q := range []string{"refund emails", "TK-NEW", "tk-new websocket"} {
		m.search.SetValue(q)
		m.refresh()
		if got := m.list(m.query()); len(got) != 1 || got[0] != linked {
			t.Errorf("%q: got %d sessions", q, len(got))
		}
	}
	m.search.SetValue("tk-new rbac")
	m.refresh()
	if got := m.list(m.query()); len(got) != 0 {
		t.Errorf("every word must match the session or its task: got %d", len(got))
	}
	m.tasks.st = nil
	m.search.SetValue("refund")
	m.refresh()
	if got := m.list(m.query()); len(got) != 0 {
		t.Errorf("without the coordinator's state the task is not searched: got %d", len(got))
	}
}

func TestTheResumeDialogOpensTheLinkedTask(t *testing.T) {
	m, linked := linkedModel(t, "Refund emails")
	m.search.SetValue("refund")
	m.refresh()
	m.openResume(linked)
	m.screen()
	label := keyed(keyOf(inResume, actTask), i18n.F("resume.btn_task", "tk-new"))
	found := false
	for _, b := range m.ov.btns {
		found = found || b.label == label
	}
	if !found {
		t.Fatalf("no %q button", label)
	}
	m.overlayKey(press(keyOf(inResume, actTask)))
	if m.view != viewTasks || m.ov.active() {
		t.Fatalf("view %d, overlay %v", m.view, m.ov.active())
	}
	if x := m.selectedTask(); x == nil || x.ID != "tk-new" {
		t.Fatalf("selected %v", x)
	}

	m.setView(viewFavorites)
	for _, r := range m.store.Query(tend.Parse("status:all")) {
		if r != linked {
			m.openResume(r)
			break
		}
	}
	m.screen()
	for _, b := range m.ov.btns {
		if strings.Contains(b.label, i18n.F("resume.btn_task", "tk-new")) {
			t.Fatal("an unlinked session has no task button")
		}
	}
	m.overlayKey(press(keyOf(inResume, actTask)))
	if m.view != viewFavorites {
		t.Error("its key does nothing there")
	}
}
