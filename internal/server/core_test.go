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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/store"
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

func TestTheFiguresActionsAndWritesUnderThePages(t *testing.T) {
	runModule(t, "webtest/select_test.js")
}

func TestThePagesInBothFormsAndLanguages(t *testing.T) { runModule(t, "webtest/pages_test.js") }

func TestTheChangesOfARunInPagesAndOnTheTab(t *testing.T) { runModule(t, "webtest/changes_test.js") }

func TestTheRunsPageAndARunsOwn(t *testing.T) { runModule(t, "webtest/runs_test.js") }

func TestTheMachinesPage(t *testing.T) { runModule(t, "webtest/team_test.js") }

func TestTheTaskPagesBoardsTreesAndDrafts(t *testing.T) { runModule(t, "webtest/tasks_test.js") }

func TestTheTaskPagesAndFormsInBothFormsAndLanguages(t *testing.T) {
	runModule(t, "webtest/taskpages_test.js")
}

// The timeline lays out events as output.Items does: the real transcripts in internal/output's testdata, and the
// shapes they lack (a group broken by a parent, a question among reads, a result before its call, a temp event).
func TestTheTimelineLaysOutEventsAsOutputItems(t *testing.T) {
	byName := map[string][]output.Event{}
	for _, name := range []string{"claude.jsonl", "codex-app-server.jsonl"} {
		b, err := os.ReadFile(filepath.Join("..", "output", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		evs, _, _ := output.Parse("f:1", 0, string(b), output.State{})
		if len(output.Items(evs)) < 10 {
			t.Fatalf("%s: %d items", name, len(output.Items(evs)))
		}
		byName[name] = evs
	}
	ev := func(id, kind, family string, more ...string) output.Event {
		e := output.Event{ID: id, Kind: kind, Family: family}
		for _, m := range more {
			k, v, _ := strings.Cut(m, "=")
			switch k {
			case "call":
				e.Call = v
			case "ref":
				e.Ref = v
			case "parent":
				e.Parent = v
			case "request":
				e.Request = v
			case "temp":
				e.Temp, e.Key = true, v
			}
		}
		return e
	}
	byName["shapes"] = []output.Event{
		ev("1", output.KindToolResult, "", "ref=c1"), ev("2", output.KindTool, output.FamilyRead, "call=c1"),
		ev("3", output.KindTool, output.FamilySearch, "call=c2"), ev("4", output.KindTool, output.FamilyRead, "call=c3", "parent=a"),
		ev("5", output.KindTool, output.FamilyRead, "call=c4", "parent=a"), ev("6", output.KindTool, "ask", "call=c5", "request=q"),
		ev("7", output.KindTool, output.FamilyRead, "call=c6", "request=r"), ev("", output.KindSay, "", "temp=f:9"),
		ev("8", output.KindTool, output.FamilyRead, "call=c7"), ev("9", output.KindToolResult, "", "ref=zz"), ev("10", output.KindToolResult, "", "ref=c7"),
		ev("11", output.KindTool, output.FamilySearch, "call=c8"),
	}
	b, _ := json.Marshal(byName)
	path := filepath.Join(t.TempDir(), "events.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := runModule(t, "webtest/output_test.js", path)
	var got map[string][]output.Item
	if err := json.Unmarshal(cases["items as output.Items lays them"].Data, &got); err != nil {
		t.Fatal(err)
	}
	for name, evs := range byName {
		if want := output.Items(evs); !reflect.DeepEqual(got[name], want) {
			t.Errorf("%s: the timeline laid out\n%v\noutput.Items\n%v", name, got[name], want)
		}
	}
}

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
	tests, _ := filepath.Glob(filepath.Join("webtest", "*.js"))
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

// strictDecode decodes raw into v, failing on a field v's type does not have.
func strictDecode(t *testing.T, where string, raw json.RawMessage, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Errorf("%s: %v", where, err)
	}
}

// The /api answers the page tests read (webtest/api.json) are the server's shapes.
func TestPageAPIAnswersAreTheServersShapes(t *testing.T) {
	shapes := map[string]func() any{
		"GET /api/users": func() any { return new([]PublicUser) }, "GET /api/admits": func() any { return new([]store.Admit) },
		"GET /api/invites": func() any { return new([]store.PendingInvite) }, "GET /api/audit": func() any { return new([]store.AuditEntry) },
		"GET /api/machines": func() any { return new([]store.Credential) },
		"POST /api/machines": func() any {
			return new(struct {
				ID      string `json:"id"`
				Token   string `json:"token"`
				Command string `json:"command"`
			})
		},
		"POST /api/invites": func() any {
			return new(struct {
				URL     string    `json:"url"`
				Expires time.Time `json:"expires"`
			})
		},
		"GET /api/trackers": func() any { return new([]TrackerView) }, "POST /api/trackers": func() any { return new(TrackerView) },
		"GET /api/trackers/issues": func() any { return new([]TrackerIssueView) },
		"GET /api/trackers/preview": func() any {
			return new(struct {
				Body string `json:"body"`
			})
		},
	}
	b, err := os.ReadFile(filepath.Join("webtest", "api.json"))
	if err != nil {
		t.Fatal(err)
	}
	var answers map[string]json.RawMessage
	if err := json.Unmarshal(b, &answers); err != nil {
		t.Fatal(err)
	}
	for k, raw := range answers {
		mk := shapes[k]
		if mk == nil {
			t.Errorf("api.json: %s has no shape here", k)
			continue
		}
		strictDecode(t, "api.json "+k, raw, mk())
	}
}

// The writes, their answers, the machines and inbox lists and the affordances in the frame files are the coordinator's
// shapes.
func TestFrameWritesAndListsAreTheCoordinatorsShapes(t *testing.T) {
	params := map[string]func() any{
		coord.MTaskStatus: func() any { return new(task.TaskStatus) }, coord.MRunDispatch: func() any { return new(coord.Dispatch) },
		coord.MRunStop: func() any { return new(task.RunRef) }, coord.MRunAnswer: func() any { return new(coord.Answer) },
		coord.MRunContinue: func() any { return new(coord.Continue) }, coord.MRunOutputPage: func() any { return new(coord.OutputPageParams) },
		coord.MTaskCreate: func() any { return new(coord.TaskCreate) }, coord.MTaskStart: func() any { return new(coord.TaskRef) },
		coord.MTaskMerge: func() any { return new(coord.TaskRef) }, coord.MTaskMove: func() any { return new(task.TaskMove) },
		coord.MTaskPlanSave: func() any { return new(coord.PlanSave) }, coord.MTaskPlanApply: func() any { return new(coord.PlanApply) },
		coord.MTaskGate: func() any { return new(coord.TaskGate) }, coord.MTaskSourceAck: func() any { return new(task.SourceAck) },
		coord.MRunPreview: func() any { return new(coord.Dispatch) }, coord.MAgentList: func() any { return new(struct{}) },
		coord.MTaskMessage: func() any { return new(coord.TaskMessage) }, coord.MTaskMessagePreview: func() any { return new(coord.MessagePreview) },
		coord.MRunSend: func() any { return new(coord.SendMessage) }, coord.MRunInterrupt: func() any { return new(coord.Interrupt) },
		coord.MRunChanges: func() any { return new(node.ChangesParams) }, coord.MRunDiff: func() any { return new(node.DiffParams) },
		coord.MMachineShare: func() any { return new(task.Share) }, coord.MProjectCreate: func() any { return new(coord.ProjectCreate) },
		coord.MProjectMember: func() any { return new(task.MemberSet) }, coord.MProjectEdit: func() any { return new(task.ProjectEdit) },
		coord.MProjectDirs: func() any { return new(coord.ProjectDirsParams) },
	}
	results := map[string]func() any{
		coord.MTaskStatus: func() any { return new(task.Task) }, coord.MRunDispatch: func() any { return new(task.Run) },
		coord.MRunStop: func() any { return new(task.Run) }, coord.MRunAnswer: func() any { return new(task.Run) },
		coord.MRunContinue: func() any { return new(task.Run) }, coord.MRunOutputPage: func() any { return new(coord.OutputPage) },
		coord.MTaskCreate: func() any { return new(task.Task) }, coord.MTaskStart: func() any { return new(task.Task) },
		coord.MTaskMerge: func() any { return new(task.Task) }, coord.MTaskMove: func() any { return new(task.Task) },
		coord.MTaskPlanSave: func() any { return new(task.Task) }, coord.MTaskPlanApply: func() any { return new(task.Task) },
		coord.MTaskGate: func() any { return new(task.Task) }, coord.MTaskSourceAck: func() any { return new(task.Task) },
		coord.MRunPreview: func() any { return new(coord.Preview) }, coord.MAgentList: func() any { return new(coord.Agents) },
		coord.MTaskMessage: func() any { return new(coord.MessageResult) }, coord.MTaskMessagePreview: func() any { return new(coord.MessageRoute) },
		coord.MRunSend: func() any { return new(task.Run) }, coord.MRunInterrupt: func() any { return new(task.Run) },
		coord.MRunChanges: func() any { return new(node.Changes) }, coord.MRunDiff: func() any { return new(node.Diff) },
		coord.MMachineShare: func() any { return new(task.Share) }, coord.MProjectCreate: func() any { return new(task.Project) },
		coord.MProjectMember: func() any { return new(task.Project) }, coord.MProjectEdit: func() any { return new(task.Project) },
		coord.MProjectDirs: func() any { return new(coord.ProjectDirs) },
	}
	files, _ := filepath.Glob(filepath.Join("webtest", "frames", "*.jsonl"))
	seen := map[string]int{}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		asked := map[int64]string{}
		for i, l := range frameLines(t, name) {
			at := name + ".jsonl:" + strconv.Itoa(i+1)
			switch {
			case l.C != nil && l.C.Type == "req":
				asked[l.C.ID] = l.C.Method
				if mk := params[l.C.Method]; mk != nil {
					strictDecode(t, at, l.C.Params, mk())
					seen[l.C.Method]++
				}
			case l.S != nil && l.S.Type == "res" && l.S.Result != nil:
				if mk := results[asked[l.S.ID]]; mk != nil {
					strictDecode(t, at, l.S.Result, mk())
				}
			case l.S != nil && l.S.Type == "push" && l.S.Method == "machines":
				strictDecode(t, at, l.S.Params, new(struct {
					Items []coord.Machine `json:"items"`
				}))
				seen["machines"]++
			case l.S != nil && l.S.Type == "push" && l.S.Method == coord.PushAffordances:
				strictDecode(t, at, l.S.Params, new(coord.Affordances))
				seen[coord.PushAffordances]++
			case l.S != nil && l.S.Type == "push" && l.S.Method == "snapshot" && strings.Contains(string(l.S.Params), `"part":"affordances"`):
				strictDecode(t, at, l.S.Params, new(struct {
					Part  string            `json:"part"`
					Items coord.Affordances `json:"items"`
				}))
				seen["affordances part"]++
			case l.S != nil && l.S.Type == "push" && l.S.Method == "inbox":
				strictDecode(t, at, l.S.Params, new(struct {
					Items []coord.InboxItem `json:"items"`
				}))
				seen["inbox"]++
			}
		}
	}
	for _, m := range []string{coord.MTaskStatus, coord.MRunDispatch, coord.MRunStop, coord.MRunAnswer, coord.MRunOutputPage, coord.MTaskCreate,
		coord.MTaskStart, coord.MTaskMerge, coord.MTaskMove, coord.MTaskPlanSave, coord.MTaskPlanApply, coord.MTaskGate, coord.MTaskSourceAck,
		coord.MRunPreview, coord.MAgentList, coord.MTaskMessage, coord.MRunInterrupt, coord.MRunChanges, coord.MRunDiff, coord.MMachineShare, coord.MProjectCreate, coord.MProjectMember, coord.MProjectEdit, coord.MProjectDirs, "machines", "inbox", coord.PushAffordances, "affordances part"} {
		if seen[m] == 0 {
			t.Errorf("no frame file has %s", m)
		}
	}
}
