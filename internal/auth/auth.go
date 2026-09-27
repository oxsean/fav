// Package auth signs people in to tend-server with an account elsewhere: a GitHub OAuth App, or any OpenID Connect
// provider (GitLab, Gitea, Keycloak…) by configuration alone. Both use the authorization code flow with state and
// PKCE; the server keeps the client secret and exchanges the code itself.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/tend"
)

// Kinds of login.
const (
	KindGitHub = "github"
	KindOIDC   = "oidc"
)

// Provider is one configured login.
type Provider struct {
	cfg    tend.Login
	secret string
	client *http.Client

	mu        sync.Mutex
	authURL   string
	tokenURL  string
	userURL   string
	emailsURL string // github
	issuer    string
}

// New prepares a provider; an OIDC one reads its discovery document when first used.
func New(cfg tend.Login) (*Provider, error) {
	if cfg.Name == "" || cfg.ClientID == "" || cfg.ClientSecretFile == "" {
		return nil, fmt.Errorf("login %q: name, client_id and client_secret_file are needed", cfg.Name)
	}
	b, err := os.ReadFile(cfg.ClientSecretFile)
	if err != nil {
		return nil, fmt.Errorf("login %s: %w", cfg.Name, err)
	}
	p := &Provider{cfg: cfg, secret: strings.TrimSpace(string(b)), client: &http.Client{Timeout: 15 * time.Second}}
	switch cfg.Kind {
	case KindGitHub:
		base := strings.TrimRight(cfg.BaseURL, "/")
		api := base + "/api/v3"
		if base == "" || base == "https://github.com" {
			base, api = "https://github.com", "https://api.github.com"
		}
		p.issuer, p.authURL, p.tokenURL = base, base+"/login/oauth/authorize", base+"/login/oauth/access_token"
		p.userURL, p.emailsURL = api+"/user", api+"/user/emails"
	case KindOIDC:
		if cfg.Issuer == "" {
			return nil, fmt.Errorf("login %s: issuer is needed", cfg.Name)
		}
	default:
		return nil, fmt.Errorf("login %s: kind %q (github | oidc)", cfg.Name, cfg.Kind)
	}
	return p, nil
}

func (p *Provider) Name() string { return p.cfg.Name }

// Display is how the sign-in page names it.
func (p *Provider) Display() string {
	if p.cfg.Display != "" {
		return p.cfg.Display
	}
	return p.cfg.Name
}

