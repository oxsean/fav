package main

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/render"
)

const cleanupShown = 10

// cleanup thresholds; vars so tests can shrink them
var (
	bigTranscript int64 = 100 << 20 // bytes
	staleAfter          = 30 * 24 * time.Hour
)

// printIdleAgents: running sessions whose transcript has not been written for capture.IdleAfter.
func printIdleAgents(s *fav.Store, idx *index.Index, live map[string]capture.Live) {
	now := time.Now()
	type row struct {
		title, where string
		idle         time.Duration
	}
	var rows []row
	for id, l := range live {
		path := idx.Transcript(id)
		st, err := os.Stat(path)
		if path == "" || err != nil || now.Sub(st.ModTime()) < capture.IdleAfter {
			continue
		}
		title := l.Title
		if f := idx.FileByPrefix(id); f != nil && f.Title != "" {
			title = f.Title
		}
		if r := s.BySession(l.Agent, id); r != nil {
			title = r.Title
		}
		where := i18n.T("cli.doctor.idle_terminal")
		switch {
		case l.TabID != "":
			where = "Herdr tab " + l.TabID
		case l.BackgroundID != "":
			where = i18n.F("cli.doctor.idle_background", l.BackgroundID)
		}
		rows = append(rows, row{title, where, now.Sub(st.ModTime())})
	}
	if len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].idle > rows[j].idle })
	fmt.Print(i18n.F("cli.doctor.idle", len(rows), int(capture.IdleAfter.Hours())))
	for _, r := range rows {
		fmt.Printf("  %-6s  %s  %s\n", render.ShortDur(r.idle), render.Truncate(r.title, 50), r.where)
	}
	fmt.Print(i18n.T("cli.doctor.idle_hint"))
}

// printBigStale: transcripts over bigTranscript untouched for staleAfter, not favorited, pinned or running — trash candidates.
func printBigStale(s *fav.Store, idx *index.Index, live map[string]capture.Live) {
	now := time.Now()
	var big []*index.Session
	var total int64
	for _, ss := range idx.Sessions() {
		if ss.Size < bigTranscript || now.Sub(ss.LastAt) < staleAfter {
			continue
		}
		if r := s.BySession(ss.Provider, ss.SessionID); r != nil && (r.Favorite() || r.PinnedPath != "") {
			continue
		}
		if _, ok := live[ss.SessionID]; ok {
			continue
		}
		big = append(big, ss)
		total += ss.Size
	}
	if len(big) == 0 {
		return
	}
	sort.Slice(big, func(i, j int) bool { return big[i].Size > big[j].Size })
	fmt.Print(i18n.F("cli.doctor.big", len(big), bigTranscript>>20, int(staleAfter.Hours()/24), total>>20))
	for _, ss := range big[:min(cleanupShown, len(big))] {
		fmt.Printf("  %5d MB  %s  fav rm %s   %s\n", ss.Size>>20, render.WhenFull(ss.LastAt), shortID(ss.SessionID), render.Truncate(ss.DisplayTitle(), 40))
	}
	if len(big) > cleanupShown {
		fmt.Print(i18n.F("cli.doctor.big_more", len(big)-cleanupShown))
	}
	fmt.Print(i18n.T("cli.doctor.big_hint"))
}
