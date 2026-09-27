package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/journal"
)

func collect(envs *[]journal.Envelope) func(journal.Envelope) error {
	return func(env journal.Envelope) error { *envs = append(*envs, env); return nil }
}

func js(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fill(t *testing.T, l interface {
	Append(journal.Actor, *journal.Receipt, []journal.Event) (journal.Envelope, error)
}) []journal.Envelope {
	t.Helper()
	var out []journal.Envelope
	add := func(a journal.Actor, cmd *journal.Receipt, evs ...journal.Event) {
		env, err := l.Append(a, cmd, evs)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, env)
	}
	add(journal.Actor{Kind: journal.ActorUser, ID: "ann"},
		&journal.Receipt{ID: "c1", Method: "task.create", Digest: "d1", Answer: func(env journal.Envelope) json.RawMessage {
			return json.RawMessage(`{"seq":` + js(t, env.Seq) + `}`)
		}},
		journal.NewEvent("task_created", map[string]string{"id": "t1", "title": "修登录页"}))
	add(journal.System, nil, journal.NewEvent("run_starting", map[string]string{"id": "r1"}), journal.NewEvent("run_observed", map[string]any{"id": "r1", "node_rev": 2}))
	add(journal.Actor{Kind: journal.ActorNode, ID: "mba"}, nil, journal.NewEvent("run_observed", map[string]string{"id": "r1", "state": "exited"}))
	return out
}

func TestEnvelopesComeBackAsTheyWereAppended(t *testing.T) {
	path := filepath.Join(t.TempDir(), File)
	l, err := Open(path, func(journal.Envelope) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	want := fill(t, l)
	if string(want[0].Command.Result) != `{"seq":1}` {
		t.Fatalf("the answer sees its own envelope: %s", want[0].Command.Result)
	}
	var read []journal.Envelope
	if err := l.ReadAfter(1, 3, func(env journal.Envelope) bool { read = append(read, env); return true }); err != nil {
		t.Fatal(err)
	}
	if js(t, read) != js(t, want[1:]) {
		t.Fatalf("read after 1:\n%s\nwant\n%s", js(t, read), js(t, want[1:]))
	}
	l.Close()

	var folded []journal.Envelope
	l, err = Open(path, collect(&folded))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if js(t, folded) != js(t, want) || l.Seq() != 3 || l.ReadOnly() != nil {
		t.Fatalf("reopen folds every envelope:\n%s\nwant\n%s", js(t, folded), js(t, want))
	}
}

func TestAnOlderEnvelopeWithoutAnActorStaysWithout(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "events.jsonl")
	line, _ := journal.Line(journal.Envelope{V: 1, Seq: 1, Events: []journal.Event{journal.NewEvent("task_created", map[string]string{"id": "t1"})}})
	os.WriteFile(src, line, 0o600)
	if _, err := Import(filepath.Join(dir, File), src, func(journal.Envelope) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var got []journal.Envelope
	l, err := Open(filepath.Join(dir, File), collect(&got))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if len(got) != 1 || got[0].Actor != nil || got[0].V != 1 || got[0].Who() != journal.System {
		t.Fatalf("%s", js(t, got))
	}
}

func TestANewerSchemaOpensReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), File)
	l, _ := Open(path, func(journal.Envelope) error { return nil })
	fill(t, l)
	l.Close()
	setVersion(t, path, latest()+1)
	var got []journal.Envelope
	l, err := Open(path, collect(&got))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !errors.Is(l.ReadOnly(), journal.ErrReadOnly) {
		t.Fatalf("%v", l.ReadOnly())
	}
	if _, err := l.Append(journal.System, nil, nil); !errors.Is(err, journal.ErrReadOnly) {
		t.Fatalf("append on a newer schema: %v", err)
	}
}

func TestAGapOrAFailedFoldOpensReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), File)
	l, _ := Open(path, func(journal.Envelope) error { return nil })
	fill(t, l)
	l.Close()

	var got []journal.Envelope
	l, _ = Open(path, func(env journal.Envelope) error {
		if env.Seq == 2 {
			return errors.New("does not apply")
		}
		got = append(got, env)
		return nil
	})
	if !errors.Is(l.ReadOnly(), journal.ErrReadOnly) || len(got) != 1 || l.Seq() != 1 {
		t.Fatalf("%v %d %d", l.ReadOnly(), len(got), l.Seq())
	}
	l.Close()

	exec(t, path, `DELETE FROM events WHERE seq = 2; DELETE FROM envelopes WHERE seq = 2`)
	l, _ = Open(path, func(journal.Envelope) error { return nil })
	defer l.Close()
	if !errors.Is(l.ReadOnly(), journal.ErrReadOnly) || l.Seq() != 1 {
		t.Fatalf("a gap: %v %d", l.ReadOnly(), l.Seq())
	}
	r, err := Check(path, func(journal.Envelope) error { return nil })
	if err != nil || r.OK() || !strings.Contains(r.Problem, "seq 3 after 1") {
		t.Fatalf("check finds the gap: %+v %v", r, err)
	}
}

