package coord

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/defs"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// The Web UI's frame files give u_b, a participant in p1 on machines u_a owns, the affordances the coordinator counts
// for them on the state those files hold.
func TestTheWebFramesOfferWhatTheCoordinatorOffers(t *testing.T) {
	for _, name := range []string{"tasks-state", "output-state"} {
		t.Run(name, func(t *testing.T) {
			hosts := []tend.Host{{Name: "mba", SSH: "mba"}, {Name: "linux", SSH: "linux"}, {Name: "win", SSH: "win"}}
			e := newEnv(t, tend.Config{Hosts: hosts, Agents: []tend.AgentProfile{{Name: "claude", Provider: "claude"}, {Name: "codex", Provider: "codex"}}})
			e.owner = func(string) string { return "u_a" }
			e.users = map[string]User{"u_a": {ID: "u_a", Name: "Ann"}, "u_b": {ID: "u_b", Name: "Bo"}}
			e.dial = unreachable
			e.start()
			parts, want := framedState(t, name)
			e.c.mu.Lock()
			for part, items := range parts {
				table := map[string]any{"tasks": &e.c.st.Tasks, "runs": &e.c.st.Runs, "projects": &e.c.st.Projects, "shares": &e.c.st.Shares, "drains": &e.c.st.Drains,
					"agent_defs": &e.c.st.AgentDefs}[part]
				if err := json.Unmarshal(items, table); err != nil {
					t.Fatal(part, err)
				}
			}
			got := Affordances{Runs: map[string][]string{}, Tasks: map[string]*TaskAffordance{}}
			for k, v := range e.c.affordances(Principal{User: "u_b"}, nil) {
				if v == "" {
					continue
				}
				if k[0] == 'r' {
					var acts []string
					json.Unmarshal([]byte(v), &acts)
					got.Runs[k[2:]] = acts
				} else {
					var a TaskAffordance
					json.Unmarshal([]byte(v), &a)
					got.Tasks[k[2:]] = &a
				}
			}
			e.c.mu.Unlock()
			if !reflect.DeepEqual(got, want) {
				b, _ := json.Marshal(got)
				t.Fatalf("the coordinator offers:\n%s", b)
			}
		})
	}
}