// discover reads an OIDC provider's endpoints once.
func (p *Provider) discover(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.authURL != "" {
		return nil
	}
	var d struct {
		Issuer   string `json:"issuer"`
		Auth     string `json:"authorization_endpoint"`
		Token    string `json:"token_endpoint"`
		UserInfo string `json:"userinfo_endpoint"`
	}
	if err := p.getJSON(ctx, strings.TrimRight(p.cfg.Issuer, "/")+"/.well-known/openid-configuration", "", &d); err != nil {
		return fmt.Errorf("login %s: discovery: %w", p.cfg.Name, err)
	}
	if d.Auth == "" || d.Token == "" || d.UserInfo == "" {
		return fmt.Errorf("login %s: discovery lacks an endpoint", p.cfg.Name)
	}
	p.issuer, p.authURL, p.tokenURL, p.userURL = cmpOr(d.Issuer, p.cfg.Issuer), d.Auth, d.Token, d.UserInfo
	return nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Flow is one sign-in in progress: state binds the callback to the browser that started it, verifier to the code.
type Flow struct {
	State    string
	Verifier string
}

func random() string {
	var b [32]byte
	rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func NewFlow() Flow { return Flow{State: random(), Verifier: random()} }

func challenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// AuthURL is where the browser goes to sign in; the provider sends it back to redirect.
func (p *Provider) AuthURL(ctx context.Context, redirect string, f Flow) (string, error) {
	scope := "read:user user:email"
	if p.cfg.Kind == KindOIDC {
		if err := p.discover(ctx); err != nil {
			return "", err
		}
		scope = "openid email profile"
	}
	q := url.Values{"response_type": {"code"}, "client_id": {p.cfg.ClientID}, "redirect_uri": {redirect}, "scope": {scope},
		"state": {f.State}, "code_challenge": {challenge(f.Verifier)}, "code_challenge_method": {"S256"}}
	sep := "?"
	if strings.Contains(p.authURL, "?") {
		sep = "&"
	}
	return p.authURL + sep + q.Encode(), nil
}

// ErrDenied: the provider refused the code or the account.
var ErrDenied = errors.New("sign-in refused")

// Exchange turns the code the provider sent back into the account that signed in.
func (p *Provider) Exchange(ctx context.Context, redirect, code string, f Flow) (store.Identity, error) {
	if p.cfg.Kind == KindOIDC {
		if err := p.discover(ctx); err != nil {
			return store.Identity{}, err
		}
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {p.cfg.ClientID}, "client_secret": {p.secret}, "code_verifier": {f.Verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return store.Identity{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var tok struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		Error       string `json:"error"`
	}
	if err := p.do(req, &tok); err != nil {
		return store.Identity{}, err
	}
	if tok.AccessToken == "" {
		return store.Identity{}, fmt.Errorf("%w: %s", ErrDenied, cmpOr(tok.Error, "no access token"))
	}
	if p.cfg.Kind == KindGitHub {
		return p.github(ctx, tok.AccessToken)
	}
	return p.userinfo(ctx, tok.AccessToken, tok.IDToken)
}

func (p *Provider) userinfo(ctx context.Context, token, idToken string) (store.Identity, error) {
	var u struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified any    `json:"email_verified"` // a bool, or a string at some providers
		Username      string `json:"preferred_username"`
		Nickname      string `json:"nickname"`
		Name          string `json:"name"`
	}
	if err := p.getJSON(ctx, p.userURL, token, &u); err != nil {
		return store.Identity{}, err
	}
	if u.Sub == "" {
		return store.Identity{}, fmt.Errorf("%w: no subject", ErrDenied)
	}
	verified := u.EmailVerified == true || u.EmailVerified == "true"
	if u.EmailVerified == nil && u.Email != "" {
		verified = p.idTokenVerifies(idToken, u.Sub, u.Email)
	}
	return store.Identity{Provider: p.cfg.Name, Issuer: p.issuer, Subject: u.Sub, Username: cmpOr(u.Username, u.Nickname),
		Email: u.Email, EmailVerified: verified, Name: u.Name}, nil
}

// idTokenVerifies: the ID token says email of sub is verified. Its signature is not checked: it came straight from the
// token endpoint (OIDC Core 3.1.3.7), so it must only be this provider's, for this client, about this subject.
func (p *Provider) idTokenVerifies(idToken, sub, email string) bool {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return false
	}
	var c struct {
		Iss      string `json:"iss"`
		Aud      any    `json:"aud"`
		Sub      string `json:"sub"`
		Email    string `json:"email"`
		Verified any    `json:"email_verified"`
	}
	if json.Unmarshal(b, &c) != nil || c.Sub != sub || strings.TrimRight(c.Iss, "/") != strings.TrimRight(p.issuer, "/") {
		return false
	}
	switch aud := c.Aud.(type) {
	case string:
		if aud != p.cfg.ClientID {
			return false
		}
	case []any:
		if !slices.Contains(aud, any(p.cfg.ClientID)) {
			return false
		}
	default:
		return false
	}
	return (c.Email == "" || strings.EqualFold(c.Email, email)) && (c.Verified == true || c.Verified == "true")
}

func (p *Provider) github(ctx context.Context, token string) (store.Identity, error) {
	var u struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := p.getJSON(ctx, p.userURL, token, &u); err != nil {
		return store.Identity{}, err
	}
	if u.ID == 0 {
		return store.Identity{}, fmt.Errorf("%w: no user id", ErrDenied)
	}
	id := store.Identity{Provider: p.cfg.Name, Issuer: p.issuer, Subject: strconv.FormatInt(u.ID, 10), Username: u.Login, Name: u.Name}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if p.getJSON(ctx, p.emailsURL, token, &emails) == nil {
		for _, e := range emails {
			if e.Primary && e.Verified {
				id.Email, id.EmailVerified = e.Email, true
			}
		}
	}
	return id, nil
}

func (p *Provider) getJSON(ctx context.Context, u, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return p.do(req, out)
}

func (p *Provider) do(req *http.Request, out any) error {
	res, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 400 {
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		json.Unmarshal(b, &e)
		return fmt.Errorf("%w: %s %s: %d %s", ErrDenied, req.Method, req.URL.Path, res.StatusCode,
			strings.TrimSpace(e.Error+" "+e.Description))
	}
	return json.Unmarshal(b, out)
}
