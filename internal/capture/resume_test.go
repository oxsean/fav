package capture

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/fav"
)

func TestShellLineQuotesTitle(t *testing.T) {
	r := &fav.Rec{Provider: fav.ProviderClaude, SessionID: "abc-123", Title: "notes-api 搜索分页游标漂移排障 it's \"quoted\"", Label: "geo 排障 it's \"quoted\""}
	spec, err := BuildResume(r)
	if err != nil {
		t.Fatal(err)
	}
	got := spec.ShellLine()
	want := `claude --resume abc-123 --name 'geo 排障 it'\''s "quoted"'`
	if got != want {
		t.Errorf("\n得到 %s\n应为 %s", got, want)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell to split the line back")
	}
	out, err := exec.Command("sh", "-c", `for a in `+got[len("claude "):]+`; do printf '%s\n' "$a"; done`).Output()
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n"); !slices.Equal(lines, spec.Args) {
		t.Errorf("shell 拆回来 %q，原 argv %q", lines, spec.Args)
	}
}

func TestShellLineChangesDirectoryFirst(t *testing.T) {
	spec := CommandSpec{Exec: "codex", Args: []string{"resume", "abc"}, Cwd: "/tmp/it's here"}
	want := `cd '/tmp/it'\''s here' && codex resume abc`
	if got := spec.ShellLine(); got != want {
		t.Errorf("\ngot  %s\nwant %s", got, want)
	}
}

func TestPlanResumeBlocksASecondWriter(t *testing.T) {
	r := &fav.Rec{Provider: fav.ProviderClaude, SessionID: "s1", Cwd: t.TempDir()}
	p, _ := PlanResume(r, map[string]Live{"s1": {Status: "idle"}}, true)
	blocked := false
	for _, c := range p.Checks {
		blocked = blocked || !c.OK && !c.Warn && (strings.Contains(c.Text, "another terminal") || strings.Contains(c.Text, "别的终端"))
	}
	if !blocked {
		t.Fatalf("running elsewhere blocks resuming here: %+v", p.Checks)
	}
	p, _ = PlanResume(r, map[string]Live{"s1": {BackgroundID: "b1"}}, true)
	for _, c := range p.Checks {
		if strings.Contains(c.Text, "another terminal") || strings.Contains(c.Text, "别的终端") {
			t.Fatal("a background session is attached, not resumed twice")
		}
	}
}
