package main

import (
	"errors"
	"flag"
	"io"
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

func stderrOf(t *testing.T, f func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	f()
	w.Close()
	os.Stderr = old
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestEverySubcommandHasHelp(t *testing.T) {
	root := t.TempDir()
	for _, k := range []string{"FAV_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "HOME", "USERPROFILE"} {
		t.Setenv(k, filepath.Join(root, k))
	}
	for name, want := range map[string]string{
		"tui": "fav tui | fav fzf", "fzf": "fav tui | fav fzf", "add": "fav add", "list": "fav list", "sessions": "fav sessions",
		"grep": "fav grep", "show": "fav show", "preview": "fav preview", "open": "fav open", "edit": "fav edit",
		"status": "fav status", "done": "fav done", "archive": "fav archive|unarchive", "unarchive": "fav archive|unarchive",
		"fav": "fav fav|unfav", "unfav": "fav fav|unfav", "pin": "fav pin|unpin", "unpin": "fav pin|unpin", "rm": "fav rm",
		"trash": "fav trash", "mv": "fav mv", "fix": "fav fix", "clean": "fav clean", "resume": "fav resume",
		"handoff": "fav handoff", "today": "fav today | fav week", "week": "fav today | fav week", "doctor": "fav doctor",
		"install-skill": "fav install-skill", "uninstall-skill": "fav uninstall-skill", "install-hook": "fav install-hook",
		"uninstall-hook": "fav install-hook", "shell-init": "fav shell-init",
	} {
		var err error
		out := stderrOf(t, func() { stdoutOf(t, func() { err = run([]string{name, "-h"}) }) })
		if !errors.Is(err, flag.ErrHelp) {
			t.Errorf("%s -h: %v", name, err)
			continue
		}
		if !strings.Contains(out, want) || name != "grep" && strings.Contains(out, "fav grep") {
			t.Errorf("%s help:\n%s", name, out)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE_CONFIG_DIR", "settings.json")); err == nil {
		t.Error("install-hook -h installed the hook")
	}
}
