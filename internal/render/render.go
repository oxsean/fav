package render

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
)

// Sep separates the hidden id from the visible part of an fzf row (--delimiter, --with-nth=2).
const Sep = "\t"

// unfavorited → o; favorited → * / ✓ by status
func glyph(r *fav.Rec) string {
	if !r.Favorite() {
		return GlyphSession
	}
	return Glyph(r.Status)
}

func Glyph(status string) string {
	switch status {
	case fav.StatusDone:
		return GlyphDone
	}
	return GlyphActive
}

func StatusLabel(s string) string {
	switch s {
	case fav.StatusTodo:
		return i18n.T("status.todo")
	case fav.StatusDone:
		return i18n.T("status.done")
	case "":
		return i18n.T("status.unmarked")
	}
	return i18n.T("status.active")
}

var RelativeTime = true

var weekdays = [...]string{"time.wd.sun", "time.wd.mon", "time.wd.tue", "time.wd.wed", "time.wd.thu", "time.wd.fri", "time.wd.sat"}

// When is the time on rows and cards. Relative: today 16:53 / yesterday 16:53 / Wed 16:53 (within 7 days) / 09-12 (this year) / 2025-12-01.
func When(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	if !RelativeTime {
		return WhenFull(t)
	}
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	switch {
	case !t.Before(today) && t.Before(today.AddDate(0, 0, 1)):
		return t.Format("15:04")
	case !t.Before(today.AddDate(0, 0, -1)) && t.Before(today):
		return i18n.T("time.yesterday_prefix") + t.Format("15:04")
	case !t.Before(today.AddDate(0, 0, -6)) && t.Before(today):
		return i18n.T(weekdays[t.Weekday()]) + " " + t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("01-02")
	}
	return t.Format("2006-01-02")
}

func WhenFull(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04")
}

func DayLabel(t, now time.Time) string {
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	date := t.Format("2006-01-02")
	switch {
	case !t.Before(today):
		return i18n.T("time.today_prefix") + date
	case !t.Before(today.AddDate(0, 0, -1)):
		return i18n.T("time.yesterday_prefix") + date
	}
	return date
}

// Line is an fzf row: the key (record id, or session id when unfavorited), a Tab, then the visible part.
func Line(r *fav.Rec, now time.Time) string {
	turns := ""
	if r.Turns > 0 {
		turns = i18n.F("line.turns", r.Turns)
	}
	return line(r, now, PadLeft(turns, 7))
}

// LiveLine is the Agents row: the turns column shows the live status instead.
func LiveLine(r *fav.Rec, l capture.Live, now time.Time) string {
	return line(r, now, Pad(LiveText(l, now), 14))
}

func line(r *fav.Rec, now time.Time, col string) string {
	var b strings.Builder
	b.WriteString(LineKey(r))
	b.WriteString(Sep)
	b.WriteString(glyph(r))
	b.WriteByte(' ')
	b.WriteString(Pad(When(r.When(), now), 11))
	b.WriteByte(' ')
	b.WriteString(dim.p(col))
	b.WriteByte(' ')
	b.WriteString(Pad(r.Title, 48))
	b.WriteString("  ")

	meta := providerShort(r.Provider)
	if r.Project != "" {
		meta += " · " + r.Project
	}
	b.WriteString(dim.p(Pad(meta, 22)))

	if len(r.Tags) > 0 {
		b.WriteString("  ")
		b.WriteString(blue.p(tagString(r.Tags)))
	}
	return b.String()
}

func LineKey(r *fav.Rec) string {
	if r.ID != "" {
		return r.ID
	}
	return r.SessionID
}

// LiveText is the status of a running session, shared by the TUI card and the fzf row.
func LiveText(l capture.Live, now time.Time) string {
	text := GlyphLive + i18n.T("live.suffix_running")
	switch l.Status {
	case "blocked":
		text = GlyphWarn + i18n.T("live.suffix_waiting")
	case "idle":
		text = GlyphSession + i18n.F("live.suffix_idle", ShortDur(now.Sub(l.Since)))
	case "done":
		text = GlyphDone + i18n.T("live.suffix_finished")
	case "working":
		text = GlyphLive + i18n.T("live.suffix_working")
	}
	if l.BackgroundID != "" {
		text += i18n.T("live.suffix_background")
	}
	return text
}

func ShortDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return i18n.T("time.just_now")
	case d < time.Hour:
		return i18n.F("time.minutes", int(d.Minutes()))
	case d < 24*time.Hour:
		return i18n.F("time.hours", int(d.Hours()))
	}
	return i18n.F("time.days", int(d.Hours()/24))
}

func Card(r *fav.Rec, width int, now time.Time) []string {
	when := When(r.When(), now)
	head := glyph(r) + " " + r.Title
	gap := width - Width(when) - 1
	line1 := Pad(head, gap) + " " + when

	meta := providerShort(r.Provider)
	if r.Project != "" {
		meta += " · " + r.Project
	}
	out := []string{line1, "  " + dim.p(Truncate(meta, width-2))}
	if len(r.Tags) > 0 {
		out = append(out, "  "+blue.p(Truncate(tagString(r.Tags), width-2)))
	}
	return out
}

// Preview is shared by the fzf preview window and the TUI right pane; it never shows env var values.
func Preview(r *fav.Rec, width int, now time.Time) string {
	if width < 20 {
		width = 20
	}
	var b strings.Builder
	line := func(s string) { b.WriteString(s); b.WriteByte('\n') }

	line(bold.p(Truncate(r.Title, width)))
	line(statusLine(r, now))
	line("")

	if r.Summary != "" {
		label := i18n.T("card.summary")
		switch {
		case r.ID == "" && r.Recap && r.Provider == fav.ProviderCodex:
			label = i18n.T("detail.recap_codex")
		case r.ID == "" && r.Recap:
			label = i18n.T("detail.recap_claude")
		}
		line(cyan.p(label))
		for _, l := range Wrap(r.Summary, width) {
			line(l)
		}
		line("")
	}

	const labelW = 12
	field := func(k, v string) {
		if v != "" {
			line(dim.p(Pad(k, labelW)) + Truncate(v, width-labelW))
		}
	}
	field(GlyphProject+i18n.T("card.project"), r.Project)
	field(GlyphTerm+i18n.T("card.type"), r.WorkType)
	field(GlyphBranch+i18n.T("card.branch"), r.GitBranch)
	field(GlyphDir+i18n.T("card.directory"), shortenHome(r.Cwd))
	if r.Repo != r.Cwd {
		field(GlyphDir+i18n.T("card.repo"), shortenHome(r.Repo))
	}
	field(GlyphEdit+i18n.T("card.files"), FilesField(r, 6))
	field(GlyphSession+i18n.T("card.session"), r.SessionID)
	if r.HerdrWorkspace != "" {
		herdr := r.HerdrWorkspace
		if r.HerdrTab != "" {
			herdr += "  " + GlyphArrow + "  " + r.HerdrTab
		}
		field(GlyphHerdr+" Herdr", herdr)
	}
	if len(r.Tags) > 0 {
		field(GlyphTag+i18n.T("card.tags"), tagString(r.Tags))
	}
	field(GlyphClock+i18n.T("card.resume"), resumeInfo(r, now))
	field(GlyphClock+i18n.T("card.last_activity"), ActivityLine(r))
	line("")

	line(cyan.p(i18n.T("card.resume_target")))
	line(Truncate(resumeTarget(r), width))
	line("")

	for _, c := range capture.Checks(r) {
		switch {
		case c.OK:
			line(green.p(GlyphOK + " " + Truncate(c.Text, width-2)))
		case c.Warn:
			for i, l := range Wrap(GlyphWarn+" "+c.Text, width) {
				if i == 0 {
					line(yellow.p(l))
				} else {
					line(yellow.p("  " + l))
				}
			}
		default:
			for i, l := range Wrap(GlyphWarn+" "+c.Text, width) {
				if i == 0 {
					line(red.p(l))
				} else {
					line(red.p("  " + l))
				}
			}
		}
	}
	return b.String()
}

func statusLine(r *fav.Rec, now time.Time) string {
	state := green.p(glyph(r) + " " + StatusLabel(r.Status))
	if r.Done() || r.Status == "" {
		state = dim.p(glyph(r) + " " + StatusLabel(r.Status))
	}
	if r.Archived() {
		state += dim.p("  " + GlyphArchive + i18n.T("card.archived"))
	}
	if !r.Favorite() {
		state += dim.p(i18n.T("card.not_favorited"))
	}
	if r.PinnedPath != "" {
		state += cyan.p("  " + GlyphPinned + i18n.T("detail.pinned"))
	}
	return state + dim.p("  ·  "+providerLabel(r.Provider)+"  ·  "+When(r.When(), now))
}

