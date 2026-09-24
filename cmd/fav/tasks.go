package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

func coordOptions() coord.Options {
	cfg := loadConfig()
	n := node.New(fav.Home())
	n.Limits = cfg.Node
	return coord.Options{Home: fav.Home(), Version: version, Config: cfg, Node: n, Sessions: remote.NewLocal(version)}
}

// withCoord runs f against the coordinator: the running one, or this process for the length of the command.
func withCoord(wopt wire.Options, f func(cl *coord.Client) error) error {
	cl, err := coord.Connect(coordOptions(), wopt)
	if errors.Is(err, coord.ErrLocked) {
		return i18n.E("cli.coord.unreachable", coord.SocketPath(fav.Home()))
	}
	if err != nil {
		return err
	}
	defer cl.Close()
	err = f(cl)
	if wire.Code(err) != "" {
		return errors.New(reasonOf(err))
	}
	return err
}

func callTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Minute)
}

func commandID() string {
	var b [8]byte
	rand.Read(b[:])
	return "cli-" + hex.EncodeToString(b[:])
}

func write(cl *coord.Client, method string, params, out any) error {
	ctx, cancel := callTimeout()
	defer cancel()
	return cl.CallCommand(ctx, method, commandID(), params, out)
}

func readState(cl *coord.Client) (*task.State, error) {
	ctx, cancel := callTimeout()
	defer cancel()
	st := task.New()
	return st, cl.Call(ctx, coord.MStateGet, nil, st)
}

// resolve finds the one id in ids that is arg or starts with it.
func resolve(arg string, ids []string, missing, ambiguous string) (string, error) {
	var hits []string
	for _, id := range ids {
		if id == arg {
			return id, nil
		}
		if strings.HasPrefix(id, arg) {
			hits = append(hits, id)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return "", i18n.E(missing, arg)
	}
	return "", i18n.E(ambiguous, arg, strings.Join(hits, " "))
}

func taskID(st *task.State, arg string) (string, error) {
	ids := make([]string, 0, len(st.Tasks))
	for id := range st.Tasks {
		ids = append(ids, id)
	}
	return resolve(arg, ids, "cli.task.not_found", "cli.task.ambiguous")
}

func runID(st *task.State, arg string) (string, error) {
	ids := make([]string, 0, len(st.Runs))
	for id := range st.Runs {
		ids = append(ids, id)
	}
	return resolve(arg, ids, "cli.run.not_found", "cli.run.ambiguous")
}

var runStates = map[string]string{
	task.Queued: "cli.run.state_queued", task.Starting: "cli.run.state_starting", task.Running: "cli.run.state_running",
	task.Unknown: "cli.run.state_unknown", task.Exited: "cli.run.state_exited", task.Stopped: "cli.run.state_stopped",
	task.Failed: "cli.run.state_failed", task.Canceled: "cli.run.state_canceled", task.Abandoned: "cli.run.state_abandoned",
}

var taskStatuses = map[string]string{
	task.StatusTodo: "cli.task.status_todo", task.StatusDone: "cli.task.status_done", task.StatusCanceled: "cli.task.status_canceled",
}

func runState(r *task.Run) string {
	s := i18n.T(runStates[r.State])
	if r.State == task.Exited && r.ExitCode != nil && *r.ExitCode != 0 {
		s = i18n.F("cli.run.exit_code", s, *r.ExitCode)
	}
	if r.Want == "stop" && task.Open(r.State) {
		s = i18n.F("cli.run.stopping", s)
	}
	return s
}

// took is how long r ran, or has run so far.
func took(r *task.Run, now time.Time) string {
	if r.StartedAt == nil {
		return "-"
	}
	end := now
	if r.EndedAt != nil {
		end = *r.EndedAt
	}
	return end.Sub(*r.StartedAt).Round(time.Second).String()
}

func readBrief(brief, file string) (string, error) {
	if file == "" {
		return brief, nil
	}
	if file == "-" {
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	}
	b, err := os.ReadFile(file)
	return string(b), err
}

func absDir(d string) (string, error) {
	if d == "" {
		return os.Getwd()
	}
	return filepath.Abs(paths.Expand(d))
}

// cmdTask manages tasks: add, list, show, edit, done, reopen, cancel.
func cmdTask(args []string) error {
	switch first(args) {
	case "add":
		return cmdTaskAdd(args[1:])
	case "", "list", "ls":
		if len(args) > 0 {
			args = args[1:]
		}
		return cmdTaskList(args)
	case "show":
		return cmdTaskShow(args[1:])
	case "edit":
		return cmdTaskEdit(args[1:])
	case "done", "reopen", "cancel":
		return cmdTaskStatus(args[0], args[1:])
	}
	return i18n.E("cli.unknown_subcommand", "task "+args[0], usage())
}

func cmdTaskAdd(args []string) error {
	fs := newFlags("task")
	brief := fs.String("brief", "", i18n.T("cli.task.flag_brief"))
	briefFile := fs.String("brief-file", "", i18n.T("cli.task.flag_brief_file"))
	dir := fs.String("dir", "", i18n.T("cli.task.flag_dir"))
	machine := fs.String("machine", "", i18n.T("cli.task.flag_machine"))
	agentName := fs.String("agent", "", i18n.T("cli.task.flag_agent"))
	title, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	b, err := readBrief(*brief, *briefFile)
	if err != nil {
		return err
	}
	d := *dir
	if *machine == "" || *machine == coord.Local {
		if d, err = absDir(d); err != nil {
			return err
		}
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		var t task.Task
		if err := write(cl, coord.MTaskCreate, coord.TaskCreate{Title: title[0], Brief: b, Dir: d, Machine: *machine, Agent: *agentName}, &t); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.task.added", t.ID, t.Title))
		return nil
	})
}

