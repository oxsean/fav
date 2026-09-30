package server

import (
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/store"
)

// What a route asks of its caller.
const (
	byAnyone = "public" // anyone; a hook checks its own signature
	byClient = "client" // a personal token or a browser session, else 401
	byAdmin  = "admin"  // a client who is an admin, else 403
	byNode   = "node"   // a node token, else 401
)

// Which buckets bound a route per address.
const (
	bySignIns = "sign-ins"
	byActing  = "acting"
)

type rule struct {
	who   string
	csrf  bool   // refused when another site's page sends it
	limit string // "" when unbounded
	body  int64  // the most of a body it reads; 0 when it reads none
}

// routeRules is every route the server registers and what it takes: a route missing here, or one here the server
// no longer has, fails TestEveryRouteTakesWhatItsRuleSays.
var routeRules = map[string]rule{
	"/":                             {who: byAnyone},
	"/node":                         {who: byNode},
	"/client":                       {who: byClient},
	"/healthz":                      {who: byAnyone},
	"/login":                        {who: byAnyone, csrf: true, limit: bySignIns, body: 4096},
	"/logout":                       {who: byAnyone, csrf: true},
	"/session":                      {who: byClient},
	"GET /auth/logins":              {who: byAnyone},
	"GET /auth/{name}/start":        {who: byAnyone, limit: bySignIns},
	"GET /auth/{name}/callback":     {who: byAnyone, limit: bySignIns},
	"GET /auth/invite":              {who: byAnyone, limit: bySignIns},
	"POST /auth/device":             {who: byAnyone, limit: bySignIns, body: 1024},
	"POST /auth/device/token":       {who: byAnyone, limit: bySignIns, body: 1024},
	"GET /theme/{file}":             {who: byAnyone},
	"GET /{$}":                      {who: byAnyone},
	"GET /sw.js":                    {who: byAnyone},
	"GET /manifest.webmanifest":     {who: byAnyone},
	"GET /api/users":                {who: byClient},
	"POST /api/users":               {who: byAdmin, csrf: true, body: 64 << 10},
	"GET /api/admits":               {who: byAdmin},
	"POST /api/admits":              {who: byAdmin, csrf: true, body: 64 << 10},
	"DELETE /api/admits":            {who: byAdmin, csrf: true, body: 64 << 10},
	"POST /api/invites":             {who: byAdmin, csrf: true, body: 64 << 10},
	"GET /api/invites":              {who: byAdmin},
	"DELETE /api/invites":           {who: byAdmin, csrf: true, body: 64 << 10},
	"GET /api/tokens":               {who: byClient},
	"POST /api/tokens":              {who: byClient, csrf: true, body: 64 << 10},
	"DELETE /api/tokens":            {who: byClient, csrf: true, body: 64 << 10},
	"GET /api/push/key":             {who: byClient},
	"PUT /api/push/device":          {who: byClient, csrf: true, body: 64 << 10},
	"DELETE /api/push/device":       {who: byClient, csrf: true, body: 64 << 10},
	"GET /api/push/devices":         {who: byClient},
	"POST /api/push/prefs":          {who: byClient, csrf: true, body: 64 << 10},
	"DELETE /api/push/devices":      {who: byClient, csrf: true, body: 64 << 10},
	"POST /api/act":                 {who: byClient, csrf: true, limit: byActing, body: 64 << 10},
	"GET /api/machines":             {who: byClient},
	"POST /api/machines":            {who: byClient, csrf: true, body: 64 << 10},
	"DELETE /api/machines":          {who: byClient, csrf: true, body: 64 << 10},
	"POST /api/machines/rebind":     {who: byClient, csrf: true, body: 64 << 10},
	"GET /api/identities":           {who: byClient},
	"DELETE /api/identities":        {who: byClient, csrf: true, body: 64 << 10},
	"GET /api/audit":                {who: byAdmin},
	"GET /api/me/webhook":           {who: byClient},
	"POST /api/me/webhook":          {who: byClient, csrf: true, body: 64 << 10},
	"POST /api/me/webhook/test":     {who: byClient, csrf: true, limit: bySignIns, body: 64 << 10},
	"POST /api/users/offboard":      {who: byAdmin, csrf: true, body: 64 << 10},
	"GET /api/device":               {who: byClient, limit: byActing},
	"POST /api/device":              {who: byClient, csrf: true, limit: byActing, body: 64 << 10},
	"GET /api/trackers":             {who: byClient},
	"POST /api/trackers":            {who: byClient, csrf: true, body: 64 << 10},
	"DELETE /api/trackers":          {who: byClient, csrf: true, body: 64 << 10},
	"POST /api/trackers/settings":   {who: byClient, csrf: true, body: 64 << 10},
	"POST /api/trackers/credential": {who: byClient, csrf: true, body: 64 << 10},
	"POST /api/trackers/rescan":     {who: byClient, csrf: true, body: 64 << 10},
	"GET /api/trackers/issues":      {who: byClient},
	"GET /api/trackers/preview":     {who: byClient},
	"GET /api/trackers/tasks":       {who: byClient},
	"GET /api/me/trackers":          {who: byClient},
	"POST /hooks/{id}":              {who: byAnyone, body: 1 << 20},
	"GET /icon-180.png":             {who: byAnyone},
	"GET /icon-192.png":             {who: byAnyone},
	"GET /icon-512.png":             {who: byAnyone},
	"GET /icon-maskable-512.png":    {who: byAnyone},
}

