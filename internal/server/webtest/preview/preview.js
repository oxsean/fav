// preview runs the page (web/pages/boot.js) on a fake server for tools/webpreview, which serves this at /: a socket that answers from the frame
// files by method, a fetch that answers the sign-in and device calls, and the clock of the home frames.
import {boot} from '../../web/pages/boot.js';

// ⚠️ The moment the home frames are written for.
const NOW = Date.parse('2026-09-30T14:32:00Z');

const lines = async name => (await (await fetch(`webtest/frames/${name}.jsonl`)).text()).split('\n').filter(Boolean).map(l => JSON.parse(l));

// answers maps each request method of the frame files to what the server sent for it: the result, then the pushes on
// its stream, remapped to the ids the page uses.
async function answers() {
  const out = {};
  for (const name of ['home-state', 'output-page', 'home-commands']) {
    const asked = {};
    for (const l of await lines(name)) {
      if (l.c?.type === 'req') { asked[l.c.id] = l.c.method; out[l.c.method] ||= {res: null, pushes: []}; }
      const f = l.s, m = f && asked[f.id];
      if (!m || out[m].done) continue;
      if (f.type === 'res') out[m].res = f;
      else if (f.type === 'push') out[m].pushes.push(f);
    }
    for (const m of Object.values(out)) if (m.res || m.pushes.length) m.done = true;
  }
  return out;
}

function socket(table) {
  const s = {
    send(data) {
      for (const line of data.split('\n').filter(Boolean)) {
        const f = JSON.parse(line);
        if (f.type !== 'req') continue;
        const a = table[f.method];
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

const me = {id: 'u_b', name: 'Bo Lin', email: 'bo@example.com', username: 'bo', role: 'member', session: 'web-1'};
const json = (status, body) => new Response(body === undefined ? '' : JSON.stringify(body), {status, headers: {'Content-Type': 'application/json'}});

function fakeFetch(signedIn) {
  return async (path, init = {}) => {
    const [p] = String(path).split('?');
    const post = init.method === 'POST';
    if (p === '/session') return signedIn ? json(200, me) : json(401, {error: 'unauthorized'});
    if (p === '/login' && post) return new URLSearchParams(init.body).get('token') === 'tend_ok' ? json(200, {}) : json(401, {error: 'unauthorized'});
    if (p === '/logout') return json(200, {});
    if (p === '/auth/logins') return json(200, [{name: 'github', display: 'GitHub'}, {name: 'oidc', display: 'Company SSO'}]);
    if (p === '/auth/invite') return json(200, {inviter: 'Al', role: 'member', project: 'Shop', access: 'participant', expires: '2026-10-07T00:00:00Z'});
    if (p === '/api/device') return post ? json(200, {}) : json(200, {code: 'K7QX-M2PD', name: 'tend on mba', ip: '100.64.0.2', created: '2026-09-30T14:30:00Z'});
    return fetch(path, init);
  };
}

const table = await answers();
const signedOut = new URLSearchParams(location.search).get('as') === 'signedout';
boot({open: () => socket(table), fetch: fakeFetch(!signedOut), clock: () => NOW});
