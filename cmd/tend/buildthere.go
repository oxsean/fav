package main

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/tend"
)

// pushed is where a host fetches the source tree's commit from: a branch of one of its remotes that holds it.
type pushed struct {
	url, branch, commit string
	proxy               string // HTTPS_PROXY and HTTP_PROXY for the build's git and go; "" leaves the host's own
}

// pushedSource is the remote branch holding root's HEAD, the current branch's upstream first. The tree must be clean:
// the host builds the commit, not what is on this disk.
func pushedSource(root string) (pushed, error) {
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := git("status", "--porcelain", "--untracked-files=no"); err != nil || out != "" {
		return pushed{}, i18n.E("cli.install.dirty")
	}
	commit, err := git("rev-parse", "HEAD")
	if err != nil {
		return pushed{}, err
	}
	out, _ := git("branch", "-r", "--contains", commit, "--format=%(refname:short)")
	var refs []string
	for _, r := range strings.Fields(out) {
		if strings.Contains(r, "/") && !strings.HasSuffix(r, "/HEAD") {
			refs = append(refs, r)
		}
	}
	if up, err := git("rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
		if i := slices.Index(refs, up); i > 0 {
			refs[0], refs[i] = refs[i], refs[0]
		}
	}
	if len(refs) == 0 {
		return pushed{}, i18n.E("cli.install.unpushed", commit[:min(12, len(commit))])
	}
	name, branch, _ := strings.Cut(refs[0], "/")
	url, err := git("remote", "get-url", name)
	if err != nil {
		return pushed{}, err
	}
	return pushed{url: url, branch: branch, commit: commit}, nil
}

// built is one program the host builds: its package and where it goes.
type built struct{ pkg, dest string }

// posixBuild fetches p's commit into ~/.cache/tend-src, alone when the remote serves a commit by id, else with its
// branch, and builds each program into place, swapped in by a rename. A relative dest is under the directory the
// script starts in.
func posixBuild(p pushed, ver string, progs []built) string {
	q := shell.POSIX.Quote
	var b strings.Builder
	b.WriteString("set -e\n")
	b.WriteString("export PATH=\"$HOME/.local/bin:$PATH\" CGO_ENABLED=0\n")
	if p.proxy != "" {
		fmt.Fprintf(&b, "export HTTPS_PROXY=%[1]s HTTP_PROXY=%[1]s\n", q(p.proxy))
	}
	b.WriteString("here=$(pwd)\nd=\"$HOME/.cache/tend-src\"\n")
	b.WriteString("[ -d \"$d/.git\" ] || git init --quiet \"$d\"\n")
	fmt.Fprintf(&b, "git -C \"$d\" fetch --quiet --depth 1 %[1]s %[3]s || git -C \"$d\" fetch --quiet %[1]s %[2]s\n", q(p.url), q(p.branch), q(p.commit))
	fmt.Fprintf(&b, "git -C \"$d\" checkout --quiet --force --detach %s\n", q(p.commit))
	b.WriteString("cd \"$d\"\n")
	b.WriteString("if [ -n \"$(command -v mise)\" ]; then mise trust --quiet mise.toml || true; gobuild() { mise exec -- go build \"$@\"; }; else gobuild() { go build \"$@\"; }; fi\n")
	for _, x := range progs {
		fmt.Fprintf(&b, "out=%s; case \"$out\" in /*) ;; *) out=\"$here/$out\" ;; esac\n", q(x.dest))
		b.WriteString("mkdir -p \"$(dirname \"$out\")\"\n")
		fmt.Fprintf(&b, "gobuild -trimpath -ldflags %s -o \"$out.new\" %s\n", q("-s -w -X main.version="+ver), q(x.pkg))
		b.WriteString("chmod 755 \"$out.new\" && mv -f \"$out.new\" \"$out\"\n")
	}
	return b.String()
}

