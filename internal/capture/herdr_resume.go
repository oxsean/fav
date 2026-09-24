package capture

import (
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/herdr"
	"github.com/oxsean/fav/internal/i18n"
)

type Plan struct {
	Spec CommandSpec
	Live Live             // running now: TabID set → just focus it, BackgroundID set → attach
	Ws   *herdr.Workspace // non-nil → new Herdr tab, nil → this terminal
	// WsChoices: several workspaces fit the directory and Ws is nil — the user picks one (or this terminal).
	WsChoices []herdr.Workspace
	Checks    []Check
}

// PlanResume: running in a Herdr tab → focus it; Claude background session → attach;
// otherwise --resume, inside Herdr when it is up and a workspace is found, else in this terminal.
func PlanResume(r *fav.Rec, live map[string]Live, noHerdr bool) (Plan, error) {
	l, running := live[r.SessionID]
	p := Plan{Live: l, Checks: Checks(r)}
	if p.Live.TabID != "" && !noHerdr {
		return p, nil
	}
	if running && p.Live.BackgroundID == "" { // a second resume would write the same session from two processes
		p.Checks = append(p.Checks, Check{Text: i18n.T("resume.check.running_elsewhere")})
	}
	var err error
	if p.Live.BackgroundID != "" {
		p.Spec = buildAttach(r, p.Live.BackgroundID)
	} else if p.Spec, err = buildResume(r); err != nil {
		return p, err
	}
	p.findWorkspace(r, noHerdr)
	return p, nil
}

// PlanFork: a new session carrying r's history, placed where a resume would go.
func PlanFork(r *fav.Rec, noHerdr bool) (Plan, error) {
	p := Plan{Checks: Checks(r)}
	var err error
	if p.Spec, err = buildFork(r); err != nil {
		return p, err
	}
	p.findWorkspace(r, noHerdr)
	return p, nil
}

// PlanStart: a new provider session in r's directory whose first message is prompt.
func PlanStart(r *fav.Rec, provider, prompt string, noHerdr bool) (Plan, error) {
	p := Plan{Checks: startChecks(provider, r.Cwd)}
	var err error
	if p.Spec, err = buildStart(provider, r.Cwd, prompt); err != nil {
		return p, err
	}
	p.findWorkspace(r, noHerdr)
	return p, nil
}

func (p *Plan) findWorkspace(r *fav.Rec, noHerdr bool) {
	if noHerdr || !herdr.Reachable() {
		return
	}
	switch ws, _ := herdr.WorkspacesFor(r.HerdrWorkspace, r.Cwd); {
	case len(ws) == 1:
		p.Ws = &ws[0]
	case len(ws) > 1:
		p.WsChoices = ws
	}
}

func (p Plan) Target(r *fav.Rec, arrow string) string {
	dir := r.Cwd
	if dir == "" {
		dir = i18n.T("resume.where.cwd")
	} else if r.Host != "" {
		dir = r.Host + ":" + dir
	}
	switch {
	case p.Live.TabID != "":
		return i18n.T("resume.where.running") + arrow + i18n.T("resume.where.switch")
	case p.Ws != nil:
		return "Herdr " + p.Ws.Label + arrow + i18n.T("resume.where.new_tab") + arrow + dir + arrow + p.Spec.Exec
	case len(p.WsChoices) > 1:
		return i18n.F("resume.where.pick_ws", len(p.WsChoices)) + arrow + i18n.T("resume.where.new_tab") + arrow + dir + arrow + p.Spec.Exec
	}
	return i18n.T("resume.where.terminal") + arrow + dir + arrow + p.Spec.Exec
}

func (p Plan) Blocking() *Check {
	for i := range p.Checks {
		if c := &p.Checks[i]; !c.OK && !c.Warn {
			return c
		}
	}
	return nil
}

func TabLabel(r *fav.Rec) string {
	if r.Label != "" {
		return runewidth.Truncate(r.Label, 24, "…")
	}
	return runewidth.Truncate(r.Title, 24, "…")
}

func (p Plan) RunInHerdr(r *fav.Rec) (msg string, warn error, err error) {
	if p.Live.TabID != "" {
		if err := herdr.FocusTab(p.Live.TabID); err != nil {
			return "", nil, err
		}
		return i18n.T("resume.switched"), nil, nil
	}
	label := TabLabel(r)
	pane, err := herdr.CreateTab(p.Ws.WorkspaceID, p.Spec.Cwd, label)
	if err != nil {
		return "", nil, err
	}
	if err := herdr.Run(pane.PaneID, p.Spec.ShellLine()); err != nil {
		return "", nil, err
	}
	if err := herdr.ReportLabel(pane.PaneID, label); err != nil {
		warn = i18n.E("resume.label_not_set", err)
	}
	if err := herdr.KeepLabel(pane.TabID, label); err != nil {
		warn = i18n.E("resume.title_not_restored", err)
	}
	return i18n.F("resume.already_in_herdr", p.Ws.Label, label), warn, nil
}

func MarkResumed(s *fav.Store, r *fav.Rec) error {
	if r.ID == "" {
		return nil
	}
	now := time.Now()
	r.LastResumedAt = &now
	r.ResumeCount++
	return s.Put(r)
}
