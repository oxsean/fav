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
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/i18n"
)

// Hosts reaches the configured machines: one client per host, dialed on first use and again after it fails.
// Safe for concurrent use; calls to one host run one at a time.
type Hosts struct {
	mu      sync.Mutex
	hosts   []fav.Host
	lang    string
	clients map[string]*Client
	hellos  map[string]Hello
	dialing map[string]chan struct{} // one dial per host at a time
	dial    func(fav.Host) (*Client, error)
	closed  bool
	files   map[string]string // host + session key → the transcript's fileio.ID as last read from the end
}

// NewHosts: lang is sent in hello, so check texts come back in the caller's language.
func NewHosts(hosts []fav.Host, lang string) *Hosts { return NewHostsDial(hosts, lang, Dial) }

// NewHostsDial reaches hosts through dial (tests: Pipe to a Handler).
func NewHostsDial(hosts []fav.Host, lang string, dial func(fav.Host) (*Client, error)) *Hosts {
	return &Hosts{hosts: hosts, lang: lang, clients: map[string]*Client{}, hellos: map[string]Hello{},
		dialing: map[string]chan struct{}{}, dial: dial, files: map[string]string{}}
}

func (h *Hosts) Names() []string {
	if h == nil {
		return nil
	}
	out := make([]string, len(h.hosts))
	for i, x := range h.hosts {
		out[i] = x.Name
	}
	return out
}

func (h *Hosts) Host(name string) (fav.Host, bool) {
	if h != nil {
		for _, x := range h.hosts {
			if x.Name == name {
				return x, true
			}
		}
	}
	return fav.Host{}, false
}

// client dials name if it has no working client, and checks its protocol with hello.
func (h *Hosts) client(ctx context.Context, name string) (*Client, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, &Error{Code: CodeClosed}
	}
	d := h.dialing[name]
	if d == nil {
		d = make(chan struct{}, 1)
		h.dialing[name] = d
	}
	h.mu.Unlock()
	select {
	case d <- struct{}{}:
	case <-ctx.Done():
		return nil, &Error{Code: CodeTimeout}
	}
	defer func() { <-d }()
	h.mu.Lock()
	c, closed := h.clients[name], h.closed
	h.mu.Unlock()
	if closed { // closed while this call waited for another one's dial
		return nil, &Error{Code: CodeClosed}
	}
	if c != nil && c.Err() == nil {
		return c, nil
	}
	host, ok := h.Host(name)
	if !ok {
		return nil, &Error{Code: CodeNotFound, Detail: name}
	}
	c, err := h.dial(host)
	if err != nil {
		return nil, err
	}
	var hello Hello
	if err := c.Call(ctx, MHello, HelloParams{Lang: h.lang}, &hello); err != nil {
		c.Close()
		return nil, err
	}
	if hello.Proto != Proto {
		c.Close()
		return nil, &Error{Code: CodeProto, Detail: hello.Version}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed { // Close ran while this one was dialing
		c.Close()
		return nil, &Error{Code: CodeClosed}
	}
	h.clients[name], h.hellos[name] = c, hello
	return c, nil
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
	var e *Error
	return errors.As(err, &e) && e.Code == CodeStale
}

// Call runs one request on name.
func (h *Hosts) Call(ctx context.Context, name, method string, params, out any) error {
	c, err := h.client(ctx, name)
	if err != nil {
		return err
	}
	return c.Call(ctx, method, params, out)
}

