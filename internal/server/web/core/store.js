// store holds what the page shows, fed by the wire's watches. The state is folded in place; each of its tables has a
// version signal that goes up when a fold touches it, and derived data reads the versions. Machines and the inbox are
// replaced whole. Run output is watched only while something holds it.
import {signal, batch} from '../vendor/signals-core.mjs';
import * as fold from './fold.js';
import {code} from './proto.js';

// tables are task.State's maps by their JSON names: what state.watch's snapshot parts carry.
export const tables = ['tasks', 'runs', 'projects', 'shares', 'agent_defs'];

// ⚠️ What the viewer may do, computed per viewer by the coordinator (T3.3): a snapshot part and a push of the same
// shape, {runs: {id: [action]}, tasks: {id: {actions, route}}}, never folded from the journal.
const perViewer = 'affordances';
const noAffordances = () => ({runs: {}, tasks: {}});

// ⚠️ A page of output is this many events; the pages fetched before a conversation's live output are dropped past
// OUTPUT_BYTES (as JSON), earliest first, once the view is back at the bottom.
export const PAGE_EVENTS = 200;
export const OUTPUT_BYTES = 32 << 20;

// fileOf is the log an event id is in: the id is file:off:n and the file (dev:ino) has colons of its own.
export const fileOf = id => String(id).split(':').slice(0, -2).join(':');

const empty = () => ({seq: 0, tasks: {}, runs: {}, projects: {}, shares: {}, agent_defs: {}});

// mergeAff puts what a push or a part says of each run and task into into; null takes one out.
function mergeAff(into, p) {
  for (const k of ['runs', 'tasks']) {
    for (const [id, v] of Object.entries(p?.[k] || {})) {
      if (v === null) delete into[k][id]; else into[k][id] = v;
    }
  }
}

