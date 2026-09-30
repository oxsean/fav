package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/oxsean/fav/internal/tend"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/wire"
)

func startDevice(t *testing.T, r *rig, name string) (deviceCode, userCode string) {
	t.Helper()
	var out struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		VerifyURL  string `json:"verify_url"`
		Interval   int    `json:"interval"`
		ExpiresIn  int    `json:"expires_in"`
	}
	if got := r.api(browser(t), "POST", "/auth/device", map[string]string{"name": name}, &out); got != http.StatusOK {
		t.Fatalf("%d", got)
	}
	if out.DeviceCode == "" || out.UserCode == "" || out.Interval == 0 || out.ExpiresIn == 0 {
		t.Fatalf("%+v", out)
	}
	if !strings.Contains(out.VerifyURL, "#device-"+out.UserCode) {
		t.Fatalf("verify_url does not carry the user code: %s", out.VerifyURL)
	}
	return out.DeviceCode, out.UserCode
}

func poll(t *testing.T, r *rig, deviceCode string) map[string]string {
	t.Helper()
	var out map[string]string
	if got := r.api(browser(t), "POST", "/auth/device/token", map[string]string{"device_code": deviceCode}, &out); got != http.StatusOK {
		t.Fatalf("%d", got)
	}
	return out
}

func TestDeviceLoginIsAllowedOncePolledOnce(t *testing.T) {
	r := newRig(t)
	deviceCode, userCode := startDevice(t, r, "mba")

	if got := poll(t, r, deviceCode); got["status"] != "pending" {
		t.Fatalf("%+v", got)
	}

	anon := browser(t)
	if got := r.api(anon, "GET", "/api/device?code="+userCode, nil, nil); got != http.StatusUnauthorized {
		t.Fatalf("an unauthenticated lookup: %d", got)
	}

	c := browser(t)
	login(t, c, r.url, r.client)
	var info deviceView
	if got := r.api(c, "GET", "/api/device?code="+userCode, nil, &info); got != http.StatusOK || info.Code != userCode || info.Name != "mba" {
		t.Fatalf("%d %+v", got, info)
	}

	if got := r.api(c, "POST", "/api/device", map[string]any{"code": userCode, "allow": true}, nil); got != http.StatusNoContent {
		t.Fatalf("%d", got)
	}

	out := poll(t, r, deviceCode)
	if out["status"] != "ok" || out["token"] == "" || out["user"] == "" {
		t.Fatalf("%+v", out)
	}

	again := poll(t, r, deviceCode)
	if again["status"] == "ok" {
		t.Fatalf("the token is delivered once: %+v", again)
	}

	var tokens []struct {
		Name string `json:"name"`
	}
	r.api(c, "GET", "/api/tokens", nil, &tokens)
	found := false
	for _, tk := range tokens {
		if tk.Name == "login:mba" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no login:mba token among %+v", tokens)
	}
}

func TestDeviceLoginCanBeDenied(t *testing.T) {
	r := newRig(t)
	deviceCode, userCode := startDevice(t, r, "laptop")
	c := browser(t)
	login(t, c, r.url, r.client)
	if got := r.api(c, "POST", "/api/device", map[string]any{"code": userCode, "allow": false}, nil); got != http.StatusNoContent {
		t.Fatalf("%d", got)
	}
	out := poll(t, r, deviceCode)
	if out["status"] != "denied" {
		t.Fatalf("%+v", out)
	}
	if again := poll(t, r, deviceCode); again["status"] != "expired" {
		t.Fatalf("a denied code is gone after it is read once: %+v", again)
	}
}

func TestDeviceLoginExpires(t *testing.T) {
	r := newRig(t)
	deviceCode, _ := startDevice(t, r, "old")
	r.srv.mu.Lock()
	r.srv.devices[deviceCode].expires = time.Now().Add(-time.Minute)
	r.srv.mu.Unlock()
	if out := poll(t, r, deviceCode); out["status"] != "expired" {
		t.Fatalf("%+v", out)
	}
	if got := r.api(browser(t), "POST", "/auth/device/token", map[string]string{"device_code": "nope"}, nil); got != http.StatusOK {
		t.Fatalf("an unknown device code: %d", got)
	}
}

