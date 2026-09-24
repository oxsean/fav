package node

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fixture"
)

// FakeAgent is `tend _fake-agent`: it stands in for claude without a model. It writes a Claude-shaped transcript of
// session --session (entrypoint sdk-cli, as `claude -p` does), prints a line per step and exits with --exit.
func FakeAgent(args []string) error {
	fs := flag.NewFlagSet("_fake-agent", flag.ContinueOnError)
	sid := fs.String("session", "", "session id")
	dir := fs.String("dir", "", "working directory")
	promptFile := fs.String("prompt-file", "", "the brief, when it does not come on stdin")
	steps := fs.Int("steps", 2, "replies before it ends")
	every := fs.Duration("every", time.Second, "time between replies")
	exit := fs.Int("exit", 0, "exit code")
	ask := fs.Bool("ask", false, "end by asking the user a question and waiting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	brief := ""
	if *promptFile != "" {
		b, _ := os.ReadFile(*promptFile)
		brief = string(b)
	}
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
		if b, _ := io.ReadAll(os.Stdin); len(b) > 0 {
			brief = string(b)
		}
	}
	if *sid == "" {
		return fmt.Errorf("--session is required")
	}
	cwd := *dir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	t := fixture.NewLiveClaude(capture.ClaudeHome(), *sid, cwd, "sdk-cli")
	if err := t.User(strings.TrimSpace(brief)); err != nil {
		return err
	}
	for i := 1; i <= *steps; i++ {
		time.Sleep(*every)
		msg := fmt.Sprintf("fake step %d of %d", i, *steps)
		fmt.Println(msg)
		if err := t.Reply(msg); err != nil {
			return err
		}
	}
	if *ask {
		t.Ask("Continue?")
		fmt.Println("waiting for an answer")
		for {
			time.Sleep(time.Hour) // ⚠️ not select{}: with no other goroutine the runtime ends it as a deadlock
		}
	}
	os.Exit(*exit)
	return nil
}
