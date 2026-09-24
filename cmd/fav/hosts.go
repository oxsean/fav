package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/shell"
)

// cmdHosts lists the configured hosts; its subcommands add, remove, probe, install fav on and forget them.
func cmdHosts(args []string) error {
	switch first(args) {
	case "check":
		return cmdHostsCheck(args[1:])
	case "add":
		return cmdHostsAdd(args[1:])
	case "rm", "remove":
		return cmdHostsRm(args[1:])
	case "install":
		return cmdHostsInstall(args[1:])
	case "clear":
		return cmdHostsClear(args[1:])
	}
	fs := newFlags("hosts")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg := loadConfig()
	if len(cfg.Hosts) == 0 {
		fmt.Print(i18n.F("cli.hosts.none", fav.ConfigPath()))
		return nil
	}
	hs := remote.NewHosts(cfg.Hosts, "")
	now := time.Now()
	rows := [][]string{{i18n.T("cli.hosts.col_host"), i18n.T("cli.hosts.col_ssh"), i18n.T("cli.hosts.col_version"), i18n.T("cli.hosts.col_fav"), i18n.T("cli.hosts.col_cached")}}
	for _, h := range cfg.Hosts {
		ssh := h.SSH
		if ssh == "" {
			ssh = i18n.T("cli.hosts.local_process")
		}
		cached, ver := i18n.T("cli.hosts.never_fetched"), "-"
		if recs, st := hs.Cached(h.Name); !st.At.IsZero() {
			cached, ver = i18n.F("cli.hosts.cached", render.When(st.At, now), len(recs)), cmp.Or(st.Version, "-")
		}
		rows = append(rows, []string{h.Name, ssh, ver, remote.RemoteShell(h).Join(favArgv(h)), cached})
	}
	printTable(rows, termWidth())
	return nil
}

func favArgv(h fav.Host) []string {
	if len(h.Fav) == 0 {
		return []string{"fav"}
	}
	return h.Fav
}

// printTable pads columns to their widest cell; the last column takes what is left of width.
func printTable(rows [][]string, width int) {
	var widths []int
	for _, r := range rows {
		for i, c := range r {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], min(render.Width(c), 40))
		}
	}
	for _, r := range rows {
		var b strings.Builder
		used := 0
		for i, c := range r {
			if i == len(r)-1 {
				b.WriteString(render.Truncate(c, max(width-used, 8)))
				break
			}
			b.WriteString(render.Pad(c, widths[i]) + "  ")
			used += widths[i] + 2
		}
		fmt.Println(strings.TrimRight(b.String(), " "))
	}
}

const echoProbe = "中文 ✓ \"q\" 'x' %PATH% $HOME"

// probe rows, top to bottom.
var probeRows = []string{"cli.hosts.row_connect", "cli.hosts.row_fav", "cli.hosts.row_os", "cli.hosts.row_endpoint",
	"cli.hosts.row_claude", "cli.hosts.row_codex", "cli.hosts.row_echo", "cli.hosts.row_list", "cli.hosts.row_page"}

type hostReport struct {
	name  string
	cells map[string]string // by probeRows key
	fails []string
}

func (r *hostReport) fail(row, reason string) {
	r.cells[row] = i18n.T("cli.hosts.fail")
	r.fails = append(r.fails, i18n.F("cli.hosts.fail_line", r.name, i18n.T(row), reason))
}

// reasonOf: the short reason, then the first line of what the other end said.
func reasonOf(err error) string {
	var e *remote.Error
	if errors.As(err, &e) && e.Detail != "" {
		detail, _, _ := strings.Cut(strings.TrimSpace(e.Detail), "\n")
		return remote.Reason(err) + " — " + detail
	}
	return remote.Reason(err)
}

func cmdHostsCheck(args []string) error {
	fs := newFlags("hosts")
	timeout := fs.Duration("timeout", 45*time.Second, i18n.T("cli.hosts.flag_timeout"))
	names, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	cfg := loadConfig()
	hosts := cfg.Hosts
	if len(names) > 0 {
		hosts = nil
		for _, n := range names {
			i := hostIndex(cfg.Hosts, n)
			if i < 0 {
				return i18n.E("cli.hosts.unknown", n, strings.Join(hostNames(cfg.Hosts), " "))
			}
			hosts = append(hosts, cfg.Hosts[i])
		}
	}
	if len(hosts) == 0 {
		fmt.Print(i18n.F("cli.hosts.none", fav.ConfigPath()))
		return nil
	}
	reports := checkHosts(hosts, remote.Dial, i18n.Resolve(cfg.Lang), *timeout)
	rows := [][]string{append([]string{""}, hostNames(hosts)...)}
	for _, row := range probeRows {
		line := []string{i18n.T(row)}
		for _, r := range reports {
			line = append(line, r.cells[row])
		}
		rows = append(rows, line)
	}
	printTable(rows, termWidth())
	failed := 0
	for _, r := range reports {
		if len(r.fails) > 0 {
			failed++
		}
	}
	if failed == 0 {
		fmt.Print(i18n.F("cli.hosts.passed", len(reports)))
		return nil
	}
	fmt.Println()
	for _, r := range reports {
		for _, f := range r.fails {
			fmt.Println(f)
		}
	}
	return i18n.E("cli.hosts.failed", failed, len(reports))
}

