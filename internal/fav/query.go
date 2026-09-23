package fav

import (
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
	Turns   int // non-favorited sessions need at least this many turns; favorites are exempt
	After   time.Time
	Before  time.Time
	Unknown []string // unknown qualifiers, already matched as plain keywords
}

var DefaultTurns = 3

func Parse(s string) Query {
	q := Query{Status: StatusOpen, Turns: DefaultTurns}
	now := time.Now()
	for _, f := range strings.Fields(s) {
		low := strings.ToLower(f)
		if strings.HasPrefix(low, "#") {
			if t := strings.TrimPrefix(low, "#"); t != "" {
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
				q.After = t
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
	case "live", "running":
		return "live"
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
	if r.ID == "" && r.Turns < q.Turns && q.Status != "live" && q.Status != StatusAgent {
		return false
	}
	if !q.After.IsZero() && r.When().Before(q.After) {
		return false
	}
	if !q.Before.IsZero() && r.When().After(q.Before) {
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
	case "live":
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
		return !r.Done()
	case StatusOpen:
		return true
	default:
		return r.Status == q.Status
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
