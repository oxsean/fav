package envcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/testkit"
)

// fakeVersion + a CLI's name is what this test binary prints for `<name> --version` when machine links it as that CLI.
const fakeVersion = "ENVCHECK_FAKE_VERSION_"

func TestMain(m *testing.M) {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if out, ok := os.LookupEnv(fakeVersion + name); ok && slices.Equal(os.Args[1:], []string{"--version"}) {
		fmt.Println(out)
		os.Exit(0)
	}
	testkit.Main(m)
}

// machine builds a fixture dataset and points this process at it, with claude and codex on PATH printing versions:
// links to this test binary, ⚠️ never a new script, which macOS checks the first time it runs, for seconds under load.
func machine(t *testing.T, claude, codex string) *fixture.Dataset {
	t.Helper()
	d, err := fixture.Build(filepath.Join(t.TempDir(), "machine"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	use(t, d)
	bin := filepath.Join(d.Root, "bin")
	os.MkdirAll(bin, 0o755)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ext := filepath.Ext(self)
	if ext != ".exe" {
		ext = ""
	}
	for name, out := range map[string]string{"claude": claude, "codex": codex} {
		if out == "" {
			continue
		}
		link := filepath.Join(bin, name+ext)
		if err := os.Link(self, link); err != nil {
			if err := os.Symlink(self, link); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv(fakeVersion+name, out)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return d
}

func use(t *testing.T, d *fixture.Dataset) {
	t.Setenv("TEND_HOME", d.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
	t.Setenv("CODEX_HOME", d.Codex)
}

func webapp(d *fixture.Dataset) string { return filepath.Join(d.Work, "webapp") }

func TestCollectReadsNamesVersionsAndHashesOnly(t *testing.T) {
	d := machine(t, "2.1.292 (Claude Code)", "codex-cli 0.156.1")
	p := Collect(context.Background(), webapp(d))
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{fixture.Secret, fixture.SecretEmail, "staging API", "Run npm test", "gateway.example.com/v1"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("the print carries %q: %s", secret, b)
		}
	}
	if len(p.Unknown) != 0 {
		t.Errorf("every source was readable: unknown %v", p.Unknown)
	}
	if !slices.Equal(p.CLIs, []CLI{{Name: "claude", Found: true, Version: "2.1.292"}, {Name: "codex", Found: true, Version: "0.156.1"}}) {
		t.Errorf("clis: %+v", p.CLIs)
	}
	var files []string
	for _, f := range p.Files {
		files = append(files, f.Kind+":"+f.Name)
		if f.SHA == "" || f.Norm == "" {
			t.Errorf("%s: no hashes", f.Name)
		}
	}
	if want := []string{"claude:CLAUDE.md", "codex:AGENTS.md", "dir:AGENTS.md", "dir:CLAUDE.local.md", "dir:CLAUDE.md", "dir:docs/oauth.md"}; !slices.Equal(files, want) {
		t.Errorf("instruction files %v, want %v", files, want)
	}
	if want := []string{"codex:rescue", "deploy", "review", "tend"}; !slices.Equal(p.Skills, want) {
		t.Errorf("skills %v, want %v", p.Skills, want)
	}
	if want := []string{"docs", "gitea", "local-db", "proj-only"}; !slices.Equal(p.MCP, want) || !slices.Equal(p.CodexMCP, []string{"gitea"}) {
		t.Errorf("mcp %v / %v", p.MCP, p.CodexMCP)
	}
	if want := (Project{AllowedTools: []string{"Bash", "WebFetch"}, MCPJSON: []string{"local-db"}, Trusted: true, CodexTrust: "trusted"}); !equalProject(p.Project, want) {
		t.Errorf("project %+v, want %+v", p.Project, want)
	}
	if want := (Provider{ClaudeEnv: []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "DISABLE_TELEMETRY", "LOCAL_TOKEN"},
		ClaudeHost: "gateway.example.com", Codex: "azure", CodexHost: "ai.example.net"}); !slices.Equal(p.Provider.ClaudeEnv, want.ClaudeEnv) ||
		p.Provider.ClaudeHost != want.ClaudeHost || p.Provider.Codex != want.Codex || p.Provider.CodexHost != want.CodexHost {
		t.Errorf("provider %+v, want %+v", p.Provider, want)
	}
	if _, err := exec.LookPath("git"); err == nil && (!p.Git.Repo || p.Git.Branch != "main" || p.Git.Remote != "https://example.com/acme/webapp.git" ||
		p.Git.Dirty == 0 || len(p.Git.Head) != 40) {
		t.Errorf("git %+v", p.Git)
	}

	home := Collect(context.Background(), "")
	if home.Git.Repo || slices.ContainsFunc(home.Files, func(f File) bool { return f.Kind == KindDir }) || home.Project.Trusted ||
		!slices.Equal(home.MCP, []string{"docs", "gitea"}) {
		t.Errorf("the machine alone has no directory's files, checkout or project settings: %+v", home)
	}
	if gone := Collect(context.Background(), filepath.Join(d.Work, "nowhere")); !gone.NoDir || gone.Git.Repo {
		t.Errorf("a directory not here: %+v", gone)
	}
}

func equalProject(a, b Project) bool {
	return slices.Equal(a.AllowedTools, b.AllowedTools) && slices.Equal(a.MCPJSON, b.MCPJSON) && a.Trusted == b.Trusted && a.CodexTrust == b.CodexTrust
}

func TestUnreadableSourcesAreUnknownAndMissingOnesAreNot(t *testing.T) {
	d := machine(t, "", "codex-cli 0.156.1") // claude stays the failing one testkit puts on PATH
	os.WriteFile(filepath.Join(d.Claude, ".claude.json"), []byte(`{"projects":{`), 0o600)
	os.WriteFile(filepath.Join(d.Codex, "config.toml"), []byte("[projects.\"unterminated\n"), 0o600)
	os.Remove(filepath.Join(d.Claude, "settings.json"))
	p := Collect(context.Background(), webapp(d))
	for _, want := range []string{"cli:claude", "claude_json", "codex_config"} {
		if !slices.Contains(p.Unknown, want) {
			t.Errorf("%s not unknown: %v", want, p.Unknown)
		}
	}
	if slices.Contains(p.Unknown, "claude_settings") {
		t.Errorf("a settings file that is not there is not unknown: %v", p.Unknown)
	}
	if c := p.CLIs[0]; c.Name != "claude" || !c.Found || c.Version != "" {
		t.Errorf("a CLI that does not tell its version: %+v", c)
	}
}

func TestProjectSettingsAreFoundThroughASymlink(t *testing.T) {
	d := machine(t, "", "")
	link := filepath.Join(t.TempDir(), "webapp")
	if err := os.Symlink(webapp(d), link); err != nil {
		t.Skip(err)
	}
	if p := Collect(context.Background(), link); p.Dir != link || !p.Project.Trusted || p.Project.CodexTrust != "trusted" {
		t.Errorf("the CLIs key a directory by its resolved path: %+v", p.Project)
	}
}

func TestGitLeavesCredentialsOutAndCountsUnpushed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "trunk")
	git("remote", "add", "origin", "https://someone:"+fixture.Secret+"@git.example.com/acme/app.git")
	git("commit", "-q", "--allow-empty", "-m", "one")
	git("commit", "-q", "--allow-empty", "-m", "two")
	for i := range 35 {
		os.WriteFile(filepath.Join(dir, "f"+strings.Repeat("x", i%3)+string(rune('a'+i%26))+".txt"), []byte("x"), 0o644)
	}
	g, err := GitOf(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if g.Remote != "https://git.example.com/acme/app.git" || strings.Contains(g.Remote, fixture.Secret) || g.RemoteKey != "git.example.com/acme/app" {
		t.Errorf("remote %q, key %q", g.Remote, g.RemoteKey)
	}
	if !g.Repo || g.Branch != "trunk" || g.Unpushed != 2 || g.Dirty < 26 || len(g.DirtyList) > 30 {
		t.Errorf("git %+v", g)
	}
	if sub := filepath.Join(dir, "sub"); os.Mkdir(sub, 0o755) == nil {
		if g, _ := GitOf(context.Background(), sub); !g.Repo {
			t.Error("a directory inside a checkout is in it")
		}
	}
	if g, err := GitOf(context.Background(), t.TempDir()); err != nil || g.Repo {
		t.Errorf("outside a checkout: %+v %v", g, err)
	}
	for in, want := range map[string]string{"git@git.example.com:acme/app.git": "git@git.example.com:acme/app.git",
		"ssh://git:" + fixture.Secret + "@git.example.com/acme/app.git": "ssh://git.example.com/acme/app.git",
		"https://" + fixture.Secret + "@git.example.com/x":              "https://git.example.com/x"} {
		if got := withoutCredentials(in); got != want {
			t.Errorf("withoutCredentials(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFileTextServesListedInstructionFilesOnly(t *testing.T) {
	d := machine(t, "", "")
	dir := webapp(d)
	if text, err := FileText(KindDir, "CLAUDE.md", dir); err != nil || !strings.Contains(text, "Run npm test") {
		t.Fatalf("a listed file: %q %v", text, err)
	}
	if text, err := FileText(KindDir, "docs/oauth.md", dir); err != nil || text != "# OAuth\n" {
		t.Fatalf("a listed import: %q %v", text, err)
	}
	if _, err := FileText(KindClaude, "CLAUDE.md", ""); err != nil {
		t.Fatalf("the global one: %v", err)
	}
	for _, f := range [][2]string{{KindDir, ".env"}, {KindDir, "../webapp/.env"}, {KindClaude, ".credentials.json"}, {KindCodex, "auth.json"},
		{KindPath, filepath.Join(dir, ".env")}, {KindDir, "src/auth/callback.ts"}} {
		if text, err := FileText(f[0], f[1], dir); err == nil || strings.Contains(text, fixture.Secret) {
			t.Errorf("%v served: %q", f, text)
		}
	}
}

func TestSeenNamesTheFilesASessionLoadedAsCollectDoes(t *testing.T) {
	d := machine(t, "", "")
	dir := webapp(d)
	e := &index.Env{Version: "2.1.280", Files: []index.EnvFile{{Path: filepath.Join(d.Claude, "CLAUDE.md"), Type: "User", Norm: "n1"},
		{Path: filepath.Join(dir, "docs", "oauth.md"), Type: "Project", Norm: "n2"}, {Path: filepath.Join(d.Root, "elsewhere.md"), Norm: "n3"}},
		Skills: []string{"review"}, Known: []string{"files", "skills"}}
	s := SeenOf("claude", e, dir)
	want := []File{{Kind: KindClaude, Name: "CLAUDE.md", Norm: "n1"}, {Kind: KindDir, Name: "docs/oauth.md", Norm: "n2"},
		{Kind: KindPath, Name: filepath.Join(d.Root, "elsewhere.md"), Norm: "n3"}}
	if s.CLI != "claude" || s.Version != "2.1.280" || !slices.Equal(s.Files, want) || !slices.Equal(s.Known, e.Known) {
		t.Errorf("seen %+v", s)
	}
	if s := SeenOf("codex", nil, dir); s == nil || s.CLI != "codex" || len(s.Known) != 0 {
		t.Errorf("a session that recorded nothing still names its CLI: %+v", s)
	}
}
