package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
)

// cmdMigrateHome moves ~/.agent/fav to ~/.agent/tend once (see fav.MigrateHome).
func cmdMigrateHome(args []string) error {
	fs := newFlags("migrate-home")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := fav.MigrateHome(time.Now())
	if m.OldTend != "" {
		fmt.Print(i18n.F("cli.migrate.old_tend", m.OldTend))
	}
	if errors.Is(err, fav.ErrHomeBusy) {
		return i18n.E("cli.migrate.busy", err)
	}
	if err != nil {
		return err
	}
	switch {
	case m.Done:
		fmt.Print(i18n.F("cli.migrate.done_before", fav.Home()))
	case m.Moved != "":
		fmt.Print(i18n.F("cli.migrate.backup", m.Backup))
		fmt.Print(i18n.F("cli.migrate.moved", m.Moved))
		if m.LinkErr != nil {
			fmt.Print(i18n.F("cli.migrate.no_link", m.LinkErr))
		}
	case m.Marked != "":
		fmt.Print(i18n.F("cli.migrate.marked", m.Marked))
	}
	return nil
}
