package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/wire"
)

func TestANoticeReachesItsRecipientsWebhookOnce(t *testing.T) {
	r := newRig(t)
	var mu sync.Mutex
	var got []WebhookPayload
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var p WebhookPayload
		json.NewDecoder(req.Body).Decode(&p)
		mu.Lock()
		got = append(got, p)
		mu.Unlock()
	}))
	defer hook.Close()
	if err := CheckWebhook("ftp://x"); err == nil {
		t.Fatal("only http and https")
	}
	if err := r.team.SetWebhook(store.LocalUser, hook.URL); err != nil {
		t.Fatal(err)
	}
	n := NewNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx, r.team, "https://tend.example/")
	x := coord.Notice{Seq: 3, Event: coord.NotifyTaskWaiting, Task: "t_1", Title: "ship", Reason: "accept", To: []string{store.LocalUser, "u_gone"}, At: time.Now()}
	n.Send(x)
	n.Send(x)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		k := len(got)
		mu.Unlock()
		if k > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].URL != "https://tend.example/#task-t_1" || got[0].Reason != "accept" || got[0].Text == "" {
		t.Fatalf("%+v", got)
	}
}

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
