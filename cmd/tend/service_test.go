package main

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/shell"
)

var winHost = serviceHost{
	home:    filepath.Join("C:", "Users", "me"),
	nodeDir: filepath.Join("C:", "Users", "me", "tend home", "node"),
	user:    `BOX\me`,
	now:     time.Date(2026, 10, 6, 21, 4, 5, 0, time.FixedZone("UTC+8", 8*3600)),
	env:     [][2]string{{"PATH", `C:\Program Files\nodejs;"C:\Program Files\quoted";C:\odd%dir%;C:\中文`}, {"TEND_HOME", `C:\Users\me\tend home`}},
}

var winArgv = []string{`C:\Program Files\tend\tend.exe`, "node", "--connect", "ws://box:7788/a%20b", "--token-file", `C:\Users\me\tend home\node token`}

var (
	winEnvFile = filepath.Join(winHost.nodeDir, "service-env.json")
	winTask    = filepath.Join(winHost.nodeDir, "service.xml")
	winLog     = filepath.Join(winHost.nodeDir, "service.log")
)

type repetition struct {
	Interval          string
	Duration          *string
	StopAtDurationEnd bool
}

type taskDef struct {
	Triggers struct {
		Boot []struct {
			Enabled    bool
			Repetition *repetition
		} `xml:"BootTrigger"`
		Logon []struct {
			Enabled    bool
			UserID     string `xml:"UserId"`
			Repetition *repetition
		} `xml:"LogonTrigger"`
		Time []struct {
			Enabled       bool
			StartBoundary string
			Repetition    *repetition
		} `xml:"TimeTrigger"`
	}
	Principal struct {
		UserID    string `xml:"UserId"`
		LogonType string
	} `xml:"Principals>Principal"`
	Settings struct {
		MultipleInstancesPolicy    string
		DisallowStartIfOnBatteries bool
		StopIfGoingOnBatteries     bool
		StartWhenAvailable         bool
		ExecutionTimeLimit         string
		Priority                   int
		RestartOnFailure           *struct{}
	}
	Exec []struct {
		Command   string
		Arguments string
	} `xml:"Actions>Exec"`
}

