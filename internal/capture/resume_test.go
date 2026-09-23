package capture

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
)

func TestResumeNamesTheSessionByItsLabel(t *testing.T) {
	r := &fav.Rec{Provider: fav.ProviderClaude, SessionID: "abc-123", Title: "notes-api 搜索分页游标漂移排障", Label: "geo 排障 it's \"quoted\""}
	spec, err := buildResume(r)
	if err != nil || !slices.Equal(spec.Argv(), []string{"claude", "--resume", "abc-123", "--name", TabLabel(r)}) {
		t.Fatalf("argv %q: %v", spec.Argv(), err)
	}
}

func TestPlanResumeRespectsLiveSessions(t *testing.T) {
	r := &fav.Rec{Provider: fav.ProviderClaude, SessionID: "abc", Title: "x", Cwd: t.TempDir()}
	p, err := PlanResume(r, map[string]Live{"abc": {TabID: "w:t1", PaneID: "w:p1"}}, false)
	if err != nil || p.Live.TabID != "w:t1" || p.Spec.Exec != "" {
		t.Errorf("在 Herdr 里跑着的会话应只切 tab：%+v %v", p, err)
	}
	p, err = PlanResume(r, map[string]Live{"abc": {BackgroundID: "abc12345"}}, true)
	if err != nil || strings.Join(p.Spec.Argv(), " ") != "claude attach abc12345" {
		t.Errorf("后台会话应 attach：%v %v", p.Spec.Argv(), err)
	}
	p, _ = PlanResume(r, nil, true)
	if !strings.HasPrefix(strings.Join(p.Spec.Argv(), " "), "claude --resume abc") || p.Ws != nil {
		t.Errorf("普通会话应 --resume 且 --no-herdr 时不进 Herdr：%+v", p)
	}
}

func TestPlanResumeBlocksASecondWriter(t *testing.T) {
	r := &fav.Rec{Provider: fav.ProviderClaude, SessionID: "s1", Cwd: t.TempDir()}
	elsewhere := Check{Text: i18n.T("resume.check.running_elsewhere")}
	if p, _ := PlanResume(r, map[string]Live{"s1": {Status: "idle"}}, true); !slices.Contains(p.Checks, elsewhere) {
		t.Fatalf("running elsewhere blocks resuming here: %+v", p.Checks)
	}
	if p, _ := PlanResume(r, map[string]Live{"s1": {BackgroundID: "b1"}}, true); slices.Contains(p.Checks, elsewhere) {
		t.Fatal("a background session is attached, not resumed twice")
	}
}

func TestChecks(t *testing.T) {
	dir := t.TempDir()
	tr := filepath.Join(dir, "s.jsonl")
	os.WriteFile(tr, []byte(`{"timestamp":"2026-09-22T10:00:06Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":90},"model_context_window":100}}}`+"\n"), 0o644)
	gone := filepath.Join(dir, "wt")
	has := func(cs []Check, key string, a ...any) *Check {
		want := i18n.F(key, a...)
		for i := range cs {
			if cs[i].Text == want {
				return &cs[i]
			}
		}
		return nil
	}
	cs := Checks(&fav.Rec{Provider: fav.ProviderCodex, SessionID: "s", Cwd: gone, Repo: dir, TranscriptPath: tr, CodexArchived: true})
	if c := has(cs, "resume.check.worktree_gone", gone, dir); c == nil || c.OK || c.Warn {
		t.Errorf("a vanished linked worktree blocks, naming the main checkout: %+v", cs)
	}
	if c := has(cs, "resume.check.context_full", 90); c == nil || !c.Warn {
		t.Errorf("a context over 80%% warns: %+v", cs)
	}
	if c := has(cs, "resume.check.codex_archived", "s"); c == nil || !c.Warn {
		t.Errorf("an archived thread warns: %+v", cs)
	}
	if c := has(cs, "resume.check.transcript_ok"); c == nil || !c.OK {
		t.Errorf("the transcript is there: %+v", cs)
	}
	cs = Checks(&fav.Rec{Provider: fav.ProviderClaude, SessionID: "s", Cwd: dir, TranscriptPath: filepath.Join(dir, "nope.jsonl")})
	if c := has(cs, "resume.check.transcript_gone"); c == nil || c.OK || c.Warn || (Plan{Checks: cs}).Blocking() == nil {
		t.Errorf("a missing transcript blocks: %+v", cs)
	}
	if has(cs, "resume.check.context_full", 90) != nil || has(cs, "resume.check.codex_archived", "s") != nil {
		t.Errorf("no warnings for a plain session: %+v", cs)
	}
}
