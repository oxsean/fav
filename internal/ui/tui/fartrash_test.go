package tui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

func (f *fakeHost) inTrash(sid string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.ContainsFunc(f.trashed, func(s remote.Session) bool { return s.SessionID == sid })
}

func listed(m *Model, sid string) *tend.Rec {
	for _, r := range remoteRows(m) {
		if r.SessionID == sid {
			return r
		}
	}
	return nil
}

// goOffline: mba's rows stay as fetched, and the next call dials again and fails.
func goOffline(t *testing.T, m *Model, f *fakeHost, off *bool) {
	t.Helper()
	rows := m.remote["mba"].recs
	*off = true
	m.hosts.Close()
	m.useHosts(hostsOf(t, f, off))
	m.remote["mba"].merge(rows)
	showHosts(m, "host:mba status:all")
}

// openTrash shows mba's trash and reads it, as the trash view does when it opens.
func openTrash(m *Model) {
	showHosts(m, "host:mba status:trash")
	pump(m, m.wakeHosts())
}

// TestRemoteDeleteGoesThroughTheMachine: D on the viewer's own machine asks as for this machine's rows, the machine
// moves the session into its trash and the row leaves the list; the trash view lists that machine's trash and D there
// restores through restore; over either transport, and nothing is written to this machine's records.
func TestRemoteDeleteGoesThroughTheMachine(t *testing.T) {
	for _, tr := range putTransports {
		t.Run(tr.name, func(t *testing.T) {
			t.Setenv("TEND_HOME", t.TempDir())
			f := newFakeHost()
			m := sized(t, 140, 40)
			m.useHosts(tr.hosts(t, f))
			m.setView(viewSessions)
			fetch(t, m)
			before := len(m.store.All())
			showHosts(m, "host:mba status:all")
			row := cursorOn(t, m, "r-a")

			key(m, "D")
			if m.ov.kind != ovConfirm || !slices.Contains(m.ov.lines, i18n.F("trash.confirm_far", "mba", keyOf(inList, actDelete))) {
				t.Fatalf("D asks first: %d %q", m.ov.kind, m.ov.lines)
			}
			key(m, "esc")
			if f.called(remote.MTrash) != 0 {
				t.Fatal("Esc sends nothing")
			}
			key(m, "D")
			key(m, "y")
			if !f.inTrash("r-a") || listed(m, "r-a") != nil {
				t.Fatalf("mba trashed it and the row left: %v", f.inTrash("r-a"))
			}
			if want := i18n.F("remote.trash_moved", "mba", render.Truncate(row.Title, 40)); m.notice != want {
				t.Fatalf("flash %q, want %q", m.notice, want)
			}
			if recs, _ := m.hosts.Cached("mba"); slices.ContainsFunc(recs, func(r *tend.Rec) bool { return r.SessionID == "r-a" }) {
				t.Fatal("the kept list drops it")
			}

			openTrash(m)
			if f.called(remote.MQuery) != 1 {
				t.Fatalf("the trash view reads mba's trash once: %d", f.called(remote.MQuery))
			}
			trashed := cursorOn(t, m, "r-a")
			if trashed.Host != "mba" || m.remote["mba"].days != 7 {
				t.Fatalf("listed as mba's: %+v", trashed)
			}
			pump(m, m.wakeHosts())
			if f.called(remote.MQuery) != 1 {
				t.Fatal("read once per visit")
			}
			key(m, "D")
			if f.inTrash("r-a") || listed(m, "r-a") != nil {
				t.Fatal("restored on mba, gone from its trash here")
			}
			if want := i18n.F("remote.restored", "mba", render.Truncate(row.Title, 40)); m.notice != want {
				t.Fatalf("flash %q, want %q", m.notice, want)
			}
			showHosts(m, "host:mba status:all")
			if listed(m, "r-a") == nil {
				t.Fatal("listed again after the fetch that follows")
			}
			if len(m.store.All()) != before {
				t.Fatal("nothing is written to this machine's records")
			}

			key(m, "D")
			key(m, "y")
			openTrash(m)
			if f.called(remote.MQuery) != 2 {
				t.Fatalf("a new visit reads the trash again: %d", f.called(remote.MQuery))
			}
		})
	}
}

