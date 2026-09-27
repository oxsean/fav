// Package tracker speaks to issue trackers (Gitea, GitHub, GitLab): it reads a
// repository's issues and their comments, and writes one comment, a close or a label back. Only tend-server carries it.
package tracker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Kinds of tracker.
const (
	KindGitea  = "gitea"
	KindGitLab = "gitlab"
	KindGitHub = "github"
)

type Repo struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
	URL      string `json:"url"`
}

type Issue struct {
	Number    int64     `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Closed    bool      `json:"closed"`
	Labels    []string  `json:"labels,omitempty"`
	Assignees []string  `json:"assignees,omitempty"` // logins
	URL       string    `json:"url"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Comment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	Author    string    `json:"author"` // login
	UpdatedAt time.Time `json:"updated_at"`
}

// Page is the issues updated since a time; NotModified when the tracker says nothing changed since ETag.
type Page struct {
	Issues      []Issue
	ETag        string
	NotModified bool
}

// Tracker is one repository of one tracker, as one account (the bot) sees it.
type Tracker interface {
	Me(ctx context.Context) (string, error) // the account's login
	Repo(ctx context.Context) (Repo, error)
	// Issues are the issues (not pull requests) updated at or after since, with label when it is not empty.
	Issues(ctx context.Context, since time.Time, label, etag string) (Page, error)
	Issue(ctx context.Context, number int64) (Issue, error)
	Comments(ctx context.Context, number int64) ([]Comment, error)
	CreateComment(ctx context.Context, number int64, body string) (Comment, error)
	EditComment(ctx context.Context, number, id int64, body string) error // ErrNotFound when it was deleted
	Close(ctx context.Context, number int64) error
	Label(ctx context.Context, number int64, label string) error
}

// Config says which repository, where, and as whom.
type Config struct {
	Kind   string
	Base   string // https://git.example
	Repo   string // owner/name; group/…/name on GitLab
	Token  string
	Client *http.Client
}

var ErrNotFound = errors.New("not found")

// AuthError: the tracker refused the credential (401) or its rights (403 without a rate limit).
type AuthError struct {
	Status int
	Detail string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("tracker refused the credential (%d): %s", e.Status, e.Detail)
}

// RateLimited: the tracker asks to wait Wait before the next request.
type RateLimited struct{ Wait time.Duration }

func (e *RateLimited) Error() string { return "tracker rate limit: wait " + e.Wait.String() }

// New is the tracker cfg names.
func New(cfg Config) (Tracker, error) {
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 30 * time.Second}
	}
	cfg.Base = strings.TrimRight(cfg.Base, "/")
	parts := strings.Split(cfg.Repo, "/")
	if len(parts) < 2 || len(parts) > 2 && cfg.Kind != KindGitLab || slices.Contains(parts, "") { // GitLab nests groups
		return nil, fmt.Errorf("repository %q: owner/name", cfg.Repo)
	}
	switch cfg.Kind {
	case KindGitea:
		return newGitea(cfg), nil
	case KindGitHub:
		return newGitHub(cfg), nil
	case KindGitLab:
		return newGitLab(cfg), nil
	}
	return nil, fmt.Errorf("tracker kind %q is not supported yet", cfg.Kind)
}

// statusError turns an unsuccessful answer into the error the sync worker acts on.
func statusError(res *http.Response, body []byte, now time.Time) error {
	detail := strings.TrimSpace(string(body))
	if len(detail) > 300 {
		detail = detail[:300]
	}
	switch {
	case res.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case res.StatusCode == http.StatusTooManyRequests,
		res.StatusCode == http.StatusForbidden && (res.Header.Get("Retry-After") != "" || res.Header.Get("X-RateLimit-Remaining") == "0" || res.Header.Get("RateLimit-Remaining") == "0"):
		return &RateLimited{Wait: retryAfter(res.Header, now)}
	case res.StatusCode == http.StatusUnauthorized, res.StatusCode == http.StatusForbidden:
		return &AuthError{Status: res.StatusCode, Detail: detail}
	}
	return fmt.Errorf("tracker answered %s: %s", res.Status, detail)
}

// retryAfter is how long a rate-limited request waits: Retry-After (seconds or a date), X-RateLimit-Reset or RateLimit-Reset (Unix
// seconds), else a minute.
func retryAfter(h http.Header, now time.Time) time.Duration {
	if v := h.Get("Retry-After"); v != "" {
		if s, err := strconv.Atoi(v); err == nil {
			return time.Duration(s) * time.Second
		}
		if t, err := http.ParseTime(v); err == nil && t.After(now) {
			return t.Sub(now)
		}
	}
	for _, k := range []string{"X-RateLimit-Reset", "RateLimit-Reset"} { // GitHub, GitLab
		v := h.Get(k)
		if v == "" {
			continue
		}
		if s, err := strconv.ParseInt(v, 10, 64); err == nil && s > now.Unix() {
			return time.Unix(s, 0).Sub(now)
		}
	}
	return time.Minute
}
