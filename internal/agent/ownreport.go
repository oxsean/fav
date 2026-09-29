package agent

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/shell"
)

var heredocHead = regexp.MustCompile(`^(.*\S)\s*<<\s*(?:'(\w+)'|"(\w+)")\s*$`)

// OwnReport reports whether an agent asks to run nothing but its own tend run note|ask|verdict|plan, as the brief
// spells it with self, the node's tend: those only write to the run's directory, which a sandbox may not let it
// write, so no one is asked. With self empty any program named tend counts, for reading a log elsewhere.
// ⚠️ anything else in the command (another command, an expansion, a redirection) and it is the user's to allow.
func OwnReport(command, self string) bool {
	words, ok := shell.POSIX.Split(command)
	if ok && len(words) == 3 && slices.Contains([]string{"sh", "bash", "zsh"}, filepath.Base(words[0])) &&
		(words[1] == "-c" || words[1] == "-lc") {
		command = words[2]
		words, ok = shell.POSIX.Split(command)
	}
	if !ok {
		head, body, found := strings.Cut(command, "\n")
		if !found {
			return false
		}
		m := heredocHead.FindStringSubmatch(head)
		if m == nil {
			return false
		}
		tag := m[2] + m[3]
		lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
		if len(lines) < 2 || lines[len(lines)-1] != tag || slices.Contains(lines[:len(lines)-1], tag) {
			return false
		}
		words, ok = shell.POSIX.Split(m[1])
		return ok && ownTend(words, self) && len(words) == 4 && words[2] == "plan" && words[3] == "-"
	}
	return ownTend(words, self) && slices.Contains([]string{"note", "ask", "verdict", "plan"}, words[2])
}

func ownTend(words []string, self string) bool {
	if len(words) < 3 || words[1] != "run" {
		return false
	}
	if self == "" {
		return strings.TrimSuffix(strings.ToLower(pathmap.Base(words[0])), ".exe") == "tend"
	}
	return filepath.Clean(words[0]) == filepath.Clean(self)
}
