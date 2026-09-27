package store

import (
	"errors"
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
	must(t, tm.PutTrackerIssue(TrackerIssue{Tracker: x.ID, Number: 3, Task: "t_1", CommentID: 99, BodyHash: "h", Written: now}))
	if is, _ = tm.TrackerIssues(x.ID, true); len(is) != 0 {
		t.Fatalf("read: %+v", is)
	}
	if i, _ := tm.TrackerIssue(x.ID, 3); i.CommentID != 99 || i.Task != "t_1" || !i.Written.Equal(now) {
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
