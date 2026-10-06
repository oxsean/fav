package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Mode 2 reads other machines through the server (node.call), never over ssh: the machines the viewer may read but
// this one, the viewer's own kept on disk and shown from there while the server cannot be reached, others' only while
// it can. ssh is only how the viewer's own machine is resumed, when the host of its name here is that machine.

// Others are the other machines whose sessions the TUI shows; the zero value shows none and contacts nothing.
type Others struct {
	Hosts  *remote.Hosts
	Server *remote.NodeCall // mode 2: the transport under Hosts, told which machines the server lets the viewer read
	SSH    *remote.Hosts    // mode 2: config.hosts, reached only to resume the viewer's own machines
}

type servedHosts struct {
	nc      *remote.NodeCall
	ssh     *remote.Hosts
	mine    map[string]bool // the machines read, and whether the viewer owns each
	fetched *coord.Client   // the connection the machines were last all fetched on
	same    map[string]bool // whether the ssh host of a machine's name is that machine (their node ids match)
	asking  map[string]bool
	oldTold bool
	retries int
}

const sshSameWait = 15 * time.Second

// ServerMachines are the machines read through the server: those the viewer may read (Machine.sessions) but this one
// (whose node id is nodeID), each Mine when the viewer owns it.
func ServerMachines(ms []coord.Machine, caller *remote.Caller, nodeID string) []remote.Machine {
	var out []remote.Machine
	for _, mc := range ms {
		if !mc.Sessions || nodeID != "" && mc.NodeID == nodeID {
			continue
		}
		out = append(out, remote.Machine{Name: mc.Name, Mine: caller != nil && mc.Owner == caller.User})
	}
	return out
}

// OldServer: its machine list carries no node ids, so it cannot say which machine this is.
func OldServer(ms []coord.Machine) bool {
	for _, mc := range ms {
		if mc.NodeID != "" {
			return false
		}
	}
	return len(ms) > 0
}

// ForgetOthers removes what was kept on disk of each machine that list, read from ms, no longer gives as the viewer's
// own; a list from an old server, or one that did not say who asks, cannot tell.
func ForgetOthers(nc *remote.NodeCall, ms []coord.Machine, caller *remote.Caller, list []remote.Machine) {
	if caller != nil && !OldServer(ms) {
		nc.Forget(list)
	}
}

// useOthers shows o's machines; mode 2 starts from the viewer's own kept on disk, until the server lists its machines.
func (m *Model) useOthers(o Others) {
	if o.Server == nil {
		m.useHosts(o.Hosts)
		return
	}
	m.hosts, m.remote = o.Hosts, map[string]*hostRows{}
	m.far = servedHosts{nc: o.Server, ssh: o.SSH, same: map[string]bool{}, asking: map[string]bool{}}
	m.setMachines(o.Server.Kept())
}

// setMachines makes ms the machines read: a new one starts from its cache, one gone is dropped. It returns the new ones.
func (m *Model) setMachines(ms []remote.Machine) []string {
	m.far.nc.SetMachines(ms)
	m.far.mine = make(map[string]bool, len(ms))
	var added []string
	for _, x := range ms {
		m.far.mine[x.Name] = x.Mine
		if m.remote[x.Name] == nil {
			recs, st := m.hosts.Cached(x.Name)
			hr := &hostRows{at: st.At}
			hr.merge(recs)
			m.remote[x.Name] = hr
			added = append(added, x.Name)
		}
	}
	for name := range m.remote {
		if _, ok := m.far.mine[name]; !ok {
			delete(m.remote, name)
		}
	}
	m.refresh()
	return added
}

// syncMachines follows the server's machine list once this connection said who it answers as and listed them; every
// machine is fetched once per connection, a new one when it comes.
func (m *Model) syncMachines() tea.Cmd {
	t := &m.tasks
	if m.far.nc == nil || t.cl == nil || m.proj.hello == nil || !t.machinesIn {
		return nil
	}
	if OldServer(t.machines) && !m.far.oldTold {
		m.far.oldTold = true
		m.flash(i18n.T("remote.old_server"))
	}
	list := ServerMachines(t.machines, m.proj.hello.Caller, m.proj.nodeID)
	added := m.setMachines(list)
	ForgetOthers(m.far.nc, t.machines, m.proj.hello.Caller, list)
	if m.far.fetched != t.cl {
		m.far.fetched = t.cl
		return m.fetchHosts()
	}
	var cmds []tea.Cmd
	for _, name := range added {
		cmds = append(cmds, m.fetchHost(name))
	}
	return tea.Batch(cmds...)
}

