package main

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/shell"
)

const favModule = "github.com/oxsean/fav"

// cmdHostsInstall cross-compiles fav from a source tree for a host and puts it where the host's config runs it — beside
// it and renamed in, inside the WSL distro or the container when fav runs there — then asks the host which fav
// answers. Only ever run by hand.
func cmdHostsInstall(args []string) error {
	fs := newFlags("hosts install")
	goos := fs.String("os", "", i18n.T("cli.install.flag_os"))
	goarch := fs.String("arch", "", i18n.T("cli.install.flag_arch"))
	src := fs.String("src", "", i18n.T("cli.install.flag_src"))
	dry := fs.Bool("dry-run", false, i18n.T("cli.install.flag_dry_run"))
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New(i18n.T("cli.install.usage"))
	}
	cfg := loadConfig()
	i := hostIndex(cfg.Hosts, pos[0])
	if i < 0 {
		return i18n.E("cli.hosts.unknown", pos[0], strings.Join(hostNames(cfg.Hosts), " "))
	}
	h := cfg.Hosts[i]
	if h.SSH == "" {
		return i18n.E("cli.install.no_ssh", h.Name)
	}
	t, ok := installTarget(h)
	if !ok {
		return i18n.E("cli.install.wrapped", h.Name, remote.RemoteShell(h).Join(h.Fav))
	}
	if t.via != "" {
		*goos = cmp.Or(*goos, "linux")
	}
	if *goos == "" || *goarch == "" {
		hello, err := askHello(h)
		if err != nil {
			return i18n.E("cli.install.no_hello", h.Name, reasonOf(err))
		}
		*goos, *goarch = cmp.Or(*goos, hello.OS), cmp.Or(*goarch, hello.Arch)
	}
	root, err := favSource(*src)
	if err != nil {
		return err
	}
	dest := t.destFor(*goos)
	switch {
	case t.via == "wsl":
		fmt.Print(i18n.F("cli.install.dest_wsl", h.Name, dest))
	case t.via != "":
		fmt.Print(i18n.F("cli.install.dest_container", h.Name, dest, t.container))
	case t.dest != "":
		fmt.Print(i18n.F("cli.install.dest_config", h.Name, dest))
	default:
		fmt.Print(i18n.F("cli.install.dest_default", h.Name, dest))
	}

	tmp, err := os.MkdirTemp("", "fav-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "fav")
	ver := sourceVersion(root)
	build := buildCmd(root, *goos, *goarch, ver, out)
	steps, cleanup := t.steps(h, *goos, out)
	fmt.Print(i18n.F("cli.install.build", *goos, *goarch, root, shell.User().Join(build.Args)))
	var lines []string
	for _, c := range steps {
		lines = append(lines, "  "+shell.User().Join(c.Args))
	}
	fmt.Print(i18n.F("cli.install.steps", strings.Join(lines, "\n")))
	if *dry {
		return nil
	}
	if cleanup != nil {
		defer cleanup.Run()
	}
	for _, c := range append([]*exec.Cmd{build}, steps...) {
		c.Stdout, c.Stderr = os.Stderr, os.Stderr
		if err := c.Run(); err != nil {
			return i18n.E("cli.install.failed", c.Args[0], err)
		}
	}
	hello, err := askHello(h)
	if err != nil {
		return i18n.E("cli.install.unverified", h.Name, dest, reasonOf(err))
	}
	if hello.Version != ver || hello.OS != *goos || hello.Arch != *goarch {
		return i18n.E("cli.install.other_fav", h.Name, hello.Version, hello.OS+"/"+hello.Arch, dest, ver)
	}
	fmt.Print(i18n.F("cli.install.done", h.Name, dest, hello.Version))
	return nil
}

// target is where fav runs on a host and how a new binary gets there.
type target struct {
	via       string   // "" (the ssh login machine), "wsl", or the container runtime's name
	dest      string   // the binary's path where it runs; "" = .local/bin/fav[.exe] under the login directory
	wrapper   string   // wsl / docker as the config spells it
	pre       []string // the wrapper's own flags (distro, user)
	container string
}

// tmpName is where the binary waits in the login directory before it goes into a distro or a container.
const tmpName = "fav-install.tmp"

