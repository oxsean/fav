package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Files a run directory holds for its agent (feature files).
const (
	settingsFile = "settings.json" // claude --settings: the definition's hooks
	mcpFile      = "mcp.json"      // claude --mcp-config: the node's servers the definition names
)

var skillName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// admitFiles: what profile p asks of this machine is here: claude for hooks and MCP servers, each server in
// node.mcp, each skill installed.
func (n *Node) admitFiles(p agent.Profile) error {
	if len(p.Hooks) == 0 && len(p.MCP) == 0 && len(p.Skills) == 0 {
		return nil
	}
	if p.Provider != tend.ProviderClaude && (len(p.Hooks) > 0 || len(p.MCP) > 0) {
		return &wire.Error{Code: wire.CodeBadRequest, Detail: "hooks and mcp: claude only"}
	}
	for _, name := range p.MCP {
		if _, ok := n.Limits.MCP[name]; !ok {
			return &wire.Error{Code: wire.CodeNotFound, Detail: "mcp " + name + " (node.mcp)"}
		}
	}
	for _, s := range p.Skills {
		if !skillName.MatchString(s) {
			return &wire.Error{Code: wire.CodeBadRequest, Detail: "skill " + s}
		}
		if p.Provider == tend.ProviderClaude {
			if _, err := os.Stat(filepath.Join(capture.ClaudeHome(), "skills", s, "SKILL.md")); err != nil {
				return &wire.Error{Code: wire.CodeNotFound, Detail: "skill " + s}
			}
		}
	}
	return nil
}

// agentFiles are the files profile p's run directory holds for its agent, by name.
func (n *Node) agentFiles(p agent.Profile) (map[string][]byte, error) {
	out := map[string][]byte{}
	if len(p.Hooks) > 0 {
		b, err := json.Marshal(map[string]any{"hooks": p.Hooks})
		if err != nil {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "hooks"}
		}
		out[settingsFile] = b
	}
	if len(p.MCP) > 0 {
		servers := map[string]json.RawMessage{}
		for _, name := range p.MCP {
			servers[name] = n.Limits.MCP[name]
		}
		b, _ := json.Marshal(map[string]any{"mcpServers": servers})
		out[mcpFile] = b
	}
	return out, nil
}
