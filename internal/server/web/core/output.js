// output turns a conversation's events (internal/output's, docs/design/runs/output.md) into what the timeline draws:
// steps (a call with its result, a run of reads and searches, a message), turns, and at a density the rows to draw,
// which are open, and the turns folded into one line. It also finds text in them and says where the errors are. Pure
// functions, the same for the desktop, the phone and the tests. The densities, how each kind of step shows at them
// and the lines they cut at are internal/output's table (proto.js).
import {density as table} from './proto.js';

export const filters = ['all', 'talk', 'shell', 'edit', 'error'];

const toolKinds = new Set(['tool', 'cmd', 'edit', 'mcp']);

const grouped = e => e.kind === 'tool' && !e.request && (e.family === 'read' || e.family === 'search');

// results maps a call to the index of its result among events, as output.Results does.
export function results(events) {
  const calls = new Set(events.filter(e => e.call).map(e => e.call));
  const out = new Map();
  events.forEach((e, i) => { if (e.kind === 'tool_result' && calls.has(e.ref)) out.set(e.ref, i); });
  return out;
}

// items lays events out as output.Items does: a result whose call is among them belongs to the call; consecutive reads
// and searches under the same parent make one group; temp events are left out. The Go test runs both on the same events.
export function items(events) {
  const res = results(events);
  const out = [];
  let open = -1;
  events.forEach((e, i) => {
    if (e.temp || e.kind === 'tool_result' && res.get(e.ref) === i) return;
    if (grouped(e) && open >= 0 && (events[out[open].events[0]].parent || '') === (e.parent || '')) {
      out[open].group = true;
      out[open].events.push(i);
      out[open].ids.push(e.id);
      return;
    }
    out.push({id: e.id, group: false, events: [i], ids: [e.id]});
    open = grouped(e) ? out.length - 1 : -1;
  });
  return out;
}

const lines = s => (s ? String(s).split('\n') : []);

// failedOf tells an event, with its result, that went wrong.
const failedOf = (e, r) => !!(e.error || (e.exit !== undefined && e.exit !== null && e.exit !== 0) || r?.error || e.kind === 'error');

// planSteps are a plan's steps from the shapes internal/output leaves in input: claude's todos, codex's plan, exec's items.
export function planSteps(input) {
  const list = input?.todos || input?.plan || input?.items || [];
  if (!Array.isArray(list)) return [];
  return list.map(x => ({
    text: x.content ?? x.step ?? x.text ?? '',
    done: x.status === 'completed' || x.completed === true,
    now: x.status === 'in_progress',
  }));
}

