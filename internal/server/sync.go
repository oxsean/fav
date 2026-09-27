package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tracker"
	"github.com/oxsean/fav/internal/wire"
)

// Sync timing: how often the worker looks, how far a scan reaches back past its cursor, how long a progress comment
// rests between edits, and how long an issue that failed waits before it is tried again.
const (
	syncTick    = 5 * time.Second
	scanOverlap = 2 * time.Minute
	editRest    = 30 * time.Second
	issueRetry  = time.Minute
)

// Tracker notice events (to admins and the project's owner).
const NoticeTrackerStopped = "tracker.stopped"

// TrackerSettings are what a binding imports and writes back.
type TrackerSettings struct {
	Label       string `json:"label"`                  // an issue with it becomes a requirement ("" imports none by label)
	Assigned    bool   `json:"assigned,omitempty"`     // an issue assigned to a member of the project does too
	Comment     bool   `json:"comment"`                // keep one progress comment on the issue
	Detail      bool   `json:"detail,omitempty"`       // list the subtasks in it (repository readers see them)
	OnAccept    string `json:"on_accept"`              // close | label
	AcceptLabel string `json:"accept_label,omitempty"` // on_accept label: this one
	Poll        int    `json:"poll"`                   // seconds between scans
}

// DefaultSettings import issues labelled tend, keep a progress comment and close an issue once its requirement is done.
func DefaultSettings() TrackerSettings {
	return TrackerSettings{Label: "tend", Comment: true, OnAccept: "close", AcceptLabel: "tend:accepted", Poll: 60}
}

func (x TrackerSettings) check() error {
	switch {
	case x.OnAccept != "close" && x.OnAccept != "label":
		return fmt.Errorf("on_accept %q", x.OnAccept)
	case x.OnAccept == "label" && x.AcceptLabel == "":
		return errors.New("accept_label")
	case x.Poll < 30 || x.Poll > 3600:
		return fmt.Errorf("poll %d: 30 to 3600 seconds", x.Poll)
	}
	return nil
}

func settingsOf(x store.Tracker) TrackerSettings {
	s := DefaultSettings()
	json.Unmarshal([]byte(x.Settings), &s)
	return s
}

// Syncer is the one worker that syncs every binding: scans, webhooks and a rescan only mark issues to read again; it
// reads each one, records what changed in the journal, and writes the progress comment and the close back.
type Syncer struct {
	team    *store.Team
	coord   *coord.Coord
	seal    *Sealer
	do      wire.Handler
	open    func(tracker.Config) (tracker.Tracker, error)
	now     func() time.Time
	notice  func(coord.Notice)
	wake    chan struct{}
	mu      sync.Mutex
	retry   map[string]time.Time // issue key → not before
	updated map[string]time.Time // issue key → the updated time its last read saw
}

func NewSyncer(team *store.Team, c *coord.Coord, seal *Sealer, notice func(coord.Notice)) *Syncer {
	return &Syncer{team: team, coord: c, seal: seal, do: c.HandlerFor(coord.System), open: tracker.New, now: time.Now, notice: notice,
		wake: make(chan struct{}, 1), retry: map[string]time.Time{}, updated: map[string]time.Time{}}
}

// Wake has the worker look now.
func (s *Syncer) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Syncer) Run(ctx context.Context) {
	t := time.NewTicker(syncTick)
	defer t.Stop()
	for {
		s.Pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
	}
}

// Pass does what is due for every binding once.
func (s *Syncer) Pass(ctx context.Context) {
	xs, err := s.team.Trackers()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tend-server: trackers:", err)
		return
	}
	for _, x := range xs {
		if x.Stopped != "" || s.now().Before(x.Paused) {
			continue
		}
		if err := s.binding(ctx, x); err != nil {
			s.failed(x, err)
		}
	}
}

