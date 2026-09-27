package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tracker"
)

// TrackerView is a binding as its project's owner and the admins see it; no secret leaves the server.
type TrackerView struct {
	ID        string          `json:"id"`
	Project   string          `json:"project"`
	Kind      string          `json:"kind"`
	Base      string          `json:"base"`
	Repo      string          `json:"repo"`
	Bot       string          `json:"bot"`
	Settings  TrackerSettings `json:"settings"`
	Hook      string          `json:"hook,omitempty"`        // the webhook address to give the tracker
	HookKey   string          `json:"hook_secret,omitempty"` // only in the answer that made the binding
	Polled    time.Time       `json:"polled,omitzero"`
	LastOK    time.Time       `json:"last_ok,omitzero"`
	LastError string          `json:"last_error,omitempty"`
	Stopped   string          `json:"stopped,omitempty"`
	Paused    time.Time       `json:"paused_until,omitzero"`
	Issues    int             `json:"issues"`
	Failing   int             `json:"failing"` // issues whose last read or write failed
}

func (s *Server) trackerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/trackers", s.api(s.listTrackers))
	mux.HandleFunc("POST /api/trackers", s.api(s.addTracker))
	mux.HandleFunc("DELETE /api/trackers", s.api(s.removeTracker))
	mux.HandleFunc("POST /api/trackers/settings", s.api(s.trackerSettings))
	mux.HandleFunc("POST /api/trackers/credential", s.api(s.trackerCredential))
	mux.HandleFunc("POST /api/trackers/rescan", s.api(s.rescanTracker))
	mux.HandleFunc("POST /hooks/{id}", s.trackerHook)
}

// managesProject: c may bind project's repositories (its owner, an admin).
func (s *Server) managesProject(c caller, project string) bool {
	if c.admin() {
		return true
	}
	ok := false
	s.opt.Coord.Read(func(st *task.State) { ok = st.Projects[project] != nil && st.Projects[project].Owner == c.user.ID })
	return ok
}

func (s *Server) hookURL(id string) string {
	return strings.TrimRight(s.opt.Config.PublicURL, "/") + "/hooks/" + id
}

func (s *Server) trackerView(x store.Tracker) TrackerView {
	v := TrackerView{ID: x.ID, Project: x.Project, Kind: x.Kind, Base: x.Base, Repo: x.Repo, Bot: x.Bot, Settings: settingsOf(x),
		Polled: x.Polled, LastOK: x.LastOK, LastError: x.LastError, Stopped: x.Stopped, Paused: x.Paused}
	if s.opt.Config.PublicURL != "" {
		v.Hook = s.hookURL(x.ID)
	}
	if rows, err := s.team().TrackerIssues(x.ID, false); err == nil {
		for _, r := range rows {
			if r.Task != "" {
				v.Issues++
			}
			if r.LastError != "" {
				v.Failing++
			}
		}
	}
	return v
}

// managed is binding id when c manages its project.
func (s *Server) managed(w http.ResponseWriter, c caller, id string) (store.Tracker, bool) {
	x, err := s.team().Tracker(id)
	if err != nil || !s.managesProject(c, x.Project) {
		apiError(w, http.StatusNotFound, "not_found")
		return store.Tracker{}, false
	}
	return x, true
}

