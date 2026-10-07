package remote

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/envcheck"
	"github.com/oxsean/fav/internal/fixture"
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
