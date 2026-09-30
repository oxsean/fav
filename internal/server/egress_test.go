package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/store"
)

func TestEgressConnectsOnlyToPublicAddresses(t *testing.T) {
	e, err := NewEgress(nil, "100.101.8.10:7788")
	if err != nil {
		t.Fatal(err)
	}
	for a, ok := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true, "100.64.0.9": false, "100.127.255.254": false, "100.128.0.1": true,
		"fd7a:115c:a1e0::1": false, "64:ff9b::6440:9": false,
		"127.0.0.1": false, "::1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.5": false, "169.254.169.254": false,
		"fe80::1": false, "fd00::1": false, "0.0.0.0": false, "::": false, "224.0.0.1": false, "255.255.255.255": false,
		"::ffff:127.0.0.1": false, "64:ff9b::7f00:1": false, "64:ff9b::808:808": true, "100.101.8.10": false,
	} {
		if e.permits(netip.MustParseAddr(a)) != ok {
			t.Errorf("%s: want %t", a, ok)
		}
	}
	lan, _ := NewEgress([]string{"10.0.0.0/8", "127.0.0.1/32"}, "127.0.0.1:1")
	if !lan.permits(netip.MustParseAddr("10.9.9.9")) || !lan.permits(netip.MustParseAddr("127.0.0.1")) || lan.permits(netip.MustParseAddr("192.168.0.1")) {
		t.Error("the allow list takes its prefixes in, and only them")
	}
	tailnet, _ := NewEgress([]string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"}, "")
	if !tailnet.permits(netip.MustParseAddr("100.100.1.2")) || !tailnet.permits(netip.MustParseAddr("fd7a:115c:a1e0::5")) {
		t.Error("the allow list takes the tailnet in")
	}
	if _, err := NewEgress([]string{"10.0.0.0"}, ""); err == nil {
		t.Error("a prefix without its length is refused")
	}
}

// hits is a server that counts its requests and answers status, with Location to for a redirect.
func hits(t *testing.T, status int, to string) (*httptest.Server, *atomic.Int32) {
	n := &atomic.Int32{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if to != "" {
			w.Header().Set("Location", to)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(s.Close)
	return s, n
}

// A member's webhook cannot reach the server's own host or its networks, nor be redirected there: the test answers
// unreachable, and nothing arrives.
func TestAWebhookTestReachesNoPrivateAddress(t *testing.T) {
	r := newRig(t)
	strict, _ := NewEgress(nil, "127.0.0.1:0")
	r.srv.opt.Egress = strict
	b := browser(t)
	if login(t, b, r.url, r.client) != http.StatusNoContent {
		t.Fatal("signs in")
	}
	inside, got := hits(t, http.StatusNoContent, "")
	if code := r.api(b, "POST", "/api/me/webhook", map[string]string{"url": inside.URL}, nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	var out map[string]any
	if r.api(b, "POST", "/api/me/webhook/test", nil, &out); out["ok"] != false || out["status"] != "unreachable" || got.Load() != 0 {
		t.Fatalf("a loopback webhook: %v, %d requests", out, got.Load())
	}

	loop, _ := NewEgress([]string{"127.0.0.0/8"}, "127.0.0.1:0")
	r.srv.opt.Egress = loop
	jump, _ := hits(t, http.StatusFound, inside.URL+"/elsewhere")
	if code := r.api(b, "POST", "/api/me/webhook", map[string]string{"url": jump.URL}, nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	if r.api(b, "POST", "/api/me/webhook/test", nil, &out); out["ok"] != false || out["status"] != "302" || got.Load() != 0 {
		t.Fatalf("a redirect is not followed to another host: %v, %d requests", out, got.Load())
	}
}

// The outbox's webhooks go through the same rules.
func TestTheOutboxReachesNoPrivateAddress(t *testing.T) {
	o := newOutbox(t)
	inside, got := hits(t, http.StatusNoContent, "")
	strict, _ := NewEgress(nil, "127.0.0.1:0")
	n := NewNotifier()
	n.now = o.clock.now
	n.attach(NotifyOptions{Team: o.team, Coord: o.coord, Seal: o.seal, Egress: strict})
	u := store.LocalUser
	must(t, o.team.SetWebhook(u, inside.URL))
	n.Send(coord.Notice{Seq: 1, Event: coord.NotifyTaskDone, Task: "t1", Title: "x", To: []string{u}, At: n0})
	n.dispatch(context.Background())
	n.wg.Wait()
	if got.Load() != 0 {
		t.Fatal("a webhook on loopback is not posted to")
	}
	if left := o.rows(); len(left) != 0 {
		t.Fatalf("a refused address is not tried again: %+v", left)
	}
}

// A tracker on an address the server does not reach is not read.
func TestTheSyncReachesNoPrivateTracker(t *testing.T) {
	r := newSyncRig(t)
	strict, _ := NewEgress(nil, "127.0.0.1:0")
	r.s.UseClient(strict.Client(time.Second))
	r.g.Open(1, "Ship it", "", "tend")
	r.pass(time.Minute)
	if r.task(1) != nil || len(r.g.Requests) != 0 {
		t.Fatalf("the tracker is not read: %v", r.g.Requests)
	}
	if x := r.tracker(); x.LastError == "" {
		t.Fatalf("the binding says why: %+v", x)
	}
}

// What a failed delivery or webhook test records is a class of error, never the text of one: a redirect whose
// Location the client cannot read would put the address, a push endpoint's secret path, in it.
func TestAFailureIsRecordedAsItsClassAlone(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Location"] = []string{"https://[::1%25s3cret/fcm/send/s3cret"}
		w.WriteHeader(http.StatusFound)
	}))
	defer bad.Close()
	loop, _ := NewEgress([]string{"127.0.0.0/8"}, "127.0.0.1:0")
	err := postWebhook(context.Background(), loop.Client(time.Second), bad.URL+"/s3cret", WebhookPayload{})
	if err == nil {
		t.Fatal("a Location it cannot read fails")
	}
	for _, e := range []error{err, errEgress, context.DeadlineExceeded, &sendError{status: 503}, nil} {
		if r := resultOf(e); !slices.Contains(resultClasses, r) && !(len(r) == 3 && r[0] >= '1' && r[0] <= '5') || strings.Contains(r, "s3cret") {
			t.Errorf("%v: %q", e, r)
		}
	}
	strict, _ := NewEgress(nil, "")
	_, derr := strict.Client(time.Second).Get("https://127.0.0.1:1/x")
	if resultOf(derr) != "refused" {
		t.Errorf("refused by the rules: %q", resultOf(derr))
	}
	_, derr = loop.Client(time.Second).Get("http://127.0.0.1:1/x")
	if resultOf(derr) != "connect" {
		t.Errorf("nothing listening: %q", resultOf(derr))
	}
	// ⚠️ A resolver that answers every name (a proxy's fake-IP DNS) never fails a real lookup, so the error is built.
	derr = &url.Error{Op: "Get", URL: "http://no-such-host.invalid/x", Err: &net.OpError{Op: "dial", Net: "tcp",
		Err: &net.DNSError{Err: "no such host", Name: "no-such-host.invalid", IsNotFound: true}}}
	if resultOf(derr) != "dns" {
		t.Errorf("no such host: %q", resultOf(derr))
	}
	tlsSrv := httptest.NewTLSServer(http.NotFoundHandler())
	defer tlsSrv.Close()
	_, derr = loop.Client(time.Second).Get(tlsSrv.URL)
	if resultOf(derr) != "tls" {
		t.Errorf("an unknown certificate: %q", resultOf(derr))
	}
}
