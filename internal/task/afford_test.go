package task

import (
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/tend"
)

func TestAConversationIsItsRootAndWhatGoesOnFromIt(t *testing.T) {
	s := New()
	ev := journal.NewEvent
	q := func(id, parent string) journal.Event {
		return ev(ERunQueued, Run{ID: id, Task: "t", Machine: "m", Parent: parent})
	}
	apply(t, s, ev(ETaskCreated, Task{ID: "t", Title: "x", Status: StatusTodo}), q("a", ""))
	apply(t, s, q("b", "a"), q("x", "gone")) // x answers a run the state no longer holds: its own root
	apply(t, s, q("c", "b"), q("d", ""))
	ids := func(rs []*Run) (out []string) {
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return out
	}
	if got := ids(s.Conversation("c")); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("from its last: %v", got)
	}
	if got := ids(s.Conversation("x")); !slices.Equal(got, []string{"x"}) {
		t.Fatalf("an orphan: %v", got)
	}
	if s.Conversation("nope") != nil {
		t.Fatal("no run")
	}
}

func TestPendingIsWhatWaitsOnSomeoneAboutATask(t *testing.T) {
	ev := journal.NewEvent
	zero, one := 0, 1
	fake := tend.AgentProfile{Name: "f", Provider: agent.ProviderFake}
	start := func(s *State, extra ...journal.Event) {
		apply(t, s, append([]journal.Event{ev(ETaskCreated, Task{ID: "t", Title: "x", Status: StatusTodo}),
			ev(ERunQueued, Run{ID: "r", Task: "t", Machine: "m", Profile: fake})}, extra...)...)
	}
	cases := []struct {
		name string
		then []journal.Event
		want []Pending
	}{
		{"a question and a permission, one answered", []journal.Event{
			ev(ERunObserved, Observation{ID: "r", State: Running, NodeRev: 1, Stream: true, Attention: AttentionPermission, Requests: []agent.Request{
				{ID: "p", Kind: agent.RequestPermission}, {ID: "q", Kind: agent.RequestQuestion}, {ID: "done", Kind: agent.RequestPermission}}}),
			ev(ERunAnswered, RunAnswer{ID: "r", Answer: agent.Answer{Request: "done", Allow: true}})},
			[]Pending{{ID: "r/p", Kind: PendPermission, Task: "t", Run: "r", Request: "p", Version: 1},
				{ID: "r/q", Kind: PendQuestion, Task: "t", Run: "r", Request: "q", Version: 1}}},
		{"an answer that did not reach the agent", []journal.Event{
			ev(ERunObserved, Observation{ID: "r", State: Running, NodeRev: 1, Stream: true, Attention: AttentionPermission,
				Requests: []agent.Request{{ID: "p", Kind: agent.RequestPermission, Failed: true}}}),
			ev(ERunAnswered, RunAnswer{ID: "r", Answer: agent.Answer{Request: "p", Allow: true}})},
			[]Pending{{ID: "r/p", Kind: PendPermission, Task: "t", Run: "r", Request: "p", Version: 1}}},
		{"asked in a terminal", []journal.Event{ev(ERunObserved, Observation{ID: "r", State: Running, NodeRev: 1, Attention: AttentionAsked})},
			[]Pending{{ID: "t/question", Kind: PendQuestion, Task: "t", Run: "r", Version: 1}}},
		{"ended on a question", []journal.Event{ev(ERunObserved, Observation{ID: "r", State: Exited, NodeRev: 1, ExitCode: &zero, Attention: AttentionAsked})},
			[]Pending{{ID: "t/continue", Kind: PendContinue, Reason: AttentionAsked, Task: "t", Run: "r", Version: 1}}},
		{"ended well", []journal.Event{ev(ERunObserved, Observation{ID: "r", State: Exited, NodeRev: 1, ExitCode: &zero})},
			[]Pending{{ID: "t/ended", Kind: PendEnded, Task: "t", Run: "r", Version: 1}}},
		{"exited badly", []journal.Event{ev(ERunObserved, Observation{ID: "r", State: Exited, NodeRev: 1, ExitCode: &one})},
			[]Pending{{ID: "t/failed", Kind: PendFailed, Reason: Exited, Task: "t", Run: "r", Version: 1}}},
		{"running", []journal.Event{ev(ERunObserved, Observation{ID: "r", State: Running, NodeRev: 1})}, nil},
	}
	for _, c := range cases {
		s := New()
		start(s, c.then...)
		if got := s.Pending(s.Tasks["t"]); !slices.Equal(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}

	s := New()
	apply(t, s, ev(ETaskCreated, Task{ID: "g", Title: "g", Status: StatusTodo, Workflow: "w", Stage: "accept",
		Flow: &Flow{Name: "w", Stages: []Stage{{Name: "accept", Gate: GateHuman}}}}), ev(ETaskStarted, TaskStart{IDs: []string{"g"}}))
	apply(t, s, ev(ETaskCreated, Task{ID: "d", Title: "d", Status: StatusTodo}))
	if got := s.Pending(s.Tasks["g"]); !slices.Equal(got, []Pending{{ID: "g/gate", Kind: PendGate, Task: "g", Version: 1}}) {
		t.Errorf("a gate: %+v", got)
	}
	if got := s.Pending(s.Tasks["d"]); got != nil {
		t.Errorf("waiting for a dispatch nobody asked for: %+v", got)
	}
}

func TestAMessageGoesWhereTheFirstRuleThatHoldsSays(t *testing.T) {
	ev := journal.NewEvent
	zero := 0
	fake := tend.AgentProfile{Name: "f", Provider: agent.ProviderFake}
	cmd := tend.AgentProfile{Name: "c", Provider: agent.ProviderCommand}
	flow := &Flow{Name: "w", Stages: []Stage{{Name: "build"}, {Name: "review"}}}
	cases := []struct {
		name   string
		task   Task
		events []journal.Event
		want   Route
	}{
		{"steers a running turn", Task{}, []journal.Event{ev(ERunQueued, Run{ID: "r", Task: "t", Profile: fake}),
			ev(ERunObserved, Observation{ID: "r", State: Running, NodeRev: 1, Stream: true, Caps: &agent.RunCaps{Steer: true}})},
			Route{To: RouteRun, Run: "r", Version: 2}},
		{"after a turn that takes no steering", Task{}, []journal.Event{ev(ERunQueued, Run{ID: "r", Task: "t", Profile: fake}),
			ev(ERunObserved, Observation{ID: "r", State: Running, NodeRev: 1, Caps: &agent.RunCaps{After: true}})},
			Route{To: RouteRun, Run: "r", Version: 2}},
		{"a run that takes nothing", Task{}, []journal.Event{ev(ERunQueued, Run{ID: "r", Task: "t", Profile: cmd}),
			ev(ERunObserved, Observation{ID: "r", State: Running, NodeRev: 1, Caps: &agent.RunCaps{}})},
			Route{To: RouteNone, Run: "r", Why: WhyBusy}},
		{"a queued run", Task{}, []journal.Event{ev(ERunQueued, Run{ID: "r", Task: "t", Profile: fake})},
			Route{To: RouteNone, Run: "r", Why: WhyStarting}},
		{"continues an ended session", Task{}, []journal.Event{ev(ERunQueued, Run{ID: "r", Task: "t", Profile: fake}),
			ev(ERunObserved, Observation{ID: "r", State: Exited, NodeRev: 1, ExitCode: &zero, Session: "s", Provider: agent.ProviderFake})},
			Route{To: RouteReply, Run: "r", Version: 2}},
		{"no session to continue", Task{}, []journal.Event{ev(ERunQueued, Run{ID: "r", Task: "t", Profile: cmd}),
			ev(ERunObserved, Observation{ID: "r", State: Exited, NodeRev: 1, ExitCode: &zero})},
			Route{To: RouteNone, Why: WhyNoSession}},
		{"between stages", Task{Workflow: "w", Flow: flow, Stage: "build"}, []journal.Event{
			ev(ERunQueued, Run{ID: "r", Task: "t", Profile: fake, Stage: "build"}),
			ev(ERunObserved, Observation{ID: "r", State: Exited, NodeRev: 1, ExitCode: &zero, Session: "s", Provider: agent.ProviderFake}),
			ev(ETaskStaged, TaskStage{ID: "t", Stage: "review"})},
			Route{To: RouteWorkpad, Stage: "review", Version: 4}},
		{"done", Task{Status: StatusDone}, nil, Route{To: RouteNone, Why: WhyFinished}},
	}
	for _, c := range cases {
		s := New()
		tk := c.task
		tk.ID, tk.Title = "t", "x"
		if tk.Status == "" {
			tk.Status = StatusTodo
		}
		apply(t, s, ev(ETaskCreated, tk))
		for _, e := range c.events {
			apply(t, s, e)
		}
		if got := s.Route(s.Tasks["t"]); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestWhatARunCarriesOnOutlivesItAndGoesOutWithTheNext(t *testing.T) {
	ev := journal.NewEvent
	zero := 0
	s := New()
	apply(t, s, ev(ETaskCreated, Task{ID: "t", Title: "x", Status: StatusTodo}), ev(ERunQueued, Run{ID: "r", Task: "t", Machine: "m"}),
		ev(ERunObserved, Observation{ID: "r", State: Running, NodeRev: 1, Stream: true, Turn: 1}),
		ev(ERunSent, RunSend{ID: "r", Send: agent.Send{ID: "steer", Text: "a", State: agent.SendQueued}}),
		ev(ERunSent, RunSend{ID: "r", Send: agent.Send{ID: "after", Text: "b", State: agent.SendQueued, Mode: agent.SendAfter}}),
		ev(ERunSent, RunSend{ID: "r", Send: agent.Send{ID: "lost", Text: "c", State: agent.SendQueued, Mode: agent.SendAfter}}),
		ev(ERunInterrupt, RunInterrupt{ID: "r", Turn: 1, Ask: "int_1"}),
		ev(ERunAnswered, RunAnswer{ID: "r", Answer: agent.Answer{Request: "q", Allow: true}}),
		ev(ERunAnswered, RunAnswer{ID: "r", Answer: agent.Answer{Request: "q", Decision: agent.DecisionDeny, By: "u"}}))
	r := s.Runs["r"]
	if r.Turn != 1 || r.Interrupt == nil || r.Interrupt.Ask != "int_1" || len(r.Answers) != 1 || r.Answers[0].By != "u" {
		t.Fatalf("%+v", r)
	}
	apply(t, s, ev(ERunObserved, Observation{ID: "r", State: Exited, NodeRev: 2, ExitCode: &zero, Session: "s", Turn: 1}),
		ev(ERunSent, RunSend{ID: "r", Send: agent.Send{ID: "lost", State: agent.SendFailed}}))
	states := func() (out []string) {
		for _, m := range s.Runs["r"].Sends {
			out = append(out, m.ID+" "+m.State)
		}
		return out
	}
	if got := states(); !slices.Equal(got, []string{"steer failed", "after queued", "lost failed"}) {
		t.Fatalf("at its end: %v", got)
	}
	apply(t, s, ev(ERunQueued, Run{ID: "next", Task: "t", Machine: "m", Parent: "r", Resume: "s", Takes: []string{"after"}}),
		ev(ERunInterrupt, RunInterrupt{ID: "r", Turn: 2, Ask: "int_2"}))
	if got := states(); !slices.Equal(got, []string{"steer failed", "after sent", "lost failed"}) || s.Runs["r"].Interrupt.Turn != 1 {
		t.Fatalf("carried on: %v %+v", got, s.Runs["r"].Interrupt)
	}
}
