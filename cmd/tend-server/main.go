// tend-server is mode 2: the coordinator served over HTTP / WebSocket to nodes and clients that hold a token, with the
// Web UI. Nodes, TUIs and CLIs are the tend program; only this one carries the server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/server"
	"github.com/oxsean/fav/internal/tend"
)

var version = "dev" // set by goreleaser via -X main.version

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "tend-server: "+err.Error())
		os.Exit(1)
	}
}

func usage() string { return i18n.T("cli.server.usage") }

func run(args []string) error {
	i18n.Set(i18n.Resolve(tend.LoadConfig().Lang))
	switch first(args) {
	case "token":
		return cmdToken(args[1:])
	case "version", "--version":
		fmt.Println("tend-server " + version)
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage())
		return nil
	case "", "serve":
		if first(args) == "serve" {
			args = args[1:]
		}
		return cmdServe(args)
	}
	if strings.HasPrefix(args[0], "-") {
		return cmdServe(args)
	}
	return i18n.E("cli.unknown_subcommand", args[0], usage())
}

func newFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("tend-server", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage())
		fs.PrintDefaults()
	}
	return fs
}

func cmdServe(args []string) error {
	fs := newFlags()
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
	home := tend.Home()
	c, err := coord.Open(coord.Options{Home: home, Version: version, Config: tend.LoadConfig(), Remote: true, Nodes: server.NodeNames(home)})
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
	return server.New(server.Options{Home: home, Coord: c, Listen: *listen, TLSCert: *cert, TLSKey: *key}).Serve(ctx)
}

func cmdToken(args []string) error {
	home := tend.Home()
	switch first(args) {
	case "add":
		fs := newFlags()
		nodeName := fs.String("node", "", i18n.T("cli.server.flag_node"))
		clientName := fs.String("client", "", i18n.T("cli.server.flag_client"))
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if (*nodeName == "") == (*clientName == "") {
			fs.Usage()
			return flag.ErrHelp
		}
		role, name := server.RoleNode, *nodeName
		if *clientName != "" {
			role, name = server.RoleClient, *clientName
		}
		token, err := server.AddToken(home, role, name)
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
		if err := server.RemoveToken(home, args[1]); errors.Is(err, os.ErrNotExist) {
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
		if err := server.RebindToken(home, args[1]); errors.Is(err, os.ErrNotExist) {
			return i18n.E("cli.server.token_missing", args[1])
		} else if err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.server.token_rebound", args[1]))
		return nil
	case "", "list", "ls":
		ts, err := server.Tokens(home)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", i18n.T("cli.server.col_name"), i18n.T("cli.server.col_role"), i18n.T("cli.server.col_created"), i18n.T("cli.server.col_machine"))
		for _, t := range ts {
			host := t.BoundHost
			if host == "" {
				host = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Name, t.Role, t.Created.Local().Format(time.DateTime), host)
		}
		return w.Flush()
	}
	return i18n.E("cli.unknown_subcommand", "token "+args[0], usage())
}

func first(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}
