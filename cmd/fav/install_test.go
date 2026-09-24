package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/shell"
)

func TestInstallTarget(t *testing.T) {
	for _, c := range []struct {
		fav                  []string
		goos                 string
		ok                   bool
		via, dest, container string
		pre                  []string
	}{
		{nil, "linux", true, "", ".local/bin/fav", "", nil},
		{nil, "windows", true, "", ".local/bin/fav.exe", "", nil},
		{[]string{"fav"}, "linux", true, "", ".local/bin/fav", "", nil},
		{[]string{"/opt/fav/bin/fav"}, "linux", true, "", "/opt/fav/bin/fav", "", nil},
		{[]string{`C:\Users\Administrator\.local\bin\fav.exe`}, "windows", true, "", "C:/Users/Administrator/.local/bin/fav.exe", "", nil},
		{[]string{"C:/Tools/FAV.EXE"}, "windows", true, "", "C:/Tools/FAV.EXE", "", nil},
		{[]string{"wsl", "-d", "Debian", "-e", "/home/me/.local/bin/fav"}, "linux", true, "wsl", "/home/me/.local/bin/fav", "", []string{"-d", "Debian"}},
		{[]string{`C:\Windows\System32\wsl.exe`, "-d", "Ubuntu", "-u", "me", "--", "/home/me/fav"}, "linux", true, "wsl", "/home/me/fav", "", []string{"-d", "Ubuntu", "-u", "me"}},
		{[]string{"wsl", "/home/me/.local/bin/fav"}, "linux", true, "wsl", "/home/me/.local/bin/fav", "", nil},
		{[]string{"docker", "exec", "-i", "dev", "/usr/local/bin/fav"}, "linux", true, "docker", "/usr/local/bin/fav", "dev", nil},
		{[]string{"/opt/bin/podman", "exec", "-it", "-u", "me", "box", "/home/me/fav"}, "linux", true, "podman", "/home/me/fav", "box", []string{"-u", "me"}},
		{[]string{"/home/me/.cache/fav-test/data/fav.sh"}, "linux", false, "", "", "", nil},
		{[]string{"./bin/fav"}, "linux", false, "", "", "", nil},
		{[]string{`bin\fav.exe`}, "windows", false, "", "", "", nil},
		{[]string{"wsl", "-d", "Debian", "-e", "/home/me/fav.sh"}, "linux", false, "", "", "", nil},
		{[]string{"wsl", "-d", "Debian", "-e", "fav"}, "linux", false, "", "", "", nil},
		{[]string{"docker", "exec", "dev", "fav", "rpc"}, "linux", false, "", "", "", nil},
		{[]string{"ssh", "other", "fav"}, "linux", false, "", "", "", nil},
	} {
		tg, ok := installTarget(fav.Host{Fav: c.fav})
		if ok != c.ok {
			t.Errorf("%v: ok=%v", c.fav, ok)
			continue
		}
		if ok && (tg.via != c.via || tg.destFor(c.goos) != c.dest || tg.container != c.container || !slices.Equal(tg.pre, c.pre)) {
			t.Errorf("%v: %+v dest %s", c.fav, tg, tg.destFor(c.goos))
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
	w := fav.Host{Name: "wsl", SSH: "pc", Fav: []string{"wsl", "-d", "Debian", "-e", "/home/me/.local/bin/fav"}}
	tg, _ := installTarget(w)
	steps, cleanup := tg.steps(w, "linux", "/tmp/x/fav")
	want := []string{"pc:" + tmpName,
		`wsl -d Debian -e sh -c "mkdir -p /home/me/.local/bin && cp fav-install.tmp /home/me/.local/bin/fav.new && chmod 755 /home/me/.local/bin/fav.new && mv -f /home/me/.local/bin/fav.new /home/me/.local/bin/fav"`}
	if got := join(steps); !slices.Equal(got, want) || cleanup.Args[len(cleanup.Args)-1] != "del /q "+tmpName {
		t.Errorf("wsl:\n%q\n%q", got, cleanup.Args)
	}

	d := fav.Host{Name: "box", SSH: "nas", Fav: []string{"/usr/local/bin/docker", "exec", "-i", "-u", "me", "dev", "/home/me/fav"}}
	tg, _ = installTarget(d)
	steps, cleanup = tg.steps(d, "linux", "/tmp/x/fav")
	want = []string{"nas:" + tmpName,
		"/usr/local/bin/docker exec -u me dev mkdir -p /home/me && /usr/local/bin/docker cp fav-install.tmp dev:/home/me/fav.new && " +
			"/usr/local/bin/docker exec -u me dev sh -c 'chmod 755 /home/me/fav.new && mv -f /home/me/fav.new /home/me/fav'"}
	if got := join(steps); !slices.Equal(got, want) || cleanup.Args[len(cleanup.Args)-1] != "rm -f "+tmpName {
		t.Errorf("container:\n%q\n%q", got, cleanup.Args)
	}
}

func TestHostsAddRmClear(t *testing.T) {
	machine(t)
	for _, c := range []struct {
		args []string
		fav  []string
	}{
		{[]string{"mba", "mba", "--fav", "/Users/me/.local/bin/fav"}, []string{"/Users/me/.local/bin/fav"}},
		{[]string{"deb", "pc", "--wsl", "Debian", "--fav", "/home/me/.local/bin/fav"}, []string{"wsl", "-d", "Debian", "-e", "/home/me/.local/bin/fav"}},
		{[]string{"box", "nas", "--docker", "dev", "--docker-cmd", "podman", "--fav", "/root/fav"}, []string{"podman", "exec", "-i", "dev", "/root/fav"}},
		{[]string{"plain", "srv"}, nil},
	} {
		if err := run(append([]string{"hosts", "add", "--no-check"}, c.args...)); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		cfg := fav.LoadConfig()
		h := cfg.Hosts[len(cfg.Hosts)-1]
		if h.Name != c.args[0] || h.SSH != c.args[1] || !slices.Equal(h.Fav, c.fav) {
			t.Errorf("%v: %+v", c.args, h)
		}
	}
	for _, bad := range [][]string{
		{"MBA", "x"},                   // taken, whatever the case
		{"all", "x"},                   // a query word
		{"a:b", "x"},                   // host:id uses the colon
		{"w", "pc", "--wsl", "Debian"}, // no path inside
		{"w", "pc", "--wsl", "D", "--docker", "c", "--fav", "/f/fav"},
		{"w", "pc", "--shell", "fish"},
		{"only-name"},
	} {
		if err := run(append([]string{"hosts", "add", "--no-check"}, bad...)); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
	if n := len(fav.LoadConfig().Hosts); n != 4 {
		t.Fatalf("%d hosts", n)
	}
	cache := filepath.Dir(remoteCacheOf(t, "deb"))
	if err := run([]string{"hosts", "rm", "DEB", "box"}); err != nil {
		t.Fatal(err)
	}
	if names := hostNames(fav.LoadConfig().Hosts); !slices.Equal(names, []string{"mba", "plain"}) {
		t.Fatalf("left: %v", names)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Error("rm forgets the host's cache")
	}
	remoteCacheOf(t, "mba")
	if err := run([]string{"hosts", "clear"}); err != nil {
		t.Fatal(err)
	}
	if paths, _ := filepath.Glob(filepath.Join(fav.Home(), "hosts", "mba-*")); len(paths) != 0 {
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
		{shell.POSIX, ".local/bin/fav", "mkdir -p .local/bin", "mv -f .local/bin/fav.new .local/bin/fav"},
		{shell.POSIX, "/opt/my fav/fav", "mkdir -p '/opt/my fav'", "mv -f '/opt/my fav/fav.new' '/opt/my fav/fav'"},
		{shell.PowerShell, "C:/Tools/fav.exe", "New-Item -ItemType Directory -Force C:/Tools | Out-Null",
			"if (Test-Path C:/Tools/fav.exe) { Move-Item -Force C:/Tools/fav.exe C:/Tools/fav.exe.old }; Move-Item -Force C:/Tools/fav.exe.new C:/Tools/fav.exe"},
		{shell.Cmd, "C:/Tools/fav.exe", `if not exist C:\Tools mkdir C:\Tools`,
			`(if exist C:\Tools\fav.exe move /y C:\Tools\fav.exe C:\Tools\fav.exe.old >nul) & move /y C:\Tools\fav.exe.new C:\Tools\fav.exe`},
	} {
		if got := prepLine(c.k, c.dest); got != c.prep {
			t.Errorf("prep %s: %s", c.dest, got)
		}
		if got := swapLine(c.k, c.dest); got != c.swap {
			t.Errorf("swap %s: %s", c.dest, got)
		}
	}
	if remoteKind(fav.Host{}, "windows") != shell.Cmd || remoteKind(fav.Host{Shell: "powershell"}, "windows") != shell.PowerShell {
		t.Error("a Windows host without a shell setting runs cmd")
	}
}

func TestPushBinCommands(t *testing.T) {
	b := buildCmd("/src/fav", "windows", "amd64", "v1.2.3", "/tmp/x/fav")
	if want := []string{"go", "build", "-trimpath", "-ldflags", "-s -w -X main.version=v1.2.3", "-o", "/tmp/x/fav", "./cmd/fav"}; !slices.Equal(b.Args, want) {
		t.Errorf("build: %q", b.Args)
	}
	if b.Dir != "/src/fav" {
		t.Errorf("build dir: %s", b.Dir)
	}
	for _, e := range []string{"GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0"} {
		if !slices.Contains(b.Env, e) {
			t.Errorf("build env lacks %s", e)
		}
	}
	cp := scpCmd("lg-win", "/tmp/x/fav", "C:/Users/Administrator/.local/bin/fav.exe")
	if want := []string{"scp", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", "/tmp/x/fav", "lg-win:C:/Users/Administrator/.local/bin/fav.exe"}; !slices.Equal(cp.Args, want) {
		t.Errorf("scp: %q", cp.Args)
	}
}

func TestFavSourceRefusesOtherTrees(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	if got, err := favSource(root); err != nil || got != root {
		t.Errorf("this checkout: %q %v", got, err)
	}
	other := t.TempDir()
	if _, err := favSource(other); err == nil {
		t.Error("a directory without go.mod")
	}
	os.WriteFile(filepath.Join(other, "go.mod"), []byte("module example.com/fav\n\ngo 1.27\n"), 0o644)
	if _, err := favSource(other); err == nil {
		t.Error("another module")
	}
}
