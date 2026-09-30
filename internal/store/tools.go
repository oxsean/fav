package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/oxsean/fav/internal/journal"
)

// ErrNotEmpty: an import goes only into a database that has no envelopes, or that imported the same journal.
var ErrNotEmpty = errors.New("the database already has envelopes")

type ImportReport struct {
	Envelopes int   `json:"envelopes"`
	LastSeq   int64 `json:"last_seq"`
	Already   bool  `json:"already,omitempty"` // this journal was imported before; nothing changed
}

// Import copies the JSONL journal at src into the database at db in one transaction, checking every line's sum, the
// seq order and fold on each envelope; any failure leaves the database as it was. The caller holds coord/lock.
func Import(db, src string, fold func(journal.Envelope) error) (ImportReport, error) {
	sum, err := fileSum(src)
	if err != nil {
		return ImportReport{}, err
	}
	l, err := Open(db, func(journal.Envelope) error { return nil })
	if err != nil {
		return ImportReport{}, err
	}
	defer l.Close()
	if err := l.ReadOnly(); err != nil {
		return ImportReport{}, err
	}
	var last int64
	switch err := l.w.QueryRow(`SELECT last_seq FROM imports WHERE sum = ?`, sum).Scan(&last); {
	case err == nil && last == l.Seq():
		return ImportReport{Envelopes: int(last), LastSeq: last, Already: true}, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return ImportReport{}, err
	}
	if l.Seq() > 0 {
		return ImportReport{}, ErrNotEmpty
	}
	var r journal.Report
	err = l.tx(func(tx *sql.Tx) error {
		r, err = journal.Verify(src, func(env journal.Envelope) error {
			if err := fold(env); err != nil {
				return err
			}
			return insert(tx, env)
		})
		switch {
		case err != nil:
			return err
		case r.Damage != nil:
			return fmt.Errorf("%s: seq %d at %d: %s", src, r.Damage.Seq, r.Damage.Offset, r.Damage.Reason)
		case r.Torn > 0:
			return tornError(src, r.Torn)
		}
		_, err := tx.Exec(`INSERT INTO imports (source, sum, last_seq, at) VALUES (?, ?, ?, ?)`, src, sum, r.LastSeq, time.Now().UnixNano())
		return err
	})
	if err != nil {
		return ImportReport{}, err
	}
	return ImportReport{Envelopes: r.Envelopes, LastSeq: r.LastSeq}, nil
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func openExisting(db string) (*sql.DB, error) {
	if _, err := os.Stat(db); err != nil {
		return nil, err
	}
	return reader(db)
}

// Export writes every envelope of the database at db to w as journal lines, which `tend journal verify` reads; it runs
// beside a server that has the database open.
func Export(db string, w io.Writer) (int64, error) {
	r, err := openExisting(db)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	var n int64
	var werr error
	err = readRange(r, 0, -1, func(env journal.Envelope) bool {
		var line []byte
		if line, werr = journal.Line(env); werr == nil {
			_, werr = w.Write(line)
		}
		n++
		return werr == nil
	})
	return n, errors.Join(err, werr)
}

// Report is what Check found; Problem is the first thing wrong, "" when nothing is.
type Report struct {
	Path      string `json:"path"`
	Schema    int    `json:"schema"`
	Envelopes int    `json:"envelopes"`
	LastSeq   int64  `json:"last_seq"`
	Problem   string `json:"problem,omitempty"`
}

func (r Report) OK() bool { return r.Problem == "" }

// Check runs SQLite's quick_check, then reads every envelope in order through fold and looks for rows no envelope
// owns; it writes nothing and runs beside a server.
func Check(db string, fold func(journal.Envelope) error) (Report, error) {
	rep := Report{Path: db}
	r, err := openExisting(db)
	if err != nil {
		return rep, err
	}
	defer r.Close()
	var quick string
	if err := r.QueryRow("PRAGMA quick_check").Scan(&quick); err != nil {
		return rep, err
	}
	if quick != "ok" {
		rep.Problem = "quick_check: " + quick
		return rep, nil
	}
	if err := r.QueryRow("PRAGMA user_version").Scan(&rep.Schema); err != nil {
		return rep, err
	}
	if rep.Schema > latest() {
		rep.Problem = fmt.Sprintf("schema %d is newer than this tend-server's %d", rep.Schema, latest())
		return rep, nil
	}
	err = readRange(r, 0, -1, func(env journal.Envelope) bool {
		if env.Seq != rep.LastSeq+1 {
			rep.Problem = fmt.Sprintf("seq %d after %d", env.Seq, rep.LastSeq)
		} else if err := fold(env); err != nil {
			rep.Problem = fmt.Sprintf("seq %d: %v", env.Seq, err)
		}
		if rep.Problem != "" {
			return false
		}
		rep.Envelopes++
		rep.LastSeq = env.Seq
		return true
	})
	if err != nil || rep.Problem != "" {
		return rep, err
	}
	var orphans int
	err = r.QueryRow(`SELECT (SELECT count(*) FROM events WHERE seq NOT IN (SELECT seq FROM envelopes))
		+ (SELECT count(*) FROM receipts WHERE seq NOT IN (SELECT seq FROM envelopes))`).Scan(&orphans)
	if err == nil && orphans > 0 {
		rep.Problem = orphansProblem(orphans)
	}
	return rep, err
}

// Backup writes a consistent copy of the database at db to the new file to (VACUUM INTO); it runs beside a server.
func Backup(db, to string) error {
	if _, err := os.Stat(db); err != nil {
		return err
	}
	if _, err := os.Stat(to); err == nil {
		return fmt.Errorf("%s exists", to)
	}
	c, err := sql.Open("sqlite", db+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer c.Close()
	return vacuumInto(c, to)
}

func tornError(src string, n int64) error {
	if n == 1 {
		return fmt.Errorf("%s: a torn last line of 1 byte (tend journal repair)", src)
	}
	return fmt.Errorf("%s: a torn last line of %d bytes (tend journal repair)", src, n)
}

func orphansProblem(n int) string {
	if n == 1 {
		return "1 event or receipt without its envelope"
	}
	return fmt.Sprintf("%d events or receipts without their envelope", n)
}
