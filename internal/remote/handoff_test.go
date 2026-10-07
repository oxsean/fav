package remote

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// A machine answers handoff.facts for one of its sessions and keeps what handoff.put writes for `tend handoff --open`,
// through either transport.
func TestHandoffFactsAndPutThroughEitherTransport(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			d := fixtureMachine(t)
			h := tr.rig(t).open("x", true)
			ctx := context.Background()
			p, err := h.Peer(ctx, "m")
			if err != nil || !p.Has(MHandoffFacts) || !p.Has(MHandoffPut) {
				t.Fatalf("peer: %+v %v", p.Hello.Methods, err)
			}
			s := d.Get("oauth")
			var f capture.HandoffFacts
			if err := p.Call(ctx, MHandoffFacts, Ref{s.Provider, s.ID}, &f); err != nil || f.SessionID != s.ID || f.Title == "" || f.Cwd != s.Cwd || len(f.Requests) == 0 {
				t.Fatalf("facts: %+v %v", f, err)
			}
			if err := p.Call(ctx, MHandoffFacts, Ref{s.Provider, "nope"}, &f); wire.Code(err) != wire.CodeNotFound {
				t.Errorf("an unknown session: %v", err)
			}
			from := PeerRef{Name: "studio", Endpoint: "e1"}
			var put HandoffPut
			err = p.Call(ctx, MHandoffPut, HandoffPutParams{Ref: Ref{s.Provider, s.ID}, Text: "# Handoff: x\n\nbody", Dir: s.Cwd, Provider: tend.ProviderCodex, From: from}, &put)
			if err != nil || !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(put.ID) {
				t.Fatalf("put: %+v %v", put, err)
			}
			m, path, err := capture.OpenHandoff(put.ID)
			if err != nil || path != put.Path || m.Dir != s.Cwd || m.Provider != tend.ProviderCodex || m.From != "studio" || m.Endpoint != "e1" ||
				m.Title != "Handoff: x" || m.Session != s.Provider+":"+s.ID {
				t.Fatalf("kept: %+v %s %v", m, path, err)
			}
			if fi, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
				t.Errorf("the pack is the user's alone: %v %v", fi, err)
			}
			for _, bad := range []HandoffPutParams{
				{Text: " ", Dir: s.Cwd, Provider: tend.ProviderClaude},
				{Text: "x", Dir: "rel/dir", Provider: tend.ProviderClaude},
				{Text: "x", Dir: s.Cwd, Provider: "sh"},
			} {
				if err := p.Call(ctx, MHandoffPut, bad, nil); wire.Code(err) != wire.CodeBadRequest {
					t.Errorf("%+v: %v", bad, err)
				}
			}
			if _, _, err := capture.OpenHandoff("../" + put.ID); err == nil {
				t.Error("an id names a file in the handoff directory only")
			}
		})
	}
}

// fakePeer answers from a table and records each call's params.
func fakePeer(name, endpoint, goos, home string, answers map[string]any, sent map[string]json.RawMessage) Peer {
	var methods []string
	for m := range answers {
		methods = append(methods, m)
	}
	hello := Hello{Version: "v9", OS: goos, Home: home, Hostname: name + "-host", Endpoint: endpoint, Methods: methods}
	return PeerOf(name, hello, func(_ context.Context, method string, params, out any) error {
		b, _ := json.Marshal(params)
		sent[method] = b
		res, _ := json.Marshal(answers[method])
		return json.Unmarshal(res, out)
	})
}