func TestInviteInfoIsPublic(t *testing.T) {
	r := newRig(t)
	secret, err := r.team.NewInvite("member", "local", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Inviter string `json:"inviter"`
		Role    string `json:"role"`
	}
	if got := r.api(browser(t), "GET", "/auth/invite?code="+secret, nil, &info); got != http.StatusOK || info.Role != "member" || info.Inviter == "" {
		t.Fatalf("%d %+v", got, info)
	}
	if got := r.api(browser(t), "GET", "/auth/invite?code=nope", nil, nil); got != http.StatusNotFound {
		t.Fatalf("an unknown invite: %d", got)
	}
	expired, _ := r.team.NewInvite("member", "local", -time.Minute)
	if got := r.api(browser(t), "GET", "/auth/invite?code="+expired, nil, nil); got != http.StatusNotFound {
		t.Fatalf("an expired invite: %d", got)
	}
}

// An invitation to a project makes whoever signs in with it a member there, with the access it names.
func TestAProjectInviteMakesItsInviteeAMember(t *testing.T) {
	who := map[string]any{"sub": "11", "email": "eve@else.example", "email_verified": true}
	_, gitea := idp(t, &who)
	r := newRig(t, gitea)
	b, _ := json.Marshal(coord.ProjectCreate{ID: "p1", Name: "One", Owner: store.LocalUser})
	if _, err := r.c.HandlerFor(coord.Owner)(context.Background(), &wire.Request{Method: coord.MProjectCreate, CommandID: "p", Params: b}); err != nil {
		t.Fatal(err)
	}
	admin := browser(t)
	if login(t, admin, r.url, r.client) != http.StatusNoContent {
		t.Fatal("the host's token signs in")
	}
	for _, bad := range []map[string]string{{"role": "member", "project": "nope", "access": "reader"}, {"role": "member", "project": "p1", "access": "owner"},
		{"role": "member", "project": "p1"}} {
		if got := r.api(admin, "POST", "/api/invites", bad, nil); got != http.StatusBadRequest {
			t.Fatalf("%v: %d", bad, got)
		}
	}
	var made struct{ URL string }
	if got := r.api(admin, "POST", "/api/invites", map[string]string{"role": "member", "project": "p1", "access": "reader"}, &made); got != http.StatusOK {
		t.Fatal(got)
	}
	_, secret, _ := strings.Cut(made.URL, "#invite-")
	var info struct{ Project, Access string }
	if got := r.api(browser(t), "GET", "/auth/invite?code="+secret, nil, &info); got != http.StatusOK || info.Project != "One" || info.Access != "reader" {
		t.Fatalf("the sign-in page names the project: %d %+v", got, info)
	}
	c := browser(t)
	signIn(t, c, r.url, "?invite="+secret)
	var me Me
	resp, err := c.Get(r.url + "/session")
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&me)
	resp.Body.Close()
	if me.ID == "" || r.c.State().Projects["p1"].Members[me.ID] != "reader" {
		t.Fatalf("eve reads p1: %+v %v", me, r.c.State().Projects["p1"].Members)
	}
}

