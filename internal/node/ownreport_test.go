package node

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/shell"
)

func TestOnlyARunsOwnReportsGoWithoutApproval(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	q := shell.POSIX.Quote(self)
	zsh := func(script string) string { return "/bin/zsh -lc " + shell.POSIX.Quote(script) }
	for cmd, want := range map[string]bool{
		q + " run verdict pass 'looks good'":                      true,
		zsh(q + " run verdict pass 'looks good'"):                 true,
		"/bin/bash -c " + shell.POSIX.Quote(q+` run note "half"`): true,
		q + " run ask 'which db?'":                                true,
		q + " run note --pr https://x.example/pr/1":               true,
		q + " run plan /tmp/plan.json":                            true,
		q + " run plan - <<'PLAN'\n{\"tasks\":[]}\nPLAN":          true,
		zsh(q + " run plan - <<'PLAN'\n{\"tasks\":[]}\nPLAN\n"):   true,
		zsh(q + " run plan - <<\"EOF\"\n{\"a\":\"$HOME\"}\nEOF"):  true,
		q + " run plan - <<'PLAN'\n{}\n  PLAN":                    false,
		q + " run note x; rm -rf ~":                               false,
		q + " run note $(id)":                                     false,
		q + " run continue r_1 hi":                                false,
		q + " sessions":                                           false,
		q + " run":                                                false,
		"tend run note x":                                         self == "tend",
		zsh("cd /; " + q + " run note x"):                         false,
		zsh(q+" run note a") + " extra":                           false,
		q + " run plan - <<PLAN\n{}\nPLAN":                        false,
		q + " run plan - <<'PLAN'\n{}\nPLAN\nrm -rf ~":            false,
		q + " run plan - <<'PLAN' && rm x\n{}\nPLAN":              false,
		q + " run plan - <<'PLAN'\n{}\n":                          false,
		q + " run note <<'PLAN'\nx\nPLAN":                         false,
		q + " run plan x - <<'PLAN'\n{}\nPLAN":                    false,
	} {
		if got := ownReport(cmd); got != want {
			t.Errorf("ownReport(%q) = %v", cmd, got)
		}
	}
}

// A codex run in a sandbox asks before it runs tend run verdict: the supervisor allows its own reports itself, and a
// workspace-write thread may write the run's directory.
func TestACodexRunReportsWithoutWaitingForAnyone(t *testing.T) {
	g := newCodexRig(t, "")
	g.s.proto.begin("b")
	g.sent(t)
	g.feed(`{"id":1,"result":{}}`)
	g.sent(t)
	g.sent(t)
	g.feed(`{"id":2,"result":{"thread":{"id":"th-1"},"sandbox":{"type":"workspaceWrite","writableRoots":["/cache"],"networkAccess":true}}}`)
	m := g.sent(t)
	policy, _ := params(m)["sandboxPolicy"].(map[string]any)
	if m["method"] != "turn/start" || policy["type"] != "workspaceWrite" || policy["networkAccess"] != true ||
		mustJSON(policy["writableRoots"]) != mustJSON([]string{"/cache", g.s.dir}) {
		t.Fatalf("%v", m)
	}
	self, _ := os.Executable()
	cmd, _ := json.Marshal("/bin/zsh -lc " + shell.POSIX.Quote(shell.POSIX.Quote(self)+" run verdict pass 'fine'"))
	g.feed(`{"id":7,"method":"item/commandExecution/requestApproval","params":{"command":` + string(cmd) + `,"itemId":"x"}}`)
	if m := g.sent(t); m["id"] != float64(7) || mustJSON(m["result"]) != `{"decision":"accept"}` {
		t.Fatalf("%v", m)
	}
	if len(g.s.st.Requests) != 0 || g.s.st.Attention != "" {
		t.Fatalf("nobody is asked: %+v", g.s.st)
	}
}

func TestAReadOnlyCodexThreadKeepsItsSandbox(t *testing.T) {
	g := newCodexRig(t, "")
	g.s.proto.begin("b")
	g.sent(t)
	g.feed(`{"id":1,"result":{}}`)
	g.sent(t)
	g.sent(t)
	g.feed(`{"id":2,"result":{"thread":{"id":"th-1"},"sandbox":{"type":"readOnly"}}}`)
	if m := g.sent(t); m["method"] != "turn/start" || params(m)["sandboxPolicy"] != nil {
		t.Fatalf("%v", m)
	}
}

func TestAClaudeRunReportsWithoutWaitingForAnyone(t *testing.T) {
	self, _ := os.Executable()
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "2", "--every", "20ms", "--permission", "Bash:"+shell.POSIX.Quote(self)+" run note half"), Brief: "b"})
	end := wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if end.State.State != StateExited || !strings.Contains(logOf(t, n, s.Run), "allowed Bash") {
		t.Fatalf("%+v\n%s", end, logOf(t, n, s.Run))
	}
}
