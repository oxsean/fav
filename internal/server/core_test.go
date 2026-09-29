package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
)

// jsCase is one case of a core test file as webtest/check.js reports it.
type jsCase struct {
	Name  string          `json:"name"`
	Error string          `json:"error"`
	Data  json.RawMessage `json:"data"`
}

// runModule runs an ES module under node and returns its cases, each already reported as a subtest.
func runModule(t *testing.T, entry string, args ...string) map[string]jsCase {
	t.Helper()
	path, _ := filepath.Abs(entry)
	var stderr bytes.Buffer
	cmd := exec.Command(nodeJS(t), append([]string{path}, args...)...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node %s: %v\n%s", entry, err, stderr.String())
	}
	var cases []jsCase
	if err := json.Unmarshal(out, &cases); err != nil || len(cases) == 0 {
		t.Fatalf("node %s printed %q: %v\n%s", entry, out, err, stderr.String())
	}
	byName := map[string]jsCase{}
	for _, c := range cases {
		byName[c.Name] = c
		t.Run(c.Name, func(t *testing.T) {
			if c.Error != "" {
				t.Fatal(c.Error)
			}
		})
	}
	return byName
}

func TestTheCoreWireSpeaksTheFrames(t *testing.T) { runModule(t, "webtest/wire_test.js") }

func TestTheCoreKeysRouterAndWords(t *testing.T) { runModule(t, "webtest/core_test.js") }

func TestTheSharedComponentsInBothForms(t *testing.T) { runModule(t, "webtest/ui_test.js") }

// The store folds a state.watch as the coordinator folds the same snapshot and envelopes.
func TestTheStoreFoldsAStateWatchAsTheCoordinatorDoes(t *testing.T) {
	cases := runModule(t, "webtest/store_test.js")
	c, ok := cases["state.watch: snapshot parts, live, envelopes, reset"]
	if !ok || c.Error != "" {
		t.Fatal("the state.watch case did not pass")
	}
	var page map[string]any
	json.Unmarshal(c.Data, &page)
	for at, until := range map[string]string{"live": "live", "final": ""} {
		var want any
		b, _ := json.Marshal(foldFrames(t, "state-snapshot", 2, until))
		json.Unmarshal(b, &want)
		if !reflect.DeepEqual(normalized(page[at]), normalized(want)) {
			p, _ := json.MarshalIndent(normalized(page[at]), "", " ")
			g, _ := json.MarshalIndent(normalized(want), "", " ")
			t.Fatalf("at %s, the store:\n%s\nGo:\n%s", at, p, g)
		}
	}
}

