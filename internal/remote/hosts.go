package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Hosts reaches other machines through a Transport and keeps their lists: on disk, or in memory for a machine whose
// lists the transport may not write. Safe for concurrent use.
type Hosts struct {
	t       Transport
	mu      sync.Mutex
	putMu   sync.Mutex
	files   map[string]string // host + session key → the transcript's fileio.ID as last read from the end
	mem     map[string]cache  // the lists of machines not kept on disk
	memLive map[string]liveCache
}

// NewHosts: lang is sent in hello, so check texts come back in the caller's language.
func NewHosts(hosts []tend.Host, lang string) *Hosts { return NewHostsDial(hosts, lang, Dial) }

// NewHostsDial reaches hosts through dial (tests: Pipe to a Handler).
func NewHostsDial(hosts []tend.Host, lang string, dial func(tend.Host) (*Client, error)) *Hosts {
	return NewHostsOver(&sshTransport{hosts: hosts, lang: lang, clients: map[string]*Client{}, hellos: map[string]Hello{},
		dialing: map[string]chan struct{}{}, dial: dial})
}

// NewHostsOver reaches machines through t.
func NewHostsOver(t Transport) *Hosts {
	return &Hosts{t: t, files: map[string]string{}, mem: map[string]cache{}, memLive: map[string]liveCache{}}
}

func (h *Hosts) Names() []string {
	if h == nil {
		return nil
	}
	return h.t.Machines()
}

// Host is name's ssh configuration; a machine reached through a coordinator has none.
func (h *Hosts) Host(name string) (tend.Host, bool) {
	if h != nil {
		if s, ok := h.t.(*sshTransport); ok {
			return s.Host(name)
		}
	}
	return tend.Host{}, false
}

// file is the file id known for key; a non-empty set replaces it ("-": the one known turned out stale).
func (h *Hosts) file(key, set string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	old := h.files[key]
	if set != "" {
		h.files[key] = set
	}
	return old
}

// Stale: err says the transcript was rewritten; what was read of it no longer lines up.
func Stale(err error) bool {
	var e *wire.Error
	return errors.As(err, &e) && e.Code == wire.CodeStale
}

// Call runs one request on name.
func (h *Hosts) Call(ctx context.Context, name, method string, params, out any) error {
	return h.t.Call(ctx, name, method, params, out)
}

// Hello is what name said when it was last reached.
func (h *Hosts) Hello(ctx context.Context, name string) (Hello, error) { return h.t.Hello(ctx, name) }

// State says where a host's list came from: At is when it was fetched; Err is set when this fetch failed and the
// list is the cached one (or none).
type State struct {
	At      time.Time
	Err     error
	Version string // the tend that answered
}

// Sessions fetches name's list and caches it; when name cannot answer it returns the cached list with the error.
func (h *Hosts) Sessions(ctx context.Context, name string) ([]*tend.Rec, State) {
	var l List
	hello, err := h.t.Hello(ctx, name)
	if err == nil {
		err = h.Call(ctx, name, MList, nil, &l)
	}
	if err != nil {
		recs, st := h.Cached(name)
		st.Err = err
		c := h.load(name)
		c.Tried, c.Failed = time.Now(), wire.CodeClosed
		if e := (*wire.Error)(nil); errors.As(err, &e) {
			c.Failed = e.Code
		}
		h.save(name, c)
		return recs, st
	}
	now := time.Now()
	v := hello.Version
	h.save(name, cache{At: now, Version: v, Sessions: l.Sessions})
	return recs(name, l.Sessions), State{At: now, Version: v}
}

// Put writes p to the session ref on name and returns the row as name wrote it, which also replaces the cached one.
// expect is the record's updated_at the editor saw: name answers stale when it has been written since. A tend whose
// hello lists no put is not sent it: unknown_method, with its version as the detail.
func (h *Hosts) Put(ctx context.Context, name string, ref Ref, p tend.Patch, expect *time.Time) (*tend.Rec, error) {
	hello, err := h.t.Hello(ctx, name)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(hello.Methods, MPut) {
		return nil, &wire.Error{Code: wire.CodeUnknownMethod, Detail: hello.Version}
	}
	var row Row
	if err := h.Call(ctx, name, MPut, PutParams{Ref: ref, Patch: p, Expect: expect}, &row); err != nil {
		return nil, err
	}
	h.putMu.Lock() // one rewrite of a cached list at a time
	defer h.putMu.Unlock()
	c := h.load(name)
	if c.At.IsZero() { // no list kept: the next fetch brings the whole one
		return row.Session.Rec(name), nil
	}
	if i := slices.IndexFunc(c.Sessions, func(s Session) bool { return s.Provider == row.Provider && s.SessionID == row.SessionID }); i >= 0 {
		c.Sessions[i] = row.Session
	} else {
		c.Sessions = append(c.Sessions, row.Session)
	}
	h.save(name, c)
	return row.Session.Rec(name), nil
}

// Cached is name's list from the last fetch, without reaching it.
func (h *Hosts) Cached(name string) ([]*tend.Rec, State) {
	c := h.load(name)
	return recs(name, c.Sessions), State{At: c.At, Version: c.Version}
}

// CacheDir holds what tend keeps about name: its last list and who ran there.
func CacheDir(name string) string { return filepath.Dir(cachePath(name)) }

func ForgetCache(name string) error { return os.RemoveAll(CacheDir(name)) }

// Failed: when the last fetch of name failed and why; nil when the last one worked.
func (h *Hosts) Failed(name string) (time.Time, error) {
	if c := h.load(name); c.Failed != "" {
		return c.Tried, &wire.Error{Code: c.Failed}
	}
	return time.Time{}, nil
}

