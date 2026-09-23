//go:build !windows

package tui

import (
	"bytes"
	"io"
	"math"
	"os"
	"time"

	"github.com/mattn/go-isatty"
)

// wheelInput wraps the terminal so wheel sequences are thinned before bubbletea parses them: pixel-scrolling
// terminals send one event per pixel and a trackpad fling is thousands. Runs are let through once per frame, and the
// events a frame collected set how many steps it scrolls (square root, like pointer acceleration).
// Fd keeps bubbletea's raw mode; the missing Name() keeps cancelreader on the plain Read path.
type wheelInput struct {
	f    *os.File
	last time.Time
	acc  int    // events seen since the last emitted run
	dir  []byte // direction of acc
	pend []byte
	raw  []byte
}

func (w *wheelInput) Fd() uintptr                 { return w.f.Fd() }
func (w *wheelInput) Write(p []byte) (int, error) { return w.f.Write(p) }
func (w *wheelInput) Close() error                { return w.f.Close() }

func (w *wheelInput) Read(p []byte) (int, error) {
	for len(w.pend) == 0 {
		n, err := w.f.Read(w.raw)
		if n > 0 {
			w.pend = w.thin(dropCut(w.raw[:n]), time.Now())
		}
		if err != nil {
			return 0, err
		}
	}
	n := copy(p, w.pend)
	w.pend = w.pend[n:]
	return n, nil
}

var wheelDown, wheelUp = []byte("\x1b[<65;"), []byte("\x1b[<64;")

// dropCut removes the halves of a mouse sequence cut by the read boundary: a leading "digits;digitsM" and a trailing
// "\x1b[<digits;…" with no final letter. Holding the tail back would swallow the next key; losing one event of a burst is nothing.
func dropCut(b []byte) []byte {
	for i, c := range b {
		if c == 'M' || c == 'm' {
			if i > 0 {
				b = b[i+1:]
			}
			break
		}
		if c != ';' && (c < '0' || c > '9') {
			break
		}
	}
	if i := bytes.LastIndex(b, []byte("\x1b[<")); i >= 0 {
		for _, c := range b[i+3:] {
			if c != ';' && (c < '0' || c > '9') {
				return b
			}
		}
		return b[:i]
	}
	return b
}

// thin: a run of same-direction wheel sequences is dropped when a run was let through less than a frame ago,
// otherwise its last `step` events pass (the last ones, so the position is current). Everything else, including
// a cut-off tail, passes untouched.
func (w *wheelInput) thin(b []byte, now time.Time) []byte {
	out := make([]byte, 0, len(b))
	var run [][]byte
	var runDir []byte
	flush := func() {
		if len(run) == 0 {
			return
		}
		if !bytes.Equal(runDir, w.dir) {
			w.acc, w.dir = 0, runDir
		}
		w.acc += len(run)
		if now.Sub(w.last) >= wheelFrame {
			w.last = now
			step, mult := wheelTune()
			keep := step * accelSteps(w.acc/step, mult)
			w.acc = 0
			last := run[len(run)-1]
			for i := 0; i < keep; i++ {
				out = append(out, last...)
			}
		}
		run, runDir = run[:0], nil
	}
	for i := 0; i < len(b); {
		var dir []byte
		switch {
		case bytes.HasPrefix(b[i:], wheelDown):
			dir = wheelDown
		case bytes.HasPrefix(b[i:], wheelUp):
			dir = wheelUp
		}
		if dir != nil {
			if end := bytes.IndexByte(b[i:], 'M'); end > 0 {
				if runDir != nil && !bytes.Equal(dir, runDir) {
					flush()
				}
				run, runDir = append(run, b[i:i+end+1]), dir
				i += end + 1
				continue
			}
		}
		flush()
		out = append(out, b[i])
		i++
	}
	flush()
	return out
}

// accelSteps: how many steps a frame scrolls when it collected `want` of them — sqrt, scaled by the speed setting.
func accelSteps(want int, mult float64) int {
	if want < 1 {
		return 1
	}
	if mult == 0 {
		return 1
	}
	return max(1, min(want, int(math.Sqrt(float64(want))*mult+0.5)))
}

// terminalInput is the tty bubbletea would use, wrapped; nil means keep bubbletea's default.
func terminalInput() io.Reader {
	f := os.Stdin
	if !isatty.IsTerminal(f.Fd()) {
		var err error
		if f, err = os.Open("/dev/tty"); err != nil {
			return nil
		}
	}
	return &wheelInput{f: f, raw: make([]byte, 64*1024)}
}
