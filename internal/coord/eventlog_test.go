package coord

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// memLog is an EventLog in memory: what the coordinator needs of a log, and nothing of the JSONL journal.
type memLog struct {
	mu   sync.Mutex
	envs []journal.Envelope
}

func (l *memLog) open(_ string, fold func(journal.Envelope) error) (EventLog, error) {
	for _, env := range l.envs {
		if err := fold(env); err != nil {
			return nil, err
		}
	}
	return l, nil
}

func (l *memLog) Append(actor journal.Actor, cmd *journal.Receipt, events []journal.Event) (journal.Envelope, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	env := journal.Envelope{V: journal.Version, Seq: int64(len(l.envs)) + 1, At: time.Now().UTC(), Actor: &actor, Command: cmd, Events: events}
	if cmd != nil && cmd.Answer != nil {
		cmd.Result = cmd.Answer(env)
	}
	b, _ := json.Marshal(env) // what a log on disk would read back
	var back journal.Envelope
	json.Unmarshal(b, &back)
	l.envs = append(l.envs, back)
	return env, nil
}

func (l *memLog) ReadAfter(after, upTo int64, fn func(journal.Envelope) bool) error {
	l.mu.Lock()
	envs := append([]journal.Envelope(nil), l.envs...)
	l.mu.Unlock()
	for _, env := range envs {
		if env.Seq > after && env.Seq <= upTo && !fn(env) {
			break
		}
	}
	return nil
}

func (l *memLog) Seq() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return int64(len(l.envs))
}

func (l *memLog) ReadOnly() error { return nil }
func (l *memLog) Close() error    { return nil }

func TestTheCoordinatorRunsOnAnyEventLog(t *testing.T) {
	log := &memLog{}
	home := t.TempDir()
	open := func() *Coord {
		c, err := Open(Options{Home: home, Version: "test", Config: tend.Config{}, Remote: true, OpenLog: log.open})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := open()
	cli := (&env{t: t, c: c}).as(Owner)
	var tk task.Task
	if err := callAs(cli, MTaskCreate, "c1", TaskCreate{Title: "kept elsewhere"}, &tk); err != nil {
		t.Fatal(err)
	}
	c.Close()
	c = open()
	defer c.Close()
	if got := c.State().Tasks[tk.ID]; got == nil || got.Title != "kept elsewhere" || log.Seq() != 1 {
		t.Fatalf("state folds from the log: %+v", got)
	}
	var again task.Task
	if err := callAs((&env{t: t, c: c}).as(Owner), MTaskCreate, "c1", TaskCreate{Title: "kept elsewhere"}, &again); err != nil || again.ID != tk.ID || log.Seq() != 1 {
		t.Fatalf("the receipt comes back from the log: %v %s %s", err, tk.ID, again.ID)
	}
}

func TestAServerWithoutNodesListsNoMachinesAsAnEmptyList(t *testing.T) {
	c, err := Open(Options{Home: t.TempDir(), Version: "test", Config: tend.Config{}, Remote: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b, _ := json.Marshal(c.Machines(context.Background(), false))
	if string(b) != `{"machines":[]}` {
		t.Fatalf("the web UI maps over this list: %s", b)
	}
}

func sqliteLog(dir string, fold func(journal.Envelope) error) (EventLog, error) {
	l, err := store.Open(filepath.Join(dir, store.File), fold)
	if err != nil {
		return nil, err
	}
	return l, nil
}

func TestTheSuiteRunsOnTheLogItNames(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	_, onSQLite := e.c.log.(*store.Log)
	if want := os.Getenv("TEND_TEST_LOG") == "sqlite"; onSQLite != want {
		t.Fatalf("TEND_TEST_LOG=%q but the log is %T", os.Getenv("TEND_TEST_LOG"), e.c.log)
	}
}
