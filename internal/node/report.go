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

	"github.com/oxsean/fav/internal/wire"
)

// Report is a line an agent adds to its run with `tend run ask` or `tend run note`.
type Report struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // ask | note
	Text string    `json:"text"`
}

// Report kinds.
const (
	ReportAsk  = "ask"
	ReportNote = "note"
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
	b, err := json.Marshal(Report{At: time.Now(), Kind: kind, Text: text})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, reportsFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// reportsFrom reads the whole report lines after offset from; it answers them and where the next read starts.
func reportsFrom(dir string, from int64) ([]Report, int64) {
	f, err := os.Open(filepath.Join(dir, reportsFile))
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
	var out []Report
	br := bufio.NewReaderSize(io.LimitReader(f, 256<<10), 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil { // a line still being written waits for the next read
			return out, from
		}
		from += int64(len(line))
		var r Report
		if json.Unmarshal(bytes.TrimSpace(line), &r) == nil && r.Text != "" {
			out = append(out, r)
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
