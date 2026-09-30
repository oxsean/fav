// Package server is mode 2: the coordinator behind HTTP. Nodes dial in on /node, clients on /client (WebSocket; the
// wire protocol runs inside, one frame per line); each connection authenticates with a credential of the team's
// database in the upgrade request: a node token, a personal token, or a browser's session.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/oxsean/fav/internal/auth"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/dial"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Roles a connection comes in as.
const (
	RoleNode   = dial.RoleNode
	RoleClient = dial.RoleClient
)

var tailnet4, tailnet6 = netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")

// CheckListen: without TLS the server listens only on loopback or a tailnet address (Tailscale encrypts the rest),
// unless plain is asked for (a container behind a forwarder, or a proxy that ends TLS).
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

// validMachine is a machine name a node token may take.
var validMachine = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// CheckMachine: name can name a node ("local" is the coordinator's own word for no machine).
func CheckMachine(name string) error {
	if !validMachine.MatchString(name) || name == coord.Local {
		return fmt.Errorf("machine name %q", name)
	}
	return nil
}

type Options struct {
	Home    string
	Coord   *coord.Coord
	Dir     *Directory
	Config  tend.ServerConfig
	Listen  string
	TLSCert string
	TLSKey  string
	Syncer  *Syncer  // nil: no tracker sync
	Push    *PushKey // nil: no Web Push
	Seal    *Sealer  // opens and seals what the database keeps sealed (push devices)
}

// Server serves the coordinator over HTTP.
type Server struct {
	opt     Options
	logins  map[string]*auth.Provider
	mu      sync.Mutex
	conns   map[*wire.Conn]held // live connections
	flows   map[string]flow     // sign-ins in progress, by state
	devices map[string]*deviceAuth
	limit   *limiter
	syncer  *Syncer
}

func New(opt Options) *Server {
	s := &Server{opt: opt, logins: map[string]*auth.Provider{}, conns: map[*wire.Conn]held{}, flows: map[string]flow{},
		devices: map[string]*deviceAuth{}, limit: newLimiter(), syncer: opt.Syncer}
	for _, l := range opt.Config.Logins {
		p, err := auth.New(l)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tend-server:", err)
			continue
		}
		s.logins[p.Name()] = p
	}
	return s
}

func (s *Server) team() *store.Team { return s.opt.Dir.Team() }

// sweep reads the team again and drops the connections whose credential no longer lets them in.
func (s *Server) sweep() {
	if err := s.opt.Dir.Reload(); err != nil {
		return
	}
	for _, name := range s.opt.Dir.NodeNames() {
		s.opt.Coord.Expect(name)
	}
	s.opt.Coord.Reaffirm() // who owns a machine, who is disabled
	s.mu.Lock()
	defer s.mu.Unlock()
	for c, h := range s.conns {
		if !s.opt.Dir.live(h.cred) {
			c.Close()
		} else if h.client { // who the holder is changed (made or no longer an admin): they connect again as that
			if u, ok := s.opt.Dir.owner(h.cred); !ok || principal(u) != h.as {
				c.Close()
			}
		}
	}
}

// held is what a connection came with: its credential and, for a client, who it acts as.
type held struct {
	cred   string
	client bool
	as     coord.Principal
}

// credential is what r signs in with: a bearer header, or for a client the browser's session cookie.
func (s *Server) credential(r *http.Request, role string) (store.Credential, store.User, bool) {
	kinds := []string{store.KindNode}
	if role == RoleClient {
		kinds = []string{store.KindToken, store.KindWeb}
	}
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return s.opt.Dir.find(strings.TrimSpace(bearer), kinds...)
	}
	if c, err := r.Cookie(sessionCookie); err == nil && role == RoleClient {
		return s.opt.Dir.find(c.Value, store.KindWeb)
	}
	return store.Credential{}, store.User{}, false
}

func (s *Server) track(c *wire.Conn, h held) {
	s.mu.Lock()
	s.conns[c] = h
	s.mu.Unlock()
}

