package tui

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// grepHost is a fakeHost that also answers grep and hits (when its hello lists them).
type grepHost struct {
	*fakeHost
	mu    sync.Mutex
	grep  func(p remote.GrepParams) remote.GrepResult
	hits  func(p remote.HitsParams) remote.HitsResult
	asked []remote.GrepParams
	hitQ  []remote.HitsParams
}

func newGrepHost(grep func(p remote.GrepParams) remote.GrepResult) *grepHost {
	f := newFakeHost()
	f.methods = append(f.methods, remote.MGrep, remote.MHits)
	return &grepHost{fakeHost: f, grep: grep}
}

func (g *grepHost) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch method {
	case remote.MGrep:
		var p remote.GrepParams
		json.Unmarshal(params, &p)
		g.asked = append(g.asked, p)
		return g.grep(p), nil
	case remote.MHits:
		var p remote.HitsParams
		json.Unmarshal(params, &p)
		g.hitQ = append(g.hitQ, p)
		if g.hits == nil {
			return remote.HitsResult{Hits: []remote.Hit{}}, nil
		}
		return g.hits(p), nil
	}
	return g.fakeHost.Handle(ctx, method, params)
}

func (g *grepHost) askedN() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.asked)
}

// farTransports reach the named machines as the TUI does in mode 1 (a tend rpc per host over ssh) and in mode 2
// (node.call through a coordinator); a name without a handler cannot be reached.
var farTransports = []struct {
	name  string
	hosts func(t *testing.T, names []string, nodes map[string]remote.Handler) *remote.Hosts
}{
	{"ssh", func(t *testing.T, names []string, nodes map[string]remote.Handler) *remote.Hosts {
		var hs []tend.Host
		for _, n := range names {
			hs = append(hs, tend.Host{Name: n})
		}
		h := remote.NewHostsDial(hs, i18n.ZH, func(host tend.Host) (*remote.Client, error) {
			if nodes[host.Name] == nil {
				return nil, &wire.Error{Code: wire.CodeOffline}
			}
			return remote.Pipe(nodes[host.Name]), nil
		})
		t.Cleanup(h.Close)
		return h
	}},
	{"node.call", func(t *testing.T, names []string, nodes map[string]remote.Handler) *remote.Hosts {
		nc := remote.NewNodeCall("https://tend.example", func(ctx context.Context, machine, method string, params json.RawMessage, out any) error {
			if nodes[machine] == nil {
				return &wire.Error{Code: wire.CodeOffline}
			}
			a, err := nodes[machine].Handle(ctx, method, params)
			if err != nil {
				return err
			}
			b, _ := json.Marshal(a)
			return json.Unmarshal(b, out)
		})
		var ms []remote.Machine
		for _, n := range names {
			ms = append(ms, remote.Machine{Name: n, Mine: true})
		}
		nc.SetMachines(ms)
		h := remote.NewHostsOver(nc)
		t.Cleanup(h.Close)
		return h
	}},
}

// farModel is msgModel's three sessions here plus the named machines, each list fetched once.
func farModel(t *testing.T, query string, hosts *remote.Hosts) *Model {
	t.Helper()
	m, _ := msgModel(t, query)
	m.useHosts(hosts)
	for _, name := range hosts.Names() {
		pump(m, m.fetchHost(name))
	}
	return m
}

// searchEverywhere runs this machine's search, then leaves the box, which asks the other machines at once.
func searchEverywhere(t *testing.T, m *Model) {
	t.Helper()
	m.typing = true
	search(t, m)
	m.typing = false
	m.search.Blur()
	pump(m, m.issueMsgSearch())
}

func farHit(sid, title, snippet string, whole bool) remote.GrepHit {
	return remote.GrepHit{Row: remote.Row{Session: remote.Session{Provider: tend.ProviderClaude, SessionID: sid, Title: title,
		LastAt: time.Now(), UpdatedAt: time.Now(), Turns: 5}}, Hits: 3, AllInOne: whole, Snippet: snippet, Off: 1500, File: "file-" + sid,
		Latest: time.Now()}
}

func titles(m *Model) []string {
	var out []string
	for _, r := range m.rows {
		if r.rec != nil {
			out = append(out, r.rec.Host+"/"+r.rec.SessionID)
		}
	}
	return out
}

