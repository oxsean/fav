package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

type echoHandler struct{ block chan struct{} }

func (h echoHandler) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case MEcho:
		var t Text
		json.Unmarshal(params, &t)
		return t, nil
	case "slow":
		<-h.block
		return nil, nil
	}
	return nil, &Error{Code: CodeUnknownMethod}
}

func pipeClient(t *testing.T, h Handler) *Client {
	t.Helper()
	c := Pipe(h)
	t.Cleanup(func() { c.Close() })
	return c
}

func TestCallRoundTripsAndReportsCodes(t *testing.T) {
	c := pipeClient(t, echoHandler{})
	var got Text
	if err := c.Call(context.Background(), MEcho, Text{"中文 ✓ \"q\" 'x' %PATH% $HOME"}, &got); err != nil || got.Text != "中文 ✓ \"q\" 'x' %PATH% $HOME" {
		t.Fatalf("echo: %q %v", got.Text, err)
	}
	var e *Error
	if err := c.Call(context.Background(), "nope", nil, nil); !errors.As(err, &e) || e.Code != CodeUnknownMethod {
		t.Fatalf("unknown method: %v", err)
	}
	if c.Err() != nil {
		t.Fatal("a remote error must not close the client")
	}
}

func TestServerRefusesAnotherProto(t *testing.T) {
	r := answer(context.Background(), []byte(`{"proto":99,"id":7,"method":"echo"}`), echoHandler{})
	if r.OK || r.ID != 7 || r.Error.Code != CodeProto {
		t.Fatalf("%+v", r)
	}
	if r := answer(context.Background(), []byte(`{"proto":99,"id":8,"method":"hello"}`), echoHandler{}); r.Error.Code != CodeUnknownMethod {
		t.Fatalf("hello answers whatever the proto, so the caller can tell: %+v", r)
	}
}

func TestATimedOutCallOnlyStopsWaiting(t *testing.T) {
	block := make(chan struct{})
	c := pipeClient(t, echoHandler{block})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "slow", nil, nil); code(err) != CodeTimeout {
		t.Fatalf("slow: %v", err)
	}
	close(block)
	var got Text
	if err := c.Call(context.Background(), MEcho, Text{"x"}, &got); err != nil || got.Text != "x" {
		t.Fatalf("the late answer is skipped and the client keeps working: %q %v", got.Text, err)
	}
}

func TestCloseDoesNotWaitForACallInFlight(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	c := Pipe(echoHandler{block})
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "slow", nil, nil) }()
	time.Sleep(50 * time.Millisecond)
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited for the call")
	}
	if err := <-errc; code(err) != CodeClosed {
		t.Fatalf("the call in flight ends as closed: %v", err)
	}
}

// oneShot answers the first request, then ends: the answer and the end arrive together.
type oneShot struct{}

func (oneShot) Handle(context.Context, string, json.RawMessage) (any, error) {
	return Text{"last"}, nil
}

func TestAnAnswerJustBeforeTheEndIsKept(t *testing.T) {
	for i := 0; i < 200; i++ {
		reqR, reqW := io.Pipe()
		resR, resW := io.Pipe()
		go func() {
			Serve(context.Background(), reqR, resW, oneShot{}, true)
			resW.Close()
		}()
		c := NewClient(resR, reqW)
		var got Text
		if err := c.Call(context.Background(), MEcho, nil, &got); err != nil || got.Text != "last" {
			t.Fatalf("run %d: %q %v", i, got.Text, err)
		}
		c.Close()
	}
}

func TestADeadProcessClosesWithItsReason(t *testing.T) {
	testkit.PosixOnly(t) // a shell script stands in for fav
	for _, c := range []struct{ script, code string }{
		{"echo 'Welcome!'; read x; exit 3", CodeClosed},
		{"echo 'sh: 1: fav: not found' >&2; exit 127", CodeNoFav},
	} {
		cl, err := Dial(fav.Host{Name: "t", Fav: []string{"sh", "-c", c.script, "fav"}})
		if err != nil {
			t.Fatal(err)
		}
		err = cl.Call(context.Background(), MEcho, Text{"x"}, nil)
		if code(err) != c.code || code(cl.Err()) != c.code {
			t.Errorf("%q: %v, want %s", c.script, err, c.code)
		}
		cl.Close()
	}
}

