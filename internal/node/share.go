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

// How share_sessions below all applies to a method.
const (
	shareOpen    = "open"    // nothing of a session: answered under every share
	shareListed  = "listed"  // lists sessions: only the runs' are kept, none under none
	shareScoped  = "scoped"  // counts, ranks or pages sessions: the handler leaves the others out before it does (remote.Scoped)
	shareNamed   = "named"   // names one session: a run's only, refused under none
	shareMachine = "machine" // this machine's files beyond any session's: refused
)

// shareClass is every session method's class; one without a class is refused below all.
var shareClass = map[string]string{
	remote.MHello: shareOpen, remote.MEcho: shareOpen,
	remote.MList: shareListed, remote.MLive: shareListed,
	remote.MQuery: shareScoped, remote.MGrep: shareScoped,
	remote.MMessages: shareNamed, remote.MText: shareNamed, remote.MSteps: shareNamed, remote.MPulse: shareNamed,
	remote.MChecks: shareNamed, remote.MPut: shareNamed, remote.MHits: shareNamed, remote.MTrash: shareNamed,
	remote.MRestore: shareNamed, remote.MHandoffFacts: shareNamed, remote.MExportPlan: shareNamed, remote.MExportRead: shareNamed,
	remote.MExportDone: shareNamed, remote.MCopies: shareNamed,
	remote.MHandoffPut: shareMachine, remote.MMemoryList: shareMachine, remote.MMemoryRead: shareMachine,
	remote.MMemoryTrash: shareMachine, remote.MMemoryRestore: shareMachine, remote.MMemoryPut: shareMachine,
	remote.MEnv: shareMachine, remote.MEnvFile: shareMachine, remote.MImportBegin: shareMachine,
	remote.MImportChunk: shareMachine, remote.MImportCommit: shareMachine, remote.MImportAbort: shareMachine,
}

// shareSessions answers a session read or write under share: a list and who is running keep its runs' sessions, a
// query or message search sees only them, a call naming another session is refused, and none refuses a query or search
// outright; a method on the machine's files, or one without a class, is refused. Even a server that forwards every call
// can then reach only the runs' sessions.
func shareSessions(ctx context.Context, share string, sessions remote.Handler, method string, params json.RawMessage) (any, error) {
	if share == "" || share == ShareAll {
		return sessions.Handle(ctx, method, params)
	}
	runs := map[string]capture.RunSession{}
	if share == ShareRuns {
		runs = capture.RunSessions()
	}
	switch shareClass[method] {
	case shareOpen:
	case shareScoped:
		sc, ok := sessions.(remote.Scoped)
		if share == ShareNone || !ok {
			return nil, &wire.Error{Code: wire.CodeUnauthorized, Detail: "sessions"}
		}
		return sc.HandleIn(ctx, method, params, func(id string) bool { _, ok := runs[id]; return ok })
	case shareNamed:
		var ref remote.Ref
		json.Unmarshal(params, &ref)
		if _, ok := runs[ref.SessionID]; !ok {
			return nil, &wire.Error{Code: wire.CodeUnauthorized, Detail: "session " + ref.SessionID}
		}
	case shareListed:
		res, err := sessions.Handle(ctx, method, params)
		if err != nil {
			return res, err
		}
		switch l := res.(type) {
		case remote.List:
			kept := l.Sessions[:0:0]
			for _, s := range l.Sessions {
				if _, ok := runs[s.SessionID]; ok {
					kept = append(kept, s)
				}
			}
			l.Sessions = kept
			return l, nil
		case remote.Live:
			kept := map[string]capture.Live{}
			for id, x := range l.Live {
				if _, ok := runs[id]; ok {
					kept[id] = x
				}
			}
			l.Live = kept
			return l, nil
		}
		return res, nil
	default:
		return nil, &wire.Error{Code: wire.CodeUnauthorized, Detail: method}
	}
	return sessions.Handle(ctx, method, params)
}
