package tui

import "io"

// Windows input goes through the console API, which bubbletea reads itself.
func terminalInput() io.Reader { return nil }
