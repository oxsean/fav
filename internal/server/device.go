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

// maxDevices bounds how many device codes wait at once; requests beyond it are refused.
const maxDevices = 100

// userCodeAlphabet excludes characters easy to confuse when copied by eye: 0/O, 1/I.
const userCodeAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

// deviceStatus values.
const (
	deviceStatusPending = "pending"
	deviceStatusAllowed = "allowed"
	deviceStatusDenied  = "denied"
)

// deviceAuth is a `tend login` waiting for someone to confirm it in a browser. It lives only in memory: a restart
// loses pending device codes, which is fine, the CLI starts over.
type deviceAuth struct {
	code     string // the secret a polling client presents
	userCode string // what a person reads and types, formatted XXXX-XXXX
	name     string // the client's own name (hostname), for the authorization page
	ip       string
	created  time.Time
	expires  time.Time
	status   string
	secret   string // the minted personal token, set once allowed; delivered once, then the code is dropped
	user     string // the display name of who allowed it
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

func (s *Server) findDeviceLocked(userCode string) *deviceAuth {
	for _, d := range s.devices {
		if d.userCode == userCode {
			return d
		}
	}
	return nil
}

// deviceStart begins `tend login`: a device code the CLI polls, and a user code someone types (or the link carries)
// into the browser to confirm it.
func (s *Server) deviceStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var p struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &p) {
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	s.mu.Lock()
	s.expireDevicesLocked()
	if len(s.devices) >= maxDevices {
		s.mu.Unlock()
		apiError(w, http.StatusTooManyRequests, "busy")
		return
	}
	now := time.Now()
	d := &deviceAuth{code: newDeviceCode(), userCode: newUserCode(), name: strings.TrimSpace(p.Name), ip: ip,
		created: now, expires: now.Add(deviceAge), status: deviceStatusPending}
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

// devicePoll is what `tend login` calls every interval until it gets a token, a denial, or an expiry.
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

type deviceView struct {
	Code    string    `json:"code"`
	Name    string    `json:"name"`
	IP      string    `json:"ip"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
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
	writeJSON(w, http.StatusOK, deviceView{Code: d.userCode, Name: d.name, IP: d.ip, Created: d.created, Expires: d.expires})
}

// deviceDecide is Allow or Deny on the terminal-authorization page. Allow mints a personal token owned by the
// signed-in user, delivered to the polling CLI exactly once.
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
