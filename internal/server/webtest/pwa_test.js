// pwa_test runs the service worker as tend-server serves it (the path comes as the first argument) on a fake worker
// scope: what it keeps when it installs, what it answers from that and what it leaves to the server, dropping older
// builds, taking over when asked; and platform: which system and form the page runs in, starting the worker only on a
// secure address with a build, and loading a newer build through the waiting worker; the pushes the worker shows, what
// a click on one opens, a subscription the browser renews; and the page's push (core/push.js) on the platform's.
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import {createPlatform} from '../web/core/platform.js';
import {createPush} from '../web/core/push.js';
import {noInbox} from '../web/core/store.js';
import {test, eq, ok, run} from './check.js';

const script = readFileSync(process.argv[2], 'utf8');
// ⚠️ The Go test passes the pending kinds (task.Pend*) as the second argument: the worker words each one.
const kinds = JSON.parse(process.argv[3] || '[]');

// worker runs sw.js in a scope of its own: caches in memory, fetch from the server recorded.
function worker(stored = {}) {
  const on = {}, caches = new Map(Object.entries(stored).map(([k, v]) => [k, new Map(Object.entries(v))]));
  const fetched = [];
  let skipped = false;
  const cacheOf = name => {
    if (!caches.has(name)) caches.set(name, new Map());
    const m = caches.get(name);
    return {addAll: async keys => { for (const k of keys) m.set(k, 'kept ' + k); }, match: async k => m.get(k)};
  };
  const self = {
    location: {origin: 'https://tend.test'},
    addEventListener: (type, fn) => { on[type] = fn; },
    skipWaiting: () => { skipped = true; },
  };
  const ctx = vm.createContext({self, URL, Promise, caches: {
    open: async name => cacheOf(name),
    keys: async () => [...caches.keys()],
    delete: async name => caches.delete(name),
  }, fetch: async req => { fetched.push(req.url); return 'net ' + req.url; }});
  vm.runInContext(script, ctx);
  const fire = async (type, data) => {
    let wait = null, answer;
    on[type]({...data, waitUntil: p => { wait = p; }, respondWith: p => { answer = p; }});
    await wait;
    return answer === undefined ? undefined : await answer;
  };
  const get = (path, mode = 'no-cors', method = 'GET', origin = 'https://tend.test') => fire('fetch', {request: {url: origin + path, mode, method}});
  return {fire, get, caches, fetched, skipped: () => skipped};
}

const boot = JSON.parse(/const BOOT = (.*);/.exec(script)[1]);

test('the served worker names its build and the files of it', () => {
  ok(/^[0-9a-f]{12}$/.test(boot.build), `build ${boot.build}`);
  for (const f of ['/', '/main.js', '/core/wire.js', '/vendor/preact.mjs', '/css/base.css']) ok(boot.files.includes(f), `${f} is kept`);
  for (const f of ['/sw.js', '/index.html', '/package.json', '/vendor/manifest.json']) ok(!boot.files.includes(f), `${f} is not kept`);
});

test('installing keeps the files and a visit to the page comes from them', async () => {
  const w = worker();
  await w.fire('install');
  eq([...w.caches.get('tend-' + boot.build).keys()], boot.files, 'kept');
  eq(await w.get('/?page=tasks&task=t1', 'navigate'), 'kept /', 'the page');
  eq(await w.get('/core/wire.js'), 'kept /core/wire.js', 'a script');
  eq(w.fetched, [], 'asked the server');
});

test('the rest goes to the server as it is', async () => {
  const w = worker();
  await w.fire('install');
  for (const [path, mode, method, origin] of [['/theme/tend.css'], ['/api/tokens'], ['/session'], ['/auth/gitea/start', 'navigate'],
    ['/manifest.webmanifest'], ['/login', 'no-cors', 'POST'], ['/core/wire.js', 'no-cors', 'GET', 'https://else.example']]) {
    eq(await w.get(path, mode, method, origin), undefined, `${method || 'GET'} ${origin || ''}${path}`);
  }
});

test('a file the install missed is fetched', async () => {
  const w = worker({['tend-' + boot.build]: {}});
  eq(await w.get('/main.js'), 'net https://tend.test/main.js', 'fetched');
});