// serverDown is why the server cannot be reached now; nil while connected or dialing the first time.
func (m *Model) serverDown() error {
	if !m.served() || m.tasks.cl != nil {
		return nil
	}
	return m.tasks.lost
}

// serverLost: the viewer's own machines stay with their cache, marked offline; the others' go.
func (m *Model) serverLost(err error) {
	m.tasks.lost = err
	if m.far.nc == nil {
		return
	}
	var own []remote.Machine
	for _, name := range m.hosts.Names() {
		if m.far.mine[name] {
			own = append(own, remote.Machine{Name: name, Mine: true})
		}
	}
	m.setMachines(own)
	off := &wire.Error{Code: wire.CodeOffline}
	for _, hr := range m.remote {
		hr.err, hr.liveErr = off, off
	}
	m.refresh()
}

type serverRetryMsg struct{}

// retryServer dials the server again later, waiting longer after each failure.
func (m *Model) retryServer() tea.Cmd {
	wait := min(hostsEvery<<m.far.retries, hostsMaxEvery)
	m.far.retries = min(m.far.retries+1, 4)
	return tea.Tick(wait, func(time.Time) tea.Msg { return serverRetryMsg{} })
}

func (serverRetryMsg) apply(m *Model) tea.Cmd {
	if m.tasks.cl != nil {
		return nil
	}
	return m.tasksOpen()
}

// shared: a machine another person lets the viewer read; its sessions are read here, never resumed.
func (m *Model) shared(host string) bool {
	return host != "" && m.far.nc != nil && !m.far.mine[host]
}

// ownerOf is who owns machine name, as the server says.
func (m *Model) ownerOf(name string) string {
	for _, mc := range m.tasks.machines {
		if mc.Name == name {
			return mc.Owner
		}
	}
	return ""
}

// hostMark: "@mba"; on a shared machine "@bobs  ·  read only · Bob".
func (m *Model) hostMark(r *tend.Rec) string {
	if m.shared(r.Host) {
		return "@" + r.Host + "  ·  " + i18n.F("remote.shared", m.ownerName(r.Host))
	}
	return "@" + r.Host
}

// readOnlyNote is why r cannot be changed here: another machine's session, or one shared with the viewer.
func (m *Model) readOnlyNote(r *tend.Rec) string {
	if r != nil && m.shared(r.Host) {
		return i18n.F("remote.shared_read_only", m.ownerName(r.Host))
	}
	return i18n.T("remote.read_only")
}

// sshHere: the machine's own resume goes over ssh (mode 1, or mode 2 when the host of its name is that machine).
func (m *Model) sshHere(host string) bool {
	if m.far.nc == nil {
		return true
	}
	if same, known := m.far.same[host]; known {
		return same
	}
	_, ok := m.far.ssh.Host(host)
	return ok
}

// nodeIDOf is the node id the server knows for machine name.
func (m *Model) nodeIDOf(name string) string {
	for _, mc := range m.tasks.machines {
		if mc.Name == name {
			return mc.NodeID
		}
	}
	return ""
}

type sshSameMsg struct {
	rec  *tend.Rec
	name string
	same bool
	err  error
}

// askSSH asks the host of r's machine name here who it is; the resume opens when it answers.
func (m *Model) askSSH(r *tend.Rec) {
	name := r.Host
	if !m.far.asking[name] {
		m.far.asking[name] = true
		h, want := m.far.ssh, m.nodeIDOf(name)
		m.pending = tea.Batch(m.pending, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), sshSameWait)
			defer cancel()
			hello, err := h.Hello(ctx, name)
			return sshSameMsg{rec: r, name: name, same: err == nil && want != "" && hello.NodeID == want, err: err}
		})
	}
	m.flash(i18n.F("remote.ssh_checking", name))
}

func (msg sshSameMsg) apply(m *Model) tea.Cmd {
	delete(m.far.asking, msg.name)
	if msg.err == nil { // unreachable now says nothing about who it is
		m.far.same[msg.name] = msg.same
	}
	if m.ov.active() || m.current() != msg.rec {
		return nil
	}
	if msg.same {
		m.openRemoteResume(msg.rec)
	} else {
		m.openRunThere(msg.rec)
	}
	return nil
}
