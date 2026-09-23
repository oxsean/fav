package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atotto/clipboard"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fulltext"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/render"
	fzfui "github.com/oxsean/fav/internal/ui/fzf"
	tuiui "github.com/oxsean/fav/internal/ui/tui"
)

func loadConfig() fav.Config {
	cfg := fav.LoadConfig()
	fav.DefaultTurns = cfg.MinTurns
	render.RelativeTime = cfg.RelativeTime
	if !render.IconsFromEnv() {
		render.SetIcons(cfg.Icons)
	}
	return cfg
}

// cmdOpen: the TUI on one session, right pane focused, even when the lists would hide it.
func cmdOpen(args []string) error {
	fs := newFlags("open")
	noMouse := fs.Bool("no-mouse", false, i18n.T("cli.flag_no_mouse"))
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	s, err := fav.Open()
	if err != nil {
		return err
	}
	r, err := pick(s, first(pos))
	if err != nil {
		return err
	}
	return runTUI(s, "", r, !*noMouse)
}

func runTUI(s *fav.Store, query string, focus *fav.Rec, mouse bool) error {
	idx, err := index.Open()
	if err != nil {
		return err
	}
	res, err := tuiui.Run(s, idx, loadConfig(), query, focus, mouse)
	if err != nil {
		return err
	}
	if res.Start != nil {
		return resumeHere(*res.Start)
	}
	if res.Resume == nil {
		return nil
	}
	return resumeRec(s, res.Resume, false, res.NoHerdr, "")
}

func cmdUI(cmd string, args []string) error {
	mode := cmd
	if mode == "" {
		if mode = os.Getenv("FAV_UI"); mode == "" {
			mode = "tui"
		}
	}
	fs := newFlags(mode)
	noMouse := fs.Bool("no-mouse", false, i18n.T("cli.flag_no_mouse"))
	flags, words := queryDashes(fs, args)
	rest, err := parseMixed(fs, flags)
	if err != nil {
		return err
	}
	query := strings.Join(append(rest, words...), " ")

	switch mode {
	case "fzf":
		id, err := fzfui.Run(query, fzfui.TabByName(loadConfig().DefaultView))
		if err != nil {
			return err
		}
		if id == "" {
			return nil
		}
		return cmdResume([]string{id})
	case "tui":
		s, err := fav.Open()
		if err != nil {
			return err
		}
		return runTUI(s, query, nil, !*noMouse)
	default:
		return i18n.E("cli.unknown_ui", mode)
	}
}

// cmdFzfList is the candidate source of all three fzf tabs, run on every keystroke; the tab comes from FZF_PROMPT.
func cmdFzfList(args []string) error {
	fs := newFlags("fzf-list")
	keep := fs.String("keep", "", "")
	flags, words := queryDashes(fs, args)
	rest, err := parseMixed(fs, flags)
	if err != nil {
		return err
	}
	query := strings.Join(append(rest, words...), " ")
	s, err := fav.Open()
	if err != nil {
		return err
	}
	idx, err := index.Open()
	if err != nil {
		return err
	}
	now := time.Now()
	tab := fzfui.TabOf(os.Getenv("FZF_PROMPT"))
	if q, ok := fulltext.Prefixed(query); ok && tab != fzfui.TabLive { // message search, run on every keystroke
		kw, scope := fulltext.Split(q)
		if kw == "" {
			return nil
		}
		if fulltext.TooLong(kw) { // a keyless row: shown, never resolved to a session
			fmt.Println(render.Sep + i18n.T("msg.too_long"))
			return nil
		}
		idx = refreshed(idx)
		if !syncText(s, idx, 300*time.Millisecond, false) { // a first build: search what is there, finish it in the background
			finishTextInBackground()
		}
		recs, res, err := grep(s, idx, kw, scope, tab == fzfui.TabSessions)
		for _, x := range res {
			fmt.Println(render.Line(recs[x.Cand], now))
		}
		return err
	}
	var recs []*fav.Rec
	switch tab {
	case fzfui.TabSessions:
		recs, err = sessionRecs(s, refreshed(idx), query, *keep)
	case fzfui.TabLive:
		live := capture.LiveSessions()
		recs, err = listRecs(s, idx, live, tabQuery(tab, query), "")
		fav.SortByStart(recs)
		for _, r := range recs {
			fmt.Println(render.LiveLine(r, live[r.SessionID], now))
		}
		return err
	default:
		recs, err = listRecs(s, idx, nil, fav.Parse(query), *keep)
		fav.SortByStart(recs)
	}
	for _, r := range recs {
		fmt.Println(render.Line(r, now))
	}
	return err
}

func cmdFzfTab(args []string) error {
	cur := fzfui.TabOf(os.Getenv("FZF_PROMPT"))
	switch first(args) {
	case "tick":
		fmt.Print(fzfui.Tick(cur))
	case "next":
		fmt.Print(fzfui.Switch(cur.Next(1)))
	case "prev":
		fmt.Print(fzfui.Switch(cur.Next(-1)))
	default:
		fmt.Print(fzfui.Switch(fzfui.TabByName(first(args))))
	}
	return nil
}

