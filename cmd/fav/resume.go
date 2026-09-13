package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
)

func cmdResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, i18n.T("cli.resume.flag_dry_run"))
	noHerdr := fs.Bool("no-herdr", false, i18n.T("cli.resume.flag_no_herdr"))
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
	return resumeRec(s, r, *dryRun, *noHerdr)
}

func resumeRec(s *fav.Store, r *fav.Rec, dryRun, noHerdr bool) error {
	plan, err := capture.PlanResume(r, capture.LiveSessions(), noHerdr)
	if err != nil {
		return err
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
