package index

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func claudeAttach(sec int, attachment string) string {
	return sprintf(`{"parentUuid":null,"isSidechain":false,"type":"attachment","attachment":%s,"uuid":"u%d","timestamp":"2026-09-10T01:00:%02dZ","cwd":"/Users/me/work/webapp","sessionId":"envs","version":"2.1.280","gitBranch":"main"}`+"\n", attachment, sec, sec)
}

func claudeUse(sec int, name, input string) string {
	return sprintf(`{"type":"assistant","timestamp":"2026-09-10T01:00:%02dZ","version":"2.1.281","message":{"model":"claude-opus-5-5","content":[{"type":"tool_use","id":"t%d","name":%q,"input":%s}]}}`+"\n", sec, sec, name, input)
}

// planted are attachments that carry personal or secret values: none of them is ever read.
var planted = []string{
	`{"type":"session_context","context":{"userEmail":"fake-user@example.invalid","gitStatus":"FAKESECRET-gitstatus"}}`,
	`{"type":"credential_org","organizationUuid":"FAKESECRET-org"}`,
	`{"type":"command_permissions","allowedTools":["Bash(curl -H 'Authorization: FAKESECRET-perm')"]}`,
	`{"type":"queued_command","prompt":"FAKESECRET-queued"}`,
	`{"type":"hook_success","content":"FAKESECRET-hook","stdout":"FAKESECRET-stdout","stderr":"FAKESECRET-stderr"}`,
	`{"type":"hook_additional_context","content":["skill_listing FAKESECRET-hookctx"],"names":["FAKESECRET-names"],"files":[{"path":"FAKESECRET-path"}]}`,
	`{"type":"file","filename":"/Users/me/work/webapp/.env","content":{"type":"text","file":{"content":"FAKESECRET-file"}}}`,
	`{"type":"edited_text_file","filename":"/Users/me/work/webapp/a.go","snippet":"FAKESECRET-edit"}`,
	`{"type":"prompt_snapshot","text":"FAKESECRET-prompt"}`,
	`{"prompt":"FAKESECRET-late","names":["FAKESECRET-latenames"],"model":"x","type":"queued_command"}`, // its type last
}

