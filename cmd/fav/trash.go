package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/render"
)

func cmdRm(args []string) error {
	fs := newFlags("rm")
	yes := fs.Bool("y", false, i18n.T("cli.rm.flag_yes"))
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	ref := first(pos)
	if ref == "" {
		return errors.New(i18n.T("cli.rm.usage"))
	}
	s, err := fav.Open()
	if err != nil {
		return err
	}
	r, err := pickLocal(s, ref)
	if err != nil {
		return err
	}
	if _, ok := capture.LocalLive()[r.SessionID]; ok {
		return errors.New(i18n.T("cli.rm.running"))
	}
	idx, _ := index.Open()
	files := index.SessionFilesOf(idx, r)
	if ok, err := confirmErr(i18n.F("cli.rm.confirm", r.Title, len(files)), *yes); !ok {
		return err
	}
	if _, err := index.Trash(s, r, files); err != nil {
		return err
	}
	fmt.Print(i18n.F("cli.rm.done", r.Title, len(files)))
	return nil
}

func cmdTrash(args []string) error {
	fs := newFlags("trash")
	restore := fs.String("restore", "", i18n.T("cli.trash.flag_restore"))
	purge := fs.Bool("purge", false, i18n.T("cli.trash.flag_purge"))
	all := fs.Bool("all", false, i18n.T("cli.trash.flag_all"))
	yes := fs.Bool("y", false, i18n.T("cli.rm.flag_yes"))
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *restore != "":
		entries, err := fav.LoadTrash()
		if err != nil {
			return err
		}
		hit, n := matchRef(entries, *restore, func(e fav.TrashEntry) (string, string) {
			if e.Record != nil {
				return e.SessionID, e.Record.ID
			}
			return e.SessionID, ""
		})
		if n == 0 {
			return i18n.E("cli.trash.not_found", *restore)
		}
		if n > 1 {
			return refErr(*restore, n)
		}
		s, err := fav.Open()
		if err != nil {
			return err
		}
		e, force, err := index.Restore(s, hit.Provider, hit.SessionID)
		if err != nil {
			return err
		}
		if len(force) > 0 {
			if idx, err := index.Open(); err == nil {
				rescanned(idx, force)
			}
		}
		fmt.Print(i18n.F("cli.trash.restored", e.Title, len(e.Files)))
		return nil
	case *purge:
		days := loadConfig().TrashDays
		if *all { // the only irreversible delete in the whole tool
			days = 0
			entries, _ := fav.LoadTrash()
			if ok, err := confirmErr(i18n.F("cli.trash.purge_confirm", len(entries)), *yes); !ok {
				return err
			}
		}
		n, err := fav.PurgeTrash(days)
		if err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.trash.purged", n))
		return nil
	}
	entries, err := fav.LoadTrash()
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(entries)
	}
	if len(entries) == 0 {
		fmt.Println(i18n.T("cli.trash.empty"))
		return nil
	}
	now := time.Now()
	for _, e := range entries {
		fmt.Printf("%s  %-6s  %s  %s\n", e.SessionID, e.Provider, render.When(e.DeletedAt, now), e.Title)
	}
	return nil
}

// autoPurge runs at startup; failures never block the command.
func autoPurge() {
	if d := loadConfig().TrashDays; d > 0 {
		fav.PurgeTrash(d)
	}
}
