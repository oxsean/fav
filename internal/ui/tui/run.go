package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/index"
)

func Run(s *fav.Store, idx *index.Index, cfg fav.Config, initialQuery string, focus *fav.Rec, mouse bool) (Result, error) {
	m := New(s, idx, cfg, initialQuery)
	m.Focus(focus)
	m.mouse = mouse && cfg.Mouse
	var opts []tea.ProgramOption
	if in := terminalInput(); in != nil {
		opts = append(opts, tea.WithInput(in))
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
