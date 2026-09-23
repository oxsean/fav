package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/index"
)

func Run(s *fav.Store, idx *index.Index, cfg fav.Config, initialQuery string, focus *fav.Rec, mouse bool) (Result, error) {
	m := New(s, idx, cfg, initialQuery)
	m.Focus(focus)
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if in := terminalInput(); in != nil {
		opts = append(opts, tea.WithInput(in))
	}
	if mouse && cfg.Mouse {
		// with the mouse on, native terminal selection needs Shift (Option on iTerm2), or --no-mouse
		opts = append(opts, tea.WithMouseCellMotion())
	}
	final, err := tea.NewProgram(m, opts...).Run()
	if err != nil {
		return Result{}, err
	}
	if fm, ok := final.(*Model); ok {
		return fm.Result(), nil
	}
	return Result{}, nil
}
