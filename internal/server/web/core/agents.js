// agents derives what the agent page shows from the coordinator's two lists (agentdef.list: the definitions the viewer
// manages or may use; agent.list: the agents they may pick, configured profiles and definitions compiled), the state
// and the machines: where each agent comes from, the machines it runs on, what uses it, and the Markdown a new,
// imported or copied definition starts from. Every function but createAgentDefs is pure.
import {signal} from '../vendor/signals-core.mjs';
import {finished} from './tasks.js';
import {isAdmin, machineGroups, agentChecks} from './team.js';

// ⚠️ The definition format's rules (internal/defs): a name, the roles, what owns a project's definition, the provider
// an imported Claude Code subagent runs with and the model it leaves to the CLI.
export const defName = /^[a-z0-9][a-z0-9_.-]{0,63}$/;
export const roles = ['planner', 'implement', 'review', 'test', 'any'];
export const projectOwner = 'project:';
const importProvider = 'claude', inheritModel = 'inherit';

// ⚠️ The agent CLIs a new definition picks from, and the model aliases each takes besides full model names.
export const providers = ['claude', 'codex'];
export const aliases = {claude: ['opus', 'sonnet', 'haiku', 'fable'], codex: []};

// createAgentDefs reads the two lists over wire. defs is null until the first answer; read asks again, and only the
// last answer asked for is kept.
export function createAgentDefs({wire}) {
  const defs = signal(null), agents = signal([]);
  let asked = 0;
  return {
    defs, agents,
    read() {
      const n = ++asked;
      return Promise.all([wire.call('agentdef.list', {}), wire.call('agent.list', {})]).then(([d, a]) => {
        if (n !== asked) return;
        defs.value = d?.defs || [];
        agents.value = a?.agents || [];
      });
    },
  };
}

// sources are the page's filters, in their order: all, the viewer's own, their projects', shared with them, other
// people's (an admin manages them without using them) and the configured profiles.
export const sources = ['all', 'mine', 'project', 'shared', 'others', 'profile'];

// sourceOf is where definition d comes from for the viewer me: theirs, a project's, someone else's they may run, or
// someone else's they only manage (usable says they may run it).
export function sourceOf(d, me, usable) {
  if (d.owner === me) return 'mine';
  if (projectOf(d.owner)) return 'project';
  return usable ? 'shared' : 'others';
}

// projectOf is the project that owns a definition, '' when a person does.
export const projectOf = owner => (owner?.startsWith(projectOwner) ? owner.slice(projectOwner.length) : '');

// rows are the agents the page lists, by name: each definition ({kind: 'def', view, profile}, profile being what it
// compiles to when the viewer may run it) and each configured profile no definition shadows ({kind: 'profile'}).
export function rows(defs, agents, me) {
  const out = (defs || []).map(view => {
    const profile = agents.find(a => a.name === view.name) || null;
    return {name: view.name, kind: 'def', view, profile, source: sourceOf(view, me, !!profile)};
  });
  for (const a of agents) if (!out.some(r => r.name === a.name)) out.push({name: a.name, kind: 'profile', view: null, profile: a, source: 'profile'});
  return out.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
}

export const counts = list => Object.fromEntries(sources.map(s => [s, s === 'all' ? list.length : list.filter(r => r.source === s).length]));

// spec is what the agent runs as: provider, model, effort, permission, the one machine it is held to, its denied tools;
// a definition the viewer may not run says only what its view does.
export function spec(r) {
  const p = r.profile || {}, v = r.view || {};
  return {provider: p.provider || v.provider || '', model: p.model || v.model || '', effort: p.effort || v.effort || '',
    permission: p.permission || '', machine: p.machine || '', deny: p.deny || []};
}

// usableMachines are the machines the viewer may send runs to: their own and those shared with them (by name, or
// through a project they are in); an admin's are all. Retired ones run nothing.
export function usableMachines(machines, state, session) {
  const live = machines.filter(m => !m.retired);
  if (isAdmin(session)) return [...live].sort((a, b) => a.name.localeCompare(b.name));
  return machineGroups(live, state, session?.id || '').filter(([g]) => g !== 'others').flatMap(([, xs]) => xs);
}

// runsOn is how each machine takes agent spec s: ok, offline (a run waits for it), auth (its CLI is not signed in),
// missing (its CLI is not installed), unknown (its node does not say whether it is signed in, or whether it has the
// CLI at all) or pinned (s runs only on another machine).
export function runsOn(s, machines) {
  return machines.map(m => {
    if (s.machine && s.machine !== m.name) return {name: m.name, state: 'pinned'};
    const c = m.agents?.[s.provider];
    const cli = c ? agentChecks({agents: {[s.provider]: c}})[0].state : 'unknown';
    if (cli === 'missing' || cli === 'auth') return {name: m.name, state: cli};
    if (m.state !== 'connected') return {name: m.name, state: 'offline'};
    return {name: m.name, state: cli};
  });
}