// ask posts body to path the way a page (X-Tend, same origin) or anything else would: extra sets or, with "", drops
// headers. It answers the status, the JSON and the cookies set.
func ask(t *testing.T, r *rig, c *http.Client, path string, body any, extra map[string]string) (int, map[string]any, []string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", r.url+path, bytes.NewReader(b))
	req.Header.Set("X-Tend", "1")
	req.Header.Set("Origin", r.url)
	for k, v := range extra {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out, resp.Header.Values("Set-Cookie")
}

func startSession(t *testing.T, r *rig, phone *http.Client, name string) (deviceCode, userCode string) {
	t.Helper()
	got, out, _ := ask(t, r, phone, "/auth/device", map[string]any{"name": name, "session": true}, nil)
	if got != http.StatusOK || out["device_code"] == nil || !strings.Contains(out["verify_url"].(string), "#device-") {
		t.Fatalf("%d %v", got, out)
	}
	return out["device_code"].(string), out["user_code"].(string)
}

// A phone (a home-screen app keeps its own cookies) signs in by a device code someone allows elsewhere: its poll
// comes back with a browser session of its own, never a token, once.
func TestADeviceCodeSignsTheAskingBrowserIn(t *testing.T) {
	r := newRig(t)
	phone := browser(t)
	deviceCode, userCode := startSession(t, r, phone, "Android")
	if got, out, cookies := ask(t, r, phone, "/auth/device/token", map[string]string{"device_code": deviceCode}, nil); got != 200 || out["status"] != "pending" || len(cookies) > 0 {
		t.Fatalf("%d %v %v", got, out, cookies)
	}

	desk := browser(t)
	login(t, desk, r.url, r.client)
	var info struct {
		Code    string `json:"code"`
		Name    string `json:"name"`
		Session bool   `json:"session"`
	}
	if got := r.api(desk, "GET", "/api/device?code="+userCode, nil, &info); got != 200 || !info.Session || info.Name != "Android" {
		t.Fatalf("the allowing page is told it signs a browser in: %d %+v", got, info)
	}
	var before []store.Credential
	r.api(desk, "GET", "/api/tokens", nil, &before)
	if got := r.api(desk, "POST", "/api/device", map[string]any{"code": userCode, "allow": true}, nil); got != http.StatusNoContent {
		t.Fatal(got)
	}

	got, out, cookies := ask(t, r, phone, "/auth/device/token", map[string]string{"device_code": deviceCode}, nil)
	if got != 200 || out["status"] != "ok" || out["token"] != nil || out["user"] == "" {
		t.Fatalf("%d %v", got, out)
	}
	if len(cookies) != 1 || !strings.HasPrefix(cookies[0], sessionCookie+"=") || !strings.Contains(cookies[0], "HttpOnly") || !strings.Contains(cookies[0], "SameSite=Strict") {
		t.Fatalf("the session cookie: %v", cookies)
	}
	var me Me
	if got := r.api(phone, "GET", "/session", nil, &me); got != 200 || me.ID != store.LocalUser {
		t.Fatalf("the phone is signed in as who allowed it: %d %+v", got, me)
	}
	var mine []struct {
		Kind, Name string
		Current    bool
	}
	r.api(phone, "GET", "/api/tokens", nil, &mine)
	found := false
	for _, c := range mine {
		found = found || c.Current && c.Kind == store.KindWeb && c.Name == "device:Android"
	}
	if !found {
		t.Fatalf("the phone's session is a browser session named for it: %+v", mine)
	}
	var after []store.Credential
	r.api(desk, "GET", "/api/tokens", nil, &after)
	for _, c := range after {
		if c.Kind == store.KindToken && !slices.ContainsFunc(before, func(b store.Credential) bool { return b.ID == c.ID }) {
			t.Fatalf("allowing a browser made a token: %+v", c)
		}
	}

	if got, out, cookies := ask(t, r, browser(t), "/auth/device/token", map[string]string{"device_code": deviceCode}, nil); out["status"] != "expired" || len(cookies) > 0 {
		t.Fatalf("the session is given once: %d %v %v", got, out, cookies)
	}
}

// Only this server's own page asks for or takes a session: another site can neither start one nor have a browser
// receive one (it would sign the browser in as someone else), and a refused poll leaves the code as it was.
func TestADeviceSessionIsOnlyForThisServersPage(t *testing.T) {
	r := newRig(t)
	phone := browser(t)
	for _, h := range []map[string]string{{"X-Tend": ""}, {"Origin": "https://else.example"}} {
		if got, _, _ := ask(t, r, phone, "/auth/device", map[string]any{"name": "x", "session": true}, h); got != http.StatusForbidden {
			t.Fatalf("a session started with %v: %d", h, got)
		}
	}
	deviceCode, userCode := startSession(t, r, phone, "iPhone")
	desk := browser(t)
	login(t, desk, r.url, r.client)
	r.api(desk, "POST", "/api/device", map[string]any{"code": userCode, "allow": true}, nil)
	for _, h := range []map[string]string{{"X-Tend": ""}, {"Origin": "https://else.example"}} {
		if got, out, cookies := ask(t, r, phone, "/auth/device/token", map[string]string{"device_code": deviceCode}, h); got != http.StatusForbidden || len(cookies) > 0 {
			t.Fatalf("a poll with %v: %d %v %v", h, got, out, cookies)
		}
	}
	if _, out, cookies := ask(t, r, phone, "/auth/device/token", map[string]string{"device_code": deviceCode}, nil); out["status"] != "ok" || len(cookies) != 1 {
		t.Fatalf("the refused polls used the code up: %v %v", out, cookies)
	}
}

// A terminal's code stays a token however it is polled: asking for a session at the poll changes nothing.
func TestATerminalsDeviceCodeNeverSetsACookie(t *testing.T) {
	r := newRig(t)
	deviceCode, userCode := startDevice(t, r, "mba")
	desk := browser(t)
	login(t, desk, r.url, r.client)
	var info struct{ Session bool }
	r.api(desk, "GET", "/api/device?code="+userCode, nil, &info)
	if info.Session {
		t.Fatal("a terminal's code is shown as a browser sign-in")
	}
	r.api(desk, "POST", "/api/device", map[string]any{"code": userCode, "allow": true}, nil)
	got, out, cookies := ask(t, r, browser(t), "/auth/device/token", map[string]any{"device_code": deviceCode, "session": true}, nil)
	if got != 200 || out["status"] != "ok" || out["token"] == "" || len(cookies) > 0 {
		t.Fatalf("%d %v %v", got, out, cookies)
	}
}

// Someone disabled between allowing a browser and its poll gives it no session.
func TestADeviceSessionIsNotGivenForSomeoneDisabled(t *testing.T) {
	r := newRig(t)
	r.team.AddAdmit(store.Admit{Kind: store.AdmitEmail, Value: "bob@corp.example", Role: store.RoleMember})
	bob, err := r.team.Admit(store.Identity{Provider: "gitea", Issuer: "https://git.example", Subject: "7", Email: "bob@corp.example", EmailVerified: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	secret, _, _ := r.team.NewCredential(store.KindToken, "cli", bob.ID, 0)
	r.srv.opt.Dir.Reload()
	desk := browser(t)
	if login(t, desk, r.url, secret) != http.StatusNoContent {
		t.Fatal("bob signs in")
	}
	phone := browser(t)
	deviceCode, userCode := startSession(t, r, phone, "Android")
	if got := r.api(desk, "POST", "/api/device", map[string]any{"code": userCode, "allow": true}, nil); got != http.StatusNoContent {
		t.Fatal(got)
	}
	yes := true
	r.team.SetUser(bob.ID, nil, &yes)
	if _, out, cookies := ask(t, r, phone, "/auth/device/token", map[string]string{"device_code": deviceCode}, nil); out["status"] != "denied" || len(cookies) > 0 {
		t.Fatalf("%v %v", out, cookies)
	}
}

// One address holds at most a few device codes at once, so it cannot take every place; the places are still bounded
// for everyone together.
func TestDeviceCodesAreBoundedPerAddress(t *testing.T) {
	r := newRig(t)
	start := func(ip string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/auth/device", strings.NewReader(`{"name":"x"}`))
		req.RemoteAddr = ip + ":40000"
		r.srv.deviceStart(rec, req)
		return rec.Code
	}
	for i := range maxDevicesPerIP {
		if got := start("203.0.113.7"); got != http.StatusOK {
			t.Fatalf("code %d: %d", i+1, got)
		}
	}
	if got := start("203.0.113.7"); got != http.StatusTooManyRequests {
		t.Fatalf("one more from the same address: %d", got)
	}
	if got := start("203.0.113.8"); got != http.StatusOK {
		t.Fatalf("another address: %d", got)
	}
	for i := 0; len(r.srv.devices) < maxDevices; i++ {
		if got := start(fmt.Sprintf("198.51.100.%d", i)); got != http.StatusOK {
			t.Fatalf("filling up: %d", got)
		}
	}
	if got := start("192.0.2.1"); got != http.StatusTooManyRequests {
		t.Fatalf("beyond every address's places: %d", got)
	}
}

// Behind a proxy on this host (tailscale serve), every request comes from a loopback address: the address a request
// came from is the last one the proxy put in X-Forwarded-For, and only a loopback peer is believed.
func TestTheAddressBehindAProxyOnThisHostIsTheOneItForwarded(t *testing.T) {
	for _, c := range []struct{ remote, xff, want string }{
		{"127.0.0.1:5000", "100.64.0.9", "100.64.0.9"},
		{"[::1]:5000", "100.64.0.9", "100.64.0.9"},
		{"127.0.0.1:5000", "198.51.100.1, 100.64.0.9", "100.64.0.9"},
		{"127.0.0.1:5000", "", "127.0.0.1"},
		{"127.0.0.1:5000", "not an address", "127.0.0.1"},
		{"203.0.113.7:5000", "100.64.0.9", "203.0.113.7"},
		{"[2001:db8::1]:5000", "127.0.0.1", "2001:db8::1"},
	} {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := New(Options{}).clientIP(req); got != c.want {
			t.Errorf("%s with X-Forwarded-For %q: %q, want %q", c.remote, c.xff, got, c.want)
		}
	}

	r := newRig(t)
	start := func(ip string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/auth/device", strings.NewReader(`{"name":"x"}`))
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("X-Forwarded-For", ip)
		r.srv.deviceStart(rec, req)
		return rec.Code
	}
	for i := range maxDevicesPerIP {
		if got := start("100.64.0.9"); got != http.StatusOK {
			t.Fatalf("code %d: %d", i+1, got)
		}
	}
	if got := start("100.64.0.10"); got != http.StatusOK {
		t.Fatalf("another address behind the same proxy: %d", got)
	}
}

// A forwarder in front of a container reaches it from the container network's address: with that network among
// server.trusted_proxies, the address is the one it forwarded, past every hop of a trusted proxy; without it, the
// forwarder's own; and a trusted list leaves loopback out unless it names it.
func TestTheTrustedProxiesAreTheConfiguredOnes(t *testing.T) {
	bridge := New(Options{Config: tend.ServerConfig{TrustedProxies: []string{"172.16.0.0/12", "127.0.0.1/32"}}})
	for _, c := range []struct {
		s                 *Server
		remote, xff, want string
	}{
		{bridge, "172.17.0.1:5000", "100.64.0.9", "100.64.0.9"},
		{bridge, "172.17.0.1:5000", "198.51.100.1, 100.64.0.9, 172.17.0.5", "100.64.0.9"},
		{bridge, "127.0.0.1:5000", "100.64.0.9, 172.17.0.1", "100.64.0.9"},
		{bridge, "172.17.0.1:5000", "172.17.0.2, 172.17.0.3", "172.17.0.2"},
		{bridge, "[::1]:5000", "100.64.0.9", "::1"},
		{New(Options{}), "172.17.0.1:5000", "100.64.0.9", "172.17.0.1"},
	} {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = c.remote
		req.Header.Set("X-Forwarded-For", c.xff)
		if got := c.s.clientIP(req); got != c.want {
			t.Errorf("%s with X-Forwarded-For %q: %q, want %q", c.remote, c.xff, got, c.want)
		}
	}
}

// A browser sign-in asked for from another address than the page allowing it is one someone may have sent a link to:
// the page is told, shows the code without its last four, and allowing it takes those four typed again; the name the
// asker gave is cut to 64 characters without control or format characters.
func TestADeviceSessionAskedFromElsewhereNeedsItsCodeTypedAgain(t *testing.T) {
	r := newRig(t)
	phone := browser(t)
	name := "Your iPhone‮\x07\n" + strings.Repeat("长", 80)
	got, out, _ := ask(t, r, phone, "/auth/device", map[string]any{"name": name, "session": true}, map[string]string{"X-Forwarded-For": "100.64.0.9"})
	if got != http.StatusOK {
		t.Fatal(got, out)
	}
	userCode := out["user_code"].(string)
	desk := browser(t)
	login(t, desk, r.url, r.client)
	var info struct {
		Code    string `json:"code"`
		Name    string `json:"name"`
		Confirm bool   `json:"confirm"`
	}
	if got := r.api(desk, "GET", "/api/device?code="+userCode, nil, &info); got != 200 || !info.Confirm || info.Code != userCode[:5]+"····" {
		t.Fatalf("%d %+v", got, info)
	}
	if n := []rune(info.Name); len(n) != 64 || strings.ContainsAny(info.Name, "‮\x07\n") || !strings.HasPrefix(info.Name, "Your iPhone长") {
		t.Fatalf("the name: %q", info.Name)
	}
	var e map[string]string
	for _, typed := range []string{"", "ZZZZ", userCode} {
		if got := r.api(desk, "POST", "/api/device", map[string]any{"code": userCode, "allow": true, "confirm": typed}, &e); got != http.StatusBadRequest || e["error"] != "confirm" {
			t.Fatalf("typed %q: %d %v", typed, got, e)
		}
	}
	if got := r.api(desk, "POST", "/api/device", map[string]any{"code": userCode, "allow": true, "confirm": strings.ToLower(userCode[5:])}, nil); got != http.StatusNoContent {
		t.Fatalf("with its last four: %d", got)
	}

	_, out, _ = ask(t, r, phone, "/auth/device", map[string]any{"name": "x", "session": true}, map[string]string{"X-Forwarded-For": "100.64.0.9"})
	if got := r.api(desk, "POST", "/api/device", map[string]any{"code": out["user_code"], "allow": false}, nil); got != http.StatusNoContent {
		t.Fatalf("denying takes nothing typed: %d", got)
	}
	_, cliCode := startDevice(t, r, "laptop")
	var term map[string]any
	if got := r.api(desk, "GET", "/api/device?code="+cliCode, nil, &term); got != 200 || term["confirm"] != nil {
		t.Fatalf("a terminal's code: %v", term)
	}
}

// Browser sign-ins are counted apart from terminals': one address holding its five of them still starts a
// `tend login`, and so does everyone once all browser sign-ins are taken.
func TestDeviceSessionsAreBoundedApartFromTerminals(t *testing.T) {
	r := newRig(t)
	start := func(ip string, session bool) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/auth/device", strings.NewReader(fmt.Sprintf(`{"name":"x","session":%t}`, session)))
		req.RemoteAddr = ip + ":40000"
		req.Host = "tend.test"
		req.Header.Set("X-Tend", "1")
		req.Header.Set("Origin", "http://tend.test")
		r.srv.deviceStart(rec, req)
		return rec.Code
	}
	for i := range maxDevicesPerIP {
		if got := start("203.0.113.7", true); got != http.StatusOK {
			t.Fatalf("session %d: %d", i+1, got)
		}
	}
	if start("203.0.113.7", true) != http.StatusTooManyRequests || start("203.0.113.7", false) != http.StatusOK {
		t.Fatal("an address's browser sign-ins and terminals' are apart")
	}
	for i := 0; ; i++ {
		if got := start(fmt.Sprintf("198.51.100.%d", i), true); got == http.StatusTooManyRequests {
			break
		} else if got != http.StatusOK || i > maxSessionDevices {
			t.Fatalf("filling up: %d at %d", got, i)
		}
	}
	if start("192.0.2.1", false) != http.StatusOK {
		t.Fatal("a terminal still starts")
	}
}
