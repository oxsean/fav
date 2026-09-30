// select_test checks what the pages are built on: the number formats, the home's figures from the home frames, the
// HTTP calls, the action table against the design's key table, the writes (pending, hidden, unknown and retried) and
// the viewer's preferences.
process.env.TZ = 'UTC';
import {tokens, money, duration, clock as hhmm, day, usageTokens} from '../web/core/format.js';
import * as sel from '../web/core/select.js';
import {createHTTP, HTTPError, startURL} from '../web/core/http.js';
import {actions, groups, bindingsFor, runnable, rank} from '../web/core/actions.js';
import {createCommands} from '../web/core/commands.js';
import {createPrefs} from '../web/core/prefs.js';
import {words} from '../web/core/i18n.js';
import {signal} from '../web/vendor/signals-core.mjs';
import {NOW, rig, home} from './rig.js';
import {test, eq, ok, throws, run} from './check.js';

const H = 3600e3, M = 60e3;
const at = s => Date.parse('2026-09-30T' + s + ':00Z');

test('numbers, lengths and times are spelled one way', () => {
  eq([0, 940, 1000, 1250, 12400, 312345, 1.2e6, 3e6].map(tokens), ['0', '940', '1k', '1.3k', '12k', '312k', '1.2M', '3M'], 'tokens');
  eq([0, 0.004, 3.181, 12].map(money), ['$0', '<$0.01', '$3.18', '$12.00'], 'money');
  eq([45e3, 12 * M, 64 * M, 51 * H].map(duration), ['45s', '12m', '1h 04m', '2d 3h'], 'duration');
  eq([hhmm(at('09:05')), day(at('09:05'))], ['09:05', '9-30'], 'clock and day');
  eq([usageTokens({input: 1, cache_read: 100, cache_write: 10, output: 5}), usageTokens(null)], [16, 0], 'usage leaves cache reads out');
});

test('the home figures are what the frames hold', async () => {
  const {store, errors} = await home();
  const st = store.state, machines = store.machines.value, inbox = store.inbox.value;
  eq(sel.counts(st, machines, inbox), {running: 3, queued: 1, offline: ['win'], waiting: 5}, 'counts');
  eq(sel.slots(machines), {slots: 4, active: 3, online: 2, offline: 1}, 'slots');
  const d = sel.today(st, NOW);
  eq({...d, usd: Math.round(d.usd * 100)}, {ended: 4, ends: {exited: 2, failed: 1, stopped: 1, canceled: 0, abandoned: 0}, mean: (85 + 32 + 34 + 46) / 4 * M,
    tokens: 285800, usd: 412, peak: 3, hourly: [1, 1, 1, 1, 2, 3, 3]}, 'today');
  const w = sel.week(st, NOW);
  eq(w.days.map(x => [new Date(x.at).getUTCDate(), x.tokens]), [[24, 125000], [25, 47000], [26, 47000], [27, 187000], [28, 31000], [29, 146000], [30, 285800]], 'days');
  eq([w.days[6].providers, w.tokens, Math.round(w.usd * 100), w.ended, w.ends, w.providers],
    [{claude: 233500, codex: 52300}, 868800, 1247, 10, {exited: 7, failed: 2, stopped: 1, canceled: 0, abandoned: 0}, ['claude', 'codex']], 'week');
  eq(sel.waits(inbox).map(x => x.task), ['t2', 't3', 't5', 't4', 't9'], 'to answer, errors, to accept, the rest; oldest first');
  eq(sel.waits(inbox, 'approver').map(x => x.task), ['t4'], 'as approver');
  eq(sel.waitSummary(inbox, NOW), {count: 5, by: {answer: 2, error: 1, accept: 1, other: 1}, longest: 4 * H + 32 * M}, 'summary');
  eq(sel.openRuns(st).map(x => [x.run.id, x.why]), [['r1', ''], ['r2', ''], ['r3', ''], ['r6', 'slot']], 'open runs');
  eq(sel.recent(st, NOW).map(r => r.id), ['r5', 'r4', 'r7', 'r8'], 'ended today, latest first');
  const l = sel.lanes(st, machines, NOW);
  eq([l.from, l.to], [at('08:00'), NOW], 'the day from 08:00');
  eq(l.lanes.map(x => [x.name, x.state, x.slots, x.rows.map(row => row.map(s => s.run + ':' + s.kind))]), [
    ['mba', 'connected', 2, [['r1:running'], ['r5:failed', 'r2:running']]],
    ['linux', 'connected', 2, [['r7:stopped', 'r4:exited', 'r3:running'], []]],
    ['win', 'offline', 1, [['r8:exited']]],
  ], 'lanes');
  eq(errors, [], 'errors');
});

