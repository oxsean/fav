// Command test-host runs on a test target from the synced tree (`mise run test-host`): vet, test, then the tend binary
// against a fresh fixture dataset. The last line is `RESULT vet=… test=… smoke=…`, which scripts/test-hosts.sh collects.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/oxsean/fav/internal/fixture"
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
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	d, err := fixture.Build(dir, time.Now())
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
	if err := d.WriteLaunchers(bin); err != nil {
		return err
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
	return hostsSmoke(d, env, bin)
}

// taskSmoke: a task run to its end by the fake agent, this process being the coordinator for the command.
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
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &tasks); err != nil || len(tasks) != 1 {
		return fmt.Errorf("task list: %s", out)
	}
	return run(env, bin, "run", "start", "--runner", "background", "--wait", tasks[0].ID)
}

// hostsSmoke: this machine as both ends of the multi-host path, tend reaching its own launcher as a process.
func hostsSmoke(d *fixture.Dataset, env []string, bin string) error {
	launcher := filepath.Join(d.Root, "tend.sh")
	if runtime.GOOS == "windows" {
		launcher = filepath.Join(d.Root, "tend.cmd")
	}
	cfg, _ := json.Marshal(map[string]any{"lang": "zh", "hosts": []map[string]any{{"name": "self", "tend": []string{launcher}}}})
	if err := os.WriteFile(filepath.Join(d.Home, "config.json"), cfg, 0o644); err != nil {
		return err
	}
	if err := run(env, bin, "hosts", "check"); err != nil {
		return fmt.Errorf("hosts check: %w", err)
	}
	return run(env, bin, "sessions", "host:self", "--limit", "3")
}

func run(env []string, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Env, c.Stdout, c.Stderr = env, os.Stdout, os.Stderr
	return c.Run()
}
