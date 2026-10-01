package node

import (
	"context"
	"encoding/json"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/wire"
)

// What a node answers of this machine's sessions (node.share_sessions).
const (
	ShareAll  = "all"  // every session: ssh and mode 1
	ShareRuns = "runs" // only its runs' sessions: the default of a node that dialed a server
	ShareNone = "none"
)

// sessionRefs are the session reads that name one session.
var sessionRefs = map[string]bool{remote.MMessages: true, remote.MText: true, remote.MSteps: true, remote.MPulse: true, remote.MChecks: true}

// shareSessions answers a session read under share: a list and who is running keep its runs' sessions, a read of
// another session is refused. Even a server that forwards every read can then reach only the runs' sessions.
func shareSessions(ctx context.Context, share string, sessions remote.Handler, method string, params json.RawMessage) (any, error) {
	if share == "" || share == ShareAll {
		return sessions.Handle(ctx, method, params)
	}
	runs := map[string]capture.RunSession{}
	if share == ShareRuns {
		runs = capture.RunSessions()
	}
	switch {
	case sessionRefs[method]:
		var ref remote.Ref
		json.Unmarshal(params, &ref)
		if _, ok := runs[ref.SessionID]; !ok {
			return nil, &wire.Error{Code: wire.CodeUnauthorized, Detail: "session " + ref.SessionID}
		}
	case method == remote.MList:
		res, err := sessions.Handle(ctx, method, params)
		if l, ok := res.(remote.List); ok && err == nil {
			kept := l.Sessions[:0:0]
			for _, s := range l.Sessions {
				if _, ok := runs[s.SessionID]; ok {
					kept = append(kept, s)
				}
			}
			l.Sessions = kept
			return l, nil
		}
		return res, err
	case method == remote.MLive:
		res, err := sessions.Handle(ctx, method, params)
		if l, ok := res.(remote.Live); ok && err == nil {
			kept := map[string]capture.Live{}
			for id, x := range l.Live {
				if _, ok := runs[id]; ok {
					kept[id] = x
				}
			}
			l.Live = kept
			return l, nil
		}
		return res, err
	}
	return sessions.Handle(ctx, method, params)
}