// binding scans x when due, then reads every issue marked and writes back what the journal says of its task.
func (s *Syncer) binding(ctx context.Context, x store.Tracker) error {
	tok, err := s.seal.Open(x.Token)
	if err != nil {
		return &tracker.AuthError{Detail: "the token cannot be unsealed (was the server key replaced?)"}
	}
	tr, err := s.open(tracker.Config{Kind: x.Kind, Base: x.Base, Repo: x.Repo, Token: string(tok)})
	if err != nil {
		return err
	}
	set := settingsOf(x)
	if s.now().Sub(x.Polled) >= time.Duration(set.Poll)*time.Second {
		if err := s.scan(ctx, x, tr, set); err != nil {
			return err
		}
	}
	for _, id := range s.writeBacks(x, set) {
		if err := s.team.MarkDirty(x.ID, id); err != nil {
			return err
		}
	}
	rows, err := s.team.TrackerIssues(x.ID, true)
	if err != nil {
		return err
	}
	for _, row := range rows {
		key := x.ID + "#" + fmt.Sprint(row.Number)
		s.mu.Lock()
		wait := s.now().Before(s.retry[key])
		s.mu.Unlock()
		if wait {
			continue
		}
		if err := s.issue(ctx, x, tr, set, row); err != nil {
			if isStop(err) {
				return err
			}
			s.mu.Lock()
			s.retry[key] = s.now().Add(issueRetry)
			s.mu.Unlock()
			row.LastError = err.Error()
			s.team.PutTrackerIssue(row)
			continue
		}
		s.team.TrackerResult(x.ID, s.now(), time.Time{}, "", "")
	}
	return nil
}

// isStop: err is the tracker's, for the whole binding (a rate limit or a refused credential), not one issue's.
func isStop(err error) bool {
	var rl *tracker.RateLimited
	var ae *tracker.AuthError
	return errors.As(err, &rl) || errors.As(err, &ae)
}

// failed records err for x: a rate limit pauses it, a refused credential stops it and tells its people.
func (s *Syncer) failed(x store.Tracker, err error) {
	var rl *tracker.RateLimited
	var ae *tracker.AuthError
	switch {
	case errors.As(err, &rl):
		s.team.TrackerResult(x.ID, time.Time{}, s.now().Add(rl.Wait), "", err.Error())
	case errors.As(err, &ae):
		s.team.TrackerResult(x.ID, time.Time{}, time.Time{}, "auth", err.Error())
		s.stopped(x, err)
	default:
		s.team.TrackerResult(x.ID, time.Time{}, time.Time{}, "", err.Error())
	}
}

// stopped tells the admins and x's project owner that x stopped syncing.
func (s *Syncer) stopped(x store.Tracker, err error) {
	if s.notice == nil {
		return
	}
	var to []string
	s.coord.Read(func(st *task.State) {
		if p := st.Projects[x.Project]; p != nil && p.Owner != "" {
			to = append(to, p.Owner)
		}
	})
	if us, uerr := s.team.Users(); uerr == nil {
		for _, u := range us {
			if u.Role == store.RoleAdmin && !u.Disabled && u.ID != store.LocalUser && !slices.Contains(to, u.ID) {
				to = append(to, u.ID)
			}
		}
	}
	s.notice(coord.Notice{Event: NoticeTrackerStopped, Title: x.Repo, Project: x.Project, Reason: err.Error(), To: to, At: s.now().UTC()})
}

// scan marks the issues updated since x's cursor that concern it: those known already, those labelled, and those
// assigned to a member of the project when the binding says so.
func (s *Syncer) scan(ctx context.Context, x store.Tracker, tr tracker.Tracker, set TrackerSettings) error {
	since := x.Cursor
	if !since.IsZero() {
		since = since.Add(-scanOverlap)
	}
	label := set.Label
	if set.Assigned {
		label = ""
	}
	page, err := tr.Issues(ctx, since, label, x.ETag)
	if err != nil {
		return err
	}
	cursor := x.Cursor
	known, err := s.team.TrackerIssues(x.ID, false)
	if err != nil {
		return err
	}
	for _, i := range page.Issues {
		if i.UpdatedAt.After(cursor) {
			cursor = i.UpdatedAt
		}
		key := x.ID + "#" + fmt.Sprint(i.Number)
		s.mu.Lock()
		same := s.updated[key].Equal(i.UpdatedAt)
		s.mu.Unlock()
		if same {
			continue
		}
		if slices.ContainsFunc(known, func(r store.TrackerIssue) bool { return r.Number == i.Number && r.Task != "" }) || s.wanted(x, set, i) {
			if err := s.team.MarkDirty(x.ID, i.Number); err != nil {
				return err
			}
		}
	}
	return s.team.Polled(x.ID, cursor, page.ETag, s.now())
}

// wanted: issue i becomes a requirement of x's project.
func (s *Syncer) wanted(x store.Tracker, set TrackerSettings, i tracker.Issue) bool {
	if i.Closed {
		return false
	}
	if set.Label != "" && slices.Contains(i.Labels, set.Label) {
		return true
	}
	return set.Assigned && s.assignee(x, i) != ""
}

// assignee is the member of x's project issue i is assigned to, "" when none maps.
func (s *Syncer) assignee(x store.Tracker, i tracker.Issue) string {
	for _, login := range i.Assignees {
		u, err := s.team.UserByLogin(x.Base, login)
		if err != nil || u == "" {
			continue
		}
		member := false
		s.coord.Read(func(st *task.State) { member = st.Projects[x.Project].Role(u) == task.RoleParticipant })
		if member {
			return u
		}
	}
	return ""
}

