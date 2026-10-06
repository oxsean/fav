package coord

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// projectStage is an env with machines far (ann's) and bobs (bob's), which never connect, and project p1 owned by ann
// with bob participating and dee reading; both machines are shared with p1. Mode 1 has the same machines, all its owner's.
func projectStage(t *testing.T, teamMode bool) *env {
	cfg := tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}, {Name: "bobs", SSH: "bobs"}}}
	var e *env
	if !teamMode {
		e = newEnv(t, cfg)
	} else {
		e = team(t, cfg)
		e.owner = func(m string) string {
			if m == "bobs" {
				return bob.User
			}
			return ann.User
		}
	}
	e.dial = unreachable
	e.start()
	if !teamMode {
		return e
	}
	if err := callAs(e.as(root), MProjectCreate, "p", ProjectCreate{ID: "p1", Name: "One", Owner: ann.User}, nil); err != nil {
		t.Fatal(err)
	}
	for _, m := range []task.MemberSet{{Project: "p1", User: bob.User, Role: task.RoleParticipant}, {Project: "p1", User: dee.User, Role: task.RoleReader}} {
		if err := callAs(e.as(ann), MProjectMember, "m-"+m.User, m, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []struct {
		by      Principal
		machine string
	}{{ann, "far"}, {bob, "bobs"}} {
		if err := callAs(e.as(s.by), MMachineShare, "s-"+s.machine, task.Share{Machine: s.machine, Projects: []string{"p1"}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *env) projectOf(id string) task.Project {
	e.t.Helper()
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	pr := e.c.st.Projects[id]
	if pr == nil {
		e.t.Fatalf("no project %s", id)
	}
	return *pr
}

// TestWhoMayCreateAProjectAndChangeItsDirectories: for each viewer and each project action, the coordinator answers as
// §2.4 of the design says: anyone creates a project they own, only an admin names another owner; the project's owner and
// admins attach and detach on any machine they see, a participant only on their own machine, a reader never, and someone
// outside the project does not know it is there. Mode 1's owner does everything.
func TestWhoMayCreateAProjectAndChangeItsDirectories(t *testing.T) {
	type want map[string]string // action → "" taken, else the error code
	cases := map[bool]map[Principal]want{
		true: {
			root: {"create": "", "create_for_other": "", "attach_far": "", "attach_bobs": "", "detach_far": "", "detach_bobs": ""},
			ann: {"create": "", "create_for_other": wire.CodeUnauthorized, "attach_far": "", "attach_bobs": "", "detach_far": "",
				"detach_bobs": ""},
			bob: {"create": "", "create_for_other": wire.CodeUnauthorized, "attach_far": wire.CodeUnauthorized, "attach_bobs": "",
				"detach_far": wire.CodeUnauthorized, "detach_bobs": ""},
			cy: {"create": "", "create_for_other": wire.CodeUnauthorized, "attach_far": wire.CodeNotFound, "attach_bobs": wire.CodeNotFound,
				"detach_far": wire.CodeNotFound, "detach_bobs": wire.CodeNotFound},
			dee: {"create": "", "create_for_other": wire.CodeUnauthorized, "attach_far": wire.CodeUnauthorized,
				"attach_bobs": wire.CodeUnauthorized, "detach_far": wire.CodeUnauthorized, "detach_bobs": wire.CodeUnauthorized},
		},
		false: {
			Owner: {"create": "", "create_for_other": wire.CodeNotFound, "attach_far": "", "attach_bobs": "", "detach_far": "", "detach_bobs": ""},
		},
	}
	for _, teamMode := range []bool{true, false} {
		e := projectStage(t, teamMode)
		project, manager := "p1", root
		if !teamMode {
			project, manager = "p1", Owner
			if err := callAs(e.as(Owner), MProjectCreate, "p", ProjectCreate{ID: "p1", Name: "One"}, nil); err != nil {
				t.Fatal(err)
			}
		}
		n := 0
		for p, wants := range cases[teamMode] {
			for act, code := range wants {
				n++
				id := strconv.Itoa(n)
				dir := "/srv/" + act + "-" + id
				var err error
				switch act {
				case "create":
					var pr task.Project
					err = callAs(e.as(p), MProjectCreate, "c"+id, ProjectCreate{Name: "mine " + id}, &pr)
					if err == nil && pr.Owner != p.User {
						t.Errorf("team %v, %s: their own project is owned by %q", teamMode, p.User, pr.Owner)
					}
				case "create_for_other":
					other := ann.User
					if p == ann {
						other = bob.User
					}
					err = callAs(e.as(p), MProjectCreate, "c"+id, ProjectCreate{Name: "theirs " + id, Owner: other}, nil)
				case "attach_far", "attach_bobs":
					err = callAs(e.as(p), MProjectAttach, "a"+id, ProjectAttach{Project: project, Machine: act[len("attach_"):], Dir: dir}, nil)
				case "detach_far", "detach_bobs":
					m := act[len("detach_"):]
					if err := callAs(e.as(manager), MProjectAttach, "pre"+id, ProjectAttach{Project: project, Machine: m, Dir: dir}, nil); err != nil {
						t.Fatal(err)
					}
					err = callAs(e.as(p), MProjectDetach, "d"+id, ProjectDetach{Project: project, Machine: m, Dir: dir}, nil)
				}
				if got := wire.Code(err); got != code {
					t.Errorf("team %v, %s %s: %v, want %q", teamMode, p.User, act, err, code)
				}
			}
		}
	}
}

// TestAttachJoinsTheRepositoryOfTheSameRemoteAndDetachDropsAnEmptyOne, in both modes: attach joins the repository whose
// remote is the directory's, else adds one named after the directory; the same directory twice changes nothing; detach
// takes the directory out and drops the repository it leaves empty.
func TestAttachJoinsTheRepositoryOfTheSameRemoteAndDetachDropsAnEmptyOne(t *testing.T) {
	for _, teamMode := range []bool{true, false} {
		e := projectStage(t, teamMode)
		who := Owner
		if teamMode {
			who = ann
		}
		cli := e.as(who)
		var pr task.Project
		if err := callAs(cli, MProjectCreate, "new", ProjectCreate{ID: "shop", Name: "Shop"}, &pr); err != nil {
			t.Fatal(err)
		}
		const remote = "https://example.com/acme/app.git"
		n := 0
		attach := func(machine, dir, remote string) (task.Project, error) {
			n++
			var out task.Project
			err := callAs(cli, MProjectAttach, "a"+strconv.Itoa(n), ProjectAttach{Project: "shop", Machine: machine, Dir: dir, Remote: remote}, &out)
			return out, err
		}
		detach := func(machine, dir string) (task.Project, error) {
			n++
			var out task.Project
			err := callAs(cli, MProjectDetach, "d"+strconv.Itoa(n), ProjectDetach{Project: "shop", Machine: machine, Dir: dir}, &out)
			return out, err
		}
		dirsOf := func(p task.Project) map[string]map[string]string {
			out := map[string]map[string]string{}
			for _, r := range p.Repos {
				out[r.Name+" "+r.Remote] = r.Dirs
			}
			return out
		}

		got, err := attach("far", "/srv/app", remote)
		if err != nil || len(got.Repos) != 1 || got.Repos[0].Name != "app" || got.Repos[0].Remote != remote || got.Repos[0].Dirs["far"] != "/srv/app" {
			t.Fatalf("team %v: the first directory makes a repository named after it: %+v %v", teamMode, got.Repos, err)
		}
		rev := e.projectOf("shop").Rev
		if again, err := attach("far", "/srv/app", remote); err != nil || e.projectOf("shop").Rev != rev || len(again.Repos) != 1 {
			t.Fatalf("team %v: the same directory again changes nothing: %+v %v", teamMode, again.Repos, err)
		}
		if got, err = attach("bobs", "/home/bob/app", remote); err != nil || len(got.Repos) != 1 || got.Repos[0].Dirs["bobs"] != "/home/bob/app" {
			t.Fatalf("team %v: a checkout of the same remote on another machine joins its repository: %+v %v", teamMode, got.Repos, err)
		}
		if got, err = attach("far", "/opt/app", ""); err != nil || len(got.Repos) != 2 || got.Repos[1].Name != "app-2" || got.Repos[1].Remote != "" {
			t.Fatalf("team %v: a directory without a remote is a repository of its own, its name told apart: %+v %v", teamMode, got.Repos, err)
		}
		if got, err = attach("far", "/work/app", remote); err != nil || len(got.Repos) != 3 || got.Repos[2].Name != "app-3" || got.Repos[2].Remote != remote {
			t.Fatalf("team %v: a second checkout of the remote on one machine is another repository: %+v %v", teamMode, got.Repos, err)
		}
		other := `C:\srv\app`
		if runtime.GOOS == "windows" {
			other = "/srv/app"
		}
		for _, dir := range []string{"~/app", "app", "", "/srv/../etc", other} {
			machine := "far"
			if dir == other || dir == "/srv/../etc" {
				machine = Local // its OS is known: the path must be in its form
			}
			if _, err := attach(machine, dir, ""); wire.Code(err) != wire.CodeBadRequest {
				t.Errorf("team %v: %q on %s is no absolute directory there: %v", teamMode, dir, machine, err)
			}
		}
		if _, err := attach("nowhere", "/srv/app", ""); wire.Code(err) != wire.CodeNotFound {
			t.Errorf("team %v: an unknown machine: %v", teamMode, err)
		}

		if got, err = detach("bobs", "/home/bob/app"); err != nil || len(got.Repos) != 3 || got.Repos[0].Dirs["bobs"] != "" || got.Repos[0].Dirs["far"] != "/srv/app" {
			t.Fatalf("team %v: detach takes one directory out: %+v %v", teamMode, dirsOf(got), err)
		}
		if got, err = detach("far", "/srv/app"); err != nil || len(got.Repos) != 2 || got.Repos[0].Name != "app-2" {
			t.Fatalf("team %v: a repository left without a directory goes: %+v %v", teamMode, dirsOf(got), err)
		}
		if _, err := detach("far", "/srv/app"); wire.Code(err) != wire.CodeNotFound {
			t.Errorf("team %v: a directory the project does not have: %v", teamMode, err)
		}
		var replay task.Project
		if err := callAs(cli, MProjectDetach, "d"+strconv.Itoa(n-1), ProjectDetach{Project: "shop", Machine: "far", Dir: "/srv/app"}, &replay); err != nil ||
			len(replay.Repos) != 2 {
			t.Errorf("team %v: a replay answers what the detach answered: %+v %v", teamMode, replay.Repos, err)
		}
		if err := callAs(cli, MProjectCreate, "none", ProjectCreate{ID: "none", Name: "None"}, nil); wire.Code(err) != wire.CodeBadRequest {
			t.Errorf("team %v: none names no project (a page's filter for sessions in none): %v", teamMode, err)
		}
	}
}

// TestAProjectDirectoryWrittenWithTheHomeStaysAsItIs: an entry under ~ that an earlier edit wrote is kept, matches no
// session, and detach takes it out as it is written.
func TestAProjectDirectoryWrittenWithTheHomeStaysAsItIs(t *testing.T) {
	e := projectStage(t, false)
	repos := []task.Repo{{Name: "app", Dirs: map[string]string{"far": "~/work/app"}}}
	e.must(MProjectCreate, ProjectCreate{ID: "app", Name: "App"}, nil)
	e.must(MProjectEdit, task.ProjectEdit{ID: "app", Repos: &repos}, nil)
	var got task.Project
	e.must(MProjectDetach, ProjectDetach{Project: "app", Machine: "far", Dir: "~/work/app"}, &got)
	if len(got.Repos) != 0 {
		t.Fatalf("detach takes the ~ entry out: %+v", got.Repos)
	}
}

// projectsOn is the project of each of this machine's sessions as sessions.query lists them to cli; read: the machine
// was asked.
func projectsOn(t *testing.T, cli *wire.Conn) (projects map[string]string, read bool) {
	t.Helper()
	projects = map[string]string{}
	sq := SessionsQuery{Q: "host:" + Local + " status:all turns:0", All: true, Limit: pageMax}
	for {
		page := query(t, cli, sq)
		if answerOf(page, Local) == nil {
			return projects, false
		}
		for _, r := range page.Rows {
			projects[r.Provider+":"+r.SessionID] = r.Project
		}
		if page.Next == nil {
			return projects, true
		}
		sq.After = page.Next
	}
}

// TestSessionsQuerySaysWhichProjectEachSessionIsIn: sessions.query lists the machine's sessions with the project each
// belongs to among those the caller sees; only whom the machine's owner lets read it is asked, as node.call. Machines
// carry their node's id.
func TestSessionsQuerySaysWhichProjectEachSessionIsIn(t *testing.T) {
	d, err := fixture.Build(filepath.Join(t.TempDir(), "machine"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEND_HOME", d.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
	t.Setenv("CODEX_HOME", d.Codex)
	_, gitErr := exec.LookPath("git")
	key := func(name string) string { s := d.Get(name); return s.Provider + ":" + s.ID }
	webapp := filepath.Join(d.Work, "webapp")

	for _, teamMode := range []bool{true, false} {
		var e *env
		mine, admin := Owner, Owner
		if teamMode {
			e = team(t, tend.Config{})
			mine, admin = ann, root
		} else {
			e = newEnv(t, tend.Config{})
		}
		e.start()
		if err := callAs(e.as(mine), MProjectCreate, "web", ProjectCreate{ID: "web", Name: "Web"}, nil); err != nil {
			t.Fatal(err)
		}
		if err := callAs(e.as(mine), MProjectAttach, "web-a", ProjectAttach{Project: "web", Machine: Local, Dir: webapp}, nil); err != nil {
			t.Fatal(err)
		}
		if teamMode { // a deeper directory in a project ann does not see
			if err := callAs(e.as(root), MProjectCreate, "deep", ProjectCreate{ID: "deep", Name: "Deep"}, nil); err != nil {
				t.Fatal(err)
			}
			if err := callAs(e.as(root), MProjectAttach, "deep-a", ProjectAttach{Project: "deep", Machine: Local,
				Dir: filepath.Join(webapp, "src", "auth")}, nil); err != nil {
				t.Fatal(err)
			}
		}

		got, read := projectsOn(t, e.as(mine))
		if !read || len(got) == 0 {
			t.Fatalf("team %v: the machine's sessions: %d %v", teamMode, len(got), read)
		}
		want := map[string]string{"oauth": "web", "worktree": "web", "webapp-sub": "web", "codex-cli": "web", "same-name": "", "pagination": ""}
		if gitErr == nil {
			want["linked-worktree"] = "web" // by its main checkout
		}
		for name, id := range want {
			if got[key(name)] != id {
				t.Errorf("team %v, %s: project %q, want %q", teamMode, name, got[key(name)], id)
			}
		}
		if !teamMode {
			continue
		}
		if _, read := projectsOn(t, e.as(admin)); read {
			t.Errorf("an admin reads no one else's machine unless its owner says so")
		}
		if err := callAs(e.as(mine), MMachineSessions, "scope", task.SessionsSet{Machine: Local, Users: []string{admin.User}}, nil); err != nil {
			t.Fatal(err)
		}
		if got, read := projectsOn(t, e.as(admin)); !read || got[key("webapp-sub")] != "deep" || got[key("oauth")] != "" {
			t.Errorf("an admin the scope names sees its own deep, not ann's personal web: %v %v", got, read)
		}
		if err := callAs(e.as(mine), MProjectMember, "m-bob", task.MemberSet{Project: "web", User: bob.User, Role: task.RoleParticipant}, nil); err != nil {
			t.Fatal(err)
		}
		if got, read := projectsOn(t, e.as(admin)); !read || got[key("webapp-sub")] != "deep" || got[key("oauth")] != "web" {
			t.Errorf("an admin sees a team's web too, the deeper directory wins: %v %v", got, read)
		}
		for _, p := range []Principal{bob, cy} {
			if _, read := projectsOn(t, e.as(p)); read {
				t.Errorf("%s reads another's machine", p.User)
			}
		}

		var ms Machines
		if err := callAs(e.as(mine), MMachineList, "", MachinesParams{Connect: true}, &ms); err != nil {
			t.Fatal(err)
		}
		at := slices.IndexFunc(ms.Machines, func(m Machine) bool { return m.Name == Local })
		if at < 0 || ms.Machines[at].NodeID == "" || ms.Machines[at].NodeID != e.c.opt.Node.ID() {
			t.Errorf("the machine carries its node's id: %+v", ms.Machines)
		}
	}
}
