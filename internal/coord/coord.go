// Package coord is the coordinator: the one writer of tasks and runs. Whoever holds <home>/coord/lock is it (a TUI,
// a CLI command, `tend service` or `tend server`); others reach it over its socket or WebSocket. It keeps the journal,
// answers clients, and converges each run's wanted state with what its machine's node reports.
package coord

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Local is this machine's name among the machines.
const Local = "local"

// Client methods.
const (
	MStateGet       = "state.get"
	MTaskCreate     = "task.create"
	MTaskEdit       = "task.edit"
	MTaskStatus     = "task.set_status"
	MRunDispatch    = "run.dispatch"
	MRunStop        = "run.stop"
	MRunAbandon     = "run.abandon"
	MRunTail        = "run.tail"
	MAgentList      = "agent.list"
	MMachineList    = "machine.list"
	MSubscribe      = "subscribe"
	MNodeCall       = "node.call"
	MTaskGet        = "task.get"
	MRunPreview     = "run.preview"
	MRunContinue    = "run.continue"
	MRunAnswer      = "run.answer"
	MRunSend        = "run.send"
	MRunMessages    = "run.messages"
	MProjectCreate  = "project.create"
	MProjectEdit    = "project.edit"
	MProjectMember  = "project.member"
	MMachineShare   = "machine.share"
	MTaskStart      = "task.start"
	MTaskSync       = "task.sync"
	MTaskSourceAck  = "task.source_ack"
	MTaskGate       = "task.gate"
	MTaskMerge      = "task.merge"
	MTaskMessage    = "task.message"
	MTaskMove       = "task.move"
	MAgentDefList   = "agentdef.list"
	MAgentDefGet    = "agentdef.get"
	MAgentDefSave   = "agentdef.save"
	MAgentDefRemove = "agentdef.remove"
	MAgentDefShare  = "agentdef.share"
	MInboxList      = "inbox.list"
	MUserOffboard   = "user.offboard"
	PushJournal     = "journal"
	PushRefetch     = "refetch" // what the subscriber may see changed: fetch the state again
	defaultSlots    = 2
	subscribeQueue  = 256
)

// ErrLocked: another process is the coordinator.
var ErrLocked = filelock.ErrLocked

type Options struct {
	Home    string
	Version string
	Config  tend.Config
	// Node is this machine's node; Sessions answers its session reads.
	Node     *node.Node
	Sessions remote.Handler
	// Dial reaches a configured host's node; tests replace it.
	Dial func(h tend.Host, opt wire.Options) (Conn, error)
	// Remote: mode 2. The machines are the nodes that dial in (Attach), named by their tokens; there is no local
	// node and no ssh.
	Remote bool
	Nodes  []string
	// MachineOwner (mode 2 with a team) names the user who owns a machine; nil: Owner owns every machine and is the
	// only user.
	MachineOwner func(machine string) string
	// Users finds a user of the team (mode 2).
	Users func(id string) (User, bool)
	// Notice receives each task that came to need someone or got done (mode 2 delivers them); it runs under the
	// coordinator's lock and must not block.
	Notice func(Notice)
	// OpenLog opens the event log in dir, folding every envelope in order; nil is the JSONL journal.
	OpenLog func(dir string, fold func(journal.Envelope) error) (EventLog, error)
}

// EventLog keeps the coordinator's envelopes: numbered without gaps, each on disk before Append returns.
type EventLog interface {
	Append(actor journal.Actor, cmd *journal.Receipt, events []journal.Event) (journal.Envelope, error)
	// ReadAfter calls fn for each envelope with seq in (after, upTo], in order, until fn returns false.
	ReadAfter(after, upTo int64, fn func(journal.Envelope) bool) error
	Seq() int64
	// ReadOnly is why the log takes no appends, nil when it does.
	ReadOnly() error
	Close() error
}

// defaultLog opens the log when Options.OpenLog is nil; the coord tests run on SQLite with TEND_TEST_LOG=sqlite.
var defaultLog = openJournal

func openJournal(dir string, fold func(journal.Envelope) error) (EventLog, error) {
	return journal.Open(filepath.Join(dir, "events.jsonl"), fold)
}

type Coord struct {
	opt      Options
	id       string
	unlock   func()
	log      EventLog
	mu       sync.Mutex
	st       *task.State
	receipts map[string]journal.Receipt
	subs     map[*wire.Conn]*sub
	ms       map[string]*machine
	sent     map[string]time.Time // runs whose run.start went out, when
	acked    map[string][]string  // per machine: ended runs recorded, to acknowledge
	ackDone  map[string]bool
	missing  map[string]int // open runs by how many lists of their node in a row lacked them
	passMu   sync.Mutex
	wake     chan struct{}
	// localCalls are the calls this machine's node is answering in this process: Close lets them finish
	localCalls sync.WaitGroup
}

