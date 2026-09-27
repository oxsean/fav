// Package trackertest is a Gitea, GitHub or GitLab for tests: one repository's issues and comments behind the API paths
// the tracker uses, with faults to inject.
package trackertest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Issue struct {
	Number    int64
	Title     string
	Body      string
	Closed    bool
	Labels    []string
	Assignees []string
	Updated   time.Time
	PR        bool // a pull request, which Gitea's and GitHub's issue lists hold too
}

type Comment struct {
	ID      int64
	Issue   int64
	Body    string
	Author  string
	Updated time.Time
	System  bool // GitLab's own note about a change
}

// Server holds one repository, owner/name with id RepoID, and the bot account Bot with token Token, speaking the API of
// Kind (gitea | github | gitlab).
type Server struct {
	*httptest.Server
	Kind, Repo, Bot, Token string
	RepoID                 int64

	mu       sync.Mutex
	issues   map[int64]*Issue
	comments []*Comment
	next     int64
	clock    time.Time
	// Faults: RateLimit answers the next n requests 429 with Retry-After 30; Refuse answers every request 401; LoseCreate
	// makes the next comment it creates answer 502 although the comment is made.
	RateLimit  int
	Refuse     bool
	LoseCreate bool
	// Requests counts requests by "METHOD path" without the query.
	Requests map[string]int
}

func New(kind, repo, bot, token string) *Server {
	s := &Server{Kind: kind, Repo: repo, Bot: bot, Token: token, RepoID: 42, issues: map[int64]*Issue{}, next: 100,
		clock: time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC), Requests: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

func (s *Server) tick() time.Time { s.clock = s.clock.Add(time.Second); return s.clock }

// Open makes issue number with title, body and labels, as a person (not the bot) would.
func (s *Server) Open(number int64, title, body string, labels ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issues[number] = &Issue{Number: number, Title: title, Body: body, Labels: labels, Updated: s.tick()}
}

// Change edits issue number: f changes it; its updated time moves.
func (s *Server) Change(number int64, f func(*Issue)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s.issues[number])
	s.issues[number].Updated = s.tick()
}

// Say adds a comment by author to issue number.
func (s *Server) Say(number int64, author, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	s.comments = append(s.comments, &Comment{ID: s.next, Issue: number, Body: body, Author: author, Updated: s.tick()})
	s.issues[number].Updated = s.clock
}

// Get is a copy of issue number.
func (s *Server) Get(number int64) Issue {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.issues[number]
}

// CommentsOf are copies of issue number's comments, without GitLab's own notes.
func (s *Server) CommentsOf(number int64) []Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Comment
	for _, c := range s.comments {
		if c.Issue == number && !c.System {
			out = append(out, *c)
		}
	}
	return out
}

// DeleteComment removes comment id, as a person would.
func (s *Server) DeleteComment(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.comments = slices.DeleteFunc(s.comments, func(c *Comment) bool { return c.ID == id })
}

func (s *Server) Count(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Requests[key]
}

