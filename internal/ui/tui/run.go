package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/remote"
)

// Run: hosts are the other machines whose sessions are shown (nil: none, nothing is contacted).
func Run(s *fav.Store, idx *index.Index, cfg fav.Config, hosts *remote.Hosts, initialQuery string, focus *fav.Rec, mouse bool) (Result, error) {
	m := New(s, idx, cfg, initialQuery)
	m.useHosts(hosts)
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
