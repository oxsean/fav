package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
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
	ref := firstArg(pos)
	if ref == "" {
		return errors.New(i18n.T("cli.rm.usage"))
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	r, err := pick(s, ref)
	if err != nil {
		return err
	}
	live := capture.ClaudeLive()
	maps.Copy(live, capture.CodexLive())
	if _, ok := live[r.SessionID]; ok {
		return errors.New(i18n.T("cli.rm.running"))
	}
	files := sessionFiles(r)
	if !confirm(i18n.F("cli.rm.confirm", r.Title, len(files)), *yes) {
		return nil
	}
	n, err := trashSession(s, r)
	if err != nil {
		return err
	}
	fmt.Print(i18n.F("cli.rm.done", r.Title, n))
	return nil
}

func sessionFiles(r *fav.Rec) []string {
	files := index.SessionFiles(r.Provider, r.SessionID)
	if r.PinnedPath != "" {
		files = append(files, r.PinnedPath)
	}
	return files
}

func trashSession(s *fav.Store, r *fav.Rec) (int, error) {
	files := sessionFiles(r)
	e := fav.TrashEntry{Provider: r.Provider, SessionID: r.SessionID, Title: r.Title, Cwd: r.Cwd}
	if r.ID != "" {
		cp := *r
		e.Record = &cp
	}
	if e.SessionID == "" {
		e.SessionID = r.ID
	}
	if _, err := fav.MoveToTrash(e, files); err != nil {
		return 0, err
	}
	if r.ID != "" {
		r.Deleted = true
		if err := s.Put(r); err != nil {
			return 0, err
		}
	}
	return len(files), nil
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
		var hit *fav.TrashEntry
		for i := range entries {
			rec := entries[i].Record
			if entries[i].SessionID == *restore || strings.HasPrefix(entries[i].SessionID, *restore) || rec != nil && rec.ID == *restore {
				if hit != nil {
					return i18n.E("cli.session_ambiguous", *restore)
				}
				hit = &entries[i]
			}
		}
		if hit == nil {
			return i18n.E("cli.trash.not_found", *restore)
		}
		e, err := fav.RestoreTrash(hit.Provider, hit.SessionID)
		if err != nil {
			return err
		}
		if e.Record != nil {
			s, err := openStore()
			if err != nil {
				return err
			}
			e.Record.Deleted = false
			if err := s.Put(e.Record); err != nil {
				return err
			}
		}
		if force := index.RescanAfterRestore(e); len(force) > 0 {
			if idx, err := index.Open(); err == nil {
				next, _ := idx.Rescan(force)
				next.Save()
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
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
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
