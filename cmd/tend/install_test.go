package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/tend"
)

func TestInstallTarget(t *testing.T) {
	for _, c := range []struct {
		tend                 []string
		goos                 string
		ok                   bool
		via, dest, container string
		pre                  []string
	}{
		{nil, "linux", true, "", ".local/bin/tend", "", nil},
		{nil, "windows", true, "", ".local/bin/tend.exe", "", nil},
		{[]string{"tend"}, "linux", true, "", ".local/bin/tend", "", nil},
		{[]string{"/opt/tend/bin/tend"}, "linux", true, "", "/opt/tend/bin/tend", "", nil},
		{[]string{`C:\Users\Administrator\.local\bin\tend.exe`}, "windows", true, "", "C:/Users/Administrator/.local/bin/tend.exe", "", nil},
		{[]string{"C:/Tools/TEND.EXE"}, "windows", true, "", "C:/Tools/TEND.EXE", "", nil},
		{[]string{"wsl", "-d", "Debian", "-e", "/home/me/.local/bin/tend"}, "linux", true, "wsl", "/home/me/.local/bin/tend", "", []string{"-d", "Debian"}},
		{[]string{`C:\Windows\System32\wsl.exe`, "-d", "Ubuntu", "-u", "me", "--", "/home/me/tend"}, "linux", true, "wsl", "/home/me/tend", "", []string{"-d", "Ubuntu", "-u", "me"}},
		{[]string{"wsl", "/home/me/.local/bin/tend"}, "linux", true, "wsl", "/home/me/.local/bin/tend", "", nil},
		{[]string{"docker", "exec", "-i", "dev", "/usr/local/bin/tend"}, "linux", true, "docker", "/usr/local/bin/tend", "dev", nil},
		{[]string{"/opt/bin/podman", "exec", "-it", "-u", "me", "box", "/home/me/tend"}, "linux", true, "podman", "/home/me/tend", "box", []string{"-u", "me"}},
		{[]string{"/home/me/.cache/tend-test/data/tend.sh"}, "linux", false, "", "", "", nil},
		{[]string{"./bin/tend"}, "linux", false, "", "", "", nil},
		{[]string{`bin\tend.exe`}, "windows", false, "", "", "", nil},
		{[]string{"wsl", "-d", "Debian", "-e", "/home/me/tend.sh"}, "linux", false, "", "", "", nil},
		{[]string{"wsl", "-d", "Debian", "-e", "tend"}, "linux", false, "", "", "", nil},
		{[]string{"docker", "exec", "dev", "tend", "rpc"}, "linux", false, "", "", "", nil},
		{[]string{"ssh", "other", "tend"}, "linux", false, "", "", "", nil},
	} {
		tg, ok := installTarget(tend.Host{Tend: c.tend})
		if ok != c.ok {
			t.Errorf("%v: ok=%v", c.tend, ok)
			continue
		}
		if ok && (tg.via != c.via || tg.destFor(c.goos) != c.dest || tg.container != c.container || !slices.Equal(tg.pre, c.pre)) {
			t.Errorf("%v: %+v dest %s", c.tend, tg, tg.destFor(c.goos))
		}
	}
}

func TestInstallIntoWSLAndContainers(t *testing.T) {
	join := func(cs []*exec.Cmd) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Args[len(c.Args)-1])
		}
		return out
	}
	w := tend.Host{Name: "wsl", SSH: "pc", Tend: []string{"wsl", "-d", "Debian", "-e", "/home/me/.local/bin/tend"}}
	tg, _ := installTarget(w)
	steps, cleanup := tg.steps(w, "linux", "/tmp/x/tend")
	want := []string{"pc:" + tmpName,
		`wsl -d Debian -e sh -c "mkdir -p /home/me/.local/bin && cp tend-install.tmp /home/me/.local/bin/tend.new && chmod 755 /home/me/.local/bin/tend.new && mv -f /home/me/.local/bin/tend.new /home/me/.local/bin/tend"`}
	if got := join(steps); !slices.Equal(got, want) || cleanup.Args[len(cleanup.Args)-1] != "del /q "+tmpName {
		t.Errorf("wsl:\n%q\n%q", got, cleanup.Args)
	}

	d := tend.Host{Name: "box", SSH: "nas", Tend: []string{"/usr/local/bin/docker", "exec", "-i", "-u", "me", "dev", "/home/me/tend"}}
	tg, _ = installTarget(d)
	steps, cleanup = tg.steps(d, "linux", "/tmp/x/tend")
	want = []string{"nas:" + tmpName,
		"/usr/local/bin/docker exec -u me dev mkdir -p /home/me && /usr/local/bin/docker cp tend-install.tmp dev:/home/me/tend.new && " +
			"/usr/local/bin/docker exec -u me dev sh -c 'chmod 755 /home/me/tend.new && mv -f /home/me/tend.new /home/me/tend'"}
	if got := join(steps); !slices.Equal(got, want) || cleanup.Args[len(cleanup.Args)-1] != "rm -f "+tmpName {
		t.Errorf("container:\n%q\n%q", got, cleanup.Args)
	}
}

