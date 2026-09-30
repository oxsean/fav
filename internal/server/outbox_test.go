package server

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/task"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var n0 = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// waits is a coordinator that answers what the test says waits on whom.
type waits struct {
	mu     sync.Mutex
	items  map[string]coord.InboxItem // user + " " + task
	hidden map[string]bool            // user + " " + task they may no longer see
	asks   map[string]agent.Request   // run + "/" + request
	denies map[string]bool            // user + " " + item they may deny from a notice
	counts map[string]int             // user → how many things wait on them
}

func newWaits() *waits {
	return &waits{items: map[string]coord.InboxItem{}, hidden: map[string]bool{}, asks: map[string]agent.Request{}, denies: map[string]bool{}, counts: map[string]int{}}
}

func (f *waits) WaitingCount(user string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[user]
}

func (f *waits) NoticeActs(user, id string, item task.Pending) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.denies[user+" "+item.ID] {
		return []string{coord.ActDeny}
	}
	return nil
}

func (f *waits) ID() string { return "c_test" }

func (f *waits) Waiting(user, id string) (coord.InboxItem, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	it, ok := f.items[user+" "+id]
	return it, ok
}

func (f *waits) Sees(user, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.hidden[user+" "+id]
}

func (f *waits) Request(run, id string) (agent.Request, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.asks[run+"/"+id]
	return r, ok
}

// wait says task id waits on user for items now.
func (f *waits) wait(user, id string, items ...task.Pending) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(items) == 0 {
		delete(f.items, user+" "+id)
		return
	}
	f.items[user+" "+id] = coord.InboxItem{Task: id, Title: "drop the old table", Project: "infra", Pending: items}
}

type clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.at = t; c.mu.Unlock() }

// pushed is a push the fake push service took, read as its browser reads it.
type pushed struct {
	path    string
	headers http.Header
	msg     PushMessage
	hook    *WebhookPayload
}

// services is one TLS server standing in for the push services and the webhooks: each path answers what the test
// set for it (201 by default), and /slow waits until the test lets it go.
type services struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	got     []pushed
	answers map[string][]int // path → the statuses it answers in turn; the last one stays
	after   map[string]string
	keys    map[string]*ecdh.PrivateKey
	auths   map[string][]byte
	release chan struct{}
}

func newServices(t *testing.T) *services {
	s := &services{t: t, answers: map[string][]int{}, after: map[string]string{}, keys: map[string]*ecdh.PrivateKey{}, auths: map[string][]byte{},
		release: make(chan struct{})}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/slow" {
			select {
			case <-s.release:
			case <-r.Context().Done():
				return
			}
		}
		p := pushed{path: r.URL.Path, headers: r.Header}
		s.mu.Lock()
		key, auth := s.keys[r.URL.Path], s.auths[r.URL.Path]
		status := http.StatusCreated
		if as := s.answers[r.URL.Path]; len(as) > 0 {
			status = as[0]
			if len(as) > 1 {
				s.answers[r.URL.Path] = as[1:]
			}
		}
		if a := s.after[r.URL.Path]; a != "" {
			w.Header().Set("Retry-After", a)
		}
		s.mu.Unlock()
		if key != nil {
			json.Unmarshal(decryptPush(t, key, auth, body), &p.msg)
		} else {
			p.hook = &WebhookPayload{}
			json.Unmarshal(body, p.hook)
		}
		s.mu.Lock()
		s.got = append(s.got, p)
		s.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(func() { close(s.release); s.srv.Close() })
	return s
}

func (s *services) answer(path string, statuses ...int) {
	s.mu.Lock()
	s.answers[path] = statuses
	s.mu.Unlock()
}

// taken is what came since the last call.
func (s *services) taken() []pushed {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.got
	s.got = nil
	return out
}

// outbox is a Notifier on a team's database, its clock and its services.
type outbox struct {
	t     *testing.T
	team  *store.Team
	seal  *Sealer
	push  *PushKey
	act   *ActKey
	coord *waits
	clock *clock
	svc   *services
	n     *Notifier
}

