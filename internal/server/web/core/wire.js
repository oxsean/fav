// wire is the page's connection to the coordinator: wire frames (one JSON object per line) on the /client WebSocket.
// Calls are answered by id in any order; a watch is a request whose pushes carry its id until one res ends it. After a
// dropped connection it reconnects with backoff and opens every watch again from the cursor its owner gives.
import {signal} from '../vendor/signals-core.mjs';
// hello's shape is remote.HelloParams / remote.Hello.
import {PROTO, code, frame} from './proto.js';

const backoffFirst = 1000, backoffMax = 30000, callWait = 30000;

export class WireError extends Error {
  constructor(code, detail = '') {
    super(detail ? `${code}: ${detail}` : code);
    this.code = code;
    this.detail = detail;
  }
}

// createWire returns the connection; nothing is dialled before start. open makes a WebSocket-like object (send,
// close, onopen, onmessage, onclose); timers and random are replaced by the tests.
export function createWire({url, open = u => new WebSocket(u), timers = globalThis, random = Math.random, lang = '', wait = callWait} = {}) {
  // status: idle before start; connecting (the socket or its hello); open; offline (waiting to reconnect); outdated
  // (the server speaks another Proto, or serves other files than the page loaded: the page must reload); closed
  // (stopped). build is the server's Web UI as the first hello named it.
  const status = signal('idle');
  let ws = null, nextID = 1, buffer = '', attempt = 0, retry = null, methods = null, visible = true, build = null;
  const calls = new Map(), streams = new Map(), watches = new Set(), waiting = [];

  const send = frame => ws.send(JSON.stringify(frame) + '\n');

  function dial() {
    retry = null;
    status.value = 'connecting';
    const s = open(url);
    ws = s;
    s.onopen = () => hello(s);
    s.onmessage = ev => {
      if (ws !== s) return;
      buffer += typeof ev.data === 'string' ? ev.data : '';
      let i;
      while ((i = buffer.indexOf('\n')) >= 0) {
        const line = buffer.slice(0, i);
        buffer = buffer.slice(i + 1);
        if (line.trim()) receive(line);
      }
    };
    s.onclose = () => {
      if (ws === s) dropped();
    };
  }

  function hello(s) {
    const params = {proto: PROTO, role: 'client'};
    if (lang) params.lang = lang;
    request('hello', params, {}).then(h => {
      if (h?.proto !== PROTO) {
        stop('outdated', new WireError(code.proto, `the server speaks ${h?.proto}`));
        return;
      }
      if (build === null) build = h.build || '';
      else if ((h.build || '') !== build) {
        stop('outdated', new WireError(code.proto, `the server's build is ${h.build}`));
        return;
      }
      methods = new Set(h.methods || []);
      attempt = 0;
      status.value = 'open';
      for (const w of waiting.splice(0)) w.go();
      for (const w of watches) if (!w.paused()) w.start();
    }, e => {
      if (ws !== s) return;
      if (e.code === code.proto) stop('outdated', e);
      else { s.close(); dropped(); }
    });
  }

  // dropped ends what the connection carried and plans the next attempt.
  function dropped() {
    ws = null;
    buffer = '';
    methods = null;
    const closed = new WireError(code.closed);
    for (const [id, c] of calls) { timers.clearTimeout(c.timer); calls.delete(id); c.reject(closed); }
    for (const w of waiting.splice(0)) w.fail(new WireError(code.offline));
    for (const w of watches) w.lost();
    status.value = 'offline';
    const d = Math.min(backoffMax, backoffFirst * 2 ** Math.min(attempt++, 5));
    retry = timers.setTimeout(dial, d / 2 + random() * d / 2);
  }

  function stop(to, err) {
    status.value = to;
    if (retry) timers.clearTimeout(retry);
    retry = null;
    const s = ws;
    ws = null;
    buffer = '';
    for (const [id, c] of calls) { timers.clearTimeout(c.timer); calls.delete(id); c.reject(err); }
    for (const w of waiting.splice(0)) w.fail(err);
    for (const w of [...watches]) w.end(err);
    s?.close();
  }

  function receive(line) {
    let f;
    try { f = JSON.parse(line); } catch { return; }
    if (f.type === frame.res) {
      const c = calls.get(f.id);
      if (c) {
        calls.delete(f.id);
        timers.clearTimeout(c.timer);
        if (f.error) c.reject(new WireError(f.error.code, f.error.detail || ''));
        else c.resolve(f.result === undefined ? null : f.result);
        return;
      }
      streams.get(f.id)?.finish(f);
    } else if (f.type === frame.push) {
      streams.get(f.id)?.push(f.method, f.params);
    } else if (f.type === frame.req) {
      send(f.method === 'ping' ? {type: frame.res, id: f.id} : {type: frame.res, id: f.id, error: {code: code.unknownMethod, detail: f.method}});
    }
  }

  function request(method, params, {commandID, timeout = wait} = {}) {
    return new Promise((resolve, reject) => {
      const id = nextID++;
      const timer = timers.setTimeout(() => {
        calls.delete(id);
        try { send({type: frame.cancel, id}); } catch {}
        reject(new WireError(code.timeout, method));
      }, timeout);
      calls.set(id, {resolve, reject, timer});
      const f = {type: frame.req, id, method, params: params ?? {}};
      if (commandID) f.command_id = commandID;
      send(f);
    });
  }

  // call answers a request; while connecting it waits for the connection, while offline it fails at once.
  function call(method, params, options = {}) {
    switch (status.value) {
      case 'open': return request(method, params, options);
      case 'connecting':
        return new Promise((resolve, reject) => waiting.push({go: () => request(method, params, options).then(resolve, reject), fail: reject}));
      case 'outdated': return Promise.reject(new WireError(code.proto));
      case 'offline': return Promise.reject(new WireError(code.offline));
      default: return Promise.reject(new WireError(code.closed));
    }
  }

  // watch opens a stream and keeps it open across reconnects until it ends. params() gives the parameters for each
  // opening, with the cursor its owner has applied up to; onPush(method, params) takes every push, open first;
  // onEnd(err) is called once, err null when the server ended it as done. A pausable watch closes while the page is
  // hidden and opens again when it shows.
  function watch(method, {params = () => ({}), onPush = () => {}, onEnd = () => {}, pausable = false} = {}) {
    const w = {
      id: 0, done: false,
      paused: () => pausable && !visible,
      start() {
        if (w.done || w.id || status.value !== 'open') return;
        if (methods && !methods.has(method)) { w.end(new WireError(code.unsupported, method)); return; }
        w.id = nextID++;
        streams.set(w.id, w); // before the req goes out, so no push finds no one
        send({type: frame.req, id: w.id, method, params: params() ?? {}});
      },
      push(m, p) { if (!w.done) onPush(m, p); },
      finish(f) {
        streams.delete(w.id);
        w.id = 0;
        if (f.error?.code === code.lagged) { w.start(); return; }
        w.end(f.error ? new WireError(f.error.code, f.error.detail || '') : null);
      },
      // lost: the connection dropped; the watch opens again on the next one.
      lost() { streams.delete(w.id); w.id = 0; },
      // pause: the page is hidden; the server stops sending, the watch opens again when it shows.
      pause() {
        if (!w.id) return;
        try { send({type: frame.cancel, id: w.id}); } catch {}
        streams.delete(w.id);
        w.id = 0;
      },
      end(err) {
        if (w.done) return;
        w.done = true;
        if (w.id) streams.delete(w.id);
        w.id = 0;
        watches.delete(w);
        onEnd(err);
      },
    };
    watches.add(w);
    if (!w.paused()) w.start();
    return {
      cancel() {
        if (w.done) return;
        if (w.id) try { send({type: frame.cancel, id: w.id}); } catch {}
        w.end(new WireError(code.canceled));
      },
      get open() { return w.id !== 0; },
    };
  }

  function reconnect() {
    if (status.value !== 'offline') return;
    timers.clearTimeout(retry);
    dial();
  }

  return {
    status,
    call,
    watch,
    start() { if (status.value === 'idle' || status.value === 'closed') { attempt = 0; dial(); } },
    // reconnect skips the wait before the next attempt.
    reconnect,
    // setVisible: the page was hidden or shown. Hidden, pausable watches close; shown, they open again, and an offline
    // connection tries at once.
    setVisible(v) {
      if (visible === v) return;
      visible = v;
      for (const w of watches) if (v) w.start(); else if (w.paused()) w.pause();
      if (v) reconnect();
    },
    close() { stop('closed', new WireError(code.closed)); },
    has: method => !!methods?.has(method),
  };
}
