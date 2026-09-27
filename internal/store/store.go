// Package store is tend-server's database: the coordinator's event log in SQLite (modernc, pure Go). SQL stays in
// this package; callers use its typed functions.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/journal"
	_ "modernc.org/sqlite"
)

// File is the database's name in the coordinator's directory.
const File = "tend.db"

//go:embed migrations/sqlite/*.sql
var migrations embed.FS

// ⚠️ one writer: every write transaction starts with BEGIN IMMEDIATE on the single write connection.
const pragmas = "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"

// Log is the coordinator's event log in a database; it satisfies coord.EventLog. Its owner holds coord/lock.
type Log struct {
	w, r     *sql.DB
	mu       sync.Mutex
	seq      int64
	readOnly error
}

// Open opens or creates the database at path, migrates it, and calls fold for each envelope in order. A newer schema,
// a gap in seq or an envelope fold refuses leaves it read-only.
func Open(path string, fold func(journal.Envelope) error) (*Log, error) {
	if err := private(path); err != nil {
		return nil, err
	}
	w, err := sql.Open("sqlite", path+"?"+pragmas+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	l := &Log{w: w}
	if l.readOnly, err = migrate(w, path); err != nil {
		w.Close()
		return nil, err
	}
	if l.r, err = reader(path); err != nil {
		w.Close()
		return nil, err
	}
	err = readRange(l.r, 0, -1, func(env journal.Envelope) bool {
		err := error(nil)
		if env.Seq != l.seq+1 {
			err = fmt.Errorf("seq %d after %d", env.Seq, l.seq)
		} else {
			err = fold(env)
		}
		if err != nil {
			l.readOnly = fmt.Errorf("%w: %v", journal.ErrReadOnly, err)
			return false
		}
		l.seq = env.Seq
		return true
	})
	if err != nil && l.readOnly == nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func reader(path string) (*sql.DB, error) {
	r, err := sql.Open("sqlite", path+"?"+pragmas+"&_pragma=query_only(1)")
	if err != nil {
		return nil, err
	}
	r.SetMaxOpenConns(4)
	return r, r.Ping()
}

// migrate brings db's schema to latest, one numbered file per transaction, copying the database aside before it
// changes one that already has a schema. A schema newer than this program's is left as it is: readOnly says so.
func migrate(db *sql.DB, path string) (readOnly error, err error) {
	var cur int
	if err := db.QueryRow("PRAGMA user_version").Scan(&cur); err != nil {
		return nil, err
	}
	steps, err := steps()
	if err != nil {
		return nil, err
	}
	switch {
	case cur > len(steps):
		return fmt.Errorf("%w: schema %d is newer than this tend-server's %d", journal.ErrReadOnly, cur, len(steps)), nil
	case cur == len(steps):
		return nil, nil
	case cur > 0:
		if err := vacuumInto(db, fmt.Sprintf("%s.v%d-%d.bak", path, cur, time.Now().Unix())); err != nil {
			return nil, err
		}
	}
	for i := cur; i < len(steps); i++ {
		b, err := migrations.ReadFile(steps[i])
		if err != nil {
			return nil, err
		}
		err = inTx(db, func(tx *sql.Tx) error {
			if _, err := tx.Exec(string(b)); err != nil {
				return fmt.Errorf("%s: %w", steps[i], err)
			}
			_, err := tx.Exec("PRAGMA user_version = " + strconv.Itoa(i+1))
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func steps() ([]string, error) {
	names, err := fs.Glob(migrations, "migrations/sqlite/*.sql")
	sort.Strings(names)
	for i, n := range names {
		if !strings.HasPrefix(filepath.Base(n), fmt.Sprintf("%04d_", i+1)) {
			return nil, fmt.Errorf("migration %s out of order", n)
		}
	}
	return names, err
}

func latest() int {
	s, _ := steps()
	return len(s)
}

func (l *Log) tx(fn func(*sql.Tx) error) error { return inTx(l.w, fn) }

func inTx(db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
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

// Append commits events (and cmd's receipt) from actor as the next envelope in one transaction.
func (l *Log) Append(actor journal.Actor, cmd *journal.Receipt, events []journal.Event) (journal.Envelope, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.readOnly != nil {
		return journal.Envelope{}, l.readOnly
	}
	env := journal.Envelope{V: journal.Version, Seq: l.seq + 1, At: time.Now().UTC(), Actor: &actor, Command: cmd, Events: events}
	if cmd != nil && cmd.Answer != nil {
		cmd.Result = cmd.Answer(env)
	}
	if err := l.tx(func(tx *sql.Tx) error { return insert(tx, env) }); err != nil {
		return journal.Envelope{}, err
	}
	l.seq = env.Seq
	return env, nil
}

func insert(tx *sql.Tx, env journal.Envelope) error {
	var kind any
	id := ""
	if env.Actor != nil {
		kind, id = env.Actor.Kind, env.Actor.ID
	}
	if _, err := tx.Exec(`INSERT INTO envelopes (seq, v, at, actor_kind, actor_id) VALUES (?, ?, ?, ?, ?)`,
		env.Seq, env.V, env.At.UnixNano(), kind, id); err != nil {
		return err
	}
	for i, e := range env.Events {
		if _, err := tx.Exec(`INSERT INTO events (seq, idx, type, data) VALUES (?, ?, ?, ?)`, env.Seq, i, e.Type, string(e.Data)); err != nil {
			return err
		}
	}
	if c := env.Command; c != nil {
		var result any
		if c.Result != nil {
			result = string(c.Result)
		}
		if _, err := tx.Exec(`INSERT INTO receipts (principal, command_id, seq, method, digest, result) VALUES (?, ?, ?, ?, ?, ?)`,
			env.Who().ID, c.ID, env.Seq, c.Method, c.Digest, result); err != nil {
			return err
		}
	}
	return nil
}

// ReadAfter calls fn for each envelope with seq in (after, upTo], in order, in batches that each read in one short
// transaction; fn returning false stops it.
func (l *Log) ReadAfter(after, upTo int64, fn func(journal.Envelope) bool) error {
	l.mu.Lock()
	upTo = min(upTo, l.seq)
	l.mu.Unlock()
	if max(after, 0) >= upTo {
		return nil
	}
	return readRange(l.r, max(after, 0), upTo, fn)
}

const batch = 256

// readRange reads (after, upTo] from db, upTo < 0 meaning to the end.
func readRange(db *sql.DB, after, upTo int64, fn func(journal.Envelope) bool) error {
	for {
		envs, err := readBatch(db, after, upTo)
		if err != nil || len(envs) == 0 {
			return err
		}
		for _, env := range envs {
			if !fn(env) {
				return nil
			}
		}
		after = envs[len(envs)-1].Seq
	}
}

func readBatch(db *sql.DB, after, upTo int64) ([]journal.Envelope, error) {
	if upTo < 0 {
		upTo = 1<<63 - 1
	}
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT e.seq, e.v, e.at, e.actor_kind, e.actor_id, r.command_id, r.method, r.digest, r.result
		FROM envelopes e LEFT JOIN receipts r ON r.seq = e.seq
		WHERE e.seq > ? AND e.seq <= ? ORDER BY e.seq LIMIT ?`, after, upTo, batch)
	if err != nil {
		return nil, err
	}
	var envs []journal.Envelope
	at := map[int64]int{}
	for rows.Next() {
		var env journal.Envelope
		var nanos int64
		var kind, cmdID, method, digest, result sql.NullString
		var actorID string
		if err := rows.Scan(&env.Seq, &env.V, &nanos, &kind, &actorID, &cmdID, &method, &digest, &result); err != nil {
			rows.Close()
			return nil, err
		}
		env.At = time.Unix(0, nanos).UTC()
		if kind.Valid {
			env.Actor = &journal.Actor{Kind: kind.String, ID: actorID}
		}
		if cmdID.Valid {
			env.Command = &journal.Receipt{ID: cmdID.String, Method: method.String, Digest: digest.String}
			if result.Valid {
				env.Command.Result = []byte(result.String)
			}
		}
		at[env.Seq] = len(envs)
		envs = append(envs, env)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(envs) == 0 {
		return nil, err
	}
	rows, err = tx.Query(`SELECT seq, type, data FROM events WHERE seq > ? AND seq <= ? ORDER BY seq, idx`, after, envs[len(envs)-1].Seq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var e journal.Event
		var data string
		if err := rows.Scan(&seq, &e.Type, &data); err != nil {
			return nil, err
		}
		e.Data = []byte(data)
		if i, ok := at[seq]; ok {
			envs[i].Events = append(envs[i].Events, e)
		}
	}
	return envs, rows.Err()
}

func (l *Log) Close() error {
	var err error
	if l.r != nil {
		err = l.r.Close()
	}
	return errors.Join(err, l.w.Close())
}

func vacuumInto(db *sql.DB, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	if _, err := db.Exec("VACUUM INTO ?", to); err != nil {
		return err
	}
	return os.Chmod(to, 0o600)
}

// private creates path's directory and the database file readable by this user only, or narrows one that exists;
// SQLite gives its -wal and -shm files the database's mode.
func private(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	return os.Chmod(path, 0o600)
}