func TestHostsRedialAfterAFailure(t *testing.T) {
	var dials atomic.Int32
	var last *Client
	h := NewHostsDial([]fav.Host{{Name: "a"}}, "", func(fav.Host) (*Client, error) {
		dials.Add(1)
		last = Pipe(helloHandler{})
		return last, nil
	})
	defer h.Close()
	ctx := context.Background()
	if err := h.Call(ctx, "a", MEcho, Text{"x"}, nil); err != nil {
		t.Fatal(err)
	}
	last.Close()
	if err := h.Call(ctx, "a", MEcho, Text{"x"}, nil); err != nil || dials.Load() != 2 {
		t.Fatalf("a closed client is dialed again: %v, %d dials", err, dials.Load())
	}
	if err := h.Call(ctx, "b", MEcho, nil, nil); code(err) != CodeNotFound {
		t.Fatalf("an unknown host: %v", err)
	}
}

type helloHandler struct{}

func (helloHandler) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method == MHello {
		return Hello{Proto: Proto}, nil
	}
	return echoHandler{}.Handle(ctx, method, params)
}

func TestCommandLines(t *testing.T) {
	testkit.PosixOnly(t) // multiplexing is on only off Windows
	sid := "fa000009-0c1a-4de0-8000-000000000009"
	for _, c := range []struct {
		host fav.Host
		want string // the command line the remote shell reads
	}{
		{fav.Host{Name: "m", SSH: "m"}, "fav resume --terminal --no-herdr " + sid},
		{fav.Host{Name: "m", SSH: "m", Fav: []string{"/Users/a b/fav"}}, "'/Users/a b/fav' resume --terminal --no-herdr " + sid},
		{fav.Host{Name: "w", SSH: "w", Fav: []string{`C:\Users\a b\fav.exe`}}, `"C:\Users\a b\fav.exe" resume --terminal --no-herdr ` + sid},
		{fav.Host{Name: "l", SSH: "w", Fav: []string{"wsl", "-d", "Debian", "-e", "/home/a/fav"}}, "wsl -d Debian -e /home/a/fav resume --terminal --no-herdr " + sid},
		{fav.Host{Name: "d", SSH: "n", Fav: []string{"docker", "exec", "-i", "dev", "/usr/local/bin/fav"}}, "docker exec -t -i dev /usr/local/bin/fav resume --terminal --no-herdr " + sid},
		{fav.Host{Name: "e", SSH: "n", Fav: []string{"docker", "exec", "-it", "dev", "fav"}}, "docker exec -it dev fav resume --terminal --no-herdr " + sid},
	} {
		h := NewHosts([]fav.Host{c.host}, "")
		cmd, ok := h.ResumeCommand(&fav.Rec{Host: c.host.Name, SessionID: sid})
		if !ok {
			t.Fatalf("%s: no command", c.host.Name)
		}
		args := cmd.Args
		if args[0] != "ssh" || !slices.Contains(args, "-t") || !slices.Contains(args, "BatchMode=yes") || args[len(args)-2] != c.host.SSH || args[len(args)-1] != c.want {
			t.Errorf("%s: %q", c.host.Name, args)
		}
	}
	h := NewHosts([]fav.Host{{Name: "m", SSH: "m"}}, "")
	if _, ok := h.ResumeCommand(&fav.Rec{Host: "m", SessionID: "x; rm -rf ~"}); ok {
		t.Error("a session id with shell syntax never reaches the remote shell")
	}
	if cmd := Command(fav.Host{Name: "p", Fav: []string{"/bin/fav"}}, false, "rpc"); cmd.Args[0] != "/bin/fav" || len(cmd.Args) != 2 {
		t.Errorf("no ssh alias runs fav here: %q", cmd.Args)
	}
}

