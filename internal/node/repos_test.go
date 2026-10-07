package node

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/wire"
)

// checkoutOf makes a checkout at dir whose origin is url, on branch.
func checkoutOf(t *testing.T, dir, url, branch string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "--quiet", "--initial-branch="+branch)
	git(t, dir, "remote", "add", "origin", url)
}

// node.repos finds a remote's checkouts under the roots alone, whatever form its URL takes, each once by its top level,
// with its branch and where it was found; Claude's githubRepoPaths come first.
func TestReposFindsTheRemotesCheckoutsUnderTheRoots(t *testing.T) {
	needGit(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	shop, deep, twin := filepath.Join(root, "zz", "shop"), filepath.Join(root, "a", "b", "shop-2"), filepath.Join(outside, "shop")
	checkoutOf(t, shop, "git@github.com:acme/shop.git", "main")
	checkoutOf(t, deep, "https://GitHub.com/acme/shop", "feat/x")
	checkoutOf(t, twin, "https://github.com/acme/shop.git", "main")
	checkoutOf(t, filepath.Join(root, "other"), "https://github.com/acme/other.git", "main")
	checkoutOf(t, filepath.Join(root, "a", "b", "c", "too-deep"), "https://github.com/acme/shop.git", "main")
	os.MkdirAll(filepath.Join(shop, "sub"), 0o755)
	state := `{"oauthAccount":{"emailAddress":"x"},"projects":{"` + filepath.ToSlash(shop) + `":{"allowedTools":[]}},` +
		`"githubRepoPaths":{"Acme/Shop":[` + jsonStrings(filepath.Join(shop, "sub"), twin) + `],"acme/other":[]},"numStartups":3}`
	os.MkdirAll(os.Getenv("CLAUDE_CONFIG_DIR"), 0o755)
	if err := os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	n := New(t.TempDir())
	n.Limits.AllowDirs = []string{root}

	got, err := n.Repos(context.Background(), remote.ReposParams{Remote: "https://github.com/acme/shop"})
	if err != nil {
		t.Fatal(err)
	}
	want := []remote.RepoDir{{Path: shop, Branch: "main", From: "claude"}, {Path: deep, Branch: "feat/x", From: "scan"}}
	if !slices.Equal(got.Dirs, want) {
		t.Fatalf("checkouts:\n%+v\nwant\n%+v", got.Dirs, want)
	}
	if got, err := n.Repos(context.Background(), remote.ReposParams{Remote: "git@github.com:acme/none"}); err != nil || len(got.Dirs) != 0 {
		t.Errorf("a remote with no checkout: %+v %v", got, err)
	}
	if _, err := n.Repos(context.Background(), remote.ReposParams{}); wire.Code(err) != wire.CodeBadRequest {
		t.Errorf("no remote: %v", err)
	}
}

func jsonStrings(ss ...string) string {
	var out string
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += `"` + filepath.ToSlash(s) + `"`
	}
	return out
}

// The Codex sessions that recorded the remote lead the indexed candidates.
func TestReposTakesTheCodexSessionsOfTheRemoteFirst(t *testing.T) {
	needGit(t)
	d, err := fixture.Build(filepath.Join(t.TempDir(), "machine"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEND_HOME", d.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
	t.Setenv("CODEX_HOME", d.Codex)
	idx, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	if idx, _ = idx.Refresh(); idx.Save() != nil {
		t.Fatal("index not saved")
	}
	n := New(t.TempDir())
	n.Limits.AllowDirs = []string{d.Work}
	got, err := n.Repos(context.Background(), remote.ReposParams{Remote: "git@example.com:acme/webapp"})
	if err != nil || len(got.Dirs) == 0 || got.Dirs[0] != (remote.RepoDir{Path: filepath.Join(d.Work, "webapp"), Branch: "main", From: "index-remote"}) {
		t.Fatalf("%+v %v", got.Dirs, err)
	}
}
