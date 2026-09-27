package coord

import (
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/wire"
)

// Principal is who calls the coordinator: every connection carries one, and every method checks it.
type Principal struct {
	User  string `json:"user"`
	Admin bool   `json:"admin,omitempty"`
}

// Owner is this machine's user: the socket, a process that became the coordinator, and mode 1.
var Owner = Principal{User: "local", Admin: true}

func (p Principal) actor() journal.Actor { return journal.Actor{Kind: journal.ActorUser, ID: p.User} }

// access is what a method needs from its caller.
type access int

const (
	anyone access = iota + 1 // the handshake
	reader                   // reads tasks, runs and machines
	writer                   // changes tasks and runs
	admin                    // reaches a machine's own sessions, beyond any task
)

// methodAccess covers every method a client may call; one missing here is refused.
var methodAccess = map[string]access{
	wire.MPing:    anyone,
	remote.MHello: anyone,
	MStateGet:     reader,
	MTaskGet:      reader,
	MAgentList:    reader,
	MMachineList:  reader,
	MSubscribe:    reader,
	MRunTail:      reader,
	MRunPreview:   reader,
	MTaskCreate:   writer,
	MTaskEdit:     writer,
	MTaskStatus:   writer,
	MRunDispatch:  writer,
	MRunStop:      writer,
	MRunAbandon:   writer,
	MRunAnswer:    writer,
	MRunSend:      writer,
	MRunContinue:  writer, // a run's own session; any other session needs admin (runContinue)
	MNodeCall:     admin,
}

func forbidden(what string) error { return &wire.Error{Code: wire.CodeUnauthorized, Detail: what} }

// may is nil when p can call method.
func (p Principal) may(method string) error {
	a, ok := methodAccess[method]
	switch {
	case !ok:
		return &wire.Error{Code: wire.CodeUnknownMethod, Detail: method}
	case a == anyone:
		return nil
	case p.User == "":
		return forbidden(method)
	case a == admin && !p.Admin:
		return forbidden(method)
	}
	return nil
}