// parseWithArgs parses flags placed before or after exactly n positional arguments.
func parseWithArgs(fs *flag.FlagSet, args []string, n int) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(pos) != n {
		fs.Usage()
		return nil, flag.ErrHelp
	}
	return pos, nil
}

func cmdTaskList(args []string) error {
	fs := newFlags("task")
	all := fs.Bool("all", false, i18n.T("cli.task.flag_all"))
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		st, err := readState(cl)
		if err != nil {
			return err
		}
		var tasks []*task.Task
		for _, t := range st.Sorted() {
			if *all || t.Status == task.StatusTodo || st.Running(t.ID) {
				tasks = append(tasks, t)
			}
		}
		if *asJSON {
			return printJSON(tasks)
		}
		if len(tasks) == 0 {
			fmt.Print(i18n.T("cli.task.none"))
			return nil
		}
		now := time.Now()
		rows := [][]string{{i18n.T("cli.task.col_id"), i18n.T("cli.task.col_status"), i18n.T("cli.task.col_run"), i18n.T("cli.task.col_title")}}
		for _, t := range tasks {
			last := "-"
			if runs := st.RunsOf(t.ID); len(runs) > 0 {
				r := runs[len(runs)-1]
				last = i18n.F("cli.task.run_cell", r.Machine, r.Agent, runState(r), took(r, now))
			}
			rows = append(rows, []string{t.ID, i18n.T(taskStatuses[t.Status]), last, t.Title})
		}
		printTable(rows, termWidth())
		return nil
	})
}

