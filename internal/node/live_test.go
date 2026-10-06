package node

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/tend"
)

// liveSup is the supervisor of an interactive claude run in pane p1 whose transcript is lines.
func liveSup(t *testing.T, session string, lines ...string) *sup {
	t.Helper()
	dir := t.TempDir()
	tp := filepath.Join(capture.ClaudeHome(), "projects", "-work", session+".jsonl")
	if err := os.MkdirAll(filepath.Dir(tp), 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(tp, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return &sup{dir: dir, spec: Spec{Provider: tend.ProviderClaude, Runner: RunnerHerdr},
		st: State{State: StateRunning, Pane: "p1", Session: session}}
}

func (s *sup) look() string {
	s.liveAt = s.liveAt.AddDate(-1, 0, 0)
	s.watchLive()
	return s.st.Attention
}

const (
	liveReply = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`
	liveAsk   = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"a1","name":"AskUserQuestion","input":{"questions":[]}}]}}`
)

func TestAnInteractiveRunIsAskedWhileItsPaneIsBlocked(t *testing.T) {
	status := "blocked"
	was := paneStatus
	paneStatus = func(string) string { return status }
	t.Cleanup(func() { paneStatus = was })
	s := liveSup(t, "11111111-1111-1111-1111-111111111111", liveReply)
	if got := s.look(); got != AttentionAsked {
		t.Fatalf("blocked pane: %q", got)
	}
	status = "working"
	if got := s.look(); got != "" {
		t.Fatalf("working again: %q", got)
	}
}

func TestAnInteractiveRunIsAskedWhileItsTranscriptEndsOnAQuestion(t *testing.T) {
	was := paneStatus
	paneStatus = func(string) string { return "" }
	t.Cleanup(func() { paneStatus = was })
	s := liveSup(t, "22222222-2222-2222-2222-222222222222", liveReply, liveAsk)
	if got := s.look(); got != AttentionAsked {
		t.Fatalf("question in the transcript: %q", got)
	}
}

func TestAHookPromptMarksAnInteractiveRunUntilItsTranscriptGrows(t *testing.T) {
	was := paneStatus
	paneStatus = func(string) string { return "" }
	t.Cleanup(func() { paneStatus = was })
	sid := "33333333-3333-3333-3333-333333333333"
	s := liveSup(t, sid, liveReply)
	tp := capture.TranscriptPath(tend.ProviderClaude, sid)
	capture.RecordHookEvent(capture.HookCall{Session: sid, Transcript: tp, Event: "PermissionRequest"})
	if got := s.look(); got != AttentionPermission {
		t.Fatalf("permission prompt: %q", got)
	}
	f, err := os.OpenFile(tp, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(liveReply + "\n")
	f.Close()
	if got := s.look(); got != "" {
		t.Fatalf("answered: %q", got)
	}
}

func TestAReportedQuestionOutlivesWhatThePaneShows(t *testing.T) {
	was := paneStatus
	paneStatus = func(string) string { return "working" }
	t.Cleanup(func() { paneStatus = was })
	s := liveSup(t, "44444444-4444-4444-4444-444444444444", liveReply)
	s.st.Attention, s.st.Ask = AttentionAsked, "which one?"
	if got := s.look(); got != AttentionAsked || s.st.Ask != "which one?" {
		t.Fatalf("tend run ask is not the pane's to clear: %q %q", got, s.st.Ask)
	}
}

// tick is one pass of an interactive run's supervisor loop as far as attention goes.
func (s *sup) tick() string {
	s.look()
	s.stall()
	return s.st.Attention
}

func growTranscript(t *testing.T, sid, line string) {
	t.Helper()
	f, err := os.OpenFile(capture.TranscriptPath(tend.ProviderClaude, sid), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(line + "\n")
	f.Close()
}

func quietPane(t *testing.T, status string) *string {
	was := paneStatus
	paneStatus = func(string) string { return status }
	t.Cleanup(func() { paneStatus = was })
	return &status
}

const liveTool = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"npm test"}}]}}`

func TestAPermissionHookMarksAnInteractiveRunPermissionWithItsTool(t *testing.T) {
	quietPane(t, "")
	sid := "55555555-5555-5555-5555-555555555555"
	s := liveSup(t, sid, liveTool)
	tp := capture.TranscriptPath(tend.ProviderClaude, sid)
	capture.RecordHookEvent(capture.HookCall{Session: sid, Transcript: tp, Event: "PermissionRequest", Tool: "Bash",
		Input: []byte(`{"command":"npm test","description":"Run the tests"}`)})
	if got := s.look(); got != AttentionPermission || s.st.Ask != "Bash: npm test" {
		t.Fatalf("a permission prompt is a permission, with its tool: %q %q", got, s.st.Ask)
	}
	capture.RecordHookEvent(capture.HookCall{Session: sid, Transcript: tp, Event: "Notification", Kind: "permission_prompt",
		Message: "Claude needs your permission to use Bash"})
	if got := s.look(); got != AttentionPermission || s.st.Ask != "Bash: npm test" {
		t.Fatalf("the notification of the same prompt keeps the tool: %q %q", got, s.st.Ask)
	}
	growTranscript(t, sid, liveReply)
	if got := s.look(); got != "" || s.st.Ask != "" {
		t.Fatalf("answered: %q %q", got, s.st.Ask)
	}
}

func TestAPermissionNotificationAloneSaysItsMessage(t *testing.T) {
	quietPane(t, "")
	sid := "56565656-5656-5656-5656-565656565656"
	s := liveSup(t, sid, liveTool)
	capture.RecordHookEvent(capture.HookCall{Session: sid, Transcript: capture.TranscriptPath(tend.ProviderClaude, sid),
		Event: "Notification", Kind: "permission_prompt", Message: "Claude needs your permission to use Bash"})
	if got := s.look(); got != AttentionPermission || s.st.Ask != "Claude needs your permission to use Bash" {
		t.Fatalf("permission_prompt: %q %q", got, s.st.Ask)
	}
}

func TestAWaitTheSourcesCannotTellIsAskedWithoutAQuestion(t *testing.T) {
	for name, call := range map[string]capture.HookCall{
		"elicitation":   {Event: "Notification", Kind: "elicitation_dialog", Message: "MCP server wants input"},
		"needs input":   {Event: "Notification", Kind: "agent_needs_input"},
		"untyped":       {Event: "Notification"},
		"plan approval": {Event: "PermissionRequest", Tool: "ExitPlanMode"},
	} {
		t.Run(name, func(t *testing.T) {
			quietPane(t, "")
			sid := "57575757-5757-5757-5757-575757575757"
			s := liveSup(t, sid, liveReply)
			call.Session, call.Transcript = sid, capture.TranscriptPath(tend.ProviderClaude, sid)
			capture.RecordHookEvent(call)
			if got := s.look(); got != AttentionAsked || s.st.Ask != "" {
				t.Fatalf("neutral wait: %q %q", got, s.st.Ask)
			}
		})
	}
	quietPane(t, "blocked")
	s := liveSup(t, "58585858-5858-5858-5858-585858585858", liveReply)
	if got := s.look(); got != AttentionAsked || s.st.Ask != "" {
		t.Fatalf("a blocked pane alone is a neutral wait: %q %q", got, s.st.Ask)
	}
}

func TestAQuestionInTheTranscriptIsAskedWithItsText(t *testing.T) {
	quietPane(t, "blocked")
	sid := "59595959-5959-5959-5959-595959595959"
	s := liveSup(t, sid, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"a1","name":"AskUserQuestion","input":{"questions":[{"question":"Which database?","options":[{"label":"pg"}]}]}}]}}`)
	capture.RecordHookEvent(capture.HookCall{Session: sid, Transcript: capture.TranscriptPath(tend.ProviderClaude, sid),
		Event: "PermissionRequest", Tool: "AskUserQuestion", Input: []byte(`{"questions":[]}`)})
	if got := s.look(); got != AttentionAsked || s.st.Ask != "Which database?" {
		t.Fatalf("a question is asked, never a permission: %q %q", got, s.st.Ask)
	}
}

