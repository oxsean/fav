package tracker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// gitea speaks Gitea's API, and GitHub's, which it follows: they differ in where the API is, how the token goes and
// how large a page is.
type gitea struct {
	rest
	page      int    // Gitea's default MAX_RESPONSE_ITEMS is 50; GitHub's per_page is 100 at most
	pageParam string // limit | per_page
	subIssues bool   // GitHub's
	// makeLabels: Gitea's. It leaves a label the repository lacks off an issue and still answers 200; GitHub makes it.
	makeLabels bool
}

// ⚠️ The colour of a label tend makes on Gitea, which requires one.
const labelColour = "#0e8a16"

func newGitea(cfg Config) *gitea {
	g := &gitea{rest: rest{cfg: cfg, api: cfg.Base + "/api/v1", accept: "application/json"}, page: 50, pageParam: "limit", makeLabels: true}
	g.auth = func(h http.Header) { h.Set("Authorization", "token "+cfg.Token) }
	return g
}

func newGitHub(cfg Config) *gitea {
	api := cfg.Base + "/api/v3"
	if cfg.Base == "https://github.com" {
		api = "https://api.github.com"
	}
	g := &gitea{rest: rest{cfg: cfg, api: api, accept: "application/vnd.github+json"}, page: 100, pageParam: "per_page", subIssues: true}
	g.auth = func(h http.Header) {
		h.Set("Authorization", "Bearer "+cfg.Token)
		h.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	return g
}

type giteaUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

type giteaIssue struct {
	ID          int64                   `json:"id"`
	Number      int64                   `json:"number"`
	Title       string                  `json:"title"`
	Body        string                  `json:"body"`
	State       string                  `json:"state"`
	Labels      []struct{ Name string } `json:"labels"`
	Assignees   []giteaUser             `json:"assignees"`
	HTMLURL     string                  `json:"html_url"`
	UpdatedAt   time.Time               `json:"updated_at"`
	PullRequest *struct{}               `json:"pull_request"`
}

type giteaComment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	User      giteaUser `json:"user"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (g *gitea) repoPath(rest string) string { return "/repos/" + g.cfg.Repo + rest }

func (g *gitea) Me(ctx context.Context) (string, error) {
	var u giteaUser
	_, err := g.do(ctx, http.MethodGet, "/user", nil, nil, &u)
	return u.Login, err
}

func (g *gitea) Repo(ctx context.Context) (Repo, error) {
	var r struct {
		ID            int64  `json:"id"`
		FullName      string `json:"full_name"`
		HTMLURL       string `json:"html_url"`
		DefaultBranch string `json:"default_branch"`
	}
	_, err := g.do(ctx, http.MethodGet, g.repoPath(""), nil, nil, &r)
	return Repo{ID: r.ID, FullName: r.FullName, URL: r.HTMLURL, DefaultBranch: r.DefaultBranch}, err
}

func (i giteaIssue) issue() Issue {
	out := Issue{ID: i.ID, Number: i.Number, Title: i.Title, Body: i.Body, Closed: i.State == "closed", URL: i.HTMLURL, UpdatedAt: i.UpdatedAt}
	for _, l := range i.Labels {
		out.Labels = append(out.Labels, l.Name)
	}
	for _, a := range i.Assignees {
		out.Assignees = append(out.Assignees, a.Login)
	}
	return out
}

func (g *gitea) Issues(ctx context.Context, since time.Time, label, etag string) (Page, error) {
	var out Page
	for page := 1; ; page++ {
		q := url.Values{"state": {"all"}, "type": {"issues"}, g.pageParam: {strconv.Itoa(g.page)}, "page": {strconv.Itoa(page)}}
		if !since.IsZero() {
			q.Set("since", since.UTC().Format(time.RFC3339))
		}
		if label != "" {
			q.Set("labels", label)
		}
		h := http.Header{}
		if page == 1 && etag != "" {
			h.Set("If-None-Match", etag)
		}
		var batch []giteaIssue
		res, err := g.do(ctx, http.MethodGet, g.repoPath("/issues?"+q.Encode()), h, nil, &batch)
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
			if i.PullRequest == nil {
				out.Issues = append(out.Issues, i.issue())
			}
		}
		if len(batch) < g.page {
			return out, nil
		}
	}
}

func (g *gitea) Issue(ctx context.Context, number int64) (Issue, error) {
	var i giteaIssue
	_, err := g.do(ctx, http.MethodGet, g.repoPath("/issues/"+strconv.FormatInt(number, 10)), nil, nil, &i)
	return i.issue(), err
}

func (g *gitea) Comments(ctx context.Context, number int64) ([]Comment, error) {
	var out []Comment
	for page := 1; ; page++ {
		var batch []giteaComment
		q := url.Values{g.pageParam: {strconv.Itoa(g.page)}, "page": {strconv.Itoa(page)}}
		if _, err := g.do(ctx, http.MethodGet, g.repoPath("/issues/"+strconv.FormatInt(number, 10)+"/comments?"+q.Encode()), nil, nil, &batch); err != nil {
			return nil, err
		}
		for _, c := range batch {
			out = append(out, Comment{ID: c.ID, Body: c.Body, Author: c.User.Login, UpdatedAt: c.UpdatedAt})
		}
		if len(batch) < g.page {
			return out, nil
		}
	}
}

func (g *gitea) CreateComment(ctx context.Context, number int64, body string) (Comment, error) {
	var c giteaComment
	_, err := g.do(ctx, http.MethodPost, g.repoPath("/issues/"+strconv.FormatInt(number, 10)+"/comments"), nil, map[string]string{"body": body}, &c)
	return Comment{ID: c.ID, Body: c.Body, Author: c.User.Login, UpdatedAt: c.UpdatedAt}, err
}

func (g *gitea) EditComment(ctx context.Context, _, id int64, body string) error {
	_, err := g.do(ctx, http.MethodPatch, g.repoPath("/issues/comments/"+strconv.FormatInt(id, 10)), nil, map[string]string{"body": body}, nil)
	return err
}

func (g *gitea) Close(ctx context.Context, number int64) error {
	_, err := g.do(ctx, http.MethodPatch, g.repoPath("/issues/"+strconv.FormatInt(number, 10)), nil, map[string]string{"state": "closed"}, nil)
	return err
}

// Label adds label to issue number and checks the answer carries it.
func (g *gitea) Label(ctx context.Context, number int64, label string) error {
	var add any = label
	if g.makeLabels {
		id, err := g.labelID(ctx, label)
		if err != nil {
			return err
		}
		add = id
	}
	var on []struct{ Name string }
	if _, err := g.do(ctx, http.MethodPost, g.repoPath("/issues/"+strconv.FormatInt(number, 10)+"/labels"), nil, map[string][]any{"labels": {add}}, &on); err != nil {
		return err
	}
	if !slices.ContainsFunc(on, func(l struct{ Name string }) bool { return l.Name == label }) {
		return fmt.Errorf("the tracker left label %q off issue #%d", label, number)
	}
	return nil
}

// labelID is the id of the repository's label name, made when the repository lacks it.
func (g *gitea) labelID(ctx context.Context, name string) (int64, error) {
	for page := 1; ; page++ {
		var batch []struct {
			ID   int64
			Name string
		}
		q := url.Values{g.pageParam: {strconv.Itoa(g.page)}, "page": {strconv.Itoa(page)}}
		if _, err := g.do(ctx, http.MethodGet, g.repoPath("/labels?"+q.Encode()), nil, nil, &batch); err != nil {
			return 0, err
		}
		for _, l := range batch {
			if l.Name == name {
				return l.ID, nil
			}
		}
		if len(batch) < g.page {
			break
		}
	}
	var l struct{ ID int64 }
	_, err := g.do(ctx, http.MethodPost, g.repoPath("/labels"), nil, map[string]string{"name": name, "color": labelColour}, &l)
	return l.ID, err
}

func (g *gitea) CreateIssue(ctx context.Context, title, body string) (Issue, error) {
	var i giteaIssue
	_, err := g.do(ctx, http.MethodPost, g.repoPath("/issues"), nil, map[string]string{"title": title, "body": body}, &i)
	return i.issue(), err
}

func (g *gitea) LinkSubIssue(ctx context.Context, parent int64, child Issue) error {
	if !g.subIssues {
		return nil
	}
	_, err := g.do(ctx, http.MethodPost, g.repoPath("/issues/"+strconv.FormatInt(parent, 10)+"/sub_issues"), nil, map[string]int64{"sub_issue_id": child.ID}, nil)
	return err
}

type giteaPull struct {
	Number  int64  `json:"number"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

func (g *gitea) PullRequest(ctx context.Context, head string) (PullRequest, error) {
	for page := 1; ; page++ {
		q := url.Values{"state": {"open"}, g.pageParam: {strconv.Itoa(g.page)}, "page": {strconv.Itoa(page)}}
		if g.subIssues { // GitHub filters by owner:branch; Gitea lists them all
			q.Set("head", strings.Split(g.cfg.Repo, "/")[0]+":"+head)
		}
		var batch []giteaPull
		if _, err := g.do(ctx, http.MethodGet, g.repoPath("/pulls?"+q.Encode()), nil, nil, &batch); err != nil {
			return PullRequest{}, err
		}
		for _, p := range batch {
			if p.Head.Ref == head {
				return PullRequest{Number: p.Number, URL: p.HTMLURL}, nil
			}
		}
		if len(batch) < g.page {
			return PullRequest{}, ErrNotFound
		}
	}
}

func (g *gitea) OpenPullRequest(ctx context.Context, head, base, title, body string) (PullRequest, error) {
	var p giteaPull
	_, err := g.do(ctx, http.MethodPost, g.repoPath("/pulls"), nil, map[string]string{"head": head, "base": base, "title": title, "body": body}, &p)
	return PullRequest{Number: p.Number, URL: p.HTMLURL}, err
}
