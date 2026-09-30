// team derives what the machines and team pages show from the store and the server's lists: whose machine is whose,
// what each one lacks, what waits on it, and who may change what. Every function is pure.
import * as sel from './select.js';

export const isAdmin = session => session?.role === 'admin';

// inProject: user takes part in project (its owner, or a member in any role).
export const inProject = (p, user) => !!p && !!user && (p.owner === user || !!(p.members || {})[user]);

// machineGroups splits the machines into the viewer's own, those shared with them (by name, or through a project they
// are in), and everyone else's; empty groups are left out, and each keeps the machines by name.
export function machineGroups(machines, state, me) {
  const list = [...machines].sort((a, b) => a.name.localeCompare(b.name));
  const toMe = m => {
    const s = state.shares?.[m.name];
    return !!s && ((s.users || []).includes(me) || (s.projects || []).some(id => inProject(state.projects?.[id], me)));
  };
  const mine = m => m.owner === me || !m.owner;
  return [['mine', list.filter(mine)], ['toMe', list.filter(m => !mine(m) && toMe(m))], ['others', list.filter(m => !mine(m) && !toMe(m))]]
    .filter(([, xs]) => xs.length);
}

// machineSummary counts the machines that still run anything: how many, how many are up, their slots in use and the
// runs queued for them.
export function machineSummary(machines) {
  const live = machines.filter(m => !m.retired);
  const up = live.filter(m => m.state === 'connected');
  return {count: live.length, up: up.length, slots: up.reduce((n, m) => n + (m.slots || 0), 0),
    active: up.reduce((n, m) => n + (m.active || 0), 0), queued: live.reduce((n, m) => n + (m.queued || 0), 0)};
}

// machineState is the Status state a machine is drawn with.
export const machineState = m => (m.retired ? 'canceled' : m.state === 'connected' ? 'online' : m.state === 'connecting' ? 'starting' : 'offline');

// ⚠️ How the node reports an agent CLI's login (agent.Check.Auth).
const authOK = 'ok', authMissing = 'missing';

// agentChecks are a machine's agent CLIs by name: ok (installed and signed in), auth (installed, not signed in),
// unknown (installed, sign-in not known) or missing.
export const agentChecks = m => Object.entries(m.agents || {}).sort(([a], [b]) => a.localeCompare(b)).map(([name, c]) => ({
  name, version: c.version || '',
  state: !c.installed ? 'missing' : c.auth === authOK ? 'ok' : c.auth === authMissing ? 'auth' : 'unknown',
}));

// machineNotes are what is wrong with a machine, worst first: retired, why it cannot be reached, the agent CLIs not
// signed in, the node features its tend lacks. Each is {kind, …} for the page to word.
export function machineNotes(m) {
  const out = [];
  if (m.retired) return [{kind: 'retired'}];
  if (m.error && m.state !== 'connected') out.push({kind: 'error', error: m.error, detail: m.detail || ''});
  const unsigned = agentChecks(m).filter(c => c.state === 'auth').map(c => c.name);
  if (unsigned.length) out.push({kind: 'auth', agents: unsigned});
  if (m.missing?.length) out.push({kind: 'missing', features: m.missing});
  return out;
}

// mayShare: session may change who else uses m (its owner or an admin; nobody once it is retired or has no owner).
export const mayShare = (session, m) => !m.retired && !!m.owner && (isAdmin(session) || m.owner === session?.id);

// queueOn is what waits for the machine: its queued runs, first queued first, each with its task and why it waits.
export const queueOn = (state, name) => sel.openRuns(state).filter(x => x.run.state === 'queued' && x.run.machine === name);

// laneOf is the machine's lane of the day (select.lanes), its slots as rows.
export function laneOf(state, machines, name, now) {
  const all = sel.lanes(state, machines.filter(m => m.name === name), now);
  return {from: all.from, to: all.to, lane: all.lanes.find(l => l.name === name) || {name, rows: [[]], slots: 0, active: 0}};
}

// credsOf splits the node tokens the server listed into those of machine name and the rest.
export const credsOf = (creds, name) => creds.filter(c => c.name === name);

// ⚠️ A machine name as the server takes it (server.CheckMachine): letters, digits, '-', '_', '.', up to 64.
export const machineName = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;
