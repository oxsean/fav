package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
)

type broken struct {
	n       int
	rec     *fav.Rec
	dirGone bool
	found   []string
	live    bool
}

// Does the stat / flock / git work once; filterBroken narrows the result without rescanning.
func scanBroken(s *fav.Store, idx *index.Index, dir string) []broken {
	live := capture.LiveSessions()
	seen := map[string]bool{}
	var out []broken
	add := func(r *fav.Rec) {
		key := r.Provider + ":" + r.SessionID
		if r.SessionID == "" || seen[key] {
			return
		}
		if dir != "" && !paths.Under(r.Cwd, dir) {
			return
		}
		dirGone := r.Cwd != "" && !paths.IsDir(r.Cwd)
		if !dirGone && capture.TranscriptAlive(r) {
			return
		}
		seen[key] = true
		_, isLive := live[r.SessionID]
		out = append(out, broken{rec: r, dirGone: dirGone, live: isLive})
	}
	for _, r := range s.All() {
		add(r)
	}
	for _, r := range idx.Attach(s, nil) {
		add(r)
	}
	found := map[string][]string{}
	for _, b := range out {
		if b.dirGone {
			for _, m := range idx.FindMissing(s, "") {
				found[m.Dir] = m.Found
			}
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].rec, out[j].rec
		if a.Cwd != b.Cwd {
			return a.Cwd < b.Cwd
		}
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		return a.SessionID < b.SessionID
	})
	for i := range out {
		if out[i].dirGone {
			out[i].found = found[out[i].rec.Cwd]
		}
	}
	return out
}

func filterBroken(list []broken, q fav.Query) []broken {
	var out []broken
	for _, b := range list {
		if q.Match(b.rec) {
			b.n = len(out) + 1
			out = append(out, b)
		}
	}
	return out
}

func printBroken(list []broken) {
	now, w := time.Now(), termWidth()
	for _, b := range list {
		r := b.rec
		kind := i18n.T("cli.broken.transcript_gone")
		switch {
		case b.dirGone && r.Repo != "":
			kind = i18n.T("cli.broken.worktree_gone")
		case b.dirGone:
			kind = i18n.T("cli.broken.dir_gone")
		}
		if b.live {
			kind += i18n.T("cli.broken.live")
		}
		fmt.Printf("%3d  %-6s  %s  %-5s  %s\n", b.n, r.Provider, shortID(r.SessionID), render.When(r.When(), now), render.Truncate(r.Title, w-30))
		fmt.Printf("     %s  %s", kind, paths.Tilde(r.Cwd))
		switch {
		case !b.dirGone:
		case len(b.found) == 1:
			fmt.Printf("  →  %s", paths.Tilde(b.found[0]))
		case len(b.found) > 1:
			found := make([]string, len(b.found))
			for i, p := range b.found {
				found[i] = paths.Tilde(p)
			}
			fmt.Print(i18n.T("cli.broken.ambiguous") + strings.Join(found, "  "))
		default:
			fmt.Print(i18n.T("cli.broken.not_found"))
		}
		fmt.Println()
	}
}

