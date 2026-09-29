// store holds what the page shows, fed by the wire's watches. The state is folded in place; each of its tables has a
// version signal that goes up when a fold touches it, and derived data reads the versions. Machines and the inbox are
// replaced whole. Run output is watched only while something holds it.
import {signal, batch} from '../vendor/signals-core.mjs';
import * as fold from './fold.js';

// tables are task.State's maps by their JSON names: what state.watch's snapshot parts carry.
export const tables = ['tasks', 'runs', 'projects', 'shares', 'agent_defs'];

// ⚠️ Parts computed per viewer (T3.3); taken without being folded until the page uses them.
const reserved = ['affordances'];

const empty = () => ({seq: 0, tasks: {}, runs: {}, projects: {}, shares: {}, agent_defs: {}});

// createStore: frame schedules a redraw's worth of version bumps (requestAnimationFrame on the page).
export function createStore({wire, frame = fn => globalThis.requestAnimationFrame(fn), onError = e => console.warn(e)}) {
  const state = empty();
  const rev = Object.fromEntries(tables.map(k => [k, signal(0)]));
  // phase: idle before the watch; snapshot while the parts arrive; live once they are all in.
  const phase = signal('idle');
  const machines = signal([]), inbox = signal([]);
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
        if (p.mode === 'snapshot') { staged = empty(); phase.value = 'snapshot'; }
        return;
      case 'reset':
        staged = empty();
        phase.value = 'snapshot';
        return;
      case 'snapshot':
        if (!staged) throw new Error('store: a snapshot part outside a snapshot');
        if (reserved.includes(p.part)) return;
        if (!tables.includes(p.part)) throw new Error(`store: no table ${p.part}`);
        Object.assign(staged[p.part], p.items || {});
        return;
      case 'live':
        if (staged) {
          staged.seq = p.seq;
          replace(staged);
          staged = null;
        }
        phase.value = 'live';
        return;
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
      onEnd: err => { if (err && err.code !== 'canceled') onError(err); },
    });
    watches.push(w);
  }

  const whole = (method, into) => {
    watches.push(wire.watch(method, {
      onPush: (m, p) => { if (m !== 'open') into.value = p.items || []; },
      onEnd: err => { if (err && err.code !== 'canceled') onError(err); },
    }));
  };

  // output is a run's events while held: release it when done. Events with a key replace the one before them with
  // that key; the cursor moves only as a push says.
  function output(run) {
    let o = outputs.get(run);
    if (!o) {
      o = {refs: 0, cursor: null, events: signal([]), keys: new Map(), done: signal(null)};
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
      o.watch = wire.watch('run.output.watch', {
        pausable: true,
        params: () => (o.cursor ? {run, from: o.cursor} : {run}),
        onPush: apply,
        onEnd: err => { o.done.value = err ? err.code : 'done'; },
      });
      outputs.set(run, o);
    }
    o.refs++;
    let held = true;
    return {
      events: o.events, done: o.done,
      release() {
        if (!held) return;
        held = false;
        if (--o.refs > 0) return;
        o.watch.cancel();
        outputs.delete(run);
      },
    };
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
    state, rev, phase, machines, inbox, session, prefs, brief, briefOf, output,
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