// windowsBuild is posixBuild for Windows PowerShell 5, into %LOCALAPPDATA%\tend-src, swapping as swapLine does.
func windowsBuild(p pushed, ver string, progs []built) string {
	q := psLiteral
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Stop'\n$env:CGO_ENABLED = '0'\n")
	if p.proxy != "" {
		fmt.Fprintf(&b, "$env:HTTPS_PROXY = %[1]s; $env:HTTP_PROXY = %[1]s\n", q(p.proxy))
	}
	b.WriteString("function Check($what) { if ($LASTEXITCODE) { throw \"$what failed\" } }\n")
	b.WriteString("$here = (Get-Location).Path\n$d = Join-Path $env:LOCALAPPDATA 'tend-src'\n")
	b.WriteString("if (-not (Test-Path (Join-Path $d '.git'))) { git init --quiet $d; Check 'git init' }\n")
	fmt.Fprintf(&b, "git -C $d fetch --quiet --depth 1 %[1]s %[3]s; if ($LASTEXITCODE) { git -C $d fetch --quiet %[1]s %[2]s; Check 'git fetch' }\n", q(p.url), q(p.branch), q(p.commit))
	fmt.Fprintf(&b, "git -C $d checkout --quiet --force --detach %s; Check 'git checkout'\n", q(p.commit))
	b.WriteString("Set-Location $d\n$mise = [bool](Get-Command mise -ErrorAction SilentlyContinue)\n")
	b.WriteString("if ($mise) { mise trust --quiet mise.toml 2>$null | Out-Null }\n")
	flags := q("-s -w -X main.version=" + ver)
	for _, x := range progs {
		fmt.Fprintf(&b, "$out = %s; if (-not [IO.Path]::IsPathRooted($out)) { $out = Join-Path $here $out }\n", q(x.dest))
		b.WriteString("New-Item -ItemType Directory -Force (Split-Path $out) | Out-Null\n")
		fmt.Fprintf(&b, "if ($mise) { mise exec -- go build -trimpath -ldflags %[1]s -o \"$out.new\" %[2]s } else { go build -trimpath -ldflags %[1]s -o \"$out.new\" %[2]s }; Check 'go build'\n", flags, q(x.pkg))
		b.WriteString(psSwap("$out") + "\n")
	}
	return b.String()
}

// psLiteral is s as a PowerShell string literal, which an assignment needs where an argument could go bare.
func psLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// buildName is the script's name in the host's login directory while it runs.
func buildName(goos string) string {
	if goos == "windows" {
		return "tend-build.ps1"
	}
	return "tend-build.sh"
}

// buildSteps copy script (a local file) to the host and run it where tend runs: on the login machine, in the WSL
// distro, or in the container. cleanup removes it from the login directory.
func (t target) buildSteps(h tend.Host, goos, script string) (steps []*exec.Cmd, cleanup *exec.Cmd) {
	name := buildName(goos)
	k := remoteKind(h, goos)
	var line string
	switch {
	case t.via == "" && goos == "windows":
		line = "powershell -NoProfile -ExecutionPolicy Bypass -File " + name
	case t.via == "":
		line = k.Join([]string{"sh", name})
	case t.via == "wsl": // wsl starts in the Windows directory ssh logged in to, where the script waits
		k = remote.RemoteShell(h)
		if k == shell.POSIX {
			k = shell.Cmd
		}
		line = k.Join(append(append([]string{t.wrapper}, t.pre...), "-e", "sh", name))
	default:
		k = remote.RemoteShell(h)
		in := path.Join("/tmp", name)
		line = andThen(k,
			k.Join([]string{t.wrapper, "cp", name, t.container + ":" + in}),
			k.Join(append(append(append([]string{t.wrapper, "exec"}, t.pre...), t.container), "sh", in)))
	}
	return []*exec.Cmd{scpCmd(h.SSH, script, name), sshCmd(h.SSH, line)}, sshCmd(h.SSH, removeLine(k, name))
}

// buildThere has the host build tend (and tend-server) from root's pushed commit, then asks it which tend answers.
func buildThere(h tend.Host, t target, root, goos, ver, tmp, proxy string, dry, withServer bool) error {
	p, err := pushedSource(root)
	if err != nil {
		return err
	}
	p.proxy = proxy
	dest := t.destFor(goos)
	progs := []built{{"./cmd/tend", dest}}
	serverDest := serverPath(dest, goos)
	if withServer {
		progs = append(progs, built{"./cmd/tend-server", serverDest})
	}
	body := posixBuild(p, ver, progs)
	if goos == "windows" && t.via == "" {
		body = windowsBuild(p, ver, progs)
	}
	script := filepath.Join(tmp, buildName(goos))
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		return err
	}
	steps, cleanup := t.buildSteps(h, goos, script)
	var lines []string
	for _, c := range steps {
		lines = append(lines, "  "+shell.User().Join(c.Args))
	}
	fmt.Print(i18n.F("cli.install.build_there", ver, h.Name, p.url, p.branch, p.commit[:min(12, len(p.commit))], strings.Join(lines, "\n")))
	if dry {
		fmt.Print(i18n.F("cli.install.script", buildName(goos), body))
		return nil
	}
	defer cleanup.Run()
	for _, c := range steps {
		c.Stdout, c.Stderr = os.Stderr, os.Stderr
		if err := c.Run(); err != nil {
			return i18n.E("cli.install.failed", c.Args[0], err)
		}
	}
	if err := checkInstalled(h, dest, goos, "", ver); err != nil {
		return err
	}
	if withServer {
		return checkServer(h, serverDest, ver)
	}
	return nil
}
