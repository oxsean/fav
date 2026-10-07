package remote

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/envcheck"
	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

func TestEnvAnswersThePrintAndWhatASessionSaw(t *testing.T) {
	d, c := localMachine(t)
	ctx := context.Background()
	var h Hello
	if err := c.Call(ctx, MHello, HelloParams{}, &h); err != nil || !slices.Contains(h.Methods, MEnv) || !slices.Contains(h.Methods, MEnvFile) {
		t.Fatalf("hello lists env and env.file: %v %v", h.Methods, err)
	}
	oauth := d.Get("oauth")
	var raw json.RawMessage
	if err := c.Call(ctx, MEnv, EnvParams{Ref: &Ref{Provider: oauth.Provider, SessionID: oauth.ID}}, &raw); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{fixture.Secret, fixture.SecretEmail} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("env carries %s: %s", secret, raw)
		}
	}
	var p envcheck.Print
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.Dir != oauth.Cwd || p.Seen == nil || p.Seen.CLI != tend.ProviderClaude || p.Seen.Model != "claude-opus-5-5" ||
		!slices.ContainsFunc(p.Seen.Files, func(f envcheck.File) bool { return f.Kind == envcheck.KindDir && f.Name == "CLAUDE.md" }) {
		t.Fatalf("without a dir, the session's own, and what it saw: %+v", p)
	}
	if !slices.Contains(p.Skills, "review") || !p.Project.Trusted {
		t.Errorf("the print of its directory: %+v", p)
	}

	var bare envcheck.Print
	if err := c.Call(ctx, MEnv, EnvParams{Dir: filepath.Join(d.Work, "notes-api")}, &bare); err != nil || bare.Seen != nil || bare.Project.Trusted {
		t.Errorf("a directory alone: %+v %v", bare, err)
	}
	for want, params := range map[string]EnvParams{
		wire.CodeNotFound:   {Ref: &Ref{Provider: "claude", SessionID: "nope"}},
		wire.CodeBadRequest: {Ref: &Ref{SessionID: oauth.ID}},
		"relative":          {Dir: filepath.Join("work", "webapp")},
	} {
		if err := c.Call(ctx, MEnv, params, &bare); code(err) != want && (want != "relative" || code(err) != wire.CodeBadRequest) {
			t.Errorf("%+v: %v", params, err)
		}
	}

	var text EnvText
	if err := c.Call(ctx, MEnvFile, EnvFileParams{Kind: envcheck.KindDir, Name: "CLAUDE.md", Dir: oauth.Cwd}, &text); err != nil ||
		!strings.Contains(text.Text, "Run npm test") {
		t.Fatalf("env.file: %q %v", text.Text, err)
	}
	for _, p := range []EnvFileParams{{Kind: envcheck.KindDir, Name: ".env", Dir: oauth.Cwd}, {Kind: envcheck.KindClaude, Name: ".credentials.json"},
		{Kind: envcheck.KindPath, Name: filepath.Join(oauth.Cwd, ".env")}} {
		if err := c.Call(ctx, MEnvFile, p, &text); code(err) != wire.CodeNotFound {
			t.Errorf("%+v: %v %q", p, err, text.Text)
		}
	}
}