func newOutbox(t *testing.T) *outbox {
	o := &outbox{t: t, team: openTeam(t), coord: newWaits(), clock: &clock{at: n0}, svc: newServices(t)}
	var err error
	if o.seal, err = LoadSealer(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if o.push, err = LoadPushKey(o.team, o.seal); err != nil {
		t.Fatal(err)
	}
	if o.act, err = LoadActKey(o.team, o.seal); err != nil {
		t.Fatal(err)
	}
	o.n = o.notifier()
	return o
}

// notifier is a fresh Notifier on the same outbox: what a restart makes.
func (o *outbox) notifier() *Notifier {
	n := NewNotifier()
	n.now, n.client = o.clock.now, o.svc.srv.Client()
	n.attach(NotifyOptions{Team: o.team, Coord: o.coord, Seal: o.seal, Push: o.push, Act: o.act, Base: "https://tend.example/"})
	return n
}

// device subscribes a browser for user whose push service is at path.
func (o *outbox) device(user, path string) store.PushDevice {
	o.t.Helper()
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)
	sub := webSubscription{Endpoint: o.svc.srv.URL + path}
	sub.Keys.P256dh = base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes())
	sub.Keys.Auth = base64.RawURLEncoding.EncodeToString(auth)
	o.svc.mu.Lock()
	o.svc.keys[path], o.svc.auths[path] = ua, auth
	o.svc.mu.Unlock()
	d, err := keepDevice(o.team, o.seal, user, "Phone", sub, o.clock.now())
	if err != nil {
		o.t.Fatal(err)
	}
	return d
}

// drain delivers everything due by the clock, waiting for each round.
func (o *outbox) drain() {
	for o.n.dispatch(context.Background()) > 0 {
		o.n.wg.Wait()
	}
}

func (o *outbox) rows() map[string]store.Delivery {
	o.t.Helper()
	out := map[string]store.Delivery{}
	due, err := o.team.Due(n0.Add(1000*time.Hour), 100)
	if err != nil {
		o.t.Fatal(err)
	}
	for _, d := range due {
		out[d.Device] = d
	}
	return out
}

var (
	permission = task.Pending{ID: "r_1/q1", Kind: task.PendPermission, Task: "t_1", Run: "r_1", Request: "q1", Version: 7}
	question   = task.Pending{ID: "r_1/q2", Kind: task.PendQuestion, Task: "t_1", Run: "r_1", Request: "q2", Version: 7}
)

func needs(seq int64, items ...task.Pending) coord.Notice {
	return coord.Notice{Seq: seq, Event: coord.NotifyTaskWaiting, Task: "t_1", Title: "drop the old table", Project: "infra",
		Reason: task.AttentionPermission, Run: "r_1", Items: items, To: []string{store.LocalUser}, At: n0}
}

// A notice goes to the webhook at once and to the phone once the page had its time; what the outbox holds when the
// server stops goes out after it starts again, and so do the notices that came before the outbox was there.
func TestAnUndeliveredOutboxGoesOutAfterARestart(t *testing.T) {
	o := newOutbox(t)
	must(t, o.team.SetWebhook(store.LocalUser, o.svc.srv.URL+"/hook"))
	o.device(store.LocalUser, "/push/phone")
	o.coord.wait(store.LocalUser, "t_1", permission)
	o.coord.asks["r_1/q1"] = agent.Request{ID: "q1", Kind: agent.RequestPermission, Tool: "Bash", Summary: "psql -c 'DROP TABLE orders_old'"}
	o.n.Send(needs(812, permission))
	o.n.Send(needs(812, permission))
	o.drain()
	got := o.svc.taken()
	if len(got) != 1 || got[0].hook == nil || got[0].hook.URL != "https://tend.example/#task-t_1" || got[0].hook.Text == "" {
		t.Fatalf("the webhook at once, once: %+v", got)
	}
	o.clock.set(n0.Add(29 * time.Second))
	o.drain()
	if got := o.svc.taken(); len(got) != 0 {
		t.Fatalf("pushed before the page had its time: %+v", got)
	}

	o.n = o.notifier() // the server stopped and started again
	o.clock.set(n0.Add(30 * time.Second))
	o.drain()
	got = o.svc.taken()
	if len(got) != 1 || got[0].path != "/push/phone" {
		t.Fatalf("the push after the restart: %+v", got)
	}
	m, h := got[0].msg, got[0].headers
	want := PushMessage{V: 1, Server: "c_test", Seq: 812, Event: coord.NotifyTaskWaiting, Task: "t_1", Item: "r_1/q1", Kind: task.PendPermission,
		Title: "drop the old table", What: "psql -c 'DROP TABLE orders_old'", Project: "infra", N: 1, Link: "#task-t_1/r-r_1", At: n0}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("the message\n got %+v\nwant %+v", m, want)
	}
	if h.Get("Content-Encoding") != "aes128gcm" || h.Get("TTL") != "86400" || h.Get("Urgency") != "high" || h.Get("Topic") != "t_1" ||
		!strings.HasPrefix(h.Get("Authorization"), "vapid t=") {
		t.Fatalf("headers %v", h)
	}
	for _, d := range o.rows() {
		t.Fatalf("still to go: %+v", d)
	}

	early := NewNotifier()
	early.now, early.client = o.clock.now, o.svc.srv.Client()
	early.Send(needs(813, permission))
	o.n = early
	early.attach(NotifyOptions{Team: o.team, Coord: o.coord, Seal: o.seal, Push: o.push})
	o.drain()
	if got := o.svc.taken(); len(got) != 2 || (got[0].hook == nil) == (got[1].hook == nil) {
		t.Fatalf("a notice from before the outbox was there, to the webhook and the phone: %+v", got)
	}
}

