package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
)

// fakeHost answers like a remote fav: a list, who runs, 60 messages (Off = 100 × index, oldest first), checks.
type fakeHost struct {
	mu       sync.Mutex
	sessions []remote.Session
	live     map[string]capture.Live
	msgs     []capture.Message
	calls    map[string]int
}

func newFakeHost() *fakeHost {
	now := time.Now()
	f := &fakeHost{calls: map[string]int{}, live: map[string]capture.Live{"r-live": {Status: "working"}}}
	for i, title := range []string{"远端分页排障", "remote websocket fix", "远端正在跑的"} {
		id := []string{"r-a", "r-b", "r-live"}[i]
		f.sessions = append(f.sessions, remote.Session{Provider: fav.ProviderClaude, SessionID: id, Title: title,
			Project: "notes-api", Cwd: "/home/u/dev/notes-api", Turns: 12, Msgs: 60, LastAt: now.Add(-time.Duration(i) * time.Hour),
			UpdatedAt: now})
	}
	for i := range 60 {
		f.msgs = append(f.msgs, capture.Message{Role: []string{"user", "assistant"}[i%2], Text: "第 " + string(rune('A'+i%26)) + " 句",
			Off: int64(i * 100), At: now.Add(time.Duration(i-60) * time.Minute)})
	}
	return f
}

func (f *fakeHost) Handle(_ context.Context, method string, params json.RawMessage) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[method]++
	switch method {
	case remote.MHello:
		return remote.Hello{Proto: remote.Proto, Version: "test"}, nil
	case remote.MList:
		return remote.List{Sessions: slices.Clone(f.sessions)}, nil
	case remote.MLive:
		return remote.Live{Live: f.live}, nil
	case remote.MMessages:
		var p remote.MessagesParams
		json.Unmarshal(params, &p)
		var page capture.Page
		for i := len(f.msgs) - 1; i >= 0 && len(page.Msgs) < p.N; i-- {
			if p.Before < 0 || f.msgs[i].Off < p.Before {
				page.Msgs = append(page.Msgs, f.msgs[i])
			}
		}
		if n := len(page.Msgs); n > 0 {
			page.From, page.Done = page.Msgs[n-1].Off, page.Msgs[n-1].Off == 0
		} else {
			page.Done = true
		}
		return page, nil
	case remote.MChecks:
		return remote.Checks{Checks: []capture.Check{{OK: true, Text: "远端检查通过"}}}, nil
	case remote.MText:
		return remote.Text{Text: "远端全文"}, nil
	}
	return nil, &remote.Error{Code: remote.CodeUnknownMethod}
}

