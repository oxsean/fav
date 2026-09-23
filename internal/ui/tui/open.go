package tui

import (
	"github.com/atotto/clipboard"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
)

func (m *Model) ideName() string {
	if m.cfg.IDE != "" {
		return m.cfg.IDE
	}
	return capture.DefaultIDE()
}

func recDir(r *fav.Rec) string {
	if r.Cwd != "" {
		return r.Cwd
	}
	return r.GitRoot
}

func (m *Model) openIDE()   { m.openWith(m.ideName(), m.ideName()) }
func (m *Model) openCode()  { m.openWith("code", "code") }
func (m *Model) openFiles() { m.openWith(capture.FileManager(), capture.FileManagerName()) }

func (m *Model) openWith(app, name string) {
	r := m.ov.rec
	if r == nil {
		r = m.current()
	}
	if r == nil {
		return
	}
	dir := recDir(r)
	if err := capture.OpenDir(app, dir); err != nil {
		m.flash(i18n.F("open.failed", name, err.Error()))
		return
	}
	m.closeOverlay()
	m.flash(i18n.F("open.done", name, render.Truncate(paths.Tilde(dir), 60)))
}

// copyText writes the system clipboard; tests replace it.
var copyText = clipboard.WriteAll