func cmdTaskShow(args []string) error {
	fs := newFlags("task")
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		st, err := readState(cl)
		if err != nil {
			return err
		}
		id, err := taskID(st, pos[0])
		if err != nil {
			return err
		}
		t, runs := st.Tasks[id], st.RunsOf(id)
		if *asJSON {
			return printJSON(map[string]any{"task": t, "runs": runs})
		}
		fmt.Print(i18n.F("cli.task.show", t.ID, t.Title, i18n.T(taskStatuses[t.Status]), orDash(t.Dir), orDash(t.Machine), orDash(t.Agent)))
		if strings.TrimSpace(t.Brief) != "" {
			fmt.Println()
			fmt.Println(strings.TrimRight(t.Brief, "\n"))
		}
		if len(runs) > 0 {
			fmt.Println()
			printRuns(runs)
		}
		return nil
	})
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func cmdTaskEdit(args []string) error {
	fs := newFlags("task")
	title := fs.String("title", "", i18n.T("cli.task.flag_title"))
	brief := fs.String("brief", "", i18n.T("cli.task.flag_brief"))
	briefFile := fs.String("brief-file", "", i18n.T("cli.task.flag_brief_file"))
	dir := fs.String("dir", "", i18n.T("cli.task.flag_dir"))
	machine := fs.String("machine", "", i18n.T("cli.task.flag_machine"))
	agentName := fs.String("agent", "", i18n.T("cli.task.flag_agent"))
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		st, err := readState(cl)
		if err != nil {
			return err
		}
		id, err := taskID(st, pos[0])
		if err != nil {
			return err
		}
		e := task.TaskEdit{ID: id}
		if set["title"] {
			e.Title = title
		}
		if set["brief"] || set["brief-file"] {
			b, err := readBrief(*brief, *briefFile)
			if err != nil {
				return err
			}
			e.Brief = &b
		}
		if set["dir"] {
			d := *dir
			if m := st.Tasks[id].Machine; (!set["machine"] && (m == "" || m == coord.Local)) || (set["machine"] && (*machine == "" || *machine == coord.Local)) {
				if d, err = absDir(d); err != nil {
					return err
				}
			}
			e.Dir = &d
		}
		if set["machine"] {
			e.Machine = machine
		}
		if set["agent"] {
			e.Agent = agentName
		}
		var t task.Task
		if err := write(cl, coord.MTaskEdit, e, &t); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.task.saved", t.ID, t.Title))
		return nil
	})
}

func cmdTaskStatus(verb string, args []string) error {
	fs := newFlags("task")
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	status := map[string]string{"done": task.StatusDone, "reopen": task.StatusTodo, "cancel": task.StatusCanceled}[verb]
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		st, err := readState(cl)
		if err != nil {
			return err
		}
		id, err := taskID(st, pos[0])
		if err != nil {
			return err
		}
		var t task.Task
		if err := write(cl, coord.MTaskStatus, task.TaskStatus{ID: id, Status: status}, &t); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.task.status_set", t.ID, i18n.T(taskStatuses[t.Status])))
		return nil
	})
}

// cmdRun runs tasks: start, stop, abandon, list, show, logs.
func cmdRun(args []string) error {
	switch first(args) {
	case "start":
		return cmdRunStart(args[1:])
	case "stop", "abandon":
		return cmdRunEnd(args[0], args[1:])
	case "", "list", "ls":
		if len(args) > 0 {
			args = args[1:]
		}
		return cmdRunList(args)
	case "show":
		return cmdRunShow(args[1:])
	case "logs":
		return cmdRunLogs(args[1:])
	}
	return i18n.E("cli.unknown_subcommand", "run "+args[0], usage())
}

func cmdRunStart(args []string) error {
	fs := newFlags("run")
	machine := fs.String("machine", "", i18n.T("cli.run.flag_machine"))
	agentName := fs.String("agent", "", i18n.T("cli.run.flag_agent"))
	runner := fs.String("runner", "", i18n.T("cli.run.flag_runner"))
	wait := fs.Bool("wait", false, i18n.T("cli.run.flag_wait"))
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	events := make(chan journal.Envelope, 64)
	wopt := wire.Options{OnPush: func(method string, params json.RawMessage) {
		var env journal.Envelope
		if method == coord.PushJournal && json.Unmarshal(params, &env) == nil {
			select {
			case events <- env:
			default:
			}
		}
	}}
	return withCoord(wopt, func(cl *coord.Client) error {
		st, err := readState(cl)
		if err != nil {
			return err
		}
		id, err := taskID(st, pos[0])
		if err != nil {
			return err
		}
		var r task.Run
		if err := write(cl, coord.MRunDispatch, coord.Dispatch{Task: id, Machine: *machine, Agent: *agentName, Runner: *runner}, &r); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.run.queued", r.ID, r.Machine, r.Agent))
		if !*wait {
			return nil
		}
		return waitRun(cl, r.ID, st.Seq, events)
	})
}

