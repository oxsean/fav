package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/render"
)

func usage() string { return i18n.T("cli.usage") }

// newFlags: a subcommand's flag set; -h prints its lines of the usage text, then its flags.
func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	own := regexp.MustCompile(`\bfav (\S+\|)?` + regexp.QuoteMeta(name) + `(\||\s|$)`)
	fs.Usage = func() {
		for l := range strings.Lines(usage()) {
			if own.MatchString(l) {
				fmt.Fprint(fs.Output(), l)
			}
		}
		fs.PrintDefaults()
	}
	return fs
}

var version = "dev" // set by goreleaser via -X main.version

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) { // the subcommand already printed its usage
			return
		}
		fmt.Fprintln(os.Stderr, "fav: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	i18n.Set(i18n.Resolve(loadConfig().Lang)) // icons / time format / turn threshold apply to every subcommand, including the fzf children
	cmd := ""
	if len(args) > 0 && (!strings.HasPrefix(args[0], "-") || slices.Contains([]string{"-h", "--help", "--version"}, args[0])) {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "", "tui", "fzf":
		autoPurge()
		return cmdUI(cmd, args)
	case "add":
		return cmdAdd(args)
	case "list":
		return cmdList(args)
	case "sessions":
		return cmdSessions(args)
	case "grep":
		return cmdGrep(args)
	case "text-sync":
		return cmdTextSync()
	case "show":
		return cmdShow(args)
	case "preview":
		return cmdPreview(args)
	case "open":
		return cmdOpen(args)
	case "edit":
		return cmdEdit(args)
	case "status", "done":
		return cmdStatus(cmd, args)
	case "archive", "unarchive", "fav", "unfav":
		return cmdFlag(cmd, args)
	case "pin", "unpin":
		return cmdPin(cmd, args)
	case "rm", "delete":
		return cmdRm(args)
	case "trash":
		return cmdTrash(args)
	case "mv", "move":
		return cmdMv(args)
	case "clean":
		return cmdClean(args)
	case "fix":
		return cmdFix(args)
	case "resume":
		return cmdResume(args)
	case "handoff":
		return cmdHandoff(args)
	case "today", "week":
		return cmdReport(cmd, args)
	case "fzf-pick":
		return cmdFzfPick(args)
	case "fzf-list":
		return cmdFzfList(args)
	case "fzf-tab":
		return cmdFzfTab(args)
	case "doctor":
		return cmdDoctor(args)
	case "rpc":
		return cmdRpc(args)
	case "hosts":
		return cmdHosts(args)
	case "install-hook":
		return cmdInstallHook(args)
	case "uninstall-hook":
		return cmdUninstallHook(args)
	case hookVerb:
		return cmdHookEvent(args)
	case "install-skill":
		return cmdInstallSkill(args)
	case "uninstall-skill":
		return cmdUninstallSkill(args)
	case "shell-init":
		return cmdShellInit(args)
	case "version", "--version":
		fmt.Println("Fav Session Manager " + version)
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage())
		return nil
	default:
		return i18n.E("cli.unknown_subcommand", cmd, usage())
	}
}

// parseMixed lets flags appear after positional args.
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// matchRef finds the items whose session id starts with ref or whose record id is ref: the last hit and how many.
func matchRef[T any](items []T, ref string, keys func(T) (sid, id string)) (hit T, n int) {
	for _, it := range items {
		if sid, id := keys(it); strings.HasPrefix(sid, ref) || id != "" && id == ref {
			hit, n = it, n+1
		}
	}
	return hit, n
}

func refErr(ref string, n int) error {
	switch {
	case n == 0:
		return i18n.E("cli.record_not_found", ref)
	case n > 1:
		return i18n.E("cli.session_ambiguous", ref)
	}
	return nil
}

func recKeys(r *fav.Rec) (string, string) { return r.SessionID, r.ID }