test('taking over drops the files of other builds, and only those', async () => {
  const w = worker({'tend-000000000000': {'/': 'old'}, 'someone-else': {}});
  await w.fire('install');
  await w.fire('activate');
  eq([...w.caches.keys()].sort(), ['someone-else', 'tend-' + boot.build], 'caches');
});

test('the worker takes over only when the page asks', async () => {
  const w = worker();
  await w.fire('message', {data: 'hello'});
  ok(!w.skipped(), 'skipped on another message');
  await w.fire('message', {data: 'skip'});
  ok(w.skipped(), 'did not skip');
});

const ua = {
  iphone: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1',
  ipad: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15',
  android: 'Mozilla/5.0 (Linux; Android 12; MuMu) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Mobile Safari/537.36',
  firefox: 'Mozilla/5.0 (Android 12; Mobile; rv:131.0) Gecko/131.0 Firefox/131.0',
  mac: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36',
};
const standalone = on => q => ({matches: on && q === '(display-mode: standalone)'});

test('which system and form the page runs in, and a name for the device', () => {
  const of = (nav, media = standalone(false)) => { const p = createPlatform({nav, media}); return [p.os, p.kind, p.name]; };
  eq(of({userAgent: ua.iphone}), ['ios', 'browser', 'iPhone'], 'Safari on an iPhone');
  eq(of({userAgent: ua.iphone, standalone: true}), ['ios', 'pwa', 'iPhone'], 'from the home screen');
  eq(of({userAgent: ua.ipad, platform: 'MacIntel', maxTouchPoints: 5}), ['ios', 'browser', 'iPad'], 'an iPad asking for the desktop site');
  eq(of({userAgent: ua.android}, standalone(true)), ['android', 'pwa', 'Android'], 'installed on Android');
  eq(of({userAgent: ua.firefox}), ['android', 'browser', 'Android'], 'Firefox on Android');
  eq(of({userAgent: ua.mac, platform: 'MacIntel', maxTouchPoints: 0}), ['other', 'browser', 'Mac'], 'a Mac');
  eq(of({}, () => undefined), ['other', 'browser', 'Browser'], 'nothing known');
});

// fakeSW is a navigator.serviceWorker: its registration (null for none) and what the page did with it.
function fakeSW(reg, controller = {}) {
  const did = [], on = {};
  return {did, on, controller, register: async (url, opt) => { did.push(['register', url, opt.scope]); }, getRegistration: async () => reg,
    addEventListener: (type, fn) => { on[type] = fn; did.push(['listen', type]); }};
}

function fakeWorker(state) {
  const w = {state, said: [], on: null};
  w.postMessage = m => w.said.push(m);
  w.addEventListener = (type, fn) => { w.on = fn; };
  return w;
}

test('the worker starts only on a secure address with a build', () => {
  for (const [secure, build, want] of [[false, 'b1', 0], [true, '', 0], [true, 'b1', 1]]) {
    const sw = fakeSW(null);
    createPlatform({nav: {serviceWorker: sw}, media: standalone(false), secure, build}).start();
    eq(sw.did.length, want, `secure ${secure}, build ${build || 'none'}`);
  }
  const sw = fakeSW(null);
  createPlatform({nav: {serviceWorker: sw}, secure: true, build: 'b1'}).start();
  eq(sw.did, [['register', '/sw.js', '/']], 'registered');
});

test('a newer build loads through the worker waiting for it', async () => {
  let reloads = 0;
  const reload = () => { reloads++; };
  const plat = sw => createPlatform({nav: {serviceWorker: sw}, secure: true, build: 'b1', reload});

  await plat(fakeSW(null)).refresh();
  eq(reloads, 1, 'no worker: a plain reload');

  const waiting = fakeWorker('installed');
  const sw = fakeSW({waiting, update: async () => {}});
  await plat(sw).refresh();
  eq(waiting.said, ['skip'], 'told the waiting worker');
  eq(reloads, 1, 'reloaded before the new worker took over');
  sw.on.controllerchange();
  eq(reloads, 2, 'reloaded once it took over');

  const installing = fakeWorker('installing');
  const sw2 = fakeSW({installing, update: async () => {}});
  await plat(sw2).refresh();
  eq(installing.said, [], 'told it while it was installing');
  installing.state = 'installed';
  installing.on();
  eq(installing.said, ['skip'], 'told it once installed');

  let updated = 0;
  await plat(fakeSW({update: async () => { updated++; }})).refresh();
  eq([updated, reloads], [1, 3], 'nothing newer after asking: a plain reload');

  await plat(fakeSW({waiting: fakeWorker('installed')}, null)).refresh();
  eq(reloads, 4, 'a page no worker controls reloads from the server');
});

