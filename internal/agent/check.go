package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/tend"
)

// Check is how a provider's CLI stands on this machine.
type Check struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Auth      string `json:"auth,omitempty"` // ok | missing | unknown
}

// Login states in Check.Auth.
const (
	AuthOK      = "ok"
	AuthMissing = "missing"
	AuthUnknown = "unknown"
)

// Run failure reasons a check predicts.
const (
	ReasonCLIMissing  = "cli_missing"
	ReasonAuthMissing = "auth_missing"
)

// Blocker is why a run of this provider cannot start here, "" when nothing is known against it.
func (c Check) Blocker() string {
	switch {
	case !c.Installed:
		return ReasonCLIMissing
	case c.Auth == AuthMissing:
		return ReasonAuthMissing
	}
	return ""
}

const probeWait = 10 * time.Second

// Probe checks provider's CLI: on PATH, its version, logged in. It never reads credentials, only asks the CLI.
func Probe(provider string) Check {
	switch provider {
	case tend.ProviderClaude:
		return probe("claude", []string{"auth", "status"}, claudeAuth)
	case tend.ProviderCodex:
		return probe("codex", []string{"login", "status"}, codexAuth)
	}
	return Check{Installed: true, Auth: AuthOK}
}

func probe(exe string, status []string, auth func(out []byte, ok bool) string) Check {
	if !onPath(exe) {
		return Check{Auth: AuthUnknown}
	}
	c := Check{Installed: true}
	if out, ok := output(exe, "--version"); ok {
		c.Version = firstLine(out)
	}
	out, ok := output(exe, status...)
	c.Auth = auth(out, ok)
	return c
}

func output(exe string, args ...string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), probeWait)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, args...).CombinedOutput()
	return out, err == nil
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(s)
}

// claudeAuth reads `claude auth status`: JSON whose loggedIn says it (the account in it is never kept).
func claudeAuth(out []byte, _ bool) string {
	var st struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	i := bytes.IndexByte(out, '{')
	if i < 0 || json.Unmarshal(out[i:], &st) != nil || st.LoggedIn == nil {
		return AuthUnknown
	}
	if *st.LoggedIn {
		return AuthOK
	}
	return AuthMissing
}

// codexAuth reads `codex login status`: exit 0 when logged in, "Not logged in" when not.
func codexAuth(out []byte, ok bool) string {
	switch {
	case ok:
		return AuthOK
	case bytes.Contains(bytes.ToLower(out), []byte("not logged in")):
		return AuthMissing
	}
	return AuthUnknown
}