// A push service that no longer knows a subscription (404, 410) takes the device away, with what was still to go to
// it; the person's other devices get theirs.
func TestAGoneSubscriptionTakesItsDevice(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		o := newOutbox(t)
		old := o.device(store.LocalUser, "/push/old")
		o.device(store.LocalUser, "/push/new")
		o.coord.wait(store.LocalUser, "t_1", question)
		o.svc.answer("/push/old", status)
		o.n.Send(needs(5, question))
		o.clock.set(n0.Add(time.Minute))
		o.drain()
		if got := o.svc.taken(); len(got) != 2 {
			t.Fatalf("%d: %+v", status, got)
		}
		if _, ok, _ := o.team.Device(old.ID); ok {
			t.Fatalf("%d: the device stayed", status)
		}
		o.n.Send(needs(6, question))
		o.clock.set(n0.Add(2 * time.Minute))
		o.drain()
		if got := o.svc.taken(); len(got) != 1 || got[0].path != "/push/new" {
			t.Fatalf("%d: after the device went: %+v", status, got)
		}
	}
}

// A push service that fails is tried again three times, 30 s, 2 min and 8 min apart, or as long as it asks when that
// is longer, and then no more; one that refuses the message for good is not tried again.
func TestAFailedPushIsTriedAgainLaterAndThenGivenUp(t *testing.T) {
	o := newOutbox(t)
	dev := o.device(store.LocalUser, "/push/down")
	o.coord.wait(store.LocalUser, "t_1", question)
	o.svc.answer("/push/down", http.StatusServiceUnavailable)
	o.n.Send(needs(5, question))
	at := n0.Add(time.Minute)
	for i, wait := range []time.Duration{30 * time.Second, 2 * time.Minute, 8 * time.Minute} {
		o.clock.set(at)
		o.drain()
		d := o.rows()[dev.ID]
		if d.Status != store.DeliveryPending || d.Attempts != i+1 || !d.Next.Equal(at.Add(wait)) || d.Result != "503" {
			t.Fatalf("after try %d: %+v", i+1, d)
		}
		o.clock.set(at.Add(wait - time.Second))
		o.drain()
		at = at.Add(wait)
	}
	o.clock.set(at)
	o.drain()
	if got := o.svc.taken(); len(got) != 4 {
		t.Fatalf("tries: %d", len(got))
	}
	if _, ok := o.rows()[dev.ID]; ok {
		t.Fatal("tried a fifth time")
	}
	if d, _, _ := o.team.Device(dev.ID); d.Failures != 4 {
		t.Fatalf("failures %d", d.Failures)
	}

	o.svc.answer("/push/down", http.StatusTooManyRequests)
	o.svc.after["/push/down"] = "600"
	o.n.Send(needs(6, question))
	o.clock.set(at.Add(time.Hour))
	o.drain()
	if d := o.rows()[dev.ID]; !d.Next.Equal(at.Add(time.Hour + 10*time.Minute)) {
		t.Fatalf("a service that asks to wait 10 minutes: %+v", d)
	}
	o.svc.answer("/push/down", http.StatusOK)
	o.clock.set(at.Add(2 * time.Hour))
	o.drain()
	if d, _, _ := o.team.Device(dev.ID); d.Failures != 0 || !d.LastOK.Equal(at.Add(2*time.Hour)) {
		t.Fatalf("after it went through: %+v", d)
	}

	o.svc.answer("/push/down", http.StatusRequestEntityTooLarge)
	o.n.Send(needs(7, question))
	o.clock.set(at.Add(3 * time.Hour))
	o.drain()
	if _, ok := o.rows()[dev.ID]; ok {
		t.Fatal("a message refused for good is tried again")
	}
}

