package server

import (
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/store"
)

// deviceAge is how long a device code waits for someone to allow or deny it.
const deviceAge = 10 * time.Minute

// deviceInterval is how often a polling client should ask again.
const deviceInterval = 3 * time.Second

// maxDevices bounds how many device codes wait at once, and maxDevicesPerIP how many of them one address holds;
// requests beyond either are refused.
const (
	maxDevices      = 100
	maxDevicesPerIP = 5
)

// userCodeAlphabet excludes characters easy to confuse when copied by eye: 0/O, 1/I.
const userCodeAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

// deviceStatus values.
const (
	deviceStatusPending = "pending"
	deviceStatusAllowed = "allowed"
	deviceStatusDenied  = "denied"
)

// deviceAuth is a `tend login`, or a browser signing in (session), waiting for someone to confirm it in a browser
// already signed in. It lives only in memory: a restart loses pending device codes, which is fine, the asker starts
// over.
type deviceAuth struct {
	code     string // the secret a polling client presents
	userCode string // what a person reads and types, formatted XXXX-XXXX
	name     string // the client's own name (hostname), for the authorization page
	ip       string
	created  time.Time
	expires  time.Time
	status   string
	session  bool   // the asker is this server's own page, which gets a browser session instead of a token
	secret   string // the minted personal token, set once allowed; delivered once, then the code is dropped
	user     string // the display name of who allowed it
	userID   string // who allowed it: a session is made for them when the page polls
}

func newDeviceCode() string {
	var b [32]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func newUserCode() string {
	part := func() string {
		b := make([]byte, 4)
		for i := range b {
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(userCodeAlphabet))))
			b[i] = userCodeAlphabet[n.Int64()]
		}
		return string(b)
	}
	return part() + "-" + part()
}

// expireDevicesLocked drops device codes nobody polled or confirmed before their deadline. Callers hold s.mu.
func (s *Server) expireDevicesLocked() {
	now := time.Now()
	for k, d := range s.devices {
		if now.After(d.expires) {
			delete(s.devices, k)
		}
	}
}

func (s *Server) expireDevices() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireDevicesLocked()
}

// devicesFromLocked counts the device codes waiting that ip asked for. Callers hold s.mu.
func (s *Server) devicesFromLocked(ip string) int {
	n := 0
	for _, d := range s.devices {
		if d.ip == ip && d.status == deviceStatusPending {
			n++
		}
	}
	return n
}

func (s *Server) findDeviceLocked(userCode string) *deviceAuth {
	for _, d := range s.devices {
		if d.userCode == userCode {
			return d
		}
	}
	return nil
}

// deviceStart begins `tend login`, or a browser's sign-in (session: the page itself asks, with its header and origin):
// a device code the asker polls, and a user code someone types (or the link carries) into a browser to confirm it.
func (s *Server) deviceStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var p struct {
		Name    string `json:"name"`
		Session bool   `json:"session"`
	}
	if !decode(w, r, &p) {
		return
	}
	if p.Session && !fromPage(r) {
		apiError(w, http.StatusForbidden, "csrf")
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	s.mu.Lock()
	s.expireDevicesLocked()
	if len(s.devices) >= maxDevices || s.devicesFromLocked(ip) >= maxDevicesPerIP {
		s.mu.Unlock()
		apiError(w, http.StatusTooManyRequests, "busy")
		return
	}
	now := time.Now()
	d := &deviceAuth{code: newDeviceCode(), userCode: newUserCode(), name: strings.TrimSpace(p.Name), ip: ip,
		created: now, expires: now.Add(deviceAge), status: deviceStatusPending, session: p.Session}
	s.devices[d.code] = d
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code": d.code,
		"user_code":   d.userCode,
		"verify_url":  s.base(r) + "/#device-" + d.userCode,
		"interval":    int(deviceInterval.Seconds()),
		"expires_in":  int(deviceAge.Seconds()),
	})
}

// fromPage: the request comes from this server's own page, which alone sends X-Tend from this origin.
func fromPage(r *http.Request) bool { return r.Header.Get("X-Tend") == "1" && sameOrigin(r) }