// writeBacks are the issues of x whose task the journal moved past what was written back: a comment to rewrite, or a
// done requirement whose issue is still to be closed.
func (s *Syncer) writeBacks(x store.Tracker, set TrackerSettings) []int64 {
	rows, err := s.team.TrackerIssues(x.ID, false)
	if err != nil {
		return nil
	}
	var out []int64
	for _, row := range rows {
		if row.Task == "" || row.Dirty {
			continue
		}
		body, done, ok := s.progress(x, set, row.Task)
		switch {
		case !ok:
		case done && !row.Closed, set.Comment && hash(body) != row.BodyHash && s.now().Sub(row.Written) >= editRest:
			out = append(out, row.Number)
		}
	}
	return out
}

// issue reads one issue, records it in the journal, and writes back what is due.
func (s *Syncer) issue(ctx context.Context, x store.Tracker, tr tracker.Tracker, set TrackerSettings, row store.TrackerIssue) error {
	i, err := tr.Issue(ctx, row.Number)
	if errors.Is(err, tracker.ErrNotFound) {
		row.Dirty, row.LastError = false, "the issue is gone"
		return s.team.PutTrackerIssue(row)
	}
	if err != nil {
		return err
	}
	comments, err := tr.Comments(ctx, row.Number)
	if err != nil {
		return err
	}
	var mine *tracker.Comment
	var theirs []tracker.Comment
	for k, c := range comments {
		switch {
		case row.CommentID != 0 && c.ID == row.CommentID,
			row.CommentID == 0 && mine == nil && c.Author == x.Bot && strings.Contains(c.Body, markerPrefix(s.coord.ID())):
			mine = &comments[k]
		case c.Author == x.Bot && strings.Contains(c.Body, markerPrefix(s.coord.ID())):
		default:
			theirs = append(theirs, c)
		}
	}
	if row.Task == "" && !s.wanted(x, set, i) {
		row.Dirty = false
		return s.team.PutTrackerIssue(row)
	}
	title, text, digest := snapshot(i, theirs)
	owner := s.assignee(x, i)
	p := coord.TaskSync{Project: x.Project, Kind: x.Kind, Tracker: x.ID, Base: x.Base, Repo: x.Repo, RepoID: x.RepoID, Number: i.Number,
		URL: i.URL, Title: title, Text: text, Digest: digest, Closed: i.Closed, Owner: owner, Unmapped: owner == "" && len(i.Assignees) > 0}
	b, _ := json.Marshal(p)
	res, err := s.do(ctx, &wire.Request{Method: coord.MTaskSync, CommandID: "sync-" + journal.Digest(b), Params: b})
	if err != nil {
		return err
	}
	var t struct{ ID string }
	if b, err := json.Marshal(res); err == nil {
		json.Unmarshal(b, &t)
	}
	if t.ID != "" {
		row.Task = t.ID
	}
	s.mu.Lock()
	s.updated[x.ID+"#"+fmt.Sprint(i.Number)] = i.UpdatedAt
	s.mu.Unlock()
	if row.Task == "" {
		row.Dirty = false
		return s.team.PutTrackerIssue(row)
	}
	if mine != nil && row.CommentID == 0 {
		row.CommentID = mine.ID
	}
	body, done, ok := s.progress(x, set, row.Task)
	if ok && set.Comment && hash(body) != row.BodyHash && s.now().Sub(row.Written) >= editRest {
		if err := s.writeComment(ctx, tr, &row, body); err != nil {
			return err
		}
	}
	if ok && done && !row.Closed {
		if set.OnAccept == "label" {
			err = tr.Label(ctx, i.Number, set.AcceptLabel)
		} else if !i.Closed {
			err = tr.Close(ctx, i.Number)
		}
		if err != nil {
			return err
		}
		row.Closed = true
	}
	row.Dirty, row.LastError = false, ""
	return s.team.PutTrackerIssue(row)
}

// writeComment edits row's progress comment, or makes it when there is none (or it was deleted); a comment made whose
// answer got lost is found again by its marker on the next read.
func (s *Syncer) writeComment(ctx context.Context, tr tracker.Tracker, row *store.TrackerIssue, body string) error {
	if row.CommentID != 0 {
		err := tr.EditComment(ctx, row.CommentID, body)
		if !errors.Is(err, tracker.ErrNotFound) {
			if err == nil {
				row.BodyHash, row.Written = hash(body), s.now()
			}
			return err
		}
		row.CommentID = 0
	}
	c, err := tr.CreateComment(ctx, row.Number, body)
	if err != nil {
		return err
	}
	row.CommentID, row.BodyHash, row.Written = c.ID, hash(body), s.now()
	return nil
}

