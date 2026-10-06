// team derives what the machines and team pages show from the store and the server's lists: whose machine is whose,
// what each one lacks, what waits on it, and who may change what. Every function is pure.
import * as sel from './select.js';
import {finished} from './tasks.js';

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

// machineNotes are what is wrong with a machine, worst first: retired, why it cannot be reached, that it takes no new
// runs (drain: its entry in the state's drains), the agent CLIs not signed in, the node features its tend lacks. Each
// is {kind, …} for the page to word.
export function machineNotes(m, drain) {
  const out = [];
  if (m.retired) return [{kind: 'retired'}];
  if (m.error && m.state !== 'connected') out.push({kind: 'error', error: m.error, detail: m.detail || ''});
  if (drain) out.push({kind: 'drain', by: drain.by || '', at: drain.at || ''});
  const unsigned = agentChecks(m).filter(c => c.state === 'auth').map(c => c.name);
  if (unsigned.length) out.push({kind: 'auth', agents: unsigned});
  if (m.missing?.length) out.push({kind: 'missing', features: m.missing});
  return out;
}

// mayShare: session may change who else uses m (its owner or an admin; nobody once it is retired or has no owner).
export const mayShare = (session, m) => !m.retired && !!m.owner && (isAdmin(session) || m.owner === session?.id);

// mayRead: the viewer may read m's own sessions, as the coordinator says (its owner, or whom its scope names).
export const mayRead = (session, m) => !!m?.sessions;

// opensSessions: the viewer may open m's own sessions page: the server forwards node.call and session may read them.
export const opensSessions = (wire, session, m) => !!wire?.has?.('node.call') && mayRead(session, m);

// sharedToMe: the viewer reads m's sessions because its owner shares them, read only.
export const sharedToMe = (session, m) => !!m?.sessions && !!m.owner && m.owner !== session?.id;

// ⚠️ Who sees a machine's sessions (task.SessionShare): nobody but its owner, people by name, a project's owner and
// members, or everyone signed in to the team.
export const scopeKinds = ['private', 'users', 'project', 'team'];

// scopeOf is the session_share the coordinator gives m's owner as one of scopeKinds, with the people or the project
// it names; a project no longer in state counts as private.
export function scopeOf(state, m) {
  const s = m?.session_share || {};
  if (s.team) return {kind: 'team', users: [], project: ''};
  const project = (s.projects || []).find(id => state.projects?.[id]) || '';
  if (project) return {kind: 'project', users: [], project};
  if (s.users?.length) return {kind: 'users', users: [...s.users], project: ''};
  return {kind: 'private', users: [], project: ''};
}

// scopeParams is what machine.sessions sends for a scope: nothing but the machine for private.
export const scopeParams = (machine, sc) => ({machine, ...(sc.kind === 'users' ? {users: sc.users}
  : sc.kind === 'project' ? {projects: [sc.project]} : sc.kind === 'team' ? {team: true} : {})});

// scopeReady: a scope that names someone, or private.
export const scopeReady = sc => (sc.kind === 'users' ? sc.users.length > 0 : sc.kind === 'project' ? !!sc.project : true);

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

// ⚠️ The roles a project member takes (task.Member*), and a user's roles on the server (store.Role*).
export const accessRoles = ['participant', 'reader'];
export const userRoles = ['member', 'admin'];

// projectsOf are the projects user takes part in, by name, each with the part: owner, participant or reader.
export const projectsOf = (state, user) => Object.values(state.projects || {})
  .filter(p => inProject(p, user)).sort((a, b) => a.name.localeCompare(b.name))
  .map(p => ({id: p.id, name: p.name, role: p.owner === user ? 'owner' : p.members[user]}));

// people are the users the server listed, the server admin (local) left out: active ones by name, then the disabled,
// each with their projects and the machines they own.
export function people(users, state, machines) {
  return users.filter(u => u.id !== 'local').map(u => ({
    ...u, projects: projectsOf(state, u.id), machines: machines.filter(m => m.owner === u.id && !m.retired).map(m => m.name).sort(),
  })).sort((a, b) => (a.disabled === b.disabled ? (a.name || a.id).localeCompare(b.name || b.id) : a.disabled ? 1 : -1));
}

