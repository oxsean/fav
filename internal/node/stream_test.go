package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/proc"
	"github.com/oxsean/fav/internal/wire"
)

func logOf(t *testing.T, n *Node, run string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(n.runDir(run), "output.log"))
	return string(b)
}

func waiting(t *testing.T, n *Node, run string) agent.Request {
	t.Helper()
	s := wait(t, n, run, func(s Snapshot) bool { return len(s.Requests) == 1 })
	return s.Requests[0]
}

func TestAPermissionPromptWaitsForItsAnswer(t *testing.T) {
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "2", "--every", "20ms", "--permission", "Bash:rm -rf build"), Brief: "b"})
	req := waiting(t, n, s.Run)
	if req.Kind != agent.RequestPermission || req.Tool != "Bash" || req.Summary != "rm -rf build" {
		t.Fatalf("%+v", req)
	}
	if got, _ := n.Snapshot(s.Run); got.Attention != AttentionPermission || got.State.State != StateRunning || !got.Stream {
		t.Fatalf("a waiting prompt marks the run: %+v", got)
	}
	if _, err := n.Answer(AnswerParams{Run: s.Run, Answer: agent.Answer{Request: req.ID, Allow: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Answer(AnswerParams{Run: s.Run, Answer: agent.Answer{Request: req.ID, Allow: true}}); err != nil {
		t.Fatalf("answering again is a no-op: %v", err)
	}
	end := wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if end.State.State != StateExited || end.Attention != "" || len(end.Requests) != 0 || !strings.Contains(logOf(t, n, s.Run), "allowed Bash") {
		t.Fatalf("%+v", end)
	}
}

func TestAToolTheUserDeniedIsNotAWaitingPermission(t *testing.T) {
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "20ms", "--permission", "Write:notes.md"), Brief: "b"})
	req := waiting(t, n, s.Run)
	if _, err := n.Answer(AnswerParams{Run: s.Run, Answer: agent.Answer{Request: req.ID, Message: "not now"}}); err != nil {
		t.Fatal(err)
	}
	end := wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if end.Attention != "" || end.Reason != "" || !strings.Contains(logOf(t, n, s.Run), "denied Write") {
		t.Fatalf("%+v", end)
	}
	if _, err := n.Answer(AnswerParams{Run: s.Run, Answer: agent.Answer{Request: "nope", Allow: true}}); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("a request it no longer waits on: %v", err)
	}
}

func TestAQuestionIsAnsweredWithAnOption(t *testing.T) {
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "20ms", "--question", "Which DB?|pg|mysql"), Brief: "b"})
	req := waiting(t, n, s.Run)
	if req.Kind != agent.RequestQuestion || len(req.Questions) != 1 || strings.Join(req.Questions[0].Options, ",") != "pg,mysql" {
		t.Fatalf("%+v", req)
	}
	if got, _ := n.Snapshot(s.Run); got.Attention != AttentionAsked || got.Ask != "Which DB?" {
		t.Fatalf("%+v", got)
	}
	if _, err := n.Answer(AnswerParams{Run: s.Run, Answer: agent.Answer{Request: req.ID, Allow: true, Answers: map[string]string{"Which DB?": "pg"}}}); err != nil {
		t.Fatal(err)
	}
	end := wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if end.Attention != "" || end.Ask != "" || !strings.Contains(logOf(t, n, s.Run), "answer: pg") {
		t.Fatalf("%+v\n%s", end, logOf(t, n, s.Run))
	}
}

func TestAMessageReachesARunningAgent(t *testing.T) {
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "6", "--every", "150ms"), Brief: "b"})
	wait(t, n, s.Run, func(s Snapshot) bool { return s.State.State == StateRunning })
	got, err := n.Send(SendParams{Run: s.Run, Send: agent.Send{ID: "m1", Text: "use tabs"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sends) != 1 || got.Sends[0].State != agent.SendQueued {
		t.Fatalf("%+v", got.Sends)
	}
	if _, err := n.Send(SendParams{Run: s.Run, Send: agent.Send{ID: "m1", Text: "use tabs"}}); err != nil {
		t.Fatalf("the same message again is a no-op: %v", err)
	}
	end := wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if len(end.Sends) != 1 || end.Sends[0].State != agent.SendSent || !strings.Contains(logOf(t, n, s.Run), "heard: use tabs") {
		t.Fatalf("%+v\n%s", end.Sends, logOf(t, n, s.Run))
	}
	if _, err := n.Send(SendParams{Run: s.Run, Send: agent.Send{ID: "m2", Text: "late"}}); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("an ended run takes no message: %v", err)
	}
}

func TestAStreamRunsLogLeavesOutTheAccount(t *testing.T) {
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "10ms", "--permission", "Bash:ls"), Brief: "b"})
	req := waiting(t, n, s.Run)
	if _, err := n.Answer(AnswerParams{Run: s.Run, Answer: agent.Answer{Request: req.ID, Allow: true}}); err != nil {
		t.Fatal(err)
	}
	wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if log := logOf(t, n, s.Run); strings.Contains(log, "fake@example.com") || !strings.Contains(log, `"type":"result"`) {
		t.Fatalf("output.log:\n%s", log)
	}
}

func TestAStopInterruptsAStreamRunGently(t *testing.T) {
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "100", "--every", "100ms"), Brief: "b"})
	wait(t, n, s.Run, func(s Snapshot) bool { return s.State.State == StateRunning })
	began := time.Now()
	if _, err := n.Stop(RunRef{Run: s.Run}); err != nil {
		t.Fatal(err)
	}
	end := wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if end.State.State != StateStopped || time.Since(began) > interruptGrace {
		t.Fatalf("%+v after %v", end, time.Since(began))
	}
}

func TestMessagesARunNeverTookFailOnceItEnded(t *testing.T) {
	dir := t.TempDir()
	appendLine(filepath.Join(dir, inboxFile), agent.Send{ID: "m1", Text: "x"})
	appendLine(filepath.Join(dir, inboxFile), agent.Send{ID: "m2", Text: "y"})
	st := State{State: StateRunning, Sends: []agent.Send{{ID: "m1", Text: "x", State: agent.SendSent}}}
	if got := withQueued(dir, st); len(got) != 2 || got[1].State != agent.SendQueued {
		t.Fatalf("%+v", got)
	}
	st.State = StateExited
	if got := withQueued(dir, st); got[1].State != agent.SendFailed {
		t.Fatalf("%+v", got)
	}
}

func TestAnAgentFloodingItsOutputWhileAnswersPileUpDoesNotDeadlock(t *testing.T) {
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "1ms", "--flood", "1000"), Brief: "b"})
	t.Cleanup(func() {
		if got, err := n.Snapshot(s.Run); err == nil && !Terminal(got.State.State) {
			proc.KillTree(got.Pid)
			proc.KillPID(got.Sup)
		}
	})
	end := wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if end.State.State != StateExited || !strings.Contains(logOf(t, n, s.Run), `"type":"result"`) {
		t.Fatalf("%+v", end)
	}
}
