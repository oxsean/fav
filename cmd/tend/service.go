package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/tend"
)

// serviceName names the node's login service: launchd label, systemd unit, scheduled task.
const serviceName = "dev.tend.node"

// nodeService is the login service that keeps `tend node --connect` running: what it writes and what it runs.
type nodeService struct {
	files   []serviceFile
	install []serviceStep // run after the files are written
	remove  [][]string    // run before the files are removed
	note    string        // what the user should know after installing
}

type serviceFile struct {
	path, content string
	utf16         bool
}

type serviceStep struct {
	argv    []string
	mayFail bool   // stops what may not be running
	refused string // i18n key said with the error when this step fails
}

// serviceHost is what a service depends on from this machine.
type serviceHost struct {
	home    string // where launchd and systemd look for the file
	nodeDir string // <tend home>/node: the log, and on Windows the task's files
	user    string // the account a Windows task runs as
	now     time.Time
	env     [][2]string
}

// cmdNodeService is `tend node install-service` / `uninstall-service`: this machine's node starts at login and keeps
// dialing the server.
func cmdNodeService(verb string, args []string) error {
	fs := newFlags("node")
	url := fs.String("connect", "", i18n.T("cli.node.flag_connect"))
	tokenFile := fs.String("token-file", "", i18n.T("cli.node.flag_token_file"))
	dryRun := fs.Bool("print", false, i18n.T("cli.node.flag_print"))
	atBoot := fs.Bool("at-boot", false, i18n.T("cli.node.flag_at_boot"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	install := verb == "install-service"
	var argv []string
	if install {
		if *url == "" || *tokenFile == "" {
			return errors.New(i18n.T("cli.node.service_needs"))
		}
		if len(loadConfig().Node.AllowDirs) == 0 {
			return i18n.E("cli.node.need_allow_dirs", tend.ConfigPath())
		}
		self, err := os.Executable()
		if err != nil {
			return err
		}
		if self, err = filepath.EvalSymlinks(self); err != nil {
			return err
		}
		tf, err := filepath.Abs(*tokenFile)
		if err != nil {
			return err
		}
		argv = []string{self, "node", "--connect", *url, "--token-file", tf}
	}
	*atBoot = *atBoot && install
	host, err := thisHost(runtime.GOOS)
	if err != nil {
		return err
	}
	svc, err := serviceFor(runtime.GOOS, host, argv, *atBoot)
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Print(svc.dryRun(shell.User(), install))
		return nil
	}
	if !install {
		for _, c := range svc.remove {
			exec.Command(c[0], c[1:]...).Run() // ⚠️ it may not be loaded; removing the files is what counts
		}
		for _, f := range svc.files {
			if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		fmt.Print(i18n.T("cli.node.service_removed"))
		return nil
	}
	if err := os.MkdirAll(host.nodeDir, 0o700); err != nil {
		return err
	}
	for _, f := range svc.files {
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return err
		}
		if err := fileio.WriteFile(f.path, f.bytes(), 0o644); err != nil {
			return err
		}
	}
	if err := runSteps(svc.install, func(c []string) ([]byte, error) { return exec.Command(c[0], c[1:]...).CombinedOutput() }); err != nil {
		return err
	}
	logFile := filepath.Join(host.nodeDir, "service.log")
	if *atBoot {
		fmt.Print(i18n.F("cli.node.service_installed_boot", logFile))
	} else {
		fmt.Print(i18n.F("cli.node.service_installed", logFile))
	}
	if svc.note != "" {
		fmt.Println(svc.note)
	}
	return nil
}

// dryRun is what --print shows: the files with their contents and the commands, or what removing it runs and deletes.
func (svc nodeService) dryRun(k shell.Kind, install bool) string {
	var b strings.Builder
	if !install {
		for _, c := range svc.remove {
			b.WriteString(k.Join(c) + "\n")
		}
		for _, f := range svc.files {
			b.WriteString(k.Join([]string{"rm", f.path}) + "\n")
		}
		return b.String()
	}
	for _, f := range svc.files {
		b.WriteString(i18n.F("cli.node.service_file", f.path) + f.content + "\n")
	}
	for _, s := range svc.install {
		b.WriteString(k.Join(s.argv) + "\n")
	}
	return b.String()
}

func runSteps(steps []serviceStep, run func([]string) ([]byte, error)) error {
	for _, s := range steps {
		out, err := run(s.argv)
		if err == nil || s.mayFail {
			continue
		}
		msg := fmt.Sprintf("%s: %v %s", shell.User().Join(s.argv), err, strings.TrimSpace(string(out)))
		if s.refused != "" {
			return errors.New(msg + "\n" + i18n.T(s.refused))
		}
		return errors.New(msg)
	}
	return nil
}

func thisHost(goos string) (serviceHost, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return serviceHost{}, err
	}
	h := serviceHost{home: home, nodeDir: filepath.Join(tend.Home(), "node"), now: time.Now()}
	h.env = [][2]string{{"PATH", os.Getenv("PATH")}} // ⚠️ a login service starts with a bare PATH: claude and codex live elsewhere
	if th := os.Getenv("TEND_HOME"); th != "" {
		h.env = append(h.env, [2]string{"TEND_HOME", th})
	}
	if goos == "windows" {
		u, err := user.Current()
		if err != nil {
			return serviceHost{}, err
		}
		h.user = u.Username
	}
	return h, nil
}

