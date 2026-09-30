// tasks derives the task pages from the state: the rows a filter keeps, the board's columns by situation and what a
// drop onto a column does, the tree as indented rows, subtask progress, where a new task may work, what a dispatch
// meets on the machine and agent picked, and a plan draft's edits and checks. Every function is pure: the state as the
// store holds it, the machines as machines.watch gives them.
import {situation} from './fold.js';
import {openStates} from './select.js';

// ⚠️ The board's columns (docs/design/tasks/board.md), in their order.
export const columns = ['waiting', 'running', 'queued', 'backlog', 'ended'];

export const columnOf = sit => (sit.kind === 'done' || sit.kind === 'canceled' ? 'ended' : sit.kind);

export const finished = status => status === 'done' || status === 'canceled';

export const openRun = (state, id) => Object.values(state.runs).find(r => r.task === id && openStates.includes(r.state)) || null;

// runsOf: a task's runs, the latest first.
export const runsOf = (state, id) => Object.values(state.runs).filter(r => r.task === id)
  .sort((a, b) => (b.seq || 0) - (a.seq || 0) || String(b.queued_at).localeCompare(String(a.queued_at)));

export const childrenOf = (state, id) => Object.values(state.tasks).filter(t => t.parent === id);

// progress: how many of a task's subtasks are done, of those not canceled; null when it has none.
export function progress(state, id) {
  const kids = childrenOf(state, id).filter(t => t.status !== 'canceled');
  if (!kids.length) return null;
  return {done: kids.filter(t => t.status === 'done').length, of: kids.length};
}

// spent: what a task's runs used in all.
export function spent(state, id) {
  let usd = 0, tok = 0;
  for (const r of Object.values(state.runs)) {
    if (r.task !== id || !r.usage) continue;
    usd += r.usage.cost_usd || 0;
    tok += (r.usage.input || 0) + (r.usage.cache_write || 0) + (r.usage.output || 0);
  }
  return {usd, tokens: tok};
}

// matcher is what a filter keeps: {me, mine (the viewer owns or accepts it), project, column, q (title, id or tag)}.
export function matcher(state, {me = '', mine = false, project = '', column = '', q = ''} = {}) {
  const query = q.trim().toLowerCase();
  return t => {
    if (mine && t.owner !== me && t.approver !== me) return false;
    if (project && t.project !== project) return false;
    if (column && columnOf(situation(state, t)) !== column) return false;
    if (query && !`${t.title} ${t.id} ${(t.tags || []).join(' ')}`.toLowerCase().includes(query)) return false;
    return true;
  };
}

const newest = (a, b) => String(b.updated_at || '').localeCompare(String(a.updated_at || '')) || a.id.localeCompare(b.id);

// rows: the tasks keep holds with their situations, by column (what waits first), the latest changed first in each.
export function rows(state, keep = () => true) {
  return Object.values(state.tasks).filter(keep).map(t => {
    const sit = situation(state, t);
    return {task: t, sit, column: columnOf(sit)};
  }).sort((a, b) => columns.indexOf(a.column) - columns.indexOf(b.column) || newest(a.task, b.task));
}

// board: rows by column.
export function board(state, keep) {
  const out = Object.fromEntries(columns.map(c => [c, []]));
  for (const r of rows(state, keep)) out[r.column].push(r);
  return out;
}

// inOrder: siblings in the order they go, a task after those it comes after, else by when it was made.
export function inOrder(list) {
  const made = [...list].sort((a, b) => String(a.created_at || '').localeCompare(String(b.created_at || '')) || a.id.localeCompare(b.id));
  const ids = new Set(made.map(t => t.id)), out = [], placed = new Set();
  while (out.length < made.length) {
    const next = made.find(t => !placed.has(t.id) && (t.after || []).every(a => !ids.has(a) || placed.has(a))) || made.find(t => !placed.has(t.id));
    placed.add(next.id);
    out.push(next);
  }
  return out;
}

