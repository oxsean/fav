package remote

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

type ranked struct {
	id    string
	whole bool
}

func TestInterleaveTakesEveryMachinesBestBeforeAnyonesSecond(t *testing.T) {
	lists := [][]ranked{
		{{"a1", true}, {"a2", false}, {"a3", true}},
		nil, // a machine that did not answer
		{{"b1", false}, {"b2", true}},
		{{"c1", true}},
	}
	ids := func(hs []ranked) []string {
		var out []string
		for _, h := range hs {
			out = append(out, h.id)
		}
		return out
	}
	whole := func(h ranked) bool { return h.whole }
	if got, want := ids(Interleave(lists, whole, 0)), []string{"a1", "b2", "c1", "a3", "a2", "b1"}; !slices.Equal(got, want) {
		t.Fatalf("whole matches first, each tier by rank across machines: %v, want %v", got, want)
	}
	if got, want := ids(Interleave(lists, whole, 4)), []string{"a1", "b2", "c1", "a3"}; !slices.Equal(got, want) {
		t.Fatalf("the limit cuts the merged list: %v, want %v", got, want)
	}
	if got := Interleave([][]ranked{{{"x", false}, {"y", true}}}, whole, 0); !slices.Equal(ids(got), []string{"y", "x"}) {
		t.Fatalf("one machine keeps its ranks within each tier: %v", ids(got))
	}
	if got := Interleave[ranked](nil, whole, 3); got == nil || len(got) != 0 {
		t.Fatalf("no machine answers an empty list, not nil: %#v", got)
	}
}

// grepNode answers grep with hits named after its machine; methods lists what its hello says.
func grepNode(name string, methods []string, sent *int, block bool, building *Progress) Handler {
	return handlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case MHello:
			return Hello{Proto: wire.Proto, Version: "v-" + name, Methods: methods}, nil
		case MGrep:
			*sent++
			if block {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			var p GrepParams
			json.Unmarshal(params, &p)
			return GrepResult{Hits: []GrepHit{{Row: Row{Session: Session{Provider: tend.ProviderClaude, SessionID: name + "-" + p.Q}},
				Hits: 2, AllInOne: true}}, Building: building, Fixes: []string{"fix-" + name}}, nil
		case MHits:
			*sent++
			return HitsResult{Hits: []Hit{{Off: 7, Role: "user", Text: name}}, Total: 1}, nil
		}
		return nil, &wire.Error{Code: wire.CodeUnknownMethod, Detail: method}
	})
}

func TestGrepAllAnswersEveryMachineInItsOrder(t *testing.T) {
	sent := map[string]*int{}
	all := []string{MHello, MGrep, MHits}
	nodes := map[string]Handler{}
	for name, h := range map[string]struct {
		methods  []string
		block    bool
		building *Progress
	}{
		"ok": {methods: all}, "old": {methods: []string{MHello, MList}}, "slow": {methods: all, block: true},
		"building": {methods: all, building: &Progress{Done: 3, Total: 10}},
	} {
		sent[name] = new(int)
		nodes[name] = grepNode(name, h.methods, sent[name], h.block, h.building)
	}
	names := []string{"ok", "old", "off", "slow", "building"}
	var hosts []tend.Host
	for _, n := range names {
		hosts = append(hosts, tend.Host{Name: n})
	}
	h := NewHostsDial(hosts, "", func(host tend.Host) (*Client, error) {
		if nodes[host.Name] == nil {
			return nil, &wire.Error{Code: wire.CodeOffline}
		}
		return Pipe(nodes[host.Name]), nil
	})
	t.Cleanup(h.Close)

	got := h.GrepAll(context.Background(), names, func(string) GrepParams { return GrepParams{Q: "kw", Limit: 5} }, 300*time.Millisecond)
	if len(got) != len(names) {
		t.Fatalf("one answer per machine: %d", len(got))
	}
	for i, g := range got {
		if g.Machine != names[i] {
			t.Fatalf("answer %d is %s, want %s", i, g.Machine, names[i])
		}
	}
	if g := got[0]; g.Err != nil || len(g.Result.Hits) != 1 || g.Result.Hits[0].Row.SessionID != "ok-kw" {
		t.Fatalf("ok: %+v", g)
	}
	if g := got[1]; wire.Code(g.Err) != wire.CodeUnknownMethod || g.Err.(*wire.Error).Detail != "v-old" || *sent["old"] != 0 {
		t.Fatalf("a tend without grep is not sent it: %v, %d sent", g.Err, *sent["old"])
	}
	if g := got[2]; wire.Code(g.Err) != wire.CodeOffline {
		t.Fatalf("off: %v", g.Err)
	}
	if g := got[3]; wire.Code(g.Err) != wire.CodeTimeout {
		t.Fatalf("a machine still searching after the wait is timed out: %v", g.Err)
	}
	if g := got[4]; g.Err != nil || g.Result.Building == nil || g.Result.Building.Done != 3 || len(g.Result.Hits) != 1 {
		t.Fatalf("a machine still building answers what it found and how far: %+v", g)
	}

	if res, err := h.Hits(context.Background(), "ok", HitsParams{Ref: Ref{tend.ProviderClaude, "ok-kw"}, Q: "kw"}); err != nil || res.Total != 1 {
		t.Fatalf("hits: %+v %v", res, err)
	}
	before := *sent["old"]
	if _, err := h.Hits(context.Background(), "old", HitsParams{Ref: Ref{tend.ProviderClaude, "x"}, Q: "kw"}); wire.Code(err) != wire.CodeUnknownMethod || *sent["old"] != before {
		t.Fatalf("a tend without hits is not sent it: %v", err)
	}
}

func TestProjectDirsOnAreTheProjectsWithADirectoryThere(t *testing.T) {
	ps := map[string]*task.Project{
		"p2": {ID: "p2", Name: "web", Repos: []task.Repo{{Dirs: map[string]string{"mba": "/w", "win": `C:\w`}}, {Dirs: map[string]string{"mba": "/w2"}}}},
		"p1": {ID: "p1", Name: "api", Repos: []task.Repo{{Dirs: map[string]string{"mba": "/a"}}}},
		"p3": {ID: "p3", Name: "elsewhere", Repos: []task.Repo{{Dirs: map[string]string{"win": `C:\e`}}}},
	}
	want := []ProjectDirs{{ID: "p1", Name: "api", Dirs: []string{"/a"}}, {ID: "p2", Name: "web", Dirs: []string{"/w", "/w2"}}}
	if got := ProjectDirsOn(ps, "mba"); !reflect.DeepEqual(got, want) {
		t.Fatalf("by id, only those with a directory there: %+v", got)
	}
	if got := ProjectDirsOn(ps, "nowhere"); got != nil {
		t.Fatalf("none there: %+v", got)
	}
}