// Open takes the coordinator lock and reads the journal; ErrLocked when another process has it.
func Open(opt Options) (*Coord, error) {
	dir := filepath.Join(opt.Home, "coord")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	unlock, err := filelock.TryLock(filepath.Join(dir, "lock"))
	if err != nil {
		return nil, err
	}
	c := &Coord{opt: opt, unlock: unlock, st: task.New(), receipts: map[string]journal.Receipt{}, subs: map[*wire.Conn]*sub{},
		ms: map[string]*machine{}, sent: map[string]time.Time{}, acked: map[string][]string{}, ackDone: map[string]bool{}, missing: map[string]int{},
		wake: make(chan struct{}, 1)}
	if c.id, err = coordID(dir); err != nil {
		unlock()
		return nil, err
	}
	open := opt.OpenLog
	if open == nil {
		open = defaultLog
	}
	c.log, err = open(dir, func(env journal.Envelope) error {
		if env.Command != nil {
			c.receipts[receiptKey(env.Who().ID, env.Command.ID)] = *env.Command
		}
		return c.st.Apply(env)
	})
	if err != nil {
		unlock()
		return nil, err
	}
	c.machines()
	return c, nil
}

// coordID names this coordinator to nodes: they list only the runs it started.
func coordID(dir string) (string, error) {
	p := filepath.Join(dir, "id")
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return strings.TrimSpace(string(b)), nil
	}
	var b [8]byte
	rand.Read(b[:])
	id := "c_" + hex.EncodeToString(b[:])
	return id, fileio.WriteFile(p, []byte(id+"\n"), 0o600) // ⚠️ a lost id hides every run from its nodes
}

func (c *Coord) ID() string { return c.id }

// Close ends every connection and gives up the lock.
func (c *Coord) Close() {
	c.mu.Lock()
	for _, m := range c.ms {
		if m.conn != nil {
			m.conn.Close()
		}
	}
	for conn, s := range c.subs {
		delete(c.subs, conn)
		close(s.ch)
	}
	c.mu.Unlock()
	c.passMu.Lock()
	defer c.passMu.Unlock()
	done := make(chan struct{})
	go func() { c.localCalls.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	c.log.Close()
	c.unlock()
}

// State is a copy of the state.
func (c *Coord) State() *task.State { return c.state(true) }

// state is a copy of the state, with or without the briefs.
func (c *Coord) state(briefs bool) *task.State {
	c.mu.Lock()
	b, _ := json.Marshal(c.st)
	c.mu.Unlock()
	s := task.New()
	json.Unmarshal(b, s)
	if !briefs {
		for _, t := range s.Tasks {
			t.Brief = ""
		}
		for _, r := range s.Runs {
			r.Brief = ""
		}
	}
	return s
}

func (c *Coord) poke() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// commit appends events from actor (with cmd's receipt) and applies them; the caller holds mu.
func (c *Coord) commit(actor journal.Actor, cmd *journal.Receipt, events ...journal.Event) error {
	if len(events) == 0 {
		return nil
	}
	env, err := c.log.Append(actor, cmd, events)
	if err != nil {
		return err
	}
	if cmd != nil {
		c.receipts[receiptKey(actor.ID, cmd.ID)] = *cmd
	}
	before := c.observed(events)
	var sits map[string]task.Situation
	if c.opt.Notice != nil || len(c.opt.Config.NotifyCommand) > 0 {
		sits = c.situations(c.touched(events))
	}
	if err := c.st.Apply(env); err != nil {
		return err
	}
	c.notify(before)
	if sits != nil {
		c.deliver(c.notices(env, sits))
	}
	c.publish(env)
	return nil
}

// Profiles are the agents one can run.
func (c *Coord) Profiles() []tend.AgentProfile { return agent.Profiles(c.opt.Config.Agents) }

func (c *Coord) profile(name string) (tend.AgentProfile, bool) {
	for _, p := range c.Profiles() {
		if p.Name == name {
			return p, true
		}
	}
	return tend.AgentProfile{}, false
}

func newID(prefix string) string {
	var b [6]byte
	rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

var errNoCommand = &wire.Error{Code: wire.CodeBadRequest, Detail: "command_id"}

func bad(detail string) error { return &wire.Error{Code: wire.CodeBadRequest, Detail: detail} }

func conflict(detail string) error { return &wire.Error{Code: wire.CodeConflict, Detail: detail} }

func notFound(detail string) error { return &wire.Error{Code: wire.CodeNotFound, Detail: detail} }