// tree: the tasks as indented rows [{task, sit, depth, kids, open}], each under its parent. keep picks tasks; their
// ancestors come along so each shows where it sits. collapsed holds the ids whose subtasks are hidden.
export function tree(state, keep = () => true, collapsed = new Set()) {
  const all = state.tasks;
  const shown = new Set();
  for (const t of Object.values(all)) {
    if (!keep(t)) continue;
    for (let x = t; x && !shown.has(x.id); x = all[x.parent]) shown.add(x.id);
  }
  const under = new Map();
  for (const t of Object.values(all)) {
    if (!shown.has(t.id)) continue;
    const p = t.parent && all[t.parent] ? t.parent : '';
    if (!under.has(p)) under.set(p, []);
    under.get(p).push(t);
  }
  const out = [];
  const walk = (parent, depth) => {
    for (const t of inOrder(under.get(parent) || [])) {
      const kids = (under.get(t.id) || []).length;
      const open = !collapsed.has(t.id);
      out.push({task: t, sit: situation(state, t), depth, kids, open});
      if (kids && open) walk(t.id, depth + 1);
    }
  };
  walk('', 0);
  return out;
}

// drop is what dropping task on a board column does: {method, params, undo} (undo when task.undo can take it back),
// {why} when the board refuses it, or null when it is its own column. Only four moves are commands.
export function drop(state, task, to) {
  const from = columnOf(situation(state, task));
  if (from === to) return null;
  const set = status => ({method: 'task.set_status', params: {id: task.id, status}});
  const open = !!openRun(state, task.id);
  if (to === 'ended') {
    if (task.flow) return {why: 'flow'};
    if (task.status !== 'todo' || open) return {why: open ? 'open' : 'rule'};
    return {...set('done'), undo: true};
  }
  if (to === 'backlog') {
    if ((task.status === 'todo' && !open) || finished(task.status)) return {...set('backlog'), undo: true};
    return {why: open ? 'open' : 'rule'};
  }
  if (to === 'queued' || to === 'running') {
    if (task.status === 'backlog') return {method: 'task.start', params: {id: task.id}};
    return {why: 'rule'};
  }
  if (to === 'waiting' && finished(task.status)) return {...set('todo'), undo: true};
  return {why: 'rule'};
}

// ⚠️ How many directories each kind of candidate lists at most.
const dirsShown = 8;

// dirs are where a new task may work, in the order they are offered: its project's checkouts (on machine, or on every
// machine when none is picked), then the directories its project's tasks used, the latest first. Each is {dir, kind,
// machine}.
export function dirs(state, {project = '', machine = ''} = {}) {
  const out = [], seen = new Set();
  const add = (dir, kind, m = '') => {
    if (!dir || seen.has(dir)) return;
    seen.add(dir);
    out.push({dir, kind, machine: m});
  };
  const p = state.projects[project];
  for (const repo of p?.repos || []) {
    for (const [m, dir] of Object.entries(repo.dirs || {})) if (!machine || m === machine) add(dir, 'project', m);
  }
  const used = Object.values(state.tasks).filter(t => t.dir && (!project || t.project === project) && (!machine || !t.machine || t.machine === machine))
    .sort(newest);
  let n = 0;
  for (const t of used) {
    if (n >= dirsShown) break;
    if (!seen.has(t.dir)) n++;
    add(t.dir, 'recent', t.machine || '');
  }
  return out.slice(0, dirsShown * 2);
}

// defaults: the machine and agent a task runs with when it names none, from its project.
export function defaults(state, task) {
  const d = state.projects[task?.project]?.defaults || {};
  return {machine: task?.machine || d.machine || '', agent: task?.agent || d.agent || d.roles?.implement || '',
    fromProject: {machine: !task?.machine && !!d.machine, agent: !task?.agent && !!(d.agent || d.roles?.implement)}};
}

// runDir is where a run of task on machine works, as the coordinator picks it (task.Run's dir): the task's own
// directory, else its project's first checkout on that machine (project); null when there is neither.
export function runDir(state, task, machine) {
  if (task?.dir) return {dir: task.dir, project: false};
  const repo = (state.projects[task?.project]?.repos || []).find(r => machine && r.dirs?.[machine]);
  return repo ? {dir: repo.dirs[machine], project: true} : null;
}