func printBrokenJSON(list []broken) error {
	type row struct {
		N              int      `json:"n"`
		Provider       string   `json:"provider"`
		SessionID      string   `json:"session_id"`
		ID             string   `json:"id,omitempty"`
		Title          string   `json:"title"`
		Cwd            string   `json:"cwd"`
		DirGone        bool     `json:"dir_gone"`
		TranscriptGone bool     `json:"transcript_gone"`
		Found          []string `json:"found,omitempty"`
		WorktreeOf     string   `json:"worktree_of,omitempty"` // the cwd was a git worktree of this main checkout
		Live           bool     `json:"live"`
	}
	rows := make([]row, 0, len(list))
	for _, b := range list {
		rows = append(rows, row{b.n, b.rec.Provider, b.rec.SessionID, b.rec.ID, b.rec.Title, b.rec.Cwd, b.dirGone, !b.dirGone, b.found, b.rec.Repo, b.live})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

func pickBroken(list []broken, args []string) ([]broken, error) {
	var out []broken
	for _, a := range args {
		if a == "all" {
			return list, nil
		}
		if n, err := strconv.Atoi(a); err == nil {
			if n < 1 || n > len(list) {
				return nil, i18n.E("cli.broken.bad_number", a)
			}
			out = append(out, list[n-1])
			continue
		}
		var hit *broken
		for i := range list {
			if strings.HasPrefix(list[i].rec.SessionID, a) || list[i].rec.ID == a {
				if hit != nil {
					return nil, i18n.E("cli.session_ambiguous", a)
				}
				hit = &list[i]
			}
		}
		if hit == nil {
			return nil, i18n.E("cli.record_not_found", a)
		}
		out = append(out, *hit)
	}
	return out, nil
}

// Positional args: a path separator means a directory, integers and "all" are selections, the rest is a query.
// Session-id prefixes are selections too but only recognisable after the scan, so they stay in rest for pickBroken.
func brokenArgs(pos []string) (dir string, sel, query []string, err error) {
	for _, a := range pos {
		switch {
		case strings.ContainsRune(a, filepath.Separator) || a == "." || a == ".." || a == "~":
			dir, err = filepath.Abs(paths.Expand(a))
		case a == "all" || isNumber(a):
			sel = append(sel, a)
		default:
			query = append(query, a)
		}
	}
	return dir, sel, query, err
}

func isNumber(s string) bool { _, err := strconv.Atoi(s); return err == nil }

// No status: / turns: filter by default: broken sessions should all show up.
func brokenQuery(words []string) fav.Query {
	q := fav.Parse(strings.Join(words, " "))
	q.All = true
	has := func(k string) bool {
		for _, w := range words {
			if strings.HasPrefix(strings.ToLower(w), k+":") {
				return true
			}
		}
		return false
	}
	if !has("status") {
		q.Status = "all"
	}
	if !has("turns") {
		q.Turns = 0
	}
	return q
}

func splitIDs(list []broken, query []string) (sel, rest []string) {
	for _, w := range query {
		hits := 0
		for _, b := range list {
			if strings.HasPrefix(b.rec.SessionID, w) || b.rec.ID == w {
				hits++
			}
		}
		if hits == 1 {
			sel = append(sel, w)
		} else {
			rest = append(rest, w)
		}
	}
	return sel, rest
}

func loadBroken(dir string) (*fav.Store, *index.Index, []broken, error) {
	s, err := openStore()
	if err != nil {
		return nil, nil, nil, err
	}
	idx, err := index.Open()
	if err != nil {
		return nil, nil, nil, err
	}
	idx, _ = idx.Refresh()
	return s, idx, scanBroken(s, idx, dir), nil
}

func loadPick(pos []string) (*fav.Store, *index.Index, []broken, []string, error) {
	dir, sel, query, err := brokenArgs(pos)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var plain, idLike []string
	for _, w := range query {
		if looksLikeID(w) {
			idLike = append(idLike, w)
		} else {
			plain = append(plain, w)
		}
	}
	s, idx, all, err := loadBroken(dir)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	list := filterBroken(all, brokenQuery(plain))
	ids, rest := splitIDs(list, idLike)
	for _, w := range rest {
		if len(w) >= 8 { // Looks like a full session id but matched nothing: a typo, not a keyword.
			return nil, nil, nil, nil, i18n.E("cli.record_not_found", w)
		}
	}
	if len(rest) > 0 {
		list = filterBroken(all, brokenQuery(append(plain, rest...)))
	}
	return s, idx, list, append(sel, ids...), nil
}

func looksLikeID(w string) bool {
	if len(w) < 4 {
		return false
	}
	for _, c := range w {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == '-') {
			return false
		}
	}
	return true
}

// Without a TTY and without -y, fail loudly instead of silently answering N with exit 0.
func confirm(prompt string, yes bool) bool {
	ok, _ := confirmErr(prompt, yes)
	return ok
}

func confirmErr(prompt string, yes bool) (bool, error) {
	if yes {
		return true, nil
	}
	if !term.IsTerminal(os.Stdin.Fd()) {
		return false, errors.New(i18n.T("cli.confirm.no_tty"))
	}
	fmt.Print(prompt)
	var ans string
	fmt.Scanln(&ans)
	return strings.EqualFold(strings.TrimSpace(ans), "y"), nil
}

func cmdClean(args []string) error {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	yes := fs.Bool("y", false, i18n.T("cli.rm.flag_yes"))
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	s, _, list, sel, err := loadPick(pos)
	if err != nil {
		return err
	}
	if *asJSON && len(sel) == 0 {
		return printBrokenJSON(list)
	}
	if len(list) == 0 {
		fmt.Println(i18n.T("cli.broken.none"))
		return nil
	}
	if len(sel) == 0 {
		printBroken(list)
		fmt.Print(i18n.T("cli.clean.hint"))
		return nil
	}
	picked, err := pickBroken(list, sel)
	if err != nil {
		return err
	}
	printBroken(picked)
	if ok, err := confirmErr(i18n.F("cli.clean.confirm", len(picked)), *yes); !ok {
		return err
	}
	done, skipped, failed := 0, 0, 0
	for _, b := range picked {
		if b.live {
			fmt.Print(i18n.F("cli.broken.skip_live", b.rec.Title))
			skipped++
			continue
		}
		n, err := trashSession(s, b.rec)
		if err != nil {
			fmt.Fprintln(os.Stderr, "  "+err.Error())
			failed++
			continue
		}
		fmt.Print(i18n.F("cli.rm.done", b.rec.Title, n))
		done++
	}
	fmt.Print(i18n.F("cli.broken.summary", done, skipped, failed))
	if failed > 0 {
		return i18n.E("cli.broken.failed", failed)
	}
	return nil
}

func cmdFix(args []string) error {
	fs := flag.NewFlagSet("fix", flag.ContinueOnError)
	yes := fs.Bool("y", false, i18n.T("cli.rm.flag_yes"))
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	to := fs.String("to", "", i18n.T("cli.fix.flag_to"))
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	s, idx, list, sel, err := loadPick(pos)
	if err != nil {
		return err
	}
	if *asJSON && len(sel) == 0 {
		return printBrokenJSON(list)
	}
	if len(list) == 0 {
		fmt.Println(i18n.T("cli.broken.none"))
		return nil
	}
	if len(sel) == 0 {
		printBroken(list)
		fmt.Print(i18n.T("cli.fix.hint"))
		return nil
	}
	picked, err := pickBroken(list, sel)
	if err != nil {
		return err
	}
	all := false
	for _, a := range sel {
		all = all || a == "all"
	}
	if all && *to != "" { // --to together with all would move the guessed ones into the same directory too.
		return errors.New(i18n.T("cli.fix.all_with_to"))
	}
	var skipped []broken
	if all {
		picked = picked[:0]
		for _, b := range list {
			if b.dirGone && (len(b.found) == 1 || *to != "") {
				picked = append(picked, b)
			} else {
				skipped = append(skipped, b)
			}
		}
	}
	for _, b := range picked {
		if !b.dirGone {
			return i18n.E("cli.fix.not_fixable", b.n)
		}
	}
	if *to != "" {
		if *to, err = filepath.Abs(paths.Expand(*to)); err != nil {
			return err
		}
		for i := range picked {
			picked[i].found = []string{*to}
		}
	}
	for _, b := range picked {
		if len(b.found) != 1 {
			return i18n.E("cli.fix.need_to", shortID(b.rec.SessionID))
		}
	}
	if len(skipped) > 0 {
		fmt.Print(i18n.F("cli.fix.skipped", len(skipped)))
		printBroken(skipped)
		fmt.Println()
	}
	if len(picked) == 0 {
		fmt.Print(i18n.F("cli.broken.summary", 0, len(skipped), 0))
		return nil
	}
	printBroken(picked)
	if ok, err := confirmErr(i18n.F("cli.fix.confirm", len(picked)), *yes); !ok {
		return err
	}
	done, failed := 0, 0
	for _, b := range picked {
		if b.live {
			fmt.Print(i18n.F("cli.broken.skip_live", b.rec.Title))
			skipped = append(skipped, b)
			continue
		}
		next, err := moveOne(s, idx, b.rec, b.found[0])
		idx = next
		if err != nil {
			fmt.Fprintln(os.Stderr, "  "+err.Error())
			failed++
			continue
		}
		done++
	}
	fmt.Print(i18n.F("cli.broken.summary", done, len(skipped), failed))
	if failed > 0 {
		return i18n.E("cli.broken.failed", failed)
	}
	return nil
}

func moveOne(s *fav.Store, idx *index.Index, r *fav.Rec, to string) (*index.Index, error) {
	plan, err := idx.PlanMove(s, capture.LiveSessions(), r.Cwd, to)
	if err != nil {
		return idx, err
	}
	plan.Only(r.Provider, r.SessionID)
	if len(plan.Live) > 0 {
		return idx, errors.New(i18n.T("cli.mv.refused"))
	}
	rep, err := plan.Apply(s)
	if err != nil {
		return idx, err
	}
	next, _ := idx.Rescan(rep.Touched)
	next.Save()
	fmt.Print(i18n.F("cli.fix.moved", render.Truncate(r.Title, 40), paths.Tilde(to)))
	return next, nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
