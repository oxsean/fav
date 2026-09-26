package main

import (
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/tend"
)

// serviceName names the node's login service: launchd label, systemd unit, scheduled task.
const serviceName = "dev.tend.node"

// nodeService is the login service that keeps `tend node --connect` running: what it writes and what it runs.
type nodeService struct {
	file    string // the file it writes, "" when none
	content string
	install [][]string // commands run after the file is written
	remove  [][]string // commands run before the file is removed
	note    string     // what the user should know after installing
}

// cmdNodeService is `tend node install-service` / `uninstall-service`: this machine's node starts at login and keeps
// dialing the server.
func cmdNodeService(verb string, args []string) error {
	fs := newFlags("node")
	url := fs.String("connect", "", i18n.T("cli.node.flag_connect"))
	tokenFile := fs.String("token-file", "", i18n.T("cli.node.flag_token_file"))
	dryRun := fs.Bool("print", false, i18n.T("cli.node.flag_print"))
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
	svc, err := serviceFor(runtime.GOOS, argv)
	if err != nil {
		return err
	}
	if *dryRun {
		cmds := svc.remove
		if install {
			cmds = svc.install
			if svc.file != "" {
				fmt.Print(i18n.F("cli.node.service_file", svc.file))
				fmt.Println(svc.content)
			}
		}
		for _, c := range cmds {
			fmt.Println(shell.User().Join(c))
		}
		if !install && svc.file != "" {
			fmt.Println(shell.User().Join([]string{"rm", svc.file}))
		}
		return nil
	}
	if !install {
		for _, c := range svc.remove {
			exec.Command(c[0], c[1:]...).Run() // ⚠️ it may not be loaded; removing the file is what counts
		}
		if svc.file != "" {
			if err := os.Remove(svc.file); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		fmt.Print(i18n.T("cli.node.service_removed"))
		return nil
	}
	if err := os.MkdirAll(filepath.Join(tend.Home(), "node"), 0o700); err != nil {
		return err
	}
	if svc.file != "" {
		if err := os.MkdirAll(filepath.Dir(svc.file), 0o755); err != nil {
			return err
		}
		if err := fileio.WriteFile(svc.file, []byte(svc.content), 0o644); err != nil {
			return err
		}
	}
	for _, c := range svc.remove {
		exec.Command(c[0], c[1:]...).Run() // a service installed before is replaced
	}
	for _, c := range svc.install {
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %v %s", shell.User().Join(c), err, strings.TrimSpace(string(out)))
		}
	}
	fmt.Print(i18n.F("cli.node.service_installed", filepath.Join(tend.Home(), "node", "service.log")))
	if svc.note != "" {
		fmt.Println(svc.note)
	}
	return nil
}

// serviceFor is the login service for goos running argv (nil argv: only what removing it needs).
func serviceFor(goos string, argv []string) (nodeService, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nodeService{}, err
	}
	logFile := filepath.Join(tend.Home(), "node", "service.log")
	env := [][2]string{{"PATH", os.Getenv("PATH")}} // ⚠️ a login service starts with a bare PATH: claude and codex live elsewhere
	if h := os.Getenv("TEND_HOME"); h != "" {
		env = append(env, [2]string{"TEND_HOME", h})
	}
	switch goos {
	case "darwin":
		domain := "gui/" + strconv.Itoa(os.Getuid())
		file := filepath.Join(home, "Library", "LaunchAgents", serviceName+".plist")
		return nodeService{file: file, content: launchdPlist(argv, env, logFile),
			install: [][]string{{"launchctl", "bootstrap", domain, file}},
			remove:  [][]string{{"launchctl", "bootout", domain + "/" + serviceName}}}, nil
	case "linux":
		file := filepath.Join(home, ".config", "systemd", "user", "tend-node.service")
		return nodeService{file: file, content: systemdUnit(argv, env, logFile),
			install: [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "--now", "tend-node.service"}},
			remove:  [][]string{{"systemctl", "--user", "disable", "--now", "tend-node.service"}},
			note:    i18n.T("cli.node.service_linger")}, nil
	case "windows":
		line := shell.Cmd.Join(argv)
		return nodeService{
			install: [][]string{{"schtasks", "/Create", "/F", "/SC", "ONLOGON", "/TN", "tend-node", "/TR", line}, {"schtasks", "/Run", "/TN", "tend-node"}},
			remove:  [][]string{{"schtasks", "/End", "/TN", "tend-node"}, {"schtasks", "/Delete", "/F", "/TN", "tend-node"}}}, nil
	}
	return nodeService{}, i18n.E("cli.node.service_unsupported", goos)
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
