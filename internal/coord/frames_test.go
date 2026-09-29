package coord

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/oxsean/fav/internal/tend"
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
				table := map[string]any{"tasks": &e.c.st.Tasks, "runs": &e.c.st.Runs, "projects": &e.c.st.Projects, "shares": &e.c.st.Shares,
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
