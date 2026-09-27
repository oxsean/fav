package server

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

func browser(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func login(t *testing.T, c *http.Client, base, token string) int {
	t.Helper()
	resp, err := c.PostForm(base+"/login", url.Values{"token": {token}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestThePageIsServedWithoutLogin(t *testing.T) {
	r := newRig(t)
	resp, err := http.Get(r.url + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), "<html") || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("%d %q %v", resp.StatusCode, b[:min(len(b), 80)], resp.Header)
	}
	if resp, _ := http.Get(r.url + "/nope.js"); resp.StatusCode != 404 {
		t.Fatalf("an unknown file: %d", resp.StatusCode)
	}
}

func TestABrowserLogsInWithAClientToken(t *testing.T) {
	r := newRig(t)
	c := browser(t)
	if got := login(t, c, r.url, r.nodeT); got != http.StatusUnauthorized {
		t.Fatalf("a node token: %d", got)
	}
	if got := login(t, c, r.url, "tend_nope"); got != http.StatusUnauthorized {
		t.Fatalf("a wrong token: %d", got)
	}
	if resp, _ := c.Get(r.url + "/session"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no session yet: %d", resp.StatusCode)
	}
	if got := login(t, c, r.url, r.client); got != http.StatusNoContent {
		t.Fatalf("a client token: %d", got)
	}
	u, _ := url.Parse(r.url)
	ck := c.Jar.Cookies(u)
	if len(ck) != 1 || ck[0].Value == "" {
		t.Fatalf("%v", ck)
	}
	resp, err := c.Get(r.url + "/session")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v %v", resp, err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"id":"local"`) || ck[0].Value == r.client {
		t.Fatalf("%s", b)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, strings.Replace(r.url, "http://", "ws://", 1)+"/client",
		&websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {ck[0].Name + "=" + ck[0].Value}}})
	if err != nil {
		t.Fatalf("the session opens /client: %v", err)
	}
	cl := wire.New(websocket.NetConn(context.Background(), ws, websocket.MessageText), wire.Options{})
	defer cl.Close()
	var st task.State
	if err := cl.Call(ctx, coord.MStateGet, coord.StateParams{NoBriefs: true}, &st); err != nil {
		t.Fatal(err)
	}

	if _, _, err := websocket.Dial(ctx, strings.Replace(r.url, "http://", "ws://", 1)+"/client", &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": {ck[0].Name + "=" + ck[0].Value}, "Origin": {"http://evil.example"}}}); err == nil {
		t.Fatal("another site's page cannot use the session")
	}

	resp, _ = c.Post(r.url+"/logout", "", nil)
	resp.Body.Close()
	if resp, _ := c.Get(r.url + "/session"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout: %d", resp.StatusCode)
	}
}

func TestARevokedTokenEndsTheBrowserSession(t *testing.T) {
	r := newRig(t)
	c := browser(t)
	login(t, c, r.url, r.client)
	cred, _, _ := r.srv.opt.Dir.find(r.client, store.KindToken)
	if err := r.team.Revoke(cred.ID); err != nil {
		t.Fatal(err)
	}
	r.srv.sweep()
	if resp, _ := c.Get(r.url + "/session"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("%d", resp.StatusCode)
	}
}
