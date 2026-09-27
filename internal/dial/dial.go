// Package dial is the client side of mode 2: nodes, TUIs and CLIs reach a tend server over WebSocket with a token.
// It is the only part of mode 2 that tend itself carries; the server lives in tend-server.
package dial

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/wire"
)

// Roles a token grants; each dials its own path.
const (
	RoleNode   = "node"
	RoleClient = "client"
)

// Compression is permessage-deflate with one window per connection: frames repeat their keys and ids.
const Compression = websocket.CompressionContextTakeover

// Keepalive on both kinds of connection: a node or client that went away is noticed within two of these.
const Keepalive = 30 * time.Second

// Dial opens a connection to the server at url (http(s):// or ws(s)://) as role with token.
func Dial(ctx context.Context, url, role, token string, opt wire.Options) (*wire.Conn, error) {
	u := strings.TrimRight(url, "/")
	u = strings.Replace(strings.Replace(u, "http://", "ws://", 1), "https://", "wss://", 1)
	ws, resp, err := websocket.Dial(ctx, u+"/"+role, &websocket.DialOptions{
		HTTPHeader:      http.Header{"Authorization": {"Bearer " + token}},
		CompressionMode: Compression,
	})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, &wire.Error{Code: wire.CodeUnauthorized, Detail: url}
		}
		return nil, &wire.Error{Code: wire.CodeOffline, Detail: err.Error()}
	}
	ws.SetReadLimit(wire.MaxFrame + 1)
	if opt.Keepalive == 0 {
		opt.Keepalive = Keepalive
	}
	return wire.New(websocket.NetConn(context.Background(), ws, websocket.MessageText), opt), nil
}

// ReadToken reads a token file: the token is its first non-empty line.
func ReadToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for l := range strings.Lines(string(b)) {
		if t := strings.TrimSpace(l); t != "" {
			return t, nil
		}
	}
	return "", fmt.Errorf("%s holds no token", path)
}

// Connect is a client of the server that config.coordinator names.
func Connect(url, tokenFile string, opt wire.Options) (*coord.Client, error) {
	token, err := ReadToken(tokenFile)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := Dial(ctx, url, RoleClient, token, opt)
	if err != nil {
		return nil, err
	}
	return &coord.Client{Conn: c}, nil
}