// serviceFor is the login service for goos running argv (nil argv: only what removing it needs); atBoot (Windows only)
// runs it whether or not the user is logged on.
func serviceFor(goos string, h serviceHost, argv []string, atBoot bool) (nodeService, error) {
	if atBoot && goos != "windows" {
		return nodeService{}, i18n.E("cli.node.service_boot_windows", goos)
	}
	logFile := filepath.Join(h.nodeDir, "service.log")
	switch goos {
	case "darwin":
		domain := "gui/" + strconv.Itoa(os.Getuid())
		file := filepath.Join(h.home, "Library", "LaunchAgents", serviceName+".plist")
		return nodeService{files: []serviceFile{{path: file, content: launchdPlist(argv, h.env, logFile)}},
			install: []serviceStep{{argv: []string{"launchctl", "bootout", domain + "/" + serviceName}, mayFail: true}, {argv: []string{"launchctl", "bootstrap", domain, file}}},
			remove:  [][]string{{"launchctl", "bootout", domain + "/" + serviceName}}}, nil
	case "linux":
		file := filepath.Join(h.home, ".config", "systemd", "user", "tend-node.service")
		return nodeService{files: []serviceFile{{path: file, content: systemdUnit(argv, h.env, logFile)}},
			install: []serviceStep{{argv: []string{"systemctl", "--user", "disable", "--now", "tend-node.service"}, mayFail: true},
				{argv: []string{"systemctl", "--user", "daemon-reload"}}, {argv: []string{"systemctl", "--user", "enable", "--now", "tend-node.service"}}},
			remove: [][]string{{"systemctl", "--user", "disable", "--now", "tend-node.service"}},
			note:   i18n.T("cli.node.service_linger")}, nil
	case "windows":
		envFile, task := filepath.Join(h.nodeDir, "service-env.json"), filepath.Join(h.nodeDir, "service.xml")
		var run []string
		if argv != nil {
			run = append(slices.Clone(argv), "--env", envFile, "--log", logFile)
		}
		svc := nodeService{
			files: []serviceFile{{path: envFile, content: envJSON(h.env)}, {path: task, content: taskXML(h.user, atBoot, h.now, run), utf16: true}},
			// ⚠️ /End before /Create: the instance it stops is the node itself; /Create /F replaces the task in place, so a
			// refused create leaves the one installed before
			install: []serviceStep{{argv: []string{"schtasks", "/End", "/TN", "tend-node"}, mayFail: true},
				{argv: []string{"schtasks", "/Create", "/F", "/TN", "tend-node", "/XML", task}}, {argv: []string{"schtasks", "/Run", "/TN", "tend-node"}}},
			remove: [][]string{{"schtasks", "/End", "/TN", "tend-node"}, {"schtasks", "/Delete", "/F", "/TN", "tend-node"}}}
		if atBoot {
			svc.install[1].refused = "cli.node.service_boot_refused"
			svc.note = i18n.T("cli.node.service_boot_note")
		}
		return svc, nil
	}
	return nodeService{}, i18n.E("cli.node.service_unsupported", goos)
}

