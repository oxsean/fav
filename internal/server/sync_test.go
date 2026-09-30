package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/tracker"
	"github.com/oxsean/fav/internal/tracker/trackertest"
	"github.com/oxsean/fav/internal/wire"
)

// syncRig is a team server's coordinator and database, a tracker, and the sync worker on a clock the test moves.
type syncRig struct {
	t       *testing.T
	team    *store.Team
	c       *coord.Coord
	s       *Syncer
	g       *trackertest.Server
	x       store.Tracker
	ann     string
	clock   time.Time
	mu      sync.Mutex
	notices []coord.Notice
	cmd     int
}

// sqliteLog is the coordinator's log as tend-server opens it.
func sqliteLog(dir string, fold func(journal.Envelope) error) (coord.EventLog, error) {
	l, err := store.Open(filepath.Join(dir, store.File), fold)
	if err != nil {
		return nil, err
	}
	return l, nil
}

func newSyncRig(t *testing.T) *syncRig { return newSyncRigOf(t, tracker.KindGitea) }

func newSyncRigOf(t *testing.T, kind string) *syncRig {
	r := &syncRig{t: t, clock: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)}
	home := t.TempDir()
	var err error
	if r.team, err = store.OpenTeam(filepath.Join(home, "coord", store.File)); err != nil {
		t.Fatal(err)
	}
	r.g = trackertest.New(kind, "acme/app", "tend-bot", "tok")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(r.team.AddAdmit(store.Admit{Kind: store.AdmitDomain, Value: "corp.example", Role: store.RoleMember}))
	ann, err := r.team.Admit(store.Identity{Provider: "gitea", Issuer: r.g.URL + "/", Subject: "1", Username: "ann", Email: "ann@corp.example", EmailVerified: true}, "")
	must(err)
	r.ann = ann.ID
	dir, err := NewDirectory(r.team)
	must(err)
	r.c, err = coord.Open(coord.Options{Home: home, Version: "test", Remote: true, MachineOwner: dir.MachineOwner, Users: dir.User, Config: tend.Config{},
		OpenLog: sqliteLog})
	must(err)
	ctx, cancel := context.WithCancel(context.Background())
	go r.c.Run(ctx)
	t.Cleanup(func() { cancel(); r.c.Close(); r.team.Close(); r.g.Close() })
	r.call(coord.Owner, coord.MProjectCreate, coord.ProjectCreate{ID: "p1", Name: "One", Owner: r.ann}, nil)
	seal, err := LoadSealer(home)
	must(err)
	r.s = NewSyncer(r.team, r.c, seal, func(n coord.Notice) { r.mu.Lock(); r.notices = append(r.notices, n); r.mu.Unlock() })
	r.s.now = func() time.Time { return r.clock }
	b, _ := json.Marshal(DefaultSettings())
	r.x, err = r.team.AddTracker(store.Tracker{Project: "p1", Kind: kind, Base: r.g.URL, Repo: "acme/app", RepoID: r.g.RepoID, Bot: "tend-bot",
		Token: seal.Seal([]byte("tok")), HookSecret: seal.Seal([]byte("hook-key")), Settings: string(b), CreatedBy: store.LocalUser})
	must(err)
	return r
}

