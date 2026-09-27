package coord

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

func asker(name, final string) tend.AgentProfile {
	return tend.AgentProfile{Name: name, Provider: agent.ProviderFake, Args: []string{"--steps", "1", "--every", "20ms", "--final", final}}
}

func TestAPreviewSaysWhyARunWouldNotStart(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.probe = func(p string) agent.Check {
		if p == tend.ProviderCodex {
			return agent.Check{Installed: true, Version: "codex-cli 1", Auth: agent.AuthMissing}
		}
		return agent.Check{Installed: true, Version: "2.1", Auth: agent.AuthOK}
	}
	e.start()
	tk := e.task("x", "")
	var pv Preview
	e.must(MRunPreview, Dispatch{Task: tk.ID, Agent: "codex"}, &pv)
	if len(pv.Blockers) != 1 || pv.Blockers[0].Code != WhyAuthMissing || pv.Check == nil || pv.Machine != Local || pv.State != MachineConnected {
		t.Fatalf("%+v", pv)
	}
	pv = Preview{}
	e.must(MRunPreview, Dispatch{Task: tk.ID, Agent: "claude", Runner: node.RunnerBackground}, &pv)
	if len(pv.Blockers) != 0 || pv.Check.Version != "2.1" {
		t.Fatalf("%+v", pv)
	}
	if err := e.call(MRunPreview, Dispatch{Task: tk.ID, Agent: "nope"}, &pv); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("what dispatch refuses, a preview refuses: %v", err)
	}
	var ms Machines
	e.must(MMachineList, MachinesParams{}, &ms)
	if ms.Machines[0].Agents[tend.ProviderCodex].Auth != agent.AuthMissing {
		t.Fatalf("machine.list keeps the checks: %+v", ms.Machines[0])
	}
	r := e.dispatch(Dispatch{Task: tk.ID, Agent: "codex"})
	if end := e.wait(r.ID, ended); end.State != task.Failed || end.Reason != agent.ReasonAuthMissing {
		t.Fatalf("the node refuses it anyway: %+v", end)
	}
}

func TestAReplyContinuesAWaitingRunInItsSession(t *testing.T) {
	e := newEnv(t, tend.Config{Agents: []tend.AgentProfile{asker("asker", "ASK: Which branch?")}})
	e.start()
	tk := e.task("pick a branch", "asker")
	first := e.wait(e.dispatch(Dispatch{Task: tk.ID}).ID, ended)
	if !first.Waiting() || first.Ask != "Which branch?" || first.Attention != task.AttentionAsked {
		t.Fatalf("%+v", first)
	}
	var next task.Run
	e.must(MRunContinue, Continue{Run: first.ID, Text: "main, please"}, &next)
	if next.Resume != first.Session || next.Parent != first.ID || next.Task != tk.ID || next.Brief != "main, please" {
		t.Fatalf("%+v", next)
	}
	end := e.wait(next.ID, ended)
	if end.Session != first.Session || end.State != task.Exited {
		t.Fatalf("%+v", end)
	}
	b, _ := os.ReadFile(capture.TranscriptPath("claude", first.Session))
	if !strings.Contains(string(b), "main, please") {
		t.Fatalf("the reply is in the same session: %.300s", b)
	}
	if err := e.call(MRunContinue, Continue{Run: first.ID}, nil); wire.Code(err) != wire.CodeBadRequest {
		t.Fatalf("a reply says something: %v", err)
	}
}

func TestAnyIndexedSessionCanBeContinued(t *testing.T) {
	e := newEnv(t, tend.Config{Agents: []tend.AgentProfile{asker("asker", "done")}})
	e.start()
	dir := t.TempDir()
	first := e.wait(e.dispatch(Dispatch{Task: e.taskIn("seed", "asker", dir).ID}).ID, ended)
	var r task.Run
	e.must(MRunContinue, Continue{Provider: "claude", Session: first.Session, Dir: dir, Agent: "asker", Text: "one more thing\nand details"}, &r)
	st := e.c.State()
	if tk := st.Tasks[r.Task]; tk == nil || tk.Title != "one more thing" || r.Resume != first.Session {
		t.Fatalf("a new task holds it: %+v %+v", tk, r)
	}
	if end := e.wait(r.ID, ended); end.Session != first.Session {
		t.Fatalf("%+v", end)
	}
}

