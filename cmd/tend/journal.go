package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

func journalPath() string { return filepath.Join(tend.Home(), "coord", "events.jsonl") }

// verifyJournal checks the coordinator's journal line by line and that it folds into a state; it writes nothing.
func verifyJournal() (journal.Report, *task.State, error) {
	st := task.New()
	r, err := journal.Verify(journalPath(), st.Apply)
	return r, st, err
}

// cmdJournal is `tend journal verify [--json]` and `tend journal repair [-y]`.
func cmdJournal(args []string) error {
	verb := first(args)
	if verb != "verify" && verb != "repair" {
		return i18n.E("cli.unknown_subcommand", "journal "+verb, usage())
	}
	fs := newFlags("journal")
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	yes := fs.Bool("y", false, i18n.T("cli.journal.flag_yes"))
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if verb == "verify" {
		r, _, err := verifyJournal()
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(r)
		}
		printJournal(r)
		if r.Damage != nil {
			return i18n.E("cli.journal.damaged")
		}
		return nil
	}
	unlock, err := filelock.TryLock(filepath.Join(tend.Home(), "coord", "lock"))
	if errors.Is(err, filelock.ErrLocked) {
		return i18n.E("cli.journal.running", coord.SocketPath(tend.Home()))
	}
	if err != nil {
		return err
	}
	defer unlock()
	r, _, err := verifyJournal()
	if err != nil {
		return err
	}
	printJournal(r)
	switch {
	case r.Damage == nil && r.Torn == 0:
		return nil
	case r.Damage != nil && !r.Damage.Last:
		return i18n.E("cli.journal.unsafe", r.Damage.Seq)
	case !*yes:
		return i18n.E("cli.journal.need_yes")
	}
	backup, err := journal.Repair(journalPath(), r)
	if err != nil {
		return err
	}
	fmt.Print(i18n.F("cli.journal.repaired", backup))
	return nil
}

func printJournal(r journal.Report) {
	fmt.Print(i18n.F("cli.journal.summary", r.Path, r.Envelopes, r.LastSeq))
	if r.Damage != nil {
		key := "cli.journal.damage"
		if r.Damage.Last {
			key = "cli.journal.damage_last"
		}
		fmt.Print(i18n.F(key, r.Damage.Seq, r.Damage.Offset, r.Damage.Reason))
	}
	if r.Torn > 0 {
		fmt.Print(i18n.F("cli.journal.torn", r.Torn))
	}
}

// doctorCoordinator is doctor's coordinator section: who holds the lock, the journal, the runs it tracks.
func doctorCoordinator() {
	dir := filepath.Join(tend.Home(), "coord")
	if !paths.Exists(journalPath()) {
		return
	}
	if filelock.Held(filepath.Join(dir, "lock")) {
		fmt.Print(i18n.F("cli.doctor.coord_running", coord.SocketPath(tend.Home())))
	} else {
		fmt.Print(i18n.T("cli.doctor.coord_idle"))
	}
	r, st, err := verifyJournal()
	if err != nil {
		fmt.Print(i18n.F("cli.doctor.coord_unreadable", err))
		return
	}
	fmt.Print(i18n.F("cli.doctor.coord_journal", r.Envelopes, r.LastSeq))
	if r.Damage != nil || r.Torn > 0 {
		printJournal(r)
		fmt.Print(i18n.T("cli.doctor.coord_repair"))
	}
	open := 0
	for _, run := range st.Runs {
		if task.Open(run.State) {
			open++
		}
	}
	fmt.Print(i18n.F("cli.doctor.coord_runs", open, len(st.NeedsYou())))
}
