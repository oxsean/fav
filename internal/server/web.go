package server

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/auth"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/skin"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

//go:embed web
var webFiles embed.FS

// sessionCookie holds a browser's session: HttpOnly so the page's scripts never see it, SameSite=Strict so no other
// site's page sends it. It is a secret of its own, never a personal token. Over https it is secureCookie: a
// __Host- cookie only this host sets, for all its paths.
const (
	sessionCookie = "tend_session"
	secureCookie  = "__Host-" + sessionCookie
)

// cookieName is the session cookie's name for r.
func (s *Server) cookieName(r *http.Request) string {
	if s.secure(r) {
		return secureCookie
	}
	return sessionCookie
}

// flowCookie binds a sign-in's callback to the browser that started it. Lax: the provider's redirect back is a
// navigation from another site.
const flowCookie = "tend_flow"

// sessionAge is how long a browser stays signed in; revoking the session, or disabling its user, ends it sooner.
const sessionAge = 30 * 24 * time.Hour

const flowAge = 10 * time.Minute

// page serves the Web UI's files.
func page() http.Handler {
	sub, _ := fs.Sub(webFiles, "web")
	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SecureHeaders(w)
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}

// SecureHeaders are the headers every page and file of the Web UI goes out with; tools/webpreview serves under them too.
func SecureHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; manifest-src 'self'; worker-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
}

func (s *Server) webRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/login", s.limited(s.login))
	mux.HandleFunc("/logout", s.logout)
	mux.HandleFunc("/session", s.session)
	mux.HandleFunc("GET /auth/logins", s.loginList)
	mux.HandleFunc("GET /auth/{name}/start", s.limited(s.start))
	mux.HandleFunc("GET /auth/{name}/callback", s.limited(s.callback))
	mux.HandleFunc("GET /auth/invite", s.limited(s.inviteInfo))
	mux.HandleFunc("POST /auth/device", s.limited(s.deviceStart))
	mux.HandleFunc("POST /auth/device/token", s.limited(s.devicePoll))
	mux.HandleFunc("GET /theme/{file}", theme)
	s.pwaRoutes(mux)
}

// theme serves a skin as a stylesheet, /theme/<name>.css with the name as skin.Named reads it, and the presets for the
// settings page, /theme/presets.json.
func theme(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("file") == "presets.json" {
		SecureHeaders(w)
		writeJSON(w, http.StatusOK, skin.Presets)
		return
	}
	name, ok := strings.CutSuffix(r.PathValue("file"), ".css")
	sk, err := skin.Named(name)
	if !ok || err != nil {
		http.NotFound(w, r)
		return
	}
	SecureHeaders(w)
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, sk.CSS())
}

// secure: cookies need the Secure flag (TLS here, or a public https address behind a proxy that ends it).
func (s *Server) secure(r *http.Request) bool {
	return r.TLS != nil || strings.HasPrefix(s.opt.Config.PublicURL, "https://")
}

// sameOrigin: a state-changing request comes from this server's own page (a missing Origin is a non-browser client);
// one a browser says another site sent (Sec-Fetch-Site) never does.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// startSession signs the browser in as u with a new session credential; name ties it to a parent token.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u store.User, name string) error {
	secret, c, err := s.team().NewSession(name, u.ID, r.UserAgent(), sessionAge)
	if err != nil {
		return err
	}
	s.opt.Dir.Reload()
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(r), Value: secret, Path: "/", MaxAge: int(sessionAge.Seconds()),
		HttpOnly: true, Secure: s.secure(r), SameSite: http.SameSiteStrictMode})
	s.audit(r, u.ID, "login", c.ID)
	return nil
}

