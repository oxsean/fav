package coord

import (
	"slices"
	"strconv"
	"strings"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// System is tend-server's own work (the tracker sync): it sees everything, and only it calls the internal methods. A
// connection never carries it.
var System = Principal{User: "system", Admin: true, system: true}

// TagUnmapped marks a requirement whose issue's assignee is nobody of the project yet: its project's owner holds it.
const TagUnmapped = "unmapped_assignee"

// TaskSync is task.sync: an issue as the sync worker read it. A new issue becomes a requirement of Project (unless it
// is closed); a known one records what changed.
type TaskSync struct {
	Project string `json:"project"`
	Kind    string `json:"kind"`
	Tracker string `json:"tracker"`
	Base    string `json:"base"`
	Repo    string `json:"repo"`
	RepoID  int64  `json:"repo_id"`
	Number  int64  `json:"number"`
	URL     string `json:"url,omitempty"`
	Title   string `json:"title"`
	Text    string `json:"text"`
	Digest  string `json:"digest"`
	Closed  bool   `json:"closed,omitempty"`
	// Reopened: open again after tend closed it for the task's completion, or closed and opened again since a read found
	// the task finished. An issue a read found closed outside tend and open again says so by Closed alone.
	Reopened bool   `json:"reopened,omitempty"`
	Owner    string `json:"owner,omitempty"` // the member the issue is assigned to, "" when none maps
	Unmapped bool   `json:"unmapped,omitempty"`
}

// taskLink records the sub-issue or pull request the sync worker opened for a task; nothing when it is recorded already.
func (c *Coord) taskLink(_ Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.Linked
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t := c.st.Tasks[p.ID]
	if t == nil {
		return "", nil, notFound("task " + p.ID)
	}
	if (p.Issue == "" || p.Issue == t.Issue) && (p.PR == "" || p.PR == t.PR) {
		return t.ID, nil, nil
	}
	return t.ID, []journal.Event{journal.NewEvent(task.ETaskLinked, p)}, nil
}

// Sourced is the task that stands for issue number of repo at base, nil when none does; the caller holds mu.
func sourced(st *task.State, base string, repo, number int64) *task.Task {
	for _, t := range st.Tasks {
		if s := t.Source; s != nil && s.Base == base && s.RepoID == repo && s.Number == number {
			return t
		}
	}
	return nil
}

// Read runs f on the coordinator's state, which f must neither change nor keep.
func (c *Coord) Read(f func(*task.State)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f(c.st)
}

func (c *Coord) taskSync(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p TaskSync
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	title := clip(strings.TrimSpace(p.Title), maxTitle)
	if title == "" {
		title = "#" + strconv.FormatInt(p.Number, 10)
	}
	text := clip(p.Text, maxBrief)
	t := sourced(c.st, p.Base, p.RepoID, p.Number)
	if t == nil {
		pr := c.st.Projects[p.Project]
		switch {
		case pr == nil:
			return "", nil, notFound("project " + p.Project)
		case p.Closed:
			return "", nil, nil
		}
		owner, tags := p.Owner, []string(nil)
		if owner == "" || pr.Role(owner) != task.RoleParticipant {
			owner, tags = pr.Owner, []string{TagUnmapped}
		}
		nt := task.Task{ID: newID("t_"), Title: title, Brief: text, Project: p.Project, Owner: owner, Kind: task.KindRequirement,
			Tags: tags, Status: task.StatusTodo, Source: &task.Source{Kind: p.Kind, Tracker: p.Tracker, Base: p.Base, Repo: p.Repo,
				RepoID: p.RepoID, Number: p.Number, URL: p.URL, Rev: 1, Digest: p.Digest, Seen: p.Digest, SeenRev: 1}}
		return nt.ID, []journal.Event{journal.NewEvent(task.ETaskCreated, nt)}, nil
	}
	var events []journal.Event
	src := t.Source
	reopened := !p.Closed && task.Finished(t.Status) && (p.Reopened || src.Closed)
	if p.Digest != src.Seen || p.Closed != src.Closed || p.Repo != src.Repo || p.URL != src.URL || reopened {
		events = append(events, journal.NewEvent(task.ETaskSourced, task.SourceUpdate{ID: t.ID, Digest: p.Digest, Title: title,
			Text: text, Closed: p.Closed, Reopened: reopened, Repo: p.Repo, URL: p.URL}))
	}
	if reopened {
		events = append(events, reopening(t, task.StatusTodo)...)
	}
	if pr := c.st.Projects[t.Project]; slices.Contains(t.Tags, TagUnmapped) && !task.Finished(t.Status) && p.Owner != "" &&
		pr != nil && pr.Role(p.Owner) == task.RoleParticipant {
		tags := slices.DeleteFunc(slices.Clone(t.Tags), func(s string) bool { return s == TagUnmapped })
		events = append(events, journal.NewEvent(task.ETaskEdited, task.TaskEdit{ID: t.ID, Owner: &p.Owner, Tags: &tags}))
	}
	return t.ID, events, nil
}

func (c *Coord) taskSourceAck(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.SourceAck
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t, err := c.writableTask(who, p.ID)
	if err != nil {
		return "", nil, err
	}
	if task.SourceWaits(t) == "" {
		return "", nil, conflict("nothing to acknowledge")
	}
	return t.ID, []journal.Event{journal.NewEvent(task.ETaskSourceAcked, p)}, nil
}