// stepOf is what an item is as the timeline shows it. run is the run the events are of; open whether it still runs;
// pending the ids of the requests it still waits on; resolved the resolved events by request.
function stepOf(it, events, res, ctx) {
  const e = events[it.events[0]];
  const base = {key: it.id, id: e.id, run: ctx.run, turn: e.turn || 0, at: e.at || '', parent: e.parent || ''};
  if (it.group) {
    const members = it.events.map(i => events[i]);
    return {...base, kind: 'group', members: members.map(m => ({id: m.id, family: m.family, title: m.title || m.tool || ''})),
      reads: members.filter(m => m.family === 'read').length, searches: members.filter(m => m.family === 'search').length};
  }
  switch (e.kind) {
    case 'user': return {...base, kind: 'you', text: e.text || '', brief: ctx.first && it === ctx.firstItem};
    case 'you': return {...base, kind: 'you', text: e.text || '', input: e.input || '', by: e.by || '', mode: e.mode || ''};
    case 'say': return {...base, kind: 'say', text: e.text || ''};
    case 'think': return {...base, kind: 'think', text: e.text || ''};
    case 'result': return {...base, kind: 'result', text: e.text || '', error: !!e.error, usage: e.usage, cost: e.cost || 0, dur: e.dur_ms || 0};
    case 'error': return {...base, kind: 'error', text: e.text || '', failed: true};
    case 'sys': return {...base, kind: 'sys', name: e.name || '', text: e.text || '', warn: e.level === 'warning'};
    case 'raw': return {...base, kind: 'raw', text: e.text || ''};
    case 'mark': return {...base, kind: 'mark', name: e.name || e.event || '', phase: e.phase || '', event: e.event || ''};
    case 'gap': return {...base, key: 'gap:' + (e.from?.file || '') + ':' + (e.from?.off ?? ''), kind: 'gap', bytes: Math.max(0, (e.to?.off || 0) - (e.from?.off || 0))};
    case 'interrupt': return {...base, kind: 'interrupt', by: e.by || '', n: e.n || 0};
    case 'resolved': return {...base, kind: 'resolved', request: e.request || ''};
    case 'tool_result': return {...base, kind: 'output', output: e.output || '', failed: !!e.error, lines: e.lines || 0, truncated: e.truncated};
  }
  if (!toolKinds.has(e.kind)) return {...base, kind: 'raw', text: e.text || JSON.stringify(e)};
  const r = e.call && res.has(e.call) ? events[res.get(e.call)] : null;
  const family = e.kind === 'edit' ? 'edit' : e.kind === 'mcp' ? 'mcp' : e.family || 'other';
  const out = r ? r.output : e.output;
  const done = e.kind !== 'tool' || !!r;
  const step = {
    ...base, kind: family === 'ask' ? 'ask' : family === 'read' || family === 'search' ? 'group' : family, family,
    tool: e.tool || '', title: e.title || e.tool || '', more: e.more || 0, call: e.call || '', input: e.input,
    output: out || '', lines: (r ? r.lines : e.lines) || lines(out).length, dur: (r ? r.dur_ms : e.dur_ms) || 0,
    exit: r ? r.exit : e.exit, failed: failedOf(e, r), truncated: (r || e).truncated,
    diff: e.diff || '', files: e.files || [],
    state: done ? (failedOf(e, r) ? 'failed' : 'ok') : ctx.open ? 'running' : 'unknown',
  };
  if (step.kind === 'group') return {...step, members: [{id: e.id, family, title: step.title}], reads: family === 'read' ? 1 : 0, searches: family === 'search' ? 1 : 0};
  if (family === 'plan') step.plan = planSteps(e.input);
  if (family === 'ask') {
    step.request = e.request || '';
    step.questions = (e.input?.questions || []).map(q => q.question || '').filter(Boolean);
    step.pending = !!step.request && ctx.pending.has(step.request);
    step.resolved = ctx.resolved.get(step.request) || null;
  }
  return step;
}

// runModel is one run's steps by turn. events are the run's in order; run the run as the state holds it; taken the
// messages of the run it continues that it carries (its takes), which its first prompt is.
function runModel(run, events, {first = true, head = null, taken = []} = {}) {
  const open = ['queued', 'starting', 'running', 'unknown'].includes(run?.state);
  const pending = new Set(open ? (run?.requests || []).map(q => q.id) : []);
  const resolved = new Map(events.filter(e => e.kind === 'resolved' && e.request).map(e => [e.request, e]));
  const laid = items(events);
  const res = results(events);
  const firstItem = laid.find(it => events[it.events[0]].kind === 'user');
  const ctx = {run: run?.id || '', open, pending, resolved, first, firstItem};
  const all = laid.flatMap(it => (it === firstItem && taken.length ? carried(it, events[it.events[0]], taken, ctx)
    : [stepOf(it, events, res, ctx)])).filter(s => s.kind !== 'resolved');
  const byParent = new Map();
  for (const s of all) if (s.parent) byParent.set(s.parent, [...(byParent.get(s.parent) || []), s]);
  const calls = new Set(all.map(s => s.call).filter(Boolean));
  for (const s of all) if (s.kind === 'agent') s.kids = byParent.get(s.call) || [];
  const top = all.filter(s => !s.parent || !calls.has(s.parent));
  const turns = [];
  let turn = null;
  for (const s of top) {
    const n = s.turn || (turn ? turn.n : 1);
    if (!turn || n !== turn.n) turns.push(turn = {key: `${ctx.run}:t${n}`, run: ctx.run, n, steps: []});
    turn.steps.push(s);
  }
  const temps = events.filter(e => e.temp && (e.text || e.title)).map(e => tempStep(e, ctx));
  const shown = new Set(events.filter(e => e.kind === 'you' && e.input).map(e => e.input));
  const sends = (run?.sends || []).filter(m => !shown.has(m.id) && (m.state === 'queued' || m.state === 'failed'))
    .map(m => ({key: 'send:' + m.id, kind: 'send', id: m.id, text: m.text, state: m.state, run: ctx.run}));
  return {run, id: ctx.run, open, turns, temps, sends, head};
}

