package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/fav"
)

// fakeCLIs puts stub claude / codex executables first on PATH so the checks pass wherever the tests run.
func fakeCLIs(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestForkFromResumeDialog(t *testing.T) {
	fakeCLIs(t)
	m := sized(t, 140, 40)
	r := m.current()
	r.Cwd = t.TempDir() // no Herdr workspace here: the fork runs in this terminal
	r.TranscriptPath = filepath.Join(r.Cwd, "s.jsonl")
	os.WriteFile(r.TranscriptPath, []byte("{}\n"), 0o644)
	m.askResume()
	if v := ansi.Strip(m.View()); !strings.Contains(v, "b 分叉") || !strings.Contains(v, "s 交接") {
		t.Fatalf("the resume dialog offers fork and handoff:\n%s", v)
	}
	m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if m.quitting {
		t.Fatal("the first b only focuses the fork button")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if !m.quitting || m.result.Start == nil || m.result.Resume != nil {
		t.Fatalf("fork quits with a start command, not a resume: %+v", m.result)
	}
	var want []string
	if r.Provider == fav.ProviderClaude {
		want = []string{"claude", "--resume", r.SessionID, "--fork-session"}
	} else {
		want = []string{"codex", "fork", r.SessionID}
	}
	if got := m.result.Start.Argv(); !slices.Equal(got, want) || m.result.Start.Cwd != r.Cwd {
		t.Errorf("fork command %q in %s", got, m.result.Start.Cwd)
	}
}

func TestHandoffDialog(t *testing.T) {
	fakeCLIs(t)
	m := sized(t, 140, 40)
	r := m.current()
	r.Cwd = t.TempDir()
	m.askResume()
	m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	var got handoffMsg
	for msg := range drain(cmd) {
		if h, ok := msg.(handoffMsg); ok {
			got = h
		}
	}
	if got.err != nil || got.path == "" {
		t.Fatalf("pack written: %+v", got)
	}
	m.Update(got)
	if m.ov.kind != ovHandoff {
		t.Fatalf("the pack is shown before anything starts: kind=%d", m.ov.kind)
	}
	v := ansi.Strip(m.View())
	for _, want := range []string{"交接包", r.Title, "1 Claude Code", "2 Codex CLI", "e 编辑", "y 复制内容"} {
		if !strings.Contains(v, want) {
			t.Errorf("handoff dialog lacks %q:\n%s", want, v)
		}
	}
	for i, l := range viewLines(m) {
		if w := ansi.StringWidth(l); w > 140 {
			t.Errorf("line %d is %d wide", i, w)
		}
	}

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if m.quitting {
		t.Fatal("the first 2 only selects Codex")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	s := m.result.Start
	if !m.quitting || s == nil || s.Exec != "codex" || len(s.Args) != 1 || !strings.Contains(s.Args[0], got.path) || s.Cwd != r.Cwd {
		t.Fatalf("2 starts Codex in the session's directory, told to read the pack: %+v", s)
	}
}

// drain runs cmd and yields every message, opening batches.
func drain(cmd tea.Cmd) func(func(tea.Msg) bool) {
	return func(yield func(tea.Msg) bool) {
		var walk func(tea.Cmd) bool
		walk = func(c tea.Cmd) bool {
			if c == nil {
				return true
			}
			msg := c()
			if b, ok := msg.(tea.BatchMsg); ok {
				for _, c := range b {
					if !walk(c) {
						return false
					}
				}
				return true
			}
			return yield(msg)
		}
		walk(cmd)
	}
}
