package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/wire"
)

func TestAnAdminOffboardsAMember(t *testing.T) {
	who := map[string]any{"sub": "9", "email": "cy@corp.example", "email_verified": true}
	_, gitea := idp(t, &who)
	r := newRig(t, gitea)
	r.team.AddAdmit(store.Admit{Kind: store.AdmitEmail, Value: "cy@corp.example", Role: store.RoleMember})
	c := browser(t)
	signIn(t, c, r.url, "")
	var added struct{ ID string }
	if got := r.api(c, "POST", "/api/machines", map[string]string{"name": "cy-box"}, &added); got != http.StatusOK {
		t.Fatal(got)
	}
	var me Me
	resp, _ := c.Get(r.url + "/session")
	json.NewDecoder(resp.Body).Decode(&me)
	resp.Body.Close()
	admin := browser(t)
	if login(t, admin, r.url, r.client) != http.StatusNoContent {
		t.Fatal("the host's token signs in")
	}
	if got := r.api(c, "POST", "/api/users/offboard", map[string]string{"user": me.ID}, nil); got != http.StatusForbidden {
		t.Fatalf("a member offboards no one: %d", got)
	}
	if got := r.api(admin, "POST", "/api/users/offboard", map[string]string{"user": me.ID}, nil); got != http.StatusNoContent {
		t.Fatalf("%d", got)
	}
	u, _, _ := r.team.User(me.ID)
	live, _ := r.team.ActiveCredentials()
	for _, x := range live {
		if x.Owner == me.ID {
			t.Fatalf("every credential of cy's ends, the machine's too: %+v", x)
		}
	}
	if !u.Disabled {
		t.Fatal("cy is disabled")
	}
	if owner := r.srv.opt.Dir.MachineOwner("cy-box"); owner != me.ID {
		t.Fatalf("a retired machine keeps its owner: %q", owner)
	}
	var ms coord.Machines
	b, _ := json.Marshal(map[string]any{})
	res, err := r.srv.opt.Coord.HandlerFor(coord.Owner)(context.Background(), &wire.Request{Method: coord.MMachineList, Params: b})
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(res)
	json.Unmarshal(b, &ms)
	if i := slices.IndexFunc(ms.Machines, func(m coord.Machine) bool { return m.Name == "cy-box" }); i < 0 || !ms.Machines[i].Retired || ms.Machines[i].Owner != me.ID {
		t.Fatalf("cy-box retires: %+v", ms.Machines)
	}
}

// "Send a test" posts to the saved webhook at once and says how it went: the status a webhook answered, and only
// "unreachable" for one that never answered.
func TestAWebhookTestSaysHowItWent(t *testing.T) {
	r := newRig(t)
	b := browser(t)
	if login(t, b, r.url, r.client) != http.StatusNoContent {
		t.Fatal("signs in")
	}
	test := func() (int, map[string]any) {
		var out map[string]any
		code := r.api(b, "POST", "/api/me/webhook/test", nil, &out)
		return code, out
	}
	if code, _ := test(); code != http.StatusConflict {
		t.Fatalf("no webhook: %d", code)
	}
	status := http.StatusNoContent
	var got WebhookPayload
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		json.NewDecoder(req.Body).Decode(&got)
		w.WriteHeader(status)
	}))
	defer hook.Close()
	if code := r.api(b, "POST", "/api/me/webhook", map[string]string{"url": hook.URL}, nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	if code, out := test(); code != http.StatusOK || out["ok"] != true || got.Event != "test" || got.Text == "" {
		t.Fatalf("%d %v %+v", code, out, got)
	}
	status = http.StatusNotFound
	if _, out := test(); out["ok"] != false || out["status"] != "404" {
		t.Fatalf("a 404: %v", out)
	}
	hook.Close()
	if _, out := test(); out["ok"] != false || out["status"] != "unreachable" {
		t.Fatalf("gone: %v", out)
	}
}
