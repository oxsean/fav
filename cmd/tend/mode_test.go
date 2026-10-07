package main

import (
	"strings"
	"testing"
)

func TestResumePrintsTheSessionsPermissionMode(t *testing.T) {
	d := machine(t)
	if err := run([]string{"sessions"}); err != nil {
		t.Fatal(err)
	}
	cjk, cx := d.Get("cjk-dir").ID, d.Get("codex-cli").ID // codex-cli is favorited: a store record
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"resume", cjk}, "claude --dangerously-skip-permissions --resume " + cjk},
		{[]string{"resume", "--fork", cjk}, "claude --dangerously-skip-permissions --resume " + cjk + " --fork-session"},
		{[]string{"resume", cx}, "codex resume -a on-request -s workspace-write " + cx},
		{[]string{"resume", "--fork", cx}, "codex fork -a on-request -s workspace-write " + cx},
	} {
		var err error
		out := stdoutOf(t, func() { err = run(append(c.args, "--dry-run", "--no-herdr")) })
		if err != nil || !strings.Contains(out, c.want) {
			t.Errorf("%v: %v\n%s", c.args, err, out)
		}
	}
}
