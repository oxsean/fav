// Package journal is the coordinator's append-only event log: one envelope per line, numbered without gaps, each line
// carrying the sha256 of its own bytes. It is read line by line, never whole.
package journal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/fileio"
)

// Envelope is one committed change: the events of one command, applied together.
type Envelope struct {
	V       int       `json:"v"`
	Seq     int64     `json:"seq"`
	At      time.Time `json:"at"`
	Command *Receipt  `json:"command,omitempty"`
	Events  []Event   `json:"events"`
}

// Receipt makes a command idempotent: replaying its id returns Result.
type Receipt struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Digest string          `json:"digest"` // sha256 of the params: the same id with other params is a conflict
	Result json.RawMessage `json:"result,omitempty"`
	// Answer, when set, makes Result from the envelope being appended (its seq and time are known only then).
	Answer func(Envelope) json.RawMessage `json:"-"`
}

type Event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// NewEvent encodes data as an event of typ.
func NewEvent(typ string, data any) Event {
	b, err := json.Marshal(data)
	if err != nil {
		panic(err) // event payloads are plain structs
	}
	return Event{Type: typ, Data: b}
}

// ErrReadOnly: the log could not be verified; nothing is appended until it is repaired.
var ErrReadOnly = errors.New("journal is read-only")

// Log is an open journal.
type Log struct {
	mu       sync.Mutex
	path     string
	f        *os.File
	size     int64
	seq      int64
	offsets  []int64 // offsets[i] is where seq i+1 starts
	readOnly error
}

// sumKey ends every line: `,"sum":"<64 hex>"}`.
var sumKey = []byte(`,"sum":"`)

const sumTail = len(`,"sum":"`) + 64 + len(`"}`)

// Open reads path, calls fold for each envelope in order and opens it for appending. A last line without its
// newline (a torn write) is backed up next to the log and cut; any other damage opens it read-only.
func Open(path string, fold func(Envelope) error) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	l := &Log{path: path}
	next, err := fileio.Lines(context.Background(), path, 0, maxLine, func(off int64, line []byte) bool {
		env, err := parse(line)
		if err == nil && env.Seq != l.seq+1 {
			err = fmt.Errorf("seq %d after %d", env.Seq, l.seq)
		}
		if err == nil {
			err = fold(env)
		}
		if err != nil {
			l.readOnly = fmt.Errorf("%w: line at %d: %v", ErrReadOnly, off, err)
			return false
		}
		l.seq = env.Seq
		l.offsets = append(l.offsets, off)
		return true
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	l.f, l.size = f, fi.Size()
	if l.readOnly == nil && next < l.size { // a torn last line: kept next to the log, then cut
		tail := make([]byte, l.size-next)
		_, err := f.ReadAt(tail, next)
		if err == nil {
			err = fileio.WriteFile(fmt.Sprintf("%s.torn-%d", path, time.Now().Unix()), tail, 0o600)
		}
		if err == nil {
			err = f.Truncate(next)
		}
		if err != nil {
			l.readOnly = fmt.Errorf("%w: torn line at %d not cut: %v", ErrReadOnly, next, err)
		} else {
			l.size = next
		}
	}
	return l, nil
}

// maxLine bounds an envelope; a longer line is skipped by the reader, which shows as a gap in seq.
const maxLine = 4 << 20

// parse checks a line's sum and decodes it.
func parse(line []byte) (Envelope, error) {
	line = bytes.TrimRight(line, "\r\n")
	var env Envelope
	if len(line) < sumTail || !bytes.Equal(line[len(line)-sumTail:len(line)-sumTail+len(sumKey)], sumKey) {
		return env, errors.New("no sum")
	}
	body := append(append([]byte(nil), line[:len(line)-sumTail]...), '}')
	want := string(line[len(line)-sumTail+len(sumKey) : len(line)-2])
	if sum(body) != want {
		return env, errors.New("sum mismatch")
	}
	err := json.Unmarshal(body, &env)
	return env, err
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ReadOnly is why the log takes no appends, nil when it does.
func (l *Log) ReadOnly() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.readOnly
}

// Seq is the last committed seq.
func (l *Log) Seq() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seq
}

// Append commits events (and cmd's receipt) as the next envelope; it is on disk when Append returns.
func (l *Log) Append(cmd *Receipt, events []Event) (Envelope, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.readOnly != nil {
		return Envelope{}, l.readOnly
	}
	env := Envelope{V: 1, Seq: l.seq + 1, At: time.Now().UTC(), Command: cmd, Events: events}
	if cmd != nil && cmd.Answer != nil {
		cmd.Result = cmd.Answer(env)
	}
	body, err := json.Marshal(env)
	if err != nil {
		return Envelope{}, err
	}
	if len(body)+sumTail >= maxLine {
		return Envelope{}, fmt.Errorf("envelope of %d bytes", len(body))
	}
	line := append(body[:len(body)-1:len(body)-1], sumKey...)
	line = append(append(line, sum(body)...), '"', '}', '\n')
	if _, err := l.f.WriteAt(line, l.size); err != nil {
		l.f.Truncate(l.size)
		return Envelope{}, err
	}
	if err := l.f.Sync(); err != nil {
		l.f.Truncate(l.size)
		return Envelope{}, err
	}
	l.offsets = append(l.offsets, l.size)
	l.size += int64(len(line))
	l.seq = env.Seq
	return env, nil
}

// ReadAfter calls fn for each envelope with seq in (after, upTo], in order, reading the file outside the append lock;
// fn returning false stops it.
func (l *Log) ReadAfter(after, upTo int64, fn func(Envelope) bool) error {
	l.mu.Lock()
	upTo = min(upTo, l.seq)
	after = max(after, 0)
	if after >= upTo {
		l.mu.Unlock()
		return nil
	}
	from := l.offsets[after]
	l.mu.Unlock()
	var ferr error
	_, err := fileio.Lines(context.Background(), l.path, from, maxLine, func(_ int64, line []byte) bool {
		env, err := parse(line)
		if err != nil {
			ferr = err
			return false
		}
		if env.Seq > upTo {
			return false
		}
		return fn(env)
	})
	if ferr != nil {
		return ferr
	}
	return err
}

func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// Digest is the params digest of a receipt.
func Digest(params json.RawMessage) string {
	var v any
	if json.Unmarshal(params, &v) == nil {
		if b, err := json.Marshal(v); err == nil { // key order and spacing do not change it
			params = b
		}
	}
	return sum(params)
}
