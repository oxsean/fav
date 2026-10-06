package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// putTransports reach mba's fakeHost as the TUI does in mode 1 (ssh: a tend rpc per host) and through a coordinator's
// node.call (the viewer's own machine, so its list is kept on disk).
var putTransports = []struct {
	name  string
	hosts func(t *testing.T, f *fakeHost) *remote.Hosts
}{
	{"ssh", func(t *testing.T, f *fakeHost) *remote.Hosts { return hostsOf(t, f, nil) }},
	{"node.call", func(t *testing.T, f *fakeHost) *remote.Hosts {
		nc := remote.NewNodeCall("https://tend.example", func(ctx context.Context, machine, method string, params json.RawMessage, out any) error {
			if machine != "mba" {
				return &wire.Error{Code: wire.CodeNotFound}
			}
			a, err := f.Handle(ctx, method, params)
			if err != nil {
				return err
			}
			b, _ := json.Marshal(a)
			return json.Unmarshal(b, out)
		})
		nc.SetMachines([]remote.Machine{{Name: "mba", Mine: true}})
		h := remote.NewHostsOver(nc)
		t.Cleanup(h.Close)
		return h
	}},
}

func (f *fakeHost) session(sid string) remote.Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[slices.IndexFunc(f.sessions, func(s remote.Session) bool { return s.SessionID == sid })]
}

// cachedOnDisk is mba's list as kept on disk.
func cachedOnDisk(t *testing.T, sid string) remote.Session {
	t.Helper()
	var c struct{ Sessions []remote.Session }
	b, err := os.ReadFile(filepath.Join(remote.CacheDir("mba"), "sessions.json"))
	if err != nil || json.Unmarshal(b, &c) != nil {
		t.Fatalf("mba's list is kept on disk: %v", err)
	}
	i := slices.IndexFunc(c.Sessions, func(s remote.Session) bool { return s.SessionID == sid })
	if i < 0 {
		t.Fatalf("%s is not in the kept list", sid)
	}
	return c.Sessions[i]
}

// TestRemoteRowsWriteThroughPut: on the viewer's own machine favorite, done, archive, edit and undo go to that
// machine's put and come back into the same row, with the flash and the undo offer of this machine's rows; over either
// transport the list kept on disk takes the answered row.
func TestRemoteRowsWriteThroughPut(t *testing.T) {
	steps := []struct {
		name string
		do   func(m *Model)
		note func(r *tend.Rec) string
		ok   func(s remote.Session) bool
	}{
		{"favorite", func(m *Model) { key(m, "f") },
			func(r *tend.Rec) string { return i18n.F("flash.favorited_fresh", render.Truncate(r.Title, 30)) },
			func(s remote.Session) bool { return s.FavoritedAt != nil && s.ID != "" }},
		{"undo the favorite", func(m *Model) { key(m, "u") },
			func(r *tend.Rec) string { return i18n.F("undo.done", r.Title) },
			func(s remote.Session) bool { return s.FavoritedAt == nil }},
		{"done", func(m *Model) { key(m, "x") },
			func(r *tend.Rec) string {
				return i18n.F("undo.offer", i18n.F("flash.status_set", render.StatusLabel(tend.StatusDone), r.Title), keyOf(inList, actUndo))
			},
			func(s remote.Session) bool { return s.Status == tend.StatusDone }},
		{"undo the done", func(m *Model) { key(m, "u") },
			func(r *tend.Rec) string { return i18n.F("undo.done", r.Title) },
			func(s remote.Session) bool { return s.Status != tend.StatusDone }},
		{"archive", func(m *Model) { key(m, "a") },
			func(r *tend.Rec) string {
				return i18n.F("undo.offer", i18n.F("flash.archived", r.Title), keyOf(inList, actUndo))
			},
			func(s remote.Session) bool { return s.ArchivedAt != nil }},
		{"undo the archive", func(m *Model) { key(m, "u") },
			func(r *tend.Rec) string { return i18n.F("undo.done", r.Title) },
			func(s remote.Session) bool { return s.ArchivedAt == nil }},
		{"edit", func(m *Model) {
			key(m, "e")
			if m.ov.kind != ovEdit {
				panic("e opens the edit dialog on the viewer's own machine")
			}
			m.ov.edit.SetValue("远端改过的标题")
			m.ov.edit2.SetValue("ops remote")
			key(m, "ctrl+s")
		},
			func(*tend.Rec) string { return i18n.F("edit.saved", "远端改过的标题") },
			func(s remote.Session) bool {
				return s.Title == "远端改过的标题" && strings.Join(s.Tags, " ") == "ops remote"
			}},
	}
	for _, tr := range putTransports {
		t.Run(tr.name, func(t *testing.T) {
			t.Setenv("TEND_HOME", t.TempDir())
			f := newFakeHost()
			m := sized(t, 140, 40)
			m.useHosts(tr.hosts(t, f))
			m.setView(viewSessions)
			fetch(t, m)
			showHosts(m, "host:mba status:all")
			row := cursorOn(t, m, "r-a")
			for _, st := range steps {
				seen := row.UpdatedAt
				st.do(m)
				if got := f.session("r-a"); !st.ok(got) {
					t.Fatalf("%s: mba's record: %+v", st.name, got)
				}
				if m.current() != row || row.Host != "mba" {
					t.Fatalf("%s: the row is the same one, kept under the cursor", st.name)
				}
				if want := st.note(row); m.notice != want {
					t.Fatalf("%s: flash %q, want %q", st.name, m.notice, want)
				}
				if got := f.session("r-a"); !row.UpdatedAt.Equal(got.UpdatedAt) || !st.ok(cachedOnDisk(t, "r-a")) {
					t.Fatalf("%s: the row and the kept list are the answered one", st.name)
				}
				f.mu.Lock()
				expect := f.expects[len(f.expects)-1]
				f.mu.Unlock()
				if edit := st.name == "edit"; edit != !expect.IsZero() || edit && !expect.Equal(seen) {
					t.Fatalf("%s: only the edit dialog says what it saw: %v", st.name, expect)
				}
			}
			if len(m.store.All()) != 4 {
				t.Fatal("nothing is written to this machine's records")
			}
		})
	}
}