// plain is a value of the worker's realm as one of this one.
const plain = v => JSON.parse(JSON.stringify(v));

// pusher runs sw.js with what its push handling touches: the notices it shows, the windows of the page open (url,
// posted, focused), the windows it opens, the subscriptions it makes and the requests it sends, which answer (a
// function of the request: a status and a body, or a throw for no network).
function pusher({lang = 'zh-CN', wins = [], answer = () => [200, {}]} = {}) {
  const on = {}, shown = [], opened = [], sent = [], subscribed = [];
  const self = {
    location: {origin: 'https://tend.test'}, navigator: {language: lang},
    addEventListener: (type, fn) => { on[type] = fn; },
    registration: {showNotification: async (title, opts) => { shown.push(plain({title, ...opts})); },
      pushManager: {subscribe: async opt => { subscribed.push({userVisibleOnly: opt.userVisibleOnly, applicationServerKey: opt.applicationServerKey}); return {toJSON: () => ({endpoint: 'https://push.example/new', keys: {}})}; }}},
    clients: {matchAll: async () => wins, openWindow: async url => { opened.push(url); }},
  };
  vm.runInContext(script, vm.createContext({self, URL, Promise, JSON, String, caches: {}, fetch: async (url, opt) => {
    sent.push({url, ...opt});
    const [status, body] = answer(url, opt);
    return {status, json: async () => body};
  }}));
  const fire = async (type, data) => {
    let wait = null;
    on[type]({...data, waitUntil: p => { wait = p; }});
    await wait;
  };
  const push = m => fire('push', {data: {json: () => (typeof m === 'string' ? JSON.parse(m) : m)}});
  return {fire, push, shown, opened, sent, subscribed};
}

const win = url => { const w = {url, posted: [], focused: 0}; w.postMessage = m => w.posted.push(plain(m)); w.focus = async () => { w.focused++; }; return w; };
const msg = (over = {}) => ({v: 1, server: 'c1', seq: 812, event: 'task.needs_you', task: 't-3f2', item: 'r-1/q1', kind: 'permission',
  title: '迁移脚本要删表', what: "psql -c 'DROP TABLE orders_old'", n: 1, link: '#wait-t-3f2', at: '2026-09-30T12:00:00Z', ...over});

test('a push shows one notice per task, saying what waits', async () => {
  const w = pusher();
  await w.push(msg());
  await w.push(msg({kind: 'question', item: 'r-1/q2', what: undefined, n: 2}));
  eq(w.shown.map(n => [n.title, n.body, n.tag, n.renotify, n.data]), [
    ['迁移脚本要删表', "要你允许：psql -c 'DROP TABLE orders_old'", 't-3f2', true, {task: 't-3f2', item: 'r-1/q1', link: '#wait-t-3f2'}],
    ['迁移脚本要删表 · 2 项等你', 'agent 有问题问你', 't-3f2', true, {task: 't-3f2', item: 'r-1/q2', link: '#wait-t-3f2'}],
  ], 'notices');
  eq(w.shown.map(n => n.actions), [[{action: 'view', title: '查看'}], [{action: 'view', title: '查看'}]], 'without a token, only the view button');
  const en = pusher({lang: 'en-US'});
  await en.push(msg({what: ''}));
  eq([en.shown[0].title, en.shown[0].body], ['迁移脚本要删表', 'Asks to use a tool'], 'in English, without what it would run');
});