// waitRun prints r's states as they change until it ends; a run that did not exit 0 is an error.
func waitRun(cl *coord.Client, id string, after int64, events <-chan journal.Envelope) error {
	ctx, cancel := callTimeout()
	err := cl.Call(ctx, coord.MSubscribe, coord.SubscribeParams{AfterSeq: after}, nil)
	cancel()
	if err != nil {
		return err
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	st, err := readState(cl)
	if err != nil {
		return err
	}
	last := ""
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		r := st.Runs[id]
		if r != nil && r.State != last {
			last = r.State
			fmt.Print(i18n.F("cli.run.now", time.Now().Format("15:04:05"), r.ID, runState(r)))
		}
		if r != nil && !task.Open(r.State) {
			if r.State == task.Exited && r.ExitCode != nil && *r.ExitCode == 0 {
				return nil
			}
			return i18n.E("cli.run.ended_badly", r.ID, runState(r), orDash(r.Reason))
		}
		select {
		case env := <-events:
			if env.Seq > st.Seq {
				st.Apply(env)
			}
		case <-tick.C: // the in-process coordinator's pushes can be missed when the queue is full
			if fresh, err := readState(cl); err == nil {
				st = fresh
			}
		case <-sigs:
			fmt.Print(i18n.F("cli.run.detached", id))
			return nil
		case <-cl.Done():
			return cl.Err()
		}
	}
}

func cmdRunEnd(verb string, args []string) error {
	fs := newFlags("run")
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	method := map[string]string{"stop": coord.MRunStop, "abandon": coord.MRunAbandon}[verb]
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		st, err := readState(cl)
		if err != nil {
			return err
		}
		id, err := runID(st, pos[0])
		if err != nil {
			return err
		}
		var r task.Run
		if err := write(cl, method, task.RunRef{ID: id}, &r); err != nil {
			return err
		}
		fmt.Print(i18n.F("cli.run.now", time.Now().Format("15:04:05"), r.ID, runState(&r)))
		return nil
	})
}

func printRuns(runs []*task.Run) {
	now := time.Now()
	rows := [][]string{{i18n.T("cli.run.col_id"), i18n.T("cli.run.col_task"), i18n.T("cli.run.col_machine"), i18n.T("cli.run.col_agent"),
		i18n.T("cli.run.col_state"), i18n.T("cli.run.col_took"), i18n.T("cli.run.col_session")}}
	for _, r := range runs {
		rows = append(rows, []string{r.ID, r.Task, r.Machine, r.Agent, runState(r), took(r, now), orDash(r.Session)})
	}
	printTable(rows, termWidth())
}

func cmdRunList(args []string) error {
	fs := newFlags("run")
	of := fs.String("task", "", i18n.T("cli.run.flag_task"))
	all := fs.Bool("all", false, i18n.T("cli.run.flag_all"))
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		st, err := readState(cl)
		if err != nil {
			return err
		}
		tid := ""
		if *of != "" {
			if tid, err = taskID(st, *of); err != nil {
				return err
			}
		}
		var runs []*task.Run
		for _, r := range st.Runs {
			if (tid == "" || r.Task == tid) && (*all || tid != "" || task.Open(r.State) || r.EndedAt != nil && time.Since(*r.EndedAt) < 24*time.Hour) {
				runs = append(runs, r)
			}
		}
		sort.Slice(runs, func(i, j int) bool { return runs[i].QueuedAt.Before(runs[j].QueuedAt) })
		if *asJSON {
			return printJSON(runs)
		}
		if len(runs) == 0 {
			fmt.Print(i18n.T("cli.run.none"))
			return nil
		}
		printRuns(runs)
		return nil
	})
}

func cmdRunShow(args []string) error {
	fs := newFlags("run")
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		st, err := readState(cl)
		if err != nil {
			return err
		}
		id, err := runID(st, pos[0])
		if err != nil {
			return err
		}
		r := st.Runs[id]
		if *asJSON {
			return printJSON(r)
		}
		fmt.Print(i18n.F("cli.run.show", r.ID, r.Task, r.Machine, r.Agent, r.Dir, runState(r), took(r, time.Now()),
			orDash(r.Provider), orDash(r.Session), orDash(r.Reason)))
		return nil
	})
}

