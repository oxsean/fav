package remote

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"

	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/wire"
)

// Peer is one end of a handoff or migration: a machine as its hello describes it and how to call it.
type Peer struct {
	Name  string // as the caller names it; "" for this machine
	Hello Hello
	call  func(ctx context.Context, method string, params, out any) error
}

// PeerOf is the machine name whose hello is hello, reached through call.
func PeerOf(name string, hello Hello, call func(ctx context.Context, method string, params, out any) error) Peer {
	return Peer{Name: name, Hello: hello, call: call}
}

// Here is this machine answered in this process, past any share_sessions gate: its files are this user's anyway.
func Here(version string) Peer { return PeerOf("", LocalHello(version), InProcess(NewLocal(version))) }

// InProcess calls h as a connection would: params and the answer go through JSON.
func InProcess(h Handler) func(ctx context.Context, method string, params, out any) error {
	return func(ctx context.Context, method string, params, out any) error {
		var raw json.RawMessage
		if params != nil {
			b, err := json.Marshal(params)
			if err != nil {
				return &wire.Error{Code: wire.CodeBadRequest, Detail: err.Error()}
			}
			raw = b
		}
		res, err := h.Handle(ctx, method, raw)
		if err != nil {
			return err
		}
		if out == nil {
			return nil
		}
		b, err := json.Marshal(res)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
}

// Peer is name reached through h's transport.
func (h *Hosts) Peer(ctx context.Context, name string) (Peer, error) {
	hello, err := h.t.Hello(ctx, name)
	if err != nil {
		return Peer{}, err
	}
	return PeerOf(name, hello, func(ctx context.Context, method string, params, out any) error {
		return h.Call(ctx, name, method, params, out)
	}), nil
}

// Has: p's hello lists method.
func (p Peer) Has(method string) bool { return slices.Contains(p.Hello.Methods, method) }

// Call runs method on p; a tend whose hello lacks it is not sent it: unknown_method, with its version as the detail.
func (p Peer) Call(ctx context.Context, method string, params, out any) error {
	if !p.Has(method) {
		return &wire.Error{Code: wire.CodeUnknownMethod, Detail: p.Hello.Version}
	}
	return p.call(ctx, method, params, out)
}

// End is p as a path mapping needs it.
func (p Peer) End() pathmap.End { return p.Hello.End() }

// End is the machine that greets with h as a path mapping needs it.
func (h Hello) End() pathmap.End {
	return pathmap.End{OS: h.OS, Home: h.Home, Host: h.Hostname, WSL: h.WSL != ""}
}

// Label is p as the caller names it, else as it names itself.
func (p Peer) Label() string { return cmp.Or(p.Name, p.Hello.Hostname, p.Hello.Endpoint) }

// Ref names p for the other end: as the caller names it, this machine by its host name.
func (p Peer) Ref() PeerRef {
	e := p.End()
	return PeerRef{Name: p.Label(), Endpoint: p.Hello.Endpoint, NodeID: p.Hello.NodeID, End: End{OS: e.OS, Home: e.Home, Host: e.Host, WSL: e.WSL}}
}

// Same: p and q are one machine.
func (p Peer) Same(q Peer) bool {
	return p.Hello.Endpoint != "" && p.Hello.Endpoint == q.Hello.Endpoint
}