// pick resolves a record id or session id prefix: favorites, indexed sessions, running ones, then any file the index
// has (one-shot runs, silent sessions, older ids of a chain); more than one session is an error. A session never
// favorited comes back with an empty ID. host:ref looks on that configured host (Rec.Host set).
func pick(s *fav.Store, ref string) (*fav.Rec, error) {
	if ref == "" {
		return nil, errors.New(i18n.T("cli.missing_id"))
	}
	if r, ok, err := pickHost(ref); ok {
		return r, err
	}
	if r := s.Get(ref); r != nil {
		return r, nil
	}
	if r, n := matchRef(s.All(), ref, recKeys); n > 0 {
		return r, refErr(ref, n)
	}
	idx, err := index.Open()
	if err != nil {
		return nil, err
	}
	sessionKeys := func(ss *index.Session) (string, string) { return ss.SessionID, "" }
	if ss, n := matchRef(idx.Sessions(), ref, sessionKeys); n > 0 {
		return ss.Rec(), refErr(ref, n)
	}
	var rows index.Rows
	running, _ := rows.List(s, idx, nil, capture.LiveSessions(), fav.Query{Status: fav.StatusLive, All: true})
	if r, n := matchRef(running, ref, recKeys); n > 0 {
		return r, refErr(ref, n)
	}
	f, n := idx.FileByPrefix(ref) // one-shot runs, silent sessions, older ids of a chain
	if err := refErr(ref, n); err != nil {
		return nil, err
	}
	return f.Rec(), nil
}

type skillInput struct {
	SchemaVersion int      `json:"schema_version"`
	Title         string   `json:"title"`
	Label         string   `json:"label"`
	Summary       string   `json:"summary"`
	Tags          []string `json:"tags"`
	Project       string   `json:"project"`
	WorkType      string   `json:"work_type"`
	Status        string   `json:"status"`
}

func cmdAdd(args []string) error {
	fs := newFlags("add")
	supersede := fs.String("supersede", "", i18n.T("cli.add.flag_supersede"))
	sessionID := fs.String("session-id", "", i18n.T("cli.add.flag_session_id"))
	provider := fs.String("provider", "", i18n.T("cli.add.flag_provider"))
	if err := fs.Parse(args); err != nil {
		return err
	}

	var in skillInput
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		return i18n.E("cli.add.read_failed", err)
	}
	if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Summary) == "" {
		return errors.New(i18n.T("cli.add.empty_fields"))
	}

	ctx, err := capture.Detect()
	if err != nil && *sessionID == "" {
		return err
	}
	if *sessionID != "" && *sessionID != ctx.SessionID {
		// refers to another session: this session's transcript and Herdr tab must not carry over
		ctx.SessionID, ctx.TranscriptPath, ctx.HerdrWorkspace, ctx.HerdrTab = *sessionID, "", "", ""
	}
	if *provider != "" {
		ctx.Provider = *provider
	}
	if ctx.Provider == "" || ctx.SessionID == "" {
		return errors.New(i18n.T("cli.add.no_session"))
	}
	if ctx.TranscriptPath == "" {
		ctx.TranscriptPath = capture.TranscriptPath(ctx.Provider, ctx.SessionID)
	}

	s, err := fav.Open()
	if err != nil {
		return err
	}

	now := time.Now()
	r := s.BySession(ctx.Provider, ctx.SessionID)
	action := i18n.T("cli.add.updated")
	if r == nil {
		r = &fav.Rec{ID: fav.NewID()}
		action = i18n.T("cli.favorited")
	}
	if *supersede != "" {
		old, err := pickLocal(s, *supersede)
		if err != nil {
			return err
		}
		r.ID, r.Supersedes, r.ResumeCount = old.ID, old.SessionID, old.ResumeCount
		action = i18n.T("cli.add.continued")
	}
	r.FavoritedAt = &now

	r.Schema = fav.Schema
	r.Provider, r.SessionID = ctx.Provider, ctx.SessionID
	r.Title, r.Summary = strings.TrimSpace(in.Title), strings.TrimSpace(in.Summary)
	r.Label = strings.TrimSpace(in.Label)
	r.Tags = fav.Normalize(in.Tags)
	r.Project, r.WorkType = in.Project, in.WorkType
	if r.Status = in.Status; !fav.ValidStatus(r.Status) {
		r.Status = fav.StatusDefault
	}
	r.Cwd, r.GitRoot, r.GitRemote, r.GitBranch = ctx.Cwd, ctx.GitRoot, ctx.GitRemote, ctx.GitBranch
	r.Hostname, r.TranscriptPath = ctx.Hostname, ctx.TranscriptPath
	if t, ok := capture.SessionStart(ctx.TranscriptPath); ok {
		r.SessionStartedAt = &t
	}
	r.HerdrWorkspace, r.HerdrTab = ctx.HerdrWorkspace, ctx.HerdrTab

	if err := s.Put(r); err != nil {
		return err
	}
	fmt.Printf("%s  %s\n", action, r.Title)
	fmt.Printf("  %s · %s · #%s\n", r.ID, r.Project, strings.Join(r.Tags, " #"))
	return nil
}