func (h *Hosts) load(name string) cache {
	var c cache
	if !h.t.Keep(name) {
		h.mu.Lock()
		c = h.mem[name]
		h.mu.Unlock()
		if c.Target != h.target(name) {
			return cache{}
		}
		return c
	}
	b, err := os.ReadFile(cachePath(name))
	if err != nil || json.Unmarshal(b, &c) != nil || c.Target != h.target(name) { // the host now points somewhere else
		return cache{}
	}
	return c
}

// Live is who runs on name now, by session id.
func (h *Hosts) Live(ctx context.Context, name string) (map[string]capture.Live, error) {
	var l Live
	if err := h.Call(ctx, name, MLive, nil, &l); err != nil {
		return nil, err
	}
	for k, v := range l.Live {
		v.Since = v.Since.Local()
		l.Live[k] = v
	}
	lc := liveCache{At: time.Now(), Target: h.target(name), Live: l.Live}
	if !h.t.Keep(name) {
		h.mu.Lock()
		h.memLive[name] = lc
		h.mu.Unlock()
	} else if b, err := json.Marshal(lc); err == nil {
		fileio.WriteFile(filepath.Join(filepath.Dir(cachePath(name)), "live.json"), b, 0o600)
	}
	return l.Live, nil
}

// CachedLive is who ran on name at the last Live, and when that was.
func (h *Hosts) CachedLive(name string) (map[string]capture.Live, time.Time) {
	var c liveCache
	if !h.t.Keep(name) {
		h.mu.Lock()
		c = h.memLive[name]
		h.mu.Unlock()
	} else if b, err := os.ReadFile(filepath.Join(filepath.Dir(cachePath(name)), "live.json")); err != nil || json.Unmarshal(b, &c) != nil {
		return nil, time.Time{}
	}
	if c.Target != h.target(name) {
		return nil, time.Time{}
	}
	return c.Live, c.At
}

type liveCache struct {
	At     time.Time               `json:"at"`
	Target string                  `json:"target"`
	Live   map[string]capture.Live `json:"live"`
}

// Source reads r where it lives.
func (h *Hosts) Source(r *tend.Rec) Source {
	if r.Host == "" || h == nil {
		return Local(r)
	}
	return far{h, Ref{r.Provider, r.SessionID}, r.Host}
}

// ResumeCommand resumes r on its host in this terminal: `ssh -t <host> tend resume --terminal --no-herdr <sid>`.
func (h *Hosts) ResumeCommand(r *tend.Rec) (*exec.Cmd, bool) {
	host, ok := h.Host(r.Host)
	if !ok || r.SessionID == "" || unsafeName.MatchString(r.SessionID) { // ⚠️ the one value that reaches the remote shell
		return nil, false
	}
	return Command(host, true, "resume", "--terminal", "--no-herdr", r.SessionID), true
}

func (h *Hosts) Close() {
	if h == nil {
		return
	}
	h.t.Close()
}

var reasons = map[string]string{
	wire.CodeOffline: "remote.err.offline", wire.CodeAuth: "remote.err.auth", wire.CodeHostKey: "remote.err.hostkey",
	wire.CodeProto: "remote.err.proto", wire.CodeTimeout: "remote.err.timeout", wire.CodeClosed: "remote.err.closed",
	wire.CodeNotFound: "remote.err.not_found", wire.CodeNoTend: "remote.err.no_tend", wire.CodeStale: "remote.err.stale",
	wire.CodeConflict: "remote.err.conflict", wire.CodeUnauthorized: "remote.err.unauthorized",
	wire.CodeBadRequest: "remote.err.bad_request", wire.CodeBusy: "remote.err.busy", wire.CodeCanceled: "remote.err.canceled",
	wire.CodeUnknownMethod: "remote.err.unknown_method",
}

// Reason is err as a short phrase for the UI.
func Reason(err error) string {
	var e *wire.Error
	if errors.As(err, &e) {
		if k, ok := reasons[e.Code]; ok {
			return i18n.T(k)
		}
	}
	return i18n.T("remote.err.other")
}

type cache struct {
	At       time.Time `json:"at"`
	Target   string    `json:"target"` // what it was fetched through (Transport.Target)
	Version  string    `json:"version,omitempty"`
	Sessions []Session `json:"sessions"`
	Tried    time.Time `json:"tried,omitzero"`   // the last fetch, when it failed
	Failed   string    `json:"failed,omitempty"` // its error code
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// cachePath: ~/.agent/tend/hosts/<name>-<hash>/sessions.json, list fields only (Session), mode 0600; the hash keeps
// names that differ only in other characters apart.
func cachePath(name string) string {
	sum := sha256.Sum256([]byte(name))
	dir := unsafeName.ReplaceAllString(name, "_") + "-" + hex.EncodeToString(sum[:4])
	return filepath.Join(tend.Home(), "hosts", dir, "sessions.json")
}

func (h *Hosts) target(name string) string { return h.t.Target(name) }

func (h *Hosts) save(name string, c cache) {
	c.Target = h.target(name)
	if !h.t.Keep(name) {
		h.mu.Lock()
		h.mem[name] = c
		h.mu.Unlock()
		return
	}
	b, err := json.Marshal(c)
	if err == nil {
		fileio.WriteFile(cachePath(name), b, 0o600)
	}
}

func recs(host string, ss []Session) []*tend.Rec {
	out := make([]*tend.Rec, len(ss))
	for i, s := range ss {
		out[i] = s.Rec(host)
	}
	return out
}