func markerPrefix(instance string) string { return "<!-- tend:progress " + instance + " " }

func marker(instance, taskID string) string { return markerPrefix(instance) + taskID + " -->" }

func hash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

// snapshot is what a requirement takes from issue i and the comments of people: its title, a brief, and a digest that
// changes when either does.
func snapshot(i tracker.Issue, comments []tracker.Comment) (title, text, digest string) {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(i.Body))
	if len(comments) > 0 {
		b.WriteString("\n\n## Comments\n")
		for _, c := range comments {
			fmt.Fprintf(&b, "\n**%s**: %s\n", c.Author, strings.TrimSpace(c.Body))
		}
	}
	text = b.String()
	return i.Title, text, hash(i.Title + "\x00" + text)[:32]
}

// progress is the comment x's issue carries for taskID, and whether the task is done; ok is false when the journal no
// longer holds it.
func (s *Syncer) progress(x store.Tracker, set TrackerSettings, taskID string) (body string, done, ok bool) {
	var owner, approver, status, stages string
	var kids []string
	var total, finished int
	s.coord.Read(func(st *task.State) {
		t := st.Tasks[taskID]
		if t == nil {
			return
		}
		ok, done = true, t.Status == task.StatusDone
		owner, approver = t.Owner, t.Approver
		status = statusLine(st.Situation(t))
		stages = stageLine(t)
		for _, k := range st.Subtree(t.ID)[1:] {
			if len(st.Children(k.ID)) > 0 {
				continue
			}
			total++
			if task.Finished(k.Status) {
				finished++
			}
			if set.Detail {
				box := " "
				if k.Status == task.StatusDone {
					box = "x"
				}
				kids = append(kids, fmt.Sprintf("- [%s] %s", box, oneLine(k.Title)))
			}
		}
	})
	if !ok {
		return "", false, false
	}
	var b strings.Builder
	b.WriteString(marker(s.coord.ID(), taskID) + "\n")
	fmt.Fprintf(&b, "**tend** · %s\n", status)
	if stages != "" {
		b.WriteString("\n" + stages + "\n")
	}
	if total > 0 {
		fmt.Fprintf(&b, "\nProgress: %d/%d subtasks finished\n", finished, total)
	}
	if people := s.mentions(x, owner, approver); people != "" {
		b.WriteString("\n" + people + "\n")
	}
	if len(kids) > 0 {
		b.WriteString("\n" + strings.Join(kids, "\n") + "\n")
	}
	return b.String(), done, true
}

// mentions names the owner and approver as the tracker knows them.
func (s *Syncer) mentions(x store.Tracker, owner, approver string) string {
	at := func(u string) string {
		if l, err := s.team.LoginOf(u, x.Base); err == nil && l != "" {
			return "@" + l
		}
		return ""
	}
	var parts []string
	if o := at(owner); o != "" {
		parts = append(parts, "Owner: "+o)
	}
	if approver != "" && approver != owner {
		if a := at(approver); a != "" {
			parts = append(parts, "Approver: "+a)
		}
	}
	return strings.Join(parts, " · ")
}

// stageLine is where t stands in its workflow: its stages with the current one in bold, and the round.
func stageLine(t *task.Task) string {
	if t.Flow == nil {
		return ""
	}
	var names []string
	for _, st := range t.Flow.Stages {
		if st.Name == t.Stage && !task.Finished(t.Status) {
			names = append(names, "**"+st.Name+"**")
		} else {
			names = append(names, st.Name)
		}
	}
	line := "Stages: " + strings.Join(names, " → ")
	if t.Loops > 0 {
		line += fmt.Sprintf(" (round %d)", t.Loops+1)
	}
	return line
}

func statusLine(sit task.Situation) string {
	switch sit.Kind {
	case task.SitDone:
		return "done"
	case task.SitCanceled:
		return "canceled"
	case task.SitBacklog:
		return "in the backlog"
	case task.SitRunning:
		return "in progress"
	case task.SitQueued:
		return "queued"
	}
	switch sit.Reason {
	case task.WhyAccept:
		return "waiting for acceptance"
	case task.WhySourceChanged:
		return "the issue changed; waiting for a decision"
	case task.WhySourceClosed:
		return "the issue was closed; waiting for a decision"
	case task.WhyDispatch:
		return "not started"
	}
	return "waiting for someone"
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}