// The driver reads the facts on the source, finds the directory on the target (a project's pair first, then its
// node.repos for the remote), writes the pack for the target's reader and puts it there.
func TestHandoffDriverAcrossTwoMachines(t *testing.T) {
	facts := capture.HandoffFacts{Provider: tend.ProviderClaude, SessionID: "s1", Title: "Paging", Cwd: "/home/dev/shop/api",
		Transcript: "/home/dev/.claude/projects/x/s1.jsonl", Requests: []string{"fix it"}, Git: capture.HandoffGit{Remote: "git@example.com:acme/shop.git"}}
	sentFrom, sentTo := map[string]json.RawMessage{}, map[string]json.RawMessage{}
	from := fakePeer("studio", "e1", "linux", "/home/dev", map[string]any{MHandoffFacts: facts}, sentFrom)
	to := fakePeer("pc", "e2", "windows", `C:\Users\dev`, map[string]any{
		MRepos:      Repos{Dirs: []RepoDir{{Path: `D:\src\shop`, Branch: "main", From: "scan"}}},
		MHandoffPut: HandoffPut{ID: "s1-1", Path: `C:\Users\dev\.agent\tend\handoff\s1-1.md`},
	}, sentTo)
	ctx := context.Background()
	x, err := Handoff(ctx, from, to, Ref{tend.ProviderClaude, "s1"})
	if err != nil || x.Facts.Title != "Paging" || string(sentFrom[MHandoffFacts]) != `{"provider":"claude","session_id":"s1"}` {
		t.Fatalf("facts: %+v %v %s", x, err, sentFrom[MHandoffFacts])
	}

	dirs, err := x.Dirs(ctx, []DirPair{{From: "/srv/elsewhere", To: `E:\x`}, {From: "/home/dev/shop", To: `D:\work\shop`}})
	if err != nil || len(dirs) != 1 || dirs[0] != (RepoDir{Path: `D:\work\shop\api`, From: "project"}) || sentTo[MRepos] != nil {
		t.Fatalf("a project's directory on both machines decides, node.repos unasked: %+v %v", dirs, err)
	}
	dirs, err = x.Dirs(ctx, nil, "https://example.com/other")
	if err != nil || len(dirs) != 1 || dirs[0].Path != `D:\src\shop` || string(sentTo[MRepos]) != `{"remote":"git@example.com:acme/shop.git"}` {
		t.Fatalf("else the target's checkouts of the session's own remote: %+v %v %s", dirs, err, sentTo[MRepos])
	}
	x.Facts.Git.Remote = ""
	delete(sentTo, MRepos)
	if dirs, err := x.Dirs(ctx, nil, "", " "); err != nil || dirs != nil || sentTo[MRepos] != nil {
		t.Errorf("no remote at all asks nothing: %+v %v", dirs, err)
	}
	old := fakePeer("pc", "e2", "windows", `C:\Users\dev`, map[string]any{MHandoffPut: HandoffPut{}}, sentTo)
	if _, err := (&Handover{From: from, To: old, Facts: facts}).Dirs(ctx, nil); wire.Code(err) != wire.CodeUnknownMethod {
		t.Errorf("a target without node.repos is not sent it: %v", err)
	}

	text := x.Text(`D:\work\shop\api`)
	for _, want := range []string{i18n.F("handoff.elsewhere", "studio"), i18n.F("handoff.dirs.dir", facts.Cwd, `D:\work\shop\api`),
		i18n.F("handoff.dirs.home", "/home/dev", `C:\Users\dev`)} {
		if !strings.Contains(text, want) {
			t.Errorf("pack lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, facts.Transcript) {
		t.Errorf("the source's transcript path stays out:\n%s", text)
	}
	put, err := x.Put(ctx, text, `D:\work\shop\api`, tend.ProviderCodex)
	var p HandoffPutParams
	json.Unmarshal(sentTo[MHandoffPut], &p)
	if err != nil || put.ID != "s1-1" || p.Ref != (Ref{tend.ProviderClaude, "s1"}) || p.Dir != `D:\work\shop\api` || p.Provider != tend.ProviderCodex ||
		p.From != (PeerRef{Name: "studio", Endpoint: "e1", End: End{OS: "linux", Home: "/home/dev", Host: "studio-host"}}) {
		t.Fatalf("put: %+v %v, sent %+v", put, err, p)
	}

	same := &Handover{From: from, To: fakePeer("studio-2", "e1", "linux", "/home/dev", nil, sentTo), Facts: facts}
	if text := same.Text(facts.Cwd); !strings.Contains(text, facts.Transcript) || strings.Contains(text, i18n.T("handoff.dirs")) {
		t.Errorf("handed to its own machine, the pack reads as a local one:\n%s", text)
	}
}

// Here answers in this process, as this machine's hello says.
func TestHereAnswersInThisProcess(t *testing.T) {
	d := fixtureMachine(t)
	here := Here("t")
	s := d.Get("oauth")
	var f capture.HandoffFacts
	if err := here.Call(context.Background(), MHandoffFacts, Ref{s.Provider, s.ID}, &f); err != nil || f.SessionID != s.ID || here.Name != "" ||
		here.Hello.Endpoint != LocalHello("t").Endpoint || !here.Same(Here("u")) {
		t.Fatalf("%+v %v", f, err)
	}
	if err := here.Call(context.Background(), MRepos, ReposParams{Remote: "x"}, nil); wire.Code(err) != wire.CodeUnknownMethod {
		t.Errorf("node methods are the node's: %v", err)
	}
}
