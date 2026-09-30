package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// Egress is how the server reaches addresses someone chose (webhooks, push services, trackers): it connects only to
// public addresses, checked after the name resolves, unless the admin's allow list takes the address in; it follows a
// redirect only to the same host over https.
type Egress struct {
	allow []netip.Prefix
	own   []netip.Addr
}

// errEgress: the address is not one the server connects to for anyone.
var errEgress = errors.New("egress refused")

// nat64 embeds an IPv4 address in its last four bytes (RFC 6052).
var nat64 = netip.MustParsePrefix("64:ff9b::/96")

// ⚠️ Shared address space (RFC 6598), where a tailnet's machines sit, and the tailnet's IPv6 range: reachable from
// the server though not public.
var tailnet = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")}

// NewEgress takes allow (server.egress_allow, CIDR prefixes) and the address the server listens on, which it never
// connects to: all of this host's addresses when it listens on every interface.
func NewEgress(allow []string, listen string) (*Egress, error) {
	e := &Egress{}
	for _, a := range allow {
		p, err := netip.ParsePrefix(a)
		if err != nil {
			return nil, fmt.Errorf("server.egress_allow: %q: %w", a, err)
		}
		e.allow = append(e.allow, p.Masked())
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		host = listen
	}
	ip, err := netip.ParseAddr(host)
	switch {
	case err == nil && !ip.IsUnspecified():
		e.own = append(e.own, ip.Unmap())
	case err == nil || host == "":
		addrs, _ := net.InterfaceAddrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(n.IP); ok {
					e.own = append(e.own, ip.Unmap())
				}
			}
		}
	}
	return e, nil
}

// TrackerTimeout bounds one request to a tracker.
const TrackerTimeout = 30 * time.Second

func (s *Server) egress() *Egress {
	if s.opt.Egress != nil {
		return s.opt.Egress
	}
	return &Egress{}
}

// permits: the server may connect to ip.
func (e *Egress) permits(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, p := range e.allow {
		if p.Contains(ip) {
			return true
		}
	}
	if nat64.Contains(ip) {
		b := ip.As16()
		ip = netip.AddrFrom4([4]byte(b[12:]))
	}
	for _, o := range e.own {
		if o == ip {
			return false
		}
	}
	for _, p := range tailnet {
		if p.Contains(ip) {
			return false
		}
	}
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && ip != netip.AddrFrom4([4]byte{255, 255, 255, 255})
}

// Client is an HTTP client under e's rules, each request at most timeout. It never goes through a proxy of the
// environment: the proxy would make the connection e checks.
func (e *Egress) Client(timeout time.Duration) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return errEgress
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !e.permits(ip) {
			return errEgress
		}
		return nil
	}}
	tr := &http.Transport{DialContext: d.DialContext, ForceAttemptHTTP2: true, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second, ExpectContinueTimeout: time.Second}
	return &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: sameHostHTTPS}
}

// sameHostHTTPS follows a redirect only from https to https on the same host, a few times; any other answers as
// the redirect itself.
func sameHostHTTPS(req *http.Request, via []*http.Request) error {
	first := via[0].URL
	if len(via) < 5 && first.Scheme == "https" && req.URL.Scheme == "https" && req.URL.Host == first.Host {
		return nil
	}
	return http.ErrUseLastResponse
}