test('a permission the viewer may deny gets a deny button beside the view one, its token kept on the notice', async () => {
  for (const [lang, view, reject] of [['zh-CN', '查看', '拒绝'], ['en', 'View', 'Deny']]) {
    const w = pusher({lang});
    await w.push(msg({actions: [{action: 'reject', token: 'a1.x.y'}, {action: 'later', token: 'a1.z.z'}]}));
    eq(w.shown[0].actions, [{action: 'view', title: view}, {action: 'reject', title: reject}], `${lang}: buttons`);
    eq(w.shown[0].data.act, 'a1.x.y', `${lang}: the token`);
  }
});

test('a push with its content hidden says only how many wait, and a task done says it is done', async () => {
  const w = pusher();
  await w.push({v: 1, server: 'c1', seq: 9, event: 'task.needs_you', n: 3});
  await w.push({v: 1, server: 'c1', seq: 10, event: 'task.needs_you', n: 0});
  await w.push({v: 1, server: 'c1', seq: 11, event: 'task.done', n: 0});
  await w.push({v: 1, server: 'c1', seq: 12, event: 'task.done', task: 't-9', title: '发版', link: '#task-t-9', n: 0});
  eq(w.shown.map(n => [n.title, n.body, n.tag, n.actions, n.data]), [
    ['tend', '3 项等你', 'tend', undefined, {count: true, link: ''}],
    ['tend', '1 项等你', 'tend', undefined, {count: true, link: ''}],
    ['tend', '任务完成了', 'tend-done', undefined, {link: ''}],
    ['发版', '任务完成了', 't-9', undefined, {task: 't-9', link: '#task-t-9'}],
  ], 'notices');
  const en = pusher({lang: 'en'});
  await en.push({v: 1, server: 'c1', seq: 9, event: 'task.needs_you', n: 2});
  eq(en.shown[0].body, '2 waiting on you', 'in English');
});

test('a push the worker cannot read still shows a notice', async () => {
  const w = pusher();
  await w.push('{"v":2}');
  await w.fire('push', {data: {json: () => { throw new Error('not JSON'); }}});
  eq(w.shown.length, 2, 'shown');
  ok(w.shown.every(n => n.title === 'tend' && n.body), 'a plain notice');
});

test('the worker words every kind of pending item in both languages', async () => {
  ok(kinds.length > 0, 'no kinds from the Go test');
  const bodies = {};
  for (const lang of ['zh-CN', 'en']) {
    const w = pusher({lang});
    for (const kind of kinds) await w.push(msg({kind, what: 'x'}));
    bodies[lang] = w.shown.map(n => n.body);
    eq(new Set(bodies[lang]).size, kinds.length, `${lang}: one text per kind`);
  }
  ok(bodies['zh-CN'].every((b, i) => b !== bodies.en[i]), 'a kind worded the same in both languages');
});

const notice = (data, over = {}) => ({title: '迁移脚本要删表', tag: 't-3f2', data, closed: 0, close() { this.closed++; }, ...over});

test('clicking a notice or its view button brings a window of the page to what it is about', async () => {
  for (const action of ['', 'view']) {
    const open = win('https://tend.test/?page=tasks'), other = win('https://else.example/');
    const w = pusher({wins: [other, open]});
    const n = notice({link: '#wait-t-3f2', act: 'a1.x.y'});
    await w.fire('notificationclick', {notification: n, action});
    eq([n.closed, open.posted, open.focused, other.posted, w.opened, w.sent], [1, [{open: '#wait-t-3f2'}], 1, [], [], []], `${action || 'the body'}: the open window`);
  }
  const none = pusher();
  await none.fire('notificationclick', {notification: notice({link: '#wait-t-1'}), action: 'view'});
  eq(none.opened, ['/#wait-t-1'], 'a new window');
  const hidden = pusher();
  await hidden.fire('notificationclick', {notification: notice({count: true, link: ''})});
  eq(hidden.opened, ['/'], 'a hidden push opens the page');
});

