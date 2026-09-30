package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// idp is an OIDC provider that signs in whoever *who says, checking the client's secret and PKCE verifier.
func idp(t *testing.T, who *map[string]any) (*httptest.Server, tend.Login) {
	var srv *httptest.Server
	challenges := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": srv.URL, "authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint": srv.URL + "/token", "userinfo_endpoint": srv.URL + "/userinfo"})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		challenges["code1"] = q.Get("code_challenge")
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=code1&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		h := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("client_secret") != "s3cret" || base64.RawURLEncoding.EncodeToString(h[:]) != challenges[r.Form.Get("code")] {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": "at"})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(*who) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	secret := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(secret, []byte("s3cret"), 0o600)
	return srv, tend.Login{Name: "gitea", Kind: "oidc", Issuer: srv.URL, ClientID: "tend", ClientSecretFile: secret}
}

// signIn goes through the provider in a browser and answers where the sign-in ended (the page's #problem, if any).
func signIn(t *testing.T, c *http.Client, base, query string) string {
	t.Helper()
	resp, err := c.Get(base + "/auth/gitea/start" + query)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	_, to, _ := strings.Cut(string(b), `url=`)
	to, _, _ = strings.Cut(to, `"`)
	return to
}

func (r *rig) api(c *http.Client, method, path string, body any, out any) int {
	r.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, r.url+path, bytes.NewReader(b))
	req.Header.Set("X-Tend", "1")
	resp, err := c.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestSigningInWithAProviderNeedsAnAdmission(t *testing.T) {
	who := map[string]any{"sub": "42", "email": "ann@corp.example", "email_verified": true, "preferred_username": "ann", "name": "Ann"}
	_, login := idp(t, &who)
	r := newRig(t, login)
	c := browser(t)
	if to := signIn(t, c, r.url, ""); !strings.HasPrefix(to, "/#signin-not_admitted?") || !strings.Contains(to, "username=ann") {
		t.Fatalf("nobody let ann in: %s", to)
	}
	if err := r.team.AddAdmit(store.Admit{Kind: store.AdmitDomain, Value: "corp.example", Role: store.RoleMember}); err != nil {
		t.Fatal(err)
	}
	if to := signIn(t, c, r.url, ""); to != "/" {
		t.Fatalf("the domain lets ann in: %s", to)
	}
	var me Me
	resp, _ := c.Get(r.url + "/session")
	json.NewDecoder(resp.Body).Decode(&me)
	resp.Body.Close()
	if me.Name != "Ann" || me.Role != store.RoleMember || !strings.HasPrefix(me.ID, "u_") {
		t.Fatalf("%+v", me)
	}

	u, _ := url.Parse(r.url)
	var cookie string
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == sessionCookie {
			cookie = ck.Value
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, strings.Replace(r.url, "http://", "ws://", 1)+"/client",
		&websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {sessionCookie + "=" + cookie}}})
	if err != nil {
		t.Fatal(err)
	}
	cl := wire.New(websocket.NetConn(context.Background(), ws, websocket.MessageText), wire.Options{})
	defer cl.Close()
	var ms coord.Machines
	if err := cl.Call(ctx, coord.MMachineList, coord.MachinesParams{}, &ms); err != nil || len(ms.Machines) != 0 {
		t.Fatalf("a member sees no machine of the host's: %+v %v", ms, err)
	}
	if err := cl.CallCommand(ctx, coord.MProjectCreate, "p", coord.ProjectCreate{Name: "x"}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a member creates no project: %v", err)
	}

	off := true
	if err := r.team.SetUser(me.ID, nil, &off); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cl.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a disabled user keeps their connection")
	}
	if to := signIn(t, browser(t), r.url, ""); to != "/#signin-disabled" {
		t.Fatalf("a disabled user signs in: %s", to)
	}
}

func TestACallbackWithoutItsBrowsersStateIsRefused(t *testing.T) {
	who := map[string]any{"sub": "42", "email": "ann@corp.example", "email_verified": true}
	_, login := idp(t, &who)
	r := newRig(t, login)
	r.team.AddAdmit(store.Admit{Kind: store.AdmitDomain, Value: "corp.example", Role: store.RoleMember})
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(r.url + "/auth/gitea/start")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	to, _ := url.Parse(resp.Header.Get("Location"))
	resp, _ = http.Get(r.url + "/auth/gitea/callback?code=code1&state=" + url.QueryEscape(to.Query().Get("state")))
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "#signin-state") {
		t.Fatalf("a callback in another browser: %d %s", resp.StatusCode, b)
	}
}

