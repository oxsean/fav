package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
)

func cmdMv(args []string) error {
	fs := newFlags("mv")
	yes := fs.Bool("y", false, i18n.T("cli.rm.flag_yes"))
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return errors.New(i18n.T("cli.mv.usage"))
	}
	old, new, err := absPair(pos[0], pos[1])
	if err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	idx, err := index.Open()
	if err != nil {
		return err
	}
	idx, _ = idx.Refresh()
	_, err = moveProject(s, idx, old, new, *yes)
	return err
}

func absPair(a, b string) (string, string, error) {
	old, err := filepath.Abs(a)
	if err != nil {
		return "", "", err
	}
	new, err := filepath.Abs(b)
	if err != nil {
		return "", "", err
	}
	return old, new, nil
}

// Returns the rescanned index so consecutive moves see fresh data.
func moveProject(s *fav.Store, idx *index.Index, old, new string, yes bool) (*index.Index, error) {
	plan, err := idx.PlanMove(s, capture.LiveSessions(), old, new)
	if err != nil {
		return idx, err
	}
	if len(plan.Live) > 0 {
		fmt.Println(i18n.T("cli.mv.live"))
		for _, l := range plan.Live {
			fmt.Printf("  %s  %s  %s\n", l.Provider, l.SessionID, l.Title)
		}
		return idx, errors.New(i18n.T("cli.mv.refused"))
	}
	if len(plan.Sessions) == 0 && len(plan.Records) == 0 && !plan.Settings {
		return idx, i18n.E("cli.mv.nothing", old)
	}
	fmt.Print(i18n.F("cli.mv.plan", old, new, len(plan.Sessions), plan.Files(), len(plan.Records)))
	if plan.Settings {
		fmt.Println(i18n.T("cli.mv.plan_settings"))
	}
	if ok, err := confirmErr(i18n.T("cli.mv.confirm"), yes); !ok {
		return idx, err
	}
	rep, err := plan.Apply(s)
	if err != nil {
		return idx, err
	}
	next, _ := idx.Rescan(rep.Touched) // rewritten in place, size and mtime unchanged: force the index to rescan
	next.Save()
	fmt.Print(i18n.F("cli.mv.done", rep.Sessions, rep.Files, rep.Records, rep.Trashed))
	return next, nil
}
