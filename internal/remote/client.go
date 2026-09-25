package remote

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Client talks to one `tend rpc --stdio`, over ssh or as a local process. Calls run concurrently; one that times out
// only gives up (the other end is told to cancel it); the process ending, or Close, ends the client: Dial again.
type Client struct {
	conn   *wire.Conn
	cmd    *exec.Cmd
	stderr *tail
	ssh    bool
	reaped sync.Once
	why    *wire.Error // how the process ended, set once reaped
}

// reapWait: how long a process whose output ended may take to exit before it is killed.
const reapWait = time.Second

// stdio is a child's stdout and stdin as one stream.
type stdio struct {
	io.ReadCloser
	io.WriteCloser
}

func (s stdio) Close() error {
	s.WriteCloser.Close()
	return s.ReadCloser.Close()
}

// Dial starts `rpc --stdio` on h.
func Dial(h tend.Host) (*Client, error) { return DialWith(h, wire.Options{}) }

// DialWith is Dial with the connection's options: a coordinator answers the node's pushes.
func DialWith(h tend.Host, opt wire.Options) (*Client, error) {
	cmd := Command(h, false, "rpc", "--stdio")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	t := &tail{}
	cmd.Stderr = t
	cmd.WaitDelay = time.Second // a child left behind may hold stderr open
	if err := cmd.Start(); err != nil {
		return nil, &wire.Error{Code: wire.CodeClosed, Detail: err.Error()}
	}
	c := &Client{cmd: cmd, stderr: t, ssh: h.SSH != ""}
	c.conn = wire.New(stdio{out, in}, opt)
	return c, nil
}

// Done is closed once the connection ended.
func (c *Client) Done() <-chan struct{} { return c.conn.Done() }

// Call sends method with params and decodes the result into out (nil: ignore it); ctx bounds the call.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	err := c.conn.Call(ctx, method, params, out)
	if wire.Code(err) == wire.CodeClosed {
		if why := c.Err(); why != nil {
			return why
		}
	}
	return err
}

// Err is why the client stopped, nil while it works.
func (c *Client) Err() *wire.Error {
	why := c.conn.Err()
	if why == nil || c.cmd == nil {
		return why
	}
	c.reaped.Do(func() { c.why = c.reap(why) })
	return c.why
}

// reap waits for the process whose connection ended and says why it ended.
func (c *Client) reap(closed *wire.Error) *wire.Error {
	exited := make(chan error, 1)
	go func() { exited <- c.cmd.Wait() }()
	var err error
	select {
	case err = <-exited:
	case <-time.After(reapWait): // its output ended but it keeps running
		c.cmd.Process.Kill()
		err = <-exited
	}
	if closed.Code != wire.CodeClosed {
		return closed
	}
	why := classify(err, c.stderr.String(), c.ssh)
	if why.Code == wire.CodeClosed && why.Detail == "" {
		return closed
	}
	return why
}

// Close ends the client; a call in flight returns at once.
func (c *Client) Close() error {
	if c.cmd != nil {
		c.cmd.Process.Kill()
	}
	c.conn.Close()
	if c.cmd != nil {
		go c.Err() // reap in the background
	}
	return nil
}

var (
	authFailed = regexp.MustCompile(`Permission denied|Too many authentication failures`)
	hostKeyBad = regexp.MustCompile(`Host key verification failed|REMOTE HOST IDENTIFICATION HAS CHANGED|host key .* not known`)
	noCommand  = regexp.MustCompile(`(?m)command not found|: not found$|is not recognized as|^fish: Unknown command|^(ba|da|k)?sh(: line \d+)?: [^:\n]+: No such file or directory$|^zsh:\d+: no such file or directory:|execvpe\([^)]*\) failed: No such file or directory`)
)

// classify turns a finished process into an error code: ssh exits 255 when it cannot reach or log in.
func classify(err error, stderr string, ssh bool) *wire.Error {
	detail := strings.TrimSpace(stderr)
	var exit *exec.ExitError
	if ssh && errors.As(err, &exit) && exit.ExitCode() == 255 {
		switch {
		case hostKeyBad.MatchString(stderr):
			return &wire.Error{Code: wire.CodeHostKey, Detail: detail}
		case authFailed.MatchString(stderr):
			return &wire.Error{Code: wire.CodeAuth, Detail: detail}
		}
		return &wire.Error{Code: wire.CodeOffline, Detail: detail}
	}
	if errors.As(err, &exit) && noCommand.MatchString(stderr) {
		return &wire.Error{Code: wire.CodeNoTend, Detail: detail}
	}
	return &wire.Error{Code: wire.CodeClosed, Detail: detail}
}

// Command runs tend on h with args: over ssh (tty for an interactive resume), or as a local process when h.SSH is empty.
func Command(h tend.Host, tty bool, args ...string) *exec.Cmd {
	argv := append(slices.Clone(h.Tend), args...)
	if len(h.Tend) == 0 {
		argv = append([]string{"tend"}, args...)
	}
	if tty {
		argv = withTTY(argv)
	}
	if h.SSH == "" {
		return exec.Command(argv[0], argv[1:]...)
	}
	opts := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=8", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "-o", "Compression=yes"}
	if tty {
		opts = append(opts, "-t")
	} else {
		opts = append(opts, "-T")
	}
	opts = append(opts, multiplex()...)
	return exec.Command("ssh", append(opts, h.SSH, RemoteShell(h).Join(argv))...)
}

// withTTY asks a container runtime for a terminal too: ssh -t gives one to docker, not to what runs inside.
func withTTY(argv []string) []string {
	if len(argv) < 3 || argv[1] != "exec" {
		return argv
	}
	switch strings.TrimSuffix(strings.ToLower(pathmap.Base(argv[0])), ".exe") {
	case "docker", "podman", "nerdctl":
	default:
		return argv
	}
	for _, a := range argv[2:] {
		switch {
		case a == "--tty" || strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "t"):
			return argv
		case !strings.HasPrefix(a, "-"): // the container: its flags are over
			return slices.Insert(slices.Clone(argv), 2, "-t")
		}
	}
	return argv
}

// RemoteShell is how h's login shell reads the command line: h.Shell, else cmd when the tend command looks like
// Windows (a drive path, .exe / .cmd, wsl), else POSIX.
func RemoteShell(h tend.Host) shell.Kind {
	if k, ok := shell.Named(h.Shell); ok {
		return k
	}
	if len(h.Tend) > 0 {
		p := strings.ToLower(h.Tend[0])
		if pathmap.Drive(p) || pathmap.Base(p) == "wsl" || slices.Contains([]string{".exe", ".cmd", ".bat"}, path.Ext(p)) {
			return shell.Cmd
		}
	}
	return shell.POSIX
}

// multiplex shares one ssh connection per host between tend's calls; Windows' OpenSSH has no ControlMaster.
func multiplex() []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir := filepath.Join(tend.Home(), "hosts", "ssh")
	// ssh adds "/" + 40 hex (%C) + a 17-byte temp suffix to the directory.
	if !paths.SocketRoom(dir, 58) || os.MkdirAll(dir, 0o700) != nil {
		return nil
	}
	return []string{"-o", "ControlMaster=auto", "-o", "ControlPath=" + filepath.Join(dir, "%C"), "-o", "ControlPersist=60"}
}

// tail keeps the last few KB a process wrote to stderr.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if n := len(t.buf); n > 4096 {
		t.buf = t.buf[n-4096:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
