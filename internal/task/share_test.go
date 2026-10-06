package task

import (
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/journal"
)

// A machine's dispatch share and its session scope are set apart: each event keeps the other half, and the entry goes
// only once both are empty.
func TestTheSessionScopeAndTheDispatchShareKeepEachOther(t *testing.T) {
	s := New()
	ev := journal.NewEvent
	apply(t, s, ev(ESessionsShared, SessionsSet{Machine: "mba", Users: []string{"u_b"}}))
	if sh := s.Shares["mba"]; sh == nil || sh.Sessions == nil || !slices.Equal(sh.Sessions.Users, []string{"u_b"}) || len(sh.Users) != 0 {
		t.Fatalf("a scope alone: %+v", sh)
	}
	before := s.Shares["mba"]
	apply(t, s, ev(EMachineShared, Share{Machine: "mba", Projects: []string{"p1"}, Sessions: &SessionShare{Team: true}}))
	sh := s.Shares["mba"]
	if !slices.Equal(sh.Projects, []string{"p1"}) || sh.Sessions == nil || sh.Sessions.Team || !slices.Equal(sh.Sessions.Users, []string{"u_b"}) {
		t.Fatalf("machine_shared keeps the scope it does not set: %+v %+v", sh, sh.Sessions)
	}
	if before.Projects != nil || sh == before {
		t.Fatal("folding replaces the entry, never changes the one a copy may hold")
	}
	apply(t, s, ev(ESessionsShared, SessionsSet{Machine: "mba", Team: true}))
	if sh := s.Shares["mba"]; !sh.Sessions.Team || len(sh.Sessions.Users) != 0 || !slices.Equal(sh.Projects, []string{"p1"}) {
		t.Fatalf("sessions_shared replaces the scope and keeps the dispatch share: %+v %+v", sh, sh.Sessions)
	}
	apply(t, s, ev(ESessionsShared, SessionsSet{Machine: "mba"}))
	if sh := s.Shares["mba"]; sh == nil || sh.Sessions != nil {
		t.Fatalf("a private scope leaves the dispatch share: %+v", sh)
	}
	apply(t, s, ev(ESessionsShared, SessionsSet{Machine: "mba", Projects: []string{"p1"}}))
	apply(t, s, ev(EMachineShared, Share{Machine: "mba"}))
	if sh := s.Shares["mba"]; sh == nil || sh.Sessions == nil || len(sh.Projects) != 0 {
		t.Fatalf("an empty dispatch share leaves the scope: %+v", sh)
	}
	apply(t, s, ev(ESessionsShared, SessionsSet{Machine: "mba"}))
	if s.Shares["mba"] != nil {
		t.Fatalf("both empty: the entry goes: %+v", s.Shares["mba"])
	}
}

func TestASessionScopeOpensToWhomItNames(t *testing.T) {
	projects := map[string]*Project{"p1": {ID: "p1", Owner: "u_o", Members: map[string]string{"u_r": RoleReader}}}
	for _, c := range []struct {
		scope *SessionShare
		user  string
		want  bool
	}{
		{nil, "u_a", false},
		{&SessionShare{}, "u_a", false},
		{&SessionShare{Users: []string{"u_a"}}, "u_a", true},
		{&SessionShare{Users: []string{"u_a"}}, "u_b", false},
		{&SessionShare{Projects: []string{"p1"}}, "u_r", true},
		{&SessionShare{Projects: []string{"p1"}}, "u_o", true},
		{&SessionShare{Projects: []string{"p1"}}, "u_a", false},
		{&SessionShare{Projects: []string{"gone"}}, "u_a", false},
		{&SessionShare{Team: true}, "u_a", true},
		{&SessionShare{Team: true}, "", false},
	} {
		if got := c.scope.Opens(projects, c.user); got != c.want {
			t.Errorf("%+v opens to %q: %v, want %v", c.scope, c.user, got, c.want)
		}
	}
}