func TestOtherMachinesHitsJoinTheMessageSearch(t *testing.T) {
	for _, tr := range farTransports {
		t.Run(tr.name, func(t *testing.T) {
			g := newGrepHost(func(p remote.GrepParams) remote.GrepResult {
				return remote.GrepResult{Hits: []remote.GrepHit{farHit("r-b", "remote websocket fix", "远端也要滚轮加速", true),
					farHit("r-new", "只在搜索里出现", "远端滚轮", false)}, Fixes: []string{"wheels"}}
			})
			m := farModel(t, "> 滚轮 加速 host:all", tr.hosts(t, []string{"mba"}, map[string]remote.Handler{"mba": g}))
			searchEverywhere(t, m)

			want := []string{"/both", "mba/r-b", "/wheel", "mba/r-new"}
			if got := titles(m); !slices.Equal(got, want) {
				t.Fatalf("merged as sessions.grep merges, whole matches first and each machine's best before anyone's second: %v, want %v", got, want)
			}
			if r := m.rows[1].rec; r != m.remote["mba"].byKey[r.Key()] {
				t.Fatal("a hit on a listed session is that row")
			}
			if len(g.asked) != 1 {
				t.Fatalf("one grep: %d", len(g.asked))
			}
			p := g.asked[0]
			if p.Q != "滚轮 加速 host:all" || p.All != (m.view != viewFavorites) || p.Limit != remote.GrepLimit || p.BudgetMS != int(remote.GrepBudget/time.Millisecond) {
				t.Fatalf("grep params: %+v", p)
			}
			screen := screenText(m)
			if !strings.Contains(screen, "远端也要滚轮加速") || !strings.Contains(screen, "wheels") {
				t.Fatalf("the remote card shows its hit and the title its spelling fix:\n%s", screen)
			}
			if strings.Contains(m.msgTitle(), "mba") {
				t.Fatalf("a machine searched in full is not named: %q", m.msgTitle())
			}
		})
	}
}

func TestMachinesNotSearchedInFullAreNamed(t *testing.T) {
	for _, tr := range farTransports {
		t.Run(tr.name, func(t *testing.T) {
			building := newGrepHost(func(remote.GrepParams) remote.GrepResult {
				return remote.GrepResult{Hits: []remote.GrepHit{farHit("r-b", "remote websocket fix", "远端也要滚轮加速", true)},
					Building: &remote.Progress{Done: 3, Total: 10}}
			})
			old := newGrepHost(func(remote.GrepParams) remote.GrepResult {
				t.Error("a tend without grep is asked")
				return remote.GrepResult{}
			})
			old.methods = []string{remote.MHello, remote.MList, remote.MLive, remote.MMessages}
			nodes := map[string]remote.Handler{"mba": building, "old": old}
			m := farModel(t, "> 滚轮 加速 host:all", tr.hosts(t, []string{"mba", "old", "win"}, nodes))
			if m.remote["win"].err == nil {
				t.Fatal("win's list could not be fetched")
			}
			searchEverywhere(t, m)

			title := m.msgTitle()
			for _, part := range []string{
				i18n.F("remote.down", "mba", i18n.F("msg.far_building", 3, 10)),
				i18n.F("remote.down", "old", i18n.T("msg.far_old")),
				i18n.F("remote.down", "win", remote.Reason(&wire.Error{Code: wire.CodeOffline})),
			} {
				if !strings.Contains(title, part) {
					t.Fatalf("title names %q: %q", part, title)
				}
			}
			if got := titles(m); !slices.Equal(got, []string{"/both", "mba/r-b", "/wheel"}) {
				t.Fatalf("this machine's results and what the building machine found: %v", got)
			}

			asked := building.askedN()
			m.typing = true // back into the box and out again: those that did not answer in full are asked again
			m.issueMsgSearch()
			m.typing = false
			pump(m, m.issueMsgSearch())
			if building.askedN() != asked+1 {
				t.Fatalf("the building machine is asked again: %d", building.askedN())
			}
		})
	}
}

