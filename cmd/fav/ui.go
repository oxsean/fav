package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

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
	if len(args) == 0 {
		return errors.New(i18n.T("cli.missing_id"))
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	r, err := pick(s, args[0])
	if err != nil {
		return err
	}
	return runTUI(s, "", r, args)
}

func runTUI(s *fav.Store, query string, focus *fav.Rec, args []string) error {
	idx, err := index.Open()
	if err != nil {
		return err
	}
	res, err := tuiui.Run(s, idx, loadConfig(), query, focus, !hasFlag(args, "--no-mouse"))
	if err != nil {
		return err
	}
	if res.Resume == nil {
		return nil
	}
	return resumeRec(s, res.Resume, false, res.NoHerdr)
}

func cmdUI(cmd string, args []string) error {
	mode := cmd
	if mode == "" {
		if mode = os.Getenv("FAV_UI"); mode == "" {
			mode = "tui"
		}
	}
	var words []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	query := strings.Join(words, " ")

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
		s, err := openStore()
		if err != nil {
			return err
		}
		return runTUI(s, query, nil, args)
	default:
		return i18n.E("cli.unknown_ui", mode)
	}
}

// cmdFzfList is the candidate source of all three fzf tabs, run on every keystroke; the tab comes from FZF_PROMPT.
func cmdFzfList(args []string) error {
	fs := flag.NewFlagSet("fzf-list", flag.ContinueOnError)
	keep := fs.String("keep", "", "")
	flags, words := queryDashes(fs, args)
	rest, err := parseMixed(fs, flags)
	if err != nil {
		return err
	}
	query := strings.Join(append(rest, words...), " ")
	s, err := openStore()
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
		recs, res := grep(s, idx, kw, scope)
		for _, x := range res {
			if r := recs[x.Cand]; tab == fzfui.TabSessions || r.Favorite() {
				fmt.Println(render.Line(r, now))
			}
		}
		return nil
	}
	switch tab {
	case fzfui.TabSessions:
		for _, r := range sessionRecs(s, refreshed(idx), query, *keep) {
			fmt.Println(render.Line(r, now))
		}
	case fzfui.TabLive:
		live := capture.LiveSessions()
		q := fav.Parse(query)
		q.All, q.Status = true, "all"
		q.Turns = 0
		var recs []*fav.Rec
		seen := map[string]bool{}
		for _, r := range append(idx.Attach(s, nil), s.All()...) {
			if _, ok := live[r.SessionID]; ok && q.Match(r) {
				recs = append(recs, r)
				seen[r.SessionID] = true
			}
		}
		for id, l := range live {
			if r := synthLive(id, l, idx.Transcript(id)); !seen[id] && q.Match(r) {
				recs = append(recs, r)
			}
		}
		sort.SliceStable(recs, func(i, j int) bool { return startedAt(recs[i]).After(startedAt(recs[j])) })
		for _, r := range recs {
			fmt.Println(render.LiveLine(r, live[r.SessionID], now))
		}
	default:
		idx.Attach(s, nil)
		q := fav.Parse(query)
		wide := q
		wide.All, wide.Status = true, "all"
		for _, r := range s.Query(wide) {
			if q.Match(r) || kept(r, *keep) {
				fmt.Println(render.Line(r, now))
			}
		}
	}
	return nil
}

func synthLive(id string, l capture.Live, transcript string) *fav.Rec {
	r := &fav.Rec{Provider: l.Agent, SessionID: id, Cwd: l.Cwd, Title: l.Title, Status: fav.StatusDoing, TranscriptPath: transcript}
	if l.Cwd != "" {
		r.Project = filepath.Base(l.Cwd)
	}
	if r.Title == "" {
		r.Title = i18n.F("live.just_started", id[:min(8, len(id))])
	}
	r.Prepare()
	return r
}

// sessions the index has not seen yet have no start time: treat them as newest
func startedAt(r *fav.Rec) time.Time {
	if r.SessionStartedAt == nil {
		return time.Now().Add(time.Hour)
	}
	return *r.SessionStartedAt
}

