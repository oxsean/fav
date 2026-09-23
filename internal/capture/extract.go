package capture

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

// Entry is one searchable piece of a transcript: a message ('u' / 'a', 's' for Claude's context recap), a tool call's input
// ('t') or the head of its output ('o').
// Off is the offset of the message it belongs to (a tool call belongs to the message before it), so a hit opens that message.
type Entry struct {
	Off  int64
	Role byte
	At   int64 // unix seconds
	Text string
}

const (
	extractLineCap = 256 * 1024 // longer lines are tool output or pasted blobs: skipped unread
	extractTextCap = 16 * 1024  // per entry, same as what the chat pane keeps
	toolInputCap   = 300
)

// Extract reads complete lines from `from` to the last newline and emits their prose. It returns the offset after the last
// complete line and the offset of the last message seen, both passed back in on the next call (owner -1 = none yet).
// It never holds more than one line in memory; when ctx ends it stops early, the offsets saying how far it got.
// outLines is how many lines of each tool output to emit (0 = none).
func Extract(ctx context.Context, path string, from, owner int64, outLines int, emit func(Entry)) (int64, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return from, owner, err
	}
	defer f.Close()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return from, owner, err
	}
	r := bufio.NewReaderSize(f, extractLineCap)
	off := from
	for n := 1; ; n++ {
		if n%4096 == 0 && ctx.Err() != nil {
			return off, owner, nil
		}
		line, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) { // an over-long line: skip to its end
			n := int64(len(line))
			for errors.Is(err, bufio.ErrBufferFull) {
				line, err = r.ReadSlice('\n')
				n += int64(len(line))
			}
			if err != nil { // no newline yet: the writer is mid-line, resume here next time
				return off, owner, nil
			}
			off += n
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return off, owner, nil // a trailing partial line is left for the next call
			}
			return off, owner, err
		}
		lineOff := off
		off += int64(len(line))
		if !interesting(line) {
			continue
		}
		var l transcriptLine
		if json.Unmarshal(bytes.TrimSpace(line), &l) != nil {
			continue
		}
		at := l.Timestamp.Unix()
		if l.Timestamp.IsZero() {
			at = 0
		}
		if m := l.speech(); m.Text != "" {
			owner = lineOff
			role := roleByte(m.Role)
			if l.IsSummary {
				role = 's'
			}
			emit(Entry{Off: lineOff, Role: role, At: at, Text: capText(m.Text, extractTextCap)})
		}
		if owner < 0 {
			continue
		}
		for _, st := range l.steps(lineOff, outLines > 0) {
			switch {
			case st.Text == "":
			case st.Result && outLines > 0:
				if text := outputHead(st.Text, outLines); text != "" {
					emit(Entry{Off: owner, Role: 'o', At: at, Text: text})
				}
			case !st.Result:
				text := strings.Join(strings.Fields(st.Tool+" "+firstLine(st.Text)), " ")
				emit(Entry{Off: owner, Role: 't', At: at, Text: capText(text, toolInputCap)})
			}
		}
	}
}

// outputHead is the first n non-blank lines of a tool output, each capped, joined by " | ".
func outputHead(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, capText(l, toolInputCap))
			if len(lines) == n {
				break
			}
		}
	}
	return strings.Join(lines, " | ")
}

func roleByte(role string) byte {
	if role == "user" {
		return 'u'
	}
	return 'a'
}

func capText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }
