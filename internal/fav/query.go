package fav

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

type Query struct {
	Tags     []string
	Words    []string
	Project  string
	Provider string
	Status   string // open (default: unarchived, any status) | active (todo+doing) | done | todo | doing | archived | live | trash | all; only archived/all include archived
	All      bool   // include non-favorites; default is favorites only
	// decides status:live; nil matches nothing
	Live    func(sessionID string) bool
	Turns   int       // non-favorited sessions need at least this many turns; favorites are exempt
	After   time.Time // after: / before: — when the session started
	Before  time.Time
	Active  time.Time // last: — active since (a session started last week and continued today counts)
	File    string    // file: — the AI wrote a file whose path contains this (lowercase)
	Unknown []string  // unknown qualifiers, already matched as plain keywords
}

var DefaultTurns = 3

// Scope is q without its tag, project, source and keyword filters: what a picker counts.
func (q Query) Scope() Query {
	q.Tags, q.Words, q.Project, q.Provider = nil, nil, "", ""
	return q
}

func Parse(s string) Query {
	q := Query{Status: StatusOpen, Turns: DefaultTurns}
	now := time.Now()
	for f := range strings.FieldsSeq(s) {
		low := strings.ToLower(f)
		if after, ok := strings.CutPrefix(low, "#"); ok {
			if t := after; t != "" {
				q.Tags = append(q.Tags, t)
			}
			continue
		}
		k, v, isKV := strings.Cut(low, ":")
		if !isKV || v == "" {
			q.Words = append(q.Words, low)
			continue
		}
		switch k {
		case "project", "p":
			q.Project = v
		case "provider", "source":
			q.Provider = v
		case "file":
			q.File = v
		case "status":
			q.Status = normalizeStatus(v)
		case "turns":
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				q.Turns = n
			} else {
				q.unknown(low)
			}
		case "after", "since":
			if t, ok := ParseWhen(v, now); ok {
				q.After = t
			} else {
				q.unknown(low)
			}
		case "before", "until":
			if t, ok := ParseWhen(v, now); ok {
				q.Before = t
			} else {
				q.unknown(low)
			}
		case "last":
			if t, ok := ParseWhen(v, now); ok {
				q.Active = t
			} else {
				q.unknown(low)
			}
		default:
			q.unknown(low)
		}
	}
	return q
}

func (q *Query) unknown(tok string) {
	q.Unknown = append(q.Unknown, tok)
	q.Words = append(q.Words, tok)
}

// StatusActive = todo + doing; StatusOpen = any board status, unarchived.
const (
	StatusActive = "active"
	StatusOpen   = "open"
	StatusTrash  = "trash" // trash rows do not live in the store; the UI reads them from the manifest
	StatusAgent  = "agent" // one-shot SDK / exec / sub-agent sessions, kept out of every other listing
	StatusLive   = "live"  // running now: Query.Live decides
)

func normalizeStatus(v string) string {
	switch v {
	case StatusTodo, StatusDoing, StatusDone, "archived":
		return v
	case "archive":
		return "archived"
	case "completed", "complete", "finished":
		return StatusDone
	case "all", "any", "*":
		return "all"
	case StatusLive, "running":
		return StatusLive
	case StatusActive:
		return StatusActive
	case StatusOpen, "unarchived":
		return StatusOpen
	case StatusTrash, "deleted":
		return StatusTrash
	case StatusAgent, "sdk", "agents":
		return StatusAgent
	default:
		return StatusOpen
	}
}