// advice is what a dispatch of task meets on machine with profile, before the coordinator is asked: block (a code the
// dispatch cannot pass) or note (how it will go), each with its detail. Same rules as the coordinator's preview where
// the page knows enough; the preview answers the rest.
export function advice(state, task, machine, profile) {
  const run = openRun(state, task.id);
  if (run) return {block: 'open', detail: run.id};
  if (task.status !== 'todo') return {block: 'status', detail: task.status};
  if (!machine || !profile) return {block: 'pick'};
  const dir = runDir(state, task, machine.name)?.dir;
  if (!dir) return {block: 'dir'};
  if (profile.machine && profile.machine !== machine.name) return {block: 'pinned', detail: profile.machine};
  const check = machine.agents?.[profile.provider];
  if (check && !check.installed) return {block: 'cli', detail: profile.provider};
  if (check?.auth === 'missing') return {block: 'auth', detail: profile.provider};
  if (machine.state !== 'connected') return {note: 'offline', detail: machine.name};
  const busy = Object.values(state.runs).some(r => r.machine === machine.name && r.dir === dir && openStates.includes(r.state));
  if (busy || (machine.slots && machine.active >= machine.slots)) return {note: 'busy', detail: `${machine.active || 0}/${machine.slots || 0}`};
  return {};
}

// ⚠️ A plan's limits, as task.Plan.Check holds them.
export const PLAN_MAX = 50;
const planKey = /^[a-z0-9][a-z0-9_-]{0,39}$/;
const sizes = ['S', 'M', 'L'];

const planOf = plan => ({...plan, tasks: [...(plan?.tasks || [])]});

// planRows: a plan's tasks with their depth, each after the one it is part of.
export function planRows(plan) {
  const list = plan?.tasks || [];
  const keys = new Set(list.map(x => x.key));
  const out = [];
  for (const x of list) {
    if (x.parent && keys.has(x.parent)) continue;
    out.push({item: x, depth: 0});
    for (const c of list) if (c.parent === x.key) out.push({item: c, depth: 1});
  }
  for (const x of list) if (!out.some(r => r.item === x)) out.push({item: x, depth: 0});
  return out;
}

// planAdd adds a task to a plan under parent (a key, or none), with a key no other task has.
export function planAdd(plan, {parent = '', title = ''} = {}) {
  const next = planOf(plan);
  const keys = new Set(next.tasks.map(x => x.key));
  let n = next.tasks.length + 1;
  while (keys.has('task-' + n)) n++;
  next.tasks.push({key: 'task-' + n, title, ...(parent ? {parent} : {})});
  return next;
}

// planRemove takes a task out of a plan with the tasks that are part of it; what came after them no longer does.
export function planRemove(plan, key) {
  const gone = new Set([key, ...(plan?.tasks || []).filter(x => x.parent === key).map(x => x.key)]);
  const next = planOf(plan);
  next.tasks = next.tasks.filter(x => !gone.has(x.key)).map(x => {
    const after = (x.after || []).filter(a => !gone.has(a));
    const y = {...x};
    if (after.length) y.after = after; else delete y.after;
    return y;
  });
  return next;
}

// planEdit changes a task's fields; an empty one is left out, as the coordinator leaves it.
export function planEdit(plan, key, fields) {
  const next = planOf(plan);
  next.tasks = next.tasks.map(x => {
    if (x.key !== key) return x;
    const y = {...x, ...fields};
    for (const [k, v] of Object.entries(y)) if (k !== 'key' && k !== 'title' && (v === '' || v === undefined || (Array.isArray(v) && !v.length))) delete y[k];
    return y;
  });
  if (fields.key && fields.key !== key) {
    next.tasks = next.tasks.map(x => ({...x, ...(x.parent === key ? {parent: fields.key} : {}),
      ...(x.after?.includes(key) ? {after: x.after.map(a => (a === key ? fields.key : a))} : {})}));
  }
  return next;
}

