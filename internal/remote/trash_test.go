package remote

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// TestHostsTrashAndRestoreThroughTheMachine: Trash moves the session's files on that machine and drops it from the
// list kept here, Trashed lists it with the time it went, Restore puts the files back; a tend without trash is not
// sent any of them.
func TestHostsTrashAndRestoreThroughTheMachine(t *testing.T) {
	d, _ := localMachine(t)
	h := NewHostsDial([]tend.Host{{Name: "mba"}}, i18n.ZH, func(tend.Host) (*Client, error) { return Pipe(NewLocal("test")), nil })
	t.Cleanup(h.Close)
	ctx := context.Background()
	s := d.Get("oauth")
	ref := Ref{s.Provider, s.ID}
	if _, st := h.Sessions(ctx, "mba"); st.Err != nil {
		t.Fatal(st.Err)
	}
	cached := func() bool {
		recs, _ := h.Cached("mba")
		return slices.ContainsFunc(recs, func(r *tend.Rec) bool { return r.SessionID == s.ID })
	}
	if !cached() {
		t.Fatal("listed before")
	}

	tr, err := h.Trash(ctx, "mba", ref)
	if err != nil || tr.Files == 0 || tr.Title == "" {
		t.Fatalf("trash: %+v %v", tr, err)
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Fatalf("the transcript moved: %v", err)
	}
	if cached() {
		t.Fatal("the kept list drops it")
	}
	recs, _, err := h.Trashed(ctx, "mba")
	if err != nil || len(recs) != 1 || recs[0].SessionID != s.ID || recs[0].Host != "mba" || time.Since(recs[0].UpdatedAt) > time.Minute {
		t.Fatalf("trashed: %+v %v", recs, err)
	}

	rs, err := h.Restore(ctx, "mba", ref)
	if err != nil || rs.Files != tr.Files {
		t.Fatalf("restore: %+v %v", rs, err)
	}
	if _, err := os.Stat(s.Path); err != nil {
		t.Fatalf("the transcript is back: %v", err)
	}
	if recs, _, _ := h.Trashed(ctx, "mba"); len(recs) != 0 {
		t.Fatalf("the trash is empty again: %+v", recs)
	}
	if _, err := h.Restore(ctx, "mba", ref); code(err) != wire.CodeNotFound {
		t.Fatalf("restored twice: %v", err)
	}

	old := NewHostsDial([]tend.Host{{Name: "old"}}, i18n.ZH, func(tend.Host) (*Client, error) { return Pipe(listHandler{}), nil })
	t.Cleanup(old.Close)
	if _, err := old.Trash(ctx, "old", ref); code(err) != wire.CodeUnknownMethod {
		t.Errorf("trash on an old tend: %v", err)
	}
	if _, err := old.Restore(ctx, "old", ref); code(err) != wire.CodeUnknownMethod {
		t.Errorf("restore on an old tend: %v", err)
	}
	if _, _, err := old.Trashed(ctx, "old"); code(err) != wire.CodeUnknownMethod {
		t.Errorf("the trash of an old tend: %v", err)
	}
}
