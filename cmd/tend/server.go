package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/server"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// cmdServer is mode 2: the coordinator served over HTTP to nodes and clients that hold a token.
func cmdServer(args []string) error {
	if first(args) == "token" {
		return cmdServerToken(args[1:])
	}
	fs := newFlags("server")
	listen := fs.String("listen", "", i18n.T("cli.server.flag_listen"))
	cert := fs.String("tls-cert", "", i18n.T("cli.server.flag_tls_cert"))
	key := fs.String("tls-key", "", i18n.T("cli.server.flag_tls_key"))
	plain := fs.Bool("plain", false, i18n.T("cli.server.flag_plain"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *listen == "" || (*cert == "") != (*key == "") {
		fs.Usage()
		return flag.ErrHelp
	}
	if err := server.CheckListen(*listen, *cert != "", *plain); err != nil {
		return i18n.E("cli.server.bad_listen", err)
	}
	opt := coordOptions()
	opt.Remote, opt.Nodes, opt.Node = true, server.NodeNames(tend.Home()), nil
	c, err := coord.Open(opt)
	if errors.Is(err, coord.ErrLocked) {
		return i18n.E("cli.service.running")
	}
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go c.Serve(ctx)
	go c.Run(ctx)
	fmt.Fprint(os.Stderr, i18n.F("cli.server.started", c.ID(), *listen))
	return server.New(server.Options{Home: tend.Home(), Coord: c, Listen: *listen, TLSCert: *cert, TLSKey: *key}).Serve(ctx)
}

func cmdServerToken(args []string) error {
	switch first(args) {
	case "add":
		fs := newFlags("server")
		nodeName := fs.String("node", "", i18n.T("cli.server.flag_node"))
		clientName := fs.String("client", "", i18n.T("cli.server.flag_client"))
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		role, name := server.RoleNode, *nodeName
		if *clientName != "" {
			role, name = server.RoleClient, *clientName
		}
		if (*nodeName == "") == (*clientName == "") {
			fs.Usage()
			return flag.ErrHelp
		}
		token, err := server.AddToken(tend.Home(), role, name)
		if errors.Is(err, server.ErrExists) {
			return i18n.E("cli.server.token_exists", name)
		}
		if err != nil {
			return err
		}
		fmt.Println(token)
		fmt.Fprint(os.Stderr, i18n.F("cli.server.token_added", role, name))
		return nil
	case "rm", "remove":
		if len(args) != 2 {
			return i18n.E("cli.server.token_rm_usage")
		}
		if err := server.RemoveToken(tend.Home(), args[1]); errors.Is(err, os.ErrNotExist) {
			return i18n.E("cli.server.token_missing", args[1])
		} else if err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.server.token_removed", args[1]))
		return nil
	case "rebind":
		if len(args) != 2 {
			return i18n.E("cli.server.token_rebind_usage")
		}
		if err := server.RebindToken(tend.Home(), args[1]); errors.Is(err, os.ErrNotExist) {
			return i18n.E("cli.server.token_missing", args[1])
		} else if err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.server.token_rebound", args[1]))
		return nil
	case "", "list", "ls":
		ts, err := server.Tokens(tend.Home())
		if err != nil {
			return err
		}
		rows := [][]string{{i18n.T("cli.server.col_name"), i18n.T("cli.server.col_role"), i18n.T("cli.server.col_created"), i18n.T("cli.server.col_machine")}}
		for _, t := range ts {
			rows = append(rows, []string{t.Name, t.Role, t.Created.Local().Format(time.DateTime), orDash(t.BoundHost)})
		}
		printTable(rows, termWidth())
		return nil
	}
	return i18n.E("cli.unknown_subcommand", "server token "+args[0], usage())
}

// connectCoord reaches the coordinator: the server config.coordinator names, else this machine's.
func connectCoord(wopt wire.Options) (*coord.Client, error) {
	if c := loadConfig().Coordinator; c != nil && c.URL != "" {
		return server.Connect(c.URL, paths.Expand(c.TokenFile), wopt)
	}
	return coord.Connect(coordOptions(), wopt)
}