func (s *Server) authorized(r *http.Request) bool {
	switch s.Kind {
	case "github":
		return r.Header.Get("Authorization") == "Bearer "+s.Token
	case "gitlab":
		return r.Header.Get("PRIVATE-TOKEN") == s.Token
	}
	return r.Header.Get("Authorization") == "token "+s.Token
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests[r.Method+" "+r.URL.Path]++
	switch {
	case s.Refuse || !s.authorized(r):
		http.Error(w, `{"message":"token is required"}`, http.StatusUnauthorized)
		return
	case s.RateLimit > 0:
		s.RateLimit--
		w.Header().Set("Retry-After", "30")
		http.Error(w, `{"message":"rate limit"}`, http.StatusTooManyRequests)
		return
	}
	if s.Kind == "gitlab" {
		s.gitlab(w, r)
		return
	}
	user := func(login string) map[string]any { return map[string]any{"id": len(login), "login": login} }
	issue := func(i *Issue) map[string]any {
		labels := []map[string]string{}
		for _, l := range i.Labels {
			labels = append(labels, map[string]string{"name": l})
		}
		as := []map[string]any{}
		for _, a := range i.Assignees {
			as = append(as, user(a))
		}
		state := "open"
		if i.Closed {
			state = "closed"
		}
		out := map[string]any{"number": i.Number, "title": i.Title, "body": i.Body, "state": state, "labels": labels, "assignees": as,
			"html_url": fmt.Sprintf("%s/%s/issues/%d", s.URL, s.Repo, i.Number), "updated_at": i.Updated}
		if i.PR {
			out["pull_request"] = map[string]any{}
		}
		return out
	}
	comment := func(c *Comment) map[string]any {
		return map[string]any{"id": c.ID, "body": c.Body, "user": user(c.Author), "updated_at": c.Updated}
	}
	prefix := map[string]string{"github": "/api/v3"}[s.Kind]
	if prefix == "" {
		prefix = "/api/v1"
	}
	rest, ok := strings.CutPrefix(r.URL.Path, prefix+"/repos/"+s.Repo)
	pageSize := "limit"
	if s.Kind == "github" {
		pageSize = "per_page"
	}
	switch {
	case r.URL.Path == prefix+"/user":
		writeJSON(w, user(s.Bot))
	case !ok:
		http.NotFound(w, r)
	case rest == "":
		writeJSON(w, map[string]any{"id": s.RepoID, "full_name": s.Repo, "html_url": s.URL + "/" + s.Repo})
	case rest == "/issues" && r.Method == http.MethodGet:
		since, _ := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
		writeJSON(w, s.list(r, since, pageSize, issue))
	case strings.HasPrefix(rest, "/issues/comments/") && r.Method == http.MethodPatch:
		id, _ := strconv.ParseInt(strings.TrimPrefix(rest, "/issues/comments/"), 10, 64)
		if c := s.edit(w, r, id); c != nil {
			writeJSON(w, comment(c))
		}
	case strings.HasPrefix(rest, "/issues/"):
		parts := strings.Split(strings.TrimPrefix(rest, "/issues/"), "/")
		n, _ := strconv.ParseInt(parts[0], 10, 64)
		i := s.issues[n]
		if i == nil {
			http.NotFound(w, r)
			return
		}
		switch {
		case len(parts) == 1 && r.Method == http.MethodGet:
			writeJSON(w, issue(i))
		case len(parts) == 1 && r.Method == http.MethodPatch:
			var p struct{ State string }
			json.NewDecoder(r.Body).Decode(&p)
			i.Closed, i.Updated = p.State == "closed", s.tick()
			writeJSON(w, issue(i))
		case parts[1] == "comments" && r.Method == http.MethodGet:
			writeJSON(w, s.commentsPage(r, n, pageSize, comment))
		case parts[1] == "comments" && r.Method == http.MethodPost:
			if c := s.create(w, r, i); c != nil {
				w.WriteHeader(http.StatusCreated)
				writeJSON(w, comment(c))
			}
		case parts[1] == "labels" && r.Method == http.MethodPost:
			var p struct{ Labels []string }
			json.NewDecoder(r.Body).Decode(&p)
			s.label(i, p.Labels...)
			writeJSON(w, []any{})
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

// gitlab answers GitLab's API: the project by its URL-encoded path, issues by iid, comments as notes.
func (s *Server) gitlab(w http.ResponseWriter, r *http.Request) {
	user := func(name string) map[string]any { return map[string]any{"id": len(name), "username": name} }
	issue := func(i *Issue) map[string]any {
		as := []map[string]any{}
		for _, a := range i.Assignees {
			as = append(as, user(a))
		}
		state := "opened"
		if i.Closed {
			state = "closed"
		}
		labels := i.Labels
		if labels == nil {
			labels = []string{}
		}
		return map[string]any{"iid": i.Number, "title": i.Title, "description": i.Body, "state": state, "labels": labels, "assignees": as,
			"web_url": fmt.Sprintf("%s/%s/-/issues/%d", s.URL, s.Repo, i.Number), "updated_at": i.Updated}
	}
	note := func(c *Comment) map[string]any {
		return map[string]any{"id": c.ID, "body": c.Body, "author": user(c.Author), "updated_at": c.Updated, "system": c.System}
	}
	path := r.URL.EscapedPath()
	rest, ok := strings.CutPrefix(path, "/api/v4/projects/"+url.PathEscape(s.Repo))
	switch {
	case path == "/api/v4/user":
		writeJSON(w, user(s.Bot))
	case !ok:
		http.NotFound(w, r)
	case rest == "":
		writeJSON(w, map[string]any{"id": s.RepoID, "path_with_namespace": s.Repo, "web_url": s.URL + "/" + s.Repo})
	case rest == "/issues" && r.Method == http.MethodGet:
		since, _ := time.Parse(time.RFC3339, r.URL.Query().Get("updated_after"))
		writeJSON(w, s.list(r, since, "per_page", issue))
	case strings.HasPrefix(rest, "/issues/"):
		parts := strings.Split(strings.TrimPrefix(rest, "/issues/"), "/")
		n, _ := strconv.ParseInt(parts[0], 10, 64)
		i := s.issues[n]
		if i == nil {
			http.NotFound(w, r)
			return
		}
		switch {
		case len(parts) == 1 && r.Method == http.MethodGet:
			writeJSON(w, issue(i))
		case len(parts) == 1 && r.Method == http.MethodPut:
			var p struct {
				StateEvent string `json:"state_event"`
				AddLabels  string `json:"add_labels"`
			}
			json.NewDecoder(r.Body).Decode(&p)
			if p.StateEvent == "close" {
				i.Closed, i.Updated = true, s.tick()
				s.system(n, "closed")
			}
			if p.AddLabels != "" {
				s.label(i, strings.Split(p.AddLabels, ",")...)
				s.system(n, "added ~"+p.AddLabels+" label")
			}
			writeJSON(w, issue(i))
		case len(parts) == 2 && parts[1] == "notes" && r.Method == http.MethodGet:
			writeJSON(w, s.commentsPage(r, n, "per_page", note))
		case len(parts) == 2 && parts[1] == "notes" && r.Method == http.MethodPost:
			if c := s.create(w, r, i); c != nil {
				w.WriteHeader(http.StatusCreated)
				writeJSON(w, note(c))
			}
		case len(parts) == 3 && parts[1] == "notes" && r.Method == http.MethodPut:
			id, _ := strconv.ParseInt(parts[2], 10, 64)
			if c := s.edit(w, r, id); c != nil {
				writeJSON(w, note(c))
			}
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

// list is a page of the issues updated since, with the label the request asks for.
func (s *Server) list(r *http.Request, since time.Time, sizeParam string, render func(*Issue) map[string]any) []map[string]any {
	label := r.URL.Query().Get("labels")
	var nums []int64
	for n, i := range s.issues {
		if !i.Updated.Before(since) && (label == "" || slices.Contains(i.Labels, label)) {
			nums = append(nums, n)
		}
	}
	slices.Sort(nums)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get(sizeParam))
	out := []map[string]any{}
	for k, n := range nums {
		if k >= (page-1)*size && k < page*size {
			out = append(out, render(s.issues[n]))
		}
	}
	return out
}

func (s *Server) commentsPage(r *http.Request, n int64, sizeParam string, render func(*Comment) map[string]any) []map[string]any {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get(sizeParam))
	out := []map[string]any{}
	k := 0
	for _, c := range s.comments {
		if c.Issue == n {
			if k >= (page-1)*size && k < page*size {
				out = append(out, render(c))
			}
			k++
		}
	}
	return out
}

// create adds the bot's comment the request carries; nil when the answer was already written (a lost response).
func (s *Server) create(w http.ResponseWriter, r *http.Request, i *Issue) *Comment {
	var p struct{ Body string }
	json.NewDecoder(r.Body).Decode(&p)
	s.next++
	c := &Comment{ID: s.next, Issue: i.Number, Body: p.Body, Author: s.Bot, Updated: s.tick()}
	s.comments = append(s.comments, c)
	i.Updated = s.clock
	if s.LoseCreate {
		s.LoseCreate = false
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return nil
	}
	return c
}

// edit changes comment id to the request's body; nil (and 404 written) when it is gone.
func (s *Server) edit(w http.ResponseWriter, r *http.Request, id int64) *Comment {
	i := slices.IndexFunc(s.comments, func(c *Comment) bool { return c.ID == id })
	if i < 0 {
		http.NotFound(w, r)
		return nil
	}
	var p struct{ Body string }
	json.NewDecoder(r.Body).Decode(&p)
	s.comments[i].Body, s.comments[i].Updated = p.Body, s.tick()
	return s.comments[i]
}

func (s *Server) label(i *Issue, labels ...string) {
	for _, l := range labels {
		if !slices.Contains(i.Labels, l) {
			i.Labels = append(i.Labels, l)
		}
	}
	i.Updated = s.tick()
}

// system adds GitLab's own note about a change to issue n.
func (s *Server) system(n int64, body string) {
	s.next++
	s.comments = append(s.comments, &Comment{ID: s.next, Issue: n, Body: body, Author: s.Bot, Updated: s.clock, System: true})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