// planCheck says what is wrong with a plan, as task.Plan.Check does: [{key, code, detail}] (key "" for the whole).
export function planCheck(plan) {
  const list = plan?.tasks || [];
  const out = [];
  if (!list.length) return [{key: '', code: 'empty'}];
  if (list.length > PLAN_MAX) out.push({key: '', code: 'many', detail: String(PLAN_MAX)});
  const by = new Map();
  for (const x of list) {
    const title = (x.title || '').trim();
    if (!planKey.test(x.key || '')) out.push({key: x.key, code: 'key'});
    else if (by.has(x.key)) out.push({key: x.key, code: 'twice'});
    else if (!title || new TextEncoder().encode(title).length > 1024) out.push({key: x.key, code: 'title'});
    else if (x.size && !sizes.includes(x.size)) out.push({key: x.key, code: 'size'});
    by.set(x.key, x);
  }
  if (out.length) return out;
  const under = (x, key) => { for (; x; x = by.get(x.parent)) if (x.key === key) return true; return false; };
  for (const x of list) {
    if (x.parent) {
      const p = by.get(x.parent);
      if (!p) out.push({key: x.key, code: 'parent', detail: x.parent});
      else if (p.parent) out.push({key: x.key, code: 'deep'});
    }
    for (const a of x.after || []) {
      const o = by.get(a);
      if (!o) out.push({key: x.key, code: 'after', detail: a});
      else if (under(x, a) || under(o, x.key)) out.push({key: x.key, code: 'own', detail: a});
    }
  }
  if (out.length) return out;
  const placed = new Set();
  for (let moved = true; moved;) {
    moved = false;
    for (const x of list) {
      if (placed.has(x.key) || !(x.after || []).every(a => placed.has(a))) continue;
      placed.add(x.key);
      moved = true;
    }
  }
  return placed.size < list.length ? [{key: '', code: 'cycle'}] : [];
}

// pageActs are the task actions the page draws, in its order: the coordinator's (task.Act*), sendBack (its last run's
// continue), abandon (its open run's, once that run's machine lost it) and copy, which is the page's own.
const pageActs = ['pass', 'rework', 'ack', 'keep', 'merge', 'sendBack', 'done', 'dispatch', 'start', 'stop', 'abandon', 'review', 'plan', 'backlog',
  'reopen', 'edit', 'move', 'child', 'copy', 'cancel'];

// actionsOf is what the page offers for task: {primary, more}, ids of pageActs. Which may be done is only what the
// coordinator says (aff, the viewer's affordances); the situation picks the one of them drawn as primary, and none
// when that one is not given.
export function actionsOf(state, aff, task) {
  const given = new Set(aff?.tasks?.[task.id]?.actions || []);
  const last = runsOf(state, task.id)[0];
  if (!task.flow && last && (aff?.runs?.[last.id] || []).includes('continue')) given.add('sendBack');
  const open = openRun(state, task.id);
  if (open?.state === 'unknown' && (aff?.runs?.[open.id] || []).includes('abandon')) given.add('abandon');
  given.add('copy');
  const sit = situation(state, task);
  const want = [
    [finished(task.status), 'reopen'],
    [task.status === 'backlog', 'start'],
    [!!open, 'stop'],
    [sit.reason === 'draft', 'review'],
    [!!task.flow && sit.reason === 'accept', 'pass'],
    [sit.reason === 'source_changed', 'ack'],
    [sit.reason === 'source_closed', 'keep'],
    [sit.reason === 'merge_conflict', 'merge'],
    [!task.flow && (sit.reason === 'ended' || sit.reason === 'accept'), 'done'],
    [!!task.flow && !task.auto, 'start'],
    [!task.flow && sit.kind === 'waiting', 'dispatch'],
  ].find(([holds]) => holds)?.[1];
  const primary = want && given.has(want) ? want : '';
  return {primary, more: pageActs.filter(id => id !== primary && given.has(id))};
}
