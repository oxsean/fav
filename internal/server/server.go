// Package server is mode 2: the coordinator behind HTTP. Nodes dial in on /node, clients on /client (WebSocket; the
// wire protocol runs inside, one frame per line); each connection authenticates with a token in the upgrade request.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/wire"
)

// Roles a token grants.
const (
	RoleNode   = "node"
	RoleClient = "client"
)

type Token struct {
	Name    string    `json:"name"`
	Role    string    `json:"role"`
	Sum     string    `json:"sum"` // sha256 of the token: the token itself is shown once and never stored
	Created time.Time `json:"created"`
}

type tokenFile struct {
	Tokens []Token `json:"tokens"`
}

func tokensPath(home string) string { return filepath.Join(home, "server", "tokens.json") }

// Tokens are home's tokens.
func Tokens(home string) ([]Token, error) {
	b, err := os.ReadFile(tokensPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f tokenFile
	err = json.Unmarshal(b, &f)
	return f.Tokens, err
}

func saveTokens(home string, ts []Token) error {
	if err := os.MkdirAll(filepath.Dir(tokensPath(home)), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(tokenFile{Tokens: ts}, "", "  ")
	return fileio.WriteFile(tokensPath(home), append(b, '\n'), 0o600)
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ErrExists: a token of that name is there already.
var ErrExists = errors.New("exists")

// AddToken makes a token for name in role and returns it; only its hash is kept.
func AddToken(home, role, name string) (string, error) {
	if !validName.MatchString(name) || name == coord.Local {
		return "", fmt.Errorf("name %q", name)
	}
	if role != RoleNode && role != RoleClient {
		return "", fmt.Errorf("role %q", role)
	}
	unlock, err := lockTokens(home)
	if err != nil {
		return "", err
	}
	defer unlock()
	ts, err := Tokens(home)
	if err != nil {
		return "", err
	}
	if slices.ContainsFunc(ts, func(t Token) bool { return t.Name == name }) {
		return "", ErrExists
	}
	var b [24]byte
	rand.Read(b[:])
	token := "tend_" + hex.EncodeToString(b[:])
	ts = append(ts, Token{Name: name, Role: role, Sum: sum(token), Created: time.Now().UTC()})
	return token, saveTokens(home, ts)
}

// RemoveToken revokes name's token; a running server drops its connections.
func RemoveToken(home, name string) error {
	unlock, err := lockTokens(home)
	if err != nil {
		return err
	}
	defer unlock()
	ts, err := Tokens(home)
	if err != nil {
		return err
	}
	n := len(ts)
	ts = slices.DeleteFunc(ts, func(t Token) bool { return t.Name == name })
	if len(ts) == n {
		return os.ErrNotExist
	}
	return saveTokens(home, ts)
}

func sum(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// NodeNames are the names of home's node tokens.
func NodeNames(home string) []string {
	ts, _ := Tokens(home)
	var out []string
	for _, t := range ts {
		if t.Role == RoleNode {
			out = append(out, t.Name)
		}
	}
	return out
}

var tailnet4, tailnet6 = netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")

// CheckListen: without TLS the server listens only on loopback or a tailnet address (Tailscale encrypts the rest),
// unless plain is asked for (a container behind a forwarder).
func CheckListen(addr string, tls, plain bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if tls || plain {
		return nil
	}
	ip, err := netip.ParseAddr(host)
	if host == "localhost" || err == nil && (ip.IsLoopback() || tailnet4.Contains(ip) || tailnet6.Contains(ip)) {
		return nil
	}
	return fmt.Errorf("%s is neither loopback nor a tailnet address", host)
}

type Options struct {
	Home    string
	Coord   *coord.Coord
	Listen  string
	TLSCert string
	TLSKey  string
}

// Server serves the coordinator over HTTP.
type Server struct {
	opt   Options
	mu    sync.Mutex
	stamp string // tokens.json identity when read
	toks  []Token
	conns map[*wire.Conn]Token // live connections by the token they came with
}

func New(opt Options) *Server { return &Server{opt: opt, conns: map[*wire.Conn]Token{}} }

// tokens reads tokens.json again when it changed and drops the connections of tokens no longer there.
func (s *Server) tokens() []Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id := fileio.ID(tokensPath(s.opt.Home)) + fmt.Sprint(modTime(tokensPath(s.opt.Home))); id != s.stamp {
		if ts, err := Tokens(s.opt.Home); err == nil {
			s.toks, s.stamp = ts, id
		}
	}
	for c, tok := range s.conns { // every time: a connection may have been tracked after its token went
		if !slices.ContainsFunc(s.toks, func(t Token) bool { return t.Sum == tok.Sum && t.Name == tok.Name && t.Role == tok.Role }) {
			c.Close()
		}
	}
	return s.toks
}

// lockTokens serializes changes to tokens.json across processes.
func lockTokens(home string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(tokensPath(home)), 0o700); err != nil {
		return nil, err
	}
	return filelock.Lock(tokensPath(home) + ".lock")
}

func modTime(p string) int64 {
	if fi, err := os.Stat(p); err == nil {
		return fi.ModTime().UnixNano()
	}
	return 0
}

// auth is the token r comes with (a bearer header, or for a client the browser's session cookie), when it has role.
func (s *Server) auth(r *http.Request, role string) (Token, bool) {
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok && role == RoleClient {
		if c, err := r.Cookie(sessionCookie); err == nil {
			bearer, ok = c.Value, true
		}
	}
	if !ok {
		return Token{}, false
	}
	return s.check(strings.TrimSpace(bearer), role)
}

// check is the token whose hash token has, when it has role.
func (s *Server) check(token, role string) (Token, bool) {
	if token == "" {
		return Token{}, false
	}
	want := sum(token)
	for _, t := range s.tokens() {
		if subtle.ConstantTimeCompare([]byte(t.Sum), []byte(want)) == 1 && t.Role == role {
			return t, true
		}
	}
	return Token{}, false
}

func (s *Server) track(c *wire.Conn, t Token) {
	s.mu.Lock()
	s.conns[c] = t
	s.mu.Unlock()
}

func (s *Server) untrack(c *wire.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

// keepalive on both kinds of connection: a node or client that went away is noticed within two of these.
const keepalive = 30 * time.Second

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	t, ok := s.auth(r, RoleNode)
	if !ok {
		http.Error(w, wire.CodeUnauthorized, http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(wire.MaxFrame + 1)
	opt := s.opt.Coord.NodeOptions()
	opt.Keepalive = keepalive
	c := wire.New(websocket.NetConn(r.Context(), ws, websocket.MessageText), opt)
	s.track(c, t)
	defer s.untrack(c)
	if err := s.opt.Coord.Attach(t.Name, c); err != nil {
		return
	}
	<-c.Done()
}

func (s *Server) handleClient(w http.ResponseWriter, r *http.Request) {
	t, ok := s.auth(r, RoleClient)
	if !ok {
		http.Error(w, wire.CodeUnauthorized, http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(wire.MaxFrame + 1)
	c := wire.New(websocket.NetConn(r.Context(), ws, websocket.MessageText), wire.Options{Handler: s.opt.Coord.Handler(), Keepalive: keepalive})
	s.track(c, t)
	defer s.untrack(c)
	<-c.Done()
}

// Handler is the server's HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/node", s.handleNode)
	mux.HandleFunc("/client", s.handleClient)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("/login", s.login)
	mux.HandleFunc("/logout", s.logout)
	mux.HandleFunc("/session", s.session)
	mux.Handle("/", page())
	return mux
}

// Serve listens until ctx ends; tokens revoked meanwhile lose their connections within a few seconds.
func (s *Server) Serve(ctx context.Context) error {
	hs := &http.Server{Addr: s.opt.Listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	l, err := net.Listen("tcp", s.opt.Listen)
	if err != nil {
		return err
	}
	go func() {
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				hs.Shutdown(sctx)
				cancel()
				s.mu.Lock()
				for c := range s.conns {
					c.Close()
				}
				s.mu.Unlock()
				return
			case <-t.C:
				s.tokens()
			}
		}
	}()
	if s.opt.TLSCert != "" {
		err = hs.ServeTLS(l, s.opt.TLSCert, s.opt.TLSKey)
	} else {
		err = hs.Serve(l)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Dial opens a connection to the server at url (http(s):// or ws(s)://) as role with token.
func Dial(ctx context.Context, url, role, token string, opt wire.Options) (*wire.Conn, error) {
	u := strings.TrimRight(url, "/")
	u = strings.Replace(strings.Replace(u, "http://", "ws://", 1), "https://", "wss://", 1)
	ws, resp, err := websocket.Dial(ctx, u+"/"+role, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, &wire.Error{Code: wire.CodeUnauthorized, Detail: url}
		}
		return nil, &wire.Error{Code: wire.CodeOffline, Detail: err.Error()}
	}
	ws.SetReadLimit(wire.MaxFrame + 1)
	if opt.Keepalive == 0 {
		opt.Keepalive = keepalive
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
