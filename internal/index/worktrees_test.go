package index

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/oxsean/fav/internal/fav"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func claudeAt(cwd, sid string) string {
	return sprintf(`{"type":"user","timestamp":"2026-09-10T01:00:00Z","cwd":%q,"gitBranch":"feat/wt","sessionId":%q,"message":{"content":"把分页修好，顺便补测试"}}`, cwd, sid) + "\n"
}

// A session in a linked worktree groups under the main checkout, and still does after the worktree is removed.
func TestWorktreeSessionsBelongToTheirRepo(t *testing.T) {
	claude, _ := setup(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo, wt := filepath.Join(root, "webapp"), filepath.Join(root, "webapp-wt", "feat-wt")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "remote", "add", "origin", "git@example.com:me/webapp.git")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, repo, "worktree", "add", "-q", "-b", "feat/wt", wt)
	write(t, filepath.Join(claude, "projects", "-main", "m1.jsonl"), claudeAt(repo, "m1"))
	write(t, filepath.Join(claude, "projects", "-wt", "w1.jsonl"), claudeAt(wt, "w1"))

	idxPath := filepath.Join(t.TempDir(), "sessions.jsonl")
	idx, _ := OpenAt(idxPath)
	idx, _ = idx.Refresh()
	store, _ := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	byID := func(idx *Index) map[string]*fav.Rec {
		out := map[string]*fav.Rec{}
		for _, r := range idx.Attach(store, nil) {
			out[r.SessionID] = r
		}
		return out
	}
	got := byID(idx)
	if got["w1"].Repo != repo || got["w1"].Project != "webapp" || got["m1"].Repo != "" || got["m1"].Project != "webapp" {
		t.Fatalf("worktree session under its repo: w1=%+v m1=%+v", got["w1"], got["m1"])
	}

	git(t, repo, "worktree", "remove", wt)
	idx, _ = OpenAt(idxPath) // a later run: only worktrees.json remembers
	idx, _ = idx.Refresh()
	if r := byID(idx)["w1"]; r.Repo != repo {
		t.Fatalf("a removed worktree keeps its repo: %+v", r)
	}
	var miss *Missing
	for _, m := range idx.FindMissing(store, "") {
		if m.Dir == wt {
			miss = &m
		}
	}
	if miss == nil || miss.Repo != repo || len(miss.Found) != 1 || miss.Found[0] != repo {
		t.Fatalf("fix moves a removed worktree's sessions to the main checkout: %+v", miss)
	}
}

func TestWorktreeRepoOf(t *testing.T) {
	_, codex := setup(t)
	repo := filepath.Join(t.TempDir(), "kolab")
	os.MkdirAll(repo, 0o755)
	w := worktrees{repo: {Remote: "git@example.com:me/kolab.git"}, "/gone/sub": {Repo: "/main"}}
	cases := []struct{ cwd, wtRepo, remote, want string }{
		{"/x", "/orig", "", "/orig"},
		{"/src/app/.claude/worktrees/fix-1", "", "", "/src/app"},
		{"/gone/sub", "", "", "/main"},
		{filepath.Join(codex, "worktrees", "28e7", "kolab"), "", "git@example.com:me/kolab", repo},
		{filepath.Join(codex, "worktrees", "28e7", "kolab"), "", "git@example.com:other/kolab.git", ""},
		{filepath.Join(codex, "worktrees", "28e7", "other"), "", "git@example.com:me/kolab.git", ""},
		{"/plain", "", "", ""},
	}
	for _, c := range cases {
		if got := w.repoOf(c.cwd, c.wtRepo, c.remote); got != c.want {
			t.Errorf("repoOf(%s, %q, %q) = %q, want %q", c.cwd, c.wtRepo, c.remote, got, c.want)
		}
	}
}

func TestWorktreeStateAndCodexRemoteScanned(t *testing.T) {
	claude, codex := setup(t)
	enter := `{"type":"worktree-state","worktreeSession":{"originalCwd":"/src/app","worktreePath":"/src/app-wt/x"},"sessionId":"c1"}` + "\n"
	leave := `{"type":"worktree-state","worktreeSession":null,"sessionId":"c1"}` + "\n"
	p := filepath.Join(claude, "projects", "-src-app", "c1.jsonl")
	write(t, p, claudeAt("/src/app", "c1")+enter)
	meta := `{"timestamp":"2026-09-11T02:00:00Z","type":"session_meta","payload":{"session_id":"x1","cwd":"/w","originator":"Codex Desktop","git":{"repository_url":"git@example.com:me/w.git"}}}` + "\n"
	write(t, filepath.Join(codex, "sessions", "2026", "09", "11", "rollout-2026-09-11T02-00-00-x1.jsonl"), meta)

	idx, _ := OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	idx, _ = idx.Refresh()
	for _, f := range idx.files {
		switch f.SessionID {
		case "c1":
			if f.WtRepo != "/src/app" {
				t.Errorf("worktree-state names the checkout it came from: %+v", f)
			}
		case "x1":
			if f.Remote != "git@example.com:me/w.git" {
				t.Errorf("Codex session_meta git remote: %+v", f)
			}
		}
	}
	appendTo(t, p, leave)
	idx, _ = idx.Refresh()
	for _, f := range idx.files {
		if f.SessionID == "c1" && f.WtRepo != "" {
			t.Errorf("leaving the worktree clears it: %+v", f)
		}
	}
}
