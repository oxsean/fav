// Package fzf drives one fzf session from fav subcommands; it never touches data files.
package fzf

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/x/term"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
)

// sub-picker results go to a file that transform-query reads back
const pickFileEnv = "FAV_PICK_FILE"

// The three tabs are three candidate sources in one fzf process; the current tab lives only in the prompt, which fzf passes to children as FZF_PROMPT.
type Tab int

const (
	TabFavorites Tab = iota
	TabSessions
	TabLive
)

var tabNames = [...]string{"favorites", "sessions", "live"}

func TabByName(name string) Tab {
	for i, n := range tabNames {
		if n == name {
			return Tab(i)
		}
	}
	return TabFavorites
}

func (t Tab) Name() string { return tabNames[t] }

func (t Tab) label() string {
	switch t {
	case TabSessions:
		return i18n.T("view.sessions")
	case TabLive:
		return "Agents"
	}
	return i18n.T("view.favorites")
}

func (t Tab) prompt() string { return t.label() + " > " }

func TabOf(prompt string) Tab {
	for _, t := range []Tab{TabSessions, TabLive} {
		if strings.HasPrefix(prompt, t.label()) {
			return t
		}
	}
	return TabFavorites
}

// version gates: transform + FZF_PROMPT 0.46; --footer 0.63; --id-nth 0.71; every(N) 0.73
var version = sync.OnceValues(func() (major, minor int) {
	out, err := exec.Command("fzf", "--version").Output()
	if err != nil {
		return 0, 0
	}
	parts := strings.SplitN(strings.Fields(string(out))[0], ".", 3)
	if len(parts) < 2 {
		return 0, 0
	}
	major, _ = strconv.Atoi(parts[0])
	minor, _ = strconv.Atoi(parts[1])
	return major, minor
})

func atLeast(major, minor, wantMinor int) bool { return major > 0 || minor >= wantMinor }

func Run(initialQuery string, start Tab) (string, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return "", errors.New(i18n.T("fzf.missing"))
	}
	major, minor := version()
	if !atLeast(major, minor, 46) {
		return "", i18n.E("fzf.too_old", strconv.Itoa(major)+"."+strconv.Itoa(minor))
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	q := shellQuote(self)

	pickFile := filepath.Join(os.TempDir(), "fav-pick-"+strconv.Itoa(os.Getpid()))
	defer os.Remove(pickFile)

	// fzf shell-escapes {q}
	reload := reloadAction(q)
	pick := func(kind string) string {
		return "execute(" + q + " fzf-pick " + kind + " {q} > " + shellQuote(pickFile) + ")" +
			"+transform-query(cat " + shellQuote(pickFile) + " 2>/dev/null)" +
			"+" + reload
	}
	// the reload after an action passes --keep {1}: the row stays until the next reload (typing, tab switch) so the action can be undone
	act := func(sub string) string {
		return "execute-silent(" + q + " " + sub + ")+reload(" + q + " fzf-list --keep {1} {q})"
	}
	tab := func(name string) string { return "transform(" + q + " fzf-tab " + name + ")" }

	head, foot := hints(start)
	args := []string{
		"--ansi", "--reverse",
		"--delimiter", render.Sep,
		"--with-nth", "2",
		"--preview", q + " preview {1}",
		"--preview-window", "right:55%:wrap",
		"--prompt", start.prompt(),
		"--header", head,
		"--info", "inline-right",
		"--disabled", // filtering is all done by fav
		"--query", initialQuery,
		"--bind", "start:" + reload,
		"--bind", "change:" + reload,
		"--bind", "ctrl-l:" + reload,
		"--bind", "f1,alt-1:" + tab("favorites"), // Ctrl-digit does not exist in terminals and Alt-digit is taken by Herdr
		"--bind", "f2,alt-2:" + tab("sessions"),
		"--bind", "f3,alt-3:" + tab("live"),
		"--bind", "tab:" + tab("next"), // no --multi, so Tab is free
		"--bind", "btab:" + tab("prev"),
		// Ctrl-letter = the TUI's letter (acts on the current row), Alt-letter = filter pickers; fzf's own ctrl-p / ctrl-u / ctrl-d stay free
		"--bind", "alt-t:" + pick("tags"),
		"--bind", "alt-p:" + pick("projects"),
		"--bind", "alt-s,ctrl-s:" + pick("status"),
		"--bind", "alt-d:" + pick("date"),
		"--bind", "ctrl-x:" + act("fzf-pick toggledone {1}"),
		"--bind", "ctrl-a:" + act("fzf-pick togglearchive {1}"),
		"--bind", "alt-f:" + act("fzf-pick togglefav {1}"), // Ctrl-F pages in the TUI and moves the cursor in fzf
		"--bind", "ctrl-e:execute(" + q + " edit {1})+" + reload,
		"--bind", "ctrl-y:execute-silent(" + q + " fzf-pick copy {1})",
		"--bind", "alt-enter:become(" + q + " resume --no-herdr {1})",
	}
	if foot != "" {
		args = append(args, "--footer", foot)
	}
	if atLeast(major, minor, 71) {
		args = append(args, "--track", "--id-nth", "1") // keeps the cursor on the same row across the Agents 3 s reloads
	}
	if atLeast(major, minor, 73) {
		args = append(args, "--bind", "every(3):"+tab("tick"))
	}

	cmd := exec.Command("fzf", args...)
	// CI=1 stops termenv probing the terminal (bubbletea's init does, and inside execute() the reply lands in the y/N prompt);
	// main drops CI again when FAV_IN_FZF is set so the exec'd claude / codex / $EDITOR never see it.
	cmd.Env = append(os.Environ(), pickFileEnv+"="+pickFile, "CI=1", "FAV_IN_FZF=1")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && (ee.ExitCode() == 130 || ee.ExitCode() == 1) {
			return "", nil
		}
		return "", err
	}
	id, _, _ := strings.Cut(strings.TrimSpace(string(out)), render.Sep)
	return id, nil
}

