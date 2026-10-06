package task

import "testing"

func TestProjectOf(t *testing.T) {
	repo := func(dirs map[string]string) Repo { return Repo{Name: "r", Dirs: dirs} }
	projects := map[string]*Project{
		"p_mono": {ID: "p_mono", Repos: []Repo{repo(map[string]string{"mac": "/src/mono", "win": `D:\src\mono`})}},
		"p_web":  {ID: "p_web", Repos: []Repo{repo(map[string]string{"mac": "/src/mono/web"})}},
		"p_two": {ID: "p_two", Repos: []Repo{repo(map[string]string{"linux": "/srv/api"}),
			repo(map[string]string{"linux": "/srv/worker", "mac": "/src/worker"})}},
		"b_same": {ID: "b_same", Repos: []Repo{repo(map[string]string{"linux": "/srv/shared/"})}},
		"c_same": {ID: "c_same", Repos: []Repo{repo(map[string]string{"linux": "/srv/shared"})}},
		"p_home": {ID: "p_home", Repos: []Repo{repo(map[string]string{"mac": "~/notes"})}},
	}
	for _, c := range []struct {
		name, machine, goos, dir, want string
	}{
		{"the repository's directory", "mac", "darwin", "/src/mono", "p_mono"},
		{"below it", "mac", "darwin", "/src/mono/cmd/x", "p_mono"},
		{"the deeper of nested directories", "mac", "darwin", "/src/mono/web/ui", "p_web"},
		{"a linked worktree through its main checkout", "mac", "darwin", "/src/mono", "p_mono"},
		{"the worktree's own directory belongs nowhere", "mac", "darwin", "/src/mono-wt/fix", ""},
		{"a sibling with a common prefix", "mac", "darwin", "/src/monolith", ""},
		{"a later repository of a project", "linux", "linux", "/srv/worker/x", "p_two"},
		{"another machine's directory does not count", "linux", "linux", "/src/mono/x", ""},
		{"a machine with no directories", "elsewhere", "linux", "/src/mono", ""},
		{"Windows rules on a Windows machine", "win", "windows", `d:\SRC\Mono\x`, "p_mono"},
		{"no match", "mac", "darwin", "/tmp/x", ""},
		{"equal length goes to the first id", "linux", "linux", "/srv/shared/x", "b_same"},
		{"a home-relative directory matches nothing", "mac", "darwin", "/Users/me/notes", ""},
		{"no directory", "mac", "darwin", "", ""},
	} {
		got := ProjectOf(projects, c.machine, c.goos, c.dir)
		if id := idOf(got); id != c.want {
			t.Errorf("%s: ProjectOf(%s, %q) = %q, want %q", c.name, c.machine, c.dir, id, c.want)
		}
	}
	if ProjectOf(nil, "mac", "darwin", "/src/mono") != nil {
		t.Error("no projects: a project")
	}
}

func idOf(p *Project) string {
	if p == nil {
		return ""
	}
	return p.ID
}

// TestMayAttach: a project's owner and admins change its directories on any machine, a participant only on a machine
// they own, a reader and someone outside never.
func TestMayAttach(t *testing.T) {
	p := &Project{ID: "p1", Owner: "ann", Members: map[string]string{"bob": RoleParticipant, "dee": RoleReader}}
	for _, c := range []struct {
		user    string
		admin   bool
		machine string // its owner
		want    bool
	}{
		{"ann", false, "bob", true},
		{"root", true, "ann", true},
		{"bob", false, "bob", true},
		{"bob", false, "ann", false},
		{"dee", false, "dee", false},
		{"eve", false, "eve", false},
		{"", false, "", false},
	} {
		if got := MayAttach(p, c.user, c.admin, c.machine); got != c.want {
			t.Errorf("%s (admin %v) on %s's machine: %v, want %v", c.user, c.admin, c.machine, got, c.want)
		}
	}
	if MayAttach(nil, "root", true, "ann") {
		t.Error("no project, still attachable")
	}
}
