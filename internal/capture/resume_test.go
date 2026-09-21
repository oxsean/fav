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