func (f *fakeHost) called(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

// hostsOf reaches one host "mba" through f; offline makes every dial fail.
func hostsOf(t *testing.T, f *fakeHost, offline *bool) *remote.Hosts {
	t.Helper()
	h := remote.NewHostsDial([]fav.Host{{Name: "mba"}}, i18n.ZH, func(fav.Host) (*remote.Client, error) {
		if offline != nil && *offline {
			return nil, &remote.Error{Code: remote.CodeOffline}
		}
		return remote.Pipe(f), nil
	})
	t.Cleanup(h.Close)
	return h
}

// remoteModel is the sessions view with host mba reached through f.
func remoteModel(t *testing.T, f *fakeHost, offline *bool) *Model {
	t.Helper()
	m := sized(t, 140, 40)
	m.useHosts(hostsOf(t, f, offline))
	m.setView(viewSessions)
	return m
}

func fetch(t *testing.T, m *Model) {
	t.Helper()
	cmd := m.fetchHost("mba")
	if cmd == nil {
		t.Fatal("no fetch issued")
	}
	m.Update(cmd())
}

func remoteRows(m *Model) []*fav.Rec {
	var out []*fav.Rec
	for _, r := range m.rows {
		if r.rec != nil && r.rec.Host != "" {
			out = append(out, r.rec)
		}
	}
	return out
}

func showHosts(m *Model, q string) {
	m.search.SetValue(q)
	m.refresh()
}

func cursorOn(t *testing.T, m *Model, sid string) *fav.Rec {
	t.Helper()
	for i, r := range m.rows {
		if r.rec != nil && r.rec.SessionID == sid {
			m.cursor = i
			return r.rec
		}
	}
	t.Fatalf("%s is not listed", sid)
	return nil
}

func TestRemoteRowsShowUnderHostAllAndKeepTheirPointer(t *testing.T) {
	f := newFakeHost()
	m := remoteModel(t, f, nil)
	fetch(t, m)
	if n := len(remoteRows(m)); n != 0 {
		t.Fatalf("by default only this machine's sessions: %d remote rows", n)
	}
	showHosts(m, "host:all")
	rows := remoteRows(m)
	if len(rows) != 3 || len(m.rows) <= 3 {
		t.Fatalf("host:all lists this machine and mba: remote %d of %d", len(rows), len(m.rows))
	}
	if !strings.Contains(ansi.Strip(strings.Join(m.cardBox(rows[0], false, 60), "\n")), "@mba") || !strings.Contains(ansi.Strip(m.recLine(rows[0], false, 60)), "@mba") {
		t.Error("remote cards and lines carry the host mark")
	}
	a := cursorOn(t, m, "r-a")
	f.mu.Lock()
	f.sessions[0].Title = "远端分页排障（续）"
	f.mu.Unlock()
	fetch(t, m)
	if m.current() != a || a.Title != "远端分页排障（续）" {
		t.Fatalf("a second fetch updates the same row in place and the cursor stays: %q", a.Title)
	}
	showHosts(m, "host:mba status:live")
	if rows := remoteRows(m); len(rows) != 1 || rows[0].SessionID != "r-live" {
		t.Fatalf("status:live asks mba's own live map: %v", rows)
	}
	m.setView(viewLive)
	showHosts(m, "host:all")
	if rows := remoteRows(m); len(rows) != 1 {
		t.Fatalf("Agents shows mba's running session when host: selects it: %d", len(rows))
	}
}

func TestRemotePanePagesThroughTheSource(t *testing.T) {
	f := newFakeHost()
	m := remoteModel(t, f, nil)
	fetch(t, m)
	showHosts(m, "host:mba")
	r := cursorOn(t, m, "r-a")
	settle(m)
	p := m.probes[r]
	if p == nil || !p.done || len(p.msgs) != recentMsgs || p.full || len(p.checks) != 1 || p.checks[0].Text != "远端检查通过" {
		t.Fatalf("the probe reads mba's tail and checks: %+v", p)
	}
	msg := runCmd(t, m.load(r, olderMsgs))
	m.Update(msg)
	if len(p.msgs) != 60 || !p.full || p.msgs[59].Off != 0 {
		t.Fatalf("the next page comes from mba too: %d full=%v", len(p.msgs), p.full)
	}
	if f.called(remote.MMessages) != 2 {
		t.Errorf("two pages, two calls: %d", f.called(remote.MMessages))
	}
	if !strings.Contains(ansi.Strip(m.screen()), "远端检查通过") {
		t.Error("the remote checks render in the detail")
	}
}

func TestUnreachableHostShowsItsCacheAndOffline(t *testing.T) {
	f := newFakeHost()
	m := sized(t, 140, 40)
	m.useHosts(hostsOf(t, f, nil))
	fetch(t, m) // writes the cache

	offline := true
	m = remoteModel(t, f, &offline)
	showHosts(m, "host:all")
	if n := len(remoteRows(m)); n != 3 {
		t.Fatalf("before any fetch the cached list shows: %d", n)
	}
	fetch(t, m)
	if n := len(remoteRows(m)); n != 3 {
		t.Fatalf("a failed fetch keeps the cached rows: %d", n)
	}
	head := ansi.Strip(m.header())
	if !strings.Contains(head, "mba："+i18n.T("remote.err.offline")) || !strings.Contains(head, "前") {
		t.Fatalf("the header marks mba offline with the age of its list: %q", head)
	}
	if m.notice != "" {
		t.Errorf("no interrupting error: %q", m.notice)
	}
	showHosts(m, "")
	if strings.Contains(ansi.Strip(m.header()), i18n.T("remote.err.offline")) {
		t.Error("the offline mark only shows while the filter shows the host")
	}
}

func TestRemoteResumeDialogOffersOnlyRemoteActions(t *testing.T) {
	f := newFakeHost()
	m := remoteModel(t, f, nil)
	fetch(t, m)
	showHosts(m, "host:mba")
	r := cursorOn(t, m, "r-a")
	m.Update(press("enter"))
	if m.ov.kind != ovResume {
		t.Fatal("Enter opens the resume dialog")
	}
	m.screen()
	var labels []string
	for _, b := range m.ov.btns {
		labels = append(labels, ansi.Strip(b.label))
	}
	want := []string{keyed(enterKey, i18n.T("resume.btn_resume")), keyed(keyOf(inResume, actCopy), i18n.T("resume.btn_copy")), cancelBtn().label}
	if !slices.Equal(labels, want) {
		t.Fatalf("resume and copy only: %q", labels)
	}
	m.Update(press("f"))
	if m.ov.kind != ovResume || m.notice != i18n.T("remote.read_only") || r.Favorite() || len(m.store.All()) != 4 {
		t.Fatalf("f in the dialog only flashes: notice=%q", m.notice)
	}
	_, cmd := m.Update(press("enter"))
	s := m.result.Start
	if !m.quitting || cmd == nil || s == nil || s.Exec != "fav" || !slices.Equal(s.Args, []string{"resume", "--terminal", "--no-herdr", "r-a"}) {
		t.Fatalf("outside Herdr Enter quits and runs the resume command here: %+v", s)
	}
}

func TestWriteKeysOnARemoteRowOnlyFlash(t *testing.T) {
	f := newFakeHost()
	m := remoteModel(t, f, nil)
	fetch(t, m)
	showHosts(m, "host:mba")
	r := cursorOn(t, m, "r-a")
	for _, k := range []string{"f", "*", "x", "a", "e", "M", "D", "w", "`", ".", "H", "X"} {
		m.notice = ""
		m.Update(press(k))
		if m.ov.active() || m.notice != i18n.T("remote.read_only") {
			t.Errorf("%s: overlay %d, notice %q", k, m.ov.kind, m.notice)
		}
		m.closeOverlay()
	}
	if r.Favorite() || r.Archived() || len(m.store.All()) != 4 {
		t.Fatal("nothing is written for another machine's session")
	}
	if m.editRec(r, func(r *fav.Rec) { r.Title = "x" }) != nil {
		t.Fatal("editRec refuses a remote record")
	}
}

func TestHostChipPicksTheHost(t *testing.T) {
	m := sized(t, 140, 40)
	if slices.ContainsFunc(m.chipData(), func(c chip) bool { return c.label == i18n.T("label.host") }) {
		t.Fatal("no host chip without hosts")
	}
	m.Update(press("m"))
	if m.ov.active() {
		t.Fatal("m does nothing without hosts")
	}
	m.useHosts(hostsOf(t, newFakeHost(), nil))
	cs := m.chipData()
	if c := cs[len(cs)-1]; c.label != i18n.T("label.host") || c.value != i18n.T("remote.local") {
		t.Fatalf("the host chip reads this machine: %+v", c)
	}
	m.Update(press("m"))
	if m.ov.kind != ovPicker || len(m.ov.items) != 3 {
		t.Fatalf("m opens the host picker: this machine / all / mba: %d", len(m.ov.items))
	}
	m.ov.cursor = 2
	m.Update(press("enter"))
	if m.search.Value() != "host:mba" {
		t.Fatalf("picking mba filters to it: %q", m.search.Value())
	}
}

// withRemote shows two hosts under host:all without reaching them: mba's rows (the cursor on one), lg-win offline
// since five minutes.
func withRemote(m *Model) {
	down := func(fav.Host) (*remote.Client, error) { return nil, &remote.Error{Code: remote.CodeOffline} }
	m.useHosts(remote.NewHostsDial([]fav.Host{{Name: "mba"}, {Name: "lg-win-workstation"}}, i18n.ZH, down))
	now := time.Now()
	var recs []*fav.Rec
	for i, title := range []string{"远端会话：分页游标在另一台机器上的排查，标题很长很长很长很长", "remote session"} {
		recs = append(recs, remote.Session{Provider: fav.ProviderCodex, SessionID: fmt.Sprintf("0199a0c2-7e1f-7a31-9d44-00000000000%d", i), Title: title, Project: "notes-api",
			Cwd: "/home/u/dev/notes-api", Turns: 30, LastAt: now.Add(time.Duration(i) * time.Minute), UpdatedAt: now}.Rec("mba"))
	}
	m.remote["mba"].merge(recs)
	m.remote["lg-win-workstation"].err, m.remote["lg-win-workstation"].at = &remote.Error{Code: remote.CodeOffline}, now.Add(-5*time.Minute)
	m.setView(viewSessions)
	m.search.SetValue("host:all")
	m.refresh()
	m.cursor, m.scroll = 0, 0
	m.clampCursor()
}

func TestHostsArePolledWhileShownAndBackOffWhenDown(t *testing.T) {
	f := newFakeHost()
	m := remoteModel(t, f, nil)
	fetch(t, m)
	hr := m.remote["mba"]
	if m.hostTick("mba") != nil || !hr.idle {
		t.Fatal("a host the filter hides rests after its first fetch")
	}
	showHosts(m, "host:mba")
	if m.wakeHosts() == nil || hr.idle || !hr.loading {
		t.Fatal("showing the host fetches it again")
	}
	hr.loading = false
	down := hostMsg{name: "mba", st: remote.State{Err: &remote.Error{Code: remote.CodeOffline}}}
	for want := 1; want <= 5; want++ {
		m.applyHost(down)
		if hr.fails != min(want, 4) {
			t.Fatalf("failure %d: fails=%d", want, hr.fails)
		}
	}
	if min(hostsEvery<<hr.fails, hostsMaxEvery) != hostsMaxEvery {
		t.Error("the wait grows to the cap")
	}
	m.applyHost(hostMsg{name: "mba", st: remote.State{At: time.Now()}})
	if hr.fails != 0 {
		t.Error("an answer resets the wait")
	}
}

func TestRemoteRowsStayReadOnlyWithAChipFocused(t *testing.T) {
	f := newFakeHost()
	m := remoteModel(t, f, nil)
	fetch(t, m)
	showHosts(m, "host:mba")
	cursorOn(t, m, "r-a")
	for _, k := range []string{"D", "M", "f"} {
		m.chipFocus, m.notice = 0, ""
		m.Update(press(k))
		if m.ov.active() || m.notice != i18n.T("remote.read_only") {
			t.Errorf("%s with a chip focused: overlay %d, notice %q", k, m.ov.kind, m.notice)
		}
		m.closeOverlay()
	}
}

func TestARemoteDialogNeverLeadsWithThisMachinesApp(t *testing.T) {
	m := sized(t, 140, 40)
	m.cfg.ResumeIn = fav.ResumeApp
	capture.SetAppAvailable(fav.ProviderClaude, true)
	t.Cleanup(func() { capture.ForgetAppAvailable(fav.ProviderClaude) })
	r := &fav.Rec{Provider: fav.ProviderClaude, SessionID: "c5126b86-64bb-46a8-9a69-fc421c8f4f9a", Cwd: t.TempDir()}
	appFiles(t, r) // a copy of the session here, so the app could open it
	if !m.appFirst(r, capture.Plan{}) {
		t.Fatal("the fixture: this machine's session leads with the app")
	}
	r.Host = "mba"
	if m.appFirst(r, capture.Plan{}) {
		t.Fatal("the same session on mba never does")
	}
}

func TestAFailedLiveReadKeepsTheLastAnswer(t *testing.T) {
	f := newFakeHost()
	m := remoteModel(t, f, nil)
	fetch(t, m)
	hr := m.remote["mba"]
	if len(hr.live) != 1 {
		t.Fatalf("live: %v", hr.live)
	}
	m.applyHost(hostMsg{name: "mba", st: remote.State{At: time.Now()}, liveErr: &remote.Error{Code: remote.CodeTimeout}})
	if len(hr.live) != 1 {
		t.Fatal("a failed live read is not \"nothing runs\"")
	}
	m.setView(viewLive)
	showHosts(m, "host:mba")
	if !strings.Contains(ansi.Strip(m.header()), "mba：运行状态未知") {
		t.Fatalf("Agents says who runs on mba is unknown: %q", ansi.Strip(m.header()))
	}
}

func TestAFailedFirstReadIsProbedAgainWhenTheHostAnswers(t *testing.T) {
	f := newFakeHost()
	m := remoteModel(t, f, nil)
	fetch(t, m)
	showHosts(m, "host:mba")
	r := cursorOn(t, m, "r-a")
	m.probes = map[*fav.Rec]*probe{r: {}}
	m.Update(probeMsg{r, nil, capture.Page{From: -1, Err: &remote.Error{Code: remote.CodeOffline}}})
	if p := m.probes[r]; !p.failed || p.full {
		t.Fatalf("a failed read is not the file head: %+v", p)
	}
	cmd := m.applyHost(hostMsg{name: "mba", st: remote.State{At: time.Now()}})
	if p := m.probes[r]; p == nil || p.failed || cmd == nil {
		t.Fatalf("the host answering probes the row again: %+v", p)
	}
}

func TestATickFetchesAShownHostAgain(t *testing.T) {
	f := newFakeHost()
	m := remoteModel(t, f, nil)
	fetch(t, m)
	showHosts(m, "host:mba")
	_, cmd := m.Update(hostTickMsg("mba"))
	if cmd == nil || !m.remote["mba"].loading {
		t.Fatal("the tick of a shown host fetches it")
	}
}

func TestAFailedPageKeepsItsPlace(t *testing.T) {
	f := newFakeHost()
	m := remoteModel(t, f, nil)
	fetch(t, m)
	showHosts(m, "host:mba")
	r := cursorOn(t, m, "r-a")
	settle(m)
	p := m.probes[r]
	from, n := p.from, len(p.msgs)
	p.loading = true
	m.Update(pageMsg{r, capture.Page{From: from, Err: &remote.Error{Code: remote.CodeTimeout}}})
	if p.from != from || p.full || p.loading || len(p.msgs) != n || m.notice == "" {
		t.Fatalf("a failed page keeps its place: from %d→%d full=%v loading=%v", from, p.from, p.full, p.loading)
	}
	m.chatScroll = len(p.msgs) // at the loaded end: a working probe would fetch the next page now
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if p.loading || f.called(remote.MMessages) != 1 {
		t.Fatalf("no page is asked again on its own after a failure: %d reads", f.called(remote.MMessages))
	}
	m.Update(pageMsg{r, capture.Page{Err: &remote.Error{Code: remote.CodeStale}}})
	if q := m.probes[r]; q == p || q == nil {
		t.Fatal("a rewritten transcript is read again from the end")
	}
}

func TestARemoteProjectIsDescribedFromItsMachine(t *testing.T) {
	f := newFakeHost()
	m := remoteModel(t, f, nil)
	fetch(t, m)
	m.setView(viewProjects)
	showHosts(m, "host:mba")
	text := ansi.Strip(strings.Join(m.projectBlock("notes-api", 0, 0, 90, 30), "\n"))
	if !strings.Contains(text, "mba:/home/u/dev/notes-api") || strings.Contains(text, strings.TrimSpace(i18n.T("project.missing"))) {
		t.Fatalf("the directory is mba's, not checked on this machine:\n%s", text)
	}
}
