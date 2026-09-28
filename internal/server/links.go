package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tracker"
	"github.com/oxsean/fav/internal/wire"
)

// maxIssueBody is how much of a subtask's brief its sub-issue carries; lostReach is how far back a sub-issue whose
// answer got lost is looked for, wide enough for the tracker's clock to differ from ours.
const (
	maxIssueBody = 4000
	lostReach    = 24 * time.Hour
)

func subMarker(instance, taskID string) string {
	return "<!-- tend:subtask " + instance + " " + taskID + " -->"
}

// links opens what x's settings ask for: a sub-issue for each subtask of a task an issue of x stands for, and a pull
// request from a requirement's branch. A failure is that issue's, tried again a minute later.
func (s *Syncer) links(ctx context.Context, x store.Tracker, tr tracker.Tracker, set TrackerSettings) error {
	if !set.SubIssues && !set.PR {
		return nil
	}
	rows, err := s.team.TrackerIssues(x.ID, false)
	if err != nil {
		return err
	}
	type kid struct {
		parent           int64
		id, title, brief string
	}
	type pull struct {
		row               store.TrackerIssue
		head, base, title string
	}
	var kids []kid
	var pulls []pull
	s.coord.Read(func(st *task.State) {
		mirrored := map[string]bool{}
		for _, r := range rows {
			if r.Task != "" {
				mirrored[r.Task] = true
			}
		}
		for _, r := range rows {
			t := st.Tasks[r.Task]
			if t == nil {
				continue
			}
			if set.SubIssues {
				for _, k := range st.Children(t.ID) {
					if !mirrored[k.ID] && !task.Finished(k.Status) {
						kids = append(kids, kid{parent: r.Number, id: k.ID, title: k.Title, brief: k.Brief})
					}
				}
			}
			if set.PR && r.Parent == 0 && r.PR == "" {
				if head, base, ok := s.branch(st, t); ok {
					pulls = append(pulls, pull{row: r, head: head, base: base, title: t.Title})
				}
			}
		}
	})
	for _, k := range kids {
		if s.waiting(x, k.parent) {
			continue
		}
		if err := s.subIssue(ctx, x, tr, k.parent, k.id, k.title, k.brief); err != nil {
			if isStop(err) {
				return err
			}
			s.later(x, k.parent, err)
		}
	}
	for _, p := range pulls {
		if s.waiting(x, p.row.Number) {
			continue
		}
		if err := s.pullRequest(ctx, x, tr, p.row, p.head, p.base, p.title); err != nil {
			if isStop(err) {
				return err
			}
			s.later(x, p.row.Number, err)
		}
	}
	return nil
}

func (s *Syncer) waiting(x store.Tracker, number int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now().Before(s.retry[x.ID+"#"+fmt.Sprint(number)])
}

// later records err on issue number of x and leaves it a minute.
func (s *Syncer) later(x store.Tracker, number int64, err error) {
	s.mu.Lock()
	s.retry[x.ID+"#"+fmt.Sprint(number)] = s.now().Add(issueRetry)
	s.mu.Unlock()
	row, rerr := s.team.TrackerIssue(x.ID, number)
	if rerr != nil {
		return
	}
	row.LastError = err.Error()
	s.team.PutTrackerIssue(row)
}

// subIssue opens the sub-issue of issue parent for task id; one whose answer got lost is found again by its marker.
func (s *Syncer) subIssue(ctx context.Context, x store.Tracker, tr tracker.Tracker, parent int64, id, title, brief string) error {
	mark := subMarker(s.coord.ID(), id)
	var made *tracker.Issue
	s.mu.Lock()
	since, unsure := s.unsure[id]
	s.mu.Unlock()
	if unsure {
		page, err := tr.Issues(ctx, since.Add(-lostReach), "", "")
		if err != nil {
			return err
		}
		for k, i := range page.Issues {
			if strings.Contains(i.Body, mark) {
				made = &page.Issues[k]
				break
			}
		}
	}
	if made == nil {
		body := strings.TrimSpace(brief)
		if len(body) > maxIssueBody {
			body = body[:maxIssueBody] + "…"
		}
		if body != "" {
			body += "\n\n"
		}
		body += fmt.Sprintf("Part of #%d.\n\n%s\n", parent, mark)
		s.mu.Lock()
		s.unsure[id] = s.now()
		s.mu.Unlock()
		i, err := tr.CreateIssue(ctx, title, body)
		if err != nil {
			return err
		}
		made = &i
	}
	if err := tr.LinkSubIssue(ctx, parent, *made); err != nil {
		return err
	}
	if err := s.team.PutTrackerIssue(store.TrackerIssue{Tracker: x.ID, Number: made.Number, Task: id, Parent: parent, Dirty: true}); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.unsure, id)
	s.mu.Unlock()
	return s.link(ctx, task.Linked{ID: id, Issue: made.URL})
}

// pullRequest opens a pull request from head into base (default: the repository's default branch) for row's
// requirement, or takes the one already open from head.
func (s *Syncer) pullRequest(ctx context.Context, x store.Tracker, tr tracker.Tracker, row store.TrackerIssue, head, base, title string) error {
	pr, err := tr.PullRequest(ctx, head)
	if errors.Is(err, tracker.ErrNotFound) {
		if base == "" {
			repo, rerr := tr.Repo(ctx)
			if rerr != nil {
				return rerr
			}
			base = repo.DefaultBranch
		}
		pr, err = tr.OpenPullRequest(ctx, head, base, title, fmt.Sprintf("For #%d, from tend.\n", row.Number))
	}
	if err != nil {
		return err
	}
	row, err = s.team.TrackerIssue(x.ID, row.Number)
	if err != nil {
		return err
	}
	row.PR, row.LastError, row.Dirty = pr.URL, "", true
	if err := s.team.PutTrackerIssue(row); err != nil {
		return err
	}
	return s.link(ctx, task.Linked{ID: row.Task, PR: pr.URL})
}

func (s *Syncer) link(ctx context.Context, l task.Linked) error {
	b, _ := json.Marshal(l)
	_, err := s.do(ctx, &wire.Request{Method: coord.MTaskLink, CommandID: "link-" + journal.Digest(b), Params: b})
	return err
}

// pullBranch is the branch a pull request for t goes from and into: t's own, once runs pushed commits to it and t
// awaits acceptance or is done.
func pullBranch(st *task.State, t *task.Task) (head, base string, ok bool) {
	if sit := st.Situation(t); t.Status != task.StatusDone && sit.Reason != task.WhyAccept && sit.Reason != task.WhyEnded {
		return "", "", false
	}
	head = task.BranchOf(t.ID)
	pushed, commits := false, false
	for _, k := range st.Subtree(t.ID) {
		for _, r := range st.RunsOf(k.ID) {
			if r.Work == nil || r.Work.Branch != head || r.Work.Remote == "" {
				continue
			}
			pushed = true
			if k.ID == t.ID && r.Work.Base != "" {
				base = r.Work.Base
			}
			if r.Worked != nil && (r.Worked.Commits > 0 || r.Worked.Merged) {
				commits = true
			}
		}
	}
	return head, base, pushed && commits
}