type frameLine struct {
	Step string
	C, S *struct {
		Type   string          `json:"type"`
		ID     int64           `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
	}
}

func frameLines(t *testing.T, name string) []frameLine {
	t.Helper()
	f, err := os.Open(filepath.Join("webtest", "frames", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []frameLine
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for n := 1; sc.Scan(); n++ {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var l frameLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("%s.jsonl:%d: %v", name, n, err)
		}
		out = append(out, l)
	}
	return out
}

// foldFrames folds what the server pushes on stream id of a frame file the way the coordinator's state is built:
// snapshot parts decoded strictly into task.State's tables, envelopes applied past the seq; until names the step it
// stops at ("": the whole file).
func foldFrames(t *testing.T, name string, id int64, until string) *task.State {
	t.Helper()
	var st, staged *task.State
	for _, l := range frameLines(t, name) {
		if until != "" && l.Step == until {
			break
		}
		if l.S == nil || l.S.Type != "push" || l.S.ID != id {
			continue
		}
		switch l.S.Method {
		case "open":
			var p struct{ Mode string }
			json.Unmarshal(l.S.Params, &p)
			if p.Mode == "snapshot" {
				staged = task.New()
			}
		case "reset":
			staged = task.New()
		case "snapshot":
			var p struct {
				Part  string          `json:"part"`
				Items json.RawMessage `json:"items"`
			}
			json.Unmarshal(l.S.Params, &p)
			if p.Part == "affordances" {
				continue
			}
			part := map[string]any{"tasks": &staged.Tasks, "runs": &staged.Runs, "projects": &staged.Projects, "shares": &staged.Shares,
				"agent_defs": &staged.AgentDefs}[p.Part]
			if part == nil {
				t.Fatalf("%s: no table %s", name, p.Part)
			}
			strictInto(t, name, p.Items, part)
		case "live":
			var p struct{ Seq int64 }
			json.Unmarshal(l.S.Params, &p)
			staged.Seq, st, staged = p.Seq, staged, nil
		case "journal":
			var env journal.Envelope
			if err := json.Unmarshal(l.S.Params, &env); err != nil {
				t.Fatal(err)
			}
			if env.Seq <= st.Seq {
				continue
			}
			if err := st.Apply(env); err != nil {
				t.Fatalf("%s seq %d: %v", name, env.Seq, err)
			}
		}
	}
	return st
}

// strictInto decodes the items of a snapshot part into its table, adding to what it holds; a field the Go type does
// not have fails, so the frame files speak task.State's shapes.
func strictInto(t *testing.T, name string, items json.RawMessage, table any) {
	t.Helper()
	fresh := reflect.New(reflect.TypeOf(table).Elem())
	dec := json.NewDecoder(bytes.NewReader(items))
	dec.DisallowUnknownFields()
	if err := dec.Decode(fresh.Interface()); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	dst := reflect.ValueOf(table).Elem()
	iter := fresh.Elem().MapRange()
	for iter.Next() {
		dst.SetMapIndex(iter.Key(), iter.Value())
	}
}

// Every frame file is played by a core test, every line is one the player knows, and the frames are wire frames.
func TestEveryFrameFileIsPlayed(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("webtest", "frames", "*.jsonl"))
	tests, _ := filepath.Glob(filepath.Join("webtest", "*_test.js"))
	var src strings.Builder
	for _, f := range tests {
		b, _ := os.ReadFile(f)
		src.Write(b)
	}
	keys := []string{"c", "s", "raw", "connect", "refuse", "drop", "dialing", "wait_ms", "step", "note"}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		if !strings.Contains(src.String(), "play('"+name+"'") {
			t.Errorf("%s: no test plays it", name)
		}
		b, _ := os.ReadFile(f)
		for n, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			var l map[string]json.RawMessage
			if err := json.Unmarshal([]byte(line), &l); err != nil || len(l) != 1 {
				t.Errorf("%s:%d: one key per line: %s", name, n+1, line)
				continue
			}
			for k, v := range l {
				if !slices.Contains(keys, k) {
					t.Errorf("%s:%d: no such line %s", name, n+1, k)
				}
				if k == "c" || k == "s" {
					var fr struct{ Type string }
					json.Unmarshal(v, &fr)
					if !slices.Contains([]string{"req", "res", "push", "cancel"}, fr.Type) {
						t.Errorf("%s:%d: frame type %q", name, n+1, fr.Type)
					}
				}
			}
		}
	}
	if len(files) < 15 {
		t.Fatalf("%d frame files", len(files))
	}
}

// Every snapshot in the frame files decodes into task.State's tables, whatever test plays it.
func TestFrameSnapshotsAreTheCoordinatorsShapes(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("webtest", "frames", "*.jsonl"))
	part := regexp.MustCompile(`"method":"snapshot"`)
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		b, _ := os.ReadFile(f)
		if !part.Match(b) {
			continue
		}
		for _, l := range frameLines(t, name) {
			if l.S == nil || l.S.Method != "snapshot" {
				continue
			}
			var p struct {
				Part  string          `json:"part"`
				Items json.RawMessage `json:"items"`
			}
			json.Unmarshal(l.S.Params, &p)
			st := task.New()
			table := map[string]any{"tasks": &st.Tasks, "runs": &st.Runs, "projects": &st.Projects, "shares": &st.Shares, "agent_defs": &st.AgentDefs}[p.Part]
			if table != nil {
				strictInto(t, name, p.Items, table)
			}
		}
	}
}