func cmdRunLogs(args []string) error {
	fs := newFlags("run")
	follow := fs.Bool("f", false, i18n.T("cli.run.flag_follow"))
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		st, err := readState(cl)
		if err != nil {
			return err
		}
		id, err := runID(st, pos[0])
		if err != nil {
			return err
		}
		file, printed := "", int64(-1)
		for {
			ctx, cancel := callTimeout()
			var t node.Tail
			err := cl.Call(ctx, coord.MRunTail, coord.TailParams{Run: id, Before: -1, Max: 256 << 10}, &t)
			cancel()
			if err != nil {
				return err
			}
			end := t.From + int64(len(t.Text))
			if t.File != file {
				file, printed = t.File, t.From
			}
			if end > printed && printed >= t.From {
				fmt.Print(render.Sanitize(t.Text[printed-t.From:]))
				printed = end
			}
			if !*follow {
				return nil
			}
			if st, err = readState(cl); err != nil {
				return err
			}
			if r := st.Runs[id]; r == nil || !task.Open(r.State) {
				return nil
			}
			time.Sleep(time.Second)
		}
	})
}

func cmdAgent(args []string) error {
	fs := newFlags("agent")
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	if first(args) == "list" || first(args) == "ls" {
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		ctx, cancel := callTimeout()
		defer cancel()
		var as coord.Agents
		if err := cl.Call(ctx, coord.MAgentList, nil, &as); err != nil {
			return err
		}
		if *asJSON {
			return printJSON(as.Agents)
		}
		rows := [][]string{{i18n.T("cli.agent.col_name"), i18n.T("cli.agent.col_provider"), i18n.T("cli.agent.col_model"),
			i18n.T("cli.agent.col_permission"), i18n.T("cli.agent.col_machine")}}
		for _, a := range as.Agents {
			rows = append(rows, []string{a.Name, a.Provider, orDash(a.Model), orDash(a.Permission), orDash(a.Machine)})
		}
		printTable(rows, termWidth())
		return nil
	})
}

var machineStates = map[string]string{
	coord.MachineConnected: "cli.machine.state_connected", coord.MachineConnecting: "cli.machine.state_connecting",
	coord.MachineOffline: "cli.machine.state_offline", coord.MachineIdle: "cli.machine.state_idle",
}

func cmdMachine(args []string) error {
	fs := newFlags("machine")
	connect := fs.Bool("connect", false, i18n.T("cli.machine.flag_connect"))
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	if first(args) == "list" || first(args) == "ls" {
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withCoord(wire.Options{}, func(cl *coord.Client) error {
		ctx, cancel := callTimeout()
		defer cancel()
		var ms coord.Machines
		if err := cl.Call(ctx, coord.MMachineList, coord.MachinesParams{Connect: *connect}, &ms); err != nil {
			return err
		}
		if *asJSON {
			return printJSON(ms.Machines)
		}
		rows := [][]string{{i18n.T("cli.machine.col_name"), i18n.T("cli.machine.col_state"), i18n.T("cli.machine.col_runs"),
			i18n.T("cli.machine.col_os"), i18n.T("cli.machine.col_version"), i18n.T("cli.machine.col_error")}}
		for _, m := range ms.Machines {
			errText := "-"
			if m.Error != "" {
				errText = reasonOf(&wire.Error{Code: m.Error, Detail: strings.TrimPrefix(m.Detail, m.Error+": ")})
			}
			rows = append(rows, []string{m.Name, i18n.T(machineStates[m.State]), i18n.F("cli.machine.runs_cell", m.Active, m.Slots, m.Queued),
				orDash(m.OS), orDash(m.Version), errText})
		}
		printTable(rows, termWidth())
		return nil
	})
}

// cmdService holds the coordinator lock until interrupted: it dispatches and watches runs and serves the socket.
func cmdService(args []string) error {
	fs := newFlags("service")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := coord.Open(coordOptions())
	if errors.Is(err, coord.ErrLocked) {
		return i18n.E("cli.service.running")
	}
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- c.Serve(ctx) }()
	go c.Run(ctx)
	fmt.Fprint(os.Stderr, i18n.F("cli.service.started", c.ID(), coord.SocketPath(fav.Home())))
	select {
	case <-ctx.Done():
		return nil
	case err := <-serveErr:
		return err
	}
}
