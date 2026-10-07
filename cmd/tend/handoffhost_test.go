package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/tend"
)

// twoHomes are two fixture machines with launchers that run this test binary as tend in each: this process is self,
// and its config reaches both as hosts self and peer.
func twoHomes(t *testing.T) (self, peer *fixture.Dataset, launcher func(*fixture.Dataset) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	launcher = func(d *fixture.Dataset) string {
		if runtime.GOOS == "windows" {
			return filepath.Join(d.Root, "tend.cmd")
		}
		return filepath.Join(d.Root, "tend.sh")
	}
	var err error
	for _, d := range []**fixture.Dataset{&self, &peer} {
		name := "self"
		if d == &peer {
			name = "peer"
		}
		if *d, err = fixture.Build(filepath.Join(root, name), time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := (*d).WriteLaunchers(os.Args[0]); err != nil {
			t.Fatal(err)
		}
		linkStubs(t, *d)
	}
	t.Setenv(asTend, "1")
	t.Setenv("TEND_HOME", self.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", self.Claude)
	t.Setenv("CODEX_HOME", self.Codex)
	writeConfig(t, self, map[string]any{"lang": "en", "hosts": []map[string]any{
		{"name": "self", "tend": []string{launcher(self)}}, {"name": "peer", "tend": []string{launcher(peer)}}}})
	writeConfig(t, peer, map[string]any{"lang": "en", "node": map[string]any{"allow_dirs": []string{peer.Work}}})
	return self, peer, launcher
}

// linkStubs makes the launchers' claude and codex stubs links to this test binary, which TestMain turns into a failing
// CLI: ⚠️ a freshly written script is checked by macOS the first time it runs, for seconds under load, and the
// environment read runs `<cli> --version` under a timeout.
func linkStubs(t *testing.T, d *fixture.Dataset) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	for _, name := range []string{"claude", "codex"} {
		stub := filepath.Join(d.Root, "stubs", name)
		if err := os.Remove(stub); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(os.Args[0], stub); err != nil {
			if err := os.Symlink(os.Args[0], stub); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func writeConfig(t *testing.T, d *fixture.Dataset, cfg map[string]any) {
	t.Helper()
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(d.Home, "config.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// tendOut runs tend in this process and returns its stdout, the hosts it reached closed again.
func tendOut(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var err error
	out := stdoutOf(t, func() { err = run(args) })
	farHosts().Hosts.Close()
	return out, err
}

// A session here handed to peer: its pack names this machine instead of its transcript, maps the directories, and is
// written in peer's home for `tend handoff --open` there; a session on self reaches peer the same way.
func TestHandoffBetweenTwoHomes(t *testing.T) {
	self, peer, launcher := twoHomes(t)
	defer i18n.Set(i18n.ZH)
	s := self.Get("oauth")
	there := filepath.Join(peer.Work, "webapp")

	_, err := tendOut(t, "handoff", s.ID, "--host", "peer", "--print")
	if err == nil || !strings.Contains(err.Error(), filepath.Join(peer.Work, "webapp-signup")) {
		t.Fatalf("two checkouts of the remote on peer are offered, not guessed: %v", err)
	}
	pack, err := tendOut(t, "handoff", s.ID, "--host", "peer", "--dir", there, "--print")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{i18n.F("handoff.dirs.dir", s.Cwd, there), "## " + i18n.T("handoff.summary")} {
		if !strings.Contains(pack, want) {
			t.Errorf("pack lacks %q:\n%s", want, pack)
		}
	}
	if strings.Contains(pack, s.Path) || strings.Contains(pack, i18n.F("handoff.transcript", "")) {
		t.Errorf("this machine's transcript stays out of a pack for peer:\n%s", pack)
	}

	line, err := tendOut(t, "handoff", s.ID, "--host", "peer", "--dir", there)
	id, ok := strings.CutPrefix(strings.TrimSpace(line), "tend handoff --open ")
	if err != nil || !ok {
		t.Fatalf("put: %q %v", line, err)
	}
	var meta capture.HandoffMeta
	b, err := os.ReadFile(filepath.Join(peer.Home, "handoff", id+".json"))
	if err != nil || json.Unmarshal(b, &meta) != nil || meta.Dir != there || meta.Provider != tend.ProviderClaude || meta.Session != "claude:"+s.ID {
		t.Fatalf("written on peer: %s %v", b, err)
	}
	if text, err := os.ReadFile(filepath.Join(peer.Home, "handoff", id+".md")); err != nil || !strings.Contains(string(text), there) {
		t.Fatalf("the pack on peer: %v", err)
	}
	c := exec.Command(launcher(peer), "handoff", "--open", id, "--dry-run", "--no-herdr")
	out, err := c.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "claude") || !strings.Contains(string(out), filepath.Join(peer.Home, "handoff", id+".md")) {
		t.Fatalf("peer opens it by its id: %v\n%s", err, out)
	}

	far, err := tendOut(t, "handoff", "self:"+s.ID, "--host", "peer", "--dir", there, "--print")
	if err != nil || !strings.Contains(far, i18n.F("handoff.dirs.dir", s.Cwd, there)) || strings.Contains(far, s.Path) {
		t.Fatalf("a session on self, both ends reached as hosts: %v\n%s", err, far)
	}
	if _, err := tendOut(t, "handoff", s.ID, "--print"); err == nil {
		t.Error("--print goes with --host")
	}
}
