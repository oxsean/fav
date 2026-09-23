package main

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestQueryDashesKeepsExclusionsOutOfTheFlags(t *testing.T) {
	fs := flag.NewFlagSet("grep", flag.ContinueOnError)
	fs.Bool("json", false, "")
	fs.Int("limit", 0, "")
	flags, words := queryDashes(fs, []string{"--limit", "3", "flyway", "-baseline", "--json", "-x=y"})
	if strings.Join(flags, " ") != "--limit 3 flyway --json" || strings.Join(words, " ") != "-baseline -x=y" {
		t.Fatalf("flags %q words %q", flags, words)
	}
}

func TestQueryDashesPassesHelp(t *testing.T) {
	fs := flag.NewFlagSet("grep", flag.ContinueOnError)
	for _, a := range []string{"-h", "--help", "-help"} {
		if flags, words := queryDashes(fs, []string{a}); len(flags) != 1 || len(words) != 0 {
			t.Errorf("%s: flags %q words %q", a, flags, words)
		}
	}
}

func TestSubcommandHelpShowsOnlyItsOwnLines(t *testing.T) {
	for name, want := range map[string]string{"mv": "fav mv ", "archive": "fav archive|unarchive", "fzf": "fav tui | fav fzf", "week": "fav today | fav week"} {
		fs := newFlags(name)
		fs.Bool("y", false, "")
		var out strings.Builder
		fs.SetOutput(&out)
		if err := fs.Parse([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("%s: %v", name, err)
		}
		if got := out.String(); !strings.Contains(got, want) || strings.Contains(got, "fav grep") || !strings.Contains(got, "-y") {
			t.Errorf("%s help:\n%s", name, got)
		}
	}
}