func TestTheNotifyCommandHearsWhenARunWantsSomeone(t *testing.T) {
	out := filepath.Join(t.TempDir(), "events")
	failer := asker("failer", "x")
	failer.Args = append(failer.Args, "--exit", "2", "--stderr", "Credit balance is too low")
	e := newEnv(t, tend.Config{NotifyCommand: []string{os.Args[0], "_notify", out}, Agents: []tend.AgentProfile{asker("asker", "ASK: Ship it?"), failer}})
	e.start()
	e.wait(e.dispatch(Dispatch{Task: e.task("a", "asker").ID}).ID, ended)
	e.wait(e.dispatch(Dispatch{Task: e.task("b", "failer").ID}).ID, ended)
	e.wait(e.dispatch(Dispatch{Task: e.task("c", "quick").ID}).ID, ended)
	var got []NotifyEvent
	for deadline := time.Now().Add(10 * time.Second); len(got) < 2 && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		got = nil
		b, _ := os.ReadFile(out)
		for l := range strings.Lines(string(b)) {
			var ev NotifyEvent
			if json.Unmarshal([]byte(l), &ev) == nil {
				got = append(got, ev)
			}
		}
	}
	time.Sleep(300 * time.Millisecond)
	b, _ := os.ReadFile(out)
	if n := strings.Count(string(b), "\n"); len(got) != 2 || n != 2 {
		t.Fatalf("one event each for the waiting and the failed run, none for the one that went well: %s", b)
	}
	byEvent := map[string]NotifyEvent{}
	for _, ev := range got {
		byEvent[ev.Event] = ev
	}
	if w := byEvent[NotifyWaiting]; w.Ask != "Ship it?" || w.Agent != "asker" {
		t.Fatalf("%+v", got)
	}
	if f := byEvent[NotifyFailed]; f.Reason != agent.ReasonQuota || f.ExitCode == nil || *f.ExitCode != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestNotifyEvents(t *testing.T) {
	two := 2
	for _, c := range []struct {
		name     string
		was, now task.Run
		want     string
	}{
		{"failed", task.Run{State: task.Running}, task.Run{State: task.Failed}, NotifyFailed},
		{"exit code", task.Run{State: task.Running}, task.Run{State: task.Exited, ExitCode: &two}, NotifyFailed},
		{"waiting", task.Run{State: task.Running}, task.Run{State: task.Exited, Attention: task.AttentionPermission}, NotifyWaiting},
		{"asked while running", task.Run{State: task.Running}, task.Run{State: task.Running, Attention: task.AttentionAsked}, NotifyAsked},
		{"asked, then ends waiting", task.Run{State: task.Running, Attention: task.AttentionAsked}, task.Run{State: task.Exited, Attention: task.AttentionAsked}, NotifyWaiting},
		{"stalled", task.Run{State: task.Running}, task.Run{State: task.Running, Attention: task.AttentionStalled}, NotifyStalled},
		{"still stalled", task.Run{State: task.Running, Attention: task.AttentionStalled}, task.Run{State: task.Running, Attention: task.AttentionStalled}, ""},
		{"stopped", task.Run{State: task.Running}, task.Run{State: task.Stopped}, ""},
		{"exited well", task.Run{State: task.Running}, task.Run{State: task.Exited}, ""},
	} {
		if got := notifyEvent(&c.was, &c.now); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestARunTheNodeHoldsBackWaitsInsteadOfFailing(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	dir := t.TempDir()
	n := node.New(e.home)
	other, err := n.Start(node.StartParams{Run: node.NewRunID(), Task: "t_x", Coordinator: "someone-else", Dir: dir, Runner: node.RunnerBackground,
		Profile: tend.AgentProfile{Name: "fake", Provider: agent.ProviderFake, Args: []string{"--steps", "2", "--every", "700ms"}}})
	if err != nil {
		t.Fatal(err)
	}
	r := e.dispatch(Dispatch{Task: e.taskIn("mine", "quick", dir).ID})
	time.Sleep(time.Second)
	if got := e.c.State().Runs[r.ID]; got.State != task.Starting {
		t.Fatalf("the node holds it back while %s runs there: %+v", other.Run, got)
	}
	if end := e.wait(r.ID, ended); end.State != task.Exited {
		t.Fatalf("it runs once the directory is free: %+v", end)
	}
}

func TestAPermissionIsAnsweredThroughTheCoordinator(t *testing.T) {
	e := newEnv(t, tend.Config{Agents: []tend.AgentProfile{{Name: "gated", Provider: agent.ProviderFake,
		Args: []string{"--steps", "2", "--every", "20ms", "--permission", "Bash:make deploy"}}}})
	e.start()
	tk := e.task("deploy", "gated")
	r := e.dispatch(Dispatch{Task: tk.ID})
	w := e.wait(r.ID, func(r *task.Run) bool { return len(r.Requests) == 1 })
	if !w.Stream || w.Requests[0].Summary != "make deploy" || w.Attention != task.AttentionPermission || !w.NeedsYou() {
		t.Fatalf("%+v", w)
	}
	if err := e.call(MRunAnswer, Answer{Run: r.ID, Answer: agent.Answer{Request: "nope", Allow: true}}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("a request it does not wait on: %v", err)
	}
	e.must(MRunAnswer, Answer{Run: r.ID, Answer: agent.Answer{Request: w.Requests[0].ID, Allow: true}}, nil)
	end := e.wait(r.ID, ended)
	if end.State != task.Exited || len(end.Requests) != 0 || len(end.Answers) != 0 || end.Attention != "" {
		t.Fatalf("%+v", end)
	}
}

func TestAMessageGoesToARunningRunAndAQuestionNeedsItsAnswers(t *testing.T) {
	e := newEnv(t, tend.Config{Agents: []tend.AgentProfile{{Name: "chatty", Provider: agent.ProviderFake,
		Args: []string{"--steps", "8", "--every", "150ms", "--question", "Which DB?|pg|mysql"}}}})
	e.start()
	tk := e.task("pick", "chatty")
	r := e.dispatch(Dispatch{Task: tk.ID})
	w := e.wait(r.ID, func(r *task.Run) bool { return len(r.Requests) == 1 })
	if err := e.call(MRunAnswer, Answer{Run: r.ID, Answer: agent.Answer{Request: w.Requests[0].ID, Allow: true}}, nil); wire.Code(err) != wire.CodeBadRequest {
		t.Fatalf("an answer answers every question: %v", err)
	}
	e.must(MRunAnswer, Answer{Run: r.ID, Answer: agent.Answer{Request: w.Requests[0].ID, Allow: true, Answers: map[string]string{"Which DB?": "pg"}}}, nil)
	e.wait(r.ID, func(r *task.Run) bool { return len(r.Requests) == 0 })
	var got task.Run
	e.must(MRunSend, SendMessage{Run: r.ID, Text: "use tabs"}, &got)
	if len(got.Sends) != 1 || got.Sends[0].State != agent.SendQueued {
		t.Fatalf("%+v", got.Sends)
	}
	end := e.wait(r.ID, ended)
	if len(end.Sends) != 1 || end.Sends[0].State != agent.SendSent {
		t.Fatalf("%+v", end.Sends)
	}
	if err := e.call(MRunSend, SendMessage{Run: r.ID, Text: "late"}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("an ended run takes no message: %v", err)
	}
}