// bytes is the file as written: schtasks reads a task definition as UTF-16 with a byte order mark.
func (f serviceFile) bytes() []byte {
	if !f.utf16 {
		return []byte(f.content)
	}
	b := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune(f.content)) {
		b = append(b, byte(u), byte(u>>8))
	}
	return b
}

// envJSON is the file `tend node --env` applies: a scheduled task carries no environment.
func envJSON(env [][2]string) string {
	m := map[string]string{}
	for _, kv := range env {
		m[kv[0]] = kv[1]
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(m)
	return b.String()
}

var xmlText = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// taskXML is the Task Scheduler definition running argv: at logon of account with its interactive token, or with atBoot
// also at boot as S4U (no stored password, so no DPAPI secrets such as Credential Manager). A time trigger from since,
// repeating each minute for ever, starts a node that exited again, and IgnoreNew skips it while the node runs:
// RestartOnFailure only retries a start that failed, and a boot or logon trigger's repetition begins only when it fires.
func taskXML(account string, atBoot bool, since time.Time, argv []string) string {
	logon, boot := "InteractiveToken", ""
	if atBoot {
		logon, boot = "S4U", "    <BootTrigger><Enabled>true</Enabled></BootTrigger>\n"
	}
	command, arguments := "", ""
	if len(argv) > 0 {
		command, arguments = argv[0], shell.Cmd.Join(argv[1:])
	}
	u := xmlText.Replace(account)
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>tend node</Description></RegistrationInfo>
  <Triggers>
` + boot + `    <LogonTrigger><Enabled>true</Enabled><UserId>` + u + `</UserId></LogonTrigger>
    <TimeTrigger><Repetition><Interval>PT1M</Interval><StopAtDurationEnd>false</StopAtDurationEnd></Repetition><StartBoundary>` +
		since.Format("2006-01-02T15:04:05") + `</StartBoundary><Enabled>true</Enabled></TimeTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author"><UserId>` + u + `</UserId><LogonType>` + logon + `</LogonType></Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>5</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec><Command>` + xmlText.Replace(command) + `</Command><Arguments>` + xmlText.Replace(arguments) + `</Arguments></Exec>
  </Actions>
</Task>
`
}

func launchdPlist(argv []string, env [][2]string, logFile string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + serviceName + `</string>
  <key>ProgramArguments</key>
  <array>
`)
	for _, a := range argv {
		b.WriteString("    <string>" + html.EscapeString(a) + "</string>\n")
	}
	b.WriteString("  </array>\n  <key>EnvironmentVariables</key>\n  <dict>\n")
	for _, kv := range env {
		b.WriteString("    <key>" + kv[0] + "</key><string>" + html.EscapeString(kv[1]) + "</string>\n")
	}
	b.WriteString(`  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>StandardOutPath</key><string>` + html.EscapeString(logFile) + `</string>
  <key>StandardErrorPath</key><string>` + html.EscapeString(logFile) + `</string>
</dict>
</plist>
`)
	return b.String()
}

func systemdUnit(argv []string, env [][2]string, logFile string) string {
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=tend node\nAfter=network-online.target\n\n[Service]\n")
	b.WriteString("ExecStart=" + strings.ReplaceAll(shell.POSIX.Join(argv), "%", "%%") + "\n")
	for _, kv := range env {
		b.WriteString("Environment=" + shell.POSIX.Quote(kv[0]+"="+strings.ReplaceAll(kv[1], "%", "%%")) + "\n")
	}
	b.WriteString("Restart=always\nRestartSec=10\n")
	b.WriteString("StandardOutput=append:" + logFile + "\nStandardError=append:" + logFile + "\n")
	b.WriteString("\n[Install]\nWantedBy=default.target\n")
	return b.String()
}