// tempStep is a temp event as the step it will be once final: what the agent is writing (say, think; a half line reads
// as say) or a codex command still running, with the last lines of its output and the bytes it printed.
function tempStep(e, ctx) {
  const base = {key: 'temp:' + e.key, run: ctx.run, turn: e.turn || 0, parent: e.parent || '', temp: true};
  if (e.kind === 'think') return {...base, kind: 'think', text: e.text || ''};
  if (e.kind !== 'cmd') return {...base, kind: 'say', text: e.text || ''};
  return {...base, kind: 'shell', family: 'shell', tool: e.tool || '', title: e.title || e.tool || '', more: e.more || 0, call: e.call || '',
    input: e.input, output: e.output || '', bytes: e.bytes || 0, lines: 0, dur: 0, failed: false, state: 'running'};
}

// carried are the steps of a continuation's first prompt: one you per message it carries, as its sender sent it (the
// coordinator joins their texts into the prompt).
function carried(it, e, taken, ctx) {
  return taken.map((m, i) => ({key: i ? `${it.id}/${i}` : it.id, id: e.id, run: ctx.run, turn: e.turn || 0, at: m.at || e.at || '', parent: '',
    kind: 'you', text: m.text || '', input: m.id, by: m.by || '', mode: m.mode || ''}));
}

// model is a conversation: parts [{run, events, head, taken}] from its first run to its last. head says what is before
// the loaded events: {more: true} while an earlier page can be fetched, {gone: true} when the run's output was cleared;
// taken are the messages of the run before that the run carries.
export function model(parts) {
  const runs = parts.map((p, i) => runModel(p.run, p.events || [], {first: i === 0, head: p.head || null, taken: p.taken || []}));
  let plan = null;
  for (const r of runs) for (const t of r.turns) for (const s of t.steps) if (s.kind === 'plan' && s.plan.length) plan = s;
  return {runs, plan};
}

// summaryOf is what a turn did beside talking: commands, files changed, reads and searches, other calls, and steps.
export function summaryOf(steps) {
  const sum = {steps: 0, shell: 0, files: 0, reads: 0, searches: 0, other: 0, failed: 0};
  const files = new Set();
  for (const s of steps) {
    if (['you', 'say', 'result', 'mark', 'sys', 'raw', 'gap', 'interrupt'].includes(s.kind)) continue;
    sum.steps++;
    if (s.failed) sum.failed++;
    if (s.kind === 'shell') sum.shell++;
    else if (s.kind === 'edit') { for (const f of s.files.length ? s.files : [s.title.replace(/ [+−-]\d+.*$/, '')]) files.add(f); }
    else if (s.kind === 'group') { sum.reads += s.reads; sum.searches += s.searches; }
    else if (s.kind !== 'think' && s.kind !== 'plan') sum.other++;
  }
  sum.files = files.size;
  return sum;
}

// foldOf is the one line a finished turn folds into: how long, how many steps, files changed, and its last words.
function foldOf(t) {
  const sum = summaryOf(t.steps);
  const last = [...t.steps].reverse().find(s => s.kind === 'say' || s.kind === 'result' && s.text);
  const result = t.steps.find(s => s.kind === 'result');
  const ats = t.steps.map(s => Date.parse(s.at)).filter(x => !Number.isNaN(x));
  const dur = result?.dur || (ats.length > 1 ? ats[ats.length - 1] - ats[0] : 0);
  return {...sum, dur, last: last ? lines(last.text)[0] : ''};
}

const talk = s => ['you', 'say', 'result', 'ask', 'error', 'interrupt'].includes(s.kind);
const passes = (s, filter) => filter === 'all' || filter === 'talk' && talk(s) || filter === 'shell' && s.kind === 'shell' ||
  filter === 'edit' && s.kind === 'edit' || filter === 'error' && !!s.failed;

