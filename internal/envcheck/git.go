package envcheck

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/oxsean/fav/internal/task"
)

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// GitOf is the checkout dir is in: a directory outside any is Git{}; an error is a git that could not tell.
func GitOf(ctx context.Context, dir string) (Git, error) {
	out, err := git(ctx, dir, "status", "--porcelain=v2", "--branch")
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && strings.Contains(string(ee.Stderr), "not a git repository") {
			return Git{}, nil
		}
		return Git{}, err
	}
	g := Git{Repo: true}
	upstream := false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			if oid := strings.TrimPrefix(line, "# branch.oid "); oid != "(initial)" {
				g.Head = oid
			}
		case strings.HasPrefix(line, "# branch.head "):
			if head := strings.TrimPrefix(line, "# branch.head "); head != "(detached)" {
				g.Branch = head
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			upstream = true
		case strings.HasPrefix(line, "# branch.ab "):
			ahead, _, _ := strings.Cut(strings.TrimPrefix(line, "# branch.ab +"), " ")
			g.Unpushed, _ = strconv.Atoi(ahead)
		case strings.HasPrefix(line, "#") || line == "":
		default:
			g.Dirty++
			if len(g.DirtyList) < dirtyCap {
				g.DirtyList = append(g.DirtyList, statusPath(line))
			}
		}
	}
	if !upstream && g.Head != "" {
		if n, err := git(ctx, dir, "rev-list", "--count", "HEAD", "--not", "--remotes"); err == nil {
			g.Unpushed, _ = strconv.Atoi(strings.TrimSpace(n))
		}
	}
	if u, err := git(ctx, dir, "config", "--get", "remote.origin.url"); err == nil {
		g.Remote = withoutCredentials(strings.TrimSpace(u))
		g.RemoteKey = task.RemoteKey(g.Remote)
	}
	return g, nil
}

// statusPath is the path of a porcelain v2 entry: the last field (a rename's is "path<TAB>orig").
func statusPath(line string) string {
	fields := map[byte]int{'1': 9, '2': 10, 'u': 11, '?': 2, '!': 2}[line[0]]
	parts := strings.SplitN(line, " ", max(fields, 1))
	p := parts[len(parts)-1]
	p, _, _ = strings.Cut(p, "\t")
	return p
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	return string(out), err
}

// withoutCredentials is a remote URL without its user information; "" when it cannot be read.
func withoutCredentials(remote string) string {
	if !strings.Contains(remote, "://") {
		if user, rest, ok := strings.Cut(remote, "@"); ok && strings.Contains(user, ":") {
			return rest
		}
		return remote
	}
	u, err := url.Parse(remote)
	if err != nil {
		return ""
	}
	u.User = nil
	return u.String()
}
