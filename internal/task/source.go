package task

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/oxsean/fav/internal/journal"
)

// Requirement source events.
const (
	ETaskSourced     = "task_sourced"      // SourceUpdate: the tracker's issue as the sync worker read it
	ETaskSourceAcked = "task_source_acked" // SourceAck: someone took or declined a newer revision, or saw the issue closed
	ETaskLinked      = "task_linked"       // Linked: the sync worker opened a sub-issue for the task or a pull request from its branch
)

// Linked is what the sync worker opened for a task; an empty field leaves the task's as it is.
type Linked struct {
	ID    string `json:"id"`
	Issue string `json:"issue,omitempty"`
	PR    string `json:"pr,omitempty"`
}

// KindRequirement is a task that stands for a requirement: a root, often from a tracker's issue.
const KindRequirement = "requirement"

// Why a task with a source waits.
const (
	WhySourceChanged  = "source_changed"  // waiting: its issue changed; take the new revision or keep this one
	WhySourceClosed   = "source_closed"   // waiting: its issue was closed outside tend
	WhySourceReopened = "source_reopened" // waiting: its issue was reopened outside tend after the task ended
)

// Source is the issue a requirement comes from, and which revision of it the task's scope is.
type Source struct {
	Kind    string `json:"kind"`    // gitea | gitlab | github
	Tracker string `json:"tracker"` // the binding that syncs it
	Base    string `json:"base"`    // the tracker's address: with RepoID and Number it names the issue
	Repo    string `json:"repo"`    // owner/name when last read
	RepoID  int64  `json:"repo_id"`
	Number  int64  `json:"number"`
	URL     string `json:"url,omitempty"`
	Rev     int    `json:"rev"`    // the revision the task's title and brief are
	Digest  string `json:"digest"` // of that revision
	// Seen is the digest of the latest revision read, SeenRev its number; a declined revision stays seen.
	Seen        string          `json:"seen"`
	SeenRev     int             `json:"seen_rev"`
	Pending     *SourceRevision `json:"pending,omitempty"` // a newer revision nobody took or declined yet
	Closed      bool            `json:"closed,omitempty"`
	ClosedAcked bool            `json:"closed_acked,omitempty"` // someone saw it closed and kept the task going
	Reopened    bool            `json:"reopened,omitempty"`     // it was reopened outside tend after the task ended
	FetchedAt   time.Time       `json:"fetched_at,omitzero"`
}

// SourceRevision is one reading of an issue.
type SourceRevision struct {
	Rev    int    `json:"rev"`
	Digest string `json:"digest"`
	Title  string `json:"title"`
	Text   string `json:"text"`
}

// SourceUpdate is what the sync worker read of a task's issue.
type SourceUpdate struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
	Title  string `json:"title"`
	Text   string `json:"text"`
	Closed bool   `json:"closed,omitempty"`
	// Reopened: the issue was reopened after it closed with the task finished; the task is reopened with it.
	Reopened bool   `json:"reopened,omitempty"`
	Repo     string `json:"repo,omitempty"`
	URL      string `json:"url,omitempty"`
}

// SourceAck takes (Accept) or declines a task's pending revision; with none pending it keeps the task going although
// its issue was closed.
type SourceAck struct {
	ID     string `json:"id"`
	Accept bool   `json:"accept,omitempty"`
}

// Clone is a copy of s that shares nothing with it.
func (s *Source) Clone() *Source {
	if s == nil {
		return nil
	}
	cp := *s
	if s.Pending != nil {
		p := *s.Pending
		cp.Pending = &p
	}
	return &cp
}

// SourceWaits is why t's source keeps it waiting, "" when it does not.
func SourceWaits(t *Task) string {
	switch src := t.Source; {
	case src == nil:
		return ""
	case src.Pending != nil:
		return WhySourceChanged
	case src.Closed && !src.ClosedAcked:
		return WhySourceClosed
	case src.Reopened:
		return WhySourceReopened
	}
	return ""
}

func (s *State) applySource(e journal.Event, at time.Time) (bool, error) {
	switch e.Type {
	case ETaskSourced:
		var d SourceUpdate
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		t := s.Tasks[d.ID]
		if t == nil || t.Source == nil {
			return true, fmt.Errorf("no sourced task %s", d.ID)
		}
		src := t.Source
		if d.Digest != "" && d.Digest != src.Seen {
			src.SeenRev++
			src.Seen = d.Digest
			src.Pending = &SourceRevision{Rev: src.SeenRev, Digest: d.Digest, Title: d.Title, Text: d.Text}
			if d.Digest == src.Digest {
				src.Pending = nil
			}
		}
		if d.Closed != src.Closed {
			src.Closed, src.ClosedAcked = d.Closed, false
		}
		src.Reopened = !d.Closed && (d.Reopened || src.Reopened)
		if d.Repo != "" {
			src.Repo = d.Repo
		}
		if d.URL != "" {
			src.URL = d.URL
		}
		src.FetchedAt = at
		t.Rev++
		t.UpdatedAt = at
	case ETaskLinked:
		var d Linked
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		t := s.Tasks[d.ID]
		if t == nil {
			return true, fmt.Errorf("no task %s", d.ID)
		}
		if d.Issue != "" {
			t.Issue = d.Issue
		}
		if d.PR != "" {
			t.PR = d.PR
		}
		t.Rev++
		t.UpdatedAt = at
	case ETaskSourceAcked:
		var d SourceAck
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		t := s.Tasks[d.ID]
		if t == nil || SourceWaits(t) == "" {
			return true, fmt.Errorf("nothing to acknowledge on %s", d.ID)
		}
		src := t.Source
		if p := src.Pending; p != nil {
			if d.Accept {
				t.Title, t.Brief = p.Title, p.Text
				src.Rev, src.Digest = p.Rev, p.Digest
			}
			src.Pending = nil
		} else if src.Closed {
			src.ClosedAcked = true
		} else {
			src.Reopened = false
		}
		t.Rev++
		t.UpdatedAt = at
	default:
		return false, nil
	}
	return true, nil
}
