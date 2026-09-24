package main

import (
	"context"
	"os"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
)

// cmdRpc answers the remote protocol: one request (`fav rpc`) or until stdin closes (`--stdio`).
// ⚠️ stdout carries protocol lines only: while serving, os.Stdout points at stderr so a stray print cannot corrupt them.
func cmdRpc(args []string) error {
	fs := newFlags("rpc")
	stdio := fs.Bool("stdio", false, i18n.T("cli.rpc.flag_stdio"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	out := os.Stdout
	os.Stdout = os.Stderr
	defer func() { os.Stdout = out }()
	return remote.Serve(context.Background(), os.Stdin, out, remote.NewLocal(version), !*stdio)
}