// login takes a personal token from the sign-in form and starts a browser session with it.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !sameOrigin(r) {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	cred, u, ok := s.opt.Dir.find(strings.TrimSpace(r.PostFormValue("token")), store.KindToken)
	if !ok {
		s.audit(r, "", "login_refused", "token")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if err := s.startSession(w, r, u, viaToken+cred.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !sameOrigin(r) {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	if c, _, ok := s.credential(r, RoleClient); ok && c.Kind == store.KindWeb {
		s.team().Revoke(c.ID)
		s.sweep()
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(r), Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: s.secure(r), SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

// Me is who a session belongs to.
type Me struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Email    string    `json:"email,omitempty"`
	Username string    `json:"username,omitempty"`
	Role     string    `json:"role"`
	Session  string    `json:"session"`         // this credential's id
	Joined   time.Time `json:"joined,omitzero"` // when they were let in; the server host has none
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	c, u, ok := s.credential(r, RoleClient)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, Me{ID: u.ID, Name: displayName(u), Email: u.Email, Username: u.Username, Role: u.Role, Session: c.ID, Joined: u.Created})
}

func displayName(u store.User) string {
	for _, n := range []string{u.Name, u.Username, u.Email, u.ID} {
		if n != "" {
			return n
		}
	}
	return ""
}

type loginInfo struct {
	Name    string `json:"name"`
	Display string `json:"display"`
}

// loginList is the ways to sign in, for the sign-in page.
func (s *Server) loginList(w http.ResponseWriter, r *http.Request) {
	out := []loginInfo{}
	for _, l := range s.opt.Config.Logins {
		if p := s.logins[l.Name]; p != nil {
			out = append(out, loginInfo{Name: p.Name(), Display: p.Display()})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// inviteInfo is what the sign-in page shows for an invitation before anyone signs in: who sent it, the role, the
// project it makes them a member of and with what access, and its expiry.
func (s *Server) inviteInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.team().Invite(r.URL.Query().Get("code"))
	if err != nil || info.Used || time.Now().After(info.Expires) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	inviter := info.CreatedBy
	if u, ok, _ := s.team().User(info.CreatedBy); ok {
		inviter = displayName(u)
	}
	out := map[string]any{"inviter": inviter, "role": info.Role, "expires": info.Expires}
	if pr := s.opt.Coord.State().Projects[info.Project]; pr != nil {
		out["project"], out["access"] = pr.Name, info.Access
	}
	writeJSON(w, http.StatusOK, out)
}

// joinInvited makes u, just admitted through invite, a member of the project the invitation names, as whoever sent
// it; a failure leaves them signed in without it and goes to the audit.
func (s *Server) joinInvited(r *http.Request, u store.User, invite string) {
	info, err := s.team().Invite(invite)
	if err != nil || info.Project == "" || info.UsedBy != u.ID {
		return
	}
	by, ok, _ := s.team().User(info.CreatedBy)
	if !ok {
		return
	}
	s.opt.Dir.Reload()
	b, _ := json.Marshal(task.MemberSet{Project: info.Project, User: u.ID, Role: info.Access})
	req := &wire.Request{Method: coord.MProjectMember, CommandID: "invite-" + u.ID, Params: b}
	if _, err := s.opt.Coord.HandlerFor(principal(by))(r.Context(), req); err != nil {
		s.audit(r, info.CreatedBy, "invite.project_failed", u.ID+" "+info.Project+": "+wire.Code(err))
		return
	}
	s.audit(r, info.CreatedBy, "member", info.Project+" "+u.ID+" "+info.Access)
}

// flow is a sign-in waiting for its callback.
type flow struct {
	auth.Flow
	login   string
	invite  string
	link    string // the user adding this account to theirs
	expires time.Time
}

// base is how the browser reaches this server: the configured public address, else the request's own.
func (s *Server) base(r *http.Request) string {
	if s.opt.Config.PublicURL != "" {
		return strings.TrimRight(s.opt.Config.PublicURL, "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) redirectURL(r *http.Request, name string) string {
	return s.base(r) + "/auth/" + url.PathEscape(name) + "/callback"
}

// start sends the browser to a provider; ?invite= carries an invitation, ?link=1 adds the account to the signed-in
// user instead.
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	p := s.logins[r.PathValue("name")]
	if p == nil {
		http.NotFound(w, r)
		return
	}
	f := flow{Flow: auth.NewFlow(), login: p.Name(), invite: r.URL.Query().Get("invite"), expires: time.Now().Add(flowAge)}
	if r.URL.Query().Get("link") != "" {
		_, u, ok := s.credential(r, RoleClient)
		if !ok {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}
		f.link = u.ID
	}
	to, err := p.AuthURL(r.Context(), s.redirectURL(r, p.Name()), f.Flow)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tend-server:", err)
		s.signinPage(w, http.StatusBadGateway, "provider")
		return
	}
	s.mu.Lock()
	s.flows[f.State] = f
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: flowCookie, Value: f.State, Path: "/auth/", MaxAge: int(flowAge.Seconds()),
		HttpOnly: true, Secure: s.secure(r), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, to, http.StatusFound)
}

func (s *Server) expireFlows() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, f := range s.flows {
		if time.Now().After(f.expires) {
			delete(s.flows, k)
		}
	}
}

// callback finishes a sign-in: the state must be the one this browser started, the account one the team admits.
func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	c, err := r.Cookie(flowCookie)
	state := q.Get("state")
	s.mu.Lock()
	f, ok := s.flows[state]
	delete(s.flows, state)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: flowCookie, Value: "", Path: "/auth/", MaxAge: -1, HttpOnly: true, Secure: s.secure(r), SameSite: http.SameSiteLaxMode})
	if err != nil || !ok || c.Value != state || f.login != r.PathValue("name") || time.Now().After(f.expires) || q.Get("code") == "" {
		s.audit(r, "", "login_refused", "state")
		s.signinPage(w, http.StatusBadRequest, "state")
		return
	}
	p := s.logins[f.login]
	id, err := p.Exchange(r.Context(), s.redirectURL(r, f.login), q.Get("code"), f.Flow)
	if err != nil {
		s.audit(r, "", "login_refused", f.login+": "+err.Error())
		s.signinPage(w, http.StatusUnauthorized, "provider")
		return
	}
	if f.link != "" {
		err := s.team().Link(f.link, id)
		s.audit(r, f.link, "link", id.Provider+":"+id.Subject)
		if err != nil {
			s.signinPage(w, http.StatusConflict, "linked")
			return
		}
		s.signinPage(w, http.StatusOK, "")
		return
	}
	u, err := s.team().Admit(id, f.invite)
	if err != nil {
		s.audit(r, "", "login_refused", id.Provider+":"+id.Subject+" "+id.Username+": "+err.Error())
		code := "not_admitted"
		if errors.Is(err, store.ErrDisabled) {
			code = "disabled"
		} else {
			// The refusal page (§10 "被拒绝") names the account so the person knows what to switch or ask about.
			q := url.Values{"provider": {id.Provider}, "username": {id.Username}, "email": {id.Email}}
			if id.EmailVerified {
				q.Set("verified", "1")
			}
			code += "?" + q.Encode()
		}
		s.signinPage(w, http.StatusForbidden, code)
		return
	}
	if f.invite != "" {
		s.joinInvited(r, u, f.invite)
	}
	if err := s.startSession(w, r, u, ""); err != nil {
		s.signinPage(w, http.StatusInternalServerError, "internal")
		return
	}
	s.signinPage(w, http.StatusOK, "")
}