func TestAMemberAddsTheirOwnMachine(t *testing.T) {
	who := map[string]any{"sub": "7", "email": "bob@corp.example", "email_verified": true}
	_, login := idp(t, &who)
	r := newRig(t, login)
	r.team.AddAdmit(store.Admit{Kind: store.AdmitEmail, Value: "bob@corp.example", Role: store.RoleMember})
	c := browser(t)
	signIn(t, c, r.url, "")
	if got := r.api(c, "GET", "/api/admits", nil, nil); got != http.StatusForbidden {
		t.Fatalf("a member reads no admission rules: %d", got)
	}
	req, _ := http.NewRequest("POST", r.url+"/api/machines", strings.NewReader(`{"name":"bobs-mac"}`))
	if resp, _ := c.Do(req); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a change without the page's header: %d", resp.StatusCode)
	}
	var added struct{ ID, Token, Command string }
	if got := r.api(c, "POST", "/api/machines", map[string]string{"name": "bobs-mac"}, &added); got != http.StatusOK || added.Token == "" {
		t.Fatalf("%d %+v", got, added)
	}
	var mine []store.Credential
	r.api(c, "GET", "/api/machines", nil, &mine)
	if len(mine) != 1 || mine[0].Name != "bobs-mac" || r.srv.opt.Dir.MachineOwner("bobs-mac") != mine[0].Owner {
		t.Fatalf("bob sees and owns only his machine: %+v", mine)
	}
	if got := r.api(c, "POST", "/api/machines", map[string]string{"name": "n1"}, nil); got != http.StatusConflict {
		t.Fatalf("a machine name taken: %d", got)
	}
}

// The Me page lists the accounts with when each last signed in, and unlinks one of them, never the last.
func TestASignInAccountIsUnlinkedButNotTheLast(t *testing.T) {
	who := map[string]any{"sub": "42", "email": "ann@corp.example", "email_verified": true, "preferred_username": "ann", "name": "Ann"}
	_, login := idp(t, &who)
	r := newRig(t, login)
	r.team.AddAdmit(store.Admit{Kind: store.AdmitDomain, Value: "corp.example", Role: store.RoleMember})
	c := browser(t)
	if to := signIn(t, c, r.url, ""); to != "/" {
		t.Fatal(to)
	}
	var ids []store.Identity
	r.api(c, "GET", "/api/identities", nil, &ids)
	if len(ids) != 1 || ids[0].LastLogin.IsZero() || ids[0].Linked.IsZero() {
		t.Fatalf("%+v", ids)
	}
	oidc := map[string]string{"provider": ids[0].Provider, "issuer": ids[0].Issuer, "subject": ids[0].Subject}
	if got := r.api(c, "DELETE", "/api/identities", oidc, nil); got != http.StatusConflict {
		t.Fatalf("the only account: %d", got)
	}
	var me Me
	r.api(c, "GET", "/session", nil, &me)
	gh := store.Identity{Provider: "github", Issuer: "https://github.com", Subject: "7", Username: "ann-gh"}
	if err := r.team.Link(me.ID, gh); err != nil {
		t.Fatal(err)
	}
	if got := r.api(c, "DELETE", "/api/identities", map[string]string{"provider": "github", "issuer": "https://github.com", "subject": "8"}, nil); got != http.StatusNotFound {
		t.Fatalf("an account that is not theirs: %d", got)
	}
	if got := r.api(c, "DELETE", "/api/identities", oidc, nil); got != http.StatusNoContent {
		t.Fatalf("one of two: %d", got)
	}
	r.api(c, "GET", "/api/identities", nil, &ids)
	if len(ids) != 1 || ids[0].Provider != "github" {
		t.Fatalf("after unlinking: %+v", ids)
	}
	log, _ := r.team.AuditLog(5)
	if log[0].Kind != "unlink" || log[0].Actor != me.ID {
		t.Fatalf("the audit: %+v", log[0])
	}
}
