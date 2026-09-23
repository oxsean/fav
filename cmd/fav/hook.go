package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/i18n"
)

// hookEvents: the Claude Code hooks fav installs; the matcher narrows Notification to questions for the user.
var hookEvents = []struct{ event, matcher string }{
	{"Notification", "permission_prompt|elicitation_dialog|agent_needs_input"},
	{"PermissionRequest", ""},
	{"UserPromptSubmit", ""},
	{"Stop", ""},
}

const hookVerb = "hook-event"

func claudeSettings() string {
	if h := os.Getenv("CLAUDE_CONFIG_DIR"); h != "" {
		return filepath.Join(h, "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

// isFavHook: a hook command fav installed (any fav binary path).
func isFavHook(cmd string) bool {
	bin, ok := strings.CutSuffix(strings.TrimSpace(cmd), " "+hookVerb)
	return ok && strings.HasPrefix(filepath.Base(strings.Trim(bin, `"'`)), "fav")
}

func loadSettings(path string) (*object, []byte, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return newObject(), nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	v, err := parseOrdered(b)
	if err != nil {
		return nil, nil, i18n.E("cli.hook.bad_settings", path, err)
	}
	o, ok := v.(*object)
	if !ok {
		return nil, nil, i18n.E("cli.hook.bad_settings", path, errors.New("not an object"))
	}
	return o, b, nil
}

// saveSettings keeps a timestamped copy of the old file, then replaces it atomically.
func saveSettings(path string, o *object, old []byte) error {
	b, err := encodeOrdered(o)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if old != nil {
		if err := os.WriteFile(path+".bak-"+time.Now().Format("20060102-150405"), old, mode); err != nil {
			return err
		}
	} else if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".fav-tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func groupsOf(hooks *object, event string) []any {
	arr, _ := hooks.get(event).([]any)
	return arr
}

func hasFavHook(group any) bool {
	g, ok := group.(*object)
	if !ok {
		return false
	}
	hs, _ := g.get("hooks").([]any)
	for _, h := range hs {
		if ho, ok := h.(*object); ok {
			if c, _ := ho.get("command").(string); isFavHook(c) {
				return true
			}
		}
	}
	return false
}

func cmdInstallHook(args []string) error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if b, err := filepath.EvalSymlinks(bin); err == nil {
		bin = b
	}
	path := claudeSettings()
	o, old, err := loadSettings(path)
	if err != nil {
		return err
	}
	hooks, _ := o.get("hooks").(*object)
	if hooks == nil {
		hooks = newObject()
		o.set("hooks", hooks)
	}
	added := 0
	for _, e := range hookEvents {
		groups := groupsOf(hooks, e.event)
		have := false
		for _, g := range groups {
			have = have || hasFavHook(g)
		}
		if have {
			continue
		}
		h := newObject()
		h.set("type", "command")
		h.set("command", shellWord(bin)+" "+hookVerb)
		h.set("timeout", json.Number("5"))
		h.set("async", true)
		g := newObject()
		if e.matcher != "" {
			g.set("matcher", e.matcher)
		}
		g.set("hooks", []any{h})
		hooks.set(e.event, append(groups, g))
		added++
	}
	if added == 0 {
		fmt.Print(i18n.F("cli.hook.already", path))
		return nil
	}
	if err := saveSettings(path, o, old); err != nil {
		return err
	}
	fmt.Print(i18n.F("cli.hook.installed", added, path))
	return nil
}

func cmdUninstallHook(args []string) error {
	path := claudeSettings()
	o, old, err := loadSettings(path)
	if err != nil || old == nil {
		return err
	}
	hooks, _ := o.get("hooks").(*object)
	if hooks == nil {
		fmt.Print(i18n.T("cli.hook.none"))
		return nil
	}
	removed := 0
	for _, event := range append([]string(nil), hooks.keys...) {
		var keep []any
		for _, g := range groupsOf(hooks, event) {
			if hasFavHook(g) {
				removed++
				continue
			}
			keep = append(keep, g)
		}
		if len(keep) == 0 {
			hooks.del(event)
		} else {
			hooks.set(event, keep)
		}
	}
	if removed == 0 {
		fmt.Print(i18n.T("cli.hook.none"))
		return nil
	}
	if len(hooks.keys) == 0 {
		o.del("hooks")
	}
	if err := saveSettings(path, o, old); err != nil {
		return err
	}
	fmt.Print(i18n.F("cli.hook.removed", removed, path))
	return nil
}

// hookInstalled: every hook fav installs is in Claude's settings.
func hookInstalled() bool {
	o, old, err := loadSettings(claudeSettings())
	if err != nil || old == nil {
		return false
	}
	hooks, _ := o.get("hooks").(*object)
	if hooks == nil {
		return false
	}
	for _, e := range hookEvents {
		have := false
		for _, g := range groupsOf(hooks, e.event) {
			have = have || hasFavHook(g)
		}
		if !have {
			return false
		}
	}
	return true
}

// cmdHookEvent is what Claude Code runs: it records the event for the session and never fails the hook.
func cmdHookEvent(args []string) error {
	b, _ := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	var in struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
		Event          string `json:"hook_event_name"`
	}
	if json.Unmarshal(b, &in) == nil {
		capture.RecordHookEvent(in.SessionID, in.Event, in.TranscriptPath)
	}
	return nil
}

func shellWord(s string) string {
	if strings.ContainsAny(s, " \t'\"$`\\") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}