func (s *Server) untrack(c *wire.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

const keepalive = dial.Keepalive

func (s *Server) audit(r *http.Request, actor, kind, detail string) {
	ip := ""
	if r != nil {
		ip, _, _ = net.SplitHostPort(r.RemoteAddr)
	}
	s.team().Audit(store.AuditEntry{Actor: actor, Kind: kind, Detail: detail, IP: ip})
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	cred, _, ok := s.credential(r, RoleNode)
	if !ok {
		s.audit(r, "", "refused", "node")
		http.Error(w, wire.CodeUnauthorized, http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: dial.Compression})
	if err != nil {
		return
	}
	ws.SetReadLimit(wire.MaxFrame + 1)
	opt := s.opt.Coord.NodeOptions()
	opt.Keepalive = keepalive
	c := wire.New(websocket.NetConn(r.Context(), ws, websocket.MessageText), opt)
	s.track(c, held{cred: cred.ID})
	defer s.untrack(c)
	s.team().Touch(cred.ID)
	err = s.opt.Coord.Attach(cred.Name, c, func(h remote.Hello) error {
		if h.NodeID == "" {
			return &wire.Error{Code: wire.CodeUnauthorized, Detail: "node identity"}
		}
		if err := s.team().Bind(cred.ID, h.NodeID, h.Hostname); err != nil {
			fmt.Fprintf(os.Stderr, "node %s refused: %v (tend-server token rebind %s)\n", cred.Name, err, cred.Name)
			s.audit(r, cred.Owner, "refused", "node "+cred.Name+": "+err.Error())
			return &wire.Error{Code: wire.CodeUnauthorized, Detail: "node identity"}
		}
		return nil
	})
	if err != nil {
		return
	}
	<-c.Done()
}

func (s *Server) handleClient(w http.ResponseWriter, r *http.Request) {
	cred, u, ok := s.credential(r, RoleClient)
	if !ok {
		http.Error(w, wire.CodeUnauthorized, http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: dial.Compression})
	if err != nil {
		return
	}
	ws.SetReadLimit(wire.MaxFrame + 1)
	s.team().Touch(cred.ID)
	c := wire.New(websocket.NetConn(r.Context(), ws, websocket.MessageText), wire.Options{Handler: s.audited(r, u, s.opt.Coord.HandlerFor(principal(u))), Bulk: coord.Bulk, Keepalive: keepalive})
	s.track(c, held{cred: cred.ID, client: true, as: principal(u)})
	defer s.untrack(c)
	<-c.Done()
}

// audited records what the security log keeps of a client's calls: refusals, shares and permission answers.
func (s *Server) audited(r *http.Request, u store.User, h wire.Handler) wire.Handler {
	return func(ctx context.Context, req *wire.Request) (any, error) {
		res, err := h(ctx, req)
		switch {
		case wire.Code(err) == wire.CodeUnauthorized:
			s.audit(r, u.ID, "denied", req.Method+" "+subjectOf(req.Params))
		case err == nil && (req.Method == coord.MMachineShare || req.Method == coord.MRunAnswer || req.Method == coord.MProjectMember):
			s.audit(r, u.ID, req.Method, subjectOf(req.Params))
		}
		return res, err
	}
}

// subjectOf names what a call is about by its ids alone: the security log keeps no message or answer text.
func subjectOf(params json.RawMessage) string {
	var p struct {
		ID, Task, Run, Project, Machine, User, Role, Request string
		Allow                                                *bool
		Users, Projects                                      []string
	}
	json.Unmarshal(params, &p)
	var parts []string
	for _, kv := range [][2]string{{"id", p.ID}, {"task", p.Task}, {"run", p.Run}, {"project", p.Project}, {"machine", p.Machine},
		{"user", p.User}, {"role", p.Role}, {"request", p.Request}} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+"="+kv[1])
		}
	}
	if p.Allow != nil {
		parts = append(parts, fmt.Sprintf("allow=%t", *p.Allow))
	}
	if len(p.Users)+len(p.Projects) > 0 {
		parts = append(parts, "users="+strings.Join(p.Users, ","), "projects="+strings.Join(p.Projects, ","))
	}
	return strings.Join(parts, " ")
}

// Handler is the server's HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/node", s.handleNode)
	mux.HandleFunc("/client", s.handleClient)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	s.webRoutes(mux)
	s.apiRoutes(mux)
	s.trackerRoutes(mux)
	mux.Handle("/", page())
	return mux
}

// Serve listens until ctx ends; credentials revoked meanwhile lose their connections within a few seconds.
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
				s.sweep()
				s.expireFlows()
				s.expireDevices()
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

// legacyToken is a token of tokens.json, kept by servers before the team's database.
type legacyToken struct {
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Sum       string    `json:"sum"`
	Created   time.Time `json:"created"`
	Bound     string    `json:"bound,omitempty"`
	BoundHost string    `json:"bound_host,omitempty"`
}

// ImportTokens moves home's tokens.json into the team's database, owned by the server host, and keeps the file
// renamed beside; nothing when there is none.
func ImportTokens(home string, team *store.Team) (int, error) {
	p := filepath.Join(home, "server", "tokens.json")
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var f struct {
		Tokens []legacyToken `json:"tokens"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return 0, err
	}
	n := 0
	for _, t := range f.Tokens {
		kind := store.KindToken
		if t.Role == RoleNode {
			kind = store.KindNode
		}
		_, err := team.ImportCredential(store.Credential{Kind: kind, Name: t.Name, Owner: store.LocalUser, Sum: t.Sum,
			NodeID: t.Bound, Host: t.BoundHost, Created: t.Created}, 0)
		if err != nil && !errors.Is(err, store.ErrExists) {
			return n, err
		}
		n++
	}
	return n, os.Rename(p, fmt.Sprintf("%s.imported-%d", p, time.Now().Unix()))
}
