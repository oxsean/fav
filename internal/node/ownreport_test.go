package node

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/shell"
)

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
