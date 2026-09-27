package node

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/wire"
)

// Report is a line an agent adds to its run with `tend run ask`, `note` or `verdict`.
type Report struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"` // ask | note | verdict
	Text    string    `json:"text"`
	Verdict string    `json:"verdict,omitempty"` // pass | rework | blocked
}

// Report kinds.
const (
	ReportAsk     = "ask"
	ReportNote    = "note"
	ReportVerdict = "verdict"
)

// Env names the supervisor gives its agent.
const (
	EnvRun    = "TEND_RUN"
	EnvRunDir = "TEND_RUN_DIR"
)

const (
	reportsFile = "reports.jsonl"
	maxReport   = 4 << 10
	maxNote     = 500
)

// AddReport appends a report to run directory dir (the agent's TEND_RUN_DIR).
func AddReport(dir, kind, text string) error {
	text = strings.TrimSpace(text)
	if text == "" || kind != ReportAsk && kind != ReportNote {
		return &wire.Error{Code: wire.CodeBadRequest, Detail: "report"}
	}
	if _, err := os.Stat(filepath.Join(dir, "spec.json")); err != nil {
		return err
	}
	text = clip(text, maxReport)
	return appendLine(filepath.Join(dir, reportsFile), Report{At: time.Now(), Kind: kind, Text: text})
}

// AddVerdict appends the run's verdict (pass | rework | blocked) with a summary to run directory dir.
func AddVerdict(dir, verdict, summary string) error {
	if verdict != agent.VerdictPass && verdict != agent.VerdictRework && verdict != agent.VerdictBlocked {
		return &wire.Error{Code: wire.CodeBadRequest, Detail: "verdict " + verdict}
	}
	if _, err := os.Stat(filepath.Join(dir, "spec.json")); err != nil {
		return err
	}
	return appendLine(filepath.Join(dir, reportsFile), Report{At: time.Now(), Kind: ReportVerdict, Verdict: verdict,
		Text: clip(summary, maxReport)})
}

// reportsFrom reads the whole report lines after offset from; it answers them and where the next read starts.
func reportsFrom(dir string, from int64) ([]Report, int64) {
	return linesFrom(filepath.Join(dir, reportsFile), from, func(r Report) bool { return r.Text != "" || r.Verdict != "" })
}

// appendLine adds v to the JSON-lines file path as one line.
func appendLine(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// linesFrom reads the whole lines of JSON-lines file path after offset from that ok accepts; it answers them and where
// the next read starts.
func linesFrom[T any](path string, from int64, ok func(T) bool) ([]T, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, from
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || fi.Size() <= from {
		return nil, from
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil, from
	}
	var out []T
	br := bufio.NewReaderSize(io.LimitReader(f, 1<<20), 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil { // a line still being written waits for the next read
			return out, from
		}
		from += int64(len(line))
		var v T
		if json.Unmarshal(bytes.TrimSpace(line), &v) == nil && ok(v) {
			out = append(out, v)
		}
	}
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xc0 == 0x80 { // not inside a UTF-8 sequence
		cut--
	}
	return s[:cut] + "…"
}
