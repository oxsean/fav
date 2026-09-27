// Package giteatest is a Gitea for tests: one repository's issues and comments behind the API paths the tracker uses,
// with faults to inject.
package giteatest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
}

type Comment struct {
	ID      int64
	Issue   int64
	Body    string
	Author  string
	Updated time.Time
}

// Server holds one repository, owner/name with id RepoID, and the bot account Bot with token Token.
type Server struct {
	*httptest.Server
	Repo, Bot, Token string
	RepoID           int64

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

func New(repo, bot, token string) *Server {
	s := &Server{Repo: repo, Bot: bot, Token: token, RepoID: 42, issues: map[int64]*Issue{}, next: 100,
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

// CommentsOf are copies of issue number's comments.
func (s *Server) CommentsOf(number int64) []Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Comment
	for _, c := range s.comments {
		if c.Issue == number {
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

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests[r.Method+" "+r.URL.Path]++
	switch {
	case s.Refuse || r.Header.Get("Authorization") != "token "+s.Token:
		http.Error(w, `{"message":"token is required"}`, http.StatusUnauthorized)
		return
	case s.RateLimit > 0:
		s.RateLimit--
		w.Header().Set("Retry-After", "30")
		http.Error(w, `{"message":"rate limit"}`, http.StatusTooManyRequests)
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
		return map[string]any{"number": i.Number, "title": i.Title, "body": i.Body, "state": state, "labels": labels, "assignees": as,
			"html_url": fmt.Sprintf("%s/%s/issues/%d", s.URL, s.Repo, i.Number), "updated_at": i.Updated}
	}
	comment := func(c *Comment) map[string]any {
		return map[string]any{"id": c.ID, "body": c.Body, "user": user(c.Author), "updated_at": c.Updated}
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/repos/"+s.Repo)
	switch {
	case r.URL.Path == "/api/v1/user":
		writeJSON(w, user(s.Bot))
	case !ok:
		http.NotFound(w, r)
	case rest == "":
		writeJSON(w, map[string]any{"id": s.RepoID, "full_name": s.Repo, "html_url": s.URL + "/" + s.Repo})
	case rest == "/issues" && r.Method == http.MethodGet:
		since, _ := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
		label := r.URL.Query().Get("labels")
		var nums []int64
		for n, i := range s.issues {
			if !i.Updated.Before(since) && (label == "" || slices.Contains(i.Labels, label)) {
				nums = append(nums, n)
			}
		}
		slices.Sort(nums)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		out := []map[string]any{}
		for k, n := range nums {
			if k >= (page-1)*limit && k < page*limit {
				out = append(out, issue(s.issues[n]))
			}
		}
		writeJSON(w, out)
	case strings.HasPrefix(rest, "/issues/comments/") && r.Method == http.MethodPatch:
		id, _ := strconv.ParseInt(strings.TrimPrefix(rest, "/issues/comments/"), 10, 64)
		i := slices.IndexFunc(s.comments, func(c *Comment) bool { return c.ID == id })
		if i < 0 {
			http.NotFound(w, r)
			return
		}
		var p struct{ Body string }
		json.NewDecoder(r.Body).Decode(&p)
		s.comments[i].Body, s.comments[i].Updated = p.Body, s.tick()
		writeJSON(w, comment(s.comments[i]))
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
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			out := []map[string]any{}
			k := 0
			for _, c := range s.comments {
				if c.Issue == n {
					if k >= (page-1)*limit && k < page*limit {
						out = append(out, comment(c))
					}
					k++
				}
			}
			writeJSON(w, out)
		case parts[1] == "comments" && r.Method == http.MethodPost:
			var p struct{ Body string }
			json.NewDecoder(r.Body).Decode(&p)
			s.next++
			c := &Comment{ID: s.next, Issue: n, Body: p.Body, Author: s.Bot, Updated: s.tick()}
			s.comments = append(s.comments, c)
			i.Updated = s.clock
			if s.LoseCreate {
				s.LoseCreate = false
				http.Error(w, "bad gateway", http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, comment(c))
		case parts[1] == "labels" && r.Method == http.MethodPost:
			var p struct{ Labels []string }
			json.NewDecoder(r.Body).Decode(&p)
			for _, l := range p.Labels {
				if !slices.Contains(i.Labels, l) {
					i.Labels = append(i.Labels, l)
				}
			}
			i.Updated = s.tick()
			writeJSON(w, []any{})
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
