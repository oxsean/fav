package server

import (
	"context"
	"encoding/json"
	"net/http"
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
