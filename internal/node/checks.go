package node

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/fileio"
)

// checkFor is how long a check stands: a good one for long, a bad one briefly (the user may be logging in right now).
const (
	checkGood = 5 * time.Minute
	checkBad  = 30 * time.Second
)

type checks struct {
	mu sync.Mutex
	at map[string]time.Time
	v  map[string]agent.Check
}

// Check is how provider's CLI stands here; fresh skips what was checked before.
func (n *Node) Check(provider string, fresh bool) agent.Check {
	n.checks.mu.Lock()
	if n.checks.v == nil {
		n.checks.at, n.checks.v = map[string]time.Time{}, map[string]agent.Check{}
	}
	c, ok := n.checks.v[provider]
	ttl := checkGood
	if c.Blocker() != "" {
		ttl = checkBad
	}
	if ok && !fresh && time.Since(n.checks.at[provider]) < ttl {
		n.checks.mu.Unlock()
		return c
	}
	n.checks.mu.Unlock()
	probe := n.Probe
	if probe == nil {
		probe = agent.Probe
	}
	c = probe(provider)
	n.checks.mu.Lock()
	n.checks.v[provider], n.checks.at[provider] = c, time.Now()
	n.checks.mu.Unlock()
	return c
}

// ChecksParams asks node.agents; Fresh checks again instead of answering what was checked lately.
type ChecksParams struct {
	Fresh bool `json:"fresh,omitempty"`
}

// Checks answers node.agents: how each agent CLI whose sessions tend knows stands here.
type Checks struct {
	Agents map[string]agent.Check `json:"agents"`
}

// Checks is every session provider's check.
func (n *Node) Checks(fresh bool) Checks {
	out := Checks{Agents: map[string]agent.Check{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range agent.Sessions() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := n.Check(p, fresh)
			mu.Lock()
			out.Agents[p] = c
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// ID is this node's lasting identity, made once and kept in its directory; "" when it cannot be kept.
func (n *Node) ID() string {
	p := filepath.Join(n.Dir, "id")
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return strings.TrimSpace(string(b))
	}
	var b [8]byte
	rand.Read(b[:])
	id := "n_" + hex.EncodeToString(b[:])
	if os.MkdirAll(n.Dir, 0o700) != nil || fileio.WriteFile(p, []byte(id+"\n"), 0o600) != nil {
		return ""
	}
	return id
}
