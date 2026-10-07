package remote

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// TestFindNamesWhatTheListsLeaveOut: a session the node's lists hide (an sdk-cli run) is found by its full id and by a
// prefix, as the machine's own commands find it; ambiguous and unknown refs are counted, a share that keeps the session
// out finds nothing, and an older node, answering the query without its id, says unknown_method.
func TestFindNamesWhatTheListsLeaveOut(t *testing.T) {
	d := fixtureMachine(t)
	sdk := d.Get("sdk")
	ctx := context.Background()
	h := NewHostsDial([]tend.Host{{Name: "m"}}, "", func(tend.Host) (*Client, error) { return Pipe(NewLocal("t")), nil })
	t.Cleanup(h.Close)
	if recs, st := h.Sessions(ctx, "m"); st.Err != nil || slices.ContainsFunc(recs, func(r *tend.Rec) bool { return r.SessionID == sdk.ID }) {
		t.Fatalf("the list hides the sdk run: %v", st.Err)
	}
	for _, ref := range []string{sdk.ID, sdk.ID[:8]} {
		r, n, err := h.Find(ctx, "m", ref)
		if err != nil || n != 1 || r == nil || r.SessionID != sdk.ID || r.Host != "m" || r.Provider != tend.ProviderClaude {
			t.Errorf("%s: %+v %d %v", ref, r, n, err)
		}
	}
	if r, n, err := h.Find(ctx, "m", "fa"); err != nil || n < 2 || r != nil {
		t.Errorf("an ambiguous prefix: %+v %d %v", r, n, err)
	}
	if r, n, err := h.Find(ctx, "m", "zzzz"); err != nil || n != 0 || r != nil {
		t.Errorf("an unknown id: %+v %d %v", r, n, err)
	}

	params, _ := json.Marshal(QueryParams{ID: sdk.ID, Limit: 1})
	a, err := NewLocal("t").(Scoped).HandleIn(ctx, MQuery, params, func(string) bool { return false })
	if res, ok := a.(QueryResult); err != nil || !ok || res.Matched != 0 || len(res.Rows) != 0 {
		t.Errorf("a share that keeps it out: %+v %v", a, err)
	}

	old := NewHostsDial([]tend.Host{{Name: "m"}}, "", func(tend.Host) (*Client, error) {
		return Pipe(handlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			if method == MQuery {
				var p QueryParams
				json.Unmarshal(params, &p)
				p.ID = ""
				params, _ = json.Marshal(p)
			}
			return NewLocal("t").Handle(ctx, method, params)
		})), nil
	})
	t.Cleanup(old.Close)
	if _, _, err := old.Find(ctx, "m", sdk.ID); wire.Code(err) != wire.CodeUnknownMethod {
		t.Errorf("an older node: %v", err)
	}
}