func winService(t *testing.T, atBoot bool) (nodeService, taskDef) {
	t.Helper()
	svc, err := serviceFor("windows", winHost, winArgv, atBoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(svc.files) != 2 || svc.files[0].path != winEnvFile || svc.files[1].path != winTask || svc.files[0].utf16 || !svc.files[1].utf16 {
		t.Fatalf("files = %+v", svc.files)
	}
	var def taskDef
	if err := xml.Unmarshal([]byte(strings.Replace(svc.files[1].content, `encoding="UTF-16"`, "", 1)), &def); err != nil {
		t.Fatalf("task XML: %v\n%s", err, svc.files[1].content)
	}
	return svc, def
}

func TestWindowsServiceRunsTheNodeItself(t *testing.T) {
	for _, atBoot := range []bool{false, true} {
		_, def := winService(t, atBoot)
		if len(def.Exec) != 1 || def.Exec[0].Command != winArgv[0] {
			t.Fatalf("atBoot=%v exec = %+v", atBoot, def.Exec)
		}
		want := `node --connect "ws://box:7788/a%20b" --token-file "C:\Users\me\tend home\node token" --env ` + shell.Cmd.Quote(winEnvFile) + " --log " + shell.Cmd.Quote(winLog)
		if def.Exec[0].Arguments != want {
			t.Errorf("atBoot=%v arguments:\n%s\nwant\n%s", atBoot, def.Exec[0].Arguments, want)
		}
	}
}

func TestWindowsServiceEnvFileKeepsValuesExactly(t *testing.T) {
	svc, _ := winService(t, false)
	var env map[string]string
	if err := json.Unmarshal([]byte(svc.files[0].content), &env); err != nil {
		t.Fatalf("env file: %v\n%s", err, svc.files[0].content)
	}
	if !reflect.DeepEqual(env, map[string]string{"PATH": winHost.env[0][1], "TEND_HOME": winHost.env[1][1]}) {
		t.Errorf("env = %q", env)
	}
}

func TestWindowsServiceAtLogon(t *testing.T) {
	svc, def := winService(t, false)
	if def.Principal.UserID != `BOX\me` || def.Principal.LogonType != "InteractiveToken" {
		t.Errorf("principal = %+v", def.Principal)
	}
	if len(def.Triggers.Boot) != 0 || len(def.Triggers.Logon) != 1 || !def.Triggers.Logon[0].Enabled || def.Triggers.Logon[0].UserID != `BOX\me` {
		t.Errorf("triggers = %+v", def.Triggers)
	}
	checkTriggers(t, def)
	checkTaskSettings(t, def)
	want := []serviceStep{
		{argv: []string{"schtasks", "/End", "/TN", "tend-node"}, mayFail: true},
		{argv: []string{"schtasks", "/Create", "/F", "/TN", "tend-node", "/XML", winTask}},
		{argv: []string{"schtasks", "/Run", "/TN", "tend-node"}},
	}
	if !reflect.DeepEqual(svc.install, want) {
		t.Errorf("install = %+v", svc.install)
	}
	if svc.note != "" {
		t.Errorf("note = %q", svc.note)
	}
}

func TestWindowsServiceAtBoot(t *testing.T) {
	svc, def := winService(t, true)
	if def.Principal.UserID != `BOX\me` || def.Principal.LogonType != "S4U" {
		t.Errorf("principal = %+v", def.Principal)
	}
	if len(def.Triggers.Boot) != 1 || !def.Triggers.Boot[0].Enabled || len(def.Triggers.Logon) != 1 || def.Triggers.Logon[0].UserID != `BOX\me` {
		t.Fatalf("triggers = %+v", def.Triggers)
	}
	checkTriggers(t, def)
	checkTaskSettings(t, def)
	if svc.install[1].argv[1] != "/Create" || svc.install[1].refused != "cli.node.service_boot_refused" || svc.install[1].mayFail {
		t.Errorf("create step = %+v", svc.install[1])
	}
	if svc.note != i18n.T("cli.node.service_boot_note") {
		t.Errorf("note = %q", svc.note)
	}
}

// checkTriggers: boot and logon start the node at once; one time trigger from the install, repeating each minute for
// ever, is what starts an exited node again (a boot or logon trigger's repetition only begins when it fires), and
// IgnoreNew makes it a no-op while the node runs.
func checkTriggers(t *testing.T, def taskDef) {
	t.Helper()
	for _, b := range def.Triggers.Boot {
		if b.Repetition != nil {
			t.Errorf("boot trigger repeats: %+v", b.Repetition)
		}
	}
	for _, l := range def.Triggers.Logon {
		if l.Repetition != nil {
			t.Errorf("logon trigger repeats: %+v", l.Repetition)
		}
	}
	if len(def.Triggers.Time) != 1 {
		t.Fatalf("time triggers = %+v", def.Triggers.Time)
	}
	tt := def.Triggers.Time[0]
	if !tt.Enabled || tt.StartBoundary != "2026-10-06T21:04:05" {
		t.Errorf("time trigger = %+v", tt)
	}
	if r := tt.Repetition; r == nil || r.Interval != "PT1M" || r.Duration != nil || r.StopAtDurationEnd {
		t.Errorf("repetition = %+v", r)
	}
}

func checkTaskSettings(t *testing.T, def taskDef) {
	t.Helper()
	s := def.Settings
	if s.MultipleInstancesPolicy != "IgnoreNew" || s.DisallowStartIfOnBatteries || s.StopIfGoingOnBatteries || !s.StartWhenAvailable ||
		s.ExecutionTimeLimit != "PT0S" || s.Priority != 5 || s.RestartOnFailure != nil {
		t.Errorf("settings = %+v", s)
	}
}

func TestWindowsServiceRemovesEitherForm(t *testing.T) {
	svc, err := serviceFor("windows", winHost, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"schtasks", "/End", "/TN", "tend-node"}, {"schtasks", "/Delete", "/F", "/TN", "tend-node"}}
	if !reflect.DeepEqual(svc.remove, want) {
		t.Errorf("remove = %v", svc.remove)
	}
	var files []string
	for _, f := range svc.files {
		files = append(files, f.path)
	}
	if !slices.Equal(files, []string{winEnvFile, winTask}) {
		t.Errorf("files = %v", files)
	}
	for _, atBoot := range []bool{false, true} {
		installed, _ := winService(t, atBoot)
		for i, f := range installed.files {
			if f.path != files[i] {
				t.Errorf("atBoot=%v writes %s, uninstall removes %s", atBoot, f.path, files[i])
			}
		}
	}
}

