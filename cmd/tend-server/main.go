// tend-server is mode 2: the coordinator served over HTTP / WebSocket to nodes and clients that hold a token, with the
// Web UI. Nodes, TUIs and CLIs are the tend program; only this one carries the server.
package main

import (
	"cmp"
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
	"github.com/oxsean/fav/internal/store"
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
	case "admin":
		return cmdAdmin(args[1:])
	case "import":
		return cmdImport(args[1:])
	case "export":
		return cmdExport(args[1:])
	case "backup":
		return cmdBackup(args[1:])
	case "db":
		return cmdDB(args[1:])
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
	if p := unimported(home); p != "" {
		return i18n.E("cli.server.import_first", p)
	}
	cfg := tend.LoadConfig()
	var dir *server.Directory
	notifier := server.NewNotifier()
	c, err := coord.Open(coord.Options{Home: home, Version: version, Config: cfg, Remote: true, OpenLog: openLog, Notice: notifier.Send,
		MachineOwner: func(m string) string {
			if dir == nil {
				return ""
			}
			return dir.MachineOwner(m)
		},
		Users: func(id string) (coord.User, bool) {
			if dir == nil {
				return coord.User{}, false
			}
			return dir.User(id)
		}})
	if errors.Is(err, coord.ErrLocked) {
		return i18n.E("cli.service.running")
	}
	if err != nil {
		return err
	}
	defer c.Close()
	team, err := store.OpenTeam(dbPath(home))
	if err != nil {
		return err
	}
	defer team.Close()
	if n, err := server.ImportTokens(home, team); err != nil {
		return err
	} else if n > 0 {
		fmt.Fprint(os.Stderr, i18n.F("cli.server.tokens_imported", n))
	}
	if dir, err = server.NewDirectory(team); err != nil {
		return err
	}
	for _, name := range dir.NodeNames() {
		c.Expect(name)
	}
	var sc tend.ServerConfig
	if cfg.Server != nil {
		sc = *cfg.Server
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go c.Serve(ctx)
	go c.Run(ctx)
	go notifier.Run(ctx, team, sc.PublicURL)
	seal, err := server.LoadSealer(home)
	if err != nil {
		return err
	}
	syncer := server.NewSyncer(team, c, seal, notifier.Send)
	go syncer.Run(ctx)
	fmt.Fprint(os.Stderr, i18n.F("cli.server.started", c.ID(), *listen))
	return server.New(server.Options{Home: home, Coord: c, Dir: dir, Config: sc, Listen: *listen, TLSCert: *cert, TLSKey: *key,
		Syncer: syncer}).Serve(ctx)
}

func openTeam() (*store.Team, error) { return store.OpenTeam(dbPath(tend.Home())) }

// credByName is the live credential a token command names: its id, a node token's machine, or a token of the server
// host's by its name.
func credByName(team *store.Team, name string) (store.Credential, bool) {
	cs, _ := team.ActiveCredentials()
	for _, c := range cs {
		if c.ID == name || c.Name == name && (c.Kind == store.KindNode || c.Kind == store.KindToken && c.Owner == store.LocalUser) {
			return c, true
		}
	}
	return store.Credential{}, false
}

func cmdToken(args []string) error {
	team, err := openTeam()
	if err != nil {
		return err
	}
	defer team.Close()
	switch first(args) {
	case "add":
		fs := newFlags()
		nodeName := fs.String("node", "", i18n.T("cli.server.flag_node"))
		clientName := fs.String("client", "", i18n.T("cli.server.flag_client"))
		owner := fs.String("owner", store.LocalUser, i18n.T("cli.server.flag_owner"))
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if (*nodeName == "") == (*clientName == "") {
			fs.Usage()
			return flag.ErrHelp
		}
		kind, name, role := store.KindNode, *nodeName, server.RoleNode
		if *clientName != "" {
			kind, name, role = store.KindToken, *clientName, server.RoleClient
		} else if err := server.CheckMachine(name); err != nil {
			return err
		}
		if _, ok, err := team.User(*owner); err != nil || !ok {
			return i18n.E("cli.server.no_user", *owner)
		}
		token, _, err := team.NewCredential(kind, name, *owner, 0)
		if errors.Is(err, store.ErrExists) {
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
		c, ok := credByName(team, args[1])
		if !ok {
			return i18n.E("cli.server.token_missing", args[1])
		}
		if err := team.Revoke(c.ID); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.server.token_removed", args[1]))
		return nil
	case "rebind":
		if len(args) != 2 {
			return i18n.E("cli.server.token_rebind_usage")
		}
		c, ok := credByName(team, args[1])
		if !ok || c.Kind != store.KindNode {
			return i18n.E("cli.server.token_missing", args[1])
		}
		if err := team.Rebind(c.ID); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.server.token_rebound", args[1]))
		return nil
	case "", "list", "ls":
		cs, err := team.ActiveCredentials()
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", "ID", i18n.T("cli.server.col_name"), i18n.T("cli.server.col_role"),
			i18n.T("cli.server.col_owner"), i18n.T("cli.server.col_created"), i18n.T("cli.server.col_machine"))
		for _, c := range cs {
			if c.Kind == store.KindWeb {
				continue
			}
			role := server.RoleClient
			if c.Kind == store.KindNode {
				role = server.RoleNode
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", c.ID, dash(c.Name), role, c.Owner, c.Created.Local().Format(time.DateTime), dash(c.Host))
		}
		return w.Flush()
	}
	return i18n.E("cli.unknown_subcommand", "token "+args[0], usage())
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// parseOne parses flags on either side of one argument; "" when there is not exactly one.
func parseOne(fs *flag.FlagSet, args []string) (string, error) {
	if err := fs.Parse(args); err != nil || fs.NArg() == 0 {
		return "", err
	}
	v := fs.Arg(0)
	if err := fs.Parse(fs.Args()[1:]); err != nil || fs.NArg() != 0 {
		return "", err
	}
	return v, nil
}

// cmdAdmin manages who may sign in; it runs on the server host, beside a running server.
func cmdAdmin(args []string) error {
	team, err := openTeam()
	if err != nil {
		return err
	}
	defer team.Close()
	switch first(args) {
	case "add":
		fs := newFlags()
		role := fs.String("role", store.RoleMember, i18n.T("cli.server.flag_role"))
		v, err := parseOne(fs, args[1:])
		if err != nil {
			return err
		}
		if v == "" {
			return i18n.E("cli.server.admin_add_usage")
		}
		kind := store.AdmitDomain
		switch {
		case strings.Contains(v, "@"):
			kind = store.AdmitEmail
		case strings.Contains(v, ":"):
			kind = store.AdmitLogin
		}
		if err := team.AddAdmit(store.Admit{Kind: kind, Value: v, Role: *role, AddedBy: store.LocalUser}); err != nil {
			return err
		}
		team.Audit(store.AuditEntry{Actor: store.LocalUser, Kind: "admit", Detail: kind + " " + v + " " + *role})
		fmt.Print(i18n.F("cli.server.admitted", kind, v, *role))
		return nil
	case "rm", "remove":
		if len(args) != 2 {
			return i18n.E("cli.server.admin_rm_usage")
		}
		for _, kind := range []string{store.AdmitEmail, store.AdmitDomain, store.AdmitLogin} {
			if team.RemoveAdmit(kind, args[1]) == nil {
				fmt.Print(i18n.F("cli.server.unadmitted", args[1]))
				return nil
			}
		}
		return i18n.E("cli.server.admit_missing", args[1])
	case "invite":
		fs := newFlags()
		role := fs.String("role", store.RoleMember, i18n.T("cli.server.flag_role"))
		project := fs.String("project", "", i18n.T("cli.server.flag_invite_project"))
		access := fs.String("access", "", i18n.T("cli.server.flag_invite_access"))
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		secret, err := team.NewProjectInvite(*role, store.LocalUser, *project, *access, 72*time.Hour)
		if err != nil {
			return err
		}
		base := "<server>"
		if cfg := tend.LoadConfig(); cfg.Server != nil && cfg.Server.PublicURL != "" {
			base = strings.TrimRight(cfg.Server.PublicURL, "/")
		}
		fmt.Println(base + "/#invite-" + secret)
		fmt.Fprint(os.Stderr, i18n.T("cli.server.invite_made"))
		return nil
	case "disable", "enable":
		if len(args) != 2 {
			return i18n.E("cli.server.admin_user_usage")
		}
		off := args[0] == "disable"
		if err := team.SetUser(args[1], nil, &off); err != nil {
			return i18n.E("cli.server.no_user", args[1])
		}
		team.Audit(store.AuditEntry{Actor: store.LocalUser, Kind: "user", Detail: args[0] + " " + args[1]})
		fmt.Print(i18n.F("cli.server.user_changed", args[1]))
		return nil
	case "role":
		if len(args) != 3 {
			return i18n.E("cli.server.admin_user_usage")
		}
		if err := team.SetUser(args[1], &args[2], nil); err != nil {
			return i18n.E("cli.server.no_user", args[1])
		}
		team.Audit(store.AuditEntry{Actor: store.LocalUser, Kind: "user", Detail: "role " + args[1] + " " + args[2]})
		fmt.Print(i18n.F("cli.server.user_changed", args[1]))
		return nil
	case "audit":
		es, err := team.AuditLog(100)
		if err != nil {
			return err
		}
		for _, e := range es {
			fmt.Printf("%s  %-8s %-14s %s %s\n", e.At.Local().Format(time.DateTime), dash(e.Actor), e.Kind, e.Detail, e.IP)
		}
		return nil
	case "", "list", "ls":
		us, err := team.Users()
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "ID\tNAME\tEMAIL\tROLE\tSTATE\n")
		for _, u := range us {
			state := "active"
			if u.Disabled {
				state = "disabled"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", u.ID, dash(cmp.Or(u.Name, u.Username)), dash(u.Email), u.Role, state)
		}
		w.Flush()
		as, err := team.Admits()
		if err != nil {
			return err
		}
		if len(as) > 0 {
			fmt.Println()
			w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "ADMITS\tVALUE\tROLE\n")
			for _, a := range as {
				fmt.Fprintf(w, "%s\t%s\t%s\n", a.Kind, a.Value, a.Role)
			}
			w.Flush()
		}
		return nil
	}
	return i18n.E("cli.unknown_subcommand", "admin "+args[0], usage())
}

func first(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}