test('the deny button sends its token with the page cookie and shows how that went, opening nothing', async () => {
  const cases = [
    ['done', () => [204, null], '已拒绝'],
    ['someone else first', () => [409, {error: 'request_gone', by: 'Bob'}], '已被 Bob 处理'],
    ['no longer asked', () => [409, {error: 'request_gone'}], '已经不等你了'],
    ['signed out', () => [401, {error: 'unauthorized'}], '请打开 tend 重新登录后处理'],
    ['expired', () => [410, {error: 'expired'}], '没能拒绝，打开 tend 处理'],
    ['refused', () => [403, {error: 'token'}], '没能拒绝，打开 tend 处理'],
    ['no network', () => { throw new Error('offline'); }, '没能拒绝，打开 tend 处理'],
  ];
  for (const [name, answer, body] of cases) {
    const open = win('https://tend.test/');
    const w = pusher({wins: [open], answer});
    const n = notice({task: 't-3f2', item: 'r-1/q1', link: '#wait-t-3f2', act: 'a1.x.y'});
    await w.fire('notificationclick', {notification: n, action: 'reject'});
    eq(w.sent.map(r => [r.url, r.method, r.credentials, r.headers['X-Tend'], JSON.parse(r.body)]), [['/api/act', 'POST', 'same-origin', '1', {token: 'a1.x.y'}]], `${name}: sent`);
    eq(w.shown.map(x => [x.title, x.body, x.tag, x.actions, x.data]), [['迁移脚本要删表', body, 't-3f2', undefined, {task: 't-3f2', link: '#wait-t-3f2'}]], `${name}: shown`);
    eq([n.closed, open.posted, w.opened], [1, [], []], `${name}: opened nothing`);
  }
  const en = pusher({lang: 'en', answer: () => [409, {error: 'request_gone', by: 'Bob'}]});
  await en.fire('notificationclick', {notification: notice({link: '', act: 'a1.x.y'}), action: 'reject'});
  eq(en.shown[0].body, 'Already handled by Bob', 'in English');
  const bare = pusher();
  await bare.fire('notificationclick', {notification: notice({link: '#wait-t-3f2'}), action: 'reject'});
  eq([bare.sent, bare.opened], [[], ['/#wait-t-3f2']], 'a deny without a token opens the page');
});

test('a subscription the browser renews is made again with the same key and registered', async () => {
  const w = pusher();
  const key = new Uint8Array([4, 1, 2]);
  await w.fire('pushsubscriptionchange', {oldSubscription: {options: {applicationServerKey: key}}});
  eq(w.subscribed, [{userVisibleOnly: true, applicationServerKey: key}], 'subscribed');
  eq(w.sent.map(r => [r.url, r.method, r.headers['X-Tend'], JSON.parse(r.body).subscription.endpoint]), [['/api/push/device', 'PUT', '1', 'https://push.example/new']], 'registered');
});

// pushPlatform is a platform whose browser has push: its permission, what asking answers, its subscription and its
// notices; did is what the page did with them.
function pushPlatform({permission = 'default', answer = 'granted', sub = null, notices = []} = {}) {
  const did = [];
  let current = sub;
  const reg = {
    pushManager: {
      getSubscription: async () => current,
      subscribe: async opt => { did.push(['subscribe', [...opt.applicationServerKey]]); current = subOf('https://push.example/b', opt.applicationServerKey); return current; },
    },
    getNotifications: async () => notices,
  };
  const subOf = (endpoint, key) => ({endpoint, options: {applicationServerKey: key.buffer || key},
    toJSON: () => ({endpoint, keys: {p256dh: 'p', auth: 'a'}}), unsubscribe: async () => { did.push(['unsubscribe', endpoint]); current = null; }});
  const Notification = {permission, requestPermission: () => { did.push(['ask']); Notification.permission = answer; return Promise.resolve(answer); }};
  const sw = {ready: Promise.resolve(reg), register: async () => {}, addEventListener: (type, fn) => { sw.on = fn; }};
  const p = createPlatform({nav: {serviceWorker: sw, userAgent: ua.android}, secure: true, build: 'b1', notices: Notification, pushes: true});
  return {p, did, sw, subOf, set: s => { current = s; }};
}

// pushHTTP is the page's http for push: the server's key and what the page registered and dropped.
function pushHTTP(key = 'BAEC') {
  const calls = [];
  return {calls, pushKey: async () => { if (!key) throw Object.assign(new Error('503'), {status: 503, code: 'push_key'}); return key; },
    putDevice: async (sub, name) => { calls.push(['put', sub.endpoint, name]); }, dropDevice: async e => { calls.push(['drop', e]); }};
}

