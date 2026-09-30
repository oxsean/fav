package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/wire"
)

// inviteAge is how long an invitation link works.
const inviteAge = 72 * time.Hour

// caller is who makes an /api request.
type caller struct {
	cred store.Credential
	user store.User
}

func (c caller) admin() bool { return c.user.Role == store.RoleAdmin }

// apiError answers a stable code the page words itself.
func apiError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// api signs the request in as a client and, for a change, requires the page's own header and origin: another site
// can neither send the header nor read the answer.
func (s *Server) api(h func(http.ResponseWriter, *http.Request, caller)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cred, u, ok := s.credential(r, RoleClient)
		if !ok {
			apiError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if r.Method != http.MethodGet && (r.Header.Get("X-Tend") != "1" || !sameOrigin(r)) {
			apiError(w, http.StatusForbidden, "csrf")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		h(w, r, caller{cred, u})
	}
}

func (s *Server) adminOnly(h func(http.ResponseWriter, *http.Request, caller)) func(http.ResponseWriter, *http.Request, caller) {
	return func(w http.ResponseWriter, r *http.Request, c caller) {
		if !c.admin() {
			s.audit(r, c.user.ID, "denied", r.Method+" "+r.URL.Path)
			apiError(w, http.StatusForbidden, "unauthorized")
			return
		}
		h(w, r, c)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return false
	}
	return true
}

func (s *Server) apiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/users", s.api(s.listUsers))
	mux.HandleFunc("POST /api/users", s.api(s.adminOnly(s.setUser)))
	mux.HandleFunc("GET /api/admits", s.api(s.adminOnly(s.listAdmits)))
	mux.HandleFunc("POST /api/admits", s.api(s.adminOnly(s.addAdmit)))
	mux.HandleFunc("DELETE /api/admits", s.api(s.adminOnly(s.removeAdmit)))
	mux.HandleFunc("POST /api/invites", s.api(s.adminOnly(s.invite)))
	mux.HandleFunc("GET /api/invites", s.api(s.adminOnly(s.listInvites)))
	mux.HandleFunc("DELETE /api/invites", s.api(s.adminOnly(s.revokeInvite)))
	mux.HandleFunc("GET /api/tokens", s.api(s.listTokens))
	mux.HandleFunc("GET /api/push/key", s.api(s.pushKey))
	mux.HandleFunc("PUT /api/push/device", s.api(s.pushDevice))
	mux.HandleFunc("DELETE /api/push/device", s.api(s.dropPushDevice))
	mux.HandleFunc("GET /api/push/devices", s.api(s.pushDevices))
	mux.HandleFunc("POST /api/push/prefs", s.api(s.setPushPrefs))
	mux.HandleFunc("DELETE /api/push/devices", s.api(s.removePushDevice))
	mux.HandleFunc("POST /api/act", s.api(s.act))
	mux.HandleFunc("POST /api/tokens", s.api(s.addToken))
	mux.HandleFunc("DELETE /api/tokens", s.api(s.revokeToken))
	mux.HandleFunc("GET /api/machines", s.api(s.listMachines))
	mux.HandleFunc("POST /api/machines", s.api(s.addMachine))
	mux.HandleFunc("DELETE /api/machines", s.api(s.revokeMachine))
	mux.HandleFunc("POST /api/machines/rebind", s.api(s.rebindMachine))
	mux.HandleFunc("GET /api/identities", s.api(s.listIdentities))
	mux.HandleFunc("DELETE /api/identities", s.api(s.unlinkIdentity))
	mux.HandleFunc("GET /api/audit", s.api(s.adminOnly(s.listAudit)))
	mux.HandleFunc("GET /api/me/webhook", s.api(s.getWebhook))
	mux.HandleFunc("POST /api/me/webhook", s.api(s.setWebhook))
	mux.HandleFunc("POST /api/me/webhook/test", s.limited(s.api(s.testWebhook)))
	mux.HandleFunc("POST /api/users/offboard", s.api(s.adminOnly(s.offboard)))
	mux.HandleFunc("GET /api/device", s.api(s.deviceLookup))
	mux.HandleFunc("POST /api/device", s.api(s.deviceDecide))
}