func hostNames(hosts []fav.Host) []string {
	out := make([]string, len(hosts))
	for i, h := range hosts {
		out[i] = h.Name
	}
	return out
}

// checkHosts probes every host at once, each within timeout.
func checkHosts(hosts []fav.Host, dial func(fav.Host) (*remote.Client, error), lang string, timeout time.Duration) []*hostReport {
	out := make([]*hostReport, len(hosts))
	var wg sync.WaitGroup
	for i, h := range hosts {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			out[i] = checkHost(ctx, h, dial, lang)
		})
	}
	wg.Wait()
	return out
}

// checkHost: dial + hello, the CLIs there, a quoting round trip, and how long the list and a 200-message page take.
func checkHost(ctx context.Context, h fav.Host, dial func(fav.Host) (*remote.Client, error), lang string) *hostReport {
	r := &hostReport{name: h.Name, cells: map[string]string{}}
	ok := i18n.T("cli.hosts.ok")
	start := time.Now()
	c, err := dial(h)
	if err != nil {
		r.fail("cli.hosts.row_connect", reasonOf(err))
		return r
	}
	defer c.Close()
	var hello remote.Hello
	if err := c.Call(ctx, remote.MHello, remote.HelloParams{Lang: lang}, &hello); err != nil {
		r.fail("cli.hosts.row_connect", reasonOf(err))
		return r
	}
	r.cells["cli.hosts.row_connect"] = ok + " " + since(start)
	r.cells["cli.hosts.row_fav"] = i18n.F("cli.hosts.version", hello.Version, hello.Proto)
	if hello.Proto != remote.Proto {
		r.fail("cli.hosts.row_fav", remote.Reason(&remote.Error{Code: remote.CodeProto}))
		return r
	}
	r.cells["cli.hosts.row_os"] = strings.TrimSpace(hello.OS + "/" + hello.Arch + " " + hello.WSL)
	r.cells["cli.hosts.row_endpoint"] = hello.Endpoint
	for row, cli := range map[string]string{"cli.hosts.row_claude": fav.ProviderClaude, "cli.hosts.row_codex": fav.ProviderCodex} {
		r.cells[row] = i18n.T("cli.hosts.cli_missing")
		if hello.CLIs[cli] {
			r.cells[row] = i18n.T("cli.hosts.cli_found")
		}
	}

	var back remote.Text
	switch err := c.Call(ctx, remote.MEcho, remote.Text{Text: echoProbe}, &back); {
	case err != nil:
		r.fail("cli.hosts.row_echo", reasonOf(err))
		return r
	case back.Text != echoProbe:
		r.fail("cli.hosts.row_echo", i18n.F("cli.hosts.echo_mismatch", back.Text))
	default:
		r.cells["cli.hosts.row_echo"] = ok
	}

	start = time.Now()
	var l remote.List
	if err := c.Call(ctx, remote.MList, nil, &l); err != nil {
		r.fail("cli.hosts.row_list", reasonOf(err))
		return r
	}
	r.cells["cli.hosts.row_list"] = i18n.F("cli.hosts.list", since(start), len(l.Sessions))
	if len(l.Sessions) == 0 {
		r.cells["cli.hosts.row_page"] = i18n.T("cli.hosts.no_sessions")
		return r
	}
	newest := slices.MaxFunc(l.Sessions, func(a, b remote.Session) int { return a.LastAt.Compare(b.LastAt) })
	start = time.Now()
	var page capture.Page
	if err := c.Call(ctx, remote.MMessages, remote.MessagesParams{Ref: remote.Ref{Provider: newest.Provider, SessionID: newest.SessionID}, Before: -1, N: 200}, &page); err != nil {
		r.fail("cli.hosts.row_page", reasonOf(err))
		return r
	}
	r.cells["cli.hosts.row_page"] = i18n.F("cli.hosts.page", since(start), len(page.Msgs))
	return r
}

