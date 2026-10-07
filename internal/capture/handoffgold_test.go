package capture

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/testkit"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/*.golden")

// packSession is a session with every section of a pack: requests, a reply, changed files and uncommitted work.
func packSession(t *testing.T) *tend.Rec {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	cwd := filepath.Join(dir, "proj")
	os.MkdirAll(cwd, 0o755)
	for _, args := range [][]string{{"-c", "init.defaultBranch=main", "init", "-q"}, {"checkout", "-q", "-b", "feat/pages"}} {
		if out, err := exec.Command("git", append([]string{"-C", cwd}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(cwd, "a.go"), []byte("package a\n"), 0o644)
	path := filepath.Join(dir, "s.jsonl")
	lines := []string{
		`{"type":"user","timestamp":"2026-09-22T10:00:00Z","message":{"role":"user","content":"fix the cursor\nit drifts"}}`,
		`{"type":"assistant","timestamp":"2026-09-22T10:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"## Plan\nchange the sort"},{"type":"tool_use","name":"Edit","input":{"file_path":` + testkit.JSONString(filepath.Join(cwd, "a.go")) + `,"old_string":"x","new_string":"y"}}]}}`,
		`{"type":"user","timestamp":"2026-09-22T10:02:00Z","message":{"role":"user","content":"now the tests"}}`,
		`{"type":"assistant","timestamp":"2026-09-22T10:03:00Z","message":{"role":"assistant","content":[{"type":"text","text":"done; next: run them"}]}}`,
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	return &tend.Rec{Provider: tend.ProviderClaude, SessionID: "abc-123", Title: "Paging", Summary: "the cursor drifts", Cwd: cwd,
		GitBranch: "feat/pages", TranscriptPath: path}
}

// The pack a handoff on this machine writes stays word for word what it was before facts and rendering were split.
func TestLocalPackIsUnchanged(t *testing.T) {
	r := packSession(t)
	local, was := time.Local, i18n.ZH
	if i18n.T("handoff.dirs") == i18n.In(i18n.EN, "handoff.dirs") {
		was = i18n.EN
	}
	time.Local = time.UTC
	defer func() { time.Local = local; i18n.Set(was) }()
	for _, lang := range []string{i18n.EN, i18n.ZH} {
		i18n.Set(lang)
		got := localPack(r)
		got = strings.ReplaceAll(got, filepath.Dir(r.Cwd)+string(filepath.Separator), "<dir>/")
		golden := filepath.Join("testdata", "handoff_local_"+lang+".golden")
		if *updateGolden {
			os.MkdirAll("testdata", 0o755)
			os.WriteFile(golden, []byte(got), 0o644)
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("%s pack changed:\n%s\n--- want ---\n%s", lang, got, want)
		}
	}
}

func localPack(r *tend.Rec) string { return RenderHandoff(HandoffFactsOf(r), HandoffTarget{}) }

// Read on another machine, the pack names the session's machine instead of its transcript, and says how the
// directories correspond; the facts cross the wire as they are.
func TestRemotePackNamesTheMachineNotTheTranscript(t *testing.T) {
	r := packSession(t)
	f := HandoffFactsOf(r)
	if f.Git.Status == "" || len(f.Requests) != 2 || f.Reply == "" || len(f.Files) != 1 {
		t.Fatalf("facts: %+v", f)
	}
	b, _ := json.Marshal(f)
	var back HandoffFacts
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if again, _ := json.Marshal(back); !bytes.Equal(again, b) { // ⚠️ not DeepEqual: a decoded time carries another Location
		t.Fatalf("facts round trip:\n%s\n%s", b, again)
	}
	to := HandoffTarget{Source: "studio", SourceHome: "/home/dev", Dir: `D:\work\proj`, Home: `C:\Users\dev`}
	pack := RenderHandoff(back, to)
	if strings.Contains(pack, r.TranscriptPath) {
		t.Errorf("the source's transcript path stays out:\n%s", pack)
	}
	for _, want := range []string{
		i18n.F("handoff.elsewhere", "studio"), "## " + i18n.T("handoff.dirs"), i18n.F("handoff.dirs.dir", r.Cwd, to.Dir),
		i18n.F("handoff.dirs.home", to.SourceHome, to.Home), "now the tests", "> done; next: run them", "- a.go", "?? a.go",
	} {
		if !strings.Contains(pack, want) {
			t.Errorf("pack lacks %q:\n%s", want, pack)
		}
	}
	if local := RenderHandoff(back, HandoffTarget{}); !strings.Contains(local, r.TranscriptPath) || strings.Contains(local, i18n.T("handoff.dirs")) {
		t.Errorf("on its own machine the pack points at the transcript and maps no directories:\n%s", local)
	}
}
