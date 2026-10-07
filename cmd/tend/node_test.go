package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/oxsean/fav/internal/i18n"
)

// keepStdio puts back what --log replaces for the whole process, closing the log. Call it after the test's t.TempDir
// calls: cleanups run last registered first, and Windows cannot remove a log file still open.
func keepStdio(t *testing.T) {
	t.Helper()
	out, errOut, logOut := os.Stdout, os.Stderr, log.Writer()
	t.Cleanup(func() {
		if os.Stderr != errOut {
			os.Stderr.Close()
		}
		os.Stdout, os.Stderr = out, errOut
		log.SetOutput(logOut)
		debug.SetCrashOutput(nil, debug.CrashOptions{})
	})
}

// serviceEnv writes the env file install-service writes and points the test's own environment back afterwards.
func serviceEnv(t *testing.T, env [][2]string) string {
	t.Helper()
	for _, kv := range env {
		t.Setenv(kv[0], os.Getenv(kv[0]))
	}
	f := filepath.Join(t.TempDir(), "node dir", "service-env.json")
	if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte(envJSON(env)), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestNodeEnvFileAppliesBeforeTheConfig(t *testing.T) {
	home := filepath.Join(t.TempDir(), "tend home 中")
	path := `C:\Program Files\nodejs;"C:\Program Files\quoted";C:\odd%dir%;/usr/bin`
	envFile := serviceEnv(t, [][2]string{{"PATH", path}, {"TEND_HOME", home}})
	err := cmdNode([]string{"--connect", "ws://127.0.0.1:1", "--token-file", "absent", "--env", envFile})
	if err == nil || err.Error() != i18n.F("cli.node.need_allow_dirs", filepath.Join(home, "config.json")) {
		t.Errorf("err = %v", err)
	}
	if os.Getenv("PATH") != path || os.Getenv("TEND_HOME") != home {
		t.Errorf("PATH = %q, TEND_HOME = %q", os.Getenv("PATH"), os.Getenv("TEND_HOME"))
	}
}

func TestNodeEnvFileUnreadable(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "env.json")
	if err := os.WriteFile(bad, []byte("PATH=x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmdNode([]string{"--connect", "ws://127.0.0.1:1", "--token-file", "absent", "--env", bad}); err == nil {
		t.Error("a malformed env file was accepted")
	}
	err := cmdNode([]string{"--connect", "ws://127.0.0.1:1", "--token-file", "absent", "--env", bad + ".gone"})
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing env file: %v", err)
	}
}

func TestNodeLogAppendsEverything(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "node dir", "service.log")
	if err := os.MkdirAll(filepath.Dir(logFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logFile, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	envFile := serviceEnv(t, [][2]string{{"TEND_HOME", t.TempDir()}})
	keepStdio(t)
	err := cmdNode([]string{"--connect", "ws://127.0.0.1:1", "--token-file", "absent", "--env", envFile, "--log", logFile})
	if err == nil {
		t.Fatal("a node without node.allow_dirs ran")
	}
	fmt.Fprintln(os.Stderr, "tend: "+err.Error()) // what main does with it
	fmt.Fprintln(os.Stdout, "out")
	log.Print("logged")
	b, rerr := os.ReadFile(logFile)
	if rerr != nil {
		t.Fatal(rerr)
	}
	want := "before\ntend: " + err.Error() + "\nout\n"
	if got := string(b); len(got) < len(want) || got[:len(want)] != want || got[len(got)-len("logged\n"):] != "logged\n" {
		t.Errorf("log = %q", got)
	}
}

func TestNodeLogNeedsConnect(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "service.log")
	keepStdio(t)
	if err := cmdNode([]string{"--log", logFile}); err == nil || err.Error() != i18n.T("cli.node.log_needs_connect") {
		t.Errorf("err = %v", err)
	}
	if _, err := os.Stat(logFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("log written: %v", err)
	}
}
