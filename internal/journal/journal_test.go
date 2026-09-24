package journal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T, path string) (*Log, []Envelope) {
	t.Helper()
	var got []Envelope
	l, err := Open(path, func(e Envelope) error { got = append(got, e); return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l, got
}

func TestAppendReopenAndReadAfter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord", "events.jsonl")
	l, got := open(t, path)
	if len(got) != 0 || l.Seq() != 0 {
		t.Fatal(got)
	}
	for i := 0; i < 5; i++ {
		cmd := &Receipt{ID: "c" + string(rune('a'+i)), Method: "task.create", Digest: Digest([]byte(`{"b":1, "a":"中文"}`))}
		if _, err := l.Append(cmd, []Event{NewEvent("task_created", map[string]any{"id": i, "title": "中文 \"q\" \n"})}); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()
	l, got = open(t, path)
	if len(got) != 5 || got[4].Seq != 5 || got[2].Command.ID != "cc" || l.ReadOnly() != nil {
		t.Fatalf("%d %+v %v", len(got), got, l.ReadOnly())
	}
	var seqs []int64
	l.ReadAfter(2, 4, func(e Envelope) bool { seqs = append(seqs, e.Seq); return true })
	if len(seqs) != 2 || seqs[0] != 3 || seqs[1] != 4 {
		t.Fatal(seqs)
	}
	if Digest([]byte(`{"a":"中文","b":1}`)) != got[0].Command.Digest {
		t.Fatal("the digest ignores key order and spacing")
	}
}

func TestATornLastLineIsCutAndKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, _ := open(t, path)
	l.Append(nil, []Event{NewEvent("a", 1)})
	l.Close()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"v":1,"seq":2,"at`)
	f.Close()
	l, got := open(t, path)
	if len(got) != 1 || l.ReadOnly() != nil {
		t.Fatal(got, l.ReadOnly())
	}
	if _, err := l.Append(nil, []Event{NewEvent("b", 2)}); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(path + ".torn-*")
	if len(matches) != 1 {
		t.Fatal("the torn tail is kept next to the log")
	}
	l.Close()
	if _, got := open(t, path); len(got) != 2 || got[1].Seq != 2 {
		t.Fatal(got)
	}
}

func TestADamagedLineOpensReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, _ := open(t, path)
	l.Append(nil, []Event{NewEvent("a", "x")})
	l.Append(nil, []Event{NewEvent("a", "y")})
	l.Close()
	b, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(b), `"y"`, `"z"`, 1)), 0o600)
	l, got := open(t, path)
	if len(got) != 1 || l.ReadOnly() == nil {
		t.Fatal(got)
	}
	if _, err := l.Append(nil, nil); err == nil {
		t.Fatal("a read-only log takes no appends")
	}
}