// installTarget reads where fav runs from h.Fav: a fav binary by absolute path, plain "fav" (or nothing) for the
// default, or `wsl [-d distro] -e <path>` / `docker|podman|nerdctl exec [flags] <container> <path>`. Anything else (a
// script, a relative path) is not something install knows how to replace.
func installTarget(h fav.Host) (target, bool) {
	switch {
	case len(h.Fav) == 0:
		return target{}, true
	case len(h.Fav) == 1:
		p := h.Fav[0]
		switch name := strings.ToLower(pathmap.Base(p)); {
		case name != "fav" && name != "fav.exe":
			return target{}, false
		case pathmap.Abs(p):
			return target{dest: strings.ReplaceAll(p, `\`, "/")}, true
		case strings.ContainsAny(p, `/\`): // relative to whatever directory ssh starts in
			return target{}, false
		}
		return target{}, true
	}
	switch base := strings.TrimSuffix(strings.ToLower(pathmap.Base(h.Fav[0])), ".exe"); base {
	case "wsl":
		return wslTarget(h.Fav)
	case "docker", "podman", "nerdctl":
		return containerTarget(base, h.Fav)
	}
	return target{}, false
}

func linuxFav(p string) bool { return strings.HasPrefix(p, "/") && path.Base(p) == "fav" }

func wslTarget(argv []string) (target, bool) {
	t := target{via: "wsl", wrapper: argv[0]}
	for i := 1; i < len(argv); i++ {
		switch a := argv[i]; {
		case a == "-e" || a == "--exec" || a == "--" || !strings.HasPrefix(a, "-"):
			cmd := argv[i:]
			if strings.HasPrefix(a, "-") {
				cmd = argv[i+1:]
			}
			if len(cmd) != 1 || !linuxFav(cmd[0]) {
				return target{}, false
			}
			t.dest = cmd[0]
			return t, true
		case a == "-d" || a == "--distribution" || a == "-u" || a == "--user":
			if i+1 >= len(argv) {
				return target{}, false
			}
			t.pre = append(t.pre, a, argv[i+1])
			i++
		default:
			t.pre = append(t.pre, a)
		}
	}
	return target{}, false
}

// containerValueFlags take the next argument as their value.
var containerValueFlags = []string{"-u", "--user", "-w", "--workdir", "-e", "--env", "--env-file"}

func containerTarget(runtime string, argv []string) (target, bool) {
	if len(argv) < 4 || argv[1] != "exec" {
		return target{}, false
	}
	t := target{via: runtime, wrapper: argv[0]}
	for i := 2; i < len(argv); i++ {
		a := argv[i]
		switch {
		case !strings.HasPrefix(a, "-"):
			if len(argv) != i+2 || !linuxFav(argv[i+1]) {
				return target{}, false
			}
			t.container, t.dest = a, argv[i+1]
			return t, true
		case slices.Contains(containerValueFlags, a):
			if i+1 >= len(argv) {
				return target{}, false
			}
			t.pre = append(t.pre, a, argv[i+1])
			i++
		case strings.HasPrefix(a, "--") || !strings.ContainsAny(a[1:], "it"): // keep flags but -i / -t: nothing here reads a terminal
			t.pre = append(t.pre, a)
		}
	}
	return target{}, false
}

func (t target) destFor(goos string) string {
	switch {
	case t.dest != "":
		return t.dest
	case goos == "windows":
		return ".local/bin/fav.exe"
	}
	return ".local/bin/fav"
}

// steps copy local to the host and swap it in: a rename, so a cut-off copy never replaces a working fav. cleanup
// removes the waiting copy from the login directory (nil when there is none).
func (t target) steps(h fav.Host, goos, local string) (steps []*exec.Cmd, cleanup *exec.Cmd) {
	dest := t.destFor(goos)
	if t.via == "" {
		k := remoteKind(h, goos)
		return []*exec.Cmd{sshCmd(h.SSH, prepLine(k, dest)), scpCmd(h.SSH, local, dest+".new"), sshCmd(h.SSH, swapLine(k, dest))}, nil
	}
	sh := shell.POSIX
	swap := sh.Join([]string{"chmod", "755", dest + ".new"}) + " && " + sh.Join([]string{"mv", "-f", dest + ".new", dest})
	var line string
	k := remote.RemoteShell(h)
	if t.via == "wsl" { // wsl starts in the Windows directory ssh logged in to, where the copy waits
		if k == shell.POSIX {
			k = shell.Cmd
		}
		script := sh.Join([]string{"mkdir", "-p", path.Dir(dest)}) + " && " + sh.Join([]string{"cp", tmpName, dest + ".new"}) + " && " + swap
		line = k.Join(append(append([]string{t.wrapper}, t.pre...), "-e", "sh", "-c", script))
	} else {
		inCtr := func(args ...string) []string {
			return append(append(append([]string{t.wrapper, "exec"}, t.pre...), t.container), args...)
		}
		line = andThen(k,
			k.Join(inCtr("mkdir", "-p", path.Dir(dest))),
			k.Join([]string{t.wrapper, "cp", tmpName, t.container + ":" + dest + ".new"}),
			k.Join(inCtr("sh", "-c", swap)))
	}
	return []*exec.Cmd{scpCmd(h.SSH, local, tmpName), sshCmd(h.SSH, line)}, sshCmd(h.SSH, removeLine(k, tmpName))
}

// andThen runs lines in turn in k's shell, stopping at the first that fails.
func andThen(k shell.Kind, lines ...string) string {
	if k == shell.PowerShell { // Windows PowerShell 5 has no &&
		return strings.Join(lines, "; if (-not $?) { exit 1 }; ")
	}
	return strings.Join(lines, " && ")
}

func removeLine(k shell.Kind, name string) string {
	switch k {
	case shell.Cmd:
		return "del /q " + k.Quote(name)
	case shell.PowerShell:
		return "Remove-Item -Force " + k.Quote(name)
	}
	return k.Join([]string{"rm", "-f", name})
}

// remoteKind is the shell ssh starts on the host: OpenSSH on Windows starts cmd unless the config says otherwise.
func remoteKind(h fav.Host, goos string) shell.Kind {
	k := remote.RemoteShell(h)
	if goos == "windows" && k == shell.POSIX {
		return shell.Cmd
	}
	return k
}

// prepLine makes dest's directory on the host.
func prepLine(k shell.Kind, dest string) string {
	switch k {
	case shell.Cmd:
		dir := strings.ReplaceAll(path.Dir(dest), "/", `\`)
		return "if not exist " + k.Quote(dir) + " mkdir " + k.Quote(dir)
	case shell.PowerShell:
		return "New-Item -ItemType Directory -Force " + k.Quote(path.Dir(dest)) + " | Out-Null"
	}
	return k.Join([]string{"mkdir", "-p", path.Dir(dest)})
}

// swapLine puts dest.new in dest's place. Windows cannot replace a running .exe but can rename it, so the old one moves
// aside to dest.old first.
func swapLine(k shell.Kind, dest string) string {
	switch k {
	case shell.Cmd:
		w := strings.ReplaceAll(dest, "/", `\`)
		return "(if exist " + k.Quote(w) + " move /y " + k.Quote(w) + " " + k.Quote(w+".old") + " >nul) & move /y " + k.Quote(w+".new") + " " + k.Quote(w)
	case shell.PowerShell:
		return "if (Test-Path " + k.Quote(dest) + ") { Move-Item -Force " + k.Quote(dest) + " " + k.Quote(dest+".old") + " }; Move-Item -Force " + k.Quote(dest+".new") + " " + k.Quote(dest)
	}
	return k.Join([]string{"mv", "-f", dest + ".new", dest})
}

func askHello(h fav.Host) (remote.Hello, error) {
	c, err := remote.Dial(h)
	if err != nil {
		return remote.Hello{}, err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var hello remote.Hello
	return hello, c.Call(ctx, remote.MHello, nil, &hello)
}

// favSource is the fav checkout to build: dir, else the git toplevel of the current directory.
func favSource(dir string) (string, error) {
	if dir == "" {
		out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return "", errors.New(i18n.T("cli.install.no_checkout"))
		}
		dir = strings.TrimSpace(string(out))
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	f, err := os.Open(filepath.Join(dir, "go.mod"))
	if err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if fields := strings.Fields(sc.Text()); len(fields) == 2 && fields[0] == "module" {
				if fields[1] == favModule {
					return dir, nil
				}
				break
			}
		}
	}
	return "", i18n.E("cli.install.not_fav", dir)
}

func sourceVersion(root string) string {
	out, err := exec.Command("git", "-C", root, "describe", "--tags", "--always", "--dirty").Output()
	if v := strings.TrimSpace(string(out)); err == nil && v != "" {
		return v
	}
	return "dev"
}

// buildCmd builds like the release: static, trimmed paths, stripped, the version stamped in.
func buildCmd(root, goos, goarch, ver, out string) *exec.Cmd {
	c := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+ver, "-o", out, "./cmd/fav")
	c.Dir = root
	c.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	return c
}

func sshCmd(ssh, line string) *exec.Cmd {
	return exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", ssh, line)
}

func scpCmd(ssh, local, dest string) *exec.Cmd {
	return exec.Command("scp", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", local, ssh+":"+dest)
}