// createStore: frame schedules a redraw's worth of version bumps (requestAnimationFrame on the page).
export function createStore({wire, frame = fn => globalThis.requestAnimationFrame(fn), onError = e => console.warn(e)}) {
  const state = empty();
  const rev = Object.fromEntries(tables.map(k => [k, signal(0)]));
  // phase: idle before the watch; snapshot while the parts arrive; live once they are all in.
  const phase = signal('idle');
  const machines = signal([]), inbox = signal([]);
  const affordances = signal(noAffordances());
  let stagedAff = null;
  const session = signal(null), prefs = signal({});
  let staged = null, briefs = true, dirty = new Set(), scheduled = false, watches = [];
  const outputs = new Map();

  function touch(parts) {
    for (const p of parts) dirty.add(p);
    if (scheduled) return;
    scheduled = true;
    frame(() => {
      scheduled = false;
      const d = dirty;
      dirty = new Set();
      batch(() => { for (const p of d) rev[p].value++; });
    });
  }

  function replace(next) {
    for (const k of tables) state[k] = next[k];
    state.seq = next.seq;
    touch(tables);
  }

  function onState(method, p) {
    switch (method) {
      case 'open':
        if (p.mode === 'snapshot') { staged = empty(); stagedAff = noAffordances(); phase.value = 'snapshot'; }
        return;
      case 'reset':
        staged = empty();
        stagedAff = noAffordances();
        phase.value = 'snapshot';
        return;
      case 'snapshot':
        if (!staged) throw new Error('store: a snapshot part outside a snapshot');
        if (p.part === perViewer) { mergeAff(stagedAff, p.items); return; }
        if (!tables.includes(p.part)) throw new Error(`store: no table ${p.part}`);
        Object.assign(staged[p.part], p.items || {});
        return;
      case 'live':
        if (staged) {
          staged.seq = p.seq;
          replace(staged);
          staged = null;
          affordances.value = stagedAff || noAffordances();
          stagedAff = null;
        }
        phase.value = 'live';
        return;
      case perViewer: {
        if (stagedAff) { mergeAff(stagedAff, p); return; }
        const next = {runs: {...affordances.value.runs}, tasks: {...affordances.value.tasks}};
        mergeAff(next, p);
        affordances.value = next;
        return;
      }
      case 'journal': {
        const into = staged || state;
        if (p.seq <= into.seq) return;
        fold.apply(into, p);
        if (!staged) touch(new Set(p.events.flatMap(e => fold.parts[e.type] || [])));
        return;
      }
    }
  }

  // watchState opens state.watch; it resumes after the last applied seq once it has been live.
  function watchState() {
    let w;
    w = wire.watch('state.watch', {
      params: () => {
        const p = {};
        if (phase.value === 'live' && state.seq > 0) p.after_seq = state.seq;
        if (!briefs) p.no_briefs = true;
        return p;
      },
      onPush: (m, p) => {
        try { onState(m, p); } catch (e) {
          // an envelope this state cannot take: start over from a snapshot
          onError(e);
          phase.value = 'idle';
          staged = null;
          w.cancel();
          watchState();
        }
      },
      onEnd: err => { if (err && err.code !== code.canceled) onError(err); },
    });
    watches.push(w);
  }

  const whole = (method, into) => {
    watches.push(wire.watch(method, {
      onPush: (m, p) => { if (m !== 'open') into.value = p.items || []; },
      onEnd: err => { if (err && err.code !== code.canceled) onError(err); },
    }));
  };

  // output is a run's events while held: release it when done. watch opens run.output.watch (a server without it
  // gives the last page instead); either way more() fetches the page before the earliest event held, and head says
  // what is before them: {more} while there is, {start} at the run's start, {gone} when it was cleared, loading while a
  // page is out. Events with a key replace the one before them with that key; the cursor moves only as a push says.
  function output(run, {watch = true} = {}) {
    let o = outputs.get(run);
    if (!o) {
      o = {refs: 0, cursor: null, events: signal([]), keys: new Map(), done: signal(null), head: signal({more: true}), older: [], back: null, watched: watch};
      const apply = (m, p) => {
        const list = [...o.events.value];
        if (m === 'open') {
          if (p.mode === 'snapshot') { list.length = 0; o.keys.clear(); }
          if (p.mode === 'gap') list.push({kind: 'gap', from: p.from, to: p.to});
          if (p.cursor) o.cursor = p.cursor;
        } else if (m === 'run.output') {
          for (const ev of p.events || []) {
            const at = ev.key !== undefined ? o.keys.get(ev.key) : undefined;
            if (at !== undefined) list[at] = ev;
            else {
              if (ev.key !== undefined) o.keys.set(ev.key, list.length);
              list.push(ev);
            }
          }
          if (p.cursor) o.cursor = p.cursor;
        } else return;
        o.events.value = list;
      };
      if (watch) {
        o.watch = wire.watch('run.output.watch', {
          pausable: true,
          params: () => (o.cursor ? {run, from: o.cursor} : {run}),
          onPush: apply,
          onEnd: err => {
            if (err?.code === code.unsupported) { o.watched = false; if (!o.events.value.length) more(o, run); return; }
            o.done.value = err ? err.code : 'done';
          },
        });
      }
      outputs.set(run, o);
    }
    o.refs++;
    let held = true;
    return {
      events: o.events, done: o.done, head: o.head,
      more: () => more(o, run),
      // trim drops the earliest fetched pages past OUTPUT_BYTES, for a view back at the bottom.
      trim: () => trim(o),
      release() {
        if (!held) return;
        held = false;
        if (--o.refs > 0) return;
        o.watch?.cancel();
        outputs.delete(run);
      },
    };
  }

  // more fetches the page before the earliest event o holds (the end of the log when it holds none), after the last
  // log generation's start from the one before it.
  function more(o, run) {
    const h = o.head.value;
    if (h.loading || h.start || h.gone) return Promise.resolve();
    const first = o.events.value.find(e => e.id);
    const params = o.back || (first ? {run, before: first.off, file: fileOf(first.id)} : {run, before: -1});
    o.head.value = {...h, loading: true};
    return wire.call('run.output.page', {...params, run, n: PAGE_EVENTS}).then(p => {
      const have = new Set(o.events.value.map(e => e.id).filter(Boolean));
      const got = (p?.events || []).filter(e => !e.id || !have.has(e.id)).filter(e => !e.temp || !o.events.value.length);
      const bytes = JSON.stringify(got).length;
      o.older.unshift({n: got.length, bytes});
      if (!o.watched && !o.events.value.length) o.cursor = null;
      o.events.value = [...got, ...o.events.value];
      const atStart = (p?.from ?? 0) <= (p?.earliest ?? 0);
      o.back = atStart && p?.prev ? {run, file: p.prev, before: -1} : atStart ? null : {run, before: p.from, file: p.file};
      o.head.value = atStart && !p?.prev ? {start: true} : {more: true};
    }, e => {
      if (e.code === code.stale) { o.back = null; o.head.value = {more: true}; return; }
      o.head.value = e.code === code.gone || e.code === code.notFound ? {gone: true} : {more: true, failed: e.code || 'error'};
    });
  }

  function trim(o) {
    let total = o.older.reduce((a, x) => a + x.bytes, 0);
    let drop = 0;
    while (o.older.length && total > OUTPUT_BYTES) {
      const x = o.older.shift();
      total -= x.bytes;
      drop += x.n;
    }
    if (!drop) return;
    const rest = o.events.value.slice(drop);
    o.events.value = rest;
    o.back = null;
    o.head.value = {more: true};
  }

  // briefOf is a task's brief as the state holds it: undefined while state.watch leaves briefs out and none was fetched.
  function briefOf(id) {
    const t = state.tasks[id];
    if (!t) return undefined;
    return t.brief !== undefined ? t.brief : briefs ? '' : undefined;
  }

  // brief is a task's brief, fetched with task.get when state.watch left it out.
  async function brief(id) {
    const t = state.tasks[id];
    if (briefOf(id) !== undefined) return briefOf(id);
    const got = await wire.call('task.get', {id});
    const now = state.tasks[id];
    if (now && now.brief === undefined) {
      now.brief = got?.brief ?? '';
      touch(['tasks']);
    }
    return got?.brief ?? '';
  }

  return {
    // raw is the last page of a run's output as its log has it.
    raw: run => wire.call('run.output.page', {run, before: -1, n: PAGE_EVENTS, raw: true}).then(p => p?.raw ?? ''),
    state, rev, phase, machines, inbox, affordances, session, prefs, brief, briefOf, output,
    // start opens the watches; noBriefs leaves the tasks' briefs out of the state (brief fetches one).
    start({noBriefs = false} = {}) {
      briefs = !noBriefs;
      watchState();
      whole('machines.watch', machines);
      whole('inbox.watch', inbox);
    },
    stop() {
      for (const w of watches.splice(0)) w.cancel();
      for (const o of outputs.values()) o.watch.cancel();
      outputs.clear();
      phase.value = 'idle';
    },
    situation: t => fold.situation(state, t),
    open: run => outputs.has(run) ? outputs.get(run).refs : 0,
  };
}
