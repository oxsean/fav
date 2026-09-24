package main

import (
	"os"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/wire"
)

// nodeKeepalive: a node whose coordinator went silent (a Mac asleep) ends instead of lingering.
const nodeKeepalive = 30 * time.Second

// cmdNode answers a coordinator on stdin / stdout (`node --stdio`, what ssh starts); `rpc` is the same.
// ⚠️ stdout carries protocol lines only: while serving, os.Stdout points at stderr so a stray print cannot corrupt them.
func cmdNode(args []string) error {
	fs := newFlags("node")
	fs.Bool("stdio", true, i18n.T("cli.rpc.flag_stdio"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	out := os.Stdout
	os.Stdout = os.Stderr
	defer func() { os.Stdout = out }()
	n := node.New(fav.Home())
	n.Limits = loadConfig().Node
	c := wire.New(stdPipes{os.Stdin, out}, wire.Options{Handler: n.Handler(remote.NewLocal(version)), Keepalive: nodeKeepalive})
	go n.Watch(c.Done(), func(runs []string) { c.Push(node.MChanged, node.Changed{Runs: runs}) })
	<-c.Done()
	return nil
}

// cmdSupervise is `_run <dir>`: the supervisor of one run.
func cmdSupervise(args []string) error {
	if len(args) != 1 {
		return i18n.E("cli.unknown_subcommand", "_run", usage())
	}
	return node.Supervise(args[0])
}
