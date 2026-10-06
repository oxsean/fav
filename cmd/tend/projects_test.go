package main

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/projects"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// addIn runs tend add as the fixture session called name, in its directory, with the skill naming project.
func addIn(t *testing.T, d *fixture.Dataset, name, project string) *tend.Rec {
	t.Helper()
	x := d.Get(name)
	t.Chdir(x.Cwd)
	t.Setenv("CLAUDE_CODE_SESSION_ID", x.ID)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.WriteString(`{"title":"Saved","summary":"what it did","tags":["t"],"project":"` + project + `"}`)
	w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	stdoutOf(t, func() { err = run([]string{"add", "--provider", tend.ProviderClaude}) })
	if err != nil {
		t.Fatal(err)
	}
	s, err := tend.Open()
	if err != nil {
		t.Fatal(err)
	}
	return s.BySession(tend.ProviderClaude, x.ID)
}

// claudeIn is a Claude session of the fixture whose directory is dir.
func claudeIn(t *testing.T, d *fixture.Dataset, dir string) string {
	t.Helper()
	for _, x := range d.Sessions {
		if x.Provider == tend.ProviderClaude && x.Cwd == dir && !x.Agent {
			return x.Name
		}
	}
	t.Fatalf("no Claude session in %s", dir)
	return ""
}

func TestAddNamesTheProjectItsDirectoryIsIn(t *testing.T) {
	d := machine(t)
	if r := addIn(t, d, claudeIn(t, d, filepath.Join(d.Work, "webapp")), "webapp"); r.Project != "Webapp" {
		t.Errorf("a session in the project's directory is saved under the project's name: %q", r.Project)
	}
	if r := addIn(t, d, claudeIn(t, d, filepath.Join(d.Work, "notes-api")), "notes"); r.Project != "notes" {
		t.Errorf("outside every project the skill's name stays: %q", r.Project)
	}
}

func TestProjectFilterListsTheProjectsSessions(t *testing.T) {
	d := machine(t)
	webapp := filepath.Join(d.Work, "webapp")
	got := sessionIDs(t, "sessions", "status:all", "project:"+fixture.Project, "--json")
	for _, x := range d.Sessions {
		in := x.Listed && (paths.Under(x.Cwd, webapp) || x.Name == "linked-worktree")
		if got[x.ID] != in {
			t.Errorf("project:%s lists %s (%s): %v, want %v", fixture.Project, x.Name, x.Cwd, got[x.ID], in)
		}
	}
	if got := sessionIDs(t, "sessions", "project:notes-api", "--json"); len(got) == 0 {
		t.Error("project: still takes an automatic group's name")
	}
	if choices := projectChoices(mustRecs(t)); len(choices) == 0 || !strings.HasPrefix(choices[0], fixture.Project+"  ") {
		t.Errorf("the fzf picker lists the project first, by its id: %q", choices)
	}
}

func mustRecs(t *testing.T) []*tend.Rec {
	t.Helper()
	sessionProjects = loadedProjects()
	s, err := tend.Open()
	if err != nil {
		t.Fatal(err)
	}
	return scopeRecs(s, "")
}

func loadedProjects() func() *projects.Snapshot {
	s := loadProjects()
	return func() *projects.Snapshot { return s }
}

// served points the configuration at a server on addr, with a token file.
func served(t *testing.T, addr string) {
	t.Helper()
	tok := filepath.Join(t.TempDir(), "client.tok")
	os.WriteFile(tok, []byte("not-a-real-token\n"), 0o600)
	cfg := tend.LoadConfig()
	cfg.Coordinator = &tend.CoordinatorConfig{URL: "http://" + addr, TokenFile: tok}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

// closedAddr is an address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestAddWithTheServerDownKeepsTheSkillsName(t *testing.T) {
	d := machine(t)
	served(t, closedAddr(t))
	start := time.Now()
	if r := addIn(t, d, claudeIn(t, d, filepath.Join(d.Work, "webapp")), "webapp"); r.Project != "webapp" {
		t.Errorf("no server, no project: the skill's name: %q", r.Project)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("one short dial, not a long wait: %v", time.Since(start))
	}
	if _, fresh, failed := projects.LoadTable(projects.TablePath(d.Home), time.Now()); fresh || !failed {
		t.Error("the failed dial is recorded, so the next commands within the table's life do not dial")
	}
	if fi, err := os.Stat(projects.TablePath(d.Home)); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("the table is the user's only: %v", err)
	}
}

func TestAddReadsAFreshProjectTable(t *testing.T) {
	d := machine(t)
	served(t, closedAddr(t))
	webapp := filepath.Join(d.Work, "webapp")
	table := projects.Snapshot{State: projects.Ready, At: time.Now(), Here: "mac", Viewer: projects.Viewer{User: "u_ann"},
		Projects: map[string]*task.Project{"p_web": {ID: "p_web", Name: "Web shop", Owner: "u_ann",
			Repos: []task.Repo{{Name: "webapp", Dirs: map[string]string{"mac": webapp}}}}}}
	if err := projects.SaveTable(projects.TablePath(d.Home), table, time.Time{}); err != nil {
		t.Fatal(err)
	}
	name := claudeIn(t, d, webapp)
	if r := addIn(t, d, name, "webapp"); r.Project != "Web shop" {
		t.Errorf("a fresh table names the project: %q", r.Project)
	}

	table.At = time.Now().Add(-projects.TableFor - time.Second)
	projects.SaveTable(projects.TablePath(d.Home), table, time.Time{})
	if r := addIn(t, d, name, "webapp"); r.Project != "webapp" {
		t.Errorf("an older table is never used: %q", r.Project)
	}
}
