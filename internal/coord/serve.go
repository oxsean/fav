package coord

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/wire"
)

// SocketPath is where home's coordinator listens.
func SocketPath(home string) string { return paths.Socket(filepath.Join(home, "coord"), "sock") }

// Serve answers clients on the socket until ctx ends.
func (c *Coord) Serve(ctx context.Context) error {
	p := SocketPath(c.opt.Home)
	os.Remove(p) // this process holds the lock: a socket left there is stale
	l, err := net.Listen("unix", p)
	if err != nil {
		return err
	}
	os.Chmod(p, 0o600)
	go func() {
		<-ctx.Done()
		l.Close()
	}()
	for {
		nc, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		wire.New(nc, wire.Options{Handler: c.Handler(), Bulk: Bulk})
	}
}

// Dial connects to the coordinator running for home.
func Dial(home string, opt wire.Options) (*wire.Conn, error) {
	nc, err := net.DialTimeout("unix", SocketPath(home), 2*time.Second)
	if err != nil {
		return nil, err
	}
	return wire.New(nc, opt), nil
}

// Client is a connection to the coordinator: the running one's socket, or this process having become it.
type Client struct {
	*wire.Conn
	Coord  *Coord // nil: another process is the coordinator
	cancel context.CancelFunc
	wrote  bool // a command went through this client
}

// CallCommand is wire's, noting that this client wrote.
func (cl *Client) CallCommand(ctx context.Context, method, commandID string, params, out any) error {
	cl.wrote = true
	return cl.Conn.CallCommand(ctx, method, commandID, params, out)
}

// lockedWait: the lock is held but the socket does not answer yet (its holder is starting).
const lockedWait = 2 * time.Second

// Connect reaches home's coordinator, or becomes it when no process is.
func Connect(opt Options, wopt wire.Options) (*Client, error) {
	deadline := time.Now().Add(lockedWait)
	for {
		if conn, err := Dial(opt.Home, wopt); err == nil {
			return &Client{Conn: conn}, nil
		}
		c, err := Open(opt)
		if err == nil {
			ctx, cancel := context.WithCancel(context.Background())
			go c.Serve(ctx)
			go c.Run(ctx)
			a, _ := wire.Pipe(wopt, wire.Options{Handler: c.Handler(), Bulk: Bulk})
			return &Client{Conn: a, Coord: c, cancel: cancel}, nil
		}
		if !errors.Is(err, ErrLocked) || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// settleWait bounds the last pass of a coordinator that lives for one command.
const settleWait = 30 * time.Second

// CloseNow ends the connection at once; runs already dispatched keep going and queued ones wait for the next
// coordinator.
func (cl *Client) CloseNow() error {
	if cl.Coord != nil {
		cl.cancel()
		cl.Coord.Close()
	}
	return cl.Conn.Close()
}

// Close ends the connection; a process that became the coordinator and was asked to change something first dispatches
// it (a read-only command leaves at once).
func (cl *Client) Close() error {
	if cl.Coord != nil && !cl.wrote {
		return cl.CloseNow()
	}
	if cl.Coord != nil {
		ctx, cancel := context.WithTimeout(context.Background(), settleWait)
		cl.Coord.Settle(ctx)
		cancel()
		cl.cancel()
		cl.Coord.Close()
	}
	return cl.Conn.Close()
}
