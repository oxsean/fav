package node

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	leave := fs.Duration("leave-child", 0, "leave a child this long holding the output when it exits")
	sleep := fs.Duration("sleep", 0, "only sleep this long (the child --leave-child leaves)")
	final := fs.String("final", "", "the last line it prints (its final message)")
	stderr := fs.String("stderr", "", "a line it prints to stderr before it ends")
	note := fs.String("note", "", "report this progress note to its run first")
	askReport := fs.String("ask-report", "", "report this question to its run first")
	stream := fs.Bool("stream", false, "talk claude's stream-json both ways on stdin and stdout")
	permission := fs.String("permission", "", "stream: after the first step, ask to use a tool (TOOL:WHAT)")
	question := fs.String("question", "", "stream: after the first step, ask the user (QUESTION|OPTION|OPTION…)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sleep > 0 {
		time.Sleep(*sleep)
		return nil
	}
	if *stream {
		return fakeStream(fakeOpts{sid: *sid, dir: *dir, steps: *steps, every: *every, exit: *exit, ask: *ask, final: *final,
			stderr: *stderr, note: *note, askReport: *askReport, permission: *permission, question: *question, leave: *leave})
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
	if *note != "" {
		if err := AddReport(os.Getenv(EnvRunDir), ReportNote, *note); err != nil {
			return err
		}
	}
	if *askReport != "" {
		if err := AddReport(os.Getenv(EnvRunDir), ReportAsk, *askReport); err != nil {
			return err
		}
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
	if *final != "" {
		fmt.Println(*final)
		if err := t.Reply(*final); err != nil {
			return err
		}
	}
	if *stderr != "" {
		fmt.Fprintln(os.Stderr, *stderr)
	}
	if *leave > 0 {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		c := exec.Command(self, "_fake-agent", "--sleep", leave.String())
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		if err := c.Start(); err != nil {
			return err
		}
	}
	os.Exit(*exit)
	return nil
}
