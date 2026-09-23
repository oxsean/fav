// Command test-host runs on a test target from the synced tree (`mise run test-host`): vet, test, then the fav binary
// against a fresh fixture dataset. The last line is `RESULT vet=… test=… smoke=…`, which scripts/test-hosts.sh collects.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/oxsean/fav/internal/fixture"
)

func main() {
	root := os.Getenv("FAV_TEST_ROOT")
	if root == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		root = filepath.Join(cache, "fav-test")
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
	bin := filepath.Join(dir, "bin", "fav")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if err := run(nil, "go", "build", "-o", bin, "./cmd/fav"); err != nil {
		return err
	}
	if err := d.WriteLaunchers(bin); err != nil {
		return err
	}
	env := append(os.Environ(), d.Env()...)
	if err := run(env, bin, "sessions"); err != nil {
		return err
	}
	run(env, bin, "doctor")
	return nil
}

func run(env []string, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Env, c.Stdout, c.Stderr = env, os.Stdout, os.Stderr
	return c.Run()
}
