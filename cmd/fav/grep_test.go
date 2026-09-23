package main

import (
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
