// Package wire is tend's protocol between clients, the coordinator and nodes: one JSON frame per line over any
// two-way byte stream. Either end may send requests once connected; answers are matched by id and may come in any
// order; a caller that stops waiting cancels the request on the other end. A request may open a stream: pushes that
// carry its id until one res ends it.
package wire

import (
	"encoding/json"
	"errors"
)

// Proto changes whenever a frame changes shape; both ends must agree. Methods are not versioned by it: hello lists
// them, and a method's params or result only gain fields an older end ignores.
const Proto = 2

// Frame types. A res carries result, or error when the call failed.
const (
	TypeReq    = "req"
	TypeRes    = "res"
	TypePush   = "push"
	TypeCancel = "cancel"
)

// MaxFrame is the longest line a Conn reads; a longer one ends the connection.
const MaxFrame = 16 << 20

type Frame struct {
	Type      string          `json:"type"`
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	CommandID string          `json:"command_id,omitempty"` // a write's idempotency key: replaying it returns the first result
	Params    json.RawMessage `json:"params,omitempty"`
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
	CodeLagged        = "lagged"      // a stream fell behind: open it again from its cursor
	CodeGone          = "gone"        // what a stream follows is no more, or its machine went away
	CodeUnsupported   = "unsupported" // the other end has no such stream

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

// PushOpen is a stream's first push: where it starts from.
const PushOpen = "open"

// Open modes: go on from the cursor, everything anew, or from after a stretch that is lost (From to To).
const (
	ModeResume   = "resume"
	ModeSnapshot = "snapshot"
	ModeGap      = "gap"
)

type Open struct {
	Cursor any    `json:"cursor,omitempty"`
	Mode   string `json:"mode"`
	From   any    `json:"from,omitempty"`
	To     any    `json:"to,omitempty"`
}

// Ended is the result of a stream its provider finished.
type Ended struct {
	Reason string `json:"reason"`
}

var EndDone = Ended{Reason: "done"}
