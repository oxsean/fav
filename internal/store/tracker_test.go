package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestATrackerBindingKeepsItsIssuesUntilItGoes(t *testing.T) {
	tm := openTeam(t)
	x, err := tm.AddTracker(Tracker{Project: "p1", Kind: "gitea", Base: "http://git", Repo: "o/r", RepoID: 7, Bot: "bot",
		Token: []byte("sealed"), HookSecret: []byte("sealed2"), Settings: "{}", CreatedBy: LocalUser})
	must(t, err)
	if _, err := tm.AddTracker(Tracker{Project: "p2", Kind: "gitea", Base: "http://git", Repo: "o/renamed", RepoID: 7, Token: []byte{}, HookSecret: []byte{}}); !errors.Is(err, ErrExists) {
		t.Fatalf("one binding per repository: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	must(t, tm.Polled(x.ID, now, `"e1"`, now))
	must(t, tm.TrackerResult(x.ID, time.Time{}, now.Add(time.Minute), "", "rate limited"))
	got, err := tm.Tracker(x.ID)
	if err != nil || !got.Cursor.Equal(now) || got.ETag != `"e1"` || got.Paused.IsZero() || string(got.Token) != "sealed" {
		t.Fatalf("%+v %v", got, err)
	}
	must(t, tm.TrackerResult(x.ID, now, time.Time{}, "", ""))
	if got, _ = tm.Tracker(x.ID); !got.Paused.IsZero() || got.LastError != "" || got.LastOK.IsZero() {
		t.Fatalf("a success clears the pause: %+v", got)
	}
	must(t, tm.MarkDirty(x.ID, 3))
	must(t, tm.MarkDirty(x.ID, 3))
	is, err := tm.TrackerIssues(x.ID, true)
	if err != nil || len(is) != 1 || is[0].Number != 3 || !is[0].Dirty {
		t.Fatalf("%+v %v", is, err)
	}
	must(t, tm.PutTrackerIssue(TrackerIssue{Tracker: x.ID, Number: 3, Task: "t_1", CommentID: 99, BodyHash: "h", Written: now, Closed: true,
		Applied: "label:tend:accepted"}))
	if is, _ = tm.TrackerIssues(x.ID, true); len(is) != 0 {
		t.Fatalf("read: %+v", is)
	}
	if i, _ := tm.TrackerIssue(x.ID, 3); i.CommentID != 99 || i.Task != "t_1" || !i.Written.Equal(now) || !i.Closed || i.Applied != "label:tend:accepted" {
		t.Fatalf("%+v", i)
	}
	if first, _ := tm.TakeDelivery("d1"); !first {
		t.Fatal("first delivery")
	}
	if again, _ := tm.TakeDelivery("d1"); again {
		t.Fatal("a delivery counts once")
	}
	must(t, tm.RemoveTracker(x.ID))
	if is, _ = tm.TrackerIssues(x.ID, false); len(is) != 0 {
		t.Fatalf("its issues go with it: %+v", is)
	}
}

func TestAnAssigneeMapsToTheMemberWhoSignedInThere(t *testing.T) {
	tm := openTeam(t)
	must(t, tm.AddAdmit(Admit{Kind: AdmitDomain, Value: "corp.example", Role: RoleMember}))
	id := gitea("1", "ann@corp.example", true)
	id.Username = "Ann"
	ann, err := tm.Admit(id, "")
	must(t, err)
	if u, _ := tm.UserByLogin(id.Issuer+"/", "ann"); u != ann.ID {
		t.Fatalf("by login at that tracker: %q", u)
	}
	if u, _ := tm.UserByLogin("https://other.example", "ann"); u != "" {
		t.Fatalf("not at another: %q", u)
	}
	if l, _ := tm.LoginOf(ann.ID, id.Issuer); l != "Ann" {
		t.Fatalf("and back: %q", l)
	}
}

// before0010 puts tracker_issues back as it was before 0010.
func before0010(t *testing.T, path string) {
	exec(t, path, `ALTER TABLE tracker_issues DROP COLUMN applied`)
}

// A write-back done before 0010 stays done, and tend claims none of it as its own: it cannot tell a close it made from
// one it found.
func TestUpgradingTheWriteBacksClaimsNoneOfThemAsTends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, File)
	tm, err := OpenTeam(path)
	if err != nil {
		t.Fatal(err)
	}
	x, err := tm.AddTracker(Tracker{Project: "p1", Kind: "gitea", Base: "http://git", Repo: "o/r", RepoID: 9, Bot: "bot",
		Token: []byte("sealed"), HookSecret: []byte("sealed2"), Settings: "{}", CreatedBy: LocalUser})
	must(t, err)
	tm.Close()
	before0014(t, path)
	before0013(t, path)
	before0012(t, path)
	before0011(t, path)
	before0010(t, path)
	exec(t, path, `INSERT INTO tracker_issues (tracker, number, task, closed, dirty) VALUES ('`+x.ID+`', 3, 't_1', 1, 0)`)
	setVersion(t, path, 9)
	if tm, err = OpenTeam(path); err != nil {
		t.Fatal(err)
	}
	defer tm.Close()
	if i, err := tm.TrackerIssue(x.ID, 3); err != nil || !i.Closed || i.Applied != "" || i.Task != "t_1" {
		t.Fatalf("%+v %v", i, err)
	}
	if baks, _ := filepath.Glob(filepath.Join(dir, File+".v9-*.bak")); len(baks) != 1 {
		t.Fatalf("no copy of the v9 database: %v", baks)
	}
}