test('the platform has push only where the browser has a worker, a PushManager and notices', () => {
  const sw = {register: async () => {}};
  const of = o => createPlatform({nav: {serviceWorker: sw}, secure: true, build: 'b1', notices: {}, pushes: true, ...o}).push;
  ok(of({}), 'all there');
  for (const o of [{secure: false}, {build: ''}, {pushes: false}, {notices: undefined}]) eq(of(o), null, JSON.stringify(o));
});

test('turning push on asks first, then subscribes with the server key and registers the device', async () => {
  const {p, did} = pushPlatform();
  const http = pushHTTP('BAEC');
  const push = createPush({http, platform: p});
  eq(await push.state(), 'off', 'before');
  eq(await push.on(), 'on', 'after');
  eq(did, [['ask'], ['subscribe', [4, 1, 2]]], 'asked, then subscribed');
  eq(http.calls, [['put', 'https://push.example/b', 'Android']], 'registered');
  eq(await push.off(), 'off', 'off again');
  eq(http.calls.at(-1), ['drop', 'https://push.example/b'], 'dropped');

  const refused = pushPlatform({answer: 'denied'});
  const r = createPush({http: pushHTTP(), platform: refused.p});
  eq(await r.on(), 'denied', 'the browser refused');
  eq(refused.did, [['ask']], 'did not subscribe');
  eq(await r.state(), 'denied', 'stays refused');

  const nokey = pushPlatform();
  let err = null;
  await createPush({http: pushHTTP(''), platform: nokey.p}).on().catch(e => { err = e; });
  eq([err?.code, nokey.did], ['push_key', [['ask']]], 'a server without a key');
  eq(await createPush({http: pushHTTP(), platform: {push: null}}).state(), 'none', 'no push in this browser');
});

test('the page renews its subscription, made again when the server key changed', async () => {
  const none = pushPlatform();
  const h0 = pushHTTP();
  await createPush({http: h0, platform: none.p}).renew();
  eq(h0.calls, [], 'nothing to renew');

  const same = pushPlatform({permission: 'granted'});
  same.set(same.subOf('https://push.example/a', new Uint8Array([4, 1, 2])));
  const h1 = pushHTTP('BAEC');
  await createPush({http: h1, platform: same.p}).renew();
  eq([same.did, h1.calls], [[], [['put', 'https://push.example/a', 'Android']]], 'the same key: renewed as it is');

  const moved = pushPlatform({permission: 'granted'});
  moved.set(moved.subOf('https://push.example/a', new Uint8Array([4, 9, 9])));
  const h2 = pushHTTP('BAEC');
  await createPush({http: h2, platform: moved.p}).renew();
  eq(moved.did, [['unsubscribe', 'https://push.example/a'], ['subscribe', [4, 1, 2]]], 'subscribed again');
  eq(h2.calls, [['put', 'https://push.example/b', 'Android']], 'the new one registered');
});

test('the notices of what no longer waits close, and a clicked one opens its link in the page', async () => {
  const note = item => ({data: {item}, closed: false, close() { this.closed = true; }});
  const gone = note('r-1/q1'), still = note('r-1/q2'), other = {closed: false, close() { this.closed = true; }};
  const count = {data: {count: true}, closed: false, close() { this.closed = true; }};
  const {p, sw} = pushPlatform({notices: [gone, still, other, count]});
  const push = createPush({http: pushHTTP(), platform: p});
  push.clear(noInbox);
  await new Promise(r => setTimeout(r, 0));
  ok(!gone.closed, 'closed before the inbox came');
  push.clear([{task: 't1', pending: [{id: 'r-1/q2'}]}, {task: 't2', reason: 'draft'}]);
  await new Promise(r => setTimeout(r, 0));
  eq([gone.closed, still.closed, other.closed, count.closed], [true, false, false, false], 'closed');
  push.clear([]);
  await new Promise(r => setTimeout(r, 0));
  eq([still.closed, other.closed, count.closed], [true, false, true], 'nothing waits');
  const links = [];
  p.onOpen(l => links.push(l));
  sw.on({data: {open: '#task-t1'}});
  sw.on({data: 'skip'});
  eq(links, ['#task-t1'], 'opened');
});

run();
