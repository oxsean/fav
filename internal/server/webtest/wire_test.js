import {createWire} from '../web/core/wire.js';
import {clock, server, settle} from './fake.js';
import {test, eq, ok, run} from './check.js';

function rig() {
  const clk = clock(), srv = server(clk);
  const wire = createWire({url: 'ws://tend.test/client', open: srv.open, timers: clk, random: () => 1});
  wire.start();
  return {clk, srv, wire};
}

// outcome records how a promise settles, without waiting for it.
function outcome(p) {
  const o = {};
  p.then(v => { o.value = v; }, e => { o.code = e.code; o.detail = e.detail; });
  return o;
}

function recorder() {
  const r = {pushes: [], ends: []};
  r.onPush = (m, p) => r.pushes.push([m, p]);
  r.onEnd = err => r.ends.push(err ? err.code : 'done');
  return r;
}

test('hello opens the connection', async () => {
  const {srv, wire} = rig();
  eq(wire.status.value, 'connecting', 'status before');
  await srv.play('hello');
  eq(wire.status.value, 'open', 'status');
  ok(wire.has('state.watch') && !wire.has('nope'), 'the methods hello named');
});

test('a server of another Proto leaves the page outdated', async () => {
  const {srv, wire} = rig();
  await srv.play('hello-proto');
  eq(wire.status.value, 'outdated', 'status');
  const c = outcome(wire.call('state.get'));
  await settle();
  eq(c.code, 'proto', 'a call');
});

test('calls are answered by id in any order', async () => {
  const {srv, wire} = rig();
  let a, b, c;
  await srv.play('call', {calls() {
    a = outcome(wire.call('state.get'));
    b = outcome(wire.call('task.create', {title: 'x'}, {commandID: 'cmd-1'}));
    c = outcome(wire.call('task.get', {id: 't9'}));
  }});
  eq(a, {value: {seq: 4}}, 'state.get');
  eq(b, {value: {id: 't1'}}, 'task.create');
  eq(c, {code: 'not_found', detail: 't9'}, 'task.get');
});

test('a call that waits too long is cancelled', async () => {
  const {srv, wire} = rig();
  let a;
  await srv.play('call-timeout', {call() { a = outcome(wire.call('state.get')); }});
  eq(a, {code: 'timeout', detail: 'state.get'}, 'the call');
});

test('a call made while connecting waits for hello', async () => {
  const {srv, wire} = rig();
  let a;
  await srv.play('call-queued', {call() { a = outcome(wire.call('state.get')); }});
  eq(a, {value: {seq: 7}}, 'the call');
});

test('the server\'s requests are answered', async () => {
  const {srv} = rig();
  await srv.play('server-req');
});

test('frames are lines, whatever the messages', async () => {
  const {srv} = rig();
  await srv.play('framing');
});

test('a watch takes its pushes until the server ends it', async () => {
  const {srv, wire} = rig();
  const r = recorder();
  await srv.play('watch', {watch() { wire.watch('run.output.watch', {params: () => ({run: 'r1'}), ...r}); }});
  eq(r.pushes.map(([m, p]) => m === 'open' ? m + ':' + p.mode : m + ':' + p.events[0].text), ['open:resume', 'run.output:one', 'run.output:two'], 'pushes');
  eq(r.ends, ['done'], 'ends');
});

test('a push sent right behind the request finds its watch', async () => {
  const {srv, wire} = rig();
  await srv.play('hello');
  const s = srv.current(), send = s.send;
  s.send = data => {
    send(data);
    const f = s.sent.at(-1);
    if (f?.method === 'run.output.watch') {
      s.sent.pop();
      s.onmessage({data: JSON.stringify({type: 'push', id: f.id, method: 'open', params: {mode: 'resume'}}) + '\n'});
    }
  };
  const r = recorder();
  wire.watch('run.output.watch', {params: () => ({run: 'r1'}), ...r});
  eq(r.pushes, [['open', {mode: 'resume'}]], 'pushes');
});