func TestTypingAsksOtherMachinesOnlyAfterAPause(t *testing.T) {
	g := newGrepHost(func(remote.GrepParams) remote.GrepResult { return remote.GrepResult{Hits: []remote.GrepHit{}} })
	m := farModel(t, "> 滚轮 host:all", farTransports[0].hosts(t, []string{"mba"}, map[string]remote.Handler{"mba": g}))
	m.typing = true
	m.search.SetValue("> 滚 host:all")
	m.issueMsgSearch()
	early := m.msg.far.seq
	m.search.SetValue("> 滚轮 加速 host:all")
	asked := g.askedN()
	m.issueMsgSearch()
	if m.msg.far.key != "" || g.askedN() != asked {
		t.Fatal("nothing is sent while typing")
	}
	if farTickMsg(early).apply(m) != nil {
		t.Fatal("a pause in an earlier query sends nothing")
	}
	pump(m, farTickMsg(m.msg.far.seq).apply(m))
	if g.askedN() != asked+1 || g.asked[asked].Q != "滚轮 加速 host:all" {
		t.Fatalf("the pause sends the query typed: %+v", g.asked)
	}
	if strings.Contains(m.msgTitle(), "mba") {
		t.Fatalf("answered: %q", m.msgTitle())
	}
	m.typing = false
	if cmd := m.issueMsgSearch(); cmd != nil {
		pump(m, cmd)
	}
	if g.askedN() != asked+1 {
		t.Fatal("leaving the box does not ask a machine that answered in full again")
	}
}

func TestAnEarlierQuerysAnswerIsDropped(t *testing.T) {
	g := newGrepHost(func(p remote.GrepParams) remote.GrepResult {
		if strings.HasPrefix(p.Q, "分页") {
			return remote.GrepResult{Hits: []remote.GrepHit{farHit("r-a", "远端分页排障", "远端分页", true)}}
		}
		return remote.GrepResult{Hits: []remote.GrepHit{farHit("r-b", "remote websocket fix", "远端也要滚轮加速", true)}}
	})
	m := farModel(t, "> 滚轮 加速 host:all", farTransports[0].hosts(t, []string{"mba"}, map[string]remote.Handler{"mba": g}))
	searchEverywhere(t, m)
	if !slices.Contains(titles(m), "mba/r-b") {
		t.Fatalf("first query: %v", titles(m))
	}

	m.typing = false
	m.search.SetValue("> 分页 host:all")
	m.refresh()
	sent := m.issueMsgSearch() // a new query: sent at once, the box being left
	if slices.Contains(titles(m), "mba/r-b") {
		t.Fatalf("the last query's remote hits go with it: %v", titles(m))
	}
	m.search.SetValue("> 滚轮 host:all")
	m.refresh()
	m.issueMsgSearch()
	pump(m, sent) // the answer to 分页 arrives after the query moved on
	if slices.Contains(titles(m), "mba/r-a") || m.msg.far.answers["mba"] == nil || !m.msg.far.answers["mba"].asking {
		t.Fatalf("an answer to an earlier query is dropped: %v", titles(m))
	}
}

func TestARemoteHitOpensWithItsMatchedMessages(t *testing.T) {
	for _, tr := range farTransports {
		t.Run(tr.name, func(t *testing.T) {
			g := newGrepHost(func(remote.GrepParams) remote.GrepResult {
				return remote.GrepResult{Hits: []remote.GrepHit{farHit("r-b", "remote websocket fix", "远端也要滚轮加速", true)}}
			})
			g.hits = func(p remote.HitsParams) remote.HitsResult {
				return remote.HitsResult{Total: 2, Hits: []remote.Hit{
					{Off: 1500, Role: "user", At: time.Now(), Text: "远端也要滚轮加速", File: "file-r-b"},
					{Off: 300, Role: "assistant", At: time.Now().Add(-time.Hour), Text: "滚轮加速改好了", File: "file-r-b"},
				}}
			}
			m := farModel(t, "> 滚轮 加速 host:all", tr.hosts(t, []string{"mba"}, map[string]remote.Handler{"mba": g}))
			searchEverywhere(t, m)
			r := cursorOn(t, m, "r-b")
			cmd := m.openHits(m.msgKeywords())
			if cmd == nil {
				t.Fatal("→ on a remote hit asks its machine")
			}
			m.Update(cmd())
			if !m.hitsOpen() || len(m.msg.hl.items) != 2 || m.msg.hl.items[0].Role != 'u' || m.msg.hl.rec != r {
				t.Fatalf("the hit list holds the remote hits: %+v", m.msg.hl.items)
			}
			if len(g.hitQ) != 1 || g.hitQ[0].SessionID != "r-b" || g.hitQ[0].Q != "滚轮 加速" || g.hitQ[0].Limit != hitLimit {
				t.Fatalf("hits params: %+v", g.hitQ)
			}
			if !strings.Contains(ansi.Strip(m.hitTitle()), ansi.Strip(i18n.F("hits.title", 2))) {
				t.Fatalf("title: %q", m.hitTitle())
			}
			if !strings.Contains(screenText(m), "滚轮加速改好了") {
				t.Fatalf("the hits are listed:\n%s", screenText(m))
			}
		})
	}
}

