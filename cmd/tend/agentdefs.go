package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/defs"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// readDef reads a definition file and what it imports (read here, never by the coordinator).
func readDef(path string) (defs.AgentDef, []string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return defs.AgentDef{}, nil, err
	}
	d, err := defs.Parse(b)
	if err != nil {
		return d, nil, fmt.Errorf("%s: %w", path, err)
	}
	if d, err = defs.Import(d, os.ReadFile); err != nil {
		return d, nil, err
	}
	errs, warns := defs.Check(d)
	if len(errs) > 0 {
		return d, warns, fmt.Errorf("%s: %s", path, strings.Join(errs, "; "))
	}
	return d, warns, nil
}

func cmdAgentCheck(args []string) error {
	fs := newFlags("agent")
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	d, warns, err := readDef(pos[0])
	for _, w := range warns {
		fmt.Fprint(os.Stderr, i18n.F("cli.agent.warning", w))
	}
	if err != nil {
		return err
	}
	fmt.Print(i18n.F("cli.agent.check_ok", d.Name))
	return nil
}

func cmdAgentImport(args []string) error {
	fs := newFlags("agent")
	project := fs.String("project", "", i18n.T("cli.agent.flag_project"))
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	d, warns, err := readDef(pos[0])
	for _, w := range warns {
		fmt.Fprint(os.Stderr, i18n.F("cli.agent.warning", w))
	}
	if err != nil {
		return err
	}
	owner := ""
	if *project != "" {
		owner = task.ProjectOwner + *project
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		var v coord.AgentDefView
		if err := write(cl, coord.MAgentDefSave, coord.AgentDefSave{Text: string(defs.Format(d)), Owner: owner}, &v); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.agent.imported", v.Name, v.Owner))
		return nil
	})
}

func cmdAgentExport(args []string) error {
	fs := newFlags("agent")
	out := fs.String("o", "", i18n.T("cli.agent.flag_out"))
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		ctx, cancel := callTimeout()
		defer cancel()
		var v coord.AgentDefView
		if err := cl.Call(ctx, coord.MAgentDefGet, task.AgentDefRef{Name: pos[0]}, &v); err != nil {
			return err
		}
		if v.Text == "" {
			return i18n.E("cli.agent.not_readable", v.Name)
		}
		if *out == "" {
			fmt.Print(v.Text)
			return nil
		}
		return os.WriteFile(*out, []byte(v.Text), 0o644)
	})
}

func cmdAgentRemove(args []string) error {
	fs := newFlags("agent")
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		if err := write(cl, coord.MAgentDefRemove, task.AgentDefRef{Name: pos[0]}, nil); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.agent.removed", pos[0]))
		return nil
	})
}

func cmdAgentShare(args []string) error {
	fs := newFlags("agent")
	users := fs.String("users", "", i18n.T("cli.agent.flag_users"))
	projects := fs.String("projects", "", i18n.T("cli.agent.flag_projects"))
	all := fs.Bool("all", false, i18n.T("cli.agent.flag_all"))
	view := fs.Bool("view", false, i18n.T("cli.agent.flag_view"))
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	share := task.DefShare{Users: list(*users), Projects: list(*projects), All: *all, View: *view}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		if err := write(cl, coord.MAgentDefShare, task.AgentDefShare{Name: pos[0], Share: share}, nil); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.agent.shared", pos[0]))
		return nil
	})
}

func cmdAgentLeave(args []string) error {
	fs := newFlags("agent")
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		if err := write(cl, coord.MAgentDefLeave, task.AgentDefRef{Name: pos[0]}, nil); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.agent.left", pos[0]))
		return nil
	})
}

func cmdAgentTransfer(args []string) error {
	fs := newFlags("agent")
	project := fs.String("project", "", i18n.T("cli.agent.flag_transfer_project"))
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if *project == "" {
		return errors.New(i18n.T("cli.agent.need_project"))
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		if err := write(cl, coord.MAgentDefTransfer, task.AgentDefTransfer{Name: pos[0], Owner: task.ProjectOwner + *project}, nil); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.agent.transferred", pos[0], *project))
		return nil
	})
}

func list(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func cmdAgentDefs(args []string) error {
	fs := newFlags("agent")
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		ctx, cancel := callTimeout()
		defer cancel()
		var l coord.AgentDefList
		if err := cl.Call(ctx, coord.MAgentDefList, nil, &l); err != nil {
			return err
		}
		if *asJSON {
			return printJSON(l.Defs)
		}
		rows := [][]string{{i18n.T("cli.agent.col_name"), i18n.T("cli.agent.col_role"), i18n.T("cli.agent.col_provider"),
			i18n.T("cli.agent.col_model"), i18n.T("cli.agent.col_owner")}}
		for _, d := range l.Defs {
			rows = append(rows, []string{d.Name, orDash(d.Role), orDash(d.Provider), orDash(d.Model), d.Owner})
		}
		printTable(rows, termWidth())
		return nil
	})
}
