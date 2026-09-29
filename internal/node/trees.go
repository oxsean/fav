package node

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// What a run in git keeps of its workspace's trees.
const (
	treesFile = "trees.json"
	baseIndex = "base.index" // the index the base tree is written from: a copy of the user's
	liveIndex = "live.index" // the index a running run's snapshots are written from
	endIndex  = "end.index"
	refPrefix = "refs/tend/runs/" // <run>/base and <run>/end keep the trees from gc
)

// trees is where a run's changes are counted from: its repository and the trees of its workspace when it started
// and ended. Git false: the directory is not in git, the changes are what the agent's tools did (touched.jsonl).
type trees struct {
	Git    bool   `json:"git"`
	GitDir string `json:"git_dir,omitempty"` // the repository's common directory: objects and refs
	Prefix string `json:"prefix,omitempty"`  // the run's directory in the repository, "" at its top, else ending in /
	Base   string `json:"base,omitempty"`
	End    string `json:"end,omitempty"`
}

// takeTrees is the repository dir is in.
func takeTrees(dir string) (trees, error) {
	g := gitIn{dir: dir}
	common, err := g.run("rev-parse", "--git-common-dir")
	if err != nil {
		return trees{}, err
	}
	prefix, err := g.run("rev-parse", "--show-prefix")
	if err != nil {
		return trees{}, err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	return trees{Git: true, GitDir: filepath.Clean(common), Prefix: prefix}, nil
}

// takeTree writes the tree of dir's workspace as it is, untracked files in and ignored ones out, through index: a
// file of the run's, seeded from seed ("" the user's index) so that unchanged files are not read again. ⚠️ The
// user's index and working tree are only read.
func takeTree(dir, index, seed string) (string, error) {
	if _, err := os.Stat(index); err != nil {
		if seed == "" {
			p, err := gitIn{dir: dir}.run("rev-parse", "--git-path", "index")
			if err != nil {
				return "", err
			}
			if !filepath.IsAbs(p) {
				p = filepath.Join(dir, p)
			}
			seed = p
		}
		if b, err := os.ReadFile(seed); err == nil {
			if err := os.WriteFile(index, b, 0o600); err != nil {
				return "", err
			}
		}
	}
	g := gitIn{dir: dir, env: []string{"GIT_INDEX_FILE=" + index, "GIT_OPTIONAL_LOCKS=0"}}
	if _, err := g.run("add", "-A", "--", "."); err != nil {
		return "", err
	}
	return g.run("write-tree")
}

// repo runs git on the run's repository alone: its workspace may be gone.
func (tr trees) repo() gitIn { return gitIn{dir: tr.GitDir, env: []string{"GIT_DIR=" + tr.GitDir}} }

// ref keeps tree from gc as refs/tend/runs/<run>/<which>.
func (tr trees) ref(run, which, tree string) error {
	_, err := tr.repo().run("update-ref", refPrefix+run+"/"+which, tree)
	return err
}

// unref lets the run's trees go.
func (tr trees) unref(run string) {
	if tr.GitDir == "" {
		return
	}
	for _, which := range []string{"base", "end"} {
		tr.repo().run("update-ref", "-d", refPrefix+run+"/"+which)
	}
}

// gitInput runs git in g.dir with stdin.
func gitInput(g gitIn, stdin string, args ...string) (string, error) {
	c := exec.Command("git", append([]string{"-C", g.dir}, args...)...)
	c.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"), g.env...)
	c.Stdin = strings.NewReader(stdin)
	var out, errs bytes.Buffer
	c.Stdout, c.Stderr = &out, &errs
	if err := c.Run(); err != nil {
		return "", errors.New("git " + args[0] + ": " + clip(strings.TrimSpace(errs.String()), 500))
	}
	return out.String(), nil
}