// projectFacts are a project's counts: its tasks and those not finished, its participants and readers (its owner
// counted as taking part).
export function projectFacts(state, p) {
  const tasks = Object.values(state.tasks || {}).filter(t => t.project === p.id);
  const roles = Object.values(p.members || {});
  return {tasks: tasks.length, open: tasks.filter(t => !finished(t.status)).length,
    participants: roles.filter(r => r === 'participant').length + (p.owner ? 1 : 0), readers: roles.filter(r => r === 'reader').length};
}

// personal: p has an owner and no one else takes part in it (task.Project.Personal: a project without an owner is not).
export const personal = p => !!p?.owner && !Object.keys(p.members || {}).some(u => u !== p.owner);

// mayManage: session may change project p's members and settings (its owner or an admin).
export const mayManage = (session, p) => !!p && (isAdmin(session) || p.owner === session?.id);

// offboardOf is user.offboard.preview's answer with every field it may leave out filled.
export const offboardOf = p => ({projects: p?.projects || [], left: p?.left || [], tasks: p?.tasks || [], defs: p?.defs || [],
  machines: p?.machines || [], canceled: p?.canceled || 0, private: p?.private || 0});

// ⚠️ The security log's kinds (server.audit) in the page's groups: sign-ins, credentials, refusals.
const auditGroups = {
  login: ['login', 'login_refused', 'device.allow', 'device.deny', 'link'],
  creds: ['token', 'machine', 'rebind', 'revoke', 'tracker.credential', 'webhook'],
  refused: ['denied', 'login_refused', 'refused', 'invite.project_failed'],
};
export const auditFilters = ['all', 'login', 'creds', 'refused'];
export const auditIn = (filter, e) => filter === 'all' || (auditGroups[filter] || []).includes(e.kind);
export const auditRefused = e => auditGroups.refused.includes(e.kind);

// ⚠️ How a sign-in rule matches (store.Admit*): a verified email, a verified email's domain, provider:username.
export const admitKinds = ['domain', 'email', 'login'];

// ⚠️ A project's hooks (task.Project.Hooks) and the roles its defaults name an agent for (workflow stages' roles).
export const hookNames = ['setup', 'before_run', 'check', 'cleanup'];
export const roleNames = ['implement', 'review', 'test', 'planner'];

const argv = text => text.trim().split(/\s+/).filter(Boolean);

// draftOf is project p as its settings form edits it: hooks as one line each, repositories and links as copies.
export const draftOf = p => ({
  name: p.name || '', owner: p.owner || '', context: p.context || '',
  repos: (p.repos || []).map(r => ({name: r.name || '', remote: r.remote || '', base: r.base || '', worktrees: !!r.worktrees,
    dirs: Object.entries(r.dirs || {}).map(([machine, path]) => ({machine, path}))})),
  links: (p.links || []).map(l => ({kind: l.kind || '', url: l.url || ''})),
  machine: p.defaults?.machine || '', workflow: p.defaults?.workflow || '',
  roles: Object.fromEntries(roleNames.map(r => [r, p.defaults?.roles?.[r] || ''])),
  hooks: Object.fromEntries(hookNames.map(h => [h, (p.hooks?.[h] || []).join(' ')])),
});

// reposOf are the draft's repositories as a project keeps them: fields trimmed, empty ones and rows without a name or
// a path left out.
export const reposOf = d => d.repos.filter(r => r.name.trim()).map(r => {
  const out = {name: r.name.trim()};
  if (r.remote.trim()) out.remote = r.remote.trim();
  if (r.base.trim()) out.base = r.base.trim();
  const dirs = r.dirs.filter(x => x.machine && x.path.trim());
  if (dirs.length) out.dirs = Object.fromEntries(dirs.map(x => [x.machine, x.path.trim()]));
  if (r.worktrees) out.worktrees = true;
  return out;
});

// linksOf are the draft's links as a project keeps them: trimmed, a row without an address left out, one without a
// kind a plain link.
export const linksOf = d => d.links.filter(l => l.url.trim()).map(l => ({kind: l.kind.trim() || 'link', url: l.url.trim()}));

// linkHref is where a project's link opens: an http(s) address, null for anything else (shown as text).
export const linkHref = url => (/^https?:\/\/\S+$/i.test(url || '') ? url : null);

const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);