// autoOpen: failures, the command that still runs and a question not yet answered open by themselves at any density.
const autoOpen = s => !!s.failed || s.kind === 'shell' && s.state === 'running' || s.kind === 'ask' && s.pending;

// showOf is how a step shows at a density, from the table; a kind it lacks shows as other.
const showOf = (s, density) => {
  const i = Math.max(0, table.names.indexOf(density));
  return (table.show[s.kind] || table.show.other)[i];
};

// openByDensity is whether a step starts open at a density, before failures and what the viewer did.
const openByDensity = (s, density) => showOf(s, density) === 'open';

// shownAt tells whether a step has its own row at a density: the rest go into the turn's one-line summary (sum) or
// are not shown.
function shownAt(s, density) {
  switch (showOf(s, density)) {
    case 'row': case 'open': return true;
    case 'sum': return !!s.failed;
    case 'warn': return !!s.warn;
    case 'hook': return s.event === 'hook';
    case 'note': return !!s.error || !!s.usage || !!s.cost;
  }
  return false;
}

// lay gives the rows to draw for a model at a density: {density, filter, open: Map key → bool (what the viewer set),
// peek: Set of keys open for now (a find hit, its turn, the agents it is in)}. A row is {type, key, depth, ...}: run
// (a later run of the conversation begins), head (what is before the loaded events), turn (a finished turn folded),
// step (with open and clip), summary (a brief turn's other steps), temp (a step still being written, by the same
// rules; a running command at any density), send.
export function lay(m, {density = 'standard', filter = 'all', open = new Map(), peek = new Set()} = {}) {
  const rows = [];
  const isOpen = (key, byDefault) => peek.has(key) || (open.has(key) ? open.get(key) : byDefault);
  const lastTurn = [...m.runs].reverse().find(r => r.turns.length)?.turns.at(-1).key;
  const push = (s, depth) => {
    const o = isOpen(s.key, autoOpen(s) || openByDensity(s, density));
    const clip = s.kind === 'say' && density === 'standard' && !o && lines(s.text).length > table.sayFold;
    const brief = s.kind === 'you' && s.brief && density !== 'detailed' && !o;
    rows.push({type: 'step', key: s.key, depth, step: s, open: o, auto: autoOpen(s), manual: open.get(s.key) === true || peek.has(s.key), clip, brief});
    if (s.kind === 'agent' && o) for (const k of s.kids || []) if (passes(k, filter) && shownAt(k, density)) push(k, depth + 1);
  };
  m.runs.forEach((r, i) => {
    if (i === 0 && r.head) rows.push({type: 'head', key: 'head:' + r.id, ...r.head});
    if (i > 0) rows.push({type: 'run', key: 'run:' + r.id, run: r.run, n: i + 1});
    if (i > 0 && r.head?.gone) rows.push({type: 'head', key: 'head:' + r.id, gone: true});
    for (const t of r.turns) {
      if (filter !== 'all') { for (const s of t.steps) if (passes(s, filter)) push(s, 0); continue; }
      if (t.key !== lastTurn && !isOpen(t.key, false)) { rows.push({type: 'turn', key: t.key, n: t.n, run: r.id, ...foldOf(t)}); continue; }
      if (t.key !== lastTurn) rows.push({type: 'turn', key: t.key, n: t.n, run: r.id, open: true, ...foldOf(t)});
      let summary = null;
      for (const s of t.steps) {
        if (shownAt(s, density) || peek.has(s.key)) { push(s, 0); continue; }
        if (showOf(s, density) !== 'sum') continue;
        if (!summary) rows.push(summary = {type: 'summary', key: 'sum:' + t.key, steps: []});
        summary.steps.push(s);
      }
      if (summary) Object.assign(summary, summaryOf(summary.steps));
    }
    for (const s of r.temps) {
      if (passes(s, filter) && (shownAt(s, density) || autoOpen(s))) {
        rows.push({type: 'temp', key: s.key, depth: 0, step: s, text: s.text, open: isOpen(s.key, autoOpen(s) || openByDensity(s, density))});
      }
    }
    if (filter === 'all') for (const x of r.sends) rows.push({type: 'send', key: x.key, ...x});
  });
  return rows;
}

