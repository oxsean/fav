package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/oxsean/fav/internal/tend"
)

// fakeIdP is an OIDC and GitHub-shaped provider that hands out one code for the client whose verifier matches.
func fakeIdP(t *testing.T) *httptest.Server { return fakeOIDC(t, nil, nil) }

// fakeOIDC answers userinfo with info (default: a verified ann) and adds an ID token with claims made by idClaims.
func fakeOIDC(t *testing.T, info map[string]any, idClaims func(issuer string) map[string]any) *httptest.Server {
	if info == nil {
		info = map[string]any{"sub": "42", "email": "ann@example.com", "email_verified": true, "preferred_username": "ann"}
	}
	var srv *httptest.Server
	codes := map[string]string{} // code → challenge
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": srv.URL, "authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint": srv.URL + "/token", "userinfo_endpoint": srv.URL + "/userinfo"})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		codes["c1"] = q.Get("code_challenge")
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=c1&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	token := func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("client_secret") != "s3cret" || challenge(r.Form.Get("code_verifier")) != codes[r.Form.Get("code")] {
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		tok := map[string]string{"access_token": "at"}
		if idClaims != nil {
			b, _ := json.Marshal(idClaims(srv.URL))
			tok["id_token"] = "e30." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
		}
		json.NewEncoder(w).Encode(tok)
	}
	mux.HandleFunc("/token", token)
	mux.HandleFunc("/login/oauth/access_token", token)
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(info)
	})
	mux.HandleFunc("/api/v3/user", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": 7, "login": "octo", "name": "Octo"})
	})
	mux.HandleFunc("/api/v3/user/emails", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"email": "old@example.com", "verified": true}, {"email": "octo@example.com", "primary": true, "verified": true}})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func signIn(t *testing.T, p *Provider) (string, error) {
	t.Helper()
	ctx := context.Background()
	f := NewFlow()
	u, err := p.AuthURL(ctx, "http://tend.test/auth/x/callback", f)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	back, _ := url.Parse(res.Header.Get("Location"))
	if back.Query().Get("state") != f.State {
		t.Fatalf("the state comes back: %s", back)
	}
	id, err := p.Exchange(ctx, "http://tend.test/auth/x/callback", back.Query().Get("code"), f)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(id)
	return string(b), nil
}

func secretFile(t *testing.T, s string) string {
	p := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(p, []byte(s+"\n"), 0o600)
	return p
}

func TestAnOIDCProviderSignsInBySubject(t *testing.T) {
	idp := fakeIdP(t)
	p, err := New(tend.Login{Name: "gitea", Kind: KindOIDC, Issuer: idp.URL, ClientID: "tend", ClientSecretFile: secretFile(t, "s3cret")})
	if err != nil {
		t.Fatal(err)
	}
	got, err := signIn(t, p)
	want := `{"provider":"gitea","issuer":"` + idp.URL + `","subject":"42","username":"ann","email":"ann@example.com","email_verified":true}`
	if err != nil || got != want {
		t.Fatalf("%s %v\nwant %s", got, err, want)
	}
}

func TestGitHubSignsInWithItsPrimaryVerifiedEmail(t *testing.T) {
	idp := fakeIdP(t)
	p, err := New(tend.Login{Name: "gh", Kind: KindGitHub, BaseURL: idp.URL, ClientID: "tend", ClientSecretFile: secretFile(t, "s3cret")})
	if err != nil {
		t.Fatal(err)
	}
	p.authURL = idp.URL + "/authorize"
	got, err := signIn(t, p)
	want := `{"provider":"gh","issuer":"` + idp.URL + `","subject":"7","username":"octo","email":"octo@example.com","email_verified":true,"name":"Octo"}`
	if err != nil || got != want {
		t.Fatalf("%s %v\nwant %s", got, err, want)
	}
}

func TestAWrongSecretOrVerifierIsRefused(t *testing.T) {
	idp := fakeIdP(t)
	p, _ := New(tend.Login{Name: "gitea", Kind: KindOIDC, Issuer: idp.URL, ClientID: "tend", ClientSecretFile: secretFile(t, "wrong")})
	if _, err := signIn(t, p); err == nil {
		t.Fatal("a wrong client secret signed in")
	}
}

func TestTheIDTokenVouchesForAnEmailUserinfoLeavesUnverified(t *testing.T) {
	info := map[string]any{"sub": "42", "email": "ann@example.com", "preferred_username": "ann"}
	for _, c := range []struct {
		name   string
		claims func(issuer string) map[string]any
		want   bool
	}{
		{"its own", func(iss string) map[string]any {
			return map[string]any{"iss": iss + "/", "aud": "tend", "sub": "42", "email": "ann@example.com", "email_verified": true}
		}, true},
		{"audience list", func(iss string) map[string]any {
			return map[string]any{"iss": iss, "aud": []string{"other", "tend"}, "sub": "42", "email_verified": true}
		}, true},
		{"none", nil, false},
		{"another client's", func(iss string) map[string]any {
			return map[string]any{"iss": iss, "aud": "other", "sub": "42", "email_verified": true}
		}, false},
		{"another subject's", func(iss string) map[string]any {
			return map[string]any{"iss": iss, "aud": "tend", "sub": "43", "email_verified": true}
		}, false},
		{"another issuer's", func(string) map[string]any {
			return map[string]any{"iss": "https://elsewhere.test", "aud": "tend", "sub": "42", "email_verified": true}
		}, false},
		{"another email's", func(iss string) map[string]any {
			return map[string]any{"iss": iss, "aud": "tend", "sub": "42", "email": "old@example.com", "email_verified": true}
		}, false},
		{"unverified", func(iss string) map[string]any {
			return map[string]any{"iss": iss, "aud": "tend", "sub": "42", "email_verified": false}
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			idp := fakeOIDC(t, info, c.claims)
			p, err := New(tend.Login{Name: "gitea", Kind: KindOIDC, Issuer: idp.URL, ClientID: "tend", ClientSecretFile: secretFile(t, "s3cret")})
			if err != nil {
				t.Fatal(err)
			}
			got, err := signIn(t, p)
			if err != nil {
				t.Fatal(err)
			}
			var id struct {
				Email    string `json:"email"`
				Verified bool   `json:"email_verified"`
			}
			json.Unmarshal([]byte(got), &id)
			if id.Email != "ann@example.com" || id.Verified != c.want {
				t.Fatalf("%s: want verified %v", got, c.want)
			}
		})
	}
}
