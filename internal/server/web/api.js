'use strict';

// api is the page's one way to the server: /login, /logout and /session over HTTP, everything else as wire frames
// (one JSON object per line) on the /client WebSocket. Methods mirror the coordinator's; writes take {command_id}.
const api = (() => {
  const callWait = 30000;
  let ws = null, opening = null, nextID = 1, buffer = '';
  const pending = new Map(), journal = new Set(), closeListeners = new Set();

  const err = (code, detail = '') => Object.assign(new Error(detail || code), {code, detail});

  function send(frame) { ws.send(JSON.stringify(frame) + '\n'); }

  function receive(line) {
    let f;
    try { f = JSON.parse(line); } catch (_) { return; }
    if (f.type === 'res') {
      const p = pending.get(f.id);
      if (!p) return;
      pending.delete(f.id);
      clearTimeout(p.timer);
      if (f.error) p.reject(err(f.error.code, f.error.detail || ''));
      else p.resolve(f.result === undefined ? null : f.result);
    } else if (f.type === 'req') { // the server's keepalive; nothing else is served here
      send(f.method === 'ping' ? {type: 'res', id: f.id}
        : {type: 'res', id: f.id, error: {code: 'unknown_method', detail: f.method}});
    } else if (f.type === 'push' && f.method === 'journal') {
      for (const fn of journal) fn(f.params);
    }
  }

  function connect() {
    if (ws && ws.readyState === WebSocket.OPEN) return Promise.resolve();
    if (opening) return opening;
    opening = new Promise((resolve, reject) => {
      const s = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/client`);
      let open = false;
      s.onopen = () => { open = true; ws = s; opening = null; resolve(); };
      s.onmessage = event => {
        buffer += typeof event.data === 'string' ? event.data : '';
        let i;
        while ((i = buffer.indexOf('\n')) >= 0) {
          const line = buffer.slice(0, i);
          buffer = buffer.slice(i + 1);
          if (line.trim()) receive(line);
        }
      };
      s.onclose = () => {
        opening = null;
        buffer = '';
        const current = ws === s; // a socket dropped on logout is no loss
        if (current) ws = null;
        for (const [id, p] of pending) { clearTimeout(p.timer); p.reject(err('closed')); pending.delete(id); }
        if (!open) { reject(err('offline')); return; }
        if (current) for (const fn of closeListeners) fn();
      };
    });
    return opening;
  }

  async function call(method, params, commandID) {
    await connect();
    const id = nextID++;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        pending.delete(id);
        try { send({type: 'cancel', id}); } catch (_) {}
        reject(err('timeout', method));
      }, callWait);
      pending.set(id, {resolve, reject, timer});
      const frame = {type: 'req', id, method, params: params ?? {}};
      if (commandID) frame.command_id = commandID;
      try { send(frame); } catch (e) { clearTimeout(timer); pending.delete(id); reject(err('closed', String(e))); }
    });
  }

  const write = method => (params, options) => {
    if (!options?.command_id) return Promise.reject(err('bad_request', 'command_id'));
    return call(method, params, options.command_id);
  };

  async function http(path, body) {
    let r;
    try {
      r = await fetch(path, {method: body === undefined ? 'GET' : 'POST', body, credentials: 'same-origin',
        headers: body === undefined ? {} : {'Content-Type': 'application/x-www-form-urlencoded'}});
    } catch (e) { throw err('offline', String(e)); }
    if (r.status === 401) throw err('unauthorized');
    if (!r.ok) throw err('internal', `HTTP ${r.status}`);
    return r.status === 204 ? null : r.json();
  }

  // page turns a transcript page (newest first) into the order the page shows it, oldest first; before and file are
  // the node's own cursor.
  function page(p) {
    return {
      messages: (p.Msgs || []).slice().reverse().map(m => ({id: `m_${m.Off}`, role: m.Role, text: m.Text, at: m.At, chars: m.Chars,
        steps: (m.Steps || []).length})),
      before: p.From, file: p.file || '', done: !!p.Done,
    };
  }

  return {
    session: () => http('/session'),
    async login(token) {
      await http('/login', new URLSearchParams({token}).toString());
      await connect();
    },
    async logout() {
      const s = ws;
      ws = null;
      try { await http('/logout', ''); } finally { journal.clear(); s?.close(); }
    },
    connect,
    onClose(fn) { closeListeners.add(fn); },
    stateGet: params => call('state.get', params),
    taskGet: params => call('task.get', params),
    taskCreate: write('task.create'),
    taskEdit: write('task.edit'),
    taskSetStatus: write('task.set_status'),
    runDispatch: write('run.dispatch'),
    runStop: write('run.stop'),
    runAbandon: write('run.abandon'),
    runContinue: write('run.continue'),
    runAnswer: write('run.answer'),
    runSend: write('run.send'),
    runPreview: params => call('run.preview', params),
    runTail: params => call('run.tail', params),
    machineList: params => call('machine.list', params),
    agentList: () => call('agent.list'),
    subscribe(params, listener) {
      journal.clear();
      journal.add(listener);
      call('subscribe', params).catch(() => {});
      return () => journal.delete(listener);
    },
    async nodeCall({machine, method, params}) {
      const r = await call('node.call', {machine, method, params});
      return method === 'messages' ? page(r) : r;
    },
  };
})();