// TestARemoteDeleteThatCannotLandSaysWhy: a session running there, a tend without trash, a machine out of reach each
// end in a flash naming the reason; the row stays.
func TestARemoteDeleteThatCannotLandSaysWhy(t *testing.T) {
	t.Run("running, as known here", func(t *testing.T) {
		f := newFakeHost()
		m := remoteModel(t, f, nil)
		fetch(t, m)
		showHosts(m, "host:mba status:all")
		cursorOn(t, m, "r-live")
		key(m, "D")
		if m.ov.active() || m.notice != i18n.F("remote.trash_busy", "mba") {
			t.Fatalf("overlay %d, %q", m.ov.kind, m.notice)
		}
	})
	t.Run("running, as the machine says", func(t *testing.T) {
		f := newFakeHost()
		m := remoteModel(t, f, nil)
		fetch(t, m)
		m.remote["mba"].live = nil
		showHosts(m, "host:mba status:all")
		cursorOn(t, m, "r-live")
		key(m, "D")
		key(m, "y")
		if m.notice != i18n.F("remote.trash_busy", "mba") || listed(m, "r-live") == nil {
			t.Fatalf("%q", m.notice)
		}
	})
	t.Run("old tend", func(t *testing.T) {
		f := newFakeHost()
		f.methods = []string{remote.MHello, remote.MList, remote.MLive, remote.MMessages, remote.MPut}
		m := remoteModel(t, f, nil)
		m.remote["mba"].merge([]*tend.Rec{f.session("r-a").Rec("mba")})
		showHosts(m, "host:mba status:all")
		cursorOn(t, m, "r-a")
		key(m, "D")
		key(m, "y")
		old := i18n.F("remote.trash_old", "mba", "mba")
		if m.notice != old || f.called(remote.MTrash) != 0 {
			t.Fatalf("unknown before the call: %q, %d sent", m.notice, f.called(remote.MTrash))
		}
		fetch(t, m)
		cursorOn(t, m, "r-a")
		m.notice = ""
		key(m, "D")
		if m.ov.active() || m.notice != old {
			t.Fatalf("known from its hello: overlay %d, %q", m.ov.kind, m.notice)
		}
		openTrash(m)
		if f.called(remote.MQuery) != 0 || len(remoteRows(m)) != 0 {
			t.Fatal("its trash is not asked for")
		}
		showHosts(m, "host:mba status:all")
		cursorOn(t, m, "r-a")
		key(m, "f")
		if !m.current().Favorite() {
			t.Fatal("put still carries the record edits")
		}
	})
	t.Run("offline", func(t *testing.T) {
		f := newFakeHost()
		off := false
		m := remoteModel(t, f, &off)
		fetch(t, m)
		goOffline(t, m, f, &off)
		cursorOn(t, m, "r-a")
		key(m, "D")
		key(m, "y")
		if want := i18n.F("remote.trash_failed", "mba", remote.Reason(&wire.Error{Code: wire.CodeOffline})); m.notice != want {
			t.Fatalf("%q, want %q", m.notice, want)
		}
		if listed(m, "r-a") == nil {
			t.Fatal("the row stays")
		}
		openTrash(m)
		if want := i18n.F("remote.trash_unread", "mba", remote.Reason(&wire.Error{Code: wire.CodeOffline})); m.notice != want {
			t.Fatalf("%q, want %q", m.notice, want)
		}
	})
}

// TestServedDeleteAndRestoreThroughTheServer: on the viewer's own machine D moves the session's files into that
// machine's trash through the server and the trash view restores them; a shared machine refuses before anything is
// sent.
func TestServedDeleteAndRestoreThroughTheServer(t *testing.T) {
	s := newServedRig(t, nil)
	m := s.m
	s.start(t)
	r := cursorOnHost(t, m, "mba")
	sid, path, title := r.SessionID, r.TranscriptPath, r.Title
	if path == "" {
		t.Fatal("the row names its transcript")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	key(m, "D")
	if m.ov.kind != ovConfirm {
		t.Fatal("D asks first")
	}
	key(m, "y")
	waitFor(t, m, func() bool { return m.notice == i18n.F("remote.trash_moved", "mba", render.Truncate(title, 40)) })
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the transcript moved on mba: %v", err)
	}
	if listed(m, sid) != nil {
		t.Fatal("the row left")
	}

	m.search.SetValue("host:mba status:trash")
	m.refresh()
	pump(m, m.wakeHosts())
	waitFor(t, m, func() bool { m.refresh(); return listed(m, sid) != nil })
	m.cursor = slices.IndexFunc(m.rows, func(row row) bool { return row.rec != nil && row.rec.SessionID == sid })
	key(m, "D")
	waitFor(t, m, func() bool { return m.notice == i18n.F("remote.restored", "mba", render.Truncate(title, 40)) })
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the transcript is back: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}

	cursorOnHost(t, m, "bobs")
	m.notice = ""
	key(m, "D")
	if m.ov.active() || m.notice != i18n.F("remote.shared_read_only", m.ownerName("bobs")) {
		t.Fatalf("a shared machine stays read only: %q", m.notice)
	}
	m.search.SetValue("host:bobs status:trash")
	m.refresh()
	pump(m, m.wakeHosts())
	if len(rowsOn(m, "bobs")) != 0 {
		t.Fatal("a shared machine's trash is not listed")
	}
}

