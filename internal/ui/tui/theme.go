package tui

import "github.com/charmbracelet/lipgloss"

// Colours. Backgrounds only on the selected card, primary buttons and warning boxes; the screen background is the terminal's.
var (
	cAccent = lipgloss.AdaptiveColor{Light: "#0e7490", Dark: "#52d7eb"}
	cText   = lipgloss.AdaptiveColor{Light: "#0f172a", Dark: "#dce7f5"}
	cMuted  = lipgloss.AdaptiveColor{Light: "#64748b", Dark: "#8ca4bd"}
	cFaint  = lipgloss.AdaptiveColor{Light: "#94a3b8", Dark: "#5b6b80"}
	cFrame  = lipgloss.AdaptiveColor{Light: "#cbd5e1", Dark: "#2b3c52"}
	cCard   = lipgloss.AdaptiveColor{Light: "#e2e8f0", Dark: "#233348"}
	cSelBg  = lipgloss.AdaptiveColor{Light: "#dbeafe", Dark: "#16314d"}
	cBtnBg  = lipgloss.AdaptiveColor{Light: "#bae6fd", Dark: "#214c70"}
	cOK     = lipgloss.AdaptiveColor{Light: "#15803d", Dark: "#76cfb0"}
	cWarn   = lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#f4ca7b"}
	cWarnBd = lipgloss.AdaptiveColor{Light: "#fcd34d", Dark: "#735b32"}
	cErr    = lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f87171"}
	cTag    = lipgloss.AdaptiveColor{Light: "#1d4ed8", Dark: "#79bdff"}

	// ⚠️ the overlay backdrop must be darker than cFrame or every border brightens when an overlay opens
	cBackdrop = lipgloss.AdaptiveColor{Light: "#94a3b8", Dark: "#2f3d50"}
)

var (
	accent    = lipgloss.NewStyle().Foreground(cAccent)
	dimmed    = lipgloss.NewStyle().Foreground(cMuted)
	faint     = lipgloss.NewStyle().Foreground(cFaint)
	plainSty  = lipgloss.NewStyle()
	frame     = lipgloss.NewStyle().Foreground(cFrame)
	boldSty   = lipgloss.NewStyle().Bold(true)
	okSty     = lipgloss.NewStyle().Foreground(cOK)
	warnSty   = lipgloss.NewStyle().Foreground(cWarn)
	errSty    = lipgloss.NewStyle().Foreground(cErr)
	brokenSty = lipgloss.NewStyle().Foreground(cErr).Faint(true) // the "unrecoverable" mark after a card title
	tagSty    = lipgloss.NewStyle().Foreground(cTag)
	hitSty    = lipgloss.NewStyle().Background(cWarn).Foreground(cText).Bold(true)
	// four tones for live sessions: working / waiting / idle / finished
	liveTone = []lipgloss.Style{accent, lipgloss.NewStyle().Foreground(cErr).Bold(true), dimmed, okSty}
	backSty  = lipgloss.NewStyle().Foreground(cBackdrop)

	selLine  = lipgloss.NewStyle().Background(cSelBg)
	selTitle = lipgloss.NewStyle().Background(cSelBg).Foreground(cAccent).Bold(true)
	selBody  = lipgloss.NewStyle().Background(cSelBg).Foreground(cText)

	panelSty = box(cFrame)
	cardSty  = box(cCard)
	cardSel  = fill(cAccent, cSelBg)
	chipSty  = box(cFrame).Foreground(cMuted)
	chipSel  = box(cAccent).Foreground(cAccent).Bold(true)
	btnSty   = box(cFrame).Foreground(cText)
	btnPri   = fill(cAccent, cBtnBg).Foreground(cAccent).Bold(true)
	btnFocus = fill(cText, cBtnBg).Foreground(cText).Bold(true)
	chipFoc  = fill(cAccent, cSelBg).Foreground(cAccent).Bold(true)
	warnBox  = box(cWarnBd).Foreground(cWarn)
	ovBox    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).Padding(0, 2)
)

func box(border lipgloss.TerminalColor) lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1)
}

// fill is a box with a background; border cells share it.
func fill(border, bg lipgloss.TerminalColor) lipgloss.Style {
	return box(border).Background(bg).BorderBackground(bg)
}

// ⚠️ no East Asian Ambiguous glyphs (★ ☆ → ▾ ▸) inline: their width varies and shifts the row; use Nerd Font PUA icons.
const (
	hRule    = "─"
	vBar     = "│"
	tabOpenL = "["
	tabOpenR = "]"
)
