package server

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/store"
)

func openTeam(t *testing.T) *store.Team {
	t.Helper()
	tm, err := store.OpenTeam(filepath.Join(t.TempDir(), store.File))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tm.Close() })
	return tm
}

// The Web Push key pair is made the first time and the same one comes back after: subscriptions are bound to it.
func TestThePushKeyIsMadeOnceAndKeptSealed(t *testing.T) {
	tm := openTeam(t)
	seal, err := LoadSealer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := LoadPushKey(tm, seal)
	if err != nil {
		t.Fatal(err)
	}
	if pub := a.Public(); len(pub) != 65 || pub[0] != 4 {
		t.Fatalf("not an uncompressed P-256 point: %x", pub)
	}
	b, err := LoadPushKey(tm, seal)
	if err != nil || !bytes.Equal(a.Public(), b.Public()) {
		t.Fatalf("a second load made another key: %v", err)
	}
	kept, _ := tm.KeepSecret(pushKeyName, nil)
	der, _ := x509.MarshalPKCS8PrivateKey(a.priv)
	if bytes.Contains(kept, der) || bytes.Contains(kept, a.priv.D.Bytes()) {
		t.Fatal("the private key is in the database unsealed")
	}
}

// A key another server key sealed cannot be opened: it is left as it is (the right key may come back) and no push
// key is served.
func TestAPushKeyThatCannotBeOpenedIsKeptAndNotReplaced(t *testing.T) {
	tm := openTeam(t)
	home := t.TempDir()
	seal, _ := LoadSealer(home)
	a, err := LoadPushKey(tm, seal)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := LoadSealer(t.TempDir())
	if k, err := LoadPushKey(tm, other); err == nil || k != nil {
		t.Fatalf("another server key opened it: %v", err)
	}
	b, err := LoadPushKey(tm, seal)
	if err != nil || !bytes.Equal(a.Public(), b.Public()) {
		t.Fatalf("the failed load replaced the key: %v", err)
	}
}

