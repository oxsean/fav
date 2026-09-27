package main

import (
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/dial"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/wire"
)

// cmdServer points at tend-server, the program mode 2 runs.
func cmdServer([]string) error { return i18n.E("cli.server.moved") }

// connectCoord reaches the coordinator: the server config.coordinator names, else this machine's.
func connectCoord(wopt wire.Options) (*coord.Client, error) {
	if c := loadConfig().Coordinator; c != nil && c.URL != "" {
		return dial.Connect(c.URL, paths.Expand(c.TokenFile), wopt)
	}
	return coord.Connect(coordOptions(), wopt)
}