func (s *Server) listTrackers(w http.ResponseWriter, r *http.Request, c caller) {
	xs, err := s.team().Trackers()
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	out := []TrackerView{}
	for _, x := range xs {
		if s.managesProject(c, x.Project) {
			out = append(out, s.trackerView(x))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// checkTracker opens the repository with token and says whom the token acts as.
func checkTracker(ctx context.Context, kind, base, repo, token string) (tracker.Repo, string, error) {
	tr, err := tracker.New(tracker.Config{Kind: kind, Base: base, Repo: repo, Token: token})
	if err != nil {
		return tracker.Repo{}, "", err
	}
	me, err := tr.Me(ctx)
	if err != nil {
		return tracker.Repo{}, "", err
	}
	rp, err := tr.Repo(ctx)
	return rp, me, err
}

// trackerError answers why a tracker could not be used, in a stable code.
func trackerError(w http.ResponseWriter, err error) {
	var ae *tracker.AuthError
	switch {
	case errors.As(err, &ae):
		apiError(w, http.StatusBadRequest, "tracker_auth")
	case errors.Is(err, tracker.ErrNotFound):
		apiError(w, http.StatusBadRequest, "tracker_repo")
	default:
		apiError(w, http.StatusBadGateway, "tracker_unreachable")
	}
}

func (s *Server) addTracker(w http.ResponseWriter, r *http.Request, c caller) {
	var p struct {
		Project  string           `json:"project"`
		Kind     string           `json:"kind"`
		Base     string           `json:"base"`
		Repo     string           `json:"repo"`
		Token    string           `json:"token"`
		Settings *TrackerSettings `json:"settings,omitempty"`
	}
	if !decode(w, r, &p) {
		return
	}
	set := DefaultSettings()
	if p.Settings != nil {
		set = *p.Settings
	}
	p.Base = strings.TrimRight(strings.TrimSpace(p.Base), "/")
	switch {
	case !s.managesProject(c, p.Project):
		apiError(w, http.StatusNotFound, "not_found")
		return
	case s.syncer == nil:
		apiError(w, http.StatusConflict, "no_sync")
		return
	case set.check() != nil, p.Token == "", !strings.HasPrefix(p.Base, "http://") && !strings.HasPrefix(p.Base, "https://"):
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	rp, me, err := checkTracker(r.Context(), p.Kind, p.Base, strings.TrimSpace(p.Repo), p.Token)
	if err != nil {
		trackerError(w, err)
		return
	}
	secret := make([]byte, 24)
	rand.Read(secret)
	hookKey := hex.EncodeToString(secret)
	settings, _ := json.Marshal(set)
	x, err := s.team().AddTracker(store.Tracker{Project: p.Project, Kind: p.Kind, Base: p.Base, Repo: rp.FullName, RepoID: rp.ID, Bot: me,
		Token: s.syncer.seal.Seal([]byte(p.Token)), HookSecret: s.syncer.seal.Seal([]byte(hookKey)), Settings: string(settings), CreatedBy: c.user.ID})
	if errors.Is(err, store.ErrExists) {
		apiError(w, http.StatusConflict, "exists")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.audit(r, c.user.ID, "tracker", p.Project+" "+x.Repo)
	s.syncer.Wake()
	v := s.trackerView(x)
	v.HookKey = hookKey
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) removeTracker(w http.ResponseWriter, r *http.Request, c caller) {
	var p struct{ ID string }
	if !decode(w, r, &p) {
		return
	}
	x, ok := s.managed(w, c, p.ID)
	if !ok {
		return
	}
	if err := s.team().RemoveTracker(x.ID); err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.audit(r, c.user.ID, "tracker.remove", x.Project+" "+x.Repo)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) trackerSettings(w http.ResponseWriter, r *http.Request, c caller) {
	var p struct {
		ID       string          `json:"id"`
		Settings TrackerSettings `json:"settings"`
	}
	if !decode(w, r, &p) {
		return
	}
	x, ok := s.managed(w, c, p.ID)
	if !ok {
		return
	}
	if p.Settings.check() != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	b, _ := json.Marshal(p.Settings)
	if err := s.team().SetTrackerSettings(x.ID, string(b)); err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.audit(r, c.user.ID, "tracker.settings", x.Project+" "+x.Repo)
	x.Settings = string(b)
	writeJSON(w, http.StatusOK, s.trackerView(x))
}

func (s *Server) trackerCredential(w http.ResponseWriter, r *http.Request, c caller) {
	var p struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if !decode(w, r, &p) {
		return
	}
	x, ok := s.managed(w, c, p.ID)
	if !ok {
		return
	}
	if s.syncer == nil {
		apiError(w, http.StatusConflict, "no_sync")
		return
	}
	rp, me, err := checkTracker(r.Context(), x.Kind, x.Base, x.Repo, p.Token)
	if err != nil {
		trackerError(w, err)
		return
	}
	if rp.ID != x.RepoID {
		apiError(w, http.StatusBadRequest, "tracker_repo")
		return
	}
	if err := s.team().SetTrackerCredential(x.ID, me, s.syncer.seal.Seal([]byte(p.Token))); err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.audit(r, c.user.ID, "tracker.credential", x.Project+" "+x.Repo)
	s.syncer.Wake()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rescanTracker(w http.ResponseWriter, r *http.Request, c caller) {
	var p struct{ ID string }
	if !decode(w, r, &p) {
		return
	}
	x, ok := s.managed(w, c, p.ID)
	if !ok {
		return
	}
	if err := s.team().Rescan(x.ID); err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	if s.syncer != nil {
		s.syncer.Wake()
	}
	w.WriteHeader(http.StatusNoContent)
}

// trackerHook takes a tracker's webhook delivery: signed with the binding's secret, about its repository, once per
// delivery id; it only marks the issue to read again.
func (s *Server) trackerHook(w http.ResponseWriter, r *http.Request) {
	x, err := s.team().Tracker(r.PathValue("id"))
	if err != nil || s.syncer == nil {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		return
	}
	secret, err := s.syncer.seal.Open(x.HookSecret)
	if err != nil || !tracker.Verify(x.Kind, secret, body, r.Header) {
		s.audit(r, "", "denied", "hook "+x.ID)
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	if id := tracker.Delivery(x.Kind, r.Header); id != "" {
		if first, err := s.team().TakeDelivery(x.ID + "/" + id); err != nil || !first {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	repo, number, err := tracker.HookIssue(x.Kind, body)
	if err != nil || repo != x.RepoID {
		http.Error(w, "not this repository", http.StatusBadRequest)
		return
	}
	if number > 0 {
		s.team().MarkDirty(x.ID, number)
		s.syncer.Wake()
	}
	w.WriteHeader(http.StatusNoContent)
}