func cmdFzfTab(args []string) error {
	cur := fzfui.TabOf(os.Getenv("FZF_PROMPT"))
	switch firstArg(args) {
	case "tick":
		fmt.Print(fzfui.Tick(cur))
	case "next":
		fmt.Print(fzfui.Switch((cur + 1) % 3))
	case "prev":
		fmt.Print(fzfui.Switch((cur + 2) % 3))
	default:
		fmt.Print(fzfui.Switch(fzfui.TabByName(firstArg(args))))
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

	s, err := openStore()
	if err != nil {
		return err
	}

	switch kind {
	case "togglefav", "togglearchive", "toggledone":
		r, err := pick(s, query)
		if err != nil {
			return err
		}
		switch {
		case kind == "toggledone" && r.Done():
			return cmdStatus([]string{query, fav.StatusDoing})
		case kind == "toggledone":
			return cmdStatus([]string{query, fav.StatusDone})
		case kind == "togglearchive" && r.Archived():
			return cmdFlag("unarchive", []string{query})
		case kind == "togglearchive":
			return cmdFlag("archive", []string{query})
		case r.Favorite():
			return cmdFlag("unfav", []string{query})
		}
		return cmdFlag("fav", []string{query})
	case "copy":
		r, err := pick(s, query)
		if err != nil {
			return err
		}
		plan, err := capture.PlanResume(r, capture.LiveSessions(), true)
		if err != nil {
			return err
		}
		return clipboard.WriteAll(plan.Spec.ShellLine())
	case "tags":
		sel := fzfui.Pick(i18n.T("label.tags"), counted(tagCounts(s)), true)
		if sel == nil {
			fmt.Print(query)
			return nil
		}
		var toks []string
		for _, line := range sel {
			toks = append(toks, "#"+firstField(line))
		}
		fmt.Print(replaceTokens(query, toks, func(t string) bool { return strings.HasPrefix(t, "#") }))
	case "projects":
		sel := fzfui.Pick(i18n.T("label.projects"), append([]string{i18n.T("picker.all")}, counted(projectCounts(s))...), false)
		if sel == nil {
			fmt.Print(query)
			return nil
		}
		var toks []string
		if sel[0] != i18n.T("picker.all") {
			toks = []string{"project:" + firstField(sel[0])}
		}
		fmt.Print(replaceTokens(query, toks, hasPrefixFn("project:")))
	case "status":
		sel := fzfui.Pick(i18n.T("label.status"), []string{i18n.T("cli.pick.status_open"), i18n.T("cli.pick.status_active"), i18n.T("cli.pick.status_done"), i18n.T("cli.pick.status_archived"), i18n.T("cli.pick.status_live"), i18n.T("cli.pick.status_all")}, false)
		if sel == nil {
			fmt.Print(query)
			return nil
		}
		fmt.Print(replaceTokens(query, []string{"status:" + firstField(sel[0])}, hasPrefixFn("status:")))
	case "date":
		sel := fzfui.Pick(i18n.T("label.time"), []string{i18n.T("picker.all"), i18n.T("cli.pick.date_today"), i18n.T("cli.pick.date_week"), i18n.T("cli.pick.date_month"), i18n.T("cli.pick.date_year")}, false)
		if sel == nil {
			fmt.Print(query)
			return nil
		}
		var toks []string
		if sel[0] != i18n.T("picker.all") {
			toks = []string{firstField(sel[0])}
		}
		fmt.Print(replaceTokens(query, toks, hasPrefixFn("last:", "after:", "before:")))
	default:
		return i18n.E("cli.pick.unknown", kind)
	}
	return nil
}

func replaceTokens(query string, add []string, isSameKind func(string) bool) string {
	var kept []string
	for _, t := range strings.Fields(query) {
		if !isSameKind(t) {
			kept = append(kept, t)
		}
	}
	return strings.TrimSpace(strings.Join(append(kept, add...), " "))
}

func hasPrefixFn(prefixes ...string) func(string) bool {
	return func(t string) bool {
		low := strings.ToLower(t)
		for _, p := range prefixes {
			if strings.HasPrefix(low, p) {
				return true
			}
		}
		return false
	}
}

func firstField(s string) string {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

func tagCounts(s *fav.Store) map[string]int {
	m := map[string]int{}
	for _, r := range s.All() {
		if r.Visible() {
			for _, t := range r.Tags {
				m[t]++
			}
		}
	}
	return m
}

func projectCounts(s *fav.Store) map[string]int {
	m := map[string]int{}
	for _, r := range s.All() {
		if r.Visible() && r.Project != "" {
			m[r.Project]++
		}
	}
	return m
}

func counted(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = fmt.Sprintf("%s  (%d)", k, m[k])
	}
	return out
}