// The push waits 30 s for a permission and 60 s for anything else; handled in that time, it never goes. One that is
// replaced before it goes gives way to the new one, which counts what waits now.
func TestAPushWaitsForThePageAndGoesNowhereOnceHandled(t *testing.T) {
	o := newOutbox(t)
	o.device(store.LocalUser, "/push/phone")
	o.coord.wait(store.LocalUser, "t_1", question)
	o.n.Send(needs(5, question))
	o.clock.set(n0.Add(59 * time.Second))
	o.drain()
	if got := o.svc.taken(); len(got) != 0 {
		t.Fatalf("a question pushed within its minute: %+v", got)
	}
	o.coord.wait(store.LocalUser, "t_1")
	o.clock.set(n0.Add(time.Minute))
	o.drain()
	if got := o.svc.taken(); len(got) != 0 {
		t.Fatalf("pushed what was answered on the page: %+v", got)
	}

	o.coord.wait(store.LocalUser, "t_1", question)
	o.n.Send(needs(6, question))
	replaced := question
	replaced.ID, replaced.Request = "r_1/q3", "q3"
	o.coord.wait(store.LocalUser, "t_1", replaced, permission)
	x := needs(7, replaced, permission)
	x.At = n0.Add(10 * time.Second)
	o.n.Send(x)
	o.clock.set(n0.Add(40 * time.Second))
	o.drain()
	got := o.svc.taken()
	if len(got) != 1 || got[0].msg.Seq != 7 || got[0].msg.Item != "r_1/q3" || got[0].msg.N != 2 {
		t.Fatalf("the permission's 30 s: %+v", got)
	}
	o.clock.set(n0.Add(2 * time.Minute))
	o.drain()
	if got := o.svc.taken(); len(got) != 0 {
		t.Fatalf("the replaced question went too: %+v", got)
	}
}