// signinPage ends a sign-in by moving to the page from this site: a Strict session cookie set in the provider's
// redirect is sent only on a navigation this site starts. A problem goes along as #signin-<code>.
func (s *Server) signinPage(w http.ResponseWriter, status int, problem string) {
	to := "/"
	if problem != "" {
		to = "/#signin-" + problem
	}
	SecureHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=%s"><a href="%s">tend</a>`, to, to)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// limiter bounds sign-in attempts per address: a burst, refilled at a steady rate.
type limiter struct {
	mu   sync.Mutex
	seen map[string]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

const (
	limitBurst = 20
	limitRate  = 20.0 / 60 // per second
)

func newLimiter() *limiter { return &limiter{seen: map[string]*bucket{}} }

func (l *limiter) allow(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.seen[addr]
	if b == nil {
		if len(l.seen) > 10000 {
			clear(l.seen)
		}
		b = &bucket{tokens: limitBurst, at: now}
		l.seen[addr] = b
	}
	b.tokens = min(limitBurst, b.tokens+now.Sub(b.at).Seconds()*limitRate)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (s *Server) limited(h http.HandlerFunc) http.HandlerFunc { return s.limitedBy(s.limit, h) }

// limitedBy bounds h per address in l's buckets.
func (s *Server) limitedBy(l *limiter, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := s.clientIP(r)
		if !l.allow(ip) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "", http.StatusTooManyRequests)
			return
		}
		h(w, r)
	}
}
