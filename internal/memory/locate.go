package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
)

const indexName = "MEMORY.md"

// ClaudeDir is where Claude keeps the project memory of dir, found as Claude finds it: settings.json's
// autoMemoryDirectory, else memory/ in the project directory of dir's repository (a worktree's main checkout).
func ClaudeDir(dir string) string {
	if d := autoMemoryDirectory(); d != "" {
		return d
	}
	return filepath.Join(index.ClaudeProjectDir(repoRoot(dir)), "memory")
}

func repoRoot(dir string) string {
	dir = filepath.Clean(dir)
	common := capture.GitOut(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if common == "" || filepath.Base(common) != ".git" {
		return dir
	}
	return filepath.Dir(filepath.Clean(common))
}

func autoMemoryDirectory() string {
	b, err := os.ReadFile(filepath.Join(capture.ClaudeHome(), "settings.json"))
	if err != nil {
		return ""
	}
	var s struct {
		Dir string `json:"autoMemoryDirectory"`
	}
	if json.Unmarshal(b, &s) != nil || strings.TrimSpace(s.Dir) == "" {
		return ""
	}
	if d := paths.Expand(strings.TrimSpace(s.Dir)); filepath.IsAbs(d) {
		return filepath.Clean(d)
	}
	return ""
}

func claudeProjects() string { return filepath.Join(capture.ClaudeHome(), "projects") }

func codexRoot() string { return filepath.Join(capture.CodexHome(), "memories") }

func codexGlobalIndex() string { return filepath.Join(codexRoot(), indexName) }

// claudeRoot is the Claude memory directory p is in, "" when none: projects/<name>/memory or autoMemoryDirectory.
func claudeRoot(p string) string {
	if d := autoMemoryDirectory(); d != "" && paths.Under(p, d) {
		return d
	}
	rel, ok := paths.Inside(claudeProjects(), p)
	if !ok {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 2 || parts[1] != "memory" {
		return ""
	}
	return filepath.Join(claudeProjects(), parts[0], "memory")
}

// DirOf is p when it is a Claude memory directory, else the Claude memory directory of the project directory p.
func DirOf(p string) string {
	p = filepath.Clean(p)
	if claudeRoot(p) == p {
		return p
	}
	return ClaudeDir(p)
}
