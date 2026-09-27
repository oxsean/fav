package tracker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// giteaPage is how many issues a page of Gitea's API holds at most (its default MAX_RESPONSE_ITEMS is 50).
const giteaPage = 50

type gitea struct{ cfg Config }

type giteaUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

type giteaIssue struct {
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

func (g *gitea) repoPath(rest string) string { return "/api/v1/repos/" + g.cfg.Repo + rest }

// do sends a request with a JSON body (when in is not nil) and decodes a JSON answer into out (when not nil).
func (g *gitea) do(ctx context.Context, method, path string, header http.Header, in, out any) (*http.Response, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.cfg.Base+path, body)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Authorization", "token "+g.cfg.Token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := g.cfg.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode == http.StatusNotModified {
		return res, nil
	}
	if res.StatusCode/100 != 2 {
		return res, statusError(res, b, time.Now())
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return res, fmt.Errorf("tracker answer: %w", err)
		}
	}
	return res, nil
}

func (g *gitea) Me(ctx context.Context) (string, error) {
	var u giteaUser
	_, err := g.do(ctx, http.MethodGet, "/api/v1/user", nil, nil, &u)
	return u.Login, err
}

func (g *gitea) Repo(ctx context.Context) (Repo, error) {
	var r struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	}
	_, err := g.do(ctx, http.MethodGet, g.repoPath(""), nil, nil, &r)
	return Repo{ID: r.ID, FullName: r.FullName, URL: r.HTMLURL}, err
}

func (i giteaIssue) issue() Issue {
	out := Issue{Number: i.Number, Title: i.Title, Body: i.Body, Closed: i.State == "closed", URL: i.HTMLURL, UpdatedAt: i.UpdatedAt}
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
		q := url.Values{"state": {"all"}, "type": {"issues"}, "limit": {strconv.Itoa(giteaPage)}, "page": {strconv.Itoa(page)}}
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
		if len(batch) < giteaPage {
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
		q := url.Values{"limit": {strconv.Itoa(giteaPage)}, "page": {strconv.Itoa(page)}}
		if _, err := g.do(ctx, http.MethodGet, g.repoPath("/issues/"+strconv.FormatInt(number, 10)+"/comments?"+q.Encode()), nil, nil, &batch); err != nil {
			return nil, err
		}
		for _, c := range batch {
			out = append(out, Comment{ID: c.ID, Body: c.Body, Author: c.User.Login, UpdatedAt: c.UpdatedAt})
		}
		if len(batch) < giteaPage {
			return out, nil
		}
	}
}

func (g *gitea) CreateComment(ctx context.Context, number int64, body string) (Comment, error) {
	var c giteaComment
	_, err := g.do(ctx, http.MethodPost, g.repoPath("/issues/"+strconv.FormatInt(number, 10)+"/comments"), nil, map[string]string{"body": body}, &c)
	return Comment{ID: c.ID, Body: c.Body, Author: c.User.Login, UpdatedAt: c.UpdatedAt}, err
}

func (g *gitea) EditComment(ctx context.Context, id int64, body string) error {
	_, err := g.do(ctx, http.MethodPatch, g.repoPath("/issues/comments/"+strconv.FormatInt(id, 10)), nil, map[string]string{"body": body}, nil)
	return err
}

func (g *gitea) Close(ctx context.Context, number int64) error {
	_, err := g.do(ctx, http.MethodPatch, g.repoPath("/issues/"+strconv.FormatInt(number, 10)), nil, map[string]string{"state": "closed"}, nil)
	return err
}

func (g *gitea) Label(ctx context.Context, number int64, label string) error {
	_, err := g.do(ctx, http.MethodPost, g.repoPath("/issues/"+strconv.FormatInt(number, 10)+"/labels"), nil, map[string][]string{"labels": {label}}, nil)
	return err
}

// GiteaSigned: sig (X-Gitea-Signature) is the HMAC-SHA256 of body under secret.
func GiteaSigned(secret, body []byte, sig string) bool {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	want, err := hex.DecodeString(sig)
	return err == nil && hmac.Equal(m.Sum(nil), want)
}

// GiteaHook is what the sync worker needs of a Gitea webhook delivery: which repository and issue it is about.
type GiteaHook struct {
	Repository struct {
		ID int64 `json:"id"`
	} `json:"repository"`
	Issue struct {
		Number int64 `json:"number"`
	} `json:"issue"`
}
