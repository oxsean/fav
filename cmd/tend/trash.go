package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/tend"
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
	s, err := tend.Open()
	if err != nil {
		return err
	}
	r, err := remotePick(s, ref)
	if err != nil {
		return err
	}
	if r.Host != "" {
		return rmFar(r, *yes)
	}
	if _, ok := capture.LocalLive()[r.SessionID]; ok {
		return errors.New(i18n.T("cli.rm.running"))
	}
	idx, _ := index.Open()
	files := index.SessionFilesOf(idx, r)
	if ok, err := confirmErr(i18n.F("cli.rm.confirm", r.Title, len(files)), *yes); !ok {
		return err
	}
	if _, _, err := index.TrashSession(s, idx, r); err != nil {
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
		if done, err := restoreFar(*restore); done {
			return err
		}
		entries, err := tend.LoadTrash()
		if err != nil {
			return err
		}
		hit, n := matchRef(entries, *restore, func(e tend.TrashEntry) (string, string) {
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
		s, err := tend.Open()
		if err != nil {
			return err
		}
		idx, _ := index.Open()
		e, next, err := index.RestoreSession(s, idx, hit.Provider, hit.SessionID)
		if err != nil {
			return err
		}
		if next != idx {
			rescanned(next, nil)
		}
		fmt.Print(i18n.F("cli.trash.restored", e.Title, len(e.Files)))
		return nil
	case *purge:
		days := loadConfig().TrashDays
		if *all { // the only irreversible delete in the whole tool
			days = 0
			entries, _ := tend.LoadTrash()
			if ok, err := confirmErr(i18n.F("cli.trash.purge_confirm", len(entries)), *yes); !ok {
				return err
			}
		}
		n, err := tend.PurgeTrash(days)
		if err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.trash.purged", n))
		return nil
	}
	entries, err := tend.LoadTrash()
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

// rmFar moves another machine's session into that machine's trash through its trash method.
func rmFar(r *tend.Rec, yes bool) error {
	if ok, err := confirmErr(i18n.F("cli.rm.confirm_far", r.Title, r.Host), yes); !ok {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	defer cancel()
	res, err := remoteHosts().Trash(ctx, r.Host, remote.Ref{Provider: r.Provider, SessionID: r.SessionID})
	if err != nil {
		return errors.New(remote.Refused(r.Host, remote.MTrash, err))
	}
	fmt.Print(i18n.F("cli.rm.done_far", r.Host, res.Title, res.Files))
	return nil
}

// restoreFar restores host:ref from that machine's trash through its restore; done is false when ref names no
// configured host, for this machine's trash to take it.
func restoreFar(ref string) (done bool, err error) {
	name, sub, found := strings.Cut(ref, ":")
	if !found {
		return false, nil
	}
	h := remoteHosts()
	if hostName(h, name) == "" {
		farHosts().reach()
	}
	if name = hostName(h, name); name == "" {
		return false, nil
	}
	if sub == "" {
		return true, errors.New(i18n.T("cli.missing_id"))
	}
	if err := farWritable(name); err != nil {
		return true, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	defer cancel()
	recs, _, err := h.Trashed(ctx, name)
	if err != nil {
		return true, errors.New(remote.Refused(name, remote.MRestore, err))
	}
	r, n := matchRef(recs, sub, recKeys)
	switch {
	case n == 0:
		return true, i18n.E("cli.trash.not_found", ref)
	case n > 1:
		return true, refErr(ref, n)
	}
	res, err := h.Restore(ctx, name, remote.Ref{Provider: r.Provider, SessionID: r.SessionID})
	if err != nil {
		return true, errors.New(remote.Refused(name, remote.MRestore, err))
	}
	fmt.Print(i18n.F("cli.trash.restored_far", name, res.Title, res.Files))
	return true, nil
}

// autoPurge runs at startup; failures never block the command.
func autoPurge() {
	if d := loadConfig().TrashDays; d > 0 {
		tend.PurgeTrash(d)
	}
}