test('cancel ends a watch once and drops what was in flight', async () => {
  const {srv, wire} = rig();
  const r = recorder();
  let w;
  await srv.play('watch-cancel', {
    watch() { w = wire.watch('run.output.watch', {params: () => ({run: 'r1'}), ...r}); },
    cancel() { w.cancel(); },
  });
  eq(r.pushes.map(([m]) => m), ['open'], 'pushes');
  eq(r.ends, ['canceled'], 'ends');
});

test('a watch the server ends with an error stays ended', async () => {
  const {srv, wire} = rig();
  const ends = {};
  const w = run => wire.watch('run.output.watch', {params: () => ({run}), onEnd: e => { ends[run] = e?.code; }});
  await srv.play('watch-end', {
    watch() { w('r1'); w('r2'); w('r3'); },
    missing() { wire.watch('runs.watch', {onEnd: e => { ends.missing = e?.code; }}); },
  });
  eq(ends, {r1: 'unauthorized', r2: 'gone', r3: 'unsupported', missing: 'unsupported'}, 'ends');
});

test('a lagged watch opens again from its cursor', async () => {
  const {srv, wire} = rig();
  let cursor = null;
  const r = recorder();
  await srv.play('watch-lagged', {watch() {
    wire.watch('run.output.watch', {
      params: () => (cursor ? {run: 'r1', from: cursor} : {run: 'r1'}),
      onPush: (m, p) => { r.onPush(m, p); if (p.cursor) cursor = p.cursor; },
      onEnd: r.onEnd,
    });
  }});
  eq(r.pushes.map(([m]) => m), ['open', 'run.output', 'open'], 'pushes');
  eq(r.ends, [], 'ends');
});

test('a dropped connection comes back with backoff and reopens each watch from its cursor', async () => {
  const {srv, wire} = rig();
  let cursor = null, pending, offline;
  const statuses = [];
  wire.status.subscribe(s => statuses.push(s));
  await srv.play('reconnect', {
    start() {
      pending = outcome(wire.call('state.get'));
      wire.watch('run.output.watch', {
        params: () => (cursor ? {run: 'r1', from: cursor} : {run: 'r1'}),
        onPush: (m, p) => { if (p.cursor) cursor = p.cursor; },
      });
    },
    offline() { offline = outcome(wire.call('state.get')); },
  });
  eq(pending, {code: 'closed', detail: ''}, 'the call in flight');
  eq(offline, {code: 'offline', detail: ''}, 'the call while offline');
  eq(statuses, ['connecting', 'open', 'offline', 'connecting', 'offline', 'connecting', 'open'], 'statuses');
});

test('a hidden page closes its output watches and opens them again when shown', async () => {
  const {srv, wire} = rig();
  let cursor = null;
  const state = recorder(), out = recorder();
  await srv.play('hidden', {
    watch() {
      wire.watch('state.watch', state);
      wire.watch('run.output.watch', {
        pausable: true,
        params: () => (cursor ? {run: 'r1', from: cursor} : {run: 'r1'}),
        onPush: (m, p) => { out.onPush(m, p); if (p.cursor) cursor = p.cursor; },
        onEnd: out.onEnd,
      });
    },
    hide() { wire.setVisible(false); },
    show() { wire.setVisible(true); },
  });
  eq(out.pushes.map(([m]) => m), ['open', 'run.output', 'open'], 'output pushes');
  eq([state.ends, out.ends], [[], []], 'ends');
});

test('a page shown while offline dials at once', async () => {
  const {srv, wire} = rig();
  await srv.play('shown-offline', {hide() { wire.setVisible(false); }, show() { wire.setVisible(true); }});
});

test('close ends everything', async () => {
  const {srv, wire} = rig();
  await srv.play('hello');
  const r = recorder();
  wire.watch('state.watch', r);
  const c = outcome(wire.call('state.get'));
  wire.close();
  await settle();
  eq([wire.status.value, c.code, r.ends], ['closed', 'closed', ['closed']], 'after close');
});

run();
