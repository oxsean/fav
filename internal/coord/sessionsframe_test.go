package coord

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// sessionsFrame is the frame file the Web UI's tests play for the sessions page: what this coordinator answers.
var sessionsFrame = filepath.Join("..", "server", "webtest", "frames", "sessions-query.jsonl")

// The Web UI's tests play sessions-query.jsonl: sessions.query and people.names as this coordinator answers them over
// machines in every state. A change of what it answers fails here until the file is written again
// (TEND_WRITE_FRAMES=1 go test ./internal/coord -run TestTheSessionsQueryFrameIsCurrent).
func TestTheSessionsQueryFrameIsCurrent(t *testing.T) {
	was := machineWait
	machineWait = time.Second
	t.Cleanup(func() { machineWait = was })
	e := served(t, map[string]string{"linux": bob.User})
	at := func(h, m int) time.Time { return time.Date(2026, 9, 30, h, m, 0, 0, time.UTC) }
	fav := func(s remote.Session, id string, faved time.Time) remote.Session {
		s.ID, s.FavoritedAt = id, &faved
		return s
	}
	row := func(provider, id, title, dir string, last time.Time, turns int) remote.Session {
		return remote.Session{Provider: provider, SessionID: id, Title: title, Cwd: dir, UpdatedAt: last, LastAt: last, Turns: turns, Msgs: 2 * turns}
	}
	fix := fav(row(tend.ProviderClaude, "c-fix", "Fix the checkout total", "/Users/ann/dev/shop", at(14, 20), 6), "r1", at(9, 0))
	fix.GitBranch = "fix-total"
	port := fav(row(tend.ProviderCodex, "x-port", "Port the importer", "/Users/ann/dev/my shop", at(13, 0), 3), "r2", at(10, 0))
	port.Tags, port.Summary = []string{"importer", "csv"}, "Stream the CSV import instead of reading it whole."
	notes := fav(row(tend.ProviderClaude, "c-notes", "Draft the release notes", "/Users/ann/dev/shop", at(12, 0), 4), "r3", at(11, 0))
	notes.Tags, notes.Status = []string{"release", "docs"}, tend.StatusDoing
	e.attach("mba", &fakeNode{resume: true, sessions: []remote.Session{fix, port, notes},
		live: map[string]capture.Live{"x-port": {Agent: tend.ProviderCodex, Status: "working", Since: at(12, 30)}}})
	e.attach("linux", &fakeNode{resume: true, sessions: []remote.Session{
		fav(row(tend.ProviderClaude, "c-deploy", "Roll out the cache", "/home/bob/svc", at(14, 10), 8), "r4", at(8, 0)),
		fav(row(tend.ProviderClaude, "c-cache", "Size the cache", "/home/bob/svc", at(11, 0), 5), "r5", at(8, 0))}})
	e.attach("old", &fakeNode{old: true, resume: true, sessions: []remote.Session{
		fav(row(tend.ProviderClaude, "c-old", "Back up the photos", "/Users/ann/photos", at(14, 0), 3), "r6", at(7, 0))}})
	e.attach("slow", &fakeNode{wait: time.Minute})
	e.c.Expect("gone", at(6, 0))
	if err := callAs(e.as(bob), MMachineSessions, "scope", task.SessionsSet{Machine: "linux", Users: []string{ann.User}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(ann), MProjectCreate, "p", ProjectCreate{ID: "shop", Name: "Shop"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(ann), MProjectAttach, "a", ProjectAttach{Project: "shop", Machine: "mba", Dir: "/Users/ann/dev/shop"}, nil); err != nil {
		t.Fatal(err)
	}
	e.ran("mba", "c-fix", "Fix the checkout total", ann.User)

	var out bytes.Buffer
	line := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(append(b, '\n'))
	}
	type frame struct {
		Type   string `json:"type"`
		ID     int64  `json:"id"`
		Method string `json:"method,omitempty"`
		Params any    `json:"params,omitempty"`
		Result any    `json:"result,omitempty"`
	}
	id := int64(4)
	call := func(before, after, method string, params, result any) {
		id++
		line(map[string]string{"step": before})
		line(map[string]frame{"c": {Type: "req", ID: id, Method: method, Params: params}})
		if err := callAs(e.as(ann), method, "", params, result); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		if page, ok := result.(*SessionsPage); ok {
			inUTC(page)
		}
		line(map[string]frame{"s": {Type: "res", ID: id, Result: result}})
		line(map[string]string{"step": after})
	}
	line(map[string]string{"note": "ann's sessions over the machines she reads, as the coordinator answers them (written by " +
		"TEND_WRITE_FRAMES=1 go test ./internal/coord -run TestTheSessionsQueryFrameIsCurrent): mba hers with put (a session of a task, " +
		"one running, a project), linux bob's shared with her (read only), old a tend without query (read only, no task made), slow " +
		"too late, gone offline; the first page, the names of the machines' owners, the next page"})
	var first SessionsPage
	call("mount", "listed", MSessionsQuery, SessionsQuery{Limit: 3}, &first)
	if first.Next == nil || len(first.Rows) != 3 {
		t.Fatalf("the first page: %+v", first)
	}
	call("names", "named", MPeopleNames, PeopleParams{IDs: []string{ann.User, bob.User}}, new(People))
	var next SessionsPage
	call("more", "paged", MSessionsQuery, SessionsQuery{Limit: 3, After: first.Next}, &next)
	if next.Next != nil || len(next.Rows) != 3 {
		t.Fatalf("the next page: %+v", next)
	}

	have, err := os.ReadFile(sessionsFrame)
	if bytes.Equal(bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n")), out.Bytes()) {
		return
	}
	if os.Getenv("TEND_WRITE_FRAMES") == "" {
		t.Fatalf("%s is stale (%v): TEND_WRITE_FRAMES=1 go test ./internal/coord -run TestTheSessionsQueryFrameIsCurrent", sessionsFrame, err)
	}
	if err := os.WriteFile(sessionsFrame, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// inUTC puts page's times in UTC: a node writes them in its own zone, the frame file in one.
func inUTC(page *SessionsPage) {
	utc := func(ts ...*time.Time) {
		for _, t := range ts {
			if t != nil {
				*t = t.UTC()
			}
		}
	}
	for i := range page.Rows {
		s := &page.Rows[i].Session
		utc(&s.UpdatedAt, &s.LastAt, s.StartedAt, s.FavoritedAt, s.ArchivedAt, s.ResumedAt)
		if l := page.Rows[i].Live; l != nil {
			utc(&l.Since)
		}
	}
	if page.Next != nil {
		utc(&page.Next.At)
	}
	for i := range page.Machines {
		utc(page.Machines[i].Since)
	}
}
