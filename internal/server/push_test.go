package server

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