// PublicUser is a user as other members see them.
type PublicUser struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username,omitempty"`
	Email    string `json:"email,omitempty"` // admins only
	Role     string `json:"role"`
	Disabled bool   `json:"disabled,omitempty"`
	// To admins: how they sign in and when a credential of theirs was last used.
	Logins []string  `json:"logins,omitempty"`
	Seen   time.Time `json:"seen,omitzero"`
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, c caller) {
	us, err := s.team().Users()
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	out := []PublicUser{}
	for _, u := range us {
		p := PublicUser{ID: u.ID, Name: displayName(u), Username: u.Username, Role: u.Role, Disabled: u.Disabled}
		if c.admin() {
			p.Email = u.Email
			ids, _ := s.team().Identities(u.ID)
			for _, i := range ids {
				if !slices.Contains(p.Logins, i.Provider) {
					p.Logins = append(p.Logins, i.Provider)
				}
			}
			for _, x := range s.creds(func(x store.Credential) bool { return x.Owner == u.ID }) {
				if x.LastUsed.After(p.Seen) {
					p.Seen = x.LastUsed
				}
			}
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) setUser(w http.ResponseWriter, r *http.Request, c caller) {
	var p struct {
		ID       string  `json:"id"`
		Role     *string `json:"role,omitempty"`
		Disabled *bool   `json:"disabled,omitempty"`
	}
	if !decode(w, r, &p) {
		return
	}
	if p.ID == store.LocalUser || p.ID == c.user.ID && (p.Disabled != nil || p.Role != nil) {
		apiError(w, http.StatusConflict, "self")
		return
	}
	if err := s.team().SetUser(p.ID, p.Role, p.Disabled); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			apiError(w, http.StatusNotFound, "not_found")
			return
		}
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	s.audit(r, c.user.ID, "user", subjectOf(mustJSON(p)))
	s.sweep()
	w.WriteHeader(http.StatusNoContent)
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func (s *Server) listAdmits(w http.ResponseWriter, r *http.Request, c caller) {
	as, err := s.team().Admits()
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	if as == nil {
		as = []store.Admit{}
	}
	writeJSON(w, http.StatusOK, as)
}

func (s *Server) addAdmit(w http.ResponseWriter, r *http.Request, c caller) {
	var a store.Admit
	if !decode(w, r, &a) {
		return
	}
	a.AddedBy = c.user.ID
	if err := s.team().AddAdmit(a); err != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	s.audit(r, c.user.ID, "admit", a.Kind+" "+a.Value+" "+a.Role)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeAdmit(w http.ResponseWriter, r *http.Request, c caller) {
	var a store.Admit
	if !decode(w, r, &a) {
		return
	}
	if err := s.team().RemoveAdmit(a.Kind, a.Value); err != nil {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	s.audit(r, c.user.ID, "admit_removed", a.Kind+" "+a.Value)
	w.WriteHeader(http.StatusNoContent)
}

// invite makes a one-time link: whoever signs in through it joins with the role, and with a project also becomes a
// member there with the access.
func (s *Server) invite(w http.ResponseWriter, r *http.Request, c caller) {
	var p struct {
		Role    string `json:"role"`
		Project string `json:"project"`
		Access  string `json:"access"`
	}
	if !decode(w, r, &p) {
		return
	}
	if p.Project != "" && s.opt.Coord.State().Projects[p.Project] == nil {
		apiError(w, http.StatusBadRequest, "project")
		return
	}
	secret, err := s.team().NewProjectInvite(p.Role, c.user.ID, p.Project, p.Access, inviteAge)
	if err != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	s.audit(r, c.user.ID, "invite", strings.TrimSpace(p.Role+" "+p.Project+" "+p.Access))
	writeJSON(w, http.StatusOK, map[string]any{"url": s.base(r) + "/#invite-" + secret, "expires": time.Now().Add(inviteAge)})
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request, c caller) {
	list, err := s.team().Invites()
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request, c caller) {
	var p struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &p) {
		return
	}
	if err := s.team().RevokeInvite(p.ID); errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	} else if err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.audit(r, c.user.ID, "invite.revoke", p.ID)
	w.WriteHeader(http.StatusNoContent)
}

// Credentials of the caller's (all, for an admin's machines), newest first.
func (s *Server) creds(keep func(store.Credential) bool) []store.Credential {
	s.opt.Dir.mu.RLock()
	defer s.opt.Dir.mu.RUnlock()
	out := []store.Credential{}
	for _, c := range s.opt.Dir.creds {
		if keep(c) {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b store.Credential) int { return b.Created.Compare(a.Created) })
	return out
}

type credView struct {
	store.Credential
	Current bool `json:"current,omitempty"`
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request, c caller) {
	out := []credView{}
	for _, x := range s.creds(func(x store.Credential) bool {
		return x.Owner == c.user.ID && (x.Kind == store.KindToken || x.Kind == store.KindWeb)
	}) {
		out = append(out, credView{x, x.ID == c.cred.ID})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) addToken(w http.ResponseWriter, r *http.Request, c caller) {
	var p struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &p) {
		return
	}
	if p.Name = strings.TrimSpace(p.Name); p.Name == "" || len(p.Name) > 64 || strings.HasPrefix(p.Name, viaToken) {
		apiError(w, http.StatusBadRequest, "name")
		return
	}
	secret, cr, err := s.team().NewCredential(store.KindToken, p.Name, c.user.ID, 0)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.opt.Dir.Reload()
	s.audit(r, c.user.ID, "token", cr.ID)
	writeJSON(w, http.StatusOK, map[string]string{"id": cr.ID, "token": secret})
}

// revoke ends credential id of kinds when it is the caller's, or an admin's to end.
func (s *Server) revoke(w http.ResponseWriter, r *http.Request, c caller, kinds ...string) {
	var p struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &p) {
		return
	}
	found := s.creds(func(x store.Credential) bool {
		return x.ID == p.ID && slices.Contains(kinds, x.Kind) && (x.Owner == c.user.ID || c.admin())
	})
	if len(found) == 0 {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err := s.team().Revoke(p.ID); err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.audit(r, c.user.ID, "revoke", found[0].Kind+" "+p.ID)
	s.sweep()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request, c caller) {
	s.revoke(w, r, c, store.KindToken, store.KindWeb)
}

func (s *Server) listMachines(w http.ResponseWriter, r *http.Request, c caller) {
	writeJSON(w, http.StatusOK, s.creds(func(x store.Credential) bool {
		return x.Kind == store.KindNode && (x.Owner == c.user.ID || c.admin())
	}))
}

// addMachine gives the caller a node token for a new machine of theirs: they own it, and only they dispatch to it
// until they share it.
func (s *Server) addMachine(w http.ResponseWriter, r *http.Request, c caller) {
	var p struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &p) {
		return
	}
	if CheckMachine(p.Name) != nil {
		apiError(w, http.StatusBadRequest, "name")
		return
	}
	secret, cr, err := s.team().NewCredential(store.KindNode, p.Name, c.user.ID, 0)
	if errors.Is(err, store.ErrExists) {
		apiError(w, http.StatusConflict, "exists")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.opt.Dir.Reload()
	s.opt.Coord.Expect(p.Name, time.Time{})
	s.audit(r, c.user.ID, "machine", cr.ID+" "+p.Name)
	writeJSON(w, http.StatusOK, map[string]string{"id": cr.ID, "token": secret,
		"command": "tend node install-service --connect " + s.base(r) + " --token-file ~/.config/tend/node-token"})
}

func (s *Server) revokeMachine(w http.ResponseWriter, r *http.Request, c caller) {
	s.revoke(w, r, c, store.KindNode)
}

func (s *Server) rebindMachine(w http.ResponseWriter, r *http.Request, c caller) {
	var p struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &p) {
		return
	}
	if len(s.creds(func(x store.Credential) bool {
		return x.ID == p.ID && x.Kind == store.KindNode && (x.Owner == c.user.ID || c.admin())
	})) == 0 {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err := s.team().Rebind(p.ID); err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.audit(r, c.user.ID, "rebind", p.ID)
	s.opt.Dir.Reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listIdentities(w http.ResponseWriter, r *http.Request, c caller) {
	ids, err := s.team().Identities(c.user.ID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	if ids == nil {
		ids = []store.Identity{}
	}
	writeJSON(w, http.StatusOK, ids)
}

// unlinkIdentity takes one of the caller's sign-in accounts off them; the last one stays (409 last).
func (s *Server) unlinkIdentity(w http.ResponseWriter, r *http.Request, c caller) {
	var p struct {
		Provider string `json:"provider"`
		Issuer   string `json:"issuer"`
		Subject  string `json:"subject"`
	}
	if !decode(w, r, &p) {
		return
	}
	switch err := s.team().Unlink(c.user.ID, store.Identity{Provider: p.Provider, Issuer: p.Issuer, Subject: p.Subject}); {
	case errors.Is(err, store.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found")
		return
	case errors.Is(err, store.ErrLastLogin):
		apiError(w, http.StatusConflict, "last")
		return
	case err != nil:
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.audit(r, c.user.ID, "unlink", p.Provider+":"+p.Subject)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request, c caller) {
	es, err := s.team().AuditLog(200)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	if es == nil {
		es = []store.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, es)
}

func (s *Server) getWebhook(w http.ResponseWriter, r *http.Request, c caller) {
	url, err := s.team().Webhook(c.user.ID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (s *Server) setWebhook(w http.ResponseWriter, r *http.Request, c caller) {
	var p struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &p) {
		return
	}
	if p.URL = strings.TrimSpace(p.URL); CheckWebhook(p.URL) != nil {
		apiError(w, http.StatusBadRequest, "url")
		return
	}
	if err := s.team().SetWebhook(c.user.ID, p.URL); err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.audit(r, c.user.ID, "webhook", c.user.ID)
	w.WriteHeader(http.StatusNoContent)
}

// testWebhook posts a test notice to the caller's saved webhook and says how it went: ok, and the HTTP status the
// webhook answered, or "unreachable" for any other failure (the kind of network error is not told: the address is the
// caller's to pick, and the answer should not map what the server can reach).
func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request, c caller) {
	hook, err := s.team().Webhook(c.user.ID)
	switch {
	case err != nil:
		apiError(w, http.StatusInternalServerError, "internal")
		return
	case hook == "":
		apiError(w, http.StatusConflict, "no_webhook")
		return
	}
	now := time.Now().UTC()
	err = postWebhook(r.Context(), s.egress().Client(sendTimeout), hook, WebhookPayload{Event: "test", Title: "tend", At: now,
		Text: "tend: a test from " + displayName(c.user) + "'s Me page"})
	s.audit(r, c.user.ID, "webhook.test", resultOf(err))
	out := map[string]any{"ok": err == nil}
	var se *sendError
	switch {
	case errors.As(err, &se):
		out["status"] = strconv.Itoa(se.status)
	case err != nil:
		out["status"] = "unreachable"
	}
	writeJSON(w, http.StatusOK, out)
}

// offboard hands a leaving user's work on (user.offboard), then disables them and ends every credential of theirs,
// their machines' too.
func (s *Server) offboard(w http.ResponseWriter, r *http.Request, c caller) {
	var p coord.Offboard
	if !decode(w, r, &p) {
		return
	}
	if p.User == store.LocalUser || p.User == c.user.ID {
		apiError(w, http.StatusConflict, "self")
		return
	}
	params, _ := json.Marshal(p)
	req := &wire.Request{Method: coord.MUserOffboard, CommandID: "offboard-" + p.User, Params: params}
	if _, err := s.opt.Coord.HandlerFor(principal(c.user))(r.Context(), req); err != nil {
		apiError(w, wireStatus(err), wire.Code(err))
		return
	}
	off := true
	if err := s.team().SetUser(p.User, nil, &off); err != nil {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err := s.team().RevokeAll(p.User); err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.audit(r, c.user.ID, "offboard", p.User+" "+p.To)
	s.sweep()
	w.WriteHeader(http.StatusNoContent)
}

// wireStatus is the HTTP status for a coordinator error.
func wireStatus(err error) int {
	switch wire.Code(err) {
	case wire.CodeNotFound:
		return http.StatusNotFound
	case wire.CodeUnauthorized:
		return http.StatusForbidden
	case wire.CodeBadRequest:
		return http.StatusBadRequest
	case wire.CodeConflict:
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}
