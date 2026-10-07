// Command test-host runs on a test target from the synced tree (`mise run test-host`): vet, test, then the tend binary
// against a fresh fixture dataset. The last line is `RESULT vet=… test=… smoke=…`, which scripts/test-hosts.sh collects.
package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
)

func main() {
	root := os.Getenv("TEND_TEST_ROOT")
	if root == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		root = filepath.Join(cache, "tend-test")
	}
	fmt.Printf("== %s/%s\n", runtime.GOOS, runtime.GOARCH)
	run(nil, "go", "version")
	for _, t := range []string{"claude", "codex"} {
		if run(nil, t, "--version") != nil {
			fmt.Printf("%s: not on PATH\n", t)
		}
	}
	res := map[string]string{"vet": "ok", "test": "ok", "smoke": "ok"}
	if run(nil, "go", "vet", "./...") != nil {
		res["vet"] = "FAIL"
	}
	if run(nil, "go", "test", "./...") != nil {
		res["test"] = "FAIL"
	}
	if err := smoke(filepath.Join(root, "data")); err != nil {
		fmt.Println("smoke:", err)
		res["smoke"] = "FAIL"
	}
	fmt.Printf("RESULT vet=%s test=%s smoke=%s\n", res["vet"], res["test"], res["smoke"])
	for _, v := range res {
		if v != "ok" {
			os.Exit(1)
		}
	}
}

func smoke(dir string) error {
	peerDir := filepath.Join(filepath.Dir(dir), "peer")
	for _, d := range []string{dir, peerDir} {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
	}
	d, err := fixture.Build(dir, time.Now())
	if err != nil {
		return err
	}
	peer, err := fixture.Build(peerDir, time.Now())
	if err != nil {
		return err
	}
	bin := filepath.Join(dir, "bin", "tend")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if err := run(nil, "go", "build", "-o", bin, "./cmd/tend"); err != nil {
		return err
	}
	for _, d := range []*fixture.Dataset{d, peer} {
		if err := d.WriteLaunchers(bin); err != nil {
			return err
		}
	}
	env := append(os.Environ(), d.Env()...)
	if err := run(env, bin, "sessions"); err != nil {
		return err
	}
	if err := run(env, bin, "doctor"); err != nil {
		return err
	}
	if err := taskSmoke(d, env, bin); err != nil {
		return fmt.Errorf("task: %w", err)
	}
	return hostsSmoke(d, peer, env, bin)
}

// taskSmoke: a task of its own, beside the fixture's, run to its end by the fake agent, this process being the
// coordinator for the command.
func taskSmoke(d *fixture.Dataset, env []string, bin string) error {
	proj := filepath.Join(d.Root, "task-smoke")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		return err
	}
	if err := run(env, bin, "task", "add", "--agent", "fake", "--dir", proj, "smoke"); err != nil {
		return err
	}
	c := exec.Command(bin, "task", "list", "--json")
	c.Env, c.Stderr = env, os.Stderr
	out, err := c.Output()
	if err != nil {
		return err
	}
	var tasks []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Dir   string `json:"dir"`
	}
	if err := json.Unmarshal(out, &tasks); err != nil {
		return fmt.Errorf("task list: %s", out)
	}
	var mine []string
	for _, t := range tasks {
		if t.Title == "smoke" && paths.Same(t.Dir, proj) {
			mine = append(mine, t.ID)
		}
	}
	if len(mine) != 1 {
		return fmt.Errorf("task list: %d smoke tasks in %s", len(mine), out)
	}
	return run(env, bin, "run", "start", "--runner", "background", "--wait", mine[0])
}

// hostsSmoke: this machine as both ends of the multi-host path, tend reaching its own launcher as a process (self)
// and a second dataset's (peer): the environment compared, a session handed off and migrated to peer, listed there
// with where it came from, migrating it again refused, and a project's memories compared.
func hostsSmoke(d, peer *fixture.Dataset, env []string, bin string) error {
	launcher := func(d *fixture.Dataset) string {
		if runtime.GOOS == "windows" {
			return filepath.Join(d.Root, "tend.cmd")
		}
		return filepath.Join(d.Root, "tend.sh")
	}
	cfg, _ := json.Marshal(map[string]any{"lang": "zh", "hosts": []map[string]any{{"name": "self", "tend": []string{launcher(d)}},
		{"name": "peer", "tend": []string{launcher(peer)}}}})
	if err := os.WriteFile(filepath.Join(d.Home, "config.json"), cfg, 0o644); err != nil {
		return err
	}
	if err := run(env, bin, "hosts", "check"); err != nil {
		return fmt.Errorf("hosts check: %w", err)
	}
	if err := run(env, bin, "sessions", "host:self", "--limit", "3"); err != nil {
		return err
	}
	oauth, here, there := d.Get("oauth"), filepath.Join(d.Work, "webapp"), filepath.Join(peer.Work, "webapp")
	if err := run(env, bin, "env", "diff", "peer", "--session", oauth.ID, "--there", there); err != nil {
		return fmt.Errorf("env diff: %w", err)
	}
	if err := run(env, bin, "handoff", oauth.ID, "--host", "peer", "--dir", there, "--print"); err != nil {
		return fmt.Errorf("handoff --print: %w", err)
	}
	out, err := output(env, bin, "handoff", oauth.ID, "--host", "peer", "--dir", there)
	id, ok := strings.CutPrefix(strings.TrimSpace(out), "tend handoff --open ")
	if err != nil || !ok || !paths.Exists(filepath.Join(peer.Home, "handoff", id+".md")) {
		return fmt.Errorf("handoff: %q %v", out, err)
	}

	l := fixture.NewLiveClaude(d.Claude, migrated, here, "cli")
	for _, s := range []string{"one", "two", "three"} {
		if err := cmp.Or(l.User("migrate "+s), l.Reply("migrated "+s)); err != nil {
			return err
		}
	}
	if err := run(env, bin, "sessions", "--limit", "1"); err != nil {
		return err
	}
	if out, err := output(env, bin, "migrate", migrated, "--to", "peer", "--dir", there); err != nil || strings.TrimSpace(out) != "tend resume peer:"+migrated {
		return fmt.Errorf("migrate: %q %v", out, err)
	}
	copied := filepath.Join(peer.Claude, "projects", index.ClaudeProjectName(there), migrated+".jsonl")
	cwd, _ := json.Marshal(there)
	if b, err := os.ReadFile(copied); err != nil || !strings.Contains(string(b), `"cwd":`+string(cwd)) {
		return fmt.Errorf("migrated copy on peer: %v", err)
	}
	if out, err := output(env, bin, "sessions", "host:peer", "migrate"); err != nil || !strings.Contains(out, "<- ") || strings.Contains(out, "<- laptop") {
		return fmt.Errorf("sessions host:peer marks where it came from: %v\n%s", err, out)
	}
	if err := run(env, bin, "migrate", migrated, "--to", "peer", "--dir", there); err == nil {
		return errors.New("migrating it again is refused")
	}
	if err := run(env, bin, "memory", "diff", here, "peer", "--dir", there); err != nil {
		return fmt.Errorf("memory diff: %w", err)
	}
	return nil
}

// migrated is the session the smoke writes on self and migrates to peer: the two datasets share every other id.
const migrated = "5e551011-0c1a-4de0-8000-0000000000e1"

func output(env []string, name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	c.Env, c.Stderr = env, os.Stderr
	out, err := c.Output()
	os.Stdout.Write(out)
	return string(out), err
}

func run(env []string, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Env, c.Stdout, c.Stderr = env, os.Stdout, os.Stderr
	return c.Run()
}
