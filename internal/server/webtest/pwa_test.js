// pwa_test runs the service worker as tend-server serves it (the path comes as the first argument) on a fake worker
// scope: what it keeps when it installs, what it answers from that and what it leaves to the server, dropping older
// builds, taking over when asked; and platform: which system and form the page runs in, starting the worker only on a
// secure address with a build, and loading a newer build through the waiting worker.
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import {createPlatform} from '../web/core/platform.js';
import {test, eq, ok, run} from './check.js';

const script = readFileSync(process.argv[2], 'utf8');

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

run();