func TestAJournalImportsAndExportsUnchanged(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "events.jsonl")
	j, err := journal.Open(src, func(journal.Envelope) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	want := fill(t, j)
	j.Close()
	orig, _ := os.ReadFile(src)

	db := filepath.Join(dir, File)
	r, err := Import(db, src, func(journal.Envelope) error { return nil })
	if err != nil || r.Envelopes != 3 || r.Already {
		t.Fatalf("%+v %v", r, err)
	}
	var buf bytes.Buffer
	if n, err := Export(db, &buf); err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	if buf.String() != string(orig) {
		t.Fatalf("export is the journal, byte for byte:\n%s\nwant\n%s", buf.String(), orig)
	}
	out := filepath.Join(dir, "export.jsonl")
	os.WriteFile(out, buf.Bytes(), 0o600)
	if v, err := journal.Verify(out, nil); err != nil || v.Damage != nil || v.LastSeq != 3 {
		t.Fatalf("the export verifies: %+v %v", v, err)
	}
	var folded []journal.Envelope
	l, _ := Open(db, collect(&folded))
	l.Close()
	if js(t, folded) != js(t, want) {
		t.Fatalf("imported envelopes fold as the journal's")
	}

	if r, err := Import(db, src, func(journal.Envelope) error { return nil }); err != nil || !r.Already {
		t.Fatalf("the same journal again: %+v %v", r, err)
	}
	other := filepath.Join(dir, "other.jsonl")
	j, _ = journal.Open(other, func(journal.Envelope) error { return nil })
	j.Append(journal.System, nil, []journal.Event{journal.NewEvent("x", 1)})
	j.Close()
	if _, err := Import(db, other, func(journal.Envelope) error { return nil }); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("another journal into a used database: %v", err)
	}
}

func TestADamagedJournalIsNotImported(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "events.jsonl")
	j, _ := journal.Open(src, func(journal.Envelope) error { return nil })
	fill(t, j)
	j.Close()
	b, _ := os.ReadFile(src)
	os.WriteFile(src, bytes.Replace(b, []byte("mba"), []byte("mbb"), 1), 0o600)
	db := filepath.Join(dir, File)
	if _, err := Import(db, src, func(journal.Envelope) error { return nil }); err == nil {
		t.Fatal("imported a journal whose sums fail")
	}
	if n, err := Export(db, &bytes.Buffer{}); err != nil || n != 0 {
		t.Fatalf("nothing of it went in: %d %v", n, err)
	}
}

func TestABackupIsAWholeDatabase(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, File)
	l, _ := Open(db, func(journal.Envelope) error { return nil })
	defer l.Close()
	want := fill(t, l)
	to := filepath.Join(dir, "backup", File)
	if err := Backup(db, to); err != nil {
		t.Fatal(err)
	}
	var got []journal.Envelope
	b, err := Open(to, collect(&got))
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	if js(t, got) != js(t, want) {
		t.Fatalf("%s", js(t, got))
	}
	if r, err := Check(to, func(journal.Envelope) error { return nil }); err != nil || !r.OK() || r.LastSeq != 3 {
		t.Fatalf("%+v %v", r, err)
	}
}

func exec(t *testing.T, path, q string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q); err != nil {
		t.Fatal(err)
	}
}

func setVersion(t *testing.T, path string, v int) {
	exec(t, path, "PRAGMA user_version = "+strconv.Itoa(v))
}

func TestTheDatabaseAndItsBackupsArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are POSIX")
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "coord", File)
	os.MkdirAll(filepath.Dir(db), 0o700)
	os.WriteFile(db, nil, 0o644) // a database an older build left readable
	l, err := Open(db, collect(new([]journal.Envelope)))
	if err != nil {
		t.Fatal(err)
	}
	fill(t, l)
	team, err := OpenTeam(db)
	if err != nil {
		t.Fatal(err)
	}
	team.Close()
	backup := filepath.Join(dir, "backups", "b", File)
	if err := Backup(db, backup); err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for _, p := range []string{db, db + "-wal", backup} {
		if fi, err := os.Stat(p); err == nil && fi.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v", filepath.Base(p), fi.Mode().Perm())
		}
	}
}