// A slow push service holds up only its own device.
func TestASlowPushServiceHoldsUpOnlyItsOwnDevice(t *testing.T) {
	o := newOutbox(t)
	o.device(store.LocalUser, "/slow")
	o.device(store.LocalUser, "/push/fast")
	o.coord.wait(store.LocalUser, "t_1", question)
	o.n.Send(needs(5, question))
	o.clock.set(n0.Add(time.Minute))
	o.n.dispatch(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := o.svc.taken()
		if len(got) == 1 && got[0].path == "/push/fast" {
			break
		}
		if len(got) > 0 || time.Now().After(deadline) {
			t.Fatalf("while one service hangs: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := o.n.dispatch(context.Background()); n != 0 {
		t.Fatalf("a second delivery to the hanging device: %d", n)
	}
	o.svc.release <- struct{}{}
	o.n.wg.Wait()
}

// Nothing goes to someone disabled, nor about a task they may no longer see.
func TestNoticesStopForSomeoneGone(t *testing.T) {
	o := newOutbox(t)
	must(t, o.team.AddAdmit(store.Admit{Kind: store.AdmitEmail, Value: "ann@corp.example", Role: store.RoleMember}))
	ann, err := o.team.Admit(store.Identity{Provider: "gitea", Issuer: "https://git.example", Subject: "1", Email: "ann@corp.example", EmailVerified: true}, "")
	must(t, err)
	must(t, o.team.SetWebhook(ann.ID, o.svc.srv.URL+"/hook"))
	o.coord.wait(ann.ID, "t_1", question)
	x := needs(5, question)
	x.To = []string{ann.ID}
	o.n.Send(x)
	done := coord.Notice{Seq: 6, Event: coord.NotifyTaskDone, Task: "t_2", Title: "t2", To: []string{ann.ID}, At: n0}
	o.n.Send(done)
	o.coord.hidden[ann.ID+" t_2"] = true
	yes := true
	must(t, o.team.SetUser(ann.ID, nil, &yes))
	o.drain()
	if got := o.svc.taken(); len(got) != 0 {
		t.Fatalf("to someone disabled: %+v", got)
	}
	no := false
	must(t, o.team.SetUser(ann.ID, nil, &no))
	o.n.Send(coord.Notice{Seq: 7, Event: coord.NotifyTaskDone, Task: "t_2", Title: "t2", To: []string{ann.ID}, At: n0})
	o.drain()
	if got := o.svc.taken(); len(got) != 0 {
		t.Fatalf("about a task out of sight: %+v", got)
	}
}

// A push about a permission its recipient may deny carries a button for it, whose token names them, the item at its
// version and the device; anything else carries none.
func TestAPushCarriesADenyOnlyForWhoMayDeny(t *testing.T) {
	o := newOutbox(t)
	dev := o.device(store.LocalUser, "/push/phone")
	o.coord.wait(store.LocalUser, "t_1", permission, question)
	o.n.Send(needs(5, question))
	o.clock.set(n0.Add(time.Minute))
	o.drain()
	o.coord.wait(store.LocalUser, "t_1", permission)
	o.n.Send(needs(6, permission))
	o.clock.set(n0.Add(2 * time.Minute))
	o.drain()
	o.coord.denies[store.LocalUser+" "+permission.ID] = true
	o.n.Send(needs(7, permission))
	o.clock.set(n0.Add(3 * time.Minute))
	o.drain()
	got := o.svc.taken()
	if len(got) != 3 || len(got[0].msg.Actions) != 0 || len(got[1].msg.Actions) != 0 {
		t.Fatalf("a question, a permission they may not deny: %+v", got)
	}
	acts := got[2].msg.Actions
	if len(acts) != 1 || acts[0].Action != "reject" {
		t.Fatalf("%+v", acts)
	}
	c, err := o.act.open(acts[0].Token, o.clock.now())
	want := actClaim{User: store.LocalUser, Task: "t_1", Item: permission.ID, Version: permission.Version, Action: coord.ActDeny, Seq: 7, Device: dev.ID,
		Until: n0.Add(3*time.Minute + actLife).Unix()}
	if err != nil || c != want {
		t.Fatalf("the token\n got %+v %v\nwant %+v", c, err, want)
	}
}

// What a device wants shapes what it gets: the events it takes, how long a push waits for the page (a task done goes
// at once), and a lock screen that says only how many things wait.
func TestADevicesSettingsShapeItsPushes(t *testing.T) {
	o := newOutbox(t)
	plain := o.device(store.LocalUser, "/push/plain")
	hidden := o.device(store.LocalUser, "/push/hidden")
	late := o.device(store.LocalUser, "/push/late")
	for id, p := range map[string]store.DevicePrefs{hidden.ID: {Events: []string{store.EventWaiting, store.EventDone}, Hide: true, Wait: -1},
		late.ID: {Events: []string{store.EventDone, store.EventWaiting}, Wait: 300}} {
		if ok, err := o.team.SetDevicePrefs(store.LocalUser, id, p); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	o.coord.wait(store.LocalUser, "t_1", permission)
	o.coord.denies[store.LocalUser+" "+permission.ID] = true
	o.coord.counts[store.LocalUser] = 3
	o.n.Send(needs(5, permission))
	o.drain()
	got := o.svc.taken()
	want := PushMessage{V: 1, Server: "c_test", Seq: 5, Event: coord.NotifyTaskWaiting, N: 3}
	if len(got) != 1 || got[0].path != "/push/hidden" || !reflect.DeepEqual(got[0].msg, want) || got[0].headers.Get("Topic") != "tend" {
		t.Fatalf("at once, saying only how many: %+v", got)
	}
	o.clock.set(n0.Add(30 * time.Second))
	o.drain()
	if got := o.svc.taken(); len(got) != 1 || got[0].path != "/push/plain" || got[0].msg.Title == "" || len(got[0].msg.Actions) != 1 {
		t.Fatalf("the default wait: %+v", got)
	}
	o.clock.set(n0.Add(299 * time.Second))
	o.drain()
	if got := o.svc.taken(); len(got) != 0 {
		t.Fatalf("before its five minutes: %+v", got)
	}
	o.clock.set(n0.Add(300 * time.Second))
	o.drain()
	if got := o.svc.taken(); len(got) != 1 || got[0].path != "/push/late" {
		t.Fatalf("after them: %+v", got)
	}

	done := coord.Notice{Seq: 6, Event: coord.NotifyTaskDone, Task: "t_2", Title: "ship it", Project: "infra", To: []string{store.LocalUser}, At: o.clock.now()}
	o.n.Send(done)
	o.drain()
	got = o.svc.taken()
	paths := map[string]PushMessage{}
	for _, p := range got {
		paths[p.path] = p.msg
	}
	if len(got) != 2 || !reflect.DeepEqual(paths["/push/late"], PushMessage{V: 1, Server: "c_test", Seq: 6, Event: coord.NotifyTaskDone, Task: "t_2", Title: "ship it",
		Project: "infra", Link: "#task-t_2", At: done.At}) || paths["/push/hidden"].Title != "" || paths["/push/hidden"].Event != coord.NotifyTaskDone {
		t.Fatalf("a task done, at once, to the devices that take it: %+v", got)
	}
	if _, ok := paths["/push/plain"]; ok || plain.ID == "" {
		t.Fatal("a device that does not take it")
	}
}