func (r *syncRig) call(p coord.Principal, method string, params, out any) {
	r.t.Helper()
	r.cmd++
	b, _ := json.Marshal(params)
	res, err := r.c.HandlerFor(p)(context.Background(), &wire.Request{Method: method, CommandID: "c" + itoa(r.cmd), Params: b})
	if err != nil {
		r.t.Fatalf("%s: %v", method, err)
	}
	if out != nil {
		b, _ = json.Marshal(res)
		json.Unmarshal(b, out)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// pass moves the clock by d and lets the worker do what is due.
func (r *syncRig) pass(d time.Duration) {
	r.clock = r.clock.Add(d)
	r.s.Pass(context.Background())
}

func (r *syncRig) task(number int64) *task.Task {
	var out *task.Task
	r.c.Read(func(st *task.State) {
		for _, t := range st.Tasks {
			if t.Source != nil && t.Source.Number == number {
				cp := *t
				cp.Source = t.Source.Clone()
				out = &cp
			}
		}
	})
	return out
}

func (r *syncRig) situation(id string) task.Situation {
	var sit task.Situation
	r.c.Read(func(st *task.State) { sit = st.Situation(st.Tasks[id]) })
	return sit
}

func (r *syncRig) tracker() store.Tracker {
	x, err := r.team.Tracker(r.x.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	return x
}

func TestLabelledIssuesBecomeRequirementsWithOneProgressComment(t *testing.T) {
	for _, kind := range []string{tracker.KindGitea, tracker.KindGitHub, tracker.KindGitLab} {
		t.Run(kind, func(t *testing.T) { labelledIssuesBecomeRequirements(t, newSyncRigOf(t, kind)) })
	}
}

func labelledIssuesBecomeRequirements(t *testing.T, r *syncRig) {
	r.g.Open(1, "Export CSV", "rows as CSV", "tend")
	r.g.Change(1, func(i *trackertest.Issue) { i.Assignees = []string{"ann"} })
	r.g.Open(2, "Unrelated", "no label")
	r.pass(0)
	x := r.task(1)
	if x == nil || x.Kind != task.KindRequirement || x.Owner != r.ann || x.Brief != "rows as CSV" || r.task(2) != nil {
		t.Fatalf("the labelled issue is a requirement of its assignee: %+v", x)
	}
	cs := r.g.CommentsOf(1)
	if len(cs) != 1 || cs[0].Author != "tend-bot" || !strings.Contains(cs[0].Body, "tend:progress") || !strings.Contains(cs[0].Body, "not started") ||
		!strings.Contains(cs[0].Body, "@ann") {
		t.Fatalf("one progress comment, naming its owner: %+v", cs)
	}

	r.pass(61 * time.Second)
	if y := r.task(1); y.Source.SeenRev != 1 || y.Source.Pending != nil {
		t.Fatalf("tend's own comment is no change of the requirement: %+v", y.Source)
	}

	var kid, kid2 task.Task
	r.call(coord.Owner, coord.MTaskCreate, coord.TaskCreate{Title: "the exporter", Parent: x.ID}, &kid)
	r.pass(5 * time.Second)
	if cs2 := r.g.CommentsOf(1); len(cs2) != 1 || cs2[0].ID != cs[0].ID || !strings.Contains(cs2[0].Body, "Progress: 0/1") {
		t.Fatalf("the same comment follows the tree: %+v", cs2)
	}
	r.call(coord.Owner, coord.MTaskCreate, coord.TaskCreate{Title: "the docs", Parent: x.ID}, &kid2)
	r.pass(5 * time.Second)
	if strings.Contains(r.g.CommentsOf(1)[0].Body, "0/2") {
		t.Fatal("a comment rests 30 seconds between edits")
	}
	r.pass(30 * time.Second)
	if !strings.Contains(r.g.CommentsOf(1)[0].Body, "Progress: 0/2") {
		t.Fatalf("then it catches up: %+v", r.g.CommentsOf(1))
	}

	r.g.Say(1, "bob", "also TSV please")
	r.pass(61 * time.Second)
	y := r.task(1)
	if y.Source.Pending == nil || r.situation(x.ID).Reason != task.WhySourceChanged || !strings.Contains(y.Source.Pending.Text, "also TSV please") {
		t.Fatalf("a comment of a person changes the requirement: %+v", y.Source)
	}
	r.call(coord.Owner, coord.MTaskSourceAck, task.SourceAck{ID: x.ID, Accept: true}, nil)

	r.call(coord.Owner, coord.MTaskStatus, task.TaskStatus{ID: kid.ID, Status: task.StatusDone}, nil)
	r.call(coord.Owner, coord.MTaskStatus, task.TaskStatus{ID: kid2.ID, Status: task.StatusDone}, nil)
	r.call(coord.Owner, coord.MTaskStatus, task.TaskStatus{ID: x.ID, Status: task.StatusDone}, nil)
	r.pass(31 * time.Second)
	if !r.g.Get(1).Closed || !strings.Contains(r.g.CommentsOf(1)[0].Body, "done") {
		t.Fatalf("accepted: the issue closes and the comment says so: %+v %+v", r.g.Get(1), r.g.CommentsOf(1))
	}
	r.pass(61 * time.Second)
	if sit := r.situation(x.ID); sit.Kind != task.SitDone || len(r.g.CommentsOf(1)) != 2 {
		t.Fatalf("tend's own close is no news: %+v %+v", sit, r.g.CommentsOf(1))
	}
}

func TestALostAnswerLeavesOneCommentAndAClosedIssueWaits(t *testing.T) {
	r := newSyncRig(t)
	r.g.Open(3, "Lost", "body", "tend")
	r.g.LoseCreate = true
	r.pass(0)
	if row, _ := r.team.TrackerIssue(r.x.ID, 3); row.LastError == "" || len(r.g.CommentsOf(3)) != 1 {
		t.Fatalf("the comment was made though its answer failed: %+v %+v", row, r.g.CommentsOf(3))
	}
	r.pass(61 * time.Second)
	cs := r.g.CommentsOf(3)
	row, _ := r.team.TrackerIssue(r.x.ID, 3)
	if len(cs) != 1 || row.CommentID != cs[0].ID || row.LastError != "" {
		t.Fatalf("found again by its marker, not made twice: %+v %+v", cs, row)
	}
	r.g.DeleteComment(cs[0].ID)
	r.g.Change(3, func(i *trackertest.Issue) { i.Closed = true })
	r.pass(61 * time.Second)
	x := r.task(3)
	if r.situation(x.ID).Reason != task.WhySourceClosed {
		t.Fatalf("closed outside tend: its owner decides: %+v", r.situation(x.ID))
	}
	r.call(coord.Owner, coord.MTaskEdit, task.TaskEdit{ID: x.ID, Title: ptr("Lost and found")}, nil)
	r.pass(31 * time.Second)
	if cs = r.g.CommentsOf(3); len(cs) != 1 || cs[0].ID == row.CommentID {
		t.Fatalf("a deleted comment is made again: %+v", cs)
	}
}

func TestAScanThatFindsNothingIsASuccess(t *testing.T) {
	r := newSyncRig(t)
	r.g.Open(2, "Unrelated", "no label")
	r.pass(0)
	if x := r.tracker(); !x.LastOK.Equal(r.clock) || x.LastError != "" {
		t.Fatalf("the tracker answered: the binding is fine: %+v", x)
	}
	r.g.Down = true
	r.pass(61 * time.Second)
	r.g.Down = false
	r.pass(61 * time.Second)
	if x := r.tracker(); !x.LastOK.Equal(r.clock) || x.LastError != "" {
		t.Fatalf("and fine again once it answers: %+v", x)
	}
}

func TestAnAcceptedIssueGetsTheAcceptLabelEvenWhereTheRepositoryLacksIt(t *testing.T) {
	r := newSyncRig(t)
	set := DefaultSettings()
	set.OnAccept = "label"
	b, _ := json.Marshal(set)
	if err := r.team.SetTrackerSettings(r.x.ID, string(b)); err != nil {
		t.Fatal(err)
	}
	r.g.Open(5, "Labelled", "body", "tend")
	r.pass(0)
	x := r.task(5)
	r.g.DropLabel = true
	r.call(coord.Owner, coord.MTaskStatus, task.TaskStatus{ID: x.ID, Status: task.StatusDone}, nil)
	r.pass(31 * time.Second)
	if row, _ := r.team.TrackerIssue(r.x.ID, 5); row.Closed || row.LastError == "" || slices.Contains(r.g.Get(5).Labels, set.AcceptLabel) {
		t.Fatalf("a label the tracker left off is no write-back: %+v %v", row, r.g.Get(5).Labels)
	}
	r.pass(61 * time.Second)
	if row, _ := r.team.TrackerIssue(r.x.ID, 5); !row.Closed || row.LastError != "" || !slices.Contains(r.g.Get(5).Labels, set.AcceptLabel) || r.g.Get(5).Closed {
		t.Fatalf("tried again, the label lands and the issue stays open: %+v %+v", row, r.g.Get(5))
	}
}

func TestARateLimitPausesAndARefusedTokenStopsTheBinding(t *testing.T) {
	r := newSyncRig(t)
	r.g.Open(4, "Limited", "body", "tend")
	r.g.RateLimit = 100
	r.pass(0)
	x := r.tracker()
	if x.Paused.Sub(r.clock) < 25*time.Second || x.Stopped != "" {
		t.Fatalf("Retry-After pauses it: %+v", x)
	}
	before := r.g.Count("GET /api/v1/repos/acme/app/issues")
	r.pass(10 * time.Second)
	if r.g.Count("GET /api/v1/repos/acme/app/issues") != before {
		t.Fatal("nothing goes out while paused")
	}
	r.g.RateLimit = 0
	r.pass(25 * time.Second)
	if r.task(4) == nil || !r.tracker().Paused.IsZero() {
		t.Fatalf("it goes on after the pause: %+v", r.tracker())
	}
	r.g.Refuse = true
	r.g.Change(4, func(i *trackertest.Issue) { i.Body = "changed" })
	r.pass(61 * time.Second)
	if x = r.tracker(); x.Stopped != "auth" || !strings.Contains(x.LastError, "401") {
		t.Fatalf("a refused token stops it: %+v", x)
	}
	r.mu.Lock()
	told := len(r.notices) == 1 && r.notices[0].Event == NoticeTrackerStopped && r.notices[0].To[0] == r.ann
	r.mu.Unlock()
	if !told {
		t.Fatalf("its project's owner hears of it: %+v", r.notices)
	}
	n := r.g.Count("GET /api/v1/user") + r.g.Count("GET /api/v1/repos/acme/app/issues")
	r.pass(time.Hour)
	if r.g.Count("GET /api/v1/user")+r.g.Count("GET /api/v1/repos/acme/app/issues") != n {
		t.Fatal("a stopped binding waits for a new credential")
	}
	r.team.SetTrackerCredential(r.x.ID, "tend-bot", r.s.seal.Seal([]byte("tok")))
	r.g.Refuse = false
	r.pass(61 * time.Second)
	if y := r.task(4); y.Source.Pending == nil {
		t.Fatalf("a new credential picks up where it stopped: %+v", y.Source)
	}
}

func TestAWebhookOnlyMarksItsOwnRepositorysIssue(t *testing.T) {
	sign := func(body string) string {
		m := hmac.New(sha256.New, []byte("hook-key"))
		m.Write([]byte(body))
		return hex.EncodeToString(m.Sum(nil))
	}
	cases := []struct {
		kind, good, other string
		secret, delivery  string
		signature         func(body string) string
	}{
		{tracker.KindGitea, `{"action":"edited","repository":{"id":42},"issue":{"number":7}}`, `{"repository":{"id":9},"issue":{"number":7}}`,
			"X-Gitea-Signature", "X-Gitea-Delivery", sign},
		{tracker.KindGitHub, `{"action":"created","repository":{"id":42},"issue":{"number":7},"comment":{"id":1}}`, `{"repository":{"id":9},"issue":{"number":7}}`,
			"X-Hub-Signature-256", "X-GitHub-Delivery", func(body string) string { return "sha256=" + sign(body) }},
		{tracker.KindGitLab, `{"object_kind":"note","project":{"id":42},"issue":{"iid":7}}`, `{"object_kind":"issue","project":{"id":9},"object_attributes":{"iid":7}}`,
			"X-Gitlab-Token", "X-Gitlab-Event-UUID", func(string) string { return "hook-key" }},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			r := newSyncRigOf(t, c.kind)
			dir, _ := NewDirectory(r.team)
			srv := New(Options{Home: t.TempDir(), Coord: r.c, Dir: dir, Syncer: r.s})
			h := srv.Handler()
			post := func(body, sig, delivery string) int {
				req := httptest.NewRequest(http.MethodPost, "/hooks/"+r.x.ID, bytes.NewReader([]byte(body)))
				req.Header.Set(c.secret, sig)
				req.Header.Set(c.delivery, delivery)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				return w.Code
			}
			if code := post(c.good, c.signature(c.good)[:5], "d1"); code != http.StatusUnauthorized {
				t.Fatalf("an unsigned delivery: %d", code)
			}
			if code := post(c.other, c.signature(c.other), "d2"); code != http.StatusBadRequest {
				t.Fatalf("another repository: %d", code)
			}
			if code := post(c.good, c.signature(c.good), "d3"); code != http.StatusNoContent {
				t.Fatalf("%d", code)
			}
			if rows, _ := r.team.TrackerIssues(r.x.ID, true); len(rows) != 1 || rows[0].Number != 7 {
				t.Fatalf("it marks the issue to read: %+v", rows)
			}
			r.team.PutTrackerIssue(store.TrackerIssue{Tracker: r.x.ID, Number: 7})
			post(c.good, c.signature(c.good), "d3")
			if rows, _ := r.team.TrackerIssues(r.x.ID, true); len(rows) != 0 {
				t.Fatalf("a delivery counts once: %+v", rows)
			}
		})
	}
}

// Each task mirrored on an issue says how that issue syncs: when it last did, what failed, when the next try is.
func TestEachTaskSaysHowItsIssueSyncs(t *testing.T) {
	r := newSyncRig(t)
	r.g.Open(3, "Lost", "body", "tend")
	r.g.LoseCreate = true
	r.pass(0)
	x := r.task(3)
	of := func() TaskSyncState {
		t.Helper()
		list, err := r.s.TaskStates("p1")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range list {
			if s.Task == x.ID {
				return s
			}
		}
		t.Fatalf("no state for %s: %+v", x.ID, list)
		return TaskSyncState{}
	}
	if s := of(); s.State != SyncFailed || s.Error == "" || !s.Next.Equal(r.clock.Add(issueRetry)) || !s.Synced.IsZero() || s.Number != 3 {
		t.Fatalf("the comment's answer was lost: %+v", s)
	}
	r.pass(61 * time.Second)
	if s := of(); s.State != SyncOK || !s.Synced.Equal(r.clock) || s.Error != "" || !s.Next.IsZero() {
		t.Fatalf("the next try took the comment: %+v", s)
	}
	if err := r.team.MarkDirty(r.x.ID, 3); err != nil {
		t.Fatal(err)
	}
	if s := of(); s.State != SyncPending {
		t.Fatalf("to be read again: %+v", s)
	}
	r.g.RateLimit = 5
	r.pass(61 * time.Second)
	if s := of(); s.State != SyncPending || !s.Next.After(r.clock) {
		t.Fatalf("a paused binding tries again later: %+v", s)
	}
	r.g.RateLimit, r.g.Down = 0, true
	r.pass(61 * time.Second)
	if s := of(); s.State != SyncFailed || s.Error == "" {
		t.Fatalf("a tracker that does not answer: %+v", s)
	}
	r.g.Down = false
	r.pass(61 * time.Second)
	if s := of(); s.State != SyncOK {
		t.Fatalf("it answers again: %+v", s)
	}
	r.g.Refuse = true
	r.pass(61 * time.Second)
	if s := of(); s.State != SyncFailed || s.Error == "" || !s.Next.IsZero() {
		t.Fatalf("a stopped binding tries no more until its token is replaced: %+v", s)
	}
	if list, _ := r.s.TaskStates("p2"); len(list) != 0 {
		t.Fatalf("another project's: %+v", list)
	}
}

// The Me page's tracker accounts are the trackers of the caller's own projects, one per address, each with the
// username they signed in with there; another project's tracker is not theirs to see.
func TestTheTrackerAccountsAreThoseOfTheCallersProjects(t *testing.T) {
	r := newRig(t)
	ann, annC := r.member("Ann")
	must(t, r.team.Link(ann.ID, store.Identity{Provider: "github", Issuer: "https://github.com", Subject: "9", Username: "ann-gh"}))
	create := func(id string, members map[string]string) {
		b, _ := json.Marshal(coord.ProjectCreate{ID: id, Name: id, Owner: store.LocalUser})
		if _, err := r.c.HandlerFor(coord.Owner)(context.Background(), &wire.Request{Method: coord.MProjectCreate, CommandID: "p-" + id, Params: b}); err != nil {
			t.Fatal(err)
		}
		for u, role := range members {
			b, _ := json.Marshal(task.MemberSet{Project: id, User: u, Role: role})
			if _, err := r.c.HandlerFor(coord.Owner)(context.Background(), &wire.Request{Method: coord.MProjectMember, CommandID: "m-" + id + u, Params: b}); err != nil {
				t.Fatal(err)
			}
		}
	}
	create("p1", map[string]string{ann.ID: task.RoleReader})
	create("p2", nil)
	for _, x := range []store.Tracker{{Project: "p1", Kind: "github", Base: "https://github.com/", Repo: "a/one"},
		{Project: "p1", Kind: "github", Base: "https://github.com", Repo: "a/two"},
		{Project: "p1", Kind: "gitea", Base: "https://git.example.com", Repo: "a/three"},
		{Project: "p2", Kind: "gitlab", Base: "https://gitlab.com", Repo: "b/four"}} {
		x.Token, x.HookSecret, x.Settings = []byte("sealed"), []byte("sealed"), "{}"
		_, err := r.team.AddTracker(x)
		must(t, err)
	}
	var got []TrackerAccount
	if code := r.api(annC, "GET", "/api/me/trackers", nil, &got); code != http.StatusOK {
		t.Fatal(code)
	}
	want := []TrackerAccount{{Kind: "github", Base: "https://github.com", Login: "ann-gh"}, {Kind: "gitea", Base: "https://git.example.com"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
}
