package herdr

import "testing"

func TestMatchWorkspacesNeverPicksOne(t *testing.T) {
	ws := []Workspace{{WorkspaceID: "a", Label: "api"}, {WorkspaceID: "b", Label: "web"}, {WorkspaceID: "c", Label: "ops"}}
	panes := []Pane{{WorkspaceID: "a", Cwd: "/w/app/api"}, {WorkspaceID: "b", Cwd: "/w/app"}, {WorkspaceID: "c", Cwd: "/w/app/web"}}
	if got := matchWorkspaces(panes, ws, "/w/app"); len(got) != 1 || got[0].Label != "web" {
		t.Fatalf("a pane exactly in the directory wins: %+v", got)
	}
	if got := matchWorkspaces(panes[:1:1], ws, "/w/app"); len(got) != 1 || got[0].Label != "api" {
		t.Fatalf("otherwise the subtree: %+v", got)
	}
	both := []Pane{{WorkspaceID: "a", Cwd: "/w/app"}, {WorkspaceID: "b", Cwd: "/w/app"}}
	if got := matchWorkspaces(both, ws, "/w/app"); len(got) != 2 {
		t.Fatalf("two workspaces in the same directory: both come back for the user to pick: %+v", got)
	}
	if got := matchWorkspaces([]Pane{{WorkspaceID: "a", Cwd: "/w/app/"}, {WorkspaceID: "b", Cwd: "/w/app/web"}}, ws, "/w/app"); len(got) != 1 || got[0].Label != "api" {
		t.Fatalf("the same directory spelled differently is still exact: %+v", got)
	}
	if got := matchWorkspaces(panes, ws, "/elsewhere"); len(got) != 0 {
		t.Fatalf("nothing there: %+v", got)
	}
}
