package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
)

const (
	reportFiles   = 5
	reportCommits = 3  // subjects shown per project
	longSession   = 20 // turns: an unfavorited session this long is worth a /fav summary
)

// periodStart: today = local midnight, week = Monday this week.
func periodStart(period string, now time.Time) time.Time {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if period == "week" {
		return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	}
	return day
}

type reportProject struct {
	Name     string          `json:"name"`
	Dir      string          `json:"dir,omitempty"`
	Sessions []*fav.Rec      `json:"-"`
	Files    []fav.FileCount `json:"files,omitempty"`
	Commits  []string        `json:"commits,omitempty"`
}

// cmdReport: the sessions active today / this week, by project, with the files they wrote and the commits made in
// that project since; long unfavorited sessions are listed for a /fav summary.
func cmdReport(period string, args []string) error {
	fs := newFlags(period)
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
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
	now := time.Now()
	since := periodStart(period, now)
	expr := strings.Join(rest, " ")
	q := fav.Parse(expr)
	if q.Status == fav.StatusOpen && !strings.Contains(expr, "status:") {
		expr += " status:all"
	}
	if !strings.Contains(expr, "turns:") {
		expr += " turns:1"
	}
	expr += " last:" + since.Format("2006-01-02")
	recs := sessionRecs(s, refreshed(idx), expr, "")
	projects := groupReport(recs, since)

	if *asJSON {
		return printReportJSON(period, since, recs, projects)
	}
	printReport(period, since, now, recs, projects)
	return nil
}

func groupReport(recs []*fav.Rec, since time.Time) []*reportProject {
	by := map[string]*reportProject{}
	var out []*reportProject
	for _, r := range recs {
		name := r.Project
		if name == "" {
			name = i18n.T("group.no_project")
		}
		p := by[name]
		if p == nil {
			p = &reportProject{Name: name}
			by[name] = p
			out = append(out, p)
		}
		p.Sessions = append(p.Sessions, r)
	}
	for _, p := range out {
		p.Dir = reportDir(p.Sessions)
		files := map[string]int{}
		for _, r := range p.Sessions {
			for f, n := range r.Files {
				files[f] += n
			}
		}
		p.Files = fav.TopFiles(files, reportFiles)
		if p.Dir != "" {
			if log := capture.GitOut(p.Dir, "log", "--since="+since.Format(time.RFC3339), "--no-merges", "--format=%s"); log != "" {
				p.Commits = strings.Split(log, "\n")
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].Sessions) > len(out[j].Sessions) })
	return out
}

// reportDir: the project's main checkout, else its most used directory that still exists.
func reportDir(recs []*fav.Rec) string {
	n := map[string]int{}
	for _, r := range recs {
		d := r.Cwd
		if r.Repo != "" {
			d = r.Repo
		}
		n[d]++
	}
	best := ""
	for d, c := range n {
		if d != "" && paths.IsDir(d) && (best == "" || c > n[best] || c == n[best] && d < best) {
			best = d
		}
	}
	return best
}

func printReport(period string, since, now time.Time, recs []*fav.Rec, projects []*reportProject) {
	w := min(termWidth(), 120)
	files := map[string]bool{}
	fresh := 0
	for _, r := range recs {
		for f := range r.Files {
			files[f] = true
		}
		if !r.When().Before(since) {
			fresh++
		}
	}
	header := "report.header_today"
	if period == "week" {
		header = "report.header_week"
	}
	fmt.Println(i18n.F(header, since.Format("01-02"), len(recs), fresh, len(files)))
	if len(recs) == 0 {
		return
	}
	for _, p := range projects {
		fmt.Println()
		head := i18n.F("report.project", p.Name, len(p.Sessions))
		if p.Dir != "" {
			head += "  " + paths.Tilde(p.Dir)
		}
		fmt.Println(head)
		for _, r := range p.Sessions {
			when := render.When(r.ActiveAt(), now)
			meta := i18n.F("report.session_meta", providerName(r.Provider), r.Turns)
			title := render.Truncate(r.Title, max(10, w-render.Width(meta)-render.Width(when)-8))
			fmt.Printf("  %s %s  %s  %s\n", reportGlyph(r), title, meta, when)
		}
		if len(p.Files) > 0 {
			fmt.Println("  " + render.Truncate(i18n.T("report.files")+render.FileList(p.Files, p.Dir), w-2))
		}
		if len(p.Commits) > 0 {
			shown := p.Commits[:min(reportCommits, len(p.Commits))]
			fmt.Println("  " + render.Truncate(i18n.F("report.commits", len(p.Commits), strings.Join(shown, "  ·  ")), w-2))
		}
	}
	var long []*fav.Rec
	for _, r := range recs {
		if !r.Favorite() && r.Turns >= longSession {
			long = append(long, r)
		}
	}
	if len(long) > 0 {
		fmt.Println()
		fmt.Println(i18n.F("report.unsaved", len(long), longSession))
		for _, r := range long[:min(5, len(long))] {
			fmt.Printf("  fav open %s   %s\n", shortID(r.SessionID), render.Truncate(r.Title, w-30))
		}
	}
}

func reportGlyph(r *fav.Rec) string {
	switch {
	case r.Favorite() && r.Done():
		return render.GlyphDone
	case r.Favorite():
		return render.GlyphActive
	}
	return render.GlyphSession
}

func providerName(p string) string {
	switch p {
	case fav.ProviderClaude:
		return "Claude"
	case fav.ProviderCodex:
		return "Codex"
	}
	return p
}

func printReportJSON(period string, since time.Time, recs []*fav.Rec, projects []*reportProject) error {
	type session struct {
		ID        string    `json:"id,omitempty"`
		SessionID string    `json:"session_id"`
		Provider  string    `json:"provider"`
		Title     string    `json:"title"`
		Turns     int       `json:"turns"`
		ActiveAt  time.Time `json:"active_at"`
		Favorite  bool      `json:"favorite"`
		Status    string    `json:"status,omitempty"`
		Cwd       string    `json:"cwd,omitempty"`
	}
	type project struct {
		*reportProject
		Sessions []session `json:"sessions"`
	}
	out := struct {
		Period   string    `json:"period"`
		Since    time.Time `json:"since"`
		Projects []project `json:"projects"`
	}{Period: period, Since: since, Projects: []project{}}
	for _, p := range projects {
		pj := project{reportProject: p}
		for _, r := range p.Sessions {
			pj.Sessions = append(pj.Sessions, session{r.ID, r.SessionID, r.Provider, r.Title, r.Turns, r.ActiveAt(), r.Favorite(), r.Status, r.Cwd})
		}
		out.Projects = append(out.Projects, pj)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