func TestTheWaitFollowsWhatItWaitsOn(t *testing.T) {
	quietPane(t, "")
	sid := "5a5a5a5a-5a5a-5a5a-5a5a-5a5a5a5a5a5a"
	s := liveSup(t, sid, liveTool)
	tp := capture.TranscriptPath(tend.ProviderClaude, sid)
	capture.RecordHookEvent(capture.HookCall{Session: sid, Transcript: tp, Event: "Notification", Kind: "agent_needs_input"})
	if got := s.look(); got != AttentionAsked {
		t.Fatalf("neutral first: %q", got)
	}
	capture.RecordHookEvent(capture.HookCall{Session: sid, Transcript: tp, Event: "PermissionRequest", Tool: "Bash", Input: []byte(`{"command":"ls"}`)})
	if got := s.look(); got != AttentionPermission || s.st.Ask != "Bash: ls" {
		t.Fatalf("then a permission: %q %q", got, s.st.Ask)
	}
}

func TestAnInteractiveRunWithAGrowingTranscriptIsNotStalled(t *testing.T) {
	quietPane(t, "working")
	sid := "5b5b5b5b-5b5b-5b5b-5b5b-5b5b5b5b5b5b"
	s := liveSup(t, sid, liveTool)
	s.spec.StallAfter = time.Minute
	s.seen = time.Now().Add(-time.Hour)
	s.look() // the first read finds the transcript as it is
	s.seen = time.Now().Add(-time.Hour)
	if got := s.tick(); got != AttentionStalled {
		t.Fatalf("an hour without its transcript growing while it works: %q", got)
	}
	growTranscript(t, sid, liveTool)
	if got := s.tick(); got != "" {
		t.Fatalf("the transcript grew: %q", got)
	}
}