// devicePoll is what the asker calls every interval until it gets a token (or, for a session, the session's cookie),
// a denial, or an expiry. A session's poll that is not the page's is refused and leaves the code as it was.
func (s *Server) devicePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var p struct {
		DeviceCode string `json:"device_code"`
	}
	if !decode(w, r, &p) {
		return
	}
	s.mu.Lock()
	d, ok := s.devices[p.DeviceCode]
	if !ok {
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"status": "expired"})
		return
	}
	if time.Now().After(d.expires) {
		delete(s.devices, p.DeviceCode)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"status": "expired"})
		return
	}
	if d.session && !fromPage(r) {
		s.mu.Unlock()
		apiError(w, http.StatusForbidden, "csrf")
		return
	}
	if d.session && d.status == deviceStatusAllowed {
		delete(s.devices, p.DeviceCode)
		s.mu.Unlock()
		s.deviceSession(w, r, d)
		return
	}
	switch d.status {
	case deviceStatusDenied:
		delete(s.devices, p.DeviceCode)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"status": "denied"})
	case deviceStatusAllowed:
		secret, user := d.secret, d.user
		delete(s.devices, p.DeviceCode)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "token": secret, "user": user})
	default:
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"status": "pending"})
	}
}

// deviceSession signs the polling page in as whoever allowed d, unless they were disabled since.
func (s *Server) deviceSession(w http.ResponseWriter, r *http.Request, d *deviceAuth) {
	u, ok, err := s.team().User(d.userID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	if !ok || u.Disabled {
		s.audit(r, d.userID, "login_refused", "device "+d.userCode)
		writeJSON(w, http.StatusOK, map[string]string{"status": deviceStatusDenied})
		return
	}
	if err := s.startSession(w, r, u, viaDevice+d.name); err != nil {
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "user": displayName(u)})
}

// viaDevice starts the name of a browser session a device code signed in; the asker's own name follows.
const viaDevice = "device:"

type deviceView struct {
	Code    string    `json:"code"`
	Name    string    `json:"name"`
	IP      string    `json:"ip"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
	Session bool      `json:"session,omitempty"` // allowing it signs a browser in, rather than making a token
}

// deviceLookup is what the terminal-authorization page reads to show the code, client and source address.
func (s *Server) deviceLookup(w http.ResponseWriter, r *http.Request, c caller) {
	code := r.URL.Query().Get("code")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireDevicesLocked()
	d := s.findDeviceLocked(code)
	if d == nil || d.status != deviceStatusPending {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, deviceView{Code: d.userCode, Name: d.name, IP: d.ip, Created: d.created, Expires: d.expires, Session: d.session})
}

// deviceDecide is Allow or Deny on the terminal-authorization page. Allow mints a personal token owned by the
// signed-in user, delivered to the polling CLI exactly once; for a browser it notes who allowed it, and the page's
// poll gets a session of theirs.
func (s *Server) deviceDecide(w http.ResponseWriter, r *http.Request, c caller) {
	var p struct {
		Code  string `json:"code"`
		Allow bool   `json:"allow"`
	}
	if !decode(w, r, &p) {
		return
	}
	s.mu.Lock()
	d := s.findDeviceLocked(p.Code)
	if d == nil || d.status != deviceStatusPending || time.Now().After(d.expires) {
		s.mu.Unlock()
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if !p.Allow {
		d.status = deviceStatusDenied
		s.mu.Unlock()
		s.audit(r, c.user.ID, "device.deny", p.Code)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if d.session {
		d.status, d.userID, d.user = deviceStatusAllowed, c.user.ID, displayName(c.user)
		s.mu.Unlock()
		s.audit(r, c.user.ID, "device.allow", "session "+p.Code)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	d.status = "deciding" // reserved: a concurrent poll neither sees a token yet nor gets to allow it twice
	name := d.name
	s.mu.Unlock()

	secret, cr, err := s.team().NewCredential(store.KindToken, "login:"+name, c.user.ID, 0)
	if err != nil {
		s.mu.Lock()
		if d2 := s.findDeviceLocked(p.Code); d2 != nil {
			d2.status = deviceStatusPending
		}
		s.mu.Unlock()
		apiError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.opt.Dir.Reload()
	s.mu.Lock()
	if d2 := s.findDeviceLocked(p.Code); d2 != nil {
		d2.status, d2.secret, d2.user = deviceStatusAllowed, secret, displayName(c.user)
	}
	s.mu.Unlock()
	s.audit(r, c.user.ID, "device.allow", cr.ID+" "+p.Code)
	w.WriteHeader(http.StatusNoContent)
}
