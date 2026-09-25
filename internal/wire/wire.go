// Package wire is tend's protocol between clients, the coordinator and nodes: one JSON frame per line over any
// two-way byte stream. Either end may send requests once connected; answers are matched by id and may come in any
// order; a caller that stops waiting cancels the request on the other end.
package wire

import (
	"encoding/json"
	"errors"
)

// Proto changes whenever a frame changes shape; both ends must agree. Methods are not versioned by it: hello lists
// them, and a method's params or result only gain fields an older end ignores.
const Proto = 2

// Frame kinds.
const (
	KindReq    = "req"
	KindRes    = "res"
	KindPush   = "push"
	KindCancel = "cancel"
)

// MaxFrame is the longest line a Conn reads; a longer one ends the connection.
const MaxFrame = 16 << 20

type Frame struct {
	Kind      string          `json:"kind"`
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	CommandID string          `json:"command_id,omitempty"` // a write's idempotency key: replaying it returns the first result
	Params    json.RawMessage `json:"params,omitempty"`
	OK        bool            `json:"ok,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *Error          `json:"error,omitempty"`
}

// Error carries a stable code, never localized text: the two ends may use different languages. Detail is for logs.
type Error struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

// Error codes. The first group comes from the other end, the second from reaching it.
const (
	CodeBadRequest    = "bad_request"
	CodeUnknownMethod = "unknown_method"
	CodeProto         = "proto" // the other end speaks another Proto: update it
	CodeNotFound      = "not_found"
	CodeStale         = "stale" // the transcript was rewritten since the offsets asked about were read
	CodeConflict      = "conflict"
	CodeUnauthorized  = "unauthorized"
	CodeBusy          = "busy"
	CodeCanceled      = "canceled"
	CodeInternal      = "internal"

	CodeOffline = "offline" // ssh could not connect
	CodeAuth    = "auth"    // ssh refused the key
	CodeHostKey = "hostkey" // the host key changed or is unknown
	CodeTimeout = "timeout"
	CodeClosed  = "closed"  // the connection or the process behind it ended
	CodeNoTend  = "no_tend" // the remote shell could not find the tend command
)

// Code is err's code, "" when err is not an *Error.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// MPing is answered by every Conn itself; keepalives use it.
const MPing = "ping"