// projectEditOf is what project.edit sends for the draft: the project's id and only the fields that changed (the
// defaults whole, keeping the agent it names outside the roles), or null when nothing did.
export function projectEditOf(p, d) {
  const e = {};
  if (d.name.trim() && d.name.trim() !== p.name) e.name = d.name.trim();
  if (d.owner && d.owner !== (p.owner || '')) e.owner = d.owner;
  if (d.context !== (p.context || '')) e.context = d.context;
  const repos = reposOf(d);
  if (!same(repos, p.repos || [])) e.repos = repos;
  const links = linksOf(d);
  if (!same(links, p.links || [])) e.links = links;
  const roles = Object.fromEntries(Object.entries(d.roles).filter(([, a]) => a));
  const defaults = {...(p.defaults?.agent ? {agent: p.defaults.agent} : {}), ...(d.machine ? {machine: d.machine} : {}),
    ...(Object.keys(roles).length ? {roles} : {}), ...(d.workflow ? {workflow: d.workflow} : {})};
  const was = p.defaults || {};
  if (!same([defaults.workflow, defaults.machine, defaults.agent, defaults.roles || {}], [was.workflow, was.machine, was.agent, was.roles || {}])) e.defaults = defaults;
  const hooks = Object.fromEntries(hookNames.map(h => [h, argv(d.hooks[h])]).filter(([, a]) => a.length));
  const wasHooks = Object.fromEntries(Object.entries(p.hooks || {}).filter(([, a]) => a?.length));
  if (!same(Object.entries(hooks).sort(), Object.entries(wasHooks).sort())) e.hooks = hooks;
  return Object.keys(e).length ? {id: p.id, ...e} : null;
}

// dirsToCheck are the draft's checkouts: each repository's machine and path.
export const dirsToCheck = d => d.repos.flatMap(r => r.dirs.filter(x => x.machine && x.path.trim()).map(x => ({repo: r.name, machine: x.machine, path: x.path.trim()})));

// parentOf and baseOf split a path on either separator: the directory it is in, and its last name.
export const parentOf = path => path.replace(/[\\/]+$/, '').replace(/[\\/][^\\/]*$/, '') || path.slice(0, 1);
export const baseOf = path => path.split(/[\\/]/).filter(Boolean).pop() || '';

// dirVerdict is what project.dirs on the path's parent says of the path: git (a checkout), plain (there, not a
// checkout), missing, or outside (the machine lets no run go there).
export function dirVerdict(r, path) {
  if (r?.outside) return 'outside';
  if (!r?.exists) return 'missing';
  const d = (r.dirs || []).find(x => x.name === baseOf(path));
  return !d ? 'missing' : d.git ? 'git' : 'plain';
}

// flowName is the name a workflow definition gives itself in its front matter, or ''.
export function flowName(text) {
  const head = /^---\s*\n([\s\S]*?)^---\s*$/m.exec(text)?.[1] || '';
  return (/^name:\s*["']?([A-Za-z0-9][\w.-]*)["']?\s*$/m.exec(head) || [])[1] || '';
}

// ⚠️ A new workflow's starting text, in the definition format (internal/workflow).
export const flowTemplate = `---
name: my-flow
description: what it is for
max_loops: 2
stages:
  - {name: implement, role: implement, check: true}
  - {name: review, role: review, output: verdict, on_rework: implement}
  - {name: accept, gate: human}
---
## implement

{{task.brief}}

{{#rework}}Round {{loops}}: fix this first:

{{rework.notes}}
{{/rework}}
`;

// ⚠️ The trackers a project binds to (tracker.Kind*) and where each lives unless it is self-hosted.
export const trackerKinds = {github: 'https://github.com', gitea: '', gitlab: 'https://gitlab.com'};

// trackerState is how a binding syncs: stopped (its token was refused), paused (rate limited) or ok.
export const trackerState = x => (x.stopped ? 'stopped' : x.paused_until ? 'paused' : 'ok');

// shownComment is a comment body as the tracker shows it: its HTML comments, tend's hidden marker among them, left out.
export const shownComment = body => String(body || '').replace(/<!--[\s\S]*?-->\n?/g, '').trim();

// issueURL is issue n of binding x on its tracker.
export const issueURL = (x, n) => `${x.base.replace(/\/$/, '')}/${x.repo}/${x.kind === 'gitlab' ? '-/' : ''}issues/${n}`;

// ⚠️ A binding's settings as the server takes them (server.TrackerSettings.check): polled every 30 to 3600 seconds.
export const pollRange = [30, 3600];