func TestWindowsServicePrint(t *testing.T) {
	svc, _ := winService(t, true)
	want := i18n.F("cli.node.service_file", winEnvFile) + svc.files[0].content + "\n" +
		i18n.F("cli.node.service_file", winTask) + svc.files[1].content + "\n" +
		"schtasks /End /TN tend-node\n" +
		"schtasks /Create /F /TN tend-node /XML " + shell.Cmd.Quote(winTask) + "\n" +
		"schtasks /Run /TN tend-node\n"
	if got := svc.dryRun(shell.Cmd, true); got != want {
		t.Errorf("install:\n%s\nwant\n%s", got, want)
	}
	gone, _ := serviceFor("windows", winHost, nil, false)
	want = "schtasks /End /TN tend-node\n" +
		"schtasks /Delete /F /TN tend-node\n" +
		"rm " + shell.Cmd.Quote(winEnvFile) + "\n" +
		"rm " + shell.Cmd.Quote(winTask) + "\n"
	if got := gone.dryRun(shell.Cmd, false); got != want {
		t.Errorf("uninstall:\n%s\nwant\n%s", got, want)
	}
}

func TestAtBootOnlyOnWindows(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		if _, err := serviceFor(goos, winHost, winArgv, true); err == nil || err.Error() != i18n.F("cli.node.service_boot_windows", goos) {
			t.Errorf("%s: err = %v", goos, err)
		}
		if _, err := serviceFor(goos, winHost, winArgv, false); err != nil {
			t.Errorf("%s without --at-boot: %v", goos, err)
		}
	}
}

func TestServiceFileUTF16(t *testing.T) {
	b := serviceFile{content: "<a>中</a>", utf16: true}.bytes()
	if b[0] != 0xFF || b[1] != 0xFE || len(b)%2 != 0 {
		t.Fatalf("bytes = % x", b)
	}
	u := make([]uint16, (len(b)-2)/2)
	for i := range u {
		u[i] = uint16(b[2+2*i]) | uint16(b[3+2*i])<<8
	}
	if got := string(utf16.Decode(u)); got != "<a>中</a>" {
		t.Errorf("decoded %q", got)
	}
	if got := string(serviceFile{content: "x"}.bytes()); got != "x" {
		t.Errorf("plain = %q", got)
	}
}

func TestRunStepsRefusedBootTask(t *testing.T) {
	svc, _ := winService(t, true)
	var ran [][]string
	err := runSteps(svc.install, func(argv []string) ([]byte, error) {
		ran = append(ran, argv)
		if argv[1] == "/End" {
			return []byte("ERROR: The system cannot find the file specified."), errors.New("exit status 1")
		}
		return []byte("ERROR: Access is denied."), errors.New("exit status 1")
	})
	if err == nil || !strings.HasSuffix(err.Error(), i18n.T("cli.node.service_boot_refused")) || !strings.Contains(err.Error(), "Access is denied.") {
		t.Fatalf("err = %v", err)
	}
	if len(ran) != 2 {
		t.Errorf("ran %v around the refused create", ran)
	}
}

func TestRunStepsIgnoresWhatMayFail(t *testing.T) {
	svc, _ := winService(t, false)
	var ran int
	err := runSteps(svc.install, func(argv []string) ([]byte, error) {
		ran++
		if argv[1] == "/End" {
			return nil, errors.New("exit status 1")
		}
		return nil, nil
	})
	if err != nil || ran != 3 {
		t.Errorf("err = %v, ran %d", err, ran)
	}
	err = runSteps(svc.install, func(argv []string) ([]byte, error) { return []byte("bad"), errors.New("exit status 1") })
	if err == nil || strings.Contains(err.Error(), i18n.T("cli.node.service_boot_refused")) {
		t.Errorf("a failed logon task: %v", err)
	}
}
