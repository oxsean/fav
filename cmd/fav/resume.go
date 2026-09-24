package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
)

func cmdResume(args []string) error {
	fs := newFlags("resume")
	dryRun := fs.Bool("dry-run", false, i18n.T("cli.resume.flag_dry_run"))
	noHerdr := fs.Bool("no-herdr", false, i18n.T("cli.resume.flag_no_herdr"))
	inApp := fs.Bool("app", false, i18n.T("cli.resume.flag_app"))
	inTerminal := fs.Bool("terminal", false, i18n.T("cli.resume.flag_terminal"))
	workspace := fs.String("workspace", "", i18n.T("cli.resume.flag_workspace"))
	fork := fs.Bool("fork", false, i18n.T("cli.resume.flag_fork"))
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	s, err := fav.Open()
	if err != nil {
		return err
	}
	r, err := pick(s, first(rest))
	if err != nil {
		return err
	}
	if r.Host != "" {
		if *fork {
			return readOnly(r)
		}
		return resumeRemote(r, *dryRun, *noHerdr, *workspace)
	}
	if *fork {
		plan, err := capture.PlanFork(r, *noHerdr)
		if err != nil {
			return err
		}
		return runPlan(s, r, plan, *dryRun, *workspace, false)
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
		fmt.Fprint(os.Stderr, i18n.F("cli.resume.count_not_saved", err))
	}
	fmt.Println(i18n.F("resume.opened_app", capture.AppName(r.Provider)))
	return nil
}

func resumeRec(s *fav.Store, r *fav.Rec, dryRun, noHerdr bool, workspace string) error {
	if r.Host != "" {
		return resumeRemote(r, dryRun, noHerdr, workspace)
	}
	plan, err := capture.PlanResume(r, capture.LiveSessions(), noHerdr)
	if err != nil {
		return err
	}
	return runPlan(s, r, plan, dryRun, workspace, true)
}

// runPlan carries out a plan; resume = it continues r itself (counted), not a fork or a new session.
func runPlan(s *fav.Store, r *fav.Rec, plan capture.Plan, dryRun bool, workspace string, resume bool) error {
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
		printPlan(r, plan, resume)
		return nil
	}
	if plan.Blocking() != nil {
		printPlan(r, plan, resume)
		return errors.New(i18n.T("cli.resume.check_failed"))
	}

	if plan.Live.TabID == "" && resume {
		if err := capture.MarkResumed(s, r); err != nil {
			fmt.Fprint(os.Stderr, i18n.F("cli.resume.count_not_saved", err))
		}
	}
	if plan.Live.TabID != "" || plan.Ws != nil {
		msg, warn, err := plan.RunInHerdr(r)
		if err == nil {
			if warn != nil {
				fmt.Fprint(os.Stderr, i18n.F("cli.resume.warn", warn))
			}
			if !resume {
				msg = i18n.F("start.opened_tab", plan.Ws.Label, capture.TabLabel(r))
			}
			fmt.Println(msg)
			return nil
		}
		fmt.Fprint(os.Stderr, i18n.F("cli.resume.herdr_failed", err))
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

func printPlan(r *fav.Rec, p capture.Plan, resume bool) {
	header := "cli.resume.header"
	if !resume {
		header = "cli.start.header"
	}
	fmt.Print(i18n.F(header, r.Title))
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

func cmdHandoff(args []string) error {
	fs := newFlags("handoff")
	to := fs.String("to", "", i18n.T("cli.handoff.flag_to"))
	dryRun := fs.Bool("dry-run", false, i18n.T("cli.handoff.flag_dry_run"))
	noHerdr := fs.Bool("no-herdr", false, i18n.T("cli.resume.flag_no_herdr"))
	workspace := fs.String("workspace", "", i18n.T("cli.resume.flag_workspace"))
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	s, err := fav.Open()
	if err != nil {
		return err
	}
	r, err := pickLocal(s, first(rest))
	if err != nil {
		return err
	}
	path, err := capture.WriteHandoff(r)
	if err != nil {
		return err
	}
	fmt.Fprint(os.Stderr, i18n.F("cli.handoff.written", path))
	if *to == "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		return err
	}
	plan, err := capture.PlanStart(r, *to, capture.HandoffPrompt(path), *noHerdr)
	if err != nil {
		return err
	}
	return runPlan(s, r, plan, *dryRun, *workspace, false)
}
