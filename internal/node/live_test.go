package node

import (
	"os"
	"path/filepath"
	"testing"

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
	capture.RecordHookEvent(sid, "PermissionRequest", tp)
	if got := s.look(); got != AttentionAsked {
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
