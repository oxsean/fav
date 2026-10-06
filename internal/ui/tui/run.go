package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/tend"
)

// Run: others are the other machines whose sessions are shown (zero: none, nothing is contacted); connect reaches the
// coordinator when the Tasks view opens, or at start in mode 2.
func Run(s *tend.Store, idx *index.Index, cfg tend.Config, others Others, connect Connector, initialQuery string, focus *tend.Rec, mouse bool) (Result, error) {
	m := New(s, idx, cfg, initialQuery)
	m.useOthers(others)
	m.SetCoordinator(connect)
	defer m.CloseCoordinator()
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
