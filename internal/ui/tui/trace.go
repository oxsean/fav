package tui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/fav"
)

// Event log, off by default: FAV_TRACE=1 writes ~/.agent/fav/trace.log (truncated at start), FAV_TRACE=path writes elsewhere.
// Wheel, keys, right-pane state and background events, one line each with ms. A separate goroutine writes; a full channel drops, the UI never waits on disk.
var traceQ chan string

func openTrace() {
	p := os.Getenv("FAV_TRACE")
	switch p {
	case "", "0", "off", "no":
		return
	case "1", "on", "yes":
		p = filepath.Join(fav.Home(), "trace.log")
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.OpenFile(p, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	q := make(chan string, 4096)
	traceQ = q
	go func() {
		w := bufio.NewWriter(f)
		for line := range q {
			w.WriteString(line)
			if len(q) == 0 {
				w.Flush()
			}
		}
	}()
}

func tracef(format string, a ...any) {
	if traceQ == nil {
		return
	}
	select {
	case traceQ <- time.Now().Format("15:04:05.000 ") + fmt.Sprintf(format, a...) + "\n":
	default:
	}
}

func (m *Model) traceChat() string {
	r := m.current()
	p := m.probes[r]
	if r == nil || p == nil {
		return "chat=-"
	}
	return fmt.Sprintf("chat=%d/%d cur=%d follow=%v msgs=%d full=%v loading=%v fresh=%d fills=%v room=%d pane=%d",
		m.chatScroll, m.chatSkip, m.chatCur, m.chatFollow, len(p.msgs), p.full, p.loading, len(p.fresh), m.chatFills(m.chatScroll, m.chatSkip), m.chatRoom, m.pane)
}

func (m *Model) traceKey(k tea.KeyPressMsg) {
	if traceQ != nil {
		tracef("key %q ov=%d cursor=%d %s", k.String(), m.ov.kind, m.cursor, m.traceChat())
	}
}
