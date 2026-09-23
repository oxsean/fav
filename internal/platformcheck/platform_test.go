// Package platformcheck fails when code outside the owning package repeats a platform rule: paths are compared and
// shortened in internal/paths, shell command lines are quoted in internal/shell, OS file locks live in internal/filelock.
package platformcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var rules = []struct {
	pattern *regexp.Regexp
	owner   string // the one directory allowed to match
	use     string
}{
	{regexp.MustCompile(`(HasPrefix|CutPrefix|TrimPrefix)\(.*filepath\.Separator`), "internal/paths", "paths.Under / paths.Inside"},
	{regexp.MustCompile(`HasPrefix\(rel, *"\.\."`), "internal/paths", "paths.Inside"},
	{regexp.MustCompile(`"~/"|"~" *\+`), "internal/paths", "paths.Tilde / paths.Expand"},
	{regexp.MustCompile(`'\\''|func shellQuote`), "internal/shell", "shell.Kind.Quote / Join / Line"},
	{regexp.MustCompile(`/dev/null`), "internal/shell", "a fav subcommand or exec without a shell"},
	{regexp.MustCompile(`syscall\.Flock|LockFileEx`), "internal/filelock", "filelock.Lock / TryLock / Held"},
}

func TestPlatformRulesHaveOneHome(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "node_modules" || strings.HasPrefix(n, ".") && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, r := range rules {
				if r.pattern.MatchString(line) && !strings.HasPrefix(rel, r.owner+"/") {
					t.Errorf("%s:%d: use %s (%s owns this rule): %s", rel, i+1, r.use, r.owner, strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
}
