package remote

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/shell"
)

// Client talks to one `fav rpc --stdio`; one call at a time. A call that times out while waiting for its answer only
// gives up (the answer is skipped when it comes); one that cannot even send, or a Close, ends the client, as does the
// other end stopping: Dial again.
type Client struct {
	turn    chan struct{} // holds the one call in progress
	mu      sync.Mutex    // err and fail
	r       io.Reader
	w       io.WriteCloser
	lines   chan []byte
	done    chan struct{} // closed when the other end stops answering
	stop    chan struct{} // closed by fail: the reader stops handing out lines
	closing chan struct{} // closed by Close: a waiting call returns at once
	once    sync.Once
	cmd     *exec.Cmd
	stderr  *tail
	ssh     bool
	next    int64
	err     *Error // why the client is closed
}

// reapWait: how long a process whose output ended may take to exit before it is killed.
const reapWait = time.Second

// NewClient speaks the protocol over r and w (an in-process server in tests).
func NewClient(r io.Reader, w io.WriteCloser) *Client {
	c := &Client{turn: make(chan struct{}, 1), r: r, w: w, lines: make(chan []byte, 1), done: make(chan struct{}),
		stop: make(chan struct{}), closing: make(chan struct{})}
	go func() {
		defer close(c.done)
		br := bufio.NewReaderSize(r, 1<<20)
		for {
			line, err := br.ReadBytes('\n')
			if len(line) > 0 {
				select {
				case c.lines <- bytes.ReplaceAll(line, []byte{0}, nil): // wsl.exe may interleave UTF-16 NULs
				case <-c.stop:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return c
}

// Dial starts `fav rpc --stdio` on h.
func Dial(h fav.Host) (*Client, error) {
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
		return nil, &Error{Code: CodeClosed, Detail: err.Error()}
	}
	c := NewClient(out, in)
	c.cmd, c.stderr, c.ssh = cmd, t, h.SSH != ""
	return c, nil
}

// Call sends method with params and decodes the result into out (nil: ignore it). ctx bounds the whole call,
// waiting for an earlier one included.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	select {
	case c.turn <- struct{}{}:
	case <-ctx.Done():
		return &Error{Code: CodeTimeout}
	case <-c.closing:
		return c.fail(&Error{Code: CodeClosed})
	}
	defer func() { <-c.turn }()
	if err := c.Err(); err != nil {
		return err
	}
	c.next++
	req := Request{Proto: Proto, ID: c.next, Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		req.Params = b
	}
	b, _ := json.Marshal(req)
	wrote := make(chan error, 1)
	go func() {
		_, err := c.w.Write(append(b, '\n'))
		wrote <- err
	}()
	select {
	case err := <-wrote:
		if err != nil {
			return c.fail(nil)
		}
	case <-ctx.Done(): // the other end does not read: a half-sent line breaks the stream
		return c.fail(&Error{Code: CodeTimeout})
	case <-c.closing:
		return c.fail(&Error{Code: CodeClosed})
	}
	for {
		select {
		case line := <-c.lines:
			if ok, err := answered(line, req.ID, out); ok {
				return err
			}
		case <-c.done:
			for { // the answer may have come just before the end
				select {
				case line := <-c.lines:
					if ok, err := answered(line, req.ID, out); ok {
						return err
					}
					continue
				default:
				}
				return c.fail(nil)
			}
		case <-c.closing:
			return c.fail(&Error{Code: CodeClosed})
		case <-ctx.Done():
			return &Error{Code: CodeTimeout}
		}
	}
}

// answered decodes line when it answers call id; a late answer to a timed-out call or noise is not one.
func answered(line []byte, id int64, out any) (bool, error) {
	var res Response
	if err := json.Unmarshal(line, &res); err != nil || res.ID != id {
		return false, nil
	}
	if !res.OK {
		if res.Error == nil {
			res.Error = &Error{Code: CodeInternal}
		}
		return true, res.Error
	}
	if out == nil {
		return true, nil
	}
	return true, json.Unmarshal(res.Result, out)
}

// Err is why the client stopped, nil while it works.
func (c *Client) Err() *Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close ends the client; a call in flight returns at once.
func (c *Client) Close() error {
	c.once.Do(func() { close(c.closing) })
	if c.cmd != nil {
		c.cmd.Process.Kill() // a call may be reaping it with mu held
	}
	c.fail(&Error{Code: CodeClosed})
	return nil
}

// fail closes the client with why, or with what the process's exit says.
func (c *Client) fail(why *Error) *Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	close(c.stop)
	c.w.Close()
	if rc, ok := c.r.(io.Closer); ok {
		rc.Close()
	}
	if c.cmd != nil {
		if why != nil {
			c.cmd.Process.Kill()
		}
		exited := make(chan error, 1)
		go func() { exited <- c.cmd.Wait() }()
		var err error
		select {
		case err = <-exited:
		case <-time.After(reapWait): // its output ended but it keeps running
			c.cmd.Process.Kill()
			err = <-exited
		}
		if why == nil {
			why = classify(err, c.stderr.String(), c.ssh)
		}
	}
	if why == nil {
		why = &Error{Code: CodeClosed}
	}
	c.err = why
	return why
}

