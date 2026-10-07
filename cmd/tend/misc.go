package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/memory"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/skills"
)

// FZF_PREVIEW_COLUMNS → COLUMNS → 80
func termWidth() int {
	for _, k := range []string{"FZF_PREVIEW_COLUMNS", "COLUMNS"} {
		if n, err := strconv.Atoi(os.Getenv(k)); err == nil && n > 20 {
			return n
		}
	}
	return 80
}

func cmdDoctor(args []string) error {
	fs := newFlags("doctor")
	compact := fs.Bool("compact", false, i18n.T("cli.doctor.flag_compact"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := tend.Open()
	if err != nil {
		return err
	}

	fmt.Print(i18n.F("cli.doctor.data_file", s.Path))
	fmt.Print(i18n.F("cli.doctor.records", len(s.All()), s.RawLines()))

	var dead []*tend.Rec
	alive, pinned, backfilled := 0, 0, 0
	for _, r := range s.All() {
		if !slices.ContainsFunc(r.Transcripts(), paths.Exists) {
			dead = append(dead, r)
			continue
		}
		alive++
		if r.PinnedPath != "" {
			pinned++
		}
		// old records lack the session start time; fill it in while the file still exists
		if r.SessionStartedAt == nil {
			for _, p := range r.Transcripts() {
				if t, ok := capture.SessionStart(p); ok {
					r.SessionStartedAt = &t
					if err := s.Put(r); err == nil {
						backfilled++
					}
					break
				}
			}
		}
	}
	fmt.Print(i18n.F("cli.doctor.transcripts", alive, pinned, len(dead)))
	if backfilled > 0 {
		fmt.Print(i18n.F("cli.doctor.backfilled", backfilled))
	}

	if idx, err := index.Open(); err == nil {
		idx = refreshed(idx)
		live := capture.LiveSessions()
		if list := filterBroken(scanBroken(s, idx, live, ""), brokenQuery(nil)); len(list) > 0 {
			fmt.Println(i18n.T("cli.doctor.broken"))
			printBroken(list)
			fmt.Print(i18n.T("cli.doctor.suggest_fix"))
		}
		if hookInstalled() {
			fmt.Print(i18n.T("cli.doctor.hook_on"))
		} else {
			fmt.Print(i18n.T("cli.doctor.hook_off"))
		}
		printIdleAgents(s, idx, live)
		printBigStale(s, idx, live)
		printMemoryScan(s, idx)
	}

	doctorCoordinator()
	if used, runs := node.New(tend.Home()).BlobUse(); runs > 0 {
		perRun, perNode := node.BlobCaps()
		fmt.Print(i18n.F("cli.doctor.run_blobs", float64(used)/(1<<20), runs, perNode>>20, perRun>>20))
	}

	if days := loadConfig().TrashDays; days > 0 {
		if n, err := tend.PurgeTrash(days); err != nil {
			fmt.Print(i18n.F("cli.doctor.purge_failed", err))
		} else if n > 0 {
			fmt.Print(i18n.F("cli.doctor.purged", n))
		}
	}

	if *compact {
		before := s.RawLines()
		if err := s.Compact(); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.doctor.compacted", before, s.RawLines()))
	} else if s.NeedsCompact() {
		fmt.Println(i18n.T("cli.doctor.suggest_compact"))
	}
	return nil
}

// printMemoryScan reports this machine's Claude memory directories: how many, the orphans with what to do about them,
// and the MEMORY.md files Claude does not load whole.
func printMemoryScan(s *tend.Store, idx *index.Index) {
	var origins []memory.Origin
	for _, x := range append(idx.Sessions(), idx.AgentSessions()...) {
		origins = append(origins, memory.Origin{Dir: x.Cwd, Remote: x.GitRemote()})
		if x.Repo != "" {
			origins = append(origins, memory.Origin{Dir: x.Repo, Remote: x.GitRemote()})
		}
	}
	for _, r := range s.All() {
		origins = append(origins, memory.Origin{Dir: r.Cwd, Remote: r.GitRemote})
	}
	rep := memory.Scan(origins, repoCheckouts())
	fmt.Print(i18n.F("cli.doctor.memory", rep.Dirs, rep.Empty))
	by := map[string][]memory.Orphan{}
	for _, o := range rep.Orphans {
		by[o.Class] = append(by[o.Class], o)
	}
	if len(rep.Orphans) > 0 {
		fmt.Print(i18n.F("cli.doctor.memory_orphans", len(by[memory.OrphanTemp]), len(by[memory.OrphanMoved]), len(by[memory.OrphanUnknown])))
	}
	sh := shell.User()
	for _, class := range []string{memory.OrphanTemp, memory.OrphanMoved, memory.OrphanUnknown} {
		for i, o := range by[class] {
			if i == 5 {
				fmt.Print(i18n.F("cli.doctor.memory_more", len(by[class])-5))
				break
			}
			switch {
			case class == memory.OrphanTemp:
				fmt.Print(i18n.F("cli.doctor.memory_temp", o.From, o.Items))
				fmt.Printf("      %s\n", sh.Join([]string{"tend", "memory", "rm", o.Dir}))
			case class == memory.OrphanMoved:
				fmt.Print(i18n.F("cli.doctor.memory_moved", o.From, o.Target, o.Items))
				fmt.Printf("      %s\n", sh.Join([]string{"tend", "memory", "merge", o.Dir, o.Target}))
			case o.From != "":
				fmt.Print(i18n.F("cli.doctor.memory_unknown", o.From, o.Items))
				fmt.Printf("      %s\n", o.Dir)
			default:
				fmt.Print(i18n.F("cli.doctor.memory_unknown_bare", o.Dir, o.Items))
			}
		}
	}
	for _, p := range rep.Over {
		fmt.Print(i18n.F("cli.doctor.memory_over", p))
	}
}

// repoCheckouts lists this machine's checkouts of a git remote as node.repos finds them, once per repository.
func repoCheckouts() func(string) []string {
	n := node.New(tend.Home())
	n.Limits = loadConfig().Node
	found := map[string][]string{}
	return func(url string) []string {
		key := task.RemoteKey(url)
		if key == "" {
			return nil
		}
		if dirs, ok := found[key]; ok {
			return dirs
		}
		res, _ := n.Repos(context.Background(), remote.ReposParams{Remote: url})
		var dirs []string
		for _, d := range res.Dirs {
			dirs = append(dirs, d.Path)
		}
		found[key] = dirs
		return dirs
	}
}

// Claude and Codex both read skills/<name>/SKILL.md: one source symlinked to both.
func cmdInstallSkill(args []string) error {
	fs := newFlags("install-skill")
	src := fs.String("from", "", i18n.T("cli.skill.flag_from"))
	if err := fs.Parse(args); err != nil {
		return err
	}

	dir := *src
	if dir == "" {
		var err error
		if dir, err = defaultSkillDir(); err != nil { // not running from the repo (release binary / go install): use the embedded copy
			dir = filepath.Join(tend.Home(), "skills", "tend")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), skills.Tend, 0o644); err != nil {
				return err
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		return i18n.E("cli.skill.not_found", dir, err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for _, dst := range skillLinks() {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if fi, err := os.Lstat(dst); err == nil {
			if fi.Mode()&os.ModeSymlink == 0 {
				return i18n.E("cli.skill.not_symlink", dst)
			}
			os.Remove(dst)
		}
		if err := os.Symlink(abs, dst); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.skill.installed", dst, abs))
	}
	return nil
}

// Only removes symlinks tend made; a real directory belongs to someone else.
func cmdUninstallSkill(args []string) error {
	if err := newFlags("uninstall-skill").Parse(args); err != nil {
		return err
	}
	for _, dst := range skillLinks() {
		fi, err := os.Lstat(dst)
		if err != nil {
			fmt.Print(i18n.F("cli.skill.not_installed", dst))
			continue
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			fmt.Print(i18n.F("cli.skill.left_alone", dst))
			continue
		}
		if err := os.Remove(dst); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.skill.removed", dst))
	}
	return nil
}

func skillLinks() []string {
	return []string{filepath.Join(capture.ClaudeHome(), "skills", "tend"), filepath.Join(capture.CodexHome(), "skills", "tend")}
}

func defaultSkillDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// two layouts: installed <prefix>/bin/tend and in-repo <repo>/tend
	for _, cand := range []string{
		filepath.Join(filepath.Dir(exe), "..", "skills", "tend"),
		filepath.Join(filepath.Dir(exe), "skills", "tend"),
	} {
		if _, err := os.Stat(filepath.Join(cand, "SKILL.md")); err == nil {
			return cand, nil
		}
	}
	if wd, err := os.Getwd(); err == nil {
		cand := filepath.Join(wd, "skills", "tend")
		if _, err := os.Stat(filepath.Join(cand, "SKILL.md")); err == nil {
			return cand, nil
		}
	}
	return "", errors.New(i18n.T("cli.skill.dir_missing"))
}

// First %s is the UI, second the key: zsh bindkey notation (^G, \eg), bash readline notation (\C-g, \eg).
var shellInit = map[string]string{
	"zsh": `tend-widget() {
  zle -I
  tend %s < /dev/tty
  zle reset-prompt
}
zle -N tend-widget
bindkey '%s' tend-widget
`,
	"bash": `bind -x '"%[2]s": tend %[1]s < /dev/tty'
`,
}

func cmdShellInit(args []string) error {
	fs := newFlags("shell-init")
	key := fs.String("key", "ctrl-g", i18n.T("cli.shell_init.flag_key"))
	ui := fs.String("ui", "fzf", i18n.T("cli.shell_init.flag_ui"))
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	shell := first(pos)
	if shell == "" {
		shell = filepath.Base(os.Getenv("SHELL"))
	}
	code, ok := shellInit[shell]
	if !ok {
		return i18n.E("cli.shell_init.unknown", shell)
	}
	seq, ok := keySeq(*key, shell)
	if !ok {
		return i18n.E("cli.shell_init.bad_key", *key)
	}
	if *ui != "fzf" && *ui != "tui" {
		return i18n.E("cli.unknown_ui", *ui)
	}
	fmt.Printf(code, *ui, seq)
	return nil
}

func keySeq(key, shell string) (string, bool) {
	k := strings.ToLower(key)
	mod, ch, ok := strings.Cut(k, "-")
	if !ok || mod != "ctrl" && mod != "alt" {
		return key, key != ""
	}
	if len(ch) != 1 || ch[0] < 'a' || ch[0] > 'z' {
		return "", false
	}
	switch mod + "/" + shell {
	case "ctrl/zsh":
		return "^" + strings.ToUpper(ch), true
	case "ctrl/bash":
		return `\C-` + ch, true
	case "alt/zsh", "alt/bash":
		return `\e` + ch, true
	}
	return "", false
}