// ParseWhen accepts absolute dates (2026-09-01 / 09-01 / 9-1, current year) and relative 7d / 2w / 3m / 24h.
func ParseWhen(v string, now time.Time) (time.Time, bool) {
	if t, ok := ParseDay(v, now); ok {
		return t, true
	}
	if len(v) < 2 {
		return time.Time{}, false
	}
	n, err := strconv.Atoi(v[:len(v)-1])
	if err != nil || n < 0 {
		return time.Time{}, false
	}
	var d time.Duration
	switch v[len(v)-1] {
	case 'h':
		d = time.Duration(n) * time.Hour
	case 'd':
		d = time.Duration(n) * 24 * time.Hour
	case 'w':
		d = time.Duration(n) * 7 * 24 * time.Hour
	case 'm':
		d = time.Duration(n) * 30 * 24 * time.Hour
	case 'y':
		d = time.Duration(n) * 365 * 24 * time.Hour
	default:
		return time.Time{}, false
	}
	return now.Add(-d), true
}

func ParseDay(v string, now time.Time) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02", "2006-1-2"} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t, true
		}
	}
	for _, layout := range []string{"01-02", "1-2"} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return time.Date(now.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local), true
		}
	}
	return time.Time{}, false
}

func (q Query) Match(r *Rec) bool {
	if !q.matchStatus(r) {
		return false
	}
	for _, t := range q.Tags {
		if !hasTag(r.Tags, t) {
			return false
		}
	}
	if q.Project != "" && !strings.EqualFold(r.Project, q.Project) {
		return false
	}
	if q.Provider != "" && !strings.EqualFold(r.Provider, q.Provider) {
		return false
	}
	if r.ID == "" && r.Turns < q.Turns && q.Status != StatusLive && q.Status != StatusAgent {
		return false
	}
	if !q.After.IsZero() && r.When().Before(q.After) {
		return false
	}
	if !q.Before.IsZero() && r.When().After(q.Before) {
		return false
	}
	if !q.Active.IsZero() && r.ActiveAt().Before(q.Active) {
		return false
	}
	if q.File != "" && !r.wrote(q.File) {
		return false
	}
	for _, w := range q.Words {
		if !strings.Contains(r.hay, w) {
			return false
		}
	}
	return true
}

func (q Query) matchStatus(r *Rec) bool {
	if r.Deleted || (!q.All && !r.Favorite()) {
		return false
	}
	switch q.Status {
	case "all":
		return true
	case StatusLive:
		return q.Live != nil && q.Live(r.SessionID)
	case "archived":
		return r.Archived()
	case StatusTrash:
		return false
	case StatusAgent:
		return true
	}
	if r.Archived() {
		return false
	}
	switch q.Status {
	case StatusActive:
		return r.Status == StatusTodo || r.Status == StatusDoing
	case StatusOpen:
		return true
	default:
		return r.Status == q.Status
	}
}

// ReplaceTokens drops the tokens of query that same picks out and appends add.
func ReplaceTokens(query string, add []string, same func(string) bool) string {
	var kept []string
	for t := range strings.FieldsSeq(query) {
		if !same(t) {
			kept = append(kept, t)
		}
	}
	return strings.TrimSpace(strings.Join(append(kept, add...), " "))
}

// HasPrefix matches a token starting with one of the lowercase prefixes, in any case.
func HasPrefix(prefixes ...string) func(string) bool {
	return func(t string) bool {
		low := strings.ToLower(t)
		for _, p := range prefixes {
			if strings.HasPrefix(low, p) {
				return true
			}
		}
		return false
	}
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

func (s *Store) Query(q Query) []*Rec {
	out := make([]*Rec, 0, 64)
	for _, r := range s.recs {
		if q.Match(r) {
			out = append(out, r)
		}
	}
	return out
}

type Count struct {
	Name string
	N    int
}

// CountBy counts the non-empty keys of recs, most frequent first, then by name.
func CountBy(recs []*Rec, keys func(*Rec) []string) []Count {
	counts := map[string]int{}
	for _, r := range recs {
		for _, k := range keys(r) {
			if k != "" {
				counts[k]++
			}
		}
	}
	out := make([]Count, 0, len(counts))
	for k, n := range counts {
		out = append(out, Count{k, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Name < out[j].Name
	})
	return out
}