func TestClassifySSHFailures(t *testing.T) {
	testkit.PosixOnly(t) // needs sh to produce an exit status
	exit255 := exec.Command("sh", "-c", "exit 255").Run()
	for _, c := range []struct{ stderr, code string }{
		{"ssh: connect to host x port 22: Operation timed out", CodeOffline},
		{"git@x: Permission denied (publickey).", CodeAuth},
		{"Host key verification failed.", CodeHostKey},
	} {
		if got := classify(exit255, c.stderr, true); got.Code != c.code {
			t.Errorf("%q: %s, want %s", c.stderr, got.Code, c.code)
		}
	}
	exit1 := exec.Command("sh", "-c", "exit 1").Run()
	for _, c := range []struct{ stderr, code string }{
		{"'fav' is not recognized as an internal or external command,", CodeNoFav},
		{"fav: The term 'fav' is not recognized as a name of a cmdlet, function, script file, or executable program.", CodeNoFav},
		{"fish: Unknown command: fav", CodeNoFav},
		{"bash: /x/fav: No such file or directory", CodeNoFav},
		{"zsh:1: no such file or directory: /x/fav", CodeNoFav},
		{"<3>WSL (12) ERROR: CreateProcessCommon:640: execvpe(/home/a/fav) failed: No such file or directory", CodeNoFav},
		{"fav: index not written: open /x/sessions.jsonl: no such file or directory\npanic: boom", CodeClosed},
		{"fav: open /home/u/.agent/fav/x: No such file or directory", CodeClosed},
	} {
		if got := classify(exit1, c.stderr, true); got.Code != c.code {
			t.Errorf("%q: %s, want %s", c.stderr, got.Code, c.code)
		}
	}
	if got := classify(exit255, "", false); got.Code != CodeClosed {
		t.Errorf("a local process exiting 255 is not an ssh failure: %s", got.Code)
	}
}

func TestRemoteShellGuess(t *testing.T) {
	for _, c := range []struct {
		fav  []string
		want shell.Kind
	}{
		{nil, shell.POSIX},
		{[]string{"/home/me/.local/bin/fav"}, shell.POSIX},
		{[]string{`C:\Users\Administrator\fav.exe`}, shell.Cmd},
		{[]string{"C:/Users/a/fav-test/data/fav.cmd"}, shell.Cmd},
		{[]string{"wsl", "-d", "Debian", "--", "fav"}, shell.Cmd},
		{[]string{"wsl.exe", "-d", "Debian"}, shell.Cmd},
	} {
		if got := RemoteShell(fav.Host{Fav: c.fav}); got != c.want {
			t.Errorf("%v: %s", c.fav, got.Name())
		}
	}
	if RemoteShell(fav.Host{Fav: []string{"fav"}, Shell: "powershell"}) != shell.PowerShell {
		t.Error("an explicit shell wins")
	}
}

func TestSessionTimesReadInThisZone(t *testing.T) {
	at := time.Date(2026, 9, 23, 20, 55, 0, 0, time.UTC)
	var s Session
	if err := json.Unmarshal([]byte(`{"last_at":"2026-09-23T20:55:00Z","updated_at":"2026-09-23T20:55:00Z","started_at":"2026-09-23T20:00:00Z"}`), &s); err != nil {
		t.Fatal(err)
	}
	r := s.Rec("linux")
	if r.LastAt.Location() != time.Local || !r.LastAt.Equal(at) || r.SessionStartedAt.Location() != time.Local {
		t.Fatalf("a remote in another zone shows its times in ours: %v %v", r.LastAt, r.SessionStartedAt)
	}
}