// runnable are the machines a run of it can go to (ok, offline or unknown), by name.
export const runnable = on => on.filter(x => x.state === 'ok' || x.state === 'offline' || x.state === 'unknown').map(x => x.name);

// usedBy is what names agent name: the projects whose defaults or roles pick it ({project, roles}, the default agent
// being role ''), and the tasks not yet finished that run with it.
export function usedBy(state, name) {
  const projects = Object.values(state.projects || {}).map(p => {
    const d = p.defaults || {};
    const rs = [...(d.agent === name ? [''] : []), ...Object.entries(d.roles || {}).filter(([, a]) => a === name).map(([r]) => r).sort()];
    return {project: p, roles: rs};
  }).filter(x => x.roles.length).sort((a, b) => a.project.name.localeCompare(b.project.name));
  const tasks = Object.values(state.tasks || {}).filter(t => t.agent === name && !finished(t.status));
  return {projects, tasks};
}

// may is what the viewer may do with row r: edit and remove (they manage it, and edit only what they may read), share
// (they manage it), copy (they may read it, or it is a profile) and export (they may read it).
export function may(r) {
  const v = r.view;
  return {edit: !!v?.manage && !!v.text, remove: !!v?.manage, share: !!v?.manage, copy: r.kind === 'profile' || (!!v?.text && !v.manage),
    export: !!v?.text, leave: !!v?.leave, transfer: !!v?.manage};
}

// ownersFor are where a new definition may go: the viewer ('') and the projects they own, all of them for an admin.
export function ownersFor(state, session) {
  const ps = Object.values(state.projects || {}).filter(p => isAdmin(session) || p.owner === session?.id)
    .sort((a, b) => a.name.localeCompare(b.name));
  return ['', ...ps.map(p => projectOwner + p.id)];
}

// transferTo are the projects the viewer may give definition r to: those they own (an admin: all), less the one
// that has it.
export const transferTo = (state, session, r) => ownersFor(state, session).filter(o => o && o !== r.view?.owner);

const front = /^---[ \t]*\n([\s\S]*?)\n---[ \t]*(?:\n|$)/;

// nameOf is the name a definition gives itself in its front matter, or ''.
export function nameOf(text) {
  const head = front.exec(String(text).replace(/\r\n/g, '\n'))?.[1] || '';
  return (/^name:[ \t]*["']?([^"'\s#]+)["']?[ \t]*$/m.exec(head) || [])[1] || '';
}

// renamed is text with the name in its front matter set to name.
export function renamed(text, name) {
  const s = String(text).replace(/\r\n/g, '\n');
  const m = front.exec(s);
  if (!m) return s;
  const head = /^name:.*$/m.test(m[1]) ? m[1].replace(/^name:.*$/m, 'name: ' + name) : 'name: ' + name + '\n' + m[1];
  return '---\n' + head + '\n---\n' + s.slice(m[0].length);
}

// draft is a new definition's Markdown from the first step's fields; the instructions come after it.
export function draft({name, provider, model = '', role = ''}) {
  const lines = ['name: ' + name, ...(role ? ['role: ' + role] : []), 'provider: ' + provider, ...(model.trim() ? ['model: ' + model.trim()] : [])];
  return '---\n' + lines.join('\n') + '\n---\n';
}

// imported is a Claude Code subagent file as tend takes it: without a provider or a profile it runs with claude, and
// model: inherit is left out (the CLI's own default). Text without a front matter is left for the coordinator to refuse.
export function imported(text) {
  const s = String(text).replace(/^﻿/, '').replace(/\r\n/g, '\n');
  const m = front.exec(s);
  if (!m) return s;
  const head = m[1].split('\n').filter(l => !new RegExp(`^model:[ \\t]*["']?${inheritModel}["']?[ \\t]*$`).test(l));
  if (!head.some(l => /^(provider|profile):/.test(l))) head.push('provider: ' + importProvider);
  return '---\n' + head.join('\n') + '\n---\n' + s.slice(m[0].length);
}

// copyName is the first of name-copy, name-copy-2, … that no agent has.
export function copyName(name, taken) {
  for (let n = 1; ; n++) {
    const c = name + '-copy' + (n > 1 ? '-' + n : '');
    if (!taken.includes(c)) return c;
  }
}

// copied is the Markdown a copy of row r starts from, named name: a definition's text, or a definition that starts
// from the profile.
export function copied(r, name) {
  if (r.kind === 'profile') return '---\nname: ' + name + '\nprofile: ' + r.name + '\n---\n';
  return renamed(r.view.text, name);
}
