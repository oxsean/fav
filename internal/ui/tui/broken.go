package tui

import (
	"os"

	"github.com/oxsean/fav/internal/fav"
)

func (m *Model) exists(p string) bool {
	if ok, seen := m.pathOK[p]; seen {
		return ok
	}
	if m.pathOK == nil {
		m.pathOK = map[string]bool{}
	}
	_, err := os.Stat(p)
	m.pathOK[p] = err == nil
	return err == nil
}

// broken: cwd gone (move it) or transcript gone (delete it); live sessions never count.
func (m *Model) broken(r *fav.Rec) (dirGone, transcriptGone bool) {
	if r == nil || r.Host != "" || m.isLive(r.SessionID) {
		return false, false
	}
	return r.Broken(m.exists)
}