test('home-state folded', async () => {
  const {store} = await home();
  const {seq, tasks, runs, projects, shares, agent_defs} = store.state;
  return {seq, tasks, runs, projects, shares, agent_defs};
});

test('what a run is doing: its node, its note, what it said', async () => {
  const {store} = await home({journal: r => eq(sel.doing(r.store.state.runs.r1), 'go vet ./internal/pay/...', 'the journal changed the note')});
  eq(sel.doing(store.state.runs.r2), 'Two libraries fit; I need a choice.', 'without a note, the last');
  eq([sel.doing({doing: '$ go test', note: 'n', last: 'l'}), sel.doing({note: 'n', last: 'l'}), sel.doing({})], ['$ go test', 'n', ''], 'order');
});

test('a day with nothing in it', () => {
  const st = {tasks: {}, runs: {}};
  const d = sel.today(st, NOW);
  eq([d.ended, d.mean, d.tokens, d.peak, d.hourly], [0, 0, 0, 0, [0]], 'today');
  const l = sel.lanes(st, [{name: 'mba', state: 'connected', slots: 2}], NOW);
  eq([l.from, l.lanes[0].rows], [at('08:00'), [[], []]], 'an empty lane per slot');
  eq(sel.lanes(st, [], at('06:10')).from, at('05:10'), 'before eight the day starts an hour back');
});

function fakeFetch(answers) {
  const calls = [];
  const fetch = async (path, init) => {
    calls.push({path, method: init.method, tend: init.headers['X-Tend'] || '', type: init.headers['Content-Type'] || '', body: init.body || ''});
    const a = answers[init.method + ' ' + path];
    if (a === 'down') throw new TypeError('network');
    const [status, body] = a || [404, {error: 'not_found'}];
    return {ok: status < 300, status, text: async () => (body === undefined ? '' : JSON.stringify(body))};
  };
  return {fetch, calls};
}

test('http: the session, sign-in, invitation and a terminal to allow', async () => {
  const me = {id: 'u_b', name: 'Bo', role: 'member'};
  const {fetch, calls} = fakeFetch({'GET /session': [200, me], 'POST /login': [200, {}], 'GET /auth/logins': [200, [{name: 'github', display: 'GitHub'}]],
    'GET /auth/invite?code=a%20b': [200, {inviter: 'Al', role: 'member'}], 'GET /api/device?code=K7': [404, {error: 'not_found'}], 'POST /api/device': [200, {}]});
  const http = createHTTP({fetch});
  eq(await http.session(), me, 'session');
  await http.login('tend_x');
  eq(await http.logins(), [{name: 'github', display: 'GitHub'}], 'logins');
  eq((await http.invite('a b')).inviter, 'Al', 'invite');
  const e = await throws(() => http.device('K7'), /404/);
  ok(e instanceof HTTPError && e.code === 'not_found', 'a gone code');
  await http.decideDevice('K7', true);
  eq(calls.map(c => [c.method, c.path, c.tend, c.type, c.body]), [
    ['GET', '/session', '', '', ''], ['POST', '/login', '1', 'application/x-www-form-urlencoded', 'token=tend_x'], ['GET', '/auth/logins', '', '', ''],
    ['GET', '/auth/invite?code=a%20b', '', '', ''], ['GET', '/api/device?code=K7', '', '', ''], ['POST', '/api/device', '1', 'application/json', '{"code":"K7","allow":true}'],
  ], 'requests');
  const out = createHTTP({fetch: fakeFetch({'GET /session': [401, {error: 'unauthorized'}]}).fetch});
  eq(await out.session(), null, 'signed out');
  eq((await throws(() => createHTTP({fetch: fakeFetch({'GET /session': 'down'}).fetch}).session())).code, 'offline', 'no network');
  eq([startURL('github'), startURL('my oidc', 'c/1')], ['/auth/github/start', '/auth/my%20oidc/start?invite=c%2F1'], 'start');
});

