package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

type twoSessions struct{}

func (twoSessions) Handle(_ context.Context, method string, _ json.RawMessage) (any, error) {
	if method == remote.MList {
		return remote.List{Sessions: []remote.Session{{SessionID: "s-run"}, {SessionID: "s-mine"}}}, nil
	}
	return remote.Text{Text: "hi"}, nil
}

func TestANodeSharingRunsAnswersOnlyItsRunsSessions(t *testing.T) {
	dir := filepath.Join(tend.Home(), "node")
	os.MkdirAll(dir, 0o700)
	if err := capture.KeepRunSession(dir, "s-run", capture.RunSession{Run: "r_1"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := shareSessions(ctx, ShareRuns, twoSessions{}, remote.MList, nil)
	if l, ok := res.(remote.List); err != nil || !ok || len(l.Sessions) != 1 || l.Sessions[0].SessionID != "s-run" {
		t.Fatalf("%+v %v", res, err)
	}
	ref := func(sid string) json.RawMessage {
		b, _ := json.Marshal(remote.MessagesParams{Ref: remote.Ref{SessionID: sid}})
		return b
	}
	if _, err := shareSessions(ctx, ShareRuns, twoSessions{}, remote.MMessages, ref("s-mine")); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a session no run left: %v", err)
	}
	if _, err := shareSessions(ctx, ShareRuns, twoSessions{}, remote.MMessages, ref("s-run")); err != nil {
		t.Fatalf("a run's session: %v", err)
	}
	if _, err := shareSessions(ctx, ShareNone, twoSessions{}, remote.MMessages, ref("s-run")); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("none: %v", err)
	}
	if _, err := shareSessions(ctx, ShareAll, twoSessions{}, remote.MMessages, ref("s-mine")); err != nil {
		t.Fatalf("all: %v", err)
	}
}
