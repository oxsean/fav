package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// preview prints how dispatching d would go; what would fail the run at once refuses it unless force.
func preview(cl *coord.Client, d coord.Dispatch, force bool) error {
	ctx, cancel := callTimeout()
	var pv coord.Preview
	err := cl.Call(ctx, coord.MRunPreview, d, &pv)
	cancel()
	if wire.Code(err) == wire.CodeUnknownMethod { // a coordinator older than previews
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Print(i18n.F("cli.run.preview_head", pv.Machine, pv.Agent, pv.Dir))
	if pv.Check != nil {
		fmt.Print(i18n.F("cli.run.preview_check", strings.TrimSpace(pv.Provider+" "+pv.Check.Version), authText(*pv.Check)))
	}
	for _, w := range pv.Blockers {
		fmt.Print(i18n.F("cli.run.preview_block", render.Why(w.Code, w.Detail)))
	}
	for _, w := range pv.Notes {
		fmt.Print(i18n.F("cli.run.preview_note", render.Why(w.Code, w.Detail)))
	}
	if len(pv.Blockers) > 0 && !force {
		return i18n.E("cli.run.blocked", render.Why(pv.Blockers[0].Code, pv.Blockers[0].Detail))
	}
	return nil
}

func authText(c agent.Check) string {
	switch {
	case !c.Installed:
		return i18n.T("cli.agent.not_installed")
	case c.Auth == agent.AuthOK:
		return i18n.T("cli.agent.logged_in")
	case c.Auth == agent.AuthMissing:
		return i18n.T("cli.agent.not_logged_in")
	}
	return i18n.T("cli.agent.login_unknown")
}

// agentsCell sums up a machine's agent checks: "claude 2.1 logged in, codex not installed".
func agentsCell(checks map[string]agent.Check) string {
	var parts []string
	for _, p := range agent.Sessions() {
		if c, ok := checks[p]; ok {
			parts = append(parts, p+" "+authText(c))
		}
	}
	return orDash(strings.Join(parts, ", "))
}

// cmdRunContinue answers a run that waits, or any indexed session, with a new run in the same session.
func cmdRunContinue(args []string) error {
	fs := newFlags("run")
	session := fs.String("session", "", i18n.T("cli.run.flag_session"))
	file := fs.String("file", "", i18n.T("cli.run.flag_text_file"))
	agentName := fs.String("agent", "", i18n.T("cli.run.flag_agent"))
	wait := fs.Bool("wait", false, i18n.T("cli.run.flag_wait"))
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	var ref, text string
	if *session == "" {
		if len(pos) == 0 {
			return i18n.E("cli.run.need_text")
		}
		ref, pos = pos[0], pos[1:]
	}
	text = strings.Join(pos, " ")
	if *file != "" {
		if text, err = readBrief("", *file); err != nil {
			return err
		}
	}
	if strings.TrimSpace(text) == "" {
		return i18n.E("cli.run.need_text")
	}
	p := coord.Continue{Text: text, Agent: *agentName}
	if *session != "" {
		s, err := tend.Open()
		if err != nil {
			return err
		}
		r, err := pick(s, *session)
		if err != nil {
			return err
		}
		if _, live := capture.LiveSessions()[r.SessionID]; live && r.Host == "" {
			return i18n.E("cli.run.session_live", r.SessionID)
		}
		p.Machine, p.Provider, p.Session, p.Dir, p.Title = r.Host, r.Provider, r.SessionID, r.Cwd, r.Title
	}
	events := make(chan journal.Envelope, 64)
	return withCoord(journalPushes(events), func(cl *coord.Client) error {
		st, err := readState(cl)
		if err != nil {
			return err
		}
		if ref != "" {
			if p.Run, err = runID(st, ref); err != nil {
				return err
			}
		}
		var r task.Run
		if err := write(cl, coord.MRunContinue, p, &r); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.run.continued", r.ID, r.Resume, r.Machine))
		if !*wait {
			return nil
		}
		return waitRun(cl, r.ID, st.Seq, events)
	})
}

// cmdRunReport is `tend run ask|note <text>`, run by an agent inside a run: it reaches the run's state.
func cmdRunReport(kind string, args []string) error {
	fs := newFlags("run")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	dir := os.Getenv(node.EnvRunDir)
	if dir == "" {
		return errors.New(i18n.T("cli.run.no_run_dir"))
	}
	text := strings.Join(pos, " ")
	if strings.TrimSpace(text) == "" {
		return i18n.E("cli.run.need_report", kind)
	}
	return node.AddReport(dir, kind, text)
}

// cmdInbox lists the runs that need you on every machine, longest waiting first.
func cmdInbox(args []string) error {
	fs := newFlags("inbox")
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		st, err := readState(cl)
		if err != nil {
			return err
		}
		runs := st.NeedsYou()
		if *asJSON {
			return printJSON(runs)
		}
		if len(runs) == 0 {
			fmt.Print(i18n.T("cli.inbox.none"))
			return nil
		}
		now := time.Now()
		rows := [][]string{{i18n.T("cli.run.col_id"), i18n.T("cli.inbox.col_title"), i18n.T("cli.run.col_machine"), i18n.T("cli.run.col_state"),
			i18n.T("cli.inbox.col_waiting"), i18n.T("cli.inbox.col_why")}}
		for _, r := range runs {
			why := r.Ask
			if why == "" && r.Reason != "" {
				why = render.RunReason(r.Reason)
			}
			rows = append(rows, []string{r.ID, orDash(st.Tasks[r.Task].Title), r.Machine, runState(r),
				render.ShortDur(now.Sub(r.Since())), orDash(render.Sanitize(strings.ReplaceAll(why, "\n", " ")))})
		}
		printTable(rows, termWidth())
		return nil
	})
}