// ⚠️ The design's key table (§6.6), every key once.
const keyTable = ['g h', 'g t', 'g b', 'g r', 'g m', 'g a', 'g p', 'g s', 'Mod+K', '?', '/', 'n', '[', 'Mod+Z', 'Shift+T', 'Shift+L', 'Shift+M',
  'v', 'd', 'e', 'x', 'Shift+D', 'j', 'k', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'Space', 'Enter', 'End', 'Home', 'Shift+O', 'Mod+F'];

test('the action table is the key table: one key per action and level, both names for each', () => {
  const all = actions.flatMap(a => a.keys);
  eq([...all].sort(), [...keyTable].sort(), 'keys');
  eq(all.length, new Set(all).size, 'no key twice');
  for (const a of actions) {
    ok(a.keys.length > 0, `${a.id} has a key`);
    ok(groups.includes(a.group), `${a.id} group`);
    const [zh, en] = words.both('act.' + a.id);
    ok(zh && en && zh !== 'act.' + a.id, `${a.id} has both names`);
  }
  for (const g of groups) ok(words.has('group.' + g), `group ${g}`);
  const hits = rank(actions.map(a => ({id: a.id, words: [...words.both('act.' + a.id), a.id, a.keys[0]]})), '主题');
  eq(hits.map(x => x.id), ['theme'], 'found by its Chinese name');
  eq(rank(actions.map(a => ({id: a.id, words: [...words.both('act.' + a.id)]})), 'go to the b').map(x => x.id), ['board'], 'and by its English one');
  eq(rank([{id: 'a', words: ['xstop']}, {id: 'b', words: ['stop']}], 'stop').map(x => x.id), ['b', 'a'], 'a start ranks first');
});

test('bindings: keys, aliases, what the bar shows and what the palette lists', () => {
  const ran = [];
  const b = bindingsFor({next: {run: k => ran.push('next ' + k)}, pick: {run: k => ran.push('pick ' + k), label: 'home.answer'}, palette: {run() {}}, stop: null});
  eq(b.map(x => [x.key, x.id, x.label, !!x.bar, !!x.alias]).slice(0, 3), [['j', 'next', 'act.move', true, false], ['ArrowDown', 'next', 'act.move', false, true],
    ['1', 'pick', 'home.answer', true, false]], 'shape');
  b[1].run();
  b[5].run();
  eq(ran, ['next ArrowDown', 'pick 4'], 'run gets its key');
  eq(runnable([...b, ...bindingsFor({theme: {run() {}}, home: {run() {}}})]).map(x => [x.id, x.key]), [['theme', 'Shift+T'], ['home', 'g h']], 'the palette leaves out moves, digits and itself');
  eq((() => { try { bindingsFor({nope: {run() {}}}); } catch (e) { return e.message; } })(), 'actions: no action nope', 'unknown ids fail');
});

test('commands: pending while out, hidden until the list changes, errors dropped', async () => {
  let answer;
  const wire = {call: (m, p, o) => new Promise((res, rej) => { answer = {res, rej, m, p, o}; })};
  const list = signal(['t4']);
  const c = createCommands({wire, newID: (() => { let n = 0; return () => 'c' + ++n; })()});
  const out = c.send('task.set_status', {id: 't4', status: 'done'}, {key: 'task:t4', hide: 't4', until: list});
  eq([c.state('task:t4'), [...c.hidden.value], answer.o], ['pending', ['t4'], {commandID: 'c1'}], 'out');
  answer.res({id: 't4'});
  await out;
  eq([c.state('task:t4'), [...c.hidden.value]], ['', ['t4']], 'answered, still hidden');
  list.value = [];
  eq([...c.hidden.value], [], 'the list changed');
  const bad = c.send('run.stop', {id: 'r1'});
  answer.rej(Object.assign(new Error('no'), {code: 'forbidden'}));
  eq((await throws(() => bad)).code, 'forbidden', 'the error');
  eq(c.pending.value.size, 0, 'a refused write is not pending');
});