// A handover compares the session's environment where it ran with the target directory's: the pack gets the summary
// and what blocks or changes (no hint), a block refuses nothing; a target that cannot answer leaves the pack saying so.
func TestAHandoverComparesBothEnvironmentsForThePack(t *testing.T) {
	src := envcheck.Print{Dir: "/home/dev/shop", CLIs: []envcheck.CLI{{Name: "claude", Found: true, Version: "2.1.292"}},
		Files: []envcheck.File{{Kind: envcheck.KindDir, Name: "CLAUDE.md", SHA: "s1", Norm: "n1"}}, Skills: []string{"review", "tend"},
		Seen: &envcheck.Seen{CLI: tend.ProviderClaude, Version: "2.1.292", Used: []string{"review"}, Known: []string{"skills"}}}
	dst := envcheck.Print{Dir: `D:\work\shop`, CLIs: []envcheck.CLI{{Name: "claude"}}, Files: []envcheck.File{{Kind: envcheck.KindDir, Name: "CLAUDE.md", SHA: "s1", Norm: "n1"}}}
	sentFrom, sentTo := map[string]json.RawMessage{}, map[string]json.RawMessage{}
	from := fakePeer("studio", "e1", "linux", "/home/dev", map[string]any{MEnv: src}, sentFrom)
	to := fakePeer("pc", "e2", "windows", `C:\Users\dev`, map[string]any{MEnv: dst}, sentTo)
	x := &Handover{From: from, To: to, Ref: Ref{tend.ProviderClaude, "s1"}, Facts: capture.HandoffFacts{Provider: tend.ProviderClaude, SessionID: "s1", Cwd: src.Dir}}
	ctx := context.Background()
	rep, err := x.Diagnose(ctx, dst.Dir)
	if err != nil || rep.Block != 1 || rep.Unequal != 1 || rep.Hint != 1 {
		t.Fatalf("a missing CLI blocks, a used skill missing is unequal, an unused one a hint: %+v %v", rep, err)
	}
	if string(sentFrom[MEnv]) != `{"dir":"","ref":{"provider":"claude","session_id":"s1"}}` || string(sentTo[MEnv]) != `{"dir":"D:\\work\\shop"}` {
		t.Errorf("the session where it ran, the directory where it goes: %s %s", sentFrom[MEnv], sentTo[MEnv])
	}
	text := x.Text(dst.Dir)
	for _, want := range []string{rep.Summary(), "- " + envcheck.LevelText(envcheck.LevelBlock) + ": " + i18n.F("envcheck.cli_missing", "claude"),
		"- " + envcheck.LevelText(envcheck.LevelUnequal) + ": " + i18n.F("envcheck.skill_missing", "review")} {
		if !strings.Contains(text, want) {
			t.Errorf("pack lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, i18n.F("envcheck.skill_missing", "tend")) {
		t.Errorf("hints stay out of the pack:\n%s", text)
	}

	x.To = fakePeer("pc", "e2", "windows", `C:\Users\dev`, map[string]any{MHandoffPut: HandoffPut{}}, sentTo)
	if _, err := x.Diagnose(ctx, dst.Dir); wire.Code(err) != wire.CodeUnknownMethod {
		t.Fatalf("a target without env: %v", err)
	}
	if text := x.Text(dst.Dir); !strings.Contains(text, i18n.F("handoff.env.unread", Reason(&wire.Error{Code: wire.CodeUnknownMethod}))) {
		t.Errorf("the pack says it was not compared:\n%s", text)
	}
}

// CompareEnv says why the environments were not compared, naming the machine that did not answer: the session's when
// its tend has no env, else the target as the caller names it; compared, the pack keeps the summary.
func TestCompareEnvNamesTheMachineThatDidNotAnswer(t *testing.T) {
	ctx, sent := context.Background(), map[string]json.RawMessage{}
	peer := func(name, endpoint string, env bool) Peer {
		answers := map[string]any{MHandoffPut: HandoffPut{}}
		if env {
			answers[MEnv] = envcheck.Print{Dir: "/home/dev/shop"}
		}
		return fakePeer(name, endpoint, "linux", "/home/dev", answers, sent)
	}
	old := &wire.Error{Code: wire.CodeUnknownMethod}
	for _, c := range []struct {
		fromEnv, toEnv bool
		who            string
	}{{false, true, "studio"}, {true, false, "the pc"}} {
		x := &Handover{From: peer("studio", "e1", c.fromEnv), To: peer("pc", "e2", c.toEnv), Ref: Ref{tend.ProviderClaude, "s1"}}
		if _, why := x.CompareEnv(ctx, "/srv/shop", "the pc"); why != EnvRefusal(c.who, old) {
			t.Errorf("from env %v, to env %v: %q", c.fromEnv, c.toEnv, why)
		}
	}
	x := &Handover{From: peer("studio", "e1", true), To: peer("pc", "e2", true), Ref: Ref{tend.ProviderClaude, "s1"}}
	if rep, why := x.CompareEnv(ctx, "/srv/shop", "pc"); why != "" || x.Env == nil || x.Env.Summary != rep.Summary() {
		t.Fatalf("compared: %q %+v", why, x.Env)
	}
}
