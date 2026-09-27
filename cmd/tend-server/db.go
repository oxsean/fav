package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

func coordDir(home string) string { return filepath.Join(home, "coord") }
func dbPath(home string) string   { return filepath.Join(coordDir(home), store.File) }
func jsonlPath(home string) string {
	return filepath.Join(coordDir(home), "events.jsonl")
}

func openLog(dir string, fold func(journal.Envelope) error) (coord.EventLog, error) {
	l, err := store.Open(filepath.Join(dir, store.File), fold)
	if err != nil {
		return nil, err
	}
	return l, nil
}

// fold applies envelopes to a fresh state, as the coordinator would on start.
func fold() func(journal.Envelope) error {
	st := task.New()
	return st.Apply
}

// unimported is the home's mode 1 journal when it holds envelopes and no database exists yet.
func unimported(home string) string {
	fi, err := os.Stat(jsonlPath(home))
	if err != nil || fi.Size() == 0 {
		return ""
	}
	if _, err := os.Stat(dbPath(home)); err == nil {
		return ""
	}
	return jsonlPath(home)
}

func lockHome(home string) (func(), error) {
	if err := os.MkdirAll(coordDir(home), 0o700); err != nil {
		return nil, err
	}
	unlock, err := filelock.TryLock(filepath.Join(coordDir(home), "lock"))
	if errors.Is(err, filelock.ErrLocked) {
		return nil, i18n.E("cli.server.db_locked", home)
	}
	return unlock, err
}

// cmdImport copies a mode 1 journal (the home's own by default) into the database, with the server stopped.
func cmdImport(args []string) error {
	fs := newFlags()
	if err := fs.Parse(args); err != nil {
		return err
	}
	home := tend.Home()
	src := jsonlPath(home)
	if fs.NArg() > 1 {
		fs.Usage()
		return flag.ErrHelp
	}
	if fs.NArg() == 1 {
		src = fs.Arg(0)
	}
	unlock, err := lockHome(home)
	if err != nil {
		return err
	}
	defer unlock()
	r, err := store.Import(dbPath(home), src, fold())
	if err != nil {
		return err
	}
	if r.Already {
		fmt.Print(i18n.F("cli.server.import_already", src))
		return nil
	}
	kept := src
	if src == jsonlPath(home) {
		kept = fmt.Sprintf("%s.imported-%d", src, time.Now().Unix())
		if err := os.Rename(src, kept); err != nil {
			return err
		}
	}
	fmt.Print(i18n.F("cli.server.imported", r.Envelopes, r.LastSeq, src, kept))
	return nil
}

// cmdExport writes the database's envelopes as a journal; it runs beside the server.
func cmdExport(args []string) error {
	fs := newFlags()
	out := fs.String("o", "", i18n.T("cli.server.flag_out"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	var w io.Writer = os.Stdout
	if *out != "" {
		f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	n, err := store.Export(dbPath(tend.Home()), w)
	if err != nil {
		return err
	}
	fmt.Fprint(os.Stderr, i18n.F("cli.server.exported", n))
	return nil
}

// cmdBackup copies the database and what a restore needs besides it into a new directory; it runs beside the server.
func cmdBackup(args []string) error {
	fs := newFlags()
	if err := fs.Parse(args); err != nil {
		return err
	}
	home := tend.Home()
	to := filepath.Join(home, "backups", time.Now().Format("20060102-150405"))
	if fs.NArg() == 1 {
		to = fs.Arg(0)
	}
	if err := store.Backup(dbPath(home), filepath.Join(to, store.File)); err != nil {
		return err
	}
	for _, rel := range []string{filepath.Join("coord", "id"), filepath.Join("server", "tokens.json"), "config.json"} {
		b, err := os.ReadFile(filepath.Join(home, rel))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err == nil {
			err = fileio.WriteFile(filepath.Join(to, rel), b, 0o600)
		}
		if err != nil {
			return err
		}
	}
	fmt.Print(i18n.F("cli.server.backup_done", to))
	return nil
}

func cmdDB(args []string) error {
	if first(args) != "check" {
		return i18n.E("cli.server.db_usage")
	}
	fs := newFlags()
	asJSON := fs.Bool("json", false, i18n.T("cli.server.flag_json"))
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	r, err := store.Check(dbPath(tend.Home()), fold())
	if err != nil {
		return err
	}
	if *asJSON {
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
	} else if r.OK() {
		fmt.Print(i18n.F("cli.server.check_ok", r.Path, r.Schema, r.Envelopes, r.LastSeq))
	}
	if !r.OK() {
		return i18n.E("cli.server.check_bad", r.Path, r.Problem)
	}
	return nil
}
