package journal

import (
	"os"
	"path/filepath"
	"runtime"
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
		if _, err := l.Append(Actor{Kind: ActorUser, ID: "me"}, cmd, []Event{NewEvent("task_created", map[string]any{"id": i, "title": "中文 \"q\" \n"})}); err != nil {
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
	if got[0].V != Version || got[0].Who() != (Actor{Kind: ActorUser, ID: "me"}) {
		t.Fatalf("v%d %+v", got[0].V, got[0].Who())
	}
}

func TestAVersionOneEnvelopeCountsAsTheSystem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	body := `{"v":1,"seq":1,"at":"2026-09-01T00:00:00Z","command":{"id":"c1","method":"task.create","digest":"d"},"events":[{"type":"a","data":1}]}`
	line := body[:len(body)-1] + `,"sum":"` + sum([]byte(body)) + `"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	l, got := open(t, path)
	if l.ReadOnly() != nil || len(got) != 1 || got[0].Who() != System || got[0].Command.ID != "c1" {
		t.Fatalf("%v %+v", l.ReadOnly(), got)
	}
}

func TestATornLastLineIsCutAndKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, _ := open(t, path)
	l.Append(System, nil, []Event{NewEvent("a", 1)})
	l.Close()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"v":1,"seq":2,"at`)
	f.Close()
	l, got := open(t, path)
	if len(got) != 1 || l.ReadOnly() != nil {
		t.Fatal(got, l.ReadOnly())
	}
	if _, err := l.Append(System, nil, []Event{NewEvent("b", 2)}); err != nil {
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

func TestATornTailThatCannotBeKeptIsNotCut(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory this process cannot write")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	l, _ := open(t, path)
	l.Append(System, nil, []Event{NewEvent("a", 1)})
	l.Close()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"v":1,"seq":2,"at`)
	f.Close()
	before, _ := os.ReadFile(path)
	os.Chmod(dir, 0o500)
	defer os.Chmod(dir, 0o700)
	l, _ = open(t, path)
	if l.ReadOnly() == nil {
		t.Fatal("a tail it could not back up leaves the log read-only")
	}
	l.Close()
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("the tail was cut without a backup")
	}
}

func TestADamagedLineOpensReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, _ := open(t, path)
	l.Append(System, nil, []Event{NewEvent("a", "x")})
	l.Append(System, nil, []Event{NewEvent("a", "y")})
	l.Close()
	b, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(b), `"y"`, `"z"`, 1)), 0o600)
	l, got := open(t, path)
	if len(got) != 1 || l.ReadOnly() == nil {
		t.Fatal(got)
	}
	if _, err := l.Append(System, nil, nil); err == nil {
		t.Fatal("a read-only log takes no appends")
	}
}
