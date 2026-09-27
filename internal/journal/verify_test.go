package journal

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func threeLines(t *testing.T) (string, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, _ := open(t, path)
	for i := 0; i < 3; i++ {
		if _, err := l.Append(System, nil, []Event{NewEvent("a", i)}); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()
	b, _ := os.ReadFile(path)
	return path, b
}

func TestVerifyReadsAndChangesNothing(t *testing.T) {
	path, b := threeLines(t)
	r, err := Verify(path, nil)
	if err != nil || r.Envelopes != 3 || r.LastSeq != 3 || r.Damage != nil || r.Torn != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	os.WriteFile(path, append(b, `{"v":1,"se`...), 0o600)
	r, _ = Verify(path, nil)
	if r.Torn != 10 || r.Damage != nil {
		t.Fatalf("a torn tail is reported: %+v", r)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, append(b, `{"v":1,"se`...)) {
		t.Fatal("verify never writes")
	}
	r, _ = Verify(path, func(e Envelope) error {
		if e.Seq == 2 {
			return errors.New("no such task")
		}
		return nil
	})
	if r.Damage == nil || r.Damage.Seq != 2 || r.Damage.Last {
		t.Fatalf("an envelope the state refuses is damage: %+v", r)
	}
}

func TestRepairCutsOnlyALastBadLineAndKeepsACopy(t *testing.T) {
	path, b := threeLines(t)
	last := bytes.LastIndexByte(b[:len(b)-1], '\n') + 1
	bad := append(append([]byte(nil), b...), 0)
	bad[len(b)-5] ^= 1 // the last line's sum no longer matches
	os.WriteFile(path, bad[:len(b)], 0o600)
	r, _ := Verify(path, nil)
	if r.Damage == nil || !r.Damage.Last || r.Damage.Offset != int64(last) {
		t.Fatalf("%+v", r)
	}
	backup, err := Repair(path, r)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(backup); !bytes.Equal(got, bad[:len(b)]) {
		t.Fatal("the backup is the log as it was")
	}
	if r, _ := Verify(path, nil); r.Damage != nil || r.LastSeq != 2 {
		t.Fatalf("%+v", r)
	}

	path, b = threeLines(t)
	first := bytes.IndexByte(b, '\n')
	b[first-5] ^= 1
	os.WriteFile(path, b, 0o600)
	r, _ = Verify(path, nil)
	if _, err := Repair(path, r); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("damage before the last line is not cut away: %v", err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, b) {
		t.Fatal("a refused repair changes nothing")
	}
}