type patterns []string

func (p *patterns) HandleFunc(pattern string, _ func(http.ResponseWriter, *http.Request)) {
	*p = append(*p, pattern)
}

// endless is a body far longer than any route takes, counting what the server read of it.
type endless struct{ read, size int64 }

func (e *endless) Read(p []byte) (int, error) {
	if e.read >= e.size {
		return 0, io.EOF
	}
	p = p[:min(int64(len(p)), e.size-e.read)]
	for i := range p {
		p[i] = 'a'
	}
	if e.read == 0 {
		copy(p, `{"a":"`)
	}
	e.read += int64(len(p))
	return len(p), nil
}

// Every route asks for the credential its rule names, refuses another site's page, is bounded per address and reads
// at most its body limit, as routeRules says; a route the server registers without a rule fails.
func TestEveryRouteTakesWhatItsRuleSays(t *testing.T) {
	r := newRig(t)
	var got patterns
	r.srv.routes(&got)
	slices.Sort(got)
	if want := slices.Sorted(maps.Keys(routeRules)); !slices.Equal(got, want) {
		for _, p := range got {
			if _, ok := routeRules[p]; !ok {
				t.Errorf("%s has no rule", p)
			}
		}
		for _, p := range want {
			if !slices.Contains(got, p) {
				t.Errorf("%s is not a route", p)
			}
		}
		t.FailNow()
	}
	member := func() string {
		must(t, r.team.AddAdmit(store.Admit{Kind: store.AdmitEmail, Value: "bob@corp.example", Role: store.RoleMember}))
		u, err := r.team.Admit(store.Identity{Provider: "gitea", Issuer: "https://git.example", Subject: "bob", Email: "bob@corp.example", EmailVerified: true}, "")
		must(t, err)
		tok, _, err := r.team.NewCredential(store.KindToken, "t-bob", u.ID, 0)
		must(t, err)
		r.srv.sweep()
		return tok
	}()
	var h http.Handler
	fresh := func() { r.srv.limit, r.srv.acting = newLimiter(), newLimiter(); h = r.srv.Handler() }
	call := func(pattern, bearer string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
		method, path, ok := strings.Cut(pattern, " ")
		if !ok {
			method, path = http.MethodPost, pattern
			if routeRules[pattern].body == 0 && !routeRules[pattern].csrf {
				method = http.MethodGet
			}
		}
		path = strings.NewReplacer("{name}", "gitea", "{file}", "light.css", "{id}", "h_none", "{$}", "").Replace(path)
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	for _, pattern := range got {
		rule := routeRules[pattern]
		t.Run(pattern, func(t *testing.T) {
			fresh()
			if rule.who != byAnyone {
				if w := call(pattern, "", nil, nil); w.Code != http.StatusUnauthorized {
					t.Errorf("without a credential: %d", w.Code)
				}
			}
			if rule.who == byNode {
				if w := call(pattern, r.client, nil, nil); w.Code != http.StatusUnauthorized {
					t.Errorf("a client's token: %d", w.Code)
				}
			}
			if rule.who == byClient || rule.who == byAdmin {
				if w := call(pattern, r.nodeT, nil, nil); w.Code != http.StatusUnauthorized {
					t.Errorf("a node's token: %d", w.Code)
				}
			}
			if rule.who == byAdmin {
				if w := call(pattern, member, nil, map[string]string{"X-Tend": "1"}); w.Code != http.StatusForbidden {
					t.Errorf("a member: %d", w.Code)
				}
			}
			if rule.csrf {
				for _, from := range []map[string]string{
					{"X-Tend": "1", "Origin": "https://elsewhere.example"},
					{"X-Tend": "1", "Sec-Fetch-Site": "same-site"},
				} {
					if w := call(pattern, r.client, nil, from); w.Code != http.StatusForbidden && w.Code != http.StatusMethodNotAllowed {
						t.Errorf("from %v: %d", from, w.Code)
					}
				}
			}
			fresh()
			last := 0
			for range limitBurst + 1 {
				last = call(pattern, "", nil, nil).Code
			}
			if limited := last == http.StatusTooManyRequests; limited != (rule.limit != "") {
				t.Errorf("after %d calls: %d", limitBurst+1, last)
			}
			if rule.limit != "" {
				other := map[string]*limiter{bySignIns: r.srv.acting, byActing: r.srv.limit}[rule.limit]
				if len(other.seen) > 0 {
					t.Errorf("its calls went to the buckets other than %s", rule.limit)
				}
			}
			if rule.body > 0 {
				fresh()
				b := &endless{size: 4*rule.body + 16<<10}
				call(pattern, r.client, b, map[string]string{"X-Tend": "1"})
				if b.read > rule.body+4096 {
					t.Errorf("read %d bytes of a body, more than %d", b.read, rule.body)
				}
			}
		})
	}
}
