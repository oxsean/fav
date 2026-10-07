package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// grepNode answers hello and grep; old: a tend from before grep.
type grepNode struct {
	old   bool
	asked *[]remote.GrepParams
}

func (g grepNode) Handle(_ context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case remote.MHello:
		h := remote.Hello{Proto: wire.Proto, Version: "v0.0.9", Methods: []string{remote.MHello, remote.MList}}
		if !g.old {
			h.Methods = append(h.Methods, remote.MGrep)
		}
		return h, nil
	case remote.MList:
		return remote.List{}, nil
	case remote.MGrep:
		var p remote.GrepParams
		json.Unmarshal(params, &p)
		*g.asked = append(*g.asked, p)
		return remote.GrepResult{Hits: []remote.GrepHit{{Row: remote.Row{Session: remote.Session{Provider: tend.ProviderClaude,
			SessionID: farFavorite, Title: "far favorite", UpdatedAt: time.Now(), LastAt: time.Now()}}, Hits: 2, AllInOne: true,
			Snippet: "远端的回调"}}}, nil
	}
	return nil, &wire.Error{Code: wire.CodeUnknownMethod}
}

func TestGrepSearchesTheMachinesHostPicks(t *testing.T) {
	machine(t)
	var asked []remote.GrepParams
	nodes := map[string]remote.Handler{"mba": grepNode{asked: &asked}, "old": grepNode{old: true, asked: &asked}}
	h := remote.NewHostsDial([]tend.Host{{Name: "mba"}, {Name: "old"}, {Name: "down"}}, i18n.EN, func(host tend.Host) (*remote.Client, error) {
		if nodes[host.Name] == nil {
			return nil, &wire.Error{Code: wire.CodeOffline}
		}
		return remote.Pipe(nodes[host.Name]), nil
	})
	was := remoteHosts
	remoteHosts = func() *remote.Hosts { return h }
	t.Cleanup(func() { remoteHosts = was; h.Close() })

	rows, stderr := listedBy(t, "grep", "--json", "回调", "host:all")
	if len(rows) < 2 || rows[0].Host != "" || rows[1].Host != "mba" || rows[1].SessionID != farFavorite {
		t.Fatalf("this machine's best, then mba's, as sessions.grep merges: %+v", rows)
	}
	if len(asked) != 1 || asked[0].Q != "回调 host:all" || !asked[0].All || asked[0].Limit != remote.GrepLimit {
		t.Fatalf("one grep, to mba: %+v", asked)
	}
	for _, want := range []string{i18n.F("remote.down", "old", i18n.T("msg.far_old")),
		i18n.F("remote.down", "down", remote.Reason(&wire.Error{Code: wire.CodeOffline}))} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr names %q:\n%s", want, stderr)
		}
	}

	rows, _ = listedBy(t, "grep", "--json", "回调")
	for _, r := range rows {
		if r.Host != "" {
			t.Fatalf("without host: only this machine: %+v", rows)
		}
	}
	if len(asked) != 1 {
		t.Fatal("no machine asked without host:")
	}
}
