package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
)

func cmdResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, i18n.T("cli.resume.flag_dry_run"))
	noHerdr := fs.Bool("no-herdr", false, i18n.T("cli.resume.flag_no_herdr"))
	inApp := fs.Bool("app", false, i18n.T("cli.resume.flag_app"))
	inTerminal := fs.Bool("terminal", false, i18n.T("cli.resume.flag_terminal"))
	workspace := fs.String("workspace", "", i18n.T("cli.resume.flag_workspace"))
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	r, err := pick(s, first(rest))
	if err != nil {
		return err
	}
	if !*inTerminal && !*dryRun && (*inApp || wantsApp(loadConfig().ResumeIn, r)) {
		err := openInApp(s, r)
		if err == nil || *inApp {
			return err
		}
		fmt.Fprintln(os.Stderr, err) // the setting asked for the app: fall back to the terminal
	}
	return resumeRec(s, r, *dryRun, *noHerdr, *workspace)
}

// wantsApp: the resume setting sends r to its desktop app.
func wantsApp(mode string, r *fav.Rec) bool {
	if capture.AppURL(r) == "" || !capture.AppAvailable(r.Provider) {
		return false
	}
	return mode == fav.ResumeApp || mode == fav.ResumeOrigin && capture.StartedInApp(r)
}

// openInApp hands r to its desktop app, unless it is running in a terminal (both would write the session).
func openInApp(s *fav.Store, r *fav.Rec) error {
	if _, live := capture.LiveSessions()[r.SessionID]; live && !capture.StartedInApp(r) {
		return errors.New(i18n.T("resume.app_live"))
	}
	if err := capture.OpenApp(r); err != nil {
		return err
	}
	if err := capture.MarkResumed(s, r); err != nil {
		fmt.Fprintf(os.Stderr, i18n.T("cli.resume.count_not_saved"), err)
	}
	fmt.Println(i18n.F("resume.opened_app", capture.AppName(r.Provider)))
	return nil
}

func resumeRec(s *fav.Store, r *fav.Rec, dryRun, noHerdr bool, workspace string) error {
	plan, err := capture.PlanResume(r, capture.LiveSessions(), noHerdr)
	if err != nil {
		return err
	}
	if plan.Ws == nil && len(plan.WsChoices) > 1 {
		var labels []string
		for i, w := range plan.WsChoices {
			labels = append(labels, w.Label)
			if workspace != "" && strings.EqualFold(w.Label, workspace) {
				plan.Ws = &plan.WsChoices[i]
			}
		}
		if plan.Ws == nil && !dryRun {
			return i18n.E("cli.resume.ws_ambiguous", len(labels), strings.Join(labels, ", "))
		}
	}
	if dryRun {
		printPlan(r, plan)
		return nil
	}
	if plan.Blocking() != nil {
		printPlan(r, plan)
		return errors.New(i18n.T("cli.resume.check_failed"))
	}

	if plan.Live.TabID == "" {
		if err := capture.MarkResumed(s, r); err != nil {
			fmt.Fprintf(os.Stderr, i18n.T("cli.resume.count_not_saved"), err)
		}
	}
	if plan.Live.TabID != "" || plan.Ws != nil {
		msg, warn, err := plan.RunInHerdr(r)
		if err == nil {
			if warn != nil {
				fmt.Fprintf(os.Stderr, i18n.T("cli.resume.warn"), warn)
			}
			fmt.Println(msg)
			return nil
		}
		fmt.Fprintf(os.Stderr, i18n.T("cli.resume.herdr_failed"), err)
	}
	return resumeHere(plan.Spec)
}

func resumeHere(spec capture.CommandSpec) error {
	bin, err := exec.LookPath(spec.Exec)
	if err != nil {
		return i18n.E("cli.resume.not_on_path", spec.Exec, err)
	}
	if spec.Cwd != "" {
		if err := os.Chdir(spec.Cwd); err != nil {
			return i18n.E("cli.resume.chdir_failed", spec.Cwd, err)
		}
	}
	return handOff(bin, spec.Argv())
}

func printPlan(r *fav.Rec, p capture.Plan) {
	fmt.Print(i18n.F("cli.resume.header", r.Title))
	fmt.Println(i18n.T("resume.target"))
	fmt.Println("  " + p.Target(r, " -> "))
	if p.Ws != nil {
		fmt.Print(i18n.F("cli.resume.tab_title", capture.TabLabel(r)))
	}
	if p.Live.TabID == "" {
		fmt.Print(i18n.F("cli.resume.command", p.Spec.Display()))
	}
	fmt.Println()
	for _, c := range p.Checks {
		mark := "!"
		if c.OK {
			mark = "+"
		}
		fmt.Printf("  %s %s\n", mark, c.Text)
	}
}