func TestAProcessThatStopsTalkingButRunsIsReaped(t *testing.T) {
	testkit.PosixOnly(t)
	cl, err := Dial(fav.Host{Name: "t", Fav: []string{"sh", "-c", "exec >&-; sleep 60", "fav"}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := cl.Call(context.Background(), MEcho, Text{"x"}, nil); code(err) != CodeClosed || time.Since(start) > reapWait+2*time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
}

func TestACallWaitingForItsTurnKeepsItsDeadline(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	c := pipeClient(t, echoHandler{block})
	go c.Call(context.Background(), "slow", nil, nil)
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := c.Call(ctx, MEcho, Text{"x"}, nil); code(err) != CodeTimeout || time.Since(start) > time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
}

func TestHostsClosedWhileDialingKeepNothing(t *testing.T) {
	dialing, release := make(chan struct{}), make(chan struct{})
	var c *Client
	var dials atomic.Int32
	h := NewHostsDial([]fav.Host{{Name: "a"}}, "", func(fav.Host) (*Client, error) {
		if dials.Add(1) == 1 {
			close(dialing)
		}
		<-release
		c = Pipe(helloHandler{})
		return c, nil
	})
	errc := make(chan error, 2)
	go func() { errc <- h.Call(context.Background(), "a", MEcho, nil, nil) }()
	<-dialing
	go func() { errc <- h.Call(context.Background(), "a", MEcho, nil, nil) }() // waits for the first dial
	time.Sleep(50 * time.Millisecond)
	h.Close()
	close(release)
	for range 2 {
		if err := <-errc; code(err) != CodeClosed {
			t.Fatalf("calls around Close end closed: %v", err)
		}
	}
	if c.Err() == nil || dials.Load() != 1 {
		t.Fatalf("a client finished after Close is closed, and nobody dials again: %d dials", dials.Load())
	}
	if err := h.Call(context.Background(), "a", MEcho, nil, nil); code(err) != CodeClosed {
		t.Fatalf("a closed Hosts dials nothing: %v", err)
	}
}

func TestCachesAreKeptApartAndFollowTheirTarget(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	if cachePath("机器一") == cachePath("机器二") || cachePath("ssh") == filepath.Join(fav.Home(), "hosts", "ssh", "sessions.json") {
		t.Fatal("cache dirs collide")
	}
	f := func(fav.Host) (*Client, error) { return Pipe(listHandler{}), nil }
	h := NewHostsDial([]fav.Host{{Name: "机器一", SSH: "a"}}, "", f)
	defer h.Close()
	if recs, st := h.Sessions(context.Background(), "机器一"); st.Err != nil || len(recs) != 1 {
		t.Fatalf("%v %d", st.Err, len(recs))
	}
	info, err := os.Stat(cachePath("机器一"))
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("cache file: %v %v", err, info)
	}
	if recs, _ := h.Cached("机器一"); len(recs) != 1 || recs[0].Host != "机器一" {
		t.Fatalf("cached: %v", recs)
	}
	moved := NewHostsDial([]fav.Host{{Name: "机器一", SSH: "b"}}, "", f)
	if recs, st := moved.Cached("机器一"); len(recs) != 0 || !st.At.IsZero() {
		t.Fatal("a host pointed at another machine does not show the old machine's cache")
	}
}

type listHandler struct{}

func (listHandler) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method == MList {
		return List{Sessions: []Session{{Provider: fav.ProviderClaude, SessionID: "s1", Title: "t"}}}, nil
	}
	return helloHandler{}.Handle(ctx, method, params)
}

func TestCloseDoesNotWaitForAReap(t *testing.T) {
	testkit.PosixOnly(t)
	cl, err := Dial(fav.Host{Name: "t", Fav: []string{"sh", "-c", "exec >&-; sleep 60", "fav"}})
	if err != nil {
		t.Fatal(err)
	}
	go cl.Call(context.Background(), MEcho, Text{"x"}, nil)
	time.Sleep(300 * time.Millisecond) // the call is reaping
	start := time.Now()
	cl.Close()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Close took %v", d)
	}
}

func TestACallWaitingForAnotherDialKeepsItsDeadline(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := NewHostsDial([]fav.Host{{Name: "a"}}, "", func(fav.Host) (*Client, error) {
		<-release
		return Pipe(helloHandler{}), nil
	})
	defer h.Close()
	go h.Call(context.Background(), "a", MEcho, nil, nil)
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := h.Call(ctx, "a", MEcho, nil, nil); code(err) != CodeTimeout || time.Since(start) > time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
}
