package server

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/wire"
)

// actions stands in for the coordinator behind /api/act: it records what it is asked and answers err.
type actions struct {
	mu  sync.Mutex
	got []string
	err error
}

func (a *actions) Act(user string, on coord.ActOn) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.got = append(a.got, user+" "+on.Task+" "+on.Item+" "+on.Action)
	return a.err
}

func (a *actions) taken() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.got
	a.got = nil
	return out
}

// member admits someone named name and signs a browser in as them.
func (r *rig) member(name string) (store.User, *http.Client) {
	r.t.Helper()
	email := strings.ToLower(name) + "@corp.example"
	must(r.t, r.team.AddAdmit(store.Admit{Kind: store.AdmitEmail, Value: email, Role: store.RoleMember}))
	u, err := r.team.Admit(store.Identity{Provider: "gitea", Issuer: "https://git.example", Subject: name, Email: email, EmailVerified: true, Name: name}, "")
	must(r.t, err)
	tok, _, err := r.team.NewCredential(store.KindToken, "t-"+name, u.ID, 0)
	must(r.t, err)
	r.srv.sweep()
	c := browser(r.t)
	login(r.t, c, r.url, tok)
	return u, c
}

// A token acts for the one it was made for, as it was made, until it expires: whatever else comes (another's session,
// a changed or foreign token, no session, another site) never reaches the coordinator. The key outlives a restart.
func TestAnActionTokenActsOnlyForItsHolderWhileItHolds(t *testing.T) {
	r := newRig(t)
	f := &actions{}
	r.srv.acts = f
	ann, annC := r.member("Ann")
	bob, bobC := r.member("Bob")
	claim := actClaim{User: ann.ID, Task: "t_1", Item: "r_1/q1", Version: 7, Action: coord.ActDeny, Seq: 5, Device: "d_1", Until: time.Now().Add(time.Hour).Unix()}
	tok := r.srv.opt.Act.mint(claim)
	act := func(c *http.Client, tok string, extra map[string]string) (int, map[string]any) {
		t.Helper()
		got, out, _ := ask(t, r, c, "/api/act", map[string]string{"token": tok}, extra)
		return got, out
	}

	if got, out := act(annC, tok, nil); got != http.StatusNoContent {
		t.Fatalf("%d %v", got, out)
	}
	if got := f.taken(); len(got) != 1 || got[0] != ann.ID+" t_1 r_1/q1 deny" {
		t.Fatalf("%v", got)
	}
	if got, _ := act(annC, tok, nil); got != http.StatusNoContent || len(f.taken()) != 1 {
		t.Fatal("the same token again goes to the coordinator, which does it once")
	}
	log, _ := r.team.AuditLog(20)
	if !strings.Contains(log[0].Kind+" "+log[0].Detail, "push.act") || !strings.Contains(log[0].Detail, "device=d_1") || log[0].Actor != ann.ID {
		t.Fatalf("audit: %+v", log[0])
	}

	other, err := LoadActKey(openTeam(t), r.srv.opt.Seal)
	must(t, err)
	body, mac, _ := strings.Cut(strings.TrimPrefix(tok, "a1."), ".")
	flip := func(s string) string { b := []byte(s); b[len(b)/2] ^= 1; return string(b) }
	late := claim
	late.Item = "r_1/q2"
	for name, bad := range map[string]string{
		"a changed signature": "a1." + body + "." + flip(mac),
		"a changed claim":     "a1." + strings.Split(r.srv.opt.Act.mint(late), ".")[1] + "." + mac,
		"another key's":       other.mint(claim),
		"no version":          body + "." + mac,
		"garbage":             "x",
		"empty":               "",
	} {
		if got, out := act(annC, bad, nil); got != http.StatusForbidden || out["error"] != "token" {
			t.Fatalf("%s: %d %v", name, got, out)
		}
	}
	old := claim
	old.Until = time.Now().Add(-time.Second).Unix()
	if got, out := act(annC, r.srv.opt.Act.mint(old), nil); got != http.StatusGone || out["error"] != "expired" {
		t.Fatalf("expired: %d %v", got, out)
	}
	if got, out := act(bobC, tok, nil); got != http.StatusForbidden || out["error"] != "token" {
		t.Fatalf("ann's token in bob's session: %d %v", got, out)
	}
	if got, _ := act(browser(t), tok, nil); got != http.StatusUnauthorized {
		t.Fatalf("no session: %d", got)
	}
	if got, out := act(annC, tok, map[string]string{"X-Tend": ""}); got != http.StatusForbidden || out["error"] != "csrf" {
		t.Fatalf("without the page's header: %d %v", got, out)
	}
	if got, out := act(annC, tok, map[string]string{"Origin": "https://elsewhere.example"}); got != http.StatusForbidden || out["error"] != "csrf" {
		t.Fatalf("from another site: %d %v", got, out)
	}
	if got := f.taken(); len(got) != 0 {
		t.Fatalf("reached the coordinator: %v", got)
	}

	f.err = &wire.Error{Code: wire.CodeRequestGone, Detail: bob.ID}
	if got, out := act(annC, tok, nil); got != http.StatusConflict || out["error"] != "request_gone" || out["by"] != "Bob" {
		t.Fatalf("handled by bob first: %d %v", got, out)
	}
	f.err = &wire.Error{Code: wire.CodeRequestGone}
	if got, out := act(annC, tok, nil); got != http.StatusConflict || out["error"] != "request_gone" || out["by"] != nil {
		t.Fatalf("no longer asked: %d %v", got, out)
	}
	f.err = &wire.Error{Code: wire.CodeUnauthorized, Detail: "permission"}
	if got, out := act(annC, tok, nil); got != http.StatusForbidden || out["error"] != "unauthorized" {
		t.Fatalf("no longer allowed to: %d %v", got, out)
	}
	f.err = nil
	f.taken()

	seal, err := LoadSealer(r.home)
	must(t, err)
	again, err := LoadActKey(r.team, seal)
	must(t, err)
	if got, err := again.open(tok, time.Now()); err != nil || got != claim {
		t.Fatalf("after a restart: %+v %v", got, err)
	}

	yes := true
	must(t, r.team.SetUser(ann.ID, nil, &yes))
	if got, _ := act(annC, tok, nil); got != http.StatusUnauthorized {
		t.Fatalf("someone disabled, before the directory reads it: %d", got)
	}
	if got := f.taken(); len(got) != 0 {
		t.Fatalf("reached the coordinator: %v", got)
	}
}