func cmdList(args []string) error {
	fs := newFlags("list")
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	asLine := fs.Bool("line", false, i18n.T("cli.list.flag_line"))
	query := fs.String("query", "", i18n.T("cli.list.flag_query"))
	limit := fs.Int("limit", 0, i18n.T("cli.flag_limit"))
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	s, err := fav.Open()
	if err != nil {
		return err
	}
	idx, err := index.Open()
	if err != nil {
		return err
	}
	recs, err := listRecs(s, idx, nil, fav.Parse(*query+" "+strings.Join(rest, " ")), "")
	if err != nil {
		return err
	}
	fav.SortByStart(recs)
	recs = recs[:limited(len(recs), *limit)]
	switch {
	case *asJSON:
		return printJSON(recs)
	case *asLine:
		now := time.Now()
		for _, r := range recs {
			fmt.Println(render.Line(r, now))
		}
	default:
		printCards(recs)
	}
	return nil
}

func cmdSessions(args []string) error {
	fs := newFlags("sessions")
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	limit := fs.Int("limit", 0, i18n.T("cli.flag_limit"))
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	s, err := fav.Open()
	if err != nil {
		return err
	}
	idx, err := index.Open()
	if err != nil {
		return err
	}
	recs, err := sessionRecs(s, refreshed(idx), strings.Join(rest, " "), "")
	if err != nil {
		return err
	}
	recs = recs[:limited(len(recs), *limit)]
	if !*asJSON {
		printCards(recs)
		return nil
	}
	type row struct {
		*fav.Rec
		Turns  int       `json:"turns"`
		Msgs   int       `json:"msgs"`
		LastAt time.Time `json:"last_at"`
	}
	rows := make([]row, len(recs))
	for i, r := range recs {
		rows[i] = row{r, r.Turns, r.Msgs, r.ActiveAt()}
	}
	return printJSON(rows)
}

func limited(n, limit int) int {
	if limit > 0 {
		return min(n, limit)
	}
	return n
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printCards(recs []*fav.Rec) {
	now := time.Now()
	for _, r := range recs {
		for _, l := range render.Card(r, min(termWidth(), 100), now) {
			fmt.Println(l)
		}
		fmt.Println()
	}
	fmt.Print(i18n.F("cli.items_count", len(recs)))
}

func refreshed(idx *index.Index) *index.Index {
	idx, changed := idx.Refresh()
	if changed {
		if err := idx.Save(); err != nil {
			fmt.Fprintln(os.Stderr, i18n.F("cli.index_not_written", err))
		}
	}
	return idx
}

func rescanned(idx *index.Index, force map[string]bool) *index.Index {
	idx, err := idx.RescanSave(force)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.F("cli.index_not_written", err))
	}
	return idx
}

// listRecs lists what q shows, with the rows of the hosts a host: token selects; keep, the fzf key of the row just
// acted on, stays listed so the action can be undone.
func listRecs(s *fav.Store, idx *index.Index, live map[string]capture.Live, q fav.Query, keep string) ([]*fav.Rec, error) {
	recs, err := localRecs(s, idx, live, q, keep)
	far, _ := hostRows(q)
	return append(recs, far...), err
}

// localRecs is listRecs on this machine only.
func localRecs(s *fav.Store, idx *index.Index, live map[string]capture.Live, q fav.Query, keep string) ([]*fav.Rec, error) {
	if live == nil && q.Status == fav.StatusLive {
		live = capture.LiveSessions()
	}
	unfav := idx.Attach(s, nil)
	var rows index.Rows
	recs, err := rows.List(s, idx, unfav, live, q)
	isKept := func(r *fav.Rec) bool { return keep != "" && (r.ID == keep || r.SessionID == keep) }
	if !slices.ContainsFunc(recs, isKept) {
		for _, pool := range [][]*fav.Rec{s.All(), unfav} {
			if i := slices.IndexFunc(pool, isKept); i >= 0 {
				recs = append(recs, pool[i])
				break
			}
		}
	}
	return recs, err
}

