package node

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/wire"
)

// FeatureWorktree: run.start's workspace is understood: a task's branch in its own worktree, a read-only copy for a
// review, a merge into an integration branch, and the project's setup and cleanup hooks.
const FeatureWorktree = "worktree"

// Workspace is where a run works in git (agent.Workspace).
type Workspace = agent.Workspace

// Run reasons of a workspace.
const (
	ReasonWork          = "work"           // the worktree could not be made (Detail says why)
	ReasonSetupFailed   = "setup_failed"   // the setup hook failed; the new worktree was removed
	ReasonMergeConflict = "merge_conflict" // the merge stopped on conflicts and was undone
)

var (
	taskBranch = regexp.MustCompile(`^tend/[A-Za-z0-9_.-]{1,80}$`)
	baseRef    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]{0,199}$`)
)

// check says what is wrong with w.
func checkWork(w *Workspace) error {
	bad := func(what string) error { return &wire.Error{Code: wire.CodeBadRequest, Detail: "workspace " + what} }
	switch {
	case w.Checkout == "" || !filepath.IsAbs(w.Checkout):
		return bad("checkout")
	case !taskBranch.MatchString(w.Branch):
		return bad("branch")
	case w.Base != "" && (!baseRef.MatchString(w.Base) || strings.Contains(w.Base, "..")):
		return bad("base")
	case strings.HasPrefix(w.Remote, "-") || strings.ContainsAny(w.Remote, " \t\r\n"):
		return bad("remote")
	case w.Merge != "" && (!taskBranch.MatchString(w.Merge) || w.ReadOnly):
		return bad("merge")
	}
	for _, b := range w.Chain {
		if !taskBranch.MatchString(b) {
			return bad("chain")
		}
	}
	return nil
}

func workRoot(w *Workspace) string { return filepath.Clean(w.Checkout) + "-wt" }

// dirOf is where branch's worktree goes.
func dirOf(w *Workspace, branch string) string {
	return filepath.Join(workRoot(w), strings.TrimPrefix(branch, "tend/"))
}

// dir is where run works: its task's worktree, a read-only copy of its own, or the worktree a merge goes into.
func workDir(w *Workspace, run string) string {
	if w.ReadOnly {
		return filepath.Join(workRoot(w), ".ro", run)
	}
	return dirOf(w, w.Branch)
}

func workHooks(w *Workspace) bool { return len(w.Setup) > 0 || len(w.Cleanup) > 0 }

// gitIn runs git in dir; env adds to its environment.
type gitIn struct {
	dir string
	env []string
}

func (g gitIn) run(args ...string) (string, error) {
	c := exec.Command("git", append([]string{"-C", g.dir}, args...)...)
	c.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_EDITOR=true", "GIT_MERGE_AUTOEDIT=no"), g.env...)
	var out, errs bytes.Buffer
	c.Stdout, c.Stderr = &out, &errs
	if err := c.Run(); err != nil {
		said := strings.TrimSpace(errs.String() + "\n" + out.String())
		if said == "" {
			said = err.Error()
		}
		return strings.TrimSpace(out.String()), fmt.Errorf("git %s: %s", args[0], clip(said, 500))
	}
	return strings.TrimSpace(out.String()), nil
}

// samePlace: a and b are the same directory, symbolic links followed.
func samePlace(a, b string) bool {
	ra, errA := realPath(a)
	rb, errB := realPath(b)
	return errA == nil && errB == nil && paths.Same(ra, rb)
}

func (g gitIn) has(ref string) bool {
	_, err := g.run("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

func (g gitIn) ancestor(a, b string) bool {
	_, err := g.run("merge-base", "--is-ancestor", a, b)
	return err == nil
}

// far is where a branch fetched from the remote is kept.
func far(branch string) string { return "refs/tend-remote/" + branch }

// fetch brings branch from remote to far(branch); a branch the remote does not have is no error.
func (g gitIn) fetch(remote, branch string) {
	g.run("fetch", "--quiet", "--no-tags", remote, "+refs/heads/"+branch+":"+far(branch))
}

// worktreeOf is the worktree that has branch checked out; "" when none.
func (g gitIn) worktreeOf(branch string) string {
	out, _ := g.run("worktree", "list", "--porcelain")
	path := ""
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		} else if line == "branch refs/heads/"+branch {
			return filepath.FromSlash(path)
		}
	}
	return ""
}

// dirty is how many files differ from the head where g works, untracked ones included.
func (g gitIn) dirty() (int, error) {
	out, err := g.run("status", "--porcelain")
	if err != nil || out == "" {
		return 0, err
	}
	return len(strings.Split(out, "\n")), nil
}

// ensure makes branch exist here: from the remote's when it has one, else from from. A branch behind the remote's is
// fast-forwarded; one that went another way is kept, with a warning.
func (g gitIn) ensure(branch, from, remote string) (warnings []string, err error) {
	local := "refs/heads/" + branch
	if remote != "" {
		g.fetch(remote, branch)
	}
	remoteHas := remote != "" && g.has(far(branch))
	switch {
	case !g.has(local) && remoteHas:
		_, err = g.run("branch", branch, far(branch))
	case !g.has(local):
		_, err = g.run("branch", branch, from)
	case remoteHas && !g.ancestor(far(branch), local):
		if !g.ancestor(local, far(branch)) {
			return []string{branch + " went another way than the remote's; this machine's is kept"}, nil
		}
		if wt := g.worktreeOf(branch); wt != "" {
			if _, err := (gitIn{dir: wt}).run("merge", "--ff-only", "--quiet", far(branch)); err != nil {
				return []string{branch + " is behind the remote's and could not catch up: " + err.Error()}, nil
			}
		} else {
			_, err = g.run("update-ref", local, far(branch))
		}
	}
	return nil, err
}

// start is the commit the first branch of a chain is made from: the remote's base, else this machine's.
func (g gitIn) start(base, remote string) (string, error) {
	if base == "" {
		return "HEAD", nil
	}
	if remote != "" {
		g.fetch(remote, base)
		if g.has(far(base)) {
			return far(base), nil
		}
	}
	if g.has("refs/heads/" + base) {
		return "refs/heads/" + base, nil
	}
	if g.has(base) {
		return base, nil
	}
	return "", errors.New("no branch " + base)
}

// workLock serializes git's bookkeeping in one checkout among this node's runs.
func workLock(runDir, checkout string) (func(), error) {
	sum := sha256.Sum256([]byte(filepath.Clean(checkout)))
	dir := filepath.Join(filepath.Dir(filepath.Dir(runDir)), "work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return filelock.Lock(filepath.Join(dir, hex.EncodeToString(sum[:8])+".lock"))
}

// prepare makes the run's worktree: the branches it starts from, then its task's worktree (running the setup hook when
// it is new) or a read-only copy of its branch's head. It answers the commit the task's branch forks from.
func (s *sup) prepare() (from string, err error) {
	w := s.spec.Work
	unlock, err := workLock(s.dir, w.Checkout)
	if err != nil {
		return "", err
	}
	defer unlock()
	g := gitIn{dir: w.Checkout}
	if _, err := g.run("rev-parse", "--git-dir"); err != nil {
		return "", errors.New(w.Checkout + " is not a git repository")
	}
	g.run("worktree", "prune")
	s.sweepCopies(g)
	if from, err = g.start(w.Base, w.Remote); err != nil {
		return "", err
	}
	for _, b := range w.Chain {
		warns, err := g.ensure(b, from, w.Remote)
		if err != nil {
			return "", err
		}
		s.warn(warns...)
		from = b
	}
	warns, err := g.ensure(w.Branch, from, w.Remote)
	if err != nil {
		return "", err
	}
	s.warn(warns...)
	dir := s.spec.Dir
	if w.ReadOnly {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return "", err
		}
		if _, err := g.run("worktree", "add", "--quiet", "--detach", dir, "refs/heads/"+w.Branch); err != nil {
			return "", err
		}
		head, _ := (gitIn{dir: dir}).run("rev-parse", "HEAD")
		s.keep(func(st *State) { st.Work = &agent.Work{Dir: dir, Branch: w.Branch, Head: head} })
		return from, nil
	}
	if wt := g.worktreeOf(w.Branch); wt != "" {
		if !samePlace(wt, dir) {
			return "", errors.New(w.Branch + " is checked out in " + wt)
		}
		return from, nil
	}
	if paths.Exists(dir) {
		return "", errors.New(dir + " is in the way")
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", err
	}
	if _, err := g.run("worktree", "add", "--quiet", dir, w.Branch); err != nil {
		return "", err
	}
	if len(w.Setup) > 0 {
		if exit, tail := s.hook(w.Setup, dir, "setup"); exit != 0 {
			g.run("worktree", "remove", "--force", dir)
			return "", &hookFailed{tail}
		}
	}
	return from, nil
}

// hookFailed: the setup hook failed with this tail of its output.
type hookFailed struct{ tail string }

func (h *hookFailed) Error() string { return "setup hook failed:\n" + h.tail }

// sweepCopies removes the read-only copies of runs that ended without removing theirs.
func (s *sup) sweepCopies(g gitIn) {
	ents, _ := os.ReadDir(filepath.Join(workRoot(s.spec.Work), ".ro"))
	for _, e := range ents {
		if e.Name() == s.spec.Run {
			continue
		}
		var st State
		if err := readState(filepath.Join(filepath.Dir(s.dir), e.Name()), &st); err == nil && !Terminal(st.State) {
			continue
		}
		g.run("worktree", "remove", "--force", filepath.Join(workRoot(s.spec.Work), ".ro", e.Name()))
	}
}

func (s *sup) warn(warnings ...string) {
	if len(warnings) == 0 {
		return
	}
	s.keep(func(st *State) {
		if st.Work == nil {
			st.Work = &agent.Work{}
		}
		st.Work.Warnings = append(st.Work.Warnings, warnings...)
	})
}

// identity is the git identity a commit tend makes needs where the repository has none configured.
func identity(g gitIn, env []string) []string {
	if name, _ := g.run("config", "user.email"); name != "" {
		return env
	}
	return append([]string{"GIT_AUTHOR_NAME=tend", "GIT_AUTHOR_EMAIL=tend@localhost", "GIT_COMMITTER_NAME=tend",
		"GIT_COMMITTER_EMAIL=tend@localhost"}, env...)
}

// commitLeft commits what the agent changed and left uncommitted in its worktree.
func (s *sup) commitLeft() {
	g := gitIn{dir: s.spec.Dir}
	n, err := g.dirty()
	if err != nil || n == 0 {
		return
	}
	g.env = identity(g, s.spec.env())
	if _, err := g.run("add", "-A"); err != nil {
		s.warn(fmt.Sprintf("%d files left uncommitted: %v", n, err))
		return
	}
	msg := fmt.Sprintf("%s\n\ntend run %s", firstLine(s.spec.Title, "tend: task "+s.spec.Task), s.spec.Run)
	if _, err := g.run("commit", "--quiet", "-m", msg); err != nil {
		s.warn(fmt.Sprintf("%d files left uncommitted: %v", n, err))
		return
	}
	s.warn(fmt.Sprintf("committed %d files the agent left uncommitted", n))
}

func firstLine(s, or string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if s == "" {
		return or
	}
	return s
}

// settle records what the run did to its branch: its head, commits and diffstat since from, pushed when there is a
// remote; a read-only copy is counted and removed instead.
func (s *sup) settle(from string) {
	w := s.spec.Work
	g := gitIn{dir: s.spec.Dir}
	if w.ReadOnly {
		n, _ := g.dirty()
		unlock, err := workLock(s.dir, w.Checkout)
		if err == nil {
			_, err = (gitIn{dir: w.Checkout}).run("worktree", "remove", "--force", s.spec.Dir)
			unlock()
		}
		s.keep(func(st *State) {
			if st.Work == nil {
				st.Work = &agent.Work{Branch: w.Branch}
			}
			st.Work.Discarded = n
			if err != nil {
				st.Work.Warnings = append(st.Work.Warnings, "the read-only copy stays: "+err.Error())
			}
		})
		return
	}
	res := agent.Work{Dir: s.spec.Dir, Branch: w.Branch}
	res.Head, _ = g.run("rev-parse", "HEAD")
	if fork, err := g.run("merge-base", "HEAD", from); err == nil {
		if n, err := g.run("rev-list", "--count", fork+"..HEAD"); err == nil {
			res.Commits, _ = strconv.Atoi(n)
		}
		res.Diffstat, _ = g.run("diff", "--shortstat", fork, "HEAD")
	}
	if w.Remote != "" {
		if _, err := g.run("push", "--quiet", w.Remote, "refs/heads/"+w.Branch+":refs/heads/"+w.Branch); err != nil {
			res.Warnings = append(res.Warnings, "not pushed: "+err.Error())
		}
	}
	s.keep(func(st *State) {
		if st.Work != nil {
			res.Warnings, res.PR = append(st.Work.Warnings, res.Warnings...), st.Work.PR
		}
		st.Work = &res
	})
}

// merge is a merge run: branch Merge goes into Branch in Branch's worktree. Conflicts undo it and end the run with
// merge_conflict and the files; a merged branch's worktree is cleaned up and removed.
func (s *sup) merge() error {
	w := s.spec.Work
	now := s.startedNow()
	if _, err := s.prepare(); err != nil {
		return s.workFailed(err)
	}
	g := gitIn{dir: s.spec.Dir}
	if n, err := g.dirty(); err != nil || n > 0 {
		return s.workFailed(fmt.Errorf("%s has %d uncommitted files", s.spec.Dir, n))
	}
	source := gitIn{dir: w.Checkout}
	if w.Remote != "" {
		source.fetch(w.Remote, w.Merge)
	}
	ref := "refs/heads/" + w.Merge
	if w.Remote != "" && source.has(far(w.Merge)) && (!source.has(ref) || source.ancestor(ref, far(w.Merge))) {
		ref = far(w.Merge)
	}
	if !source.has(ref) {
		return s.workFailed(errors.New("no branch " + w.Merge))
	}
	res := agent.Work{Dir: s.spec.Dir, Branch: w.Branch, Merged: true}
	if !g.ancestor(ref, "HEAD") {
		g.env = identity(g, s.spec.env())
		msg := fmt.Sprintf("Merge %s: %s", w.Merge, firstLine(s.spec.Title, w.Merge))
		if _, err := g.run("merge", "--no-ff", "--no-edit", "-m", msg, ref); err != nil {
			files, _ := g.run("diff", "--name-only", "--diff-filter=U")
			g.run("merge", "--abort")
			res.Merged, res.Conflict = false, strings.Fields(files)
			if len(res.Conflict) == 0 {
				return s.workFailed(err)
			}
		}
	}
	res.Head, _ = g.run("rev-parse", "HEAD")
	code := 0
	if !res.Merged {
		code = 1
	} else {
		if w.Remote != "" {
			if _, err := g.run("push", "--quiet", w.Remote, "refs/heads/"+w.Branch+":refs/heads/"+w.Branch); err != nil {
				res.Warnings = append(res.Warnings, "not pushed: "+err.Error())
			}
		}
		res.Warnings = append(res.Warnings, s.removeMerged()...)
	}
	end := time1()
	return s.keep(func(st *State) {
		if st.Work != nil {
			res.Warnings = append(st.Work.Warnings, res.Warnings...)
		}
		st.Work, st.State, st.ExitCode, st.StartedAt, st.EndedAt = &res, StateExited, &code, now, end
		if code != 0 {
			st.Reason, st.Detail = ReasonMergeConflict, clip(strings.Join(res.Conflict, ", "), 300)
		}
	})
}

// removeMerged runs the cleanup hook in the merged branch's worktree and removes it; the branch stays.
func (s *sup) removeMerged() []string {
	w := s.spec.Work
	dir := dirOf(w, w.Merge)
	g := gitIn{dir: w.Checkout}
	if wt := g.worktreeOf(w.Merge); wt == "" || !samePlace(wt, dir) {
		return nil
	}
	var warns []string
	if len(w.Cleanup) > 0 {
		if exit, _ := s.hook(w.Cleanup, dir, "cleanup"); exit != 0 {
			warns = append(warns, fmt.Sprintf("cleanup hook exited %d", exit))
		}
	}
	unlock, err := workLock(s.dir, w.Checkout)
	if err == nil {
		_, err = g.run("worktree", "remove", "--force", dir)
		unlock()
	}
	if err != nil {
		warns = append(warns, "the merged worktree stays: "+err.Error())
	}
	return warns
}

// workFailed ends the run before its agent started: its worktree could not be made.
func (s *sup) workFailed(err error) error {
	reason, detail := ReasonWork, err.Error()
	var hf *hookFailed
	if errors.As(err, &hf) {
		reason, detail = ReasonSetupFailed, hf.tail
	}
	now := time1()
	return s.keep(func(st *State) {
		st.State, st.Reason, st.Detail, st.EndedAt = StateFailed, reason, clip(detail, 1500), now
	})
}

// workConvention tells an agent working on its task's branch how to leave its work.
const workConvention = `
- You work in your own git worktree on branch %[1]s. Commit your changes to it before you stop; do not switch
  branches, rebase, or push: tend merges and pushes the branch.
`

// copyConvention tells a reviewing agent that its directory is a copy.
const copyConvention = `
- This directory is a read-only copy of branch %[1]s at its latest commit. Whatever you change here is thrown away.
`