// TestResumeSavesAnEditedTitleThroughEditRec: the title edited in the resume dialog is saved as any record edit: this
// machine's through the store (a session without a record gets one, not a favorite), another machine's through its put
// before the resume goes on.
func TestResumeSavesAnEditedTitleThroughEditRec(t *testing.T) {
	t.Run("saved record", func(t *testing.T) {
		m := sized(t, 140, 40)
		r := m.current()
		m.askResume()
		m.ov.edit.SetValue("恢复时改的标题")
		m.doResume(true)
		if got := m.store.Get(r.ID); got == nil || got.Title != "恢复时改的标题" {
			t.Fatalf("the store has the new title: %+v", got)
		}
		reloaded, err := tend.OpenAt(m.store.Path)
		if err != nil {
			t.Fatal(err)
		}
		if got := reloaded.Get(r.ID); got == nil || got.Title != "恢复时改的标题" {
			t.Fatalf("written to disk: %+v", got)
		}
	})
	t.Run("no record yet", func(t *testing.T) {
		m := sized(t, 140, 40)
		r := &tend.Rec{Provider: tend.ProviderClaude, SessionID: "fresh-0001", Title: "没收藏的", Cwd: t.TempDir()}
		r.Prepare()
		m.unfav = append(m.unfav, r)
		m.openResume(r)
		m.ov.edit.SetValue("头一回起的名字")
		before := len(m.store.All())
		m.doResume(true)
		got := m.store.BySession(r.Provider, r.SessionID)
		if got == nil || got.Title != "头一回起的名字" || got.Favorite() || len(m.store.All()) != before+1 {
			t.Fatalf("a record, not a favorite: %+v", got)
		}
		if slices.Contains(m.unfav, r) {
			t.Fatal("it leaves the unsaved rows")
		}
	})
	t.Run("another machine", func(t *testing.T) {
		f := newFakeHost()
		m := remoteModel(t, f, nil)
		fetch(t, m)
		showHosts(m, "host:mba status:all")
		row := cursorOn(t, m, "r-a")
		key(m, "enter")
		if m.ov.kind != ovResume {
			t.Fatalf("Enter opens the resume dialog: %d", m.ov.kind)
		}
		m.ov.edit.SetValue("远端恢复前改名")
		key(m, "enter")
		if f.session("r-a").Title != "远端恢复前改名" || row.Title != "远端恢复前改名" {
			t.Fatalf("mba's record and the row: %q %q", f.session("r-a").Title, row.Title)
		}
		if !m.quitting || m.Result().Start == nil {
			t.Fatal("the resume goes on once mba took the title")
		}
	})
	t.Run("another machine that cannot take it", func(t *testing.T) {
		f := newFakeHost()
		off := false
		m := remoteModel(t, f, &off)
		fetch(t, m)
		goOffline(t, m, f, &off)
		row := cursorOn(t, m, "r-a")
		key(m, "enter")
		m.ov.edit.SetValue("改不过去")
		key(m, "enter")
		if m.quitting || row.Title == "改不过去" || m.ov.kind != ovResume {
			t.Fatalf("the dialog stays with the reason: %q", m.notice)
		}
		if want := i18n.F("remote.put_failed", "mba", remote.Reason(&wire.Error{Code: wire.CodeOffline})); m.notice != want {
			t.Fatalf("%q, want %q", m.notice, want)
		}
	})
}
