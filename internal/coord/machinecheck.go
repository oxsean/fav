package coord

import (
	"cmp"
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/wire"
)

// checkMachines is machine.check: machine name, reached first, or every connected machine p sees, has its node probe
// its agent CLIs afresh.
func (c *Coord) checkMachines(ctx context.Context, p Principal, name string) (MachineChecks, error) {
	out := MachineChecks{Machines: []Machine{}}
	var ms []*machine
	c.mu.Lock()
	if name != "" {
		m := c.ms[name]
		if m == nil || !c.canSee(p, name) {
			c.mu.Unlock()
			return out, notFound("machine " + name)
		}
		ms = append(ms, m)
	} else {
		for _, m := range c.ms {
			if m.conn != nil && c.canSee(p, m.name) {
				ms = append(ms, m)
			}
		}
	}
	c.mu.Unlock()
	errs := make([]error, len(ms))
	var wg sync.WaitGroup
	for i, m := range ms {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if name != "" {
				if _, errs[i] = c.reach(ctx, m); errs[i] != nil {
					return
				}
			}
			errs[i] = c.probe(ctx, m)
		}()
	}
	wg.Wait()
	if name != "" && errs[0] != nil {
		return out, errs[0]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var views []Machine
	for i, m := range ms {
		if errs[i] != nil {
			if out.Failed == nil {
				out.Failed = map[string]string{}
			}
			out.Failed[m.name] = cmp.Or(wire.Code(errs[i]), wire.CodeInternal)
			continue
		}
		views = append(views, c.machineView(m))
	}
	out.Machines = c.machinesFor(p, views)
	sort.Slice(out.Machines, func(i, j int) bool { return out.Machines[i].Name < out.Machines[j].Name })
	return out, nil
}

// probe has m's node probe its agent CLIs afresh and waits for it; a probe under way is waited for, not repeated. The
// probe itself outlives ctx, for whoever else waits for it.
func (c *Coord) probe(ctx context.Context, m *machine) error {
	c.mu.Lock()
	pr := m.probing
	if pr == nil {
		conn := m.conn
		switch {
		case conn == nil:
			c.mu.Unlock()
			return &wire.Error{Code: wire.CodeOffline, Detail: m.name}
		case !slices.Contains(m.hello.Methods, node.MAgents):
			c.mu.Unlock()
			return &wire.Error{Code: wire.CodeUnsupported, Detail: node.MAgents}
		}
		pr = &probe{done: make(chan struct{})}
		m.probing = pr
		go func() {
			pctx, cancel := context.WithTimeout(context.Background(), callWait)
			defer cancel()
			var out node.Checks
			_, err := c.callNode(pctx, m, conn, node.MAgents, node.ChecksParams{Fresh: true}, &out)
			if wire.Code(err) == wire.CodeUnknownMethod {
				err = &wire.Error{Code: wire.CodeUnsupported, Detail: node.MAgents}
			}
			c.mu.Lock()
			if err == nil {
				m.checks, m.checkedAt = out.Agents, time.Now()
				c.machinesMoved()
			}
			pr.err, m.probing = err, nil
			c.mu.Unlock()
			close(pr.done)
		}()
	}
	c.mu.Unlock()
	select {
	case <-pr.done:
		return pr.err
	case <-ctx.Done():
		return &wire.Error{Code: wire.CodeTimeout, Detail: node.MAgents}
	}
}