func TestHostsAddRmClear(t *testing.T) {
	machine(t)
	for _, c := range []struct {
		args []string
		tend []string
	}{
		{[]string{"mba", "mba", "--tend", "/Users/me/.local/bin/tend"}, []string{"/Users/me/.local/bin/tend"}},
		{[]string{"deb", "pc", "--wsl", "Debian", "--tend", "/home/me/.local/bin/tend"}, []string{"wsl", "-d", "Debian", "-e", "/home/me/.local/bin/tend"}},
		{[]string{"box", "nas", "--docker", "dev", "--docker-cmd", "podman", "--tend", "/root/tend"}, []string{"podman", "exec", "-i", "dev", "/root/tend"}},
		{[]string{"plain", "srv"}, nil},
	} {
		if err := run(append([]string{"hosts", "add", "--no-check"}, c.args...)); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		cfg := tend.LoadConfig()
		h := cfg.Hosts[len(cfg.Hosts)-1]
		if h.Name != c.args[0] || h.SSH != c.args[1] || !slices.Equal(h.Tend, c.tend) {
			t.Errorf("%v: %+v", c.args, h)
		}
	}
	for _, bad := range [][]string{
		{"MBA", "x"},                   // taken, whatever the case
		{"all", "x"},                   // a query word
		{"a:b", "x"},                   // host:id uses the colon
		{"w", "pc", "--wsl", "Debian"}, // no path inside
		{"w", "pc", "--wsl", "D", "--docker", "c", "--tend", "/f/tend"},
		{"w", "pc", "--shell", "fish"},
		{"only-name"},
	} {
		if err := run(append([]string{"hosts", "add", "--no-check"}, bad...)); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
	if n := len(tend.LoadConfig().Hosts); n != 4 {
		t.Fatalf("%d hosts", n)
	}
	cache := filepath.Dir(remoteCacheOf(t, "deb"))
	if err := run([]string{"hosts", "rm", "DEB", "box"}); err != nil {
		t.Fatal(err)
	}
	if names := hostNames(tend.LoadConfig().Hosts); !slices.Equal(names, []string{"mba", "plain"}) {
		t.Fatalf("left: %v", names)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Error("rm forgets the host's cache")
	}
	remoteCacheOf(t, "mba")
	if err := run([]string{"hosts", "clear"}); err != nil {
		t.Fatal(err)
	}
	if paths, _ := filepath.Glob(filepath.Join(tend.Home(), "hosts", "mba-*")); len(paths) != 0 {
		t.Errorf("clear forgets every cache: %v", paths)
	}
	if run([]string{"hosts", "rm", "nope"}) == nil || run([]string{"hosts", "install", "nope"}) == nil {
		t.Error("an unknown host is an error")
	}
}

// remoteCacheOf writes a cached list for host and returns its path.
func remoteCacheOf(t *testing.T, host string) string {
	t.Helper()
	dir := remote.CacheDir(host)
	os.MkdirAll(dir, 0o700)
	p := filepath.Join(dir, "sessions.json")
	os.WriteFile(p, []byte("{}"), 0o600)
	return p
}

func TestDirectInstallSwapsByRename(t *testing.T) {
	for _, c := range []struct {
		k          shell.Kind
		dest       string
		prep, swap string
	}{
		{shell.POSIX, ".local/bin/tend", "mkdir -p .local/bin", "mv -f .local/bin/tend.new .local/bin/tend"},
		{shell.POSIX, "/opt/my tend/tend", "mkdir -p '/opt/my tend'", "mv -f '/opt/my tend/tend.new' '/opt/my tend/tend'"},
		{shell.PowerShell, "C:/Tools/tend.exe", "New-Item -ItemType Directory -Force C:/Tools | Out-Null",
			"if (Test-Path C:/Tools/tend.exe) { Move-Item -Force C:/Tools/tend.exe C:/Tools/tend.exe.old }; Move-Item -Force C:/Tools/tend.exe.new C:/Tools/tend.exe"},
		{shell.Cmd, "C:/Tools/tend.exe", `if not exist C:\Tools mkdir C:\Tools`,
			`(if exist C:\Tools\tend.exe move /y C:\Tools\tend.exe C:\Tools\tend.exe.old >nul) & move /y C:\Tools\tend.exe.new C:\Tools\tend.exe`},
	} {
		if got := prepLine(c.k, c.dest); got != c.prep {
			t.Errorf("prep %s: %s", c.dest, got)
		}
		if got := swapLine(c.k, c.dest); got != c.swap {
			t.Errorf("swap %s: %s", c.dest, got)
		}
	}
	if remoteKind(tend.Host{}, "windows") != shell.Cmd || remoteKind(tend.Host{Shell: "powershell"}, "windows") != shell.PowerShell {
		t.Error("a Windows host without a shell setting runs cmd")
	}
}

func TestPushBinCommands(t *testing.T) {
	b := buildCmd("/src/tend", "windows", "amd64", "v1.2.3", "/tmp/x/tend")
	if want := []string{"go", "build", "-trimpath", "-ldflags", "-s -w -X main.version=v1.2.3", "-o", "/tmp/x/tend", "./cmd/tend"}; !slices.Equal(b.Args, want) {
		t.Errorf("build: %q", b.Args)
	}
	if b.Dir != "/src/tend" {
		t.Errorf("build dir: %s", b.Dir)
	}
	for _, e := range []string{"GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0"} {
		if !slices.Contains(b.Env, e) {
			t.Errorf("build env lacks %s", e)
		}
	}
	cp := scpCmd("lg-win", "/tmp/x/tend", "C:/Users/Administrator/.local/bin/tend.exe")
	if want := []string{"scp", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", "/tmp/x/tend", "lg-win:C:/Users/Administrator/.local/bin/tend.exe"}; !slices.Equal(cp.Args, want) {
		t.Errorf("scp: %q", cp.Args)
	}
}

func TestTendSourceRefusesOtherTrees(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	if got, err := tendSource(root); err != nil || got != root {
		t.Errorf("this checkout: %q %v", got, err)
	}
	other := t.TempDir()
	if _, err := tendSource(other); err == nil {
		t.Error("a directory without go.mod")
	}
	os.WriteFile(filepath.Join(other, "go.mod"), []byte("module example.com/other\n\ngo 1.27\n"), 0o644)
	if _, err := tendSource(other); err == nil {
		t.Error("another module")
	}
}
