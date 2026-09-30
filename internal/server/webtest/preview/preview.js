// preview runs the page (web/pages/boot.js) on a fake server for tools/webpreview, which serves this at /: a socket that answers from the frame
// files by method, a fetch that answers the sign-in and device calls, and the clock of the home frames. ?frames=tasks
// plays the task pages' frames instead of the home's, ?frames=output a conversation's and its changes (open
// ?page=tasks&task=t1), ?frames=carry that conversation carried on into a third run, ?frames=gone with its question
// answered by someone else first, ?frames=team the machines and team pages' data (?as=admin signs in as its admin),
// ?frames=agents the agent page's (open ?page=agents).
// /api/* answers come from webtest/api.json by method and path; a write it has no answer for succeeds empty.
import {boot} from '../../web/pages/boot.js';

// ⚠️ The moment the home frames are written for.
const NOW = Date.parse('2026-09-30T14:32:00Z');

const lines = async name => (await (await fetch(`webtest/frames/${name}.jsonl`)).text()).split('\n').filter(Boolean).map(l => JSON.parse(l));

// answers maps each request method of the frame files to what the server sent for it: the result, then the pushes on
// its stream, remapped to the ids the page uses; a method asked of a run is answered per run, and
// a run the files do not ask of as the first one they do (with the same page or path, and a diff's same hunk, line and
// context).
const sets = {home: ['home-state', 'output-page', 'home-commands', 'changes-gone'],
  tasks: ['tasks-state', 'tasks-create', 'tasks-dispatch', 'tasks-plan', 'tasks-acts', 'tasks-board', 'changes-list'],
  output: ['output-state', 'output-conv', 'output-send', 'output-answer', 'changes-list'],
  carry: ['output-state', 'output-conv', 'output-send', 'output-carry', 'changes-list'],
  gone: ['output-state', 'output-conv', 'output-answer-gone', 'changes-list'],
  team: ['team-state', 'team-share', 'team-project', 'team-settings'],
  agents: ['agents-state', 'agents-edit', 'agents-share']};
// joins are the files whose pushes on a stream an earlier file opened go on that stream, after what it pushed there.
const joins = new Set(['output-send', 'output-carry']);
const keyOf = (f, run = true) => [f.method, run && f.params?.run, f.params?.after, f.params?.path, ...['hunk', 'line', 'context'].map(k => f.params?.[k] && k + f.params[k])]
  .filter(Boolean).join(' ');

async function answers(names) {
  const out = {}, opened = {};
  for (const name of names) {
    const asked = {};
    for (const l of await lines(name)) {
      if (l.c?.type === 'req') {
        const k = keyOf(l.c);
        asked[l.c.id] = k;
        out[k] ||= {res: null, pushes: []};
        out[keyOf(l.c, false)] ||= out[k];
        out[l.c.method] ||= out[k];
      }
      const f = l.s, m = f && asked[f.id];
      if (!m && f?.type === 'push' && joins.has(name) && opened[f.id]) out[opened[f.id]].pushes.push(f);
      if (!m || out[m].done) continue;
      if (f.type === 'res') out[m].res = f;
      else if (f.type === 'push') out[m].pushes.push(f);
    }
    for (const m of Object.values(out)) if (m.res || m.pushes.length) m.done = true;
    Object.assign(opened, asked);
  }
  return out;
}

function socket(table) {
  const s = {
    send(data) {
      for (const line of data.split('\n').filter(Boolean)) {
        const f = JSON.parse(line);
        if (f.type !== 'req') continue;
        const a = table[keyOf(f)] || table[keyOf(f, false)];
        setTimeout(() => {
          if (!a) { s.reply({type: 'res', id: f.id, result: {}}); return; }
          if (a.res) s.reply({...a.res, id: f.id});
          for (const p of a.pushes) s.reply({...p, id: f.id});
        }, 30);
      }
    },
    reply: f => s.onmessage?.({data: JSON.stringify(f) + '\n'}),
    close() { setTimeout(() => s.onclose?.({}), 0); },
  };
  setTimeout(() => s.onopen?.({}), 10);
  return s;
}

const people = {member: {id: 'u_b', name: 'Bo Lin', email: 'bo@example.com', username: 'bo', role: 'member', session: 'web-1'},
  admin: {id: 'u_a', name: 'Ann Lee', email: 'ann@example.com', username: 'ann', role: 'admin', session: 'web-2'}};
const json = (status, body) => new Response(body === undefined ? '' : JSON.stringify(body), {status, headers: {'Content-Type': 'application/json'}});

function fakeFetch(me, api) {
  return async (path, init = {}) => {
    const [p] = String(path).split('?');
    const post = init.method === 'POST';
    if (p === '/session') return me ? json(200, me) : json(401, {error: 'unauthorized'});
    if (p === '/login' && post) return new URLSearchParams(init.body).get('token') === 'tend_ok' ? json(200, {}) : json(401, {error: 'unauthorized'});
    if (p === '/logout') return json(200, {});
    if (p === '/auth/logins') return json(200, [{name: 'github', display: 'GitHub'}, {name: 'oidc', display: 'Company SSO'}]);
    if (p === '/auth/invite') return json(200, {inviter: 'Al', role: 'member', project: 'Shop', access: 'participant', expires: '2026-10-07T00:00:00Z'});
    if (p === '/api/device') return post ? json(200, {}) : json(200, {code: 'K7QX-M2PD', name: 'tend on mba', ip: '100.64.0.2', created: '2026-09-30T14:30:00Z'});
    const method = init.method || 'GET';
    if (p.startsWith('/api/')) return method + ' ' + p in api ? json(200, api[method + ' ' + p]) : method === 'GET' ? json(404, {error: 'not_found'}) : json(200, {});
    return fetch(path, init);
  };
}

const query = new URLSearchParams(location.search);
const table = await answers(sets[query.get('frames')] || sets.home);
const api = await (await fetch('webtest/api.json')).json();
const as = query.get('as');
boot({open: () => socket(table), fetch: fakeFetch(as === 'signedout' ? null : people[as] || people.member, api), clock: () => NOW});
