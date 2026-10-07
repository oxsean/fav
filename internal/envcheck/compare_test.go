package envcheck

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/pathmap"
)

var mac, linux = pathmap.End{OS: "darwin", Home: "/Users/a", Host: "mac"}, pathmap.End{OS: "linux", Home: "/home/a", Host: "box"}

// items is the report as dim/level/name/evidence lines.
func items(r Report) []string {
	var out []string
	for _, it := range r.Items {
		out = append(out, it.Dim+" "+it.Level+" "+it.Name+" "+it.Evidence)
		if it.What == "" {
			out = append(out, "no text: "+it.Dim+" "+it.Name)
		}
	}
	return out
}

func TestCompareRanksWhatTheSessionWouldMiss(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	src := Print{At: at, Dir: "/Users/a/work/webapp",
		CLIs: []CLI{{Name: "claude", Found: true, Version: "2.1.250"}, {Name: "codex", Found: true, Version: "0.156.1"}},
		Git:  Git{Repo: true, Branch: "main", Head: "aaaa", Dirty: 2, Unpushed: 1},
		Files: []File{{Kind: KindClaude, Name: "CLAUDE.md", SHA: "s1", Norm: "n1"}, {Kind: KindDir, Name: "CLAUDE.md", SHA: "s2", Norm: "n2"},
			{Kind: KindDir, Name: "CLAUDE.local.md", SHA: "s3", Norm: "n3"}},
		Skills:   []string{"review", "tend"},
		MCP:      []string{"docs", "gitea"},
		Project:  Project{AllowedTools: []string{"Bash", "WebFetch"}, Trusted: true},
		Provider: Provider{ClaudeEnv: []string{"A", "B"}, ClaudeHost: "gw.example.com"},
		Seen: &Seen{At: at.Add(-time.Hour), CLI: "claude", Version: "2.1.292", Skills: []string{"review", "tend"}, Used: []string{"review"},
			UsedMCP: []string{"gitea", "claude_ai_Drive"}, Files: []File{{Kind: KindClaude, Name: "CLAUDE.md", Norm: "n1"}},
			Known: []string{"files", "mcp", "skills"}},
	}
	dst := Print{At: at, Dir: "/home/a/work/webapp",
		CLIs: []CLI{{Name: "claude", Found: true, Version: "2.1.200"}},
		Git:  Git{Repo: true, Branch: "dev", Head: "bbbb", Dirty: 1},
		Files: []File{{Kind: KindClaude, Name: "CLAUDE.md", SHA: "s1-crlf", Norm: "n1"}, {Kind: KindDir, Name: "CLAUDE.md", SHA: "s2x", Norm: "n2x"},
			{Kind: KindDir, Name: "AGENTS.md", SHA: "s4", Norm: "n4"}},
		MCP:      []string{"docs"},
		Project:  Project{AllowedTools: []string{"Bash"}},
		Provider: Provider{ClaudeEnv: []string{"A"}, ClaudeHost: "gw.example.com"},
		Unknown:  []string{"codex_config"},
	}
	r := Compare(src, dst, mac, linux)
	want := []string{
		"cli unequal claude session",
		"code unequal branch config",
		"code unequal head config",
		"code unequal dirty config",
		"code unequal unpushed config",
		"code unequal dirty_there config",
		"files unequal dir:CLAUDE.md config",
		"files unequal dir:CLAUDE.local.md config",
		"files unequal dir:AGENTS.md config",
		"skills unequal review session",
		"mcp unequal gitea session",
		"project unequal allowed_tools config",
		"project unequal trusted config",
		"provider unequal claude_env config",
		"files hint claude:CLAUDE.md session",
		"skills hint tend session",
		"mcp hint claude_ai_Drive unknown",
		"unknown hint codex_config unknown",
	}
	if got := items(r); !slices.Equal(got, want) {
		t.Errorf("items:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if r.Block != 0 || r.Unequal != 14 || r.Hint != 4 || r.Blocked() {
		t.Errorf("counts %d %d %d", r.Block, r.Unequal, r.Hint)
	}
	if got, want := r.Summary(), i18n.F("envcheck.summary", 0, 14, 4); got != want {
		t.Errorf("summary %q, want %q", got, want)
	}
	for _, it := range r.Items {
		if it.Dim == "cli" && (!it.At.Equal(src.Seen.At) || it.Here != "2.1.292" || it.There != "2.1.200" || it.Fix == "") {
			t.Errorf("the CLI's evidence is the session's version and time: %+v", it)
		}
	}
}

func TestCompareBlocksOnAMissingCLIOrCheckout(t *testing.T) {
	src := Print{Dir: "/Users/a/w", CLIs: []CLI{{Name: "claude", Found: true, Version: "2.1.292"}}, Git: Git{Repo: true, Branch: "main"},
		Seen: &Seen{CLI: "claude", Version: "2.1.292", Known: []string{"skills"}}}
	for name, dst := range map[string]Print{
		"no CLI":   {Dir: "/home/a/w", CLIs: []CLI{{Name: "claude"}}, Git: Git{Repo: true, Branch: "main"}},
		"no dir":   {Dir: "/home/a/w", NoDir: true, CLIs: []CLI{{Name: "claude", Found: true, Version: "2.1.292"}}},
		"not repo": {Dir: "/home/a/w", CLIs: []CLI{{Name: "claude", Found: true, Version: "2.1.292"}}},
	} {
		r := Compare(src, dst, mac, linux)
		if !r.Blocked() || r.Block != 1 || r.Items[0].Level != LevelBlock || r.Items[0].Fix == "" {
			t.Errorf("%s: %v", name, items(r))
		}
	}
	if r := Compare(src, Print{Dir: "/home/a/w", CLIs: []CLI{{Name: "claude", Found: true, Version: "2.1.292"}}, Git: Git{Repo: true, Branch: "main"}},
		mac, linux); len(r.Items) != 0 {
		t.Errorf("nothing differs: %v", items(r))
	}
}

func TestAMissingDirectoryIsOneBlockNotEachOfItsFiles(t *testing.T) {
	src := Print{Dir: "/Users/a/w", Git: Git{Repo: true}, Files: []File{{Kind: KindDir, Name: "CLAUDE.md", Norm: "n"}, {Kind: KindClaude, Name: "CLAUDE.md", Norm: "g"}},
		Project: Project{Trusted: true, AllowedTools: []string{"Bash"}}}
	dst := Print{Dir: "/home/a/w", NoDir: true}
	if got, want := items(Compare(src, dst, mac, linux)), []string{"code block dir config", "files unequal claude:CLAUDE.md config"}; !slices.Equal(got, want) {
		t.Errorf("items %v, want %v", got, want)
	}
}

func TestTheSameRepositoryInEitherFormIsNoDifference(t *testing.T) {
	src := Print{Dir: "/Users/a/w", Git: Git{Repo: true, Branch: "main", Remote: "git@example.com:acme/shop.git", RemoteKey: "example.com/acme/shop"}}
	for name, c := range map[string]struct {
		remote, key string
		want        []string
	}{
		"https":                {"https://example.com/acme/shop", "example.com/acme/shop", nil},
		"key left out (older)": {"ssh://git@example.com/acme/shop.git", "", nil},
		"another repository":   {"https://example.com/acme/other.git", "example.com/acme/other", []string{"code unequal remote config"}},
		"no origin":            {"", "", []string{"code unequal remote config"}},
	} {
		dst := Print{Dir: "/home/a/w", Git: Git{Repo: true, Branch: "main", Remote: c.remote, RemoteKey: c.key}}
		if got := items(Compare(src, dst, mac, linux)); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
	noOrigin := src
	noOrigin.Git.Remote, noOrigin.Git.RemoteKey = "", ""
	if got := items(Compare(noOrigin, Print{Dir: "/home/a/w", Git: Git{Repo: true, Branch: "main", Remote: "https://example.com/x"}}, mac, linux)); len(got) != 0 {
		t.Errorf("a source without origin has nothing to compare: %v", got)
	}
}

func TestOneCheckoutIsNotComparedWithItself(t *testing.T) {
	src := Print{Dir: "/Users/a/w", Git: Git{Repo: true, Branch: "main", Dirty: 3, Unpushed: 1}}
	if r := Compare(src, src, mac, mac); len(r.Items) != 0 {
		t.Errorf("one directory: %v", items(r))
	}
	other := src
	other.Dir = "/Users/a/w2"
	if r := Compare(src, other, mac, mac); len(r.Items) == 0 {
		t.Error("two checkouts on one machine are compared")
	}
}

func TestWindowsAndWSLOnOneMachineShareTheCode(t *testing.T) {
	win, wsl := pathmap.End{OS: "windows", Home: `C:\Users\a`, Host: "pc"}, pathmap.End{OS: "linux", Home: "/home/a", Host: "pc", WSL: true}
	src := Print{Dir: `C:\work\app`, Git: Git{Repo: true, Branch: "main", Dirty: 3}}
	dst := Print{Dir: "/mnt/c/work/app", Git: Git{Repo: true, Branch: "other"}}
	if r := Compare(src, dst, win, wsl); len(r.Items) != 0 {
		t.Errorf("neighbours: %v", items(r))
	}
	other := wsl
	other.Host = "laptop"
	if r := Compare(src, dst, win, other); len(r.Items) == 0 {
		t.Error("another machine's WSL is compared")
	}
}

func TestASessionThatRecordedNothingIsUnknownNotEqual(t *testing.T) {
	src := Print{CLIs: []CLI{{Name: "claude", Found: true, Version: "2.1.292"}, {Name: "codex", Found: true, Version: "0.156.1"}},
		Skills: []string{"review"}, Seen: &Seen{CLI: "codex"}}
	dst := Print{CLIs: []CLI{{Name: "claude", Found: true, Version: "2.1.292"}, {Name: "codex", Found: true}}}
	want := []string{"session hint codex unknown", "cli hint codex unknown"}
	if got := items(Compare(src, dst, mac, linux)); !slices.Equal(got, want) {
		t.Errorf("items %v, want %v (a Codex session: Claude's skills are not compared)", got, want)
	}
	machineOnly := src
	machineOnly.Seen = nil
	dst.CLIs[1].Version = "0.150.0"
	want = []string{"cli unequal codex config", "skills hint review config"}
	if got := items(Compare(machineOnly, dst, mac, linux)); !slices.Equal(got, want) {
		t.Errorf("machines alone: %v, want %v", got, want)
	}
}

func TestTwoFixtureHomesDifferOnlyWhereTheyWereChanged(t *testing.T) {
	a := machine(t, "2.1.292 (Claude Code)", "codex-cli 0.156.1")
	src := Collect(context.Background(), webapp(a))
	idx, err := index.OpenAt(filepath.Join(a.Home, "sessions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	idx, _ = idx.Refresh()
	src.Seen = SeenOf("claude", idx.Env("claude", a.Get("oauth").ID), webapp(a))

	b := machine(t, "2.1.292 (Claude Code)", "codex-cli 0.156.1")
	global := filepath.Join(b.Claude, "CLAUDE.md")
	text, _ := os.ReadFile(global)
	os.WriteFile(global, []byte(strings.ReplaceAll(string(text), "\n", "\r\n")), 0o600)
	os.RemoveAll(filepath.Join(b.Claude, "skills", "review"))
	os.RemoveAll(filepath.Join(b.Claude, "skills", "tend"))
	os.WriteFile(filepath.Join(webapp(b), "CLAUDE.local.md"), []byte("Use the production API.\n"), 0o600)
	dst := Collect(context.Background(), webapp(b))

	end := pathmap.End{OS: "darwin", Home: a.Root, Host: "here"}
	r := Compare(src, dst, end, end)
	got := items(r)
	for _, want := range []string{"files unequal dir:CLAUDE.local.md config", "skills unequal review session", "skills hint tend session",
		"files hint claude:CLAUDE.md session"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, strings.Join(got, "\n"))
		}
	}
	for _, it := range r.Items {
		if it.Dim == "cli" || it.Dim == "mcp" || it.Dim == "project" || it.Dim == "provider" || it.Name == "dir:CLAUDE.md" {
			t.Errorf("the same on both homes: %+v", it)
		}
	}
}