test('a write whose answer never came is retried under its command id', async () => {
  const r = rig();
  const c = createCommands({wire: r.wire, newID: () => 'c1'});
  let first, again;
  await r.srv.play('command-unknown', {
    send() { first = c.send('task.set_status', {id: 't4', status: 'done'}, {key: 'task:t4'}).catch(e => e.code); },
    async unknown() {
      eq([await first, c.state('task:t4'), c.pending.value.get('task:t4').error], ['timeout', 'unknown', 'timeout'], 'unknown');
      again = c.retry('task:t4');
    },
    async answered() { eq((await again).status, 'done', 'the retry is answered'); },
  });
  eq(c.state('task:t4'), '', 'settled');
  await throws(() => c.retry('task:t4'), /nothing to retry/);
});

test('prefs: the old keys, cycling, and storage that refuses', () => {
  const saved = new Map([['tend-lang', 'en'], ['tend-theme', 'dark'], ['tend-look', '{"skin":"forest","accent":"2d7a5b","high":true,"density":"compact"}']]);
  const storage = {getItem: k => saved.get(k) ?? null, setItem: (k, v) => saved.set(k, v)};
  const p = createPrefs({storage, asked: 'zh-CN'});
  eq([p.lang.value, p.theme.value, p.density.value, p.skin.value], ['en', 'dark', 'compact', 'forest-2d7a5b-high'], 'read');
  p.toggleLang(); p.cycleTheme(); p.cycleDensity();
  eq([p.lang.value, p.theme.value, p.density.value, saved.get('tend-lang'), saved.get('tend-theme'), JSON.parse(saved.get('tend-look'))],
    ['zh', 'system', 'default', 'zh', 'system', {skin: 'forest', accent: '2d7a5b', high: true, density: 'default'}], 'cycled and kept');
  const broken = {getItem() { throw new Error('denied'); }, setItem() { throw new Error('denied'); }};
  const q = createPrefs({storage: broken, asked: 'zh-TW'});
  q.cycleTheme();
  eq([q.lang.value, q.theme.value, q.density.value, q.skin.value], ['zh', 'light', 'default', 'tend'], 'defaults without storage');
  eq(createPrefs({storage: {getItem: k => (k === 'tend-look' ? '{"skin":"../x"}' : null), setItem() {}}}).skin.value, 'tend', 'a bad skin name');
});

test('prefs: the me page sets the language, the look and browser notices', () => {
  const saved = new Map([['tend-lang', 'en']]);
  const storage = {getItem: k => saved.get(k) ?? null, setItem: (k, v) => saved.set(k, v), removeItem: k => saved.delete(k)};
  const p = createPrefs({storage, asked: 'zh-CN'});
  eq([p.langChoice.value, p.lang.value], ['en', 'en'], 'a language chosen');
  p.setLang('auto');
  eq([p.langChoice.value, p.lang.value, saved.has('tend-lang')], ['auto', 'zh', false], 'the browser\'s, forgetting the choice');
  p.setLang('xx');
  eq(p.langChoice.value, 'auto', 'no such language');
  p.setLook({skin: 'ember', accent: 'aa3300'});
  p.setLook({high: true, accent: 'nothex', skin: '../x'});
  eq(p.skin.value, 'ember-aa3300-high', 'a skin, an accent and the contrast; what is not well formed left');
  p.setLook({accent: ''});
  p.setDensity('comfortable');
  eq([p.skin.value, JSON.parse(saved.get('tend-look'))], ['ember-high', {skin: 'ember', accent: '', high: true, density: 'comfortable'}], 'the accent dropped, kept with the density');
  eq(p.notify.value, true, 'notices are on until turned off');
  p.setNotify(false);
  eq([p.notify.value, createPrefs({storage}).notify.value], [false, false], 'turned off and kept');
});

await run();
