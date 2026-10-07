package main

import (
	"encoding/json"
	"errors"
	"flag"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/envcheck"
	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
)

// selfHost is config.hosts' self: this fixture machine answering over a pipe, as `tend rpc` does over ssh.
func selfHost(t *testing.T) {
	h := remote.NewHostsDial([]tend.Host{{Name: "self", SSH: "self"}}, i18n.EN, func(tend.Host) (*remote.Client, error) {
		return remote.Pipe(remote.NewLocal("test")), nil
	})
	was := remoteHosts
	remoteHosts = func() *remote.Hosts { return h }
	t.Cleanup(func() { remoteHosts = was; h.Close() })
}

// runOut runs tend with args and returns what it printed and its error.
func runOut(t *testing.T, args ...string) (string, error) {
	var err error
	out := stdoutOf(t, func() { err = run(args) })
	return out, err
}

func TestEnvPrintsNamesOnly(t *testing.T) {
	d := fixtureMachine(t)
	dir := filepath.Join(d.Work, "webapp")
	out, err := runOut(t, "env", "--dir", dir, "--json")
	var p envcheck.Print
	if err != nil || json.Unmarshal([]byte(out), &p) != nil || p.Dir != dir || !p.Project.Trusted {
		t.Fatalf("env --json is env's answer: %v %.300s", err, out)
	}
	text, err := runOut(t, "env", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range []string{out, text} {
		if strings.Contains(o, fixture.Secret) || strings.Contains(o, fixture.SecretEmail) {
			t.Fatalf("a secret in %s", o)
		}
	}
	if !strings.Contains(text, "codex:rescue") || !strings.Contains(text, "gateway.example.com") {
		t.Errorf("env: %s", text)
	}
	for _, args := range [][]string{{"env", "-h"}, {"env", "diff", "-h"}} {
		help := stderrOf(t, func() { err = run(args) })
		if !errors.Is(err, flag.ErrHelp) || !strings.Contains(help, "tend env diff <") || !strings.Contains(help, "-there") {
			t.Errorf("%v: %v %s", args, err, help)
		}
	}
}

func TestEnvDiffAgainstAHost(t *testing.T) {
	d := fixtureMachine(t)
	selfHost(t)
	oauth := d.Get("oauth")
	out, err := runOut(t, "env", "diff", "self", "--session", oauth.ID, "--json")
	var r envcheck.Report
	if err != nil || json.Unmarshal([]byte(out), &r) != nil || r.Blocked() || r.Unequal != 0 || r.Items == nil {
		t.Fatalf("the same machine differs in nothing that blocks or changes: %v %s", err, out)
	}
	if strings.Contains(out, fixture.Secret) {
		t.Fatalf("a secret in %s", out)
	}
	text, err := runOut(t, "env", "diff", "self", "--session", oauth.ID)
	if err != nil || !strings.HasPrefix(text, r.Summary()) {
		t.Errorf("the summary comes first: %s", text)
	}

	gone := filepath.Join(d.Work, "nowhere")
	text, err = runOut(t, "env", "diff", "self", "--session", oauth.ID, "--there", gone)
	if err == nil || !strings.Contains(text, i18n.F("envcheck.no_dir", gone)) {
		t.Errorf("a block exits non-zero: %v %s", err, text)
	}
	if err := run([]string{"env", "diff", "nohost"}); err == nil {
		t.Error("an unknown host")
	}
}

// Without --there, env diff looks for the directory there as a handoff does: a project's directory on both machines,
// else the checkouts there of the session's remote, one taken, several listed for --there.
func TestEnvDiffFindsTheDirectoryThereAsAHandoffDoes(t *testing.T) {
	self, peer, _ := twoHomes(t)
	defer i18n.Set(i18n.ZH)
	dev := filepath.Join(peer.Root, "dev")
	writeConfig(t, peer, map[string]any{"lang": "en", "node": map[string]any{"allow_dirs": []string{peer.Work, dev}}})
	_, err := tendOut(t, "env", "diff", "peer", "--session", self.Get("oauth").ID)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(peer.Work, "webapp-signup")) || !strings.Contains(err.Error(), "--there") {
		t.Fatalf("two checkouts of the remote on peer are listed: %v", err)
	}
	var out string
	stderr := stderrOf(t, func() {
		out, err = tendOut(t, "env", "diff", "peer", "--session", self.Get("codex-missing-dir").ID, "--json")
	})
	var r envcheck.Report
	if err != nil || json.Unmarshal([]byte(out), &r) != nil || !strings.Contains(stderr, i18n.F("cli.env.target", "peer", filepath.Join(dev, "legacy-app"))) {
		t.Fatalf("the one checkout of its remote there: %v\n%s\n%s", err, out, stderr)
	}
	if _, err := tendOut(t, "env", "diff", "peer", "--dir", filepath.Join(self.Root, "dev")); err == nil || !strings.Contains(err.Error(), "--there") {
		t.Errorf("a directory with no counterpart asks for --there: %v", err)
	}
}
