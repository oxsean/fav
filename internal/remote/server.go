package remote

import (
	"context"
	"encoding/json"
	"io"

	"github.com/oxsean/fav/internal/wire"
)

// Handler answers one request; an error that is not a *wire.Error goes back as internal.
type Handler interface {
	Handle(ctx context.Context, method string, params json.RawMessage) (any, error)
}

// Wire is h as a wire handler.
func Wire(h Handler) wire.Handler {
	return func(ctx context.Context, r *wire.Request) (any, error) { return h.Handle(ctx, r.Method, r.Params) }
}

// Serve answers requests on rw until the other end hangs up.
func Serve(rw io.ReadWriteCloser, h Handler) {
	<-wire.New(rw, wire.Options{Handler: Wire(h)}).Done()
}

// Pipe is a Client served by h in this process.
func Pipe(h Handler) *Client {
	c, _ := wire.Pipe(wire.Options{}, wire.Options{Handler: Wire(h)})
	return &Client{conn: c}
}
