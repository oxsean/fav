package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/server"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// nodeKeepalive: a node whose coordinator went silent (a Mac asleep) ends instead of lingering.
const nodeKeepalive = 30 * time.Second

// cmdNode answers a coordinator on stdin / stdout (`node --stdio`, what ssh starts; `rpc` is the same), or dials a
// server and answers it until interrupted (`node --connect URL --token-file F`, mode 2).
// ⚠️ stdout carries protocol lines only: while serving, os.Stdout points at stderr so a stray print cannot corrupt them.
func cmdNode(args []string) error {
	if v := first(args); v == "install-service" || v == "uninstall-service" {
		return cmdNodeService(v, args[1:])
	}
	fs := newFlags("node")
	fs.Bool("stdio", true, i18n.T("cli.rpc.flag_stdio"))
	url := fs.String("connect", "", i18n.T("cli.node.flag_connect"))
	tokenFile := fs.String("token-file", "", i18n.T("cli.node.flag_token_file"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	n := node.New(tend.Home())
	cfg := loadConfig()
	n.Limits, n.Profiles = cfg.Node, agent.Profiles(cfg.Agents)
	if *url != "" {
		return connectNode(n, *url, *tokenFile)
	}
	out := os.Stdout
	os.Stdout = os.Stderr
	defer func() { os.Stdout = out }()
	c := wire.New(stdPipes{os.Stdin, out}, wire.Options{Handler: n.Handler(remote.NewLocal(version)), Keepalive: nodeKeepalive})
	go n.Watch(c.Done(), func(runs []string) { c.Push(node.MChanged, node.Changed{Runs: runs}) })
	<-c.Done()
	return nil
}

// connectNode keeps a connection to the server: after a drop it dials again, waiting 1 s, doubling to 60 s.
func connectNode(n *node.Node, url, tokenFile string) error {
	if len(n.Limits.AllowDirs) == 0 {
		return i18n.E("cli.node.need_allow_dirs", tend.ConfigPath())
	}
	token, err := server.ReadToken(tokenFile)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sessions := remote.NewLocal(version)
	wait := time.Second
	for ctx.Err() == nil {
		dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		c, err := server.Dial(dctx, url, server.RoleNode, token, wire.Options{Handler: n.Handler(sessions), Keepalive: nodeKeepalive})
		cancel()
		if wire.Code(err) == wire.CodeUnauthorized {
			return i18n.E("cli.node.unauthorized", url)
		}
		if err == nil {
			fmt.Fprint(os.Stderr, i18n.F("cli.node.connected", url))
			since := time.Now()
			go n.Watch(c.Done(), func(runs []string) { c.Push(node.MChanged, node.Changed{Runs: runs}) })
			select {
			case <-c.Done():
			case <-ctx.Done():
				c.Close()
				return nil
			}
			if time.Since(since) > time.Minute {
				wait = time.Second
			}
			err = c.Err()
		}
		fmt.Fprint(os.Stderr, i18n.F("cli.node.retry", reasonOf(err), wait))
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		wait = min(wait*2, time.Minute)
	}
	return nil
}

// cmdSupervise is `_run <dir>`: the supervisor of one run.
func cmdSupervise(args []string) error {
	if len(args) != 1 {
		return i18n.E("cli.unknown_subcommand", "_run", usage())
	}
	return node.Supervise(args[0])
}