// cmdFzfPick backs the fzf sub-pickers: reads the query, nests an fzf, prints the rewritten query.
func cmdFzfPick(args []string) error {
	if len(args) == 0 {
		return errors.New(i18n.T("cli.pick.usage"))
	}
	kind := args[0]
	query := strings.Join(args[1:], " ")

	s, err := fav.Open()
	if err != nil {
		return err
	}

	switch kind {
	case "togglefav", "togglearchive", "toggledone":
		r, err := pick(s, query)
		if err != nil {
			return err
		}
		now := time.Now()
		_, err = s.Update(r, func(r *fav.Rec) {
			switch kind {
			case "togglefav":
				r.ToggleFavorite(now)
			case "togglearchive":
				r.ToggleArchived(now)
			default:
				r.ToggleStatus(fav.StatusDone)
			}
		})
		return err
	case "copy":
		r, err := pick(s, query)
		if err != nil {
			return err
		}
		plan, err := capture.PlanResume(r, capture.LiveSessions(), true)
		if err != nil {
			return err
		}
		return clipboard.WriteAll(plan.Spec.TerminalLine())
	case "read":
		f := os.Getenv(fzfui.PickFileEnv)
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Print(query)
			return nil
		}
		os.Remove(f)
		fmt.Print(string(b))
	case "tags":
		sel := fzfui.Pick(i18n.T("label.tags"), counted(fav.CountBy(scopeRecs(s, query), func(r *fav.Rec) []string { return r.Tags })), true)
		if sel == nil {
			return pickResult(query)
		}
		var toks []string
		for _, line := range sel {
			toks = append(toks, "#"+firstField(line))
		}
		return pickResult(fav.ReplaceTokens(query, toks, fav.HasPrefix("#")))
	case "projects":
		sel := fzfui.Pick(i18n.T("label.projects"), append([]string{i18n.T("picker.all")}, counted(fav.CountBy(scopeRecs(s, query), func(r *fav.Rec) []string { return []string{r.Project} }))...), false)
		if sel == nil {
			return pickResult(query)
		}
		var toks []string
		if sel[0] != i18n.T("picker.all") {
			toks = []string{"project:" + firstField(sel[0])}
		}
		return pickResult(fav.ReplaceTokens(query, toks, fav.HasPrefix("project:")))
	case "status":
		sel := fzfui.Pick(i18n.T("label.status"), []string{i18n.T("cli.pick.status_open"), i18n.T("cli.pick.status_active"), i18n.T("cli.pick.status_done"), i18n.T("cli.pick.status_archived"), i18n.T("cli.pick.status_live"), i18n.T("cli.pick.status_all")}, false)
		if sel == nil {
			return pickResult(query)
		}
		return pickResult(fav.ReplaceTokens(query, []string{"status:" + firstField(sel[0])}, fav.HasPrefix("status:")))
	case "date":
		sel := fzfui.Pick(i18n.T("label.time"), []string{i18n.T("picker.all"), i18n.T("cli.pick.date_today"), i18n.T("cli.pick.date_week"), i18n.T("cli.pick.date_month"), i18n.T("cli.pick.date_year")}, false)
		if sel == nil {
			return pickResult(query)
		}
		var toks []string
		if sel[0] != i18n.T("picker.all") {
			toks = []string{firstField(sel[0])}
		}
		return pickResult(fav.ReplaceTokens(query, toks, fav.HasPrefix("last:", "after:", "before:")))
	default:
		return i18n.E("cli.pick.unknown", kind)
	}
	return nil
}

// pickResult hands a picker's new query to fzf: through $FAV_PICK_FILE inside fzf, else stdout.
func pickResult(q string) error {
	if f := os.Getenv(fzfui.PickFileEnv); f != "" {
		return os.WriteFile(f, []byte(q), 0o600)
	}
	_, err := fmt.Print(q)
	return err
}

func firstField(s string) string {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

// tabQuery is what a tab lists for the typed text: the favorites tab only favorites, the Agents tab what is running.
func tabQuery(tab fzfui.Tab, text string) fav.Query {
	q := fav.Parse(text)
	q.All = tab != fzfui.TabFavorites
	if tab == fzfui.TabLive {
		q.Status, q.Turns = fav.StatusLive, 0
	}
	return q
}

// scopeRecs: the tab's rows for query without its tag, project, source and keyword filters (picker candidates).
func scopeRecs(s *fav.Store, query string) []*fav.Rec {
	q := tabQuery(fzfui.TabOf(os.Getenv("FZF_PROMPT")), query).Scope()
	idx, err := index.Open()
	if err != nil {
		return s.Query(q)
	}
	recs, _ := listRecs(s, idx, nil, q, "")
	return recs
}

func counted(counts []fav.Count) []string {
	out := make([]string, len(counts))
	for i, c := range counts {
		out[i] = fmt.Sprintf("%s  (%d)", c.Name, c.N)
	}
	return out
}