var (
	authFailed = regexp.MustCompile(`Permission denied|Too many authentication failures`)
	hostKeyBad = regexp.MustCompile(`Host key verification failed|REMOTE HOST IDENTIFICATION HAS CHANGED|host key .* not known`)
	noCommand  = regexp.MustCompile(`(?m)command not found|: not found$|is not recognized as|^fish: Unknown command|^(ba|da|k)?sh(: line \d+)?: [^:\n]+: No such file or directory$|^zsh:\d+: no such file or directory:|execvpe\([^)]*\) failed: No such file or directory`)
)

// classify turns a finished process into an error code: ssh exits 255 when it cannot reach or log in.
func classify(err error, stderr string, ssh bool) *Error {
	detail := strings.TrimSpace(stderr)
	var exit *exec.ExitError
	if ssh && errors.As(err, &exit) && exit.ExitCode() == 255 {
		switch {
		case hostKeyBad.MatchString(stderr):
			return &Error{Code: CodeHostKey, Detail: detail}
		case authFailed.MatchString(stderr):
			return &Error{Code: CodeAuth, Detail: detail}
		}
		return &Error{Code: CodeOffline, Detail: detail}
	}
	if errors.As(err, &exit) && noCommand.MatchString(stderr) {
		return &Error{Code: CodeNoFav, Detail: detail}
	}
	return &Error{Code: CodeClosed, Detail: detail}
}

// Command runs fav on h with args: over ssh (tty for an interactive resume), or as a local process when h.SSH is empty.
func Command(h fav.Host, tty bool, args ...string) *exec.Cmd {
	argv := append(slices.Clone(h.Fav), args...)
	if len(h.Fav) == 0 {
		argv = append([]string{"fav"}, args...)
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

// RemoteShell is how h's login shell reads the command line: h.Shell, else cmd when the fav command looks like
// Windows (a drive path, .exe / .cmd, wsl), else POSIX.
func RemoteShell(h fav.Host) shell.Kind {
	if k, ok := shell.Named(h.Shell); ok {
		return k
	}
	if len(h.Fav) > 0 {
		p := strings.ToLower(h.Fav[0])
		if pathmap.Drive(p) || pathmap.Base(p) == "wsl" || slices.Contains([]string{".exe", ".cmd", ".bat"}, path.Ext(p)) {
			return shell.Cmd
		}
	}
	return shell.POSIX
}

// multiplex shares one ssh connection per host between fav's calls; Windows' OpenSSH has no ControlMaster.
func multiplex() []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir := filepath.Join(fav.Home(), "hosts", "ssh")
	// ⚠️ a Unix socket path holds 104 bytes (macOS); ssh adds "/" + 40 hex (%C) + a 17-byte temp suffix.
	if len(dir)+58 >= 104 || os.MkdirAll(dir, 0o700) != nil {
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
