package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fulltext"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/render"
)

// cmdGrep searches the messages of every session (or those the filter tokens pick) and lists the sessions holding every keyword.
func cmdGrep(args []string) error {
	fs := flag.NewFlagSet("grep", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	limit := fs.Int("limit", 0, i18n.T("cli.flag_limit"))
	flags, words := queryDashes(fs, args)
	rest, err := parseMixed(fs, flags)
	if err != nil {
		return err
	}
	query, _ := fulltext.Prefixed(strings.Join(append(rest, words...), " "))
	kw, scope := fulltext.Split(query)
	if kw == "" {
		return errors.New(i18n.T("cli.grep.usage"))
	}
	if fulltext.TooLong(kw) {
		return errors.New(i18n.T("msg.too_long"))
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	idx, err := index.Open()
	if err != nil {
		return err
	}
	idx = refreshed(idx)
	syncText(s, idx, 0, term.IsTerminal(os.Stderr.Fd()))
	recs, res := grep(s, idx, kw, scope)
	if *limit > 0 && len(res) > *limit {
		res = res[:*limit]
	}

	if *asJSON {
		type row struct {
			*fav.Rec
			Hits    int    `json:"hits"`
			Snippet string `json:"snippet"`
		}
		rows := make([]row, len(res))
		for i, x := range res {
			rows[i] = row{recs[x.Cand], x.Hits, x.Snippet}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	if fixes := fulltext.Expand(fulltext.Dir(), fulltext.ParseQuery(kw)).Fixes(); len(fixes) > 0 {
		fmt.Println(strings.TrimPrefix(i18n.F("msg.also", strings.Join(fixes, " ")), " · ") + "\n")
	}
	now := time.Now()
	w := min(termWidth(), 120)
	for _, x := range res {
		r := recs[x.Cand]
		hits := i18n.F("cli.grep.hits", x.Hits)
		fmt.Println(render.Truncate(r.Title, max(8, w-render.Width(hits)-2)) + "  " + hits)
		meta := render.Provider(r.Provider)
		if r.Project != "" {
			meta += " · " + r.Project
		}
		fmt.Println("  " + render.Truncate(meta+" · "+render.When(r.When(), now)+" · "+shortID(r.SessionID), w-2))
		for _, l := range render.Wrap(x.Snippet, w-4) {
			fmt.Println("    " + l)
		}
		fmt.Println()
	}
	fmt.Print(i18n.F("cli.items_count", len(res)))
	return nil
}

// grep runs a message search over the sessions scope picks; results index into the returned records.
func grep(s *fav.Store, idx *index.Index, kw, scope string) ([]*fav.Rec, []fulltext.Result) {
	recs := sessionRecs(s, idx, scope, "")
	return recs, fulltext.Search(context.Background(), fulltext.Dir(), fulltext.Cands(recs, idx.PathsBySession()), kw)
}

// syncText brings the full-text store up to date within budget (0 = no limit); false: the budget cut it short.
func syncText(s *fav.Store, idx *index.Index, budget time.Duration, show bool) bool {
	ctx := context.Background()
	if budget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, budget)
		defer cancel()
	}
	shown := false
	if len(idx.Paths()) == 0 { // ⚠️ an index not built yet would make Update drop every text file
		return true
	}
	paths := fulltext.Sources(idx.Paths(), s.All())
	p, err := fulltext.UpdateWith(ctx, fulltext.Dir(), paths, fulltext.Options{OutLines: loadConfig().ToolOutput}, func(p fulltext.Progress) {
		if show && p.Total >= 20 {
			fmt.Fprint(os.Stderr, "\r"+i18n.F("cli.grep.indexing", p.Done, p.Total))
			shown = true
		}
	})
	if shown {
		fmt.Fprintln(os.Stderr)
	}
	if errors.Is(err, fulltext.ErrBusy) {
		if show {
			fmt.Fprintln(os.Stderr, i18n.T("cli.grep.busy"))
		}
		return true // another fav is on it
	}
	return err != nil || p.Done == p.Total
}

// cmdTextSync finishes a full-text update an fzf keystroke started (hidden; run detached).
func cmdTextSync() error {
	s, err := openStore()
	if err != nil {
		return err
	}
	idx, err := index.Open()
	if err != nil {
		return err
	}
	syncText(s, refreshed(idx), 0, false)
	return nil
}

// finishTextInBackground runs fav text-sync detached, so the store keeps building after this fzf-list exits.
func finishTextInBackground() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	c := exec.Command(exe, "text-sync")
	detach(c)
	if c.Start() == nil {
		c.Process.Release()
	}
}

// queryDashes takes -x words (message-search exclusions) out of args, leaving the flags fs defines to it.
func queryDashes(fs *flag.FlagSet, args []string) (flags, words []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, _, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		f := fs.Lookup(name)
		switch {
		case !strings.HasPrefix(a, "-") || a == "-":
			flags = append(flags, a)
		case f == nil:
			words = append(words, a)
		default:
			flags = append(flags, a)
			if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !hasValue && !(ok && b.IsBoolFlag()) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		}
	}
	return flags, words
}