func since(t time.Time) string {
	if d := time.Since(t); d < time.Millisecond {
		return d.Round(time.Microsecond).String()
	}
	return time.Since(t).Round(time.Millisecond).String()
}

// hostIndex is the configured host called name (any case), -1 when there is none.
func hostIndex(hosts []fav.Host, name string) int {
	return slices.IndexFunc(hosts, func(h fav.Host) bool { return strings.EqualFold(h.Name, name) })
}

var hostNameRule = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// cmdHostsAdd adds a machine to the config: `fav hosts add <name> <ssh alias>` with where fav runs there, then checks it.
func cmdHostsAdd(args []string) error {
	fs := newFlags("hosts add")
	favPath := fs.String("fav", "", i18n.T("cli.hosts.flag_fav"))
	wsl := fs.String("wsl", "", i18n.T("cli.hosts.flag_wsl"))
	container := fs.String("docker", "", i18n.T("cli.hosts.flag_docker"))
	runtime := fs.String("docker-cmd", "docker", i18n.T("cli.hosts.flag_docker_cmd"))
	sh := fs.String("shell", "", i18n.T("cli.hosts.flag_shell"))
	noCheck := fs.Bool("no-check", false, i18n.T("cli.hosts.flag_no_check"))
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return errors.New(i18n.T("cli.hosts.add_usage"))
	}
	h, err := newHost(pos[0], pos[1], *favPath, *wsl, *container, *runtime, *sh)
	if err != nil {
		return err
	}
	cfg := fav.LoadConfig()
	if hostIndex(cfg.Hosts, h.Name) >= 0 {
		return i18n.E("cli.hosts.exists", h.Name)
	}
	cfg.Hosts = append(cfg.Hosts, h)
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Print(i18n.F("cli.hosts.added", h.Name, remote.RemoteShell(h).Join(favArgv(h)), fav.ConfigPath()))
	if *noCheck {
		return nil
	}
	return cmdHostsCheck([]string{h.Name})
}

// newHost is a host entry from the add flags: fav's path there, run directly, in a WSL distro or in a container.
func newHost(name, ssh, favPath, wsl, container, runtime, sh string) (fav.Host, error) {
	switch {
	case !hostNameRule.MatchString(name) || strings.EqualFold(name, fav.HostAll) || strings.EqualFold(name, fav.HostLocal):
		return fav.Host{}, i18n.E("cli.hosts.bad_name", name)
	case wsl != "" && container != "":
		return fav.Host{}, errors.New(i18n.T("cli.hosts.wsl_or_docker"))
	case (wsl != "" || container != "") && !linuxFav(favPath):
		return fav.Host{}, errors.New(i18n.T("cli.hosts.need_linux_fav"))
	}
	if _, ok := shell.Named(sh); sh != "" && !ok {
		return fav.Host{}, i18n.E("cli.hosts.bad_shell", sh)
	}
	h := fav.Host{Name: name, SSH: ssh, Shell: sh}
	switch {
	case wsl != "":
		h.Fav = []string{"wsl", "-d", wsl, "-e", favPath}
	case container != "":
		h.Fav = []string{runtime, "exec", "-i", container, favPath}
	case favPath != "":
		h.Fav = []string{favPath}
	}
	return h, nil
}

// cmdHostsRm removes machines from the config and forgets their cached lists.
func cmdHostsRm(args []string) error {
	fs := newFlags("hosts rm")
	names, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return errors.New(i18n.T("cli.hosts.rm_usage"))
	}
	cfg := fav.LoadConfig()
	for _, n := range names {
		i := hostIndex(cfg.Hosts, n)
		if i < 0 {
			return i18n.E("cli.hosts.unknown", n, strings.Join(hostNames(cfg.Hosts), " "))
		}
		name := cfg.Hosts[i].Name
		cfg.Hosts = slices.Delete(cfg.Hosts, i, i+1)
		if err := remote.ForgetCache(name); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.hosts.removed", name))
	}
	return cfg.Save()
}

// cmdHostsClear forgets the cached lists of the named machines, or of every one.
func cmdHostsClear(args []string) error {
	fs := newFlags("hosts clear")
	names, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	cfg := loadConfig()
	if len(names) == 0 {
		names = hostNames(cfg.Hosts)
	}
	for _, n := range names {
		i := hostIndex(cfg.Hosts, n)
		if i < 0 {
			return i18n.E("cli.hosts.unknown", n, strings.Join(hostNames(cfg.Hosts), " "))
		}
		if err := remote.ForgetCache(cfg.Hosts[i].Name); err != nil {
			return err
		}
	}
	fmt.Print(i18n.F("cli.hosts.cleared", len(names)))
	return nil
}
