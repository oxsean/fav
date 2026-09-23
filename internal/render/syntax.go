package render

import (
	"strings"

	"github.com/oxsean/fav/internal/i18n"
)

// SyntaxRow is one form of the search syntax and what it does.
type SyntaxRow struct{ Form, Meaning string }

type SyntaxSection struct {
	Title string
	Rows  []SyntaxRow
}

// SearchSyntax is the query language of the search box and `fav grep`: session filters, then message search.
func SearchSyntax() []SyntaxSection {
	rows := func(pairs ...[2]string) []SyntaxRow {
		out := make([]SyntaxRow, len(pairs))
		for i, p := range pairs {
			out[i] = SyntaxRow{i18n.T(p[0]), i18n.T(p[1])}
		}
		return out
	}
	return []SyntaxSection{
		{i18n.T("syntax.sessions"), rows(
			[2]string{"syntax.words", "syntax.words_do"},
			[2]string{"syntax.tag", "syntax.tag_do"},
			[2]string{"syntax.project", "syntax.project_do"},
			[2]string{"syntax.provider", "syntax.provider_do"},
			[2]string{"syntax.status", "syntax.status_do"},
			[2]string{"syntax.file", "syntax.file_do"},
			[2]string{"syntax.turns", "syntax.turns_do"},
			[2]string{"syntax.last", "syntax.last_do"},
			[2]string{"syntax.after", "syntax.after_do"},
			[2]string{"syntax.when", "syntax.when_do"},
		)},
		{i18n.T("syntax.messages"), rows(
			[2]string{"syntax.msg_words", "syntax.msg_words_do"},
			[2]string{"syntax.quote", "syntax.quote_do"},
			[2]string{"syntax.either", "syntax.either_do"},
			[2]string{"syntax.exclude", "syntax.exclude_do"},
			[2]string{"syntax.who", "syntax.who_do"},
			[2]string{"syntax.filters", "syntax.filters_do"},
			[2]string{"syntax.example", "syntax.example_do"},
		)},
	}
}

// SyntaxText is SearchSyntax as plain aligned text for a terminal.
func SyntaxText() string {
	var b strings.Builder
	for i, sec := range SearchSyntax() {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(sec.Title + "\n")
		w := 0
		for _, r := range sec.Rows {
			w = max(w, Width(r.Form))
		}
		for _, r := range sec.Rows {
			b.WriteString("  " + Pad(r.Form, w+2) + r.Meaning + "\n")
		}
	}
	return b.String()
}
