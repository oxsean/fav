package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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

var version = "dev" // set by goreleaser via -X main.version

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) { // the flag package already printed the subcommand's usage
			fmt.Print(usage())
			return
		}
		fmt.Fprintln(os.Stderr, "fav: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	i18n.Set(i18n.Resolve(loadConfig().Lang)) // icons / time format / turn threshold apply to every subcommand, including the fzf children
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
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
	case "status":
		return cmdStatus(args)
	case "done":
		return cmdStatus([]string{firstArg(args), fav.StatusDone})
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
	case "install-hook":
		return cmdInstallHook(args)
	case "uninstall-hook":
		return cmdUninstallHook(args)
	case hookVerb:
		return cmdHookEvent(args)
	case "install-skill":
		return cmdInstallSkill(args)
	case "uninstall-skill":
		return cmdUninstallSkill()
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

func openStore() (*fav.Store, error) { return fav.Open() }

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

// pick falls back from record id to session id (prefix ok): favorites, then the index, then live sessions;
// a session that was never favorited comes back with an empty ID.
func pick(s *fav.Store, ref string) (*fav.Rec, error) {
	if ref == "" {
		return nil, errors.New(i18n.T("cli.missing_id"))
	}
	if r := s.Get(ref); r != nil {
		return r, nil
	}
	var hit *fav.Rec
	for _, r := range s.All() {
		if r.SessionID == ref || strings.HasPrefix(r.SessionID, ref) {
			if hit != nil {
				return nil, i18n.E("cli.session_ambiguous", ref)
			}
			hit = r
		}
	}
	if hit != nil {
		return hit, nil
	}
	idx, err := index.Open()
	if err != nil {
		return nil, err
	}
	for _, ss := range idx.Sessions() {
		if ss.SessionID == ref || strings.HasPrefix(ss.SessionID, ref) {
			if hit != nil {
				return nil, i18n.E("cli.session_ambiguous", ref)
			}
			hit = ss.Rec()
		}
	}
	if hit == nil {
		for id, l := range capture.LiveSessions() {
			if id == ref || strings.HasPrefix(id, ref) {
				hit = synthLive(id, l, idx.Transcript(id))
			}
		}
	}
	if hit == nil {
		if f := idx.FileByPrefix(ref); f != nil {
			hit = f.Rec()
		}
	}
	if hit == nil {
		return nil, i18n.E("cli.record_not_found", ref)
	}
	return hit, nil
}

// save gives a session from the index a record on its first write; favorited_at stays empty so it is not a favorite.
func save(s *fav.Store, r *fav.Rec) error {
	if r.ID == "" {
		r.ID = fav.NewID()
	}
	return s.Put(r)
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
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
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

	s, err := openStore()
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
		old, err := pick(s, *supersede)
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
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	asLine := fs.Bool("line", false, i18n.T("cli.list.flag_line"))
	query := fs.String("query", "", i18n.T("cli.list.flag_query"))
	limit := fs.Int("limit", 0, i18n.T("cli.flag_limit"))
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	expr := strings.TrimSpace(*query + " " + strings.Join(rest, " "))

	s, err := openStore()
	if err != nil {
		return err
	}
	if idx, err := index.Open(); err == nil {
		idx.Attach(s, nil)
	}
	q := fav.Parse(expr)
	if q.Status == "live" {
		live := capture.LiveSessions()
		q.Live = func(id string) bool { _, ok := live[id]; return ok }
	}
	recs := s.Query(q)
	if *limit > 0 && len(recs) > *limit {
		recs = recs[:*limit]
	}

	now := time.Now()
	switch {
	case *asJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(recs)
	case *asLine:
		for _, r := range recs {
			fmt.Println(render.Line(r, now))
		}
	default:
		for _, r := range recs {
			for _, l := range render.Card(r, min(termWidth(), 100), now) {
				fmt.Println(l)
			}
			fmt.Println()
		}
		fmt.Print(i18n.F("cli.items_count", len(recs)))
	}
	return nil
}

func cmdSessions(args []string) error {
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	limit := fs.Int("limit", 0, i18n.T("cli.flag_limit"))
	rest, err := parseMixed(fs, args)
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
	recs := sessionRecs(s, refreshed(idx), strings.Join(rest, " "), "")
	if *limit > 0 && len(recs) > *limit {
		recs = recs[:*limit]
	}

	now := time.Now()
	switch {
	case *asJSON:
		type row struct {
			*fav.Rec
			Turns  int       `json:"turns"`
			Msgs   int       `json:"msgs"`
			LastAt time.Time `json:"last_at"`
		}
		rows := make([]row, len(recs))
		for i, r := range recs {
			rows[i] = row{r, r.Turns, r.Msgs, lastAt(r)}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	default:
		for _, r := range recs {
			for _, l := range render.Card(r, min(termWidth(), 100), now) {
				fmt.Println(l)
			}
			fmt.Println()
		}
		fmt.Print(i18n.F("cli.items_count", len(recs)))
	}
	return nil
}

func refreshed(idx *index.Index) *index.Index {
	idx, changed := idx.Refresh()
	if changed {
		if err := idx.Save(); err != nil {
			fmt.Fprintln(os.Stderr, i18n.T("cli.index_not_written")+err.Error())
		}
	}
	return idx
}

// keep is the key of the row just acted on: it stays until the next reload even when it no longer matches,
// so the action can be undone (the TUI's pin).
func sessionRecs(s *fav.Store, idx *index.Index, expr, keep string) []*fav.Rec {
	q := fav.Parse(expr)
	q.All = true
	all := agentRecs(idx, q)
	if all == nil {
		all = append(idx.Attach(s, nil), s.All()...)
	}
	if q.Status == "live" {
		live := capture.LiveSessions()
		q.Live = func(id string) bool { _, ok := live[id]; return ok }
	}
	var recs []*fav.Rec
	for _, r := range all {
		if q.Match(r) || kept(r, keep) {
			recs = append(recs, r)
		}
	}
	sort.SliceStable(recs, func(i, j int) bool { return lastAt(recs[i]).After(lastAt(recs[j])) })
	return recs
}

// agentRecs: the rows of status:agent, nil for every other query.
func agentRecs(idx *index.Index, q fav.Query) []*fav.Rec {
	if q.Status != fav.StatusAgent {
		return nil
	}
	out := []*fav.Rec{}
	for _, ss := range idx.AgentSessions() {
		out = append(out, ss.Rec())
	}
	return out
}

func kept(r *fav.Rec, keep string) bool { return keep != "" && !r.Deleted && render.LineKey(r) == keep }

func lastAt(r *fav.Rec) time.Time {
	if !r.LastAt.IsZero() {
		return r.LastAt
	}
	if r.FavoritedAt != nil {
		return *r.FavoritedAt
	}
	return r.When()
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
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
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	fmt.Print(render.Preview(r, min(termWidth(), 100), time.Now()))
	return nil
}

func cmdPreview(args []string) error {
	fs := flag.NewFlagSet("preview", flag.ContinueOnError)
	width := fs.Int("width", 0, i18n.T("cli.preview.flag_width"))
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
	w := *width
	if w == 0 {
		w = termWidth()
	}
	fmt.Print(render.Preview(r, w, time.Now()))
	path := r.TranscriptPath
	if r.PinnedPath != "" {
		path = r.PinnedPath
	}
	if page := capture.Messages(path, -1, previewMsgs); len(page.Msgs) > 0 {
		fmt.Println()
		fmt.Println(i18n.F("preview.chat", len(page.Msgs)))
		for _, l := range render.Chat(page.Msgs, w, 6) {
			fmt.Println(l)
		}
	}
	return nil
}

const previewMsgs = 40 // reads the last 1MB of the file, like the TUI right pane

func cmdStatus(args []string) error {
	if len(args) < 2 || !fav.ValidStatus(args[1]) {
		return errors.New(i18n.T("cli.status.usage"))
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	r, err := pick(s, args[0])
	if err != nil {
		return err
	}
	r.Status = args[1]
	if err := save(s, r); err != nil {
		return err
	}
	fmt.Printf("%s → %s\n", r.Title, render.StatusLabel(r.Status))
	return nil
}

func cmdFlag(cmd string, args []string) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	r, err := pick(s, firstArg(args))
	if err != nil {
		return err
	}
	now := time.Now()
	var msg string
	switch cmd {
	case "fav":
		r.FavoritedAt, msg = &now, i18n.T("cli.favorited")
	case "unfav":
		r.FavoritedAt, msg = nil, i18n.T("cli.unfavorited")
	case "archive":
		r.ArchivedAt, msg = &now, i18n.T("status.archived")
	case "unarchive":
		r.ArchivedAt, msg = nil, i18n.T("cli.unarchived")
	}
	if err := save(s, r); err != nil {
		return err
	}
	fmt.Printf("%s  %s\n", msg, r.Title)
	return nil
}

func cmdPin(cmd string, args []string) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	r, err := pick(s, firstArg(args))
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
		r.PinnedPath = ""
		if err := s.Put(r); err != nil {
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
	dir := filepath.Join(fav.Home(), "pinned")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dst := filepath.Join(dir, r.Provider+"-"+r.SessionID+".jsonl")
	os.Remove(dst)
	if err := os.Link(r.TranscriptPath, dst); err != nil {
		// ⚠️ never falls back to copying across devices
		return i18n.E("cli.pin.link_failed", err)
	}
	r.PinnedPath = dst
	if err := s.Put(r); err != nil {
		return err
	}
	fmt.Print(i18n.F("cli.pin.pinned", r.Title, dst, float64(st.Size())/(1<<20)))
	return nil
}

func firstArg(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

func first(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}
