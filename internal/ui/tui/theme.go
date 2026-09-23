package tui

import (
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

// Colours as light / dark pairs; setTheme picks one side once the terminal reports its background. Backgrounds only on
// the selected card, primary buttons and warning boxes; the screen background is the terminal's.
type pair struct{ light, dark string }

var (
	pAccent = pair{"#0e7490", "#52d7eb"}
	pText   = pair{"#0f172a", "#dce7f5"}
	pMuted  = pair{"#64748b", "#8ca4bd"}
	pFaint  = pair{"#94a3b8", "#5b6b80"}
	pFrame  = pair{"#cbd5e1", "#2b3c52"}
	pCard   = pair{"#e2e8f0", "#233348"}
	pSelBg  = pair{"#dbeafe", "#16314d"}
	pBtnBg  = pair{"#bae6fd", "#214c70"}
	pOK     = pair{"#15803d", "#76cfb0"}
	pWarn   = pair{"#b45309", "#f4ca7b"}
	pWarnBd = pair{"#fcd34d", "#735b32"}
	pErr    = pair{"#b91c1c", "#f87171"}
	pTag    = pair{"#1d4ed8", "#79bdff"}
	// ⚠️ the overlay backdrop must be darker than pFrame or every border brightens when an overlay opens
	pBackdrop = pair{"#94a3b8", "#2f3d50"}
)

var (
	cAccent, cText, cMuted, cFaint, cFrame, cCard, cSelBg, cBtnBg, cOK, cWarn, cWarnBd, cErr, cTag, cBackdrop color.Color

	accent, dimmed, faint, plainSty, frame, boldSty, okSty, warnSty, errSty, brokenSty, tagSty, hitSty, backSty lipgloss.Style
	liveTone                                                                                                    []lipgloss.Style // working / waiting / idle / finished
	selLine, selTitle, selBody                                                                                  lipgloss.Style
	panelSty, cardSty, cardSel, chipSty, chipSel, btnSty, noticeSty, btnKey, btnText, btnPri, btnFocus          lipgloss.Style
	chipFoc, warnBox, ovBox                                                                                     lipgloss.Style
	darkTheme                                                                                                   bool
)

func init() { setTheme(true) }

func setTheme(dark bool) {
	darkTheme = dark
	pick := func(p pair) color.Color {
		if dark {
			return lipgloss.Color(p.dark)
		}
		return lipgloss.Color(p.light)
	}
	cAccent, cText, cMuted, cFaint, cFrame, cCard = pick(pAccent), pick(pText), pick(pMuted), pick(pFaint), pick(pFrame), pick(pCard)
	cSelBg, cBtnBg, cOK, cWarn, cWarnBd, cErr, cTag, cBackdrop = pick(pSelBg), pick(pBtnBg), pick(pOK), pick(pWarn), pick(pWarnBd), pick(pErr), pick(pTag), pick(pBackdrop)

	accent = lipgloss.NewStyle().Foreground(cAccent)
	dimmed = lipgloss.NewStyle().Foreground(cMuted)
	faint = lipgloss.NewStyle().Foreground(cFaint)
	plainSty = lipgloss.NewStyle()
	frame = lipgloss.NewStyle().Foreground(cFrame)
	boldSty = lipgloss.NewStyle().Bold(true)
	okSty = lipgloss.NewStyle().Foreground(cOK)
	warnSty = lipgloss.NewStyle().Foreground(cWarn)
	errSty = lipgloss.NewStyle().Foreground(cErr)
	brokenSty = lipgloss.NewStyle().Foreground(cErr).Faint(true) // the "unrecoverable" mark after a card title
	tagSty = lipgloss.NewStyle().Foreground(cTag)
	hitSty = lipgloss.NewStyle().Background(cWarn).Foreground(cText).Bold(true)
	liveTone = []lipgloss.Style{accent, lipgloss.NewStyle().Foreground(cErr).Bold(true), dimmed, okSty}
	backSty = lipgloss.NewStyle().Foreground(cBackdrop)

	selLine = lipgloss.NewStyle().Background(cSelBg)
	selTitle = lipgloss.NewStyle().Background(cSelBg).Foreground(cAccent).Bold(true)
	selBody = lipgloss.NewStyle().Background(cSelBg).Foreground(cText)

	panelSty = box(cFrame)
	cardSty = box(cCard)
	cardSel = fill(cAccent, cSelBg)
	chipSty = box(cFrame).Foreground(cMuted)
	chipSel = box(cAccent).Foreground(cAccent).Bold(true)
	btnSty = box(cFrame).Foreground(cText)
	noticeSty = lipgloss.NewStyle().Background(cBtnBg).Foreground(cAccent).Bold(true)
	btnKey = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	btnText = lipgloss.NewStyle().Foreground(cText)
	btnPri = fill(cAccent, cBtnBg).Foreground(cAccent).Bold(true)
	btnFocus = fill(cText, cBtnBg).Foreground(cText).Bold(true)
	chipFoc = fill(cAccent, cSelBg).Foreground(cAccent).Bold(true)
	warnBox = box(cWarnBd).Foreground(cWarn)
	ovBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).Padding(0, 2)
}

func box(border color.Color) lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1)
}

// fill is a box with a background; border cells share it.
func fill(border, bg color.Color) lipgloss.Style {
	return box(border).Background(bg).BorderBackground(bg)
}

// ⚠️ no East Asian Ambiguous glyphs (★ ☆ → ▾ ▸) inline: their width varies and shifts the row; use Nerd Font PUA icons.
const (
	hRule    = "─"
	vBar     = "│"
	tabOpenL = "["
	tabOpenR = "]"
)

// newInput: a text input with plain prompt and text, a dim placeholder and a reverse-video cursor in the text colour.
func newInput() textinput.Model {
	ti := textinput.New()
	ti.SetStyles(inputStyles())
	return ti
}

func inputStyles() textinput.Styles {
	st := textinput.DefaultStyles(darkTheme)
	for _, s := range []*textinput.StyleState{&st.Focused, &st.Blurred} {
		s.Prompt, s.Text = lipgloss.NewStyle(), lipgloss.NewStyle()
	}
	st.Cursor.Color = lipgloss.NoColor{}
	return st
}

func newArea() textarea.Model {
	ta := textarea.New()
	st := textarea.DefaultStyles(darkTheme)
	st.Cursor.Color = lipgloss.NoColor{}
	ta.SetStyles(st)
	return ta
}

// inputView: ⚠️ bubbles pads a placeholder wider than its rune count (CJK) with NUL runes, which reach the terminal.
func inputView(ti textinput.Model) string { return strings.ReplaceAll(ti.View(), "\x00", "") }

type themeTimeoutMsg struct{}

// askTheme asks for the background colour and, in the same write, for the device attributes every terminal answers: a
// terminal replies in order, so the attributes arriving first mean no background reply is coming. The timeout covers
// a terminal that answers neither.
func askTheme() tea.Cmd {
	return tea.Batch(
		tea.Raw(ansi.RequestBackgroundColor+ansi.RequestPrimaryDeviceAttributes),
		tea.Tick(time.Second, func(time.Time) tea.Msg { return themeTimeoutMsg{} }),
	)
}