// Hello is what name said when it was last dialed.
func (h *Hosts) Hello(ctx context.Context, name string) (Hello, error) {
	if _, err := h.client(ctx, name); err != nil {
		return Hello{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hellos[name], nil
}

// State says where a host's list came from: At is when it was fetched; Err is set when this fetch failed and the
// list is the cached one (or none).
type State struct {
	At      time.Time
	Err     error
	Version string // the fav that answered
}

// Sessions fetches name's list and caches it; when name cannot answer it returns the cached list with the error.
func (h *Hosts) Sessions(ctx context.Context, name string) ([]*fav.Rec, State) {
	var l List
	if err := h.Call(ctx, name, MList, nil, &l); err != nil {
		recs, st := h.Cached(name)
		st.Err = err
		c := h.load(name)
		c.Tried, c.Failed = time.Now(), CodeClosed
		if e := (*Error)(nil); errors.As(err, &e) {
			c.Failed = e.Code
		}
		h.save(name, c)
		return recs, st
	}
	now := time.Now()
	h.mu.Lock()
	v := h.hellos[name].Version
	h.mu.Unlock()
	h.save(name, cache{At: now, Version: v, Sessions: l.Sessions})
	return recs(name, l.Sessions), State{At: now, Version: v}
}

// Cached is name's list from the last fetch, without reaching it.
func (h *Hosts) Cached(name string) ([]*fav.Rec, State) {
	c := h.load(name)
	return recs(name, c.Sessions), State{At: c.At, Version: c.Version}
}

// CacheDir holds what fav keeps about name: its last list and who ran there.
func CacheDir(name string) string { return filepath.Dir(cachePath(name)) }

func ForgetCache(name string) error { return os.RemoveAll(CacheDir(name)) }

// Failed: when the last fetch of name failed and why; nil when the last one worked.
func (h *Hosts) Failed(name string) (time.Time, error) {
	if c := h.load(name); c.Failed != "" {
		return c.Tried, &Error{Code: c.Failed}
	}
	return time.Time{}, nil
}

func (h *Hosts) load(name string) cache {
	var c cache
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
	if b, err := json.Marshal(liveCache{At: time.Now(), Target: h.target(name), Live: l.Live}); err == nil {
		fileio.WriteFile(filepath.Join(filepath.Dir(cachePath(name)), "live.json"), b, 0o600)
	}
	return l.Live, nil
}

// CachedLive is who ran on name at the last Live, and when that was.
func (h *Hosts) CachedLive(name string) (map[string]capture.Live, time.Time) {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(cachePath(name)), "live.json"))
	if err != nil {
		return nil, time.Time{}
	}
	var c liveCache
	if json.Unmarshal(b, &c) != nil || c.Target != h.target(name) {
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
func (h *Hosts) Source(r *fav.Rec) Source {
	if r.Host == "" || h == nil {
		return Local(r)
	}
	return far{h, Ref{r.Provider, r.SessionID}, r.Host}
}

// ResumeCommand resumes r on its host in this terminal: `ssh -t <host> fav resume --terminal --no-herdr <sid>`.
func (h *Hosts) ResumeCommand(r *fav.Rec) (*exec.Cmd, bool) {
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
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for _, c := range h.clients {
		c.Close()
	}
	clear(h.clients)
}

var reasons = map[string]string{
	CodeOffline: "remote.err.offline", CodeAuth: "remote.err.auth", CodeHostKey: "remote.err.hostkey",
	CodeProto: "remote.err.proto", CodeTimeout: "remote.err.timeout", CodeClosed: "remote.err.closed",
	CodeNotFound: "remote.err.not_found", CodeNoFav: "remote.err.no_fav", CodeStale: "remote.err.stale",
}

// Reason is err as a short phrase for the UI.
func Reason(err error) string {
	var e *Error
	if errors.As(err, &e) {
		if k, ok := reasons[e.Code]; ok {
			return i18n.T(k)
		}
	}
	return i18n.T("remote.err.other")
}

type cache struct {
	At       time.Time `json:"at"`
	Target   string    `json:"target"` // the ssh alias and fav command it was fetched through
	Version  string    `json:"version,omitempty"`
	Sessions []Session `json:"sessions"`
	Tried    time.Time `json:"tried,omitzero"`   // the last fetch, when it failed
	Failed   string    `json:"failed,omitempty"` // its error code
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// cachePath: ~/.agent/fav/hosts/<name>-<hash>/sessions.json, list fields only (Session), mode 0600; the hash keeps
// names that differ only in other characters apart.
func cachePath(name string) string {
	sum := sha256.Sum256([]byte(name))
	dir := unsafeName.ReplaceAllString(name, "_") + "-" + hex.EncodeToString(sum[:4])
	return filepath.Join(fav.Home(), "hosts", dir, "sessions.json")
}

func (h *Hosts) target(name string) string {
	host, _ := h.Host(name)
	return strings.Join(append([]string{host.SSH}, host.Fav...), "\x00")
}

func (h *Hosts) save(name string, c cache) {
	c.Target = h.target(name)
	b, err := json.Marshal(c)
	if err == nil {
		fileio.WriteFile(cachePath(name), b, 0o600)
	}
}

func recs(host string, ss []Session) []*fav.Rec {
	out := make([]*fav.Rec, len(ss))
	for i, s := range ss {
		out[i] = s.Rec(host)
	}
	return out
}