func TestAnOldMachinesHitStepsThroughThePane(t *testing.T) {
	g := newGrepHost(func(remote.GrepParams) remote.GrepResult {
		return remote.GrepResult{Hits: []remote.GrepHit{farHit("r-b", "remote websocket fix", "远端也要滚轮加速", true)}}
	})
	g.methods = slices.DeleteFunc(g.methods, func(s string) bool { return s == remote.MHits })
	m := farModel(t, "> 滚轮 加速 host:all", farTransports[0].hosts(t, []string{"mba"}, map[string]remote.Handler{"mba": g}))
	searchEverywhere(t, m)
	cursorOn(t, m, "r-b")
	m.Update(m.openHits(m.msgKeywords())())
	if m.hitsOpen() || len(g.hitQ) != 0 {
		t.Fatalf("a tend without hits is not asked and no list opens: %d asked", len(g.hitQ))
	}
}

func TestServerDownSearchesOnlyThisMachine(t *testing.T) {
	g := newGrepHost(func(remote.GrepParams) remote.GrepResult {
		t.Error("asked through a server that is down")
		return remote.GrepResult{}
	})
	h := farTransports[1].hosts(t, []string{"mba"}, map[string]remote.Handler{"mba": g})
	m, _ := msgModel(t, "> 滚轮 加速 host:all")
	m.cfg.Coordinator = &tend.CoordinatorConfig{URL: "https://tend.example"}
	m.useHosts(h)
	m.tasks.lost = &wire.Error{Code: wire.CodeOffline}
	searchEverywhere(t, m)
	if got := titles(m); !slices.Equal(got, []string{"/both", "/wheel"}) {
		t.Fatalf("this machine's results: %v", got)
	}
	if want := i18n.F("remote.down", "mba", remote.Reason(m.tasks.lost)); !strings.Contains(m.msgTitle(), want) {
		t.Fatalf("title names mba as not searched: %q", m.msgTitle())
	}
}

// TestServedMessageSearchReachesTheNodes: mode 2 end to end, the viewer's own mba and Bob's shared bobs answer grep
// through the coordinator's node.call, and a hit opens with its matched messages.
func TestServedMessageSearchReachesTheNodes(t *testing.T) {
	s := newServedRig(t, nil)
	m := s.m
	s.start(t)
	waitFor(t, m, func() bool { return slices.Equal(names(m), []string{"bobs", "mba"}) })
	m.search.SetValue("host:all")
	m.refresh()
	waitFor(t, m, func() bool { m.refresh(); return len(rowsOn(m, "mba")) > 0 && len(rowsOn(m, "bobs")) > 0 })

	m.typing = false
	m.search.SetValue("> 回调 state host:all")
	m.refresh()
	pump(m, m.issueMsgSearch())
	oauth := s.d.Get("oauth").ID
	waitFor(t, m, func() bool {
		return slices.Contains(titles(m), "mba/"+oauth) && slices.Contains(titles(m), "bobs/"+oauth)
	})
	for i, row := range m.rows {
		if row.rec != nil && row.rec.Host == "bobs" && row.rec.SessionID == oauth {
			m.cursor = i
		}
	}
	m.Update(m.openHits(m.msgKeywords())())
	if !m.hitsOpen() || len(m.msg.hl.items) == 0 || m.msg.hl.rec.Host != "bobs" {
		t.Fatalf("the shared machine's hits are read through the server: %+v", m.msg.hl.items)
	}
}