// counted are the keys of the rows that count as steps for "N new": everything a viewer would call a step, not a
// temp event, a pending message, a run's or a turn's heading.
export const counted = rows => rows.filter(r => r.type === 'step' && r.depth === 0 || r.type === 'summary').map(r => r.key);

// waits are the keys of rows that want the viewer: a question not yet answered, a failure.
export const waits = rows => rows.filter(r => r.type === 'step' && (r.step.kind === 'ask' && r.step.pending || r.step.failed)).map(r => r.key);

// ⚠️ Find folds case in ASCII only, so a CJK or accented text is matched as written.
const fold = s => String(s || '').replace(/[A-Z]/g, c => c.toLowerCase());

// textOf is what find searches in a step: what it said, its title, the head and tail of its output, a diff's preview,
// a question. Cut middles and whole files moved to blobs are not in the events, so they are not searched.
export function textOf(s) {
  const parts = [s.text, s.title, s.output, s.diff, ...(s.questions || []), ...(s.members || []).map(m => m.title), ...(s.plan || []).map(p => p.text)];
  if (s.input?.command && typeof s.input.command === 'string') parts.push(s.input.command);
  return parts.filter(Boolean).join('\n');
}

// find is where q is in a model, in timeline order, within a filter: [{key, turn, agents, run}] — the step, the turn
// it is in and the agents it is under, which lay's peek opens while the hit is shown.
export function find(m, q, {filter = 'all'} = {}) {
  const needle = fold(q.trim());
  if (!needle) return [];
  const hits = [];
  const walk = (s, turn, agents, run) => {
    if (passes(s, filter) && fold(textOf(s)).includes(needle)) hits.push({key: s.key, turn, agents, run});
    for (const k of s.kids || []) walk(k, turn, [...agents, s.key], run);
  };
  for (const r of m.runs) for (const t of r.turns) for (const s of t.steps) walk(s, t.key, [], r.id);
  return hits;
}

// peekOf is what a hit opens: its step, its turn and the agents it is under.
export const peekOf = hit => new Set(hit ? [hit.key, hit.turn, ...hit.agents] : []);

// errors are the failed steps in timeline order, as hits (for "next error").
export function errors(m) {
  const out = [];
  const walk = (s, turn, agents, run) => {
    if (s.failed) out.push({key: s.key, turn, agents, run});
    for (const k of s.kids || []) walk(k, turn, [...agents, s.key], run);
  };
  for (const r of m.runs) for (const t of r.turns) for (const s of t.steps) walk(s, t.key, [], r.id);
  return out;
}

// nextOf is the hit after the one at key (the first when none is), wrapping; step -1 goes back.
export function nextOf(hits, key, step = 1) {
  if (!hits.length) return null;
  const i = hits.findIndex(h => h.key === key);
  if (i < 0) return step > 0 ? hits[0] : hits[hits.length - 1];
  return hits[(i + step + hits.length) % hits.length];
}

// ends is the head and tail of an output: all of it when it has at most 2n lines, else n, a gap, n.
export function ends(text, n) {
  const ls = lines(text);
  if (ls.length <= 2 * n) return {head: ls, cut: 0, tail: []};
  return {head: ls.slice(0, n), cut: ls.length - 2 * n, tail: ls.slice(-n)};
}

// tail is the last n lines of a text.
export const tail = (text, n) => lines(text).slice(-n);

// cut is a text's first n lines and how many more there are.
export function cut(text, n) {
  const ls = lines(text);
  return {lines: ls.slice(0, n), more: Math.max(0, ls.length - n)};
}

// editLines is what an edit step shows open: its diff, or the old and new text of a claude edit, as diff lines.
export function editLines(s) {
  if (s.diff) return lines(s.diff);
  const i = s.input || {};
  const pairs = i.edits || [{old_string: i.old_string, new_string: i.new_string ?? i.content ?? i.new_source}];
  return pairs.flatMap(p => [...lines(p.old_string).map(l => '-' + l), ...lines(p.new_string).map(l => '+' + l)]);
}
