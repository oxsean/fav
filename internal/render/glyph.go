package render

import "os"

// ASCII by default; icons=nerd / FAV_ICONS=nerd switches to Nerd Font PUA (Font Awesome U+F000–F2E0, Powerline U+E0A0),
// which renders 2 cells wide without the font. ⚠️ Code points, not literals: editors and transports drop PUA characters silently.
var (
	GlyphActive  = string(rune(0xf006)) // star_o              active
	GlyphDone    = string(rune(0xf058)) // check_circle        done
	GlyphPinned  = string(rune(0xf08d)) // thumb_tack          pinned
	GlyphArchive = string(rune(0xf187)) // archive             archived
	GlyphArrow   = string(rune(0xf178)) // long_arrow_right
	GlyphOpen    = string(rune(0xf078)) // chevron_down        group open
	GlyphClosed  = string(rune(0xf054)) // chevron_right       group closed
	GlyphOK      = string(rune(0xf00c)) // check
	GlyphWarn    = string(rune(0xf071)) // exclamation_triangle
	GlyphErr     = string(rune(0xf057)) // times_circle
	GlyphSearch  = string(rune(0xf002)) // search
	GlyphProject = string(rune(0xf1b2)) // cube
	GlyphBranch  = string(rune(0xe0a0)) // powerline branch
	GlyphDir     = string(rune(0xf07c)) // folder_open
	GlyphTag     = string(rune(0xf02b)) // tag
	GlyphClock   = string(rune(0xf017)) // clock_o
	GlyphTerm    = string(rune(0xf120)) // terminal
	GlyphHerdr   = string(rune(0xf009)) // th_large            workspace / tab
	GlyphBrand   = string(rune(0xf02e)) // bookmark
	GlyphLive    = string(rune(0xf04b)) // play                running
	GlyphSession = string(rune(0xf10c)) // circle_o            unfavorited session
	GlyphChars   = string(rune(0xf036)) // align_left          message length
	GlyphEdit    = string(rune(0xf040)) // pencil              files written
)

func init() {
	SetIcons(os.Getenv("FAV_ICONS"))
}

func IconsFromEnv() bool { return os.Getenv("FAV_ICONS") != "" }

var nerd = [...]string{GlyphActive, GlyphDone, GlyphPinned, GlyphArchive, GlyphArrow, GlyphOpen, GlyphClosed,
	GlyphOK, GlyphWarn, GlyphErr, GlyphSearch, GlyphProject, GlyphBranch, GlyphDir, GlyphTag, GlyphClock, GlyphTerm,
	GlyphHerdr, GlyphBrand, GlyphLive, GlyphSession, GlyphChars, GlyphEdit}

var ascii = [...]string{"*", "✓", "!", "#", "->", "v", ">", "+", "!", "x", "/", "#", "@", "~", "#", "@", ">", "#", "*", ">", "o", "~", "+"}

func SetIcons(kind string) {
	set := ascii
	if kind == "nerd" {
		set = nerd
	}
	GlyphActive, GlyphDone, GlyphPinned, GlyphArchive, GlyphArrow, GlyphOpen, GlyphClosed = set[0], set[1], set[2], set[3], set[4], set[5], set[6]
	GlyphOK, GlyphWarn, GlyphErr, GlyphSearch, GlyphProject, GlyphBranch, GlyphDir = set[7], set[8], set[9], set[10], set[11], set[12], set[13]
	GlyphTag, GlyphClock, GlyphTerm, GlyphHerdr, GlyphBrand, GlyphLive, GlyphSession, GlyphChars = set[14], set[15], set[16], set[17], set[18], set[19], set[20], set[21]
	GlyphEdit = set[22]
}