func TestThePushKeyIsServedToWhoeverIsSignedIn(t *testing.T) {
	r := newRig(t)
	if got := r.api(browser(t), "GET", "/api/push/key", nil, nil); got != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", got)
	}
	c := browser(t)
	login(t, c, r.url, r.client)
	var out struct{ Key string }
	if got := r.api(c, "GET", "/api/push/key", nil, &out); got != http.StatusOK {
		t.Fatalf("%d", got)
	}
	pub, err := base64.RawURLEncoding.DecodeString(out.Key)
	if err != nil || !bytes.Equal(pub, r.srv.opt.Push.Public()) {
		t.Fatalf("%q %v", out.Key, err)
	}

	none := New(Options{Home: r.home, Coord: r.c, Dir: r.srv.opt.Dir})
	w := httptest.NewRecorder()
	none.pushKey(w, httptest.NewRequest("GET", "/api/push/key", nil), caller{})
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"push_key"`) {
		t.Fatalf("without a key: %d %s", w.Code, w.Body)
	}
}

// A signed-in page registers its browser's subscription and renews it each time it opens, as the same device; one it
// cannot push to is refused; turning push off forgets it.
func TestAPageRegistersItsBrowserForPushAndForgetsIt(t *testing.T) {
	r := newRig(t)
	c := browser(t)
	login(t, c, r.url, r.client)
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	sub := map[string]any{"endpoint": "https://push.example/send/abc", "keys": map[string]string{
		"p256dh": base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes()), "auth": base64.RawURLEncoding.EncodeToString(make([]byte, 16))}}
	var a, b struct{ ID string }
	if got := r.api(c, "PUT", "/api/push/device", map[string]any{"subscription": sub, "name": "Android"}, &a); got != http.StatusOK || a.ID == "" {
		t.Fatalf("%d %+v", got, a)
	}
	if got := r.api(c, "PUT", "/api/push/device", map[string]any{"subscription": sub, "name": "Android"}, &b); got != http.StatusOK || b.ID != a.ID {
		t.Fatalf("renewing made another device: %d %+v", got, b)
	}
	ds, _ := r.team.Devices(store.LocalUser)
	if len(ds) != 1 || ds[0].Name != "Android" || bytes.Contains(ds[0].Target, []byte("push.example")) {
		t.Fatalf("the device, its target sealed: %+v", ds)
	}
	for _, bad := range []map[string]any{{"endpoint": "http://push.example/x", "keys": sub["keys"]}, {"endpoint": "https://push.example/x"}} {
		if got := r.api(c, "PUT", "/api/push/device", map[string]any{"subscription": bad}, nil); got != http.StatusBadRequest {
			t.Fatalf("%v: %d", bad, got)
		}
	}
	if got := r.api(c, "DELETE", "/api/push/device", map[string]string{"endpoint": "https://push.example/send/abc"}, nil); got != http.StatusNoContent {
		t.Fatal(got)
	}
	if ds, _ := r.team.Devices(store.LocalUser); len(ds) != 0 {
		t.Fatalf("still there: %+v", ds)
	}
}

// A person lists their devices, changes what each wants and removes one; someone else's devices are out of their reach.
func TestSomeoneSetsUpTheirOwnDevicesOnly(t *testing.T) {
	r := newRig(t)
	ann, annC := r.member("Ann")
	_, bobC := r.member("Bob")
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	sub := map[string]any{"endpoint": "https://push.example/send/ann", "keys": map[string]string{
		"p256dh": base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes()), "auth": base64.RawURLEncoding.EncodeToString(make([]byte, 16))}}
	var d struct{ ID string }
	if got := r.api(annC, "PUT", "/api/push/device", map[string]any{"subscription": sub, "name": "Android"}, &d); got != http.StatusOK {
		t.Fatal(got)
	}
	prefs := store.DevicePrefs{Events: []string{store.EventWaiting, store.EventDone}, Hide: true, Wait: -1}
	for _, bad := range []store.DevicePrefs{{Events: []string{"task.everything"}}, {Wait: 7200}, {Events: []string{store.EventDone, store.EventDone}}} {
		if got := r.api(annC, "POST", "/api/push/prefs", map[string]any{"id": d.ID, "prefs": bad}, nil); got != http.StatusBadRequest {
			t.Fatalf("%+v: %d", bad, got)
		}
	}
	if got := r.api(bobC, "POST", "/api/push/prefs", map[string]any{"id": d.ID, "prefs": prefs}, nil); got != http.StatusNotFound {
		t.Fatalf("bob sets ann's device: %d", got)
	}
	if got := r.api(annC, "POST", "/api/push/prefs", map[string]any{"id": d.ID, "prefs": prefs}, nil); got != http.StatusNoContent {
		t.Fatal(got)
	}
	var mine, bobs []store.PushDevice
	if got := r.api(annC, "GET", "/api/push/devices", nil, &mine); got != http.StatusOK || len(mine) != 1 || mine[0].Name != "Android" || !reflect.DeepEqual(mine[0].Prefs, prefs) {
		t.Fatalf("%d %+v", got, mine)
	}
	if got := r.api(bobC, "GET", "/api/push/devices", nil, &bobs); got != http.StatusOK || len(bobs) != 0 {
		t.Fatalf("bob sees ann's: %+v", bobs)
	}
	if got := r.api(bobC, "DELETE", "/api/push/devices", map[string]string{"id": d.ID}, nil); got != http.StatusNotFound {
		t.Fatalf("bob removes ann's device: %d", got)
	}
	if got := r.api(annC, "DELETE", "/api/push/devices", map[string]string{"id": d.ID}, nil); got != http.StatusNoContent {
		t.Fatal(got)
	}
	if ds, _ := r.team.Devices(ann.ID); len(ds) != 0 {
		t.Fatalf("still there: %+v", ds)
	}
}
