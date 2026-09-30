package tracker

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// gitlabPage is how many items a page of GitLab's API holds (its maximum per_page is 100).
const gitlabPage = 100

// gitlab speaks GitLab's REST API (v4): a project by its URL-encoded path, issues by iid, comments as notes.
type gitlab struct{ rest }

type gitlabUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type gitlabIssue struct {
	ID          int64        `json:"id"`
	IID         int64        `json:"iid"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	State       string       `json:"state"` // opened | closed
	Labels      []string     `json:"labels"`
	Assignees   []gitlabUser `json:"assignees"`
	WebURL      string       `json:"web_url"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

type gitlabNote struct {
	ID        int64      `json:"id"`
	Body      string     `json:"body"`
	Author    gitlabUser `json:"author"`
	System    bool       `json:"system"` // written by GitLab itself: a label, a close
	UpdatedAt time.Time  `json:"updated_at"`
}

func newGitLab(cfg Config) *gitlab {
	g := &gitlab{rest{cfg: cfg, api: cfg.Base + "/api/v4", accept: "application/json"}}
	g.auth = func(h http.Header) { h.Set("PRIVATE-TOKEN", cfg.Token) }
	return g
}

func (g *gitlab) project(rest string) string { return "/projects/" + url.PathEscape(g.cfg.Repo) + rest }

func (g *gitlab) issuePath(number int64, rest string) string {
	return g.project("/issues/" + strconv.FormatInt(number, 10) + rest)
}

func (g *gitlab) Me(ctx context.Context) (string, error) {
	var u gitlabUser
	_, err := g.do(ctx, http.MethodGet, "/user", nil, nil, &u)
	return u.Username, err
}

func (g *gitlab) Repo(ctx context.Context) (Repo, error) {
	var r struct {
		ID            int64  `json:"id"`
		Path          string `json:"path_with_namespace"`
		WebURL        string `json:"web_url"`
		DefaultBranch string `json:"default_branch"`
	}
	_, err := g.do(ctx, http.MethodGet, g.project(""), nil, nil, &r)
	return Repo{ID: r.ID, FullName: r.Path, URL: r.WebURL, DefaultBranch: r.DefaultBranch}, err
}

func (i gitlabIssue) issue() Issue {
	out := Issue{ID: i.ID, Number: i.IID, Title: i.Title, Body: i.Description, Closed: i.State == "closed", Labels: i.Labels, URL: i.WebURL,
		UpdatedAt: i.UpdatedAt}
	for _, a := range i.Assignees {
		out.Assignees = append(out.Assignees, a.Username)
	}
	return out
}

func (g *gitlab) Issues(ctx context.Context, since time.Time, label, etag string) (Page, error) {
	var out Page
	for page := 1; ; page++ {
		q := url.Values{"scope": {"all"}, "per_page": {strconv.Itoa(gitlabPage)}, "page": {strconv.Itoa(page)}, "order_by": {"updated_at"},
			"sort": {"asc"}}
		if !since.IsZero() {
			q.Set("updated_after", since.UTC().Format(time.RFC3339))
		}
		if label != "" {
			q.Set("labels", label)
		}
		h := http.Header{}
		if page == 1 && etag != "" {
			h.Set("If-None-Match", etag)
		}
		var batch []gitlabIssue
		res, err := g.do(ctx, http.MethodGet, g.project("/issues?"+q.Encode()), h, nil, &batch)
		if err != nil {
			return Page{}, err
		}
		if page == 1 {
			if res.StatusCode == http.StatusNotModified {
				return Page{NotModified: true, ETag: etag}, nil
			}
			out.ETag = res.Header.Get("ETag")
		}
		for _, i := range batch {
			out.Issues = append(out.Issues, i.issue())
		}
		if len(batch) < gitlabPage {
			return out, nil
		}
	}
}

func (g *gitlab) Issue(ctx context.Context, number int64) (Issue, error) {
	var i gitlabIssue
	_, err := g.do(ctx, http.MethodGet, g.issuePath(number, ""), nil, nil, &i)
	return i.issue(), err
}

// StateEvents reads GitLab's resource state events.
func (g *gitlab) StateEvents(ctx context.Context, number int64, since time.Time) ([]StateEvent, error) {
	var out []StateEvent
	for page := 1; ; page++ {
		var batch []struct {
			State     string    `json:"state"`
			CreatedAt time.Time `json:"created_at"`
		}
		q := url.Values{"per_page": {strconv.Itoa(gitlabPage)}, "page": {strconv.Itoa(page)}}
		if _, err := g.do(ctx, http.MethodGet, g.issuePath(number, "/resource_state_events?"+q.Encode()), nil, nil, &batch); err != nil {
			return nil, err
		}
		for _, e := range batch {
			switch e.State {
			case "closed":
				out = append(out, StateEvent{Closed: true, At: e.CreatedAt})
			case "reopened":
				out = append(out, StateEvent{At: e.CreatedAt})
			}
		}
		if len(batch) < gitlabPage {
			return after(out, since), nil
		}
	}
}

func (g *gitlab) Comments(ctx context.Context, number int64) ([]Comment, error) {
	var out []Comment
	for page := 1; ; page++ {
		var batch []gitlabNote
		q := url.Values{"per_page": {strconv.Itoa(gitlabPage)}, "page": {strconv.Itoa(page)}, "sort": {"asc"}, "order_by": {"created_at"}}
		if _, err := g.do(ctx, http.MethodGet, g.issuePath(number, "/notes?"+q.Encode()), nil, nil, &batch); err != nil {
			return nil, err
		}
		for _, n := range batch {
			if !n.System {
				out = append(out, Comment{ID: n.ID, Body: n.Body, Author: n.Author.Username, UpdatedAt: n.UpdatedAt})
			}
		}
		if len(batch) < gitlabPage {
			return out, nil
		}
	}
}

func (g *gitlab) CreateComment(ctx context.Context, number int64, body string) (Comment, error) {
	var n gitlabNote
	_, err := g.do(ctx, http.MethodPost, g.issuePath(number, "/notes"), nil, map[string]string{"body": body}, &n)
	return Comment{ID: n.ID, Body: n.Body, Author: n.Author.Username, UpdatedAt: n.UpdatedAt}, err
}

func (g *gitlab) EditComment(ctx context.Context, number, id int64, body string) error {
	_, err := g.do(ctx, http.MethodPut, g.issuePath(number, "/notes/"+strconv.FormatInt(id, 10)), nil, map[string]string{"body": body}, nil)
	return err
}

func (g *gitlab) Close(ctx context.Context, number int64) error {
	_, err := g.do(ctx, http.MethodPut, g.issuePath(number, ""), nil, map[string]string{"state_event": "close"}, nil)
	return err
}

func (g *gitlab) Reopen(ctx context.Context, number int64) error {
	_, err := g.do(ctx, http.MethodPut, g.issuePath(number, ""), nil, map[string]string{"state_event": "reopen"}, nil)
	return err
}

func (g *gitlab) Unlabel(ctx context.Context, number int64, label string) error {
	_, err := g.do(ctx, http.MethodPut, g.issuePath(number, ""), nil, map[string]string{"remove_labels": label}, nil)
	return err
}

func (g *gitlab) Label(ctx context.Context, number int64, label string) error {
	_, err := g.do(ctx, http.MethodPut, g.issuePath(number, ""), nil, map[string]string{"add_labels": label}, nil)
	return err
}

func (g *gitlab) CreateIssue(ctx context.Context, title, body string) (Issue, error) {
	var i gitlabIssue
	_, err := g.do(ctx, http.MethodPost, g.project("/issues"), nil, map[string]string{"title": title, "description": body}, &i)
	return i.issue(), err
}

func (g *gitlab) LinkSubIssue(context.Context, int64, Issue) error { return nil }

type gitlabMerge struct {
	IID    int64  `json:"iid"`
	WebURL string `json:"web_url"`
}

func (g *gitlab) PullRequest(ctx context.Context, head string) (PullRequest, error) {
	var batch []gitlabMerge
	q := url.Values{"state": {"opened"}, "source_branch": {head}}
	if _, err := g.do(ctx, http.MethodGet, g.project("/merge_requests?"+q.Encode()), nil, nil, &batch); err != nil {
		return PullRequest{}, err
	}
	if len(batch) == 0 {
		return PullRequest{}, ErrNotFound
	}
	return PullRequest{Number: batch[0].IID, URL: batch[0].WebURL}, nil
}

func (g *gitlab) OpenPullRequest(ctx context.Context, head, base, title, body string) (PullRequest, error) {
	var m gitlabMerge
	_, err := g.do(ctx, http.MethodPost, g.project("/merge_requests"), nil,
		map[string]string{"source_branch": head, "target_branch": base, "title": title, "description": body}, &m)
	return PullRequest{Number: m.IID, URL: m.WebURL}, err
}