func sessionRecs(s *fav.Store, idx *index.Index, expr, keep string) ([]*fav.Rec, error) {
	q := fav.Parse(expr)
	q.All = true
	recs, err := listRecs(s, idx, nil, q, keep)
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].ActiveAt().After(recs[j].ActiveAt()) })
	return recs, err
}

func cmdShow(args []string) error {
	fs := newFlags("show")
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
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
	if *asJSON {
		return printJSON(r)
	}
	fmt.Print(render.Preview(r, remoteHosts().Source(r), min(termWidth(), 100), time.Now()))
	return nil
}

func cmdPreview(args []string) error {
	fs := newFlags("preview")
	width := fs.Int("width", 0, i18n.T("cli.preview.flag_width"))
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
	w := *width
	if w == 0 {
		w = termWidth()
	}
	src := remoteHosts().Source(r)
	fmt.Print(render.Preview(r, src, w, time.Now()))
	if page := src.Messages(-1, previewMsgs); len(page.Msgs) > 0 {
		fmt.Println()
		fmt.Println(i18n.F("preview.chat", len(page.Msgs)))
		for _, l := range render.Chat(page.Msgs, w, 6) {
			fmt.Println(l)
		}
	}
	return nil
}

const previewMsgs = 40 // reads the last 1MB of the file, like the TUI right pane

func cmdStatus(cmd string, args []string) error {
	fs := newFlags(cmd)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if cmd == "done" {
		pos = []string{first(pos), fav.StatusDone}
	}
	if len(pos) != 2 || !fav.ValidStatus(pos[1]) {
		return errors.New(i18n.T("cli.status.usage"))
	}
	r, err := update(pos[0], func(r *fav.Rec) { r.Status = pos[1] })
	if err != nil {
		return err
	}
	fmt.Printf("%s → %s\n", r.Title, render.StatusLabel(r.Status))
	return nil
}

func cmdFlag(cmd string, args []string) error {
	fs := newFlags(cmd)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	now := time.Now()
	msg := map[string]string{"fav": "cli.favorited", "unfav": "cli.unfavorited", "archive": "status.archived", "unarchive": "cli.unarchived"}[cmd]
	r, err := update(first(pos), func(r *fav.Rec) {
		switch cmd {
		case "fav":
			r.FavoritedAt = &now
		case "unfav":
			r.FavoritedAt = nil
		case "archive":
			r.ArchivedAt = &now
		case "unarchive":
			r.ArchivedAt = nil
		}
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s  %s\n", i18n.T(msg), r.Title)
	return nil
}

func update(ref string, change func(*fav.Rec)) (*fav.Rec, error) {
	s, err := fav.Open()
	if err != nil {
		return nil, err
	}
	r, err := pickLocal(s, ref)
	if err != nil {
		return nil, err
	}
	r, err = s.Update(r, change)
	return r, err
}

func cmdPin(cmd string, args []string) error {
	fs := newFlags(cmd)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	s, err := fav.Open()
	if err != nil {
		return err
	}
	r, err := pickLocal(s, first(pos))
	if err != nil {
		return err
	}

	if cmd == "unpin" {
		if r.PinnedPath == "" {
			return errors.New(i18n.T("cli.pin.not_pinned"))
		}
		if err := os.Remove(r.PinnedPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		if _, err := s.Update(r, func(r *fav.Rec) { r.PinnedPath = "" }); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.pin.unpinned", r.Title))
		return nil
	}

	if r.TranscriptPath == "" {
		return errors.New(i18n.T("cli.pin.no_transcript"))
	}
	st, err := os.Stat(r.TranscriptPath)
	if err != nil {
		return i18n.E("cli.pin.transcript_unavailable", err)
	}
	var linkErr error
	r, err = s.Update(r, func(r *fav.Rec) {
		r.PinnedPath = filepath.Join(fav.Home(), "pinned", r.Provider+"-"+r.SessionID+".jsonl")
		linkErr = r.Relink(r.TranscriptPath)
	})
	if err != nil {
		return err
	}
	if linkErr != nil {
		return i18n.E("cli.pin.link_failed", linkErr)
	}
	fmt.Print(i18n.F("cli.pin.pinned", r.Title, r.PinnedPath, float64(st.Size())/(1<<20)))
	return nil
}

func first(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}