// TestARemoteEditOverAChangedRecordSaysSo: the edit dialog's put fails as stale when the record changed since it was
// read; the row keeps what it showed.
func TestARemoteEditOverAChangedRecordSaysSo(t *testing.T) {
	t.Setenv("TEND_HOME", t.TempDir())
	f := newFakeHost()
	m := remoteModel(t, f, nil)
	fetch(t, m)
	showHosts(m, "host:mba")
	row := cursorOn(t, m, "r-a")
	key(m, "e")
	f.mu.Lock()
	f.sessions[0].UpdatedAt = time.Now().Add(time.Minute)
	f.mu.Unlock()
	m.ov.edit.SetValue("x")
	key(m, "ctrl+s")
	if m.notice != i18n.F("remote.put_stale", "mba") || row.Title == "x" {
		t.Fatalf("%q %q", m.notice, row.Title)
	}
}

// TestAnOldTendKeepsItsRowsReadOnly: a machine whose hello lists no put says how to update it, before any dialog
// opens; one not reached yet says it when the put comes back unknown.
func TestAnOldTendKeepsItsRowsReadOnly(t *testing.T) {
	old := i18n.F("remote.put_old", "mba", "mba")
	f := newFakeHost()
	f.methods = []string{remote.MHello, remote.MList, remote.MLive, remote.MMessages}
	m := remoteModel(t, f, nil)
	m.remote["mba"].merge([]*tend.Rec{f.session("r-a").Rec("mba")})
	showHosts(m, "host:mba")
	cursorOn(t, m, "r-a")
	key(m, "f")
	if m.notice != old || f.called(remote.MPut) != 0 {
		t.Fatalf("unknown before the put: %q, %d sent", m.notice, f.called(remote.MPut))
	}
	fetch(t, m)
	cursorOn(t, m, "r-a")
	for _, k := range []string{"f", "x", "a", "e"} {
		m.notice = ""
		key(m, k)
		if m.ov.active() || m.notice != old {
			t.Fatalf("%s: overlay %d, %q", k, m.ov.kind, m.notice)
		}
	}
	if f.called(remote.MPut) != 0 || m.current().Favorite() {
		t.Fatal("nothing is sent to an old tend")
	}
}

// TestARemotePutThatFailsSaysWhy: a machine that cannot be reached leaves the row as it was.
func TestARemotePutThatFailsSaysWhy(t *testing.T) {
	f := newFakeHost()
	offline := false
	m := remoteModel(t, f, &offline)
	fetch(t, m)
	showHosts(m, "host:mba")
	row := cursorOn(t, m, "r-a")
	offline = true
	m.hosts.Close() // the next call dials again, and fails
	m.useHosts(hostsOf(t, f, &offline))
	showHosts(m, "host:mba")
	m.remote["mba"].merge([]*tend.Rec{row})
	m.refresh()
	cursorOn(t, m, "r-a")
	key(m, "f")
	if want := i18n.F("remote.put_failed", "mba", remote.Reason(&wire.Error{Code: wire.CodeOffline})); m.notice != want || row.Favorite() {
		t.Fatalf("%q, want %q", m.notice, want)
	}
}
