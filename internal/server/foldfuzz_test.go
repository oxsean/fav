package server

import (
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/defs"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// journalGen makes random envelopes over a few tasks, runs, projects and agents: mostly about things that exist, now
// and then about things that do not.
type journalGen struct {
	r                            *rand.Rand
	seq                          int64
	at                           time.Time
	tasks, runs, projects, names []string
}

var genFlows = []*task.Flow{
	{Name: "f1", MaxLoops: 1, Budget: &task.Budget{Minutes: 1}, Stages: []task.Stage{{Name: "build", Role: "implement", Check: true},
		{Name: "review", Output: task.OutputVerdict}, {Name: "accept", Gate: task.GateHuman}}},
	{Name: "f2", Stages: []task.Stage{{Name: "only"}}},
}

func (g *journalGen) n(k int) int       { return g.r.IntN(k) }
func (g *journalGen) chance(p int) bool { return g.r.IntN(100) < p }

func (g *journalGen) pick(pool []string, prefix string) string {
	if len(pool) == 0 || g.chance(4) {
		return prefix + "none" + strconv.Itoa(g.n(3))
	}
	return pool[g.n(len(pool))]
}

func (g *journalGen) some(pool []string, prefix string) []string {
	var out []string
	for range g.n(3) {
		out = append(out, g.pick(pool, prefix))
	}
	return out
}

func (g *journalGen) of(xs ...string) string { return xs[g.n(len(xs))] }

func (g *journalGen) when() *time.Time {
	t := g.at.Add(time.Duration(g.n(1e6)) * time.Microsecond).Add(time.Duration(g.n(1000)))
	return &t
}

func (g *journalGen) flow() *task.Flow {
	if g.chance(60) {
		return nil
	}
	return genFlows[g.n(len(genFlows))]
}

func (g *journalGen) user() string { return g.of("u_a", "u_b", "u_c", "") }

func (g *journalGen) workspace(t string) *agent.Workspace {
	if g.chance(60) {
		return nil
	}
	w := &agent.Workspace{Checkout: "/src", Branch: task.BranchOf(t), ReadOnly: g.chance(20)}
	if g.chance(40) {
		w.Chain = []string{task.BranchOf(g.pick(g.tasks, "t"))}
	}
	if g.chance(20) {
		w.Merge = task.BranchOf(g.pick(g.tasks, "t"))
	}
	return w
}

func (g *journalGen) event() journal.Event {
	ev := journal.NewEvent
	switch g.n(28) {
	case 0, 1:
		id := "t" + strconv.Itoa(len(g.tasks))
		if g.chance(5) && len(g.tasks) > 0 {
			id = g.pick(g.tasks, "t")
		} else {
			g.tasks = append(g.tasks, id)
		}
		t := task.Task{ID: id, Title: "x" + id, Brief: g.of("", "b"), Dir: g.of("", "/w", "/d"), Project: g.of("", g.pick(g.projects, "p")),
			Owner: g.user(), Status: g.of(task.StatusTodo, task.StatusTodo, task.StatusBacklog, task.StatusDone), Kind: g.of("", task.KindRequirement)}
		if g.chance(30) && len(g.tasks) > 1 {
			t.Parent = g.pick(g.tasks, "t")
		}
		if g.chance(20) {
			t.After = g.some(g.tasks, "t")
		}
		if f := g.flow(); f != nil {
			t.Workflow, t.Flow, t.Stage = f.Name, f, f.Stages[0].Name
		}
		if g.chance(20) {
			t.Source = &task.Source{Kind: "gitea", Number: int64(g.n(50)), Rev: 1, Digest: "a", Seen: "a", SeenRev: 1}
		}
		return ev(task.ETaskCreated, t)
	case 2:
		d := task.TaskEdit{ID: g.pick(g.tasks, "t")}
		if g.chance(50) {
			d.Title = ptr(g.of("y", "z"))
		}
		if g.chance(30) {
			d.Owner = ptr(g.user())
		}
		if g.chance(20) {
			d.Project = ptr(g.pick(g.projects, "p"))
		}
		if g.chance(20) {
			d.Tags = ptr(g.some([]string{"a", "b"}, "tag"))
		}
		if g.chance(20) {
			f := g.flow()
			name := ""
			if f != nil {
				name = f.Name
			}
			d.Workflow, d.Flow = &name, f
		}
		return ev(task.ETaskEdited, d)
	case 3:
		return ev(task.ETaskStatus, task.TaskStatus{ID: g.pick(g.tasks, "t"), Status: g.of(task.StatusTodo, task.StatusDone, task.StatusCanceled, task.StatusBacklog)})
	case 4, 5:
		id := "r" + strconv.Itoa(len(g.runs))
		g.runs = append(g.runs, id)
		t := g.pick(g.tasks, "t")
		r := task.Run{ID: id, Task: t, Machine: g.of("m1", "m2"), Agent: "fake", Profile: tend.AgentProfile{Name: "fake", Provider: g.of(agent.ProviderFake, tend.ProviderClaude, agent.ProviderCommand)},
			Dir: g.of("/w", "/d"), Stage: g.of("", "build", "review", task.StageMerge, task.StagePlan), Planner: g.chance(10), Judge: g.chance(10), Work: g.workspace(t)}
		if g.chance(40) && len(g.runs) > 1 {
			r.Parent = g.pick(g.runs, "r")
			if g.chance(50) {
				r.Takes = g.some([]string{"m1", "m2", "m3"}, "m")
			}
		}
		return ev(task.ERunQueued, r)
	case 6:
		return ev(task.ERunStarting, task.RunStarting{ID: g.pick(g.runs, "r"), Dir: g.of("", "/w2")})
	case 7, 8, 9, 10:
		o := task.Observation{ID: g.pick(g.runs, "r"), State: g.of(task.Starting, task.Running, task.Running, task.Unknown, task.Exited, task.Exited, task.Stopped, task.Failed),
			NodeRev: g.n(6), Attention: g.of("", "", task.AttentionAsked, task.AttentionPermission, task.AttentionStalled), Last: g.of("", "l"), Doing: g.of("", "go test"),
			Stream: g.chance(50), Reason: g.of("", "", "quota", task.WhyMergeConflict), Session: g.of("", "", "s1"), Provider: agent.ProviderFake, Turn: g.n(3)}
		if o.State == task.Exited {
			code := g.of("0", "0", "1")
			c, _ := strconv.Atoi(code)
			o.ExitCode = &c
		}
		if g.chance(30) {
			o.Requests = []agent.Request{{ID: g.of("q1", "q2"), Kind: g.of(agent.RequestPermission, agent.RequestQuestion), AllowRun: g.chance(50), Failed: g.chance(20)}}
		}
		if g.chance(30) {
			o.Sends = []agent.Send{{ID: g.of("m1", "m2"), Text: "hi", State: g.of(agent.SendSent, agent.SendSeen)}}
		}
		if g.chance(30) {
			o.Caps = &agent.RunCaps{Steer: g.chance(50), After: g.chance(50), Interrupt: g.chance(50), Continue: g.chance(50)}
		}
		if g.chance(20) {
			o.Verdict = &agent.Verdict{Verdict: g.of(agent.VerdictPass, agent.VerdictRework, agent.VerdictBlocked), At: *g.when()}
		}
		if g.chance(15) {
			o.Check = &agent.CheckResult{Argv: []string{"gate"}, Exit: g.n(2)}
		}
		if g.chance(20) {
			o.Work = &agent.Work{Head: g.of("h1", "h2"), Merged: g.chance(30)}
		}
		if g.chance(10) {
			o.Plan = &task.Plan{Tasks: []task.PlanTask{{Key: "a", Title: "A"}}}
		}
		if g.chance(50) {
			o.StartedAt = g.when()
		}
		if g.chance(40) {
			o.EndedAt = g.when()
		}
		if g.chance(20) {
			o.Usage = &agent.Usage{CostUSD: float64(g.n(3)), Turns: 1}
		}
		return ev(task.ERunObserved, o)
	case 11:
		return ev(task.ERunStopAsked, task.RunRef{ID: g.pick(g.runs, "r")})
	case 12:
		return ev(task.ERunCanceled, task.RunRef{ID: g.pick(g.runs, "r"), Reason: g.of("", "access_revoked")})
	case 13:
		return ev(task.ERunAbandoned, task.RunRef{ID: g.pick(g.runs, "r")})
	case 14:
		return ev(task.ERunAnswered, task.RunAnswer{ID: g.pick(g.runs, "r"), Answer: agent.Answer{Request: g.of("q1", "q2"), Allow: g.chance(50), Decision: g.of("", agent.DecisionAllowRun), By: g.user()}})
	case 15:
		return ev(task.ERunSent, task.RunSend{ID: g.pick(g.runs, "r"), Send: agent.Send{ID: g.of("m1", "m2", "m3"), Text: "t",
			State: g.of(agent.SendQueued, agent.SendQueued, agent.SendFailed), Mode: g.of("", agent.SendAfter, agent.SendInterrupt), By: g.user()}})
	case 16:
		d := task.TaskMove{ID: g.pick(g.tasks, "t")}
		if g.chance(50) {
			d.Parent = ptr(g.of("", g.pick(g.tasks, "t")))
		}
		if g.chance(50) {
			d.After = ptr(g.some(g.tasks, "t"))
		}
		return ev(task.ETaskMoved, d)
	case 17:
		return ev(task.ETaskStarted, task.TaskStart{IDs: g.some(g.tasks, "t")})
	case 18:
		return ev(task.ETaskHeld, task.TaskHold{ID: g.pick(g.tasks, "t"), Reason: "no_agent", Detail: g.of("", "d")})
	case 19:
		if g.chance(50) {
			return ev(task.ETaskSourced, task.SourceUpdate{ID: g.pick(g.tasks, "t"), Digest: g.of("a", "b", "c"), Title: "n", Text: "v", Closed: g.chance(30)})
		}
		return ev(task.ETaskSourceAcked, task.SourceAck{ID: g.pick(g.tasks, "t"), Accept: g.chance(50)})
	case 20:
		return ev(task.ETaskLinked, task.Linked{ID: g.pick(g.tasks, "t"), Issue: g.of("", "i"), PR: g.of("", "p")})
	case 21:
		if g.chance(50) {
			return ev(task.ETaskNoted, task.TaskNote{ID: g.pick(g.tasks, "t"), Note: task.Note{Kind: task.NoteMessage, Text: "n"}})
		}
		return ev(task.ETaskStaged, task.TaskStage{ID: g.pick(g.tasks, "t"), Stage: g.of("build", "review", "accept", "only", "nope"), Loops: g.n(3), Back: g.chance(30)})
	case 22:
		if g.chance(70) {
			var p *task.Plan
			if g.chance(80) {
				p = &task.Plan{Tasks: []task.PlanTask{{Key: "a", Title: "A"}}}
			}
			return ev(task.EPlanDrafted, task.PlanDraft{ID: g.pick(g.tasks, "t"), Plan: p, By: g.user()})
		}
		return ev(task.EPlanApplied, task.PlanApplied{ID: g.pick(g.tasks, "t")})
	case 23:
		id := "p" + strconv.Itoa(len(g.projects))
		g.projects = append(g.projects, id)
		return ev(task.EProjectCreated, task.Project{ID: id, Name: id, Owner: g.user()})
	case 24:
		if g.chance(50) {
			return ev(task.EProjectEdited, task.ProjectEdit{ID: g.pick(g.projects, "p"), Name: ptr("n"), Context: ptr(g.of("", "c"))})
		}
		return ev(task.EMemberSet, task.MemberSet{Project: g.pick(g.projects, "p"), User: g.of("u_a", "u_b"), Role: g.of("", task.RoleParticipant, task.RoleReader)})
	case 26:
		return ev(task.ERunInterrupt, task.RunInterrupt{ID: g.pick(g.runs, "r"), Turn: 1 + g.n(2), Ask: g.of("int_1", "int_2"), By: g.user()})
	case 25:
		return ev(task.EMachineShared, task.Share{Machine: g.of("m1", "m2"), Users: g.some([]string{"u_a", "u_b"}, "u"), Projects: g.some(g.projects, "p")})
	default:
		switch g.n(3) {
		case 0:
			name := "d" + strconv.Itoa(g.n(3))
			g.names = append(g.names, name)
			return ev(task.EAgentDefSaved, task.AgentDef{AgentDef: defs.AgentDef{Name: name, Provider: "claude"}, Owner: g.user()})
		case 1:
			return ev(task.EAgentDefShared, task.AgentDefShare{Name: g.pick(g.names, "d"), Share: task.DefShare{All: g.chance(50)}})
		}
		return ev(task.EAgentDefRemoved, task.AgentDefRef{Name: g.pick(g.names, "d")})
	}
}

func (g *journalGen) envelope() journal.Envelope {
	g.seq++
	g.at = g.at.Add(time.Second + time.Duration(g.n(1000)))
	var events []journal.Event
	for range 1 + g.n(3) {
		events = append(events, g.event())
	}
	return journal.Envelope{V: journal.Version, Seq: g.seq, At: g.at, Events: events}
}

// journal is n envelopes the coordinator could have written (each applies to the state before it), and for some
// seeds one more that does not.
func (g *journalGen) journal(n int) []journal.Envelope {
	st := task.New()
	var envs []journal.Envelope
	for tries := 0; len(envs) < n && tries < 20*n; tries++ {
		seq, at, pools := g.seq, g.at, [4]int{len(g.tasks), len(g.runs), len(g.projects), len(g.names)}
		env := g.envelope()
		b, _ := json.Marshal(st)
		cp := task.New()
		json.Unmarshal(b, cp)
		if cp.Apply(env) != nil || twoOpen(cp) {
			g.seq, g.at = seq, at // as if it never was
			g.tasks, g.runs, g.projects, g.names = g.tasks[:pools[0]], g.runs[:pools[1]], g.projects[:pools[2]], g.names[:pools[3]]
			continue
		}
		st, envs = cp, append(envs, env)
	}
	if g.chance(30) {
		b, _ := json.Marshal(st)
		for range 50 {
			cp := task.New()
			json.Unmarshal(b, cp)
			if env := g.envelope(); cp.Apply(env) != nil {
				return append(envs, env)
			}
		}
	}
	return envs
}

// twoOpen: a task of st has two open runs, which the coordinator never queues.
func twoOpen(st *task.State) bool {
	open := map[string]bool{}
	for _, r := range st.Runs {
		if task.Open(r.State) {
			if open[r.Task] {
				return true
			}
			open[r.Task] = true
		}
	}
	return false
}

// diffs are the paths where a and b differ, at most max of them.
func diffs(path string, a, b any, out *[]string, max int) {
	if len(*out) >= max || reflect.DeepEqual(a, b) {
		return
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		for _, k := range slices.Sorted(maps.Keys(keys)) {
			diffs(path+"."+k, am[k], bm[k], out, max)
		}
		return
	}
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	*out = append(*out, fmt.Sprintf("%s: Go %s, page %s", path, ab, bb))
}

// folded is how one journal came out: the state after the last envelope that applied, where the first one that did
// not was (-1: none), how each task stands and each run's conversation.
type folded struct {
	State  any                       `json:"state"`
	FailAt int                       `json:"fail_at"`
	Sits   map[string]task.Situation `json:"sits"`
	Convs  map[string][]string       `json:"convs"`
	err    error
}

func goFold(envs []journal.Envelope) folded {
	st := task.New()
	out := folded{FailAt: -1, Sits: map[string]task.Situation{}, Convs: map[string][]string{}}
	for i, env := range envs {
		b, _ := json.Marshal(st)
		cp := task.New()
		json.Unmarshal(b, cp)
		if out.err = cp.Apply(env); out.err != nil {
			out.FailAt = i
			break
		}
		st = cp
	}
	for id, t := range st.Tasks {
		out.Sits[id] = st.Situation(t)
	}
	for id := range st.Runs {
		for _, r := range st.Conversation(id) {
			out.Convs[id] = append(out.Convs[id], r.ID)
		}
	}
	b, _ := json.Marshal(st)
	json.Unmarshal(b, &out.State)
	return out
}

// TestThePageFoldsRandomJournalsAsTheCoordinatorDoes: on random journals, fold.js refuses the same envelope Go does,
// and up to it comes to the same state, the same situations and the same conversations. TEND_FOLD_SEEDS runs more.
func TestThePageFoldsRandomJournalsAsTheCoordinatorDoes(t *testing.T) {
	nodeBin := nodeJS(t)
	seeds := 300
	if n, err := strconv.Atoi(os.Getenv("TEND_FOLD_SEEDS")); err == nil {
		seeds = n
	}
	var journals [][]journal.Envelope
	for seed := range seeds {
		g := &journalGen{r: rand.New(rand.NewPCG(uint64(seed), 7)), at: time.Date(2026, 9, 30, 10, 0, 0, 123456789, time.UTC)}
		journals = append(journals, g.journal(20+g.n(60)))
	}
	dir := t.TempDir()
	b, _ := json.Marshal(journals)
	os.WriteFile(filepath.Join(dir, "journals.json"), b, 0o600)
	body := `const out=[];for(const envs of JSON.parse(fs.readFileSync(process.argv[2],'utf8'))){
let s={seq:0,tasks:{},runs:{},projects:{},shares:{},agent_defs:{}},fail_at=-1;
for(let i=0;i<envs.length;i++){const c=structuredClone(s);try{Fold.apply(c,envs[i]);s=c}catch(e){fail_at=i;break}}
const sits={},convs={};for(const t of Object.values(s.tasks))sits[t.id]=Fold.situation(s,t);
for(const id of Object.keys(s.runs))convs[id]=Fold.conversation(s,id).map(r=>r.id);
out.push({state:s,fail_at,sits,convs});}
fs.writeFileSync(process.argv[3],JSON.stringify(out));`
	// the page's classic script and the new UI's module fold the same way until the switch removes the first
	scripts := map[string][]string{
		"web/fold.js": {"-e", `const fs=require('fs'),vm=require('vm');vm.runInThisContext(fs.readFileSync(process.argv[1],'utf8'));` + body},
		"web/core/fold.js": {"--input-type=module", "-e", `import fs from 'node:fs';import {pathToFileURL} from 'node:url';
const Fold=await import(pathToFileURL(process.argv[1]));` + body},
	}
	for file, script := range scripts {
		t.Run(file, func(t *testing.T) {
			fold, _ := filepath.Abs(filepath.FromSlash(file))
			res := filepath.Join(dir, "out.json")
			if out, err := exec.Command(nodeBin, append(script, fold, filepath.Join(dir, "journals.json"), res)...).CombinedOutput(); err != nil {
				t.Fatalf("node: %v\n%s", err, out)
			}
			var pages []folded
			raw, _ := os.ReadFile(res)
			if err := json.Unmarshal(raw, &pages); err != nil || len(pages) != len(journals) {
				t.Fatalf("%d results: %v", len(pages), err)
			}
			bad := 0
			for i, envs := range journals {
				g, p := goFold(envs), pages[i]
				why := ""
				switch {
				case g.FailAt != p.FailAt:
					why = fmt.Sprintf("Go refuses envelope %d (%v), the page %d", g.FailAt, g.err, p.FailAt)
				case !reflect.DeepEqual(g.Sits, p.Sits):
					why = fmt.Sprintf("situations:\n%v\n%v", g.Sits, p.Sits)
				case !reflect.DeepEqual(normalized(anyOf(g.Convs)), normalized(anyOf(p.Convs))):
					why = fmt.Sprintf("conversations:\n%v\n%v", g.Convs, p.Convs)
				case !reflect.DeepEqual(normalized(g.State), normalized(p.State)):
					var ds []string
					diffs("", normalized(g.State), normalized(p.State), &ds, 5)
					why = "state: " + strings.Join(ds, "; ")
				}
				if why != "" {
					if bad++; bad <= 8 {
						at := len(envs)
						if g.FailAt >= 0 {
							at = g.FailAt + 1
						}
						eb, _ := json.Marshal(envs[:at])
						if len(eb) > 1500 && os.Getenv("TEND_FOLD_FULL") == "" {
							eb = append(eb[:1500], "…"...)
						}
						t.Errorf("seed %d: %s\nenvelopes: %s", i, why, eb)
					}
				}
			}
			if bad > 0 {
				t.Fatalf("%d of %d journals differ", bad, len(journals))
			}
			if envs, refused := 0, 0; true {
				for i, j := range journals {
					envs += len(j)
					if pages[i].FailAt >= 0 {
						refused++
					}
				}
				if envs < 20*len(journals) || refused < len(journals)/10 {
					t.Fatalf("%d envelopes in %d journals, %d refused: the journals test too little", envs, len(journals), refused)
				}
			}
		})
	}
}

func anyOf(v any) any {
	b, _ := json.Marshal(v)
	var out any
	json.Unmarshal(b, &out)
	return out
}