func reloadAction(q string) string { return "reload(" + q + " fzf-list {q})" }

// Switch is the action string `fav fzf-tab` prints for fzf transform: prompt, header, footer, reload.
// Arguments are wrapped in ^ because the texts contain ( ) [ ] and newlines.
func Switch(t Tab) string {
	self, _ := os.Executable()
	head, foot := hints(t)
	s := "change-prompt(" + t.prompt() + ")+change-header^" + head + "^"
	if foot != "" {
		s += "+change-footer^" + foot + "^"
	}
	return s + "+" + reloadAction(shellQuote(self))
}

// Tick handles every(3): only the Agents tab refreshes on the timer.
func Tick(cur Tab) string {
	if cur != TabLive {
		return ""
	}
	self, _ := os.Executable()
	return reloadAction(shellQuote(self))
}

func HasTimer() bool {
	major, minor := version()
	return atLeast(major, minor, 73)
}

// header / footer occupy the 45% list column and fzf does not wrap them, so rows are packed here: keys word by word,
// text by display width. Keys go to the footer (fzf 0.63+), text to the header; without a footer both go to the header.
func hints(t Tab) (header, footer string) {
	cols, _ := strconv.Atoi(os.Getenv("FZF_COLUMNS")) // no TTY inside transform children; fzf passes the width in FZF_COLUMNS
	if cols == 0 {
		cols, _, _ = term.GetSize(os.Stderr.Fd())
	}
	if cols < 40 {
		cols = 80
	}
	width := cols*45/100 - 3
	var keys, text string
	switch t {
	case TabSessions:
		keys = i18n.T("fzf.keys_sessions")
		text = i18n.T("fzf.header_sessions") + "\n" + i18n.T("fzf.header_query")
	case TabLive:
		keys = i18n.T("fzf.keys_live")
		text = i18n.T("fzf.header_live")
		if !HasTimer() {
			text = i18n.T("fzf.header_live_manual")
		}
	default:
		keys = i18n.T("fzf.keys_favorites")
		text = i18n.T("fzf.header_favorites") + "\n" + i18n.T("fzf.header_query")
	}
	rows := packKeys(i18n.T("fzf.keys_tabs")+"  "+keys, width)
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		lines = append(lines, render.Wrap(para, width)...)
	}
	major, minor := version()
	if atLeast(major, minor, 63) {
		return strings.Join(lines, "\n"), strings.Join(rows, "\n")
	}
	return strings.Join(append(rows, lines...), "\n"), ""
}

func packKeys(spec string, width int) []string {
	var out []string
	cur := ""
	for _, k := range strings.Split(spec, "  ") {
		if cur != "" && render.Width(cur)+2+render.Width(k) > width {
			out = append(out, cur)
			cur = ""
		}
		if cur != "" {
			cur += "  "
		}
		cur += k
	}
	return append(out, cur)
}

func Pick(prompt string, items []string, multi bool) []string {
	if len(items) == 0 {
		return nil
	}
	args := []string{"--reverse", "--prompt", prompt + " > ", "--height", "60%", "--border"}
	if multi {
		args = append(args, "--multi", "--header", i18n.T("fzf.pick_header"))
	}
	cmd := exec.Command("fzf", args...)
	cmd.Stdin = strings.NewReader(strings.Join(items, "\n"))
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var sel []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			sel = append(sel, l)
		}
	}
	return sel
}

// shellQuote is for --bind strings, which a shell executes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
