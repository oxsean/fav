// preview runs the page (web/pages/boot.js) on a fake server for tools/webpreview, which serves this at /: a socket that answers from the frame
// files by method, a fetch that answers the sign-in and device calls, and the clock of the home frames. ?frames=tasks
// plays the task pages' frames instead of the home's, ?frames=output a conversation's and its changes (open
// ?page=tasks&task=t1), ?frames=carry that conversation carried on into a third run, ?frames=gone with its question
// answered by someone else first, ?frames=team the machines, team and mba's sessions pages' data (?as=admin signs in as its admin),
// ?frames=agents the agent page's (open ?page=agents).
// /api/* answers come from webtest/api.json by method and path; a write it has no answer for succeeds empty.
import {boot} from '../../web/pages/boot.js';
import {sets, answers, socket} from './answer.js';

// ⚠️ The moment the home frames are written for.
const NOW = Date.parse('2026-09-30T14:32:00Z');

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
    if (p === '/auth/device') return json(200, {device_code: 'dc', user_code: 'K7QX-M2PD', verify_url: location.origin + '/#device-K7QX-M2PD', interval: 3, expires_in: 600});
    if (p === '/auth/device/token') return json(200, {status: 'pending'});
    if (p === '/api/device') return post ? json(200, {}) : json(200, {code: 'K7QX-M2PD', name: 'tend on mba', ip: '100.64.0.2', created: '2026-09-30T14:30:00Z'});
    const method = init.method || 'GET';
    if (p.startsWith('/api/')) return method + ' ' + p in api ? json(200, api[method + ' ' + p]) : method === 'GET' ? json(404, {error: 'not_found'}) : json(200, {});
    return fetch(path, init);
  };
}

const query = new URLSearchParams(location.search);
const text = async name => (await fetch(`webtest/frames/${name}.jsonl`)).text();
const table = await answers(sets[query.get('frames')] || sets.home, text);
const api = await (await fetch('webtest/api.json')).json();
const as = query.get('as');
boot({open: () => socket(table), fetch: fakeFetch(as === 'signedout' ? null : people[as] || people.member, api), clock: () => NOW});