func norm(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestEnvFoldsAttachmentsAsTheScanReadsOn(t *testing.T) {
	claude, _ := setup(t)
	cache := filepath.Join(t.TempDir(), "sessions.jsonl")
	p := filepath.Join(claude, "projects", "-Users-me-work-webapp", "envs.jsonl")
	first := claudeAttach(1, `{"type":"model","identity":{"modelId":"claude-opus-5-5","marketingName":"Opus 5.5","knowledgeCutoff":"2026-06"},"text":"You are powered by Opus"}`) +
		claudeAttach(2, `{"type":"instructions","files":[{"path":"/Users/me/.claude/CLAUDE.md","type":"User","content":"global rules\r\nline two FAKESECRET-instr"},{"path":"/Users/me/work/webapp/CLAUDE.md","type":"Project","content":"project rules"},{"path":"/Users/me/.claude/projects/x/memory/MEMORY.md","type":"AutoMem","content":"memory"}]}`) +
		claudeAttach(3, `{"type":"skill_listing","content":"- review: FAKESECRET-listing","skillCount":2,"isInitial":true,"names":["review","tend"]}`) +
		claudeAttach(4, `{"type":"deferred_tools_delta","addedNames":["WebSearch","WebFetch"],"addedLines":["WebFetch FAKESECRET-line"],"removedNames":[]}`) +
		claudeAttach(5, `{"type":"mcp_instructions_delta","addedNames":["gitea"],"addedBlocks":["## gitea FAKESECRET-block"],"removedNames":[]}`) +
		claudeAttach(6, `{"type":"agent_listing_delta","addedTypes":["Explore","coder"],"addedLines":["- Explore"],"removedTypes":[],"isInitial":true,"showConcurrencyNote":false}`) +
		claudeAttach(7, `{"type":"environment","snapshot":{"workingDirectory":"/Users/me/work/webapp","isWorktree":false,"isGitRepo":true,"additionalWorkingDirectories":[],"platform":"darwin","shell":"zsh","osVersion":"Darwin 24.6.0","scratchpadDirectory":"/tmp/x"}}`)
	for i, a := range planted {
		first += claudeAttach(10+i, a)
	}
	first += claudeLines("set up the environment check for webapp", "second turn", "third turn") +
		claudeUse(30, "Skill", `{"skill":"review"}`) +
		claudeUse(31, "mcp__gitea__list_issues", `{"token":"FAKESECRET-mcpinput"}`)
	write(t, p, first)

	idx, err := OpenAt(cache)
	if err != nil {
		t.Fatal(err)
	}
	idx, _ = idx.Refresh()
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
	e := idx.Env("claude", "envs")
	if e == nil {
		t.Fatal("no env for a session with attachments")
	}
	if e.Model != "claude-opus-5-5" || e.Version != "2.1.281" || e.Platform != "darwin" || e.Shell != "zsh" || e.Worktree {
		t.Fatalf("plain fields: %+v", e)
	}
	if len(e.Files) != 2 || e.Files[0].Path != "/Users/me/.claude/CLAUDE.md" || e.Files[0].Type != "User" ||
		e.Files[0].Norm != norm("global rules\nline two FAKESECRET-instr") || e.Files[1].Norm != norm("project rules") {
		t.Fatalf("instruction files (memory files left out, CRLF read as LF): %+v", e.Files)
	}
	for name, got := range map[string][]string{"skills": e.Skills, "tools": e.Tools, "mcp": e.MCP, "agents": e.Agents,
		"used": e.Used, "used_mcp": e.UsedMCP, "known": e.Known} {
		want := map[string][]string{"skills": {"review", "tend"}, "tools": {"WebFetch", "WebSearch"}, "mcp": {"gitea"},
			"agents": {"Explore", "coder"}, "used": {"review"}, "used_mcp": {"gitea"},
			"known": {"agents", "files", "mcp", "skills", "tools"}}[name]
		if !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
	b, _ := os.ReadFile(cache)
	for _, secret := range []string{"FAKESECRET", "fake-user@"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("the cache holds %s: %s", secret, b)
		}
	}

	before := idx.files[p].Size
	// The first part rewritten in place, same length: a scan reading on from its offset never sees it.
	write(t, p, strings.Replace(first, `"names":["review","tend"]`, `"names":["zzzzzz","tend"]`, 1)+
		claudeAttach(40, `{"type":"instructions","files":[{"path":"/Users/me/work/webapp/CLAUDE.md","type":"Project","content":"project rules v2"}],"removed":["/Users/me/.claude/CLAUDE.md"],"changed":true,"reason":"compact"}`)+
		claudeAttach(41, `{"type":"skill_listing","content":"- simplify","skillCount":1,"isInitial":false,"names":["simplify"]}`)+
		claudeAttach(42, `{"type":"deferred_tools_delta","addedNames":[],"removedNames":["WebFetch"]}`)+
		claudeAttach(43, `{"type":"mcp_instructions_delta","addedNames":[],"addedBlocks":[],"removedNames":["gitea"]}`)+
		claudeAttach(44, `{"type":"agent_listing_delta","addedTypes":["reviewer"],"removedTypes":["Explore"],"isInitial":false}`)+
		claudeAttach(45, `{"type":"invoked_skills","skills":[{"name":"tend","path":"/Users/me/.claude/skills/tend/SKILL.md","content":"FAKESECRET-skillbody"}]}`)+
		claudeAttach(46, `{"type":"environment","snapshot":{"platform":"darwin","shell":"bash","isWorktree":true},"changes":[{"field":"shell","from":"zsh"}]}`)+
		claudeAttach(47, `{"type":"model","identity":{"modelId":"claude-fable-5-1"},"text":"x"}`)+
		claudeAttach(48, `{"names":["late"],"isInitial":false,"skillCount":1,"type":"skill_listing"}`))
	appendTo(t, p, "")
	idx2, changed := idx.Refresh()
	if !changed || idx2.files[p].Size <= before {
		t.Fatal("the appended lines were not read")
	}
	e = idx2.Env("claude", "envs")
	if e.Model != "claude-fable-5-1" || e.Shell != "bash" || !e.Worktree || e.Version != "2.1.280" {
		t.Fatalf("plain fields after the append: %+v", e)
	}
	if len(e.Files) != 1 || e.Files[0].Norm != norm("project rules v2") {
		t.Fatalf("a changed instructions attachment updates and removes by path: %+v", e.Files)
	}
	if !slices.Equal(e.Skills, []string{"late", "review", "simplify", "tend"}) || !slices.Equal(e.Tools, []string{"WebSearch"}) ||
		len(e.MCP) != 0 || !slices.Equal(e.Agents, []string{"coder", "reviewer"}) || !slices.Equal(e.Used, []string{"review", "tend"}) {
		t.Fatalf("sets after the deltas (read on from the offset, not from the start): %+v", e)
	}
	if old := idx.Env("claude", "envs"); len(old.Files) != 2 || len(old.MCP) != 1 {
		t.Fatalf("the earlier snapshot changed under its readers: %+v", old)
	}
}

func TestEnvOfCodexAndTheNewestRollout(t *testing.T) {
	_, codex := setup(t)
	meta := `{"timestamp":"2026-09-11T02:00:00Z","type":"session_meta","payload":{"session_id":"cxe","id":"cxe","cwd":"/w","originator":"codex_cli_rs","cli_version":"%s","source":"cli","model_provider":"%s"}}` + "\n"
	turn := `{"timestamp":"2026-09-11T02:00:0%dZ","type":"turn_context","payload":{"cwd":"/w","approval_policy":%q,"sandbox_policy":%s,"model":%q}}` + "\n"
	user := `{"timestamp":"2026-09-11T02:00:05Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the build please"}]}}` + "\n"
	write(t, rolloutPath(codex, 11, "cxe"), sprintf(meta, "0.150.0", "openai")+sprintf(turn, 1, "never", `{"mode":"read-only"}`, "gpt-5")+user)
	newer := rolloutPath(codex, 12, "cxe")
	write(t, newer, sprintf(meta, "0.156.1", "azure")+
		sprintf(turn, 1, "on-request", `{"type":"workspace-write","network_access":false}`, "gpt-6")+
		sprintf(turn, 2, "untrusted", `"danger-full-access"`, "gpt-6-astra")+user)
	idx, _ := (&Index{files: map[string]*File{}}).Refresh()
	e := idx.Env("codex", "cxe")
	if e == nil || e.Version != "0.156.1" || e.Provider != "azure" || e.Model != "gpt-6-astra" || e.Approval != "untrusted" ||
		e.Sandbox != "danger-full-access" {
		t.Fatalf("the newest rollout's last values: %+v", e)
	}
	if old := idx.files[rolloutPath(codex, 11, "cxe")].Env; old == nil || old.Sandbox != "read-only" || old.Model != "gpt-5" {
		t.Fatalf("the older rollout: %+v", old)
	}
	if idx.Env("codex", "nope") != nil {
		t.Fatal("an unknown session has no env")
	}
}
