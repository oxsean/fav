package server

import (
	"net/http"
	"strings"
	"testing"
	"time"
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