func TestAnInteractiveRunIsNeverStalledWhileItWaitsOrRests(t *testing.T) {
	status := quietPane(t, "blocked")
	sid := "5c5c5c5c-5c5c-5c5c-5c5c-5c5c5c5c5c5c"
	s := liveSup(t, sid, liveTool)
	s.spec.StallAfter = time.Minute
	s.look()
	s.seen = time.Now().Add(-time.Hour)
	if got := s.tick(); got != AttentionAsked {
		t.Fatalf("waiting for the user is not stalled: %q", got)
	}
	*status = "working"
	if got := s.tick(); got != "" {
		t.Fatalf("the quiet time starts again once the wait ends: %q", got)
	}
	*status = "idle"
	s.seen = time.Now().Add(-time.Hour)
	if got := s.tick(); got != "" {
		t.Fatalf("an idle pane has ended its turn: %q", got)
	}
	*status = ""
	growTranscript(t, sid, liveReply)
	s.look()
	s.seen = time.Now().Add(-time.Hour)
	if got := s.tick(); got != "" {
		t.Fatalf("a transcript ending on its reply has ended its turn: %q", got)
	}
}

func TestAStalledInteractiveRunThatWaitsShowsTheWait(t *testing.T) {
	status := quietPane(t, "working")
	s := liveSup(t, "5d5d5d5d-5d5d-5d5d-5d5d-5d5d5d5d5d5d", liveTool)
	s.spec.StallAfter = time.Minute
	s.look()
	s.seen = time.Now().Add(-time.Hour)
	if got := s.tick(); got != AttentionStalled {
		t.Fatalf("stalled: %q", got)
	}
	*status = "blocked"
	if got := s.tick(); got != AttentionAsked {
		t.Fatalf("a wait replaces stalled: %q", got)
	}
}

func TestStallAfterOffNeverStallsAnInteractiveRun(t *testing.T) {
	quietPane(t, "working")
	s := liveSup(t, "5e5e5e5e-5e5e-5e5e-5e5e-5e5e5e5e5e5e", liveTool)
	s.look()
	s.seen = time.Now().Add(-time.Hour)
	if got := s.tick(); got != "" {
		t.Fatalf("stall_after off: %q", got)
	}
}

func TestAnInteractiveRunsOutputTimeIsItsTranscriptsLastGrowthToTheMinute(t *testing.T) {
	quietPane(t, "working")
	sid := "5f5f5f5f-5f5f-5f5f-5f5f-5f5f5f5f5f5f"
	s := liveSup(t, sid, liveTool)
	s.look()
	s.seen = time.Now().Add(-time.Hour)
	s.outputAt()
	want := s.seen.Truncate(time.Minute)
	if s.st.OutputAt == nil || !s.st.OutputAt.Equal(want) {
		t.Fatalf("output_at %v, want %v", s.st.OutputAt, want)
	}
	rev := s.st.Rev
	s.seen = s.seen.Add(time.Second)
	if s.seen.Truncate(time.Minute).Equal(want) {
		s.outputAt()
		if s.st.Rev != rev {
			t.Fatal("output within the same minute writes no new state")
		}
	}
	growTranscript(t, sid, liveTool)
	s.look()
	s.outputAt()
	if s.st.OutputAt == nil || time.Since(*s.st.OutputAt) > time.Minute {
		t.Fatalf("the transcript grew: output_at %v", s.st.OutputAt)
	}
}

func TestAnInteractiveRunThatEndsDropsTheWaitItsPaneShowed(t *testing.T) {
	quietPane(t, "blocked")
	s := liveSup(t, "60606060-6060-6060-6060-606060606060", liveReply)
	if got := s.look(); got != AttentionAsked {
		t.Fatalf("blocked: %q", got)
	}
	s.exited(0)
	if s.st.Attention != "" {
		t.Fatalf("nothing waits once the agent is gone: %q", s.st.Attention)
	}
}
