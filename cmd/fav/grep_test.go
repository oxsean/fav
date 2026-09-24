package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQueryDashes(t *testing.T) {
	fs := flag.NewFlagSet("grep", flag.ContinueOnError)
	fs.Bool("json", false, "")
	fs.Int("limit", 0, "")
	for _, c := range []struct{ args, flags, words string }{
		{"--limit 3 flyway -baseline --json -x=y", "--limit 3 flyway --json", "-baseline -x=y"},
		{"-h", "-h", ""},
		{"--help", "--help", ""},
		{"-help", "-help", ""},
	} {
		flags, words := queryDashes(fs, strings.Fields(c.args))
		if strings.Join(flags, " ") != c.flags || strings.Join(words, " ") != c.words {
			t.Errorf("%s: flags %q words %q", c.args, flags, words)
		}
	}
}

func stderrOf(t *testing.T, f func()) string { return captured(t, &os.Stderr, f) }

func TestEverySubcommandHasHelp(t *testing.T) {
	root := t.TempDir()
	for _, k := range []string{"FAV_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "HOME", "USERPROFILE"} {
		t.Setenv(k, filepath.Join(root, k))
	}
	for name, want := range map[string]string{
		"tui": "tend tui | tend fzf", "fzf": "tend tui | tend fzf", "add": "tend add", "list": "tend list", "sessions": "tend sessions",
		"grep": "tend grep", "show": "tend show", "preview": "tend preview", "open": "tend open", "edit": "tend edit",
		"status": "tend status", "done": "tend done", "archive": "tend archive|unarchive", "unarchive": "tend archive|unarchive",
		"fav": "tend fav|unfav", "unfav": "tend fav|unfav", "pin": "tend pin|unpin", "unpin": "tend pin|unpin", "rm": "tend rm",
		"trash": "tend trash", "mv": "tend mv", "fix": "tend fix", "clean": "tend clean", "resume": "tend resume",
		"handoff": "tend handoff", "today": "tend today | tend week", "week": "tend today | tend week", "doctor": "tend doctor",
		"install-skill": "tend install-skill", "uninstall-skill": "tend uninstall-skill", "install-hook": "tend install-hook",
		"uninstall-hook": "tend install-hook", "shell-init": "tend shell-init",
	} {
		var err error
		out := stderrOf(t, func() { stdoutOf(t, func() { err = run([]string{name, "-h"}) }) })
		if !errors.Is(err, flag.ErrHelp) {
			t.Errorf("%s -h: %v", name, err)
			continue
		}
		if !strings.Contains(out, want) || name != "grep" && strings.Contains(out, "tend grep") {
			t.Errorf("%s help:\n%s", name, out)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE_CONFIG_DIR", "settings.json")); err == nil {
		t.Error("install-hook -h installed the hook")
	}
}
