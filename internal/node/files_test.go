package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

func TestADefinitionsHooksServersAndSkillsReachClaude(t *testing.T) {
	n := New(t.TempDir())
	n.Limits.MCP = map[string]json.RawMessage{"docs": json.RawMessage(`{"command":"docs-mcp"}`)}
	hooks := map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "make test"}}}}}
	p := StartParams{Run: NewRunID(), Task: "t", Coordinator: "c", Dir: t.TempDir(), Runner: RunnerBackground,
		Profile: agent.Profile{Name: "careful", Provider: tend.ProviderClaude, Hooks: hooks, MCP: []string{"docs"}, Skills: []string{"review"}}}
	code := func(err error) string { return wire.Code(err) }
	if err := n.admit(&p); code(err) != wire.CodeUnauthorized {
		t.Fatalf("hooks run commands: they need node.allow_hooks: %v", err)
	}
	n.Limits.AllowHooks = true
	if err := n.admit(&p); code(err) != wire.CodeNotFound {
		t.Fatalf("a skill must be installed: %v", err)
	}
	skill := filepath.Join(capture.ClaudeHome(), "skills", "review")
	os.MkdirAll(skill, 0o700)
	os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: review\n---\n"), 0o600)
	if err := n.admit(&p); err != nil {
		t.Fatal(err)
	}
	dir := n.runDir(p.Run)
	spec, err := n.spec(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	i, j := slices.Index(spec.Argv, "--settings"), slices.Index(spec.Argv, "--mcp-config")
	if i < 0 || spec.Argv[i+1] != filepath.Join(dir, settingsFile) || j < 0 || spec.Argv[j+1] != filepath.Join(dir, mcpFile) {
		t.Fatalf("claude takes the run's files: %q", spec.Argv)
	}
	files, _ := n.agentFiles(p.Profile)
	var mcp struct {
		MCPServers map[string]map[string]string `json:"mcpServers"`
	}
	json.Unmarshal(files[mcpFile], &mcp)
	if mcp.MCPServers["docs"]["command"] != "docs-mcp" || len(files[settingsFile]) == 0 {
		t.Fatalf("%s %s", files[mcpFile], files[settingsFile])
	}

	for _, bad := range []agent.Profile{
		{Name: "x", Provider: tend.ProviderClaude, MCP: []string{"secrets"}},
		{Name: "x", Provider: tend.ProviderCodex, Hooks: hooks},
		{Name: "x", Provider: tend.ProviderClaude, Skills: []string{"../etc"}},
		{Name: "x", Provider: tend.ProviderClaude, Args: []string{"--settings", "/tmp/mine.json"}},
	} {
		q := p
		q.Profile = bad
		if err := n.admit(&q); err == nil {
			if _, err := n.spec(q, dir); err == nil {
				t.Errorf("accepted %+v", bad)
			}
		}
	}
}
