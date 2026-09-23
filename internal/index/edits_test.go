package index

import (
	"path/filepath"
	"testing"

	"github.com/oxsean/fav/internal/testkit"
)

func TestEditedFilesIndexed(t *testing.T) {
	testkit.PosixOnly(t)
	claude, codex := setup(t)
	edit := `{"type":"assistant","timestamp":"2026-09-10T01:00:05Z","cwd":"/Users/me/work/webapp","message":{"content":[{"type":"text","text":"改一下"},{"type":"tool_use","name":"Edit","input":{"file_path":"/Users/me/work/webapp/a.go","old_string":"x","new_string":"y"}},{"type":"tool_use","name":"Bash","input":{"command":"go test"}}]}}` + "\n"
	write2 := `{"type":"assistant","timestamp":"2026-09-10T01:00:06Z","cwd":"/Users/me/work/webapp","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"/Users/me/work/webapp/a.go","content":"z"}},{"type":"tool_use","name":"NotebookEdit","input":{"notebook_path":"/Users/me/work/webapp/n.ipynb"}},{"type":"tool_use","name":"Write","input":{"file_path":"/private/tmp/claude-501/x/scratchpad/notes.md"}}]}}` + "\n"
	write(t, filepath.Join(claude, "projects", "-w", "c1.jsonl"), claudeLines("把分页修好，顺便补测试")+edit+write2)

	meta := `{"timestamp":"2026-09-11T02:00:00Z","type":"session_meta","payload":{"session_id":"x1","cwd":"/Users/me/work/env","originator":"codex-tui"}}` + "\n"
	msg := `{"timestamp":"2026-09-11T02:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"把 env 仓库的 CI 修好"}]}}` + "\n"
	patch := `{"timestamp":"2026-09-11T02:00:02Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n*** Update File: ci/build.yml\n@@\n-a\n+b\n*** Add File: /abs/new.sh\n+echo\n*** End Patch"}}` + "\n"
	write(t, filepath.Join(codex, "sessions", "2026", "09", "11", "rollout-2026-09-11T02-00-00-x1.jsonl"), meta+msg+patch)

	idx, _ := OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	idx, _ = idx.Refresh()
	got := map[string]*Session{}
	for _, s := range idx.Sessions() {
		got[s.SessionID] = s
	}
	c := got["c1"]
	if c == nil || c.Files["/Users/me/work/webapp/a.go"] != 2 || c.Files["/Users/me/work/webapp/n.ipynb"] != 1 || len(c.Files) != 2 || c.Replies != 1 {
		t.Fatalf("Claude Edit / Write / NotebookEdit counted per path, the reply still counted: %+v", c)
	}
	x := got["x1"]
	if x == nil || x.Files["/Users/me/work/env/ci/build.yml"] != 1 || x.Files["/abs/new.sh"] != 1 || len(x.Files) != 2 {
		t.Fatalf("Codex apply_patch paths, relative ones under the cwd: %+v", x)
	}
}