// framedState is the snapshot parts stream 2 of a frame file pushes, and its affordances part.
func framedState(t *testing.T, name string) (map[string]json.RawMessage, Affordances) {
	f, err := os.Open(filepath.Join("..", "server", "webtest", "frames", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	parts := map[string]json.RawMessage{}
	var aff Affordances
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<22)
	for sc.Scan() {
		var l struct {
			S *struct {
				ID     int64
				Method string
				Params struct {
					Part  string
					Items json.RawMessage
				}
			}
		}
		json.Unmarshal(sc.Bytes(), &l)
		if l.S == nil || l.S.ID != 2 || l.S.Method != "snapshot" {
			continue
		}
		if l.S.Params.Part == "affordances" {
			json.Unmarshal(l.S.Params.Items, &aff)
		} else {
			parts[l.S.Params.Part] = l.S.Params.Items
		}
	}
	return parts, aff
}

// The agent page's frames hold what the coordinator answers Bo (u_b, a member): his lists of definitions and agents,
// the definitions his state shows, and what each of his writes answers and pushes. The definitions he may not read
// are the coordinator's alone: ann-plan is shared with him for use only, ann-secret with nobody.
func TestTheWebAgentFramesAreTheCoordinators(t *testing.T) {
	for _, name := range []string{"agents-state", "agents-edit", "agents-share", "agents-check"} {
		t.Run(name, func(t *testing.T) {
			c := framedAgents(t)
			if name != "agents-state" {
				replayAgentFrames(t, c, "agents-state")
			}
			replayAgentFrames(t, c, name)
		})
	}
}

// framedLog is an event log in memory whose n-th envelope is written at 14:33 + n minutes on the frames' day.
type framedLog struct {
	seq  int64
	envs []journal.Envelope
}

func (l *framedLog) Append(actor journal.Actor, cmd *journal.Receipt, events []journal.Event) (journal.Envelope, error) {
	at := time.Date(2026, 9, 30, 14, 33+len(l.envs), 0, 0, time.UTC)
	env := journal.Envelope{V: journal.Version, Seq: l.seq + 1, At: at, Actor: &actor, Command: cmd, Events: events}
	if cmd != nil && cmd.Answer != nil {
		cmd.Result = cmd.Answer(env)
	}
	l.seq = env.Seq
	l.envs = append(l.envs, env)
	return env, nil
}

func (l *framedLog) ReadAfter(after, upTo int64, fn func(journal.Envelope) bool) error {
	for _, env := range l.envs {
		if env.Seq > after && env.Seq <= upTo && !fn(env) {
			break
		}
	}
	return nil
}

func (l *framedLog) Seq() int64      { return l.seq }
func (l *framedLog) ReadOnly() error { return nil }
func (l *framedLog) Close() error    { return nil }
func (l *framedLog) at(seq int64) int {
	return slices.IndexFunc(l.envs, func(e journal.Envelope) bool { return e.Seq == seq })
}

// framedAgents is a coordinator holding the state agents-state shows Bo, the definitions he may not read, and the
// profiles its config names.
func framedAgents(t *testing.T) *Coord {
	log := &framedLog{}
	owners := map[string]string{"mba": "u_a", "linux": "u_a", "bo-laptop": "u_b"}
	people := map[string]User{"u_a": {ID: "u_a", Name: "Ann Lee", Admin: true}, "u_b": {ID: "u_b", Name: "Bo Lin"}, "u_c": {ID: "u_c", Name: "Cy Park"}}
	c, err := Open(Options{Home: t.TempDir(), Version: "test", Config: tend.Config{Agents: []tend.AgentProfile{
		{Name: "quick", Provider: "claude", Model: "haiku", Permission: "acceptEdits"}, {Name: "codex-high", Provider: "codex", Effort: "xhigh"}}},
		MachineOwner: func(m string) string { return owners[m] }, Users: func(id string) (User, bool) { u, ok := people[id]; return u, ok },
		OpenLog: func(string, func(journal.Envelope) error) (EventLog, error) { return log, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	parts, _ := framedState(t, "agents-state")
	for part, items := range parts {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(items, &m); err != nil {
			t.Fatal(part, err)
		}
		if err := c.st.Take(part, m); err != nil {
			t.Fatal(part, err)
		}
	}
	at := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	c.st.AgentDefs["ann-plan"] = &task.AgentDef{AgentDef: defs.AgentDef{Name: "ann-plan", Description: "Breaks features into tasks", Role: "planner",
		Provider: "claude", Model: "opus", Effort: "max", Output: "plan", Body: "Cut the feature into tasks a day long or less."},
		Owner: "u_a", Share: task.DefShare{Users: []string{"u_b"}}, Rev: 1, UpdatedAt: at}
	c.st.AgentDefs["ann-secret"] = &task.AgentDef{AgentDef: defs.AgentDef{Name: "ann-secret", Role: "any", Provider: "claude"}, Owner: "u_a", Rev: 1, UpdatedAt: at}
	c.st.Seq, log.seq = 40, 40
	return c
}

// replayAgentFrames asks the coordinator what Bo sends in frame file name and compares its answers and pushes with
// what the file has the server send.
func replayAgentFrames(t *testing.T, c *Coord, name string) {
	bo := Principal{User: "u_b"}
	log := c.log.(*framedLog)
	f, err := os.Open(filepath.Join("..", "server", "webtest", "frames", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	norm := func(v any) any {
		b, _ := json.Marshal(v)
		var out any
		json.Unmarshal(b, &out)
		return out
	}
	same := func(at string, got any, want json.RawMessage) {
		var w any
		json.Unmarshal(want, &w)
		if g := norm(got); !reflect.DeepEqual(g, w) {
			b, _ := json.Marshal(got)
			t.Errorf("%s: the coordinator has\n%s", at, b)
		}
	}
	answers, pushed := map[int64]any{}, int64(-1)
	var unpushed []int64
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<22)
	for n := 1; sc.Scan(); n++ {
		at := fmt.Sprintf("%s.jsonl:%d", name, n)
		var l struct {
			C *struct {
				ID        int64
				Method    string
				CommandID string `json:"command_id"`
				Params    json.RawMessage
			}
			S *struct {
				Type   string
				ID     int64
				Method string
				Result json.RawMessage
				Error  json.RawMessage
				Params json.RawMessage
			}
		}
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatal(at, err)
		}
		switch {
		case l.C != nil && l.C.ID > 4:
			res, err := c.HandlerFor(bo)(context.Background(), &wire.Request{Method: l.C.Method, CommandID: l.C.CommandID, Params: l.C.Params})
			if err != nil {
				answers[l.C.ID] = map[string]any{"error": err}
			} else {
				answers[l.C.ID] = map[string]any{"result": res}
			}
			if s := log.seq; s != pushed && l.C.CommandID != "" && err == nil {
				unpushed = append(unpushed, s)
				pushed = s
			}
		case l.S != nil && l.S.Type == "res" && answers[l.S.ID] != nil:
			if l.S.Error != nil {
				same(at, answers[l.S.ID], json.RawMessage(`{"error":`+string(l.S.Error)+`}`))
			} else {
				if l.S.Result == nil {
					l.S.Result = json.RawMessage("null")
				}
				same(at, answers[l.S.ID], json.RawMessage(`{"result":`+string(l.S.Result)+`}`))
			}
		case l.S != nil && l.S.ID == 2 && l.S.Method == "journal":
			if len(unpushed) == 0 {
				t.Fatalf("%s: a journal push the coordinator has nothing for", at)
			}
			env := log.envs[log.at(unpushed[0])]
			unpushed = unpushed[1:]
			if reshapes(env) {
				t.Errorf("%s: the coordinator resets the state for seq %d", at, env.Seq)
			}
			c.mu.Lock()
			v := c.visibleEnv(bo, env)
			c.mu.Unlock()
			same(at, v, l.S.Params)
		case l.S != nil && l.S.ID == 2 && l.S.Method == "reset":
			for _, s := range unpushed {
				if !reshapes(log.envs[log.at(s)]) {
					t.Errorf("%s: the coordinator pushes seq %d as it is", at, s)
				}
			}
			unpushed = nil
		case l.S != nil && l.S.ID == 2 && l.S.Method == "snapshot":
			var p struct {
				Part  string
				Items json.RawMessage
			}
			json.Unmarshal(l.S.Params, &p)
			st := c.visibleState(bo, c.state(true))
			table := map[string]any{"projects": st.Projects, "tasks": st.Tasks, "runs": st.Runs, "shares": st.Shares, "drains": st.Drains, "agent_defs": st.AgentDefs}[p.Part]
			same(at+" "+p.Part, table, p.Items)
		case l.S != nil && l.S.ID == 2 && l.S.Method == "live":
			same(at, map[string]int64{"seq": c.st.Seq}, l.S.Params)
		}
	}
	if len(unpushed) > 0 {
		t.Errorf("%s: seqs %v are never pushed", name, unpushed)
	}
}