func resumeInfo(r *fav.Rec, now time.Time) string {
	if r.ResumeCount == 0 {
		return ""
	}
	s := i18n.F("card.resume_times", r.ResumeCount)
	if r.LastResumedAt != nil {
		s += i18n.F("card.resume_last", When(*r.LastResumedAt, now))
	}
	return s
}

func resumeTarget(r *fav.Rec) string {
	target := providerLabel(r.Provider)
	dir := shortenHome(r.Cwd)
	if dir == "" {
		dir = i18n.T("resume.where.cwd")
	}
	if r.HerdrWorkspace != "" {
		return "Herdr " + r.HerdrWorkspace + " " + GlyphArrow + " " + i18n.T("resume.where.new_tab") + " " + GlyphArrow + " " + dir + " " + GlyphArrow + " " + target
	}
	return i18n.T("resume.where.terminal") + " " + GlyphArrow + " " + dir + " " + GlyphArrow + " " + target
}

func tagString(tags []string) string {
	var b strings.Builder
	for i, t := range tags {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('#')
		b.WriteString(t)
	}
	return b.String()
}

// Provider is the short provider name shown on cards: Claude / Codex.
func Provider(p string) string { return providerShort(p) }

func providerShort(p string) string {
	switch p {
	case fav.ProviderClaude:
		return "Claude"
	case fav.ProviderCodex:
		return "Codex"
	}
	return p
}

func providerLabel(p string) string {
	switch p {
	case fav.ProviderClaude:
		return "Claude Code"
	case fav.ProviderCodex:
		return "Codex CLI"
	}
	return p
}

var home, _ = os.UserHomeDir()

// FileList: "a.go ×3  ·  b.go" with paths under base written relative to it.
func FileList(fs []fav.FileCount, base string) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		p := shortenHome(f.Path)
		if base != "" {
			if rel, err := filepath.Rel(base, f.Path); err == nil && !strings.HasPrefix(rel, "..") {
				p = rel
			}
		}
		if f.N > 1 {
			p += " ×" + strconv.Itoa(f.N)
		}
		parts[i] = p
	}
	return strings.Join(parts, "  ·  ")
}

// FilesField is the detail line for the files r's AI wrote: how many, the most written first.
func FilesField(r *fav.Rec, n int) string {
	if len(r.Files) == 0 {
		return ""
	}
	base := r.Cwd
	if base == "" {
		base = r.Repo
	}
	return i18n.F("card.files_value", len(r.Files), FileList(fav.TopFiles(r.Files, n), base))
}

func shortenHome(p string) string {
	if p != "" && home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func ActivityLine(r *fav.Rec) string {
	for _, p := range []string{r.PinnedPath, r.TranscriptPath} {
		if p == "" {
			continue
		}
		a, ok := capture.LastActivity(p)
		if !ok {
			continue
		}
		s := a.ModTime.Format("2006-01-02 15:04")
		if a.Last.Text != "" {
			s += "  " + strings.Join(strings.Fields(a.Last.Text), " ")
		}
		return s
	}
	return ""
}

// Chat has the same shape as the TUI right pane: a header (who · time · length) and at most bodyRows lines of body per message.
func Chat(msgs []capture.Message, width, bodyRows int) []string {
	var out []string
	for _, msg := range msgs {
		who, sty := i18n.T("chat.you"), cyan
		if msg.Role != "user" {
			who, sty = "AI", dim
		}
		out = append(out, sty.p(bold.p(who))+dim.p("  ·  "+msg.At.Format("01-02 15:04")+"  "+GlyphChars+" "+Chars(msg.Chars)))
		body := Wrap(strings.NewReplacer("**", "", "`", "").Replace(msg.Text), width-2)
		if len(body) > bodyRows {
			body = body[:bodyRows]
			if last := &body[bodyRows-1]; !strings.HasSuffix(*last, "…") {
				*last = Truncate(*last, width-4) + " …"
			}
		}
		for _, l := range body {
			out = append(out, "  "+l)
		}
		out = append(out, "")
	}
	return out
}

func Chars(n int) string {
	if n >= 1000 {
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
	}
	return strconv.Itoa(n)
}
