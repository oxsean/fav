import {createWire} from '../web/core/wire.js';
import {createStore} from '../web/core/store.js';
import {clock, server} from './fake.js';
import {test, eq, ok, run} from './check.js';

function rig() {
  const clk = clock(), srv = server(clk);
  const wire = createWire({url: 'ws://tend.test/client', open: srv.open, timers: clk, random: () => 1});
  const frames = [], errors = [];
  const store = createStore({wire, frame: fn => frames.push(fn), onError: e => errors.push(String(e.message || e))});
  const flush = () => { for (const fn of frames.splice(0)) fn(); };
  const revs = () => Object.fromEntries(Object.entries(store.rev).map(([k, v]) => [k, v.value]));
  wire.start();
  return {srv, wire, store, flush, revs, errors};
}

test('state.watch: snapshot parts, live, envelopes, reset', async () => {
  const {srv, store, flush, revs, errors} = rig();
  let before, live, atLive;
  await srv.play('state-snapshot', {
    start() { store.start(); },
    parts() {
      flush();
      before = {phase: store.phase.value, tasks: Object.keys(store.state.tasks), revs: revs()};
    },
    live() {
      flush();
      atLive = structuredClone(store.state);
      live = {phase: store.phase.value, seq: store.state.seq, tasks: Object.keys(store.state.tasks).sort(), revs: revs(),
        title: store.state.tasks.t1.title, r1: store.state.runs.r1.last, machines: store.machines.value, inbox: store.inbox.value};
    },
  });
  flush();
  eq(before, {phase: 'snapshot', tasks: [], revs: {tasks: 0, runs: 0, projects: 0, shares: 0, agent_defs: 0}}, 'while the parts arrive');
  eq(live, {phase: 'live', seq: 13, tasks: ['t1', 't2', 't3'], title: 'Pay timeout on retry', r1: 'go test ./...',
    revs: {tasks: 1, runs: 1, projects: 1, shares: 1, agent_defs: 1},
    machines: [{name: 'mba', state: 'connected', slots: 2, active: 1, queued: 0}], inbox: [{task: 't1', reason: 'dispatch'}]}, 'live');
  eq(revs(), {tasks: 2, runs: 2, projects: 2, shares: 2, agent_defs: 2}, 'versions after the reset');
  eq(errors, [], 'errors');
  return {live: atLive, final: store.state};
});

test('versions go up once a frame, for the tables an envelope touched', async () => {
  const {srv, store, flush, revs} = rig();
  const seen = [];
  await srv.play('state-snapshot', {
    start() { store.start(); },
    parts() {},
    live() { seen.push(revs()); flush(); seen.push(revs()); },
  });
  eq(seen, [{tasks: 0, runs: 0, projects: 0, shares: 0, agent_defs: 0}, {tasks: 1, runs: 1, projects: 1, shares: 1, agent_defs: 1}], 'one bump for the snapshot and all envelopes of the frame');
});

test('no_briefs: a brief is fetched when asked for; the watch resumes after the applied seq', async () => {
  const {srv, store, flush} = rig();
  let brief;
  await srv.play('state-resume', {
    start() { store.start({noBriefs: true}); },
    brief() { brief = store.brief('t1'); },
  });
  flush();
  eq(await brief, 'Fix the timeout.', 'the brief');
  eq([store.state.tasks.t1.brief, store.state.tasks.t1.title, store.state.seq], ['Fix the timeout.', 'Pay timeout, again', 6], 'the task');
  eq(await store.brief('t1'), 'Fix the timeout.', 'asked again, from the state');
});

test('run output: one watch per run, keyed events replaced, a gap marked, the last release cancels', async () => {
  const {srv, store} = rig();
  const held = [];
  let seen, r2;
  await srv.play('output', {
    hold() { held.push(store.output('r1'), store.output('r1')); },
    gap() { seen = held[0].events.value.map(e => [e.id || '', e.key || '', e.text || e.tool]); r2 = store.output('r2'); },
    release() { held.pop().release(); },
  });
  eq(seen, [['a1:9:0:0', '', '先跑一遍测试。'], ['a1:9:40:0', 'item_7', '正在写这一句。'], ['a1:9:90:0', '', 'Bash']], 'r1');
  eq(r2.events.value, [{kind: 'gap', from: {file: 'b2:1', off: 0}, to: {file: 'b2:1', off: 900}}], 'r2');
  eq([store.open('r1'), store.open('r2')], [0, 1], 'holders');
});

test('a push the state cannot take starts over from a snapshot', async () => {
  const {srv, store, errors} = rig();
  await srv.play('state-restart', {start() { store.start(); }});
  eq(errors.length, 2, 'errors');
  ok(/t404/.test(errors[0]) && /nope/.test(errors[1]), errors.join('; '));
  eq(store.phase.value, 'idle', 'phase');
});

test('find asks the node where it left off, until a hit, the log\'s start or a few rounds', async () => {
  const asked = [];
  const answers = [];
  const wire = {has: m => m === 'run.output.find', call: (m, p) => { asked.push([m, p]); return Promise.resolve(answers.shift()); }};
  const store = createStore({wire, frame: fn => fn(), onError: () => {}});
  ok(store.canFind(), 'the server can look');
  answers.push({hits: [], file: 'a', next: {file: 'a', before: 500}}, {hits: [{id: 'r1/a:10:0'}], file: 'a'});
  eq(await store.find('r1', 'coupon'), true, 'a hit past where the node stopped');
  eq(asked.map(([, p]) => p), [{run: 'r1', q: 'coupon', before: -1, limit: 1}, {run: 'r1', q: 'coupon', before: 500, file: 'a', limit: 1}], 'from the end, then on');
  asked.length = 0;
  answers.push({hits: [], file: 'a'});
  eq([await store.find('r1', 'coupon'), asked.length], [false, 1], 'none at the log\'s start');
  for (let i = 0; i < 20; i++) answers.push({hits: [], file: 'a', next: {file: 'a', before: 10}});
  asked.length = 0;
  eq([await store.find('r1', 'coupon'), asked.length], [false, 8], 'a few rounds at most');
});

run();
