package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
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
	s, err := fav.Open()
	if err != nil {
		return err
	}

	fmt.Print(i18n.F("cli.doctor.data_file", s.Path))
	fmt.Print(i18n.F("cli.doctor.records", len(s.All()), s.RawLines()))

	var dead []*fav.Rec
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
	}

	if days := loadConfig().TrashDays; days > 0 {
		if n, err := fav.PurgeTrash(days); err != nil {
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
			dir = filepath.Join(fav.Home(), "skills", "fav")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), skills.Fav, 0o644); err != nil {
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

// Only removes symlinks fav made; a real directory belongs to someone else.
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
	return []string{filepath.Join(capture.ClaudeHome(), "skills", "fav"), filepath.Join(capture.CodexHome(), "skills", "fav")}
}

func defaultSkillDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// two layouts: installed <prefix>/bin/fav and in-repo <repo>/fav
	for _, cand := range []string{
		filepath.Join(filepath.Dir(exe), "..", "skills", "fav"),
		filepath.Join(filepath.Dir(exe), "skills", "fav"),
	} {
		if _, err := os.Stat(filepath.Join(cand, "SKILL.md")); err == nil {
			return cand, nil
		}
	}
	if wd, err := os.Getwd(); err == nil {
		cand := filepath.Join(wd, "skills", "fav")
		if _, err := os.Stat(filepath.Join(cand, "SKILL.md")); err == nil {
			return cand, nil
		}
	}
	return "", errors.New(i18n.T("cli.skill.dir_missing"))
}

// First %s is the UI, second the key: zsh bindkey notation (^G, \eg), bash readline notation (\C-g, \eg).
var shellInit = map[string]string{
	"zsh": `fav-widget() {
  zle -I
  fav %s < /dev/tty
  zle reset-prompt
}
zle -N fav-widget
bindkey '%s' fav-widget
`,
	"bash": `bind -x '"%[2]s": fav %[1]s < /dev/tty'
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
