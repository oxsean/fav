// pages_test draws the pages (sign-in, a terminal's sign-in, the home inside the app) in both forms and both languages
// from the home frames, and drives them in a fake document: the home's keys and writes against home-commands, an
// open item's last steps from output-page, the palette and the shortcuts, signing in and allowing a terminal.
process.env.TZ = 'UTC';
import {readFileSync} from 'node:fs';
import {h, render} from '../web/vendor/preact.mjs';
import {signal} from '../web/vendor/signals-core.mjs';
import renderToString from './vendor/render-to-string.mjs';
import {act} from './vendor/test-utils.mjs';
import {createKeys} from '../web/core/keys.js';
import {createRouter} from '../web/core/router.js';
import {form, createNav} from '../web/core/layout.js';
import {createToasts} from '../web/core/toasts.js';
import {createCommands} from '../web/core/commands.js';
import {createPrefs} from '../web/core/prefs.js';
import {words} from '../web/core/i18n.js';
import {html, KeysContext} from '../web/ui/base.js';
import {AuthFrame, Login, Device} from '../web/pages/auth.js';
import {Home} from '../web/pages/home.js';
import {App} from '../web/pages/app.js';
import {install} from './dom.js';
import {settle, clock} from './fake.js';
import {NOW, home} from './rig.js';
import {test, eq, ok, run} from './check.js';

const css = ['base.css', 'components.css', 'pages.css'].map(f => readFileSync(new URL(`../web/css/${f}`, import.meta.url), 'utf8')).join('\n');
const cssClasses = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map(m => m[1]));
const classesOf = s => new Set([...s.matchAll(/class="([^"]*)"/g)].flatMap(m => m[1].split(/\s+/).filter(Boolean)));
const wordsLeft = s => s.match(/\b(auth|signin|denied|device|app|home|why|theme|density|role|act|group|palette|chart|shell|nav)\.[a-zA-Z]+\b/g);
const press = (key, more = {}) => ({key, target: globalThis.document?.body, preventDefault() {}, ...more});
const noStore = {getItem: () => null, setItem() {}};

// fakeHistory keeps the entries as a browser does; back() moves to the one before and tells onPop.
function fakeHistory(url = '/') {
  const loc = {pathname: '/', search: '', hash: ''};
  const set = u => { const x = new URL(u, 'http://tend.test'); loc.pathname = x.pathname; loc.search = x.search; loc.hash = x.hash; };
  const entries = [{url, state: null}];
  let at = 0;
  set(url);
  const history = {
    get state() { return entries[at].state; },
    pushState(state, _, u) { entries.splice(++at, Infinity, {url: u, state}); set(u); },
    replaceState(state, _, u) { entries[at] = {url: u, state}; set(u); },
    back() { if (at > 0) { set(entries[--at].url); history.onPop?.(); } },
    entries: () => entries.slice(0, at + 1).map(e => e.url),
  };
  return {location: loc, history};
}

// app builds the signed-in page around a home rig; ids numbers the command ids c1, c2, …
// wire stands in for the rig's under the commands; changes for core/changes.js's.
function app(r, {url = '/', fetchOutput = () => Promise.resolve({events: []}), wire = r.wire, changes, storage = noStore} = {}) {
  const keys = createKeys({timers: r.clk});
  const toasts = createToasts({timers: r.clk});
  let n = 0;
  const commands = createCommands({wire, newID: () => 'c' + ++n});
  const {location, history} = fakeHistory(url);
  const router = createRouter({location, history});
  history.onPop = () => router.popped();
  const prefs = createPrefs({storage: noStore, asked: 'zh'});
  const nav = createNav({storage: noStore, width: 1440});
  const props = {store: r.store, commands, toasts, wire: r.wire, router, keys, nav, prefs, session: {id: 'u_b', name: 'Bo'},
    clock: () => NOW, fetchOutput, changes, storage, onLogout: () => {}};
  return {...props, history, vnode: () => html`<${KeysContext.Provider} value=${keys}><${App} ...${props} /><//>`};
}

function drawn(vnode, f, lang) {
  form.value = f;
  words.lang.value = lang;
  try { return renderToString(vnode); } finally { form.value = 'desktop'; words.lang.value = 'zh'; }
}

function styled(s, what) {
  eq([...classesOf(s)].filter(c => !cssClasses.has(c)), [], `${what}: classes without a rule`);
  eq(wordsLeft(s), null, `${what}: words not found`);
}

test('the home draws in both forms and both languages, styled and worded', async () => {
  const r = await home();
  const a = app(r);
  const sizes = {};
  for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
    const s = drawn(a.vnode(), f, lang);
    styled(s, `${f}/${lang}`);
    sizes[f + '/' + lang] = s.length;
  }
  const d = drawn(a.vnode(), 'desktop', 'zh');
  for (const want of ['class="home-stats"', 'class="timeline"', 'class="bars"', 'class="meter"', 'Which PDF library should the export use?',
    '要执行 Bash：rm -rf ./testdata/webhooks-old', '运行失败：the check failed', '运行结束，待确认完成：Prices round half-even; the cart tests pass.', '等派发',
    'go vet ./internal/pay/...', '等机器接手（2/2 在用）', '用了 3 个位置，共 4 个', '今天最多同时 3 个', '286k tok · 7 天 $12.47', '2 / 2 满', '离线',
    '869k tok · claude 估算 $12.47', '10 次', '最久一条等了 4h 32m']) ok(d.includes(want), `desktop: no ${want}`);
  const p = drawn(a.vnode(), 'phone', 'en');
  for (const not of ['home-stats', 'timeline', 'class="bars"', 'class="meter"', 'Ended lately']) ok(!p.includes(not), `phone: has ${not}`);
  for (const want of ['Waiting on you', '3 running · 1 queued ›', 'I dispatched', 'class="tabbar"']) ok(p.includes(want), `phone: no ${want}`);
  ok(!p.includes('run-row'), 'phone: the desktop run rows');
  return sizes;
});

test('the phone home: task titles over what they ask, one quick action each, what runs; a row opens its own page', async () => {
  const r = await home();
  const calls = [];
  const wire = {...r.wire, call: (method, params) => { calls.push([method, params]); return new Promise(() => {}); }};
  const kept = new Map();
  const storage = {getItem: k => kept.get(k) ?? null, setItem: (k, v) => kept.set(k, v), removeItem: k => kept.delete(k)};
  const a = app(r, {wire, storage});
  form.value = 'phone';
  try {
    const root = await mount(a.vnode());
    const items = () => root.find('.xi');
    eq(titles(root), ['Receipt PDF export', 'Refund webhook', 'Login rate limit', 'Cart price rounding', 'Price feed importer'], 'task titles, grouped');
    eq(root.find('.xi-sub')[0].textContent, 'Which PDF library should the export use?', 'what it asks under the title');
    eq(items().map(x => x.find('.xi-actions').flatMap(e => e.find('button')).map(b => b.textContent)),
      [[], ['允许'], ['重试'], ['完成'], ['打开']], 'a question opens; the rest have one quick action');
    eq(root.one('.home-going-head').textContent, '在跑 3 · 排队 1 ›', 'what runs, counted');
    eq(root.find('.going-row').length, 3, 'only the running ones listed');
    ok(items().every(x => x.one('.xi-head').getAttribute('aria-expanded') === null && x.one('.xi-toggle').textContent === '›'), 'rows that open a page');

    await act(() => items()[0].one('.xi-head').dispatch('click'));
    eq(a.router.route.value, {page: 'home', wait: 't2'}, 'the row opened its page');
    eq(root.find('.xi').length, 0, 'the list gave way');
    const page = () => root.one('.wait-page');
    const head = () => [page().one('h1').textContent, root.one('.wait-pos').textContent];
    eq(head(), ['等你回答', '1 / 5'], 'what it waits for, and where it stands');
    eq(root.one('.wait-name').one('b').textContent, 'Receipt PDF export', 'the task');
    eq(root.one('.wait-next').textContent, '处理完自动跳到下一条 · 还剩 4 条 · 返回回到「等你」', 'what comes after');
    eq(root.one('.wait-meta').textContent, 't2 · claude @ mba · 你负责', 'whose it is');
    eq(root.one('.wait-goes').find('span')[0].textContent, '回给 claude：它在这一轮里接着做', 'where the answer goes');
    eq(page().find('.answer').length, 1, 'the answer form');

    await act(() => a.router.go({page: 'home', wait: 't3'}, {replace: true}));
    eq(head(), ['等你批准', '2 / 5'], 'a permission');
    eq(page().find('.choice-hint').map(x => x.textContent), [words.t('ans.onceHint')], 'what allow does');
    const allow = page().find('button').find(b => b.textContent.startsWith('允许'));
    await act(() => allow.dispatch('click'));
    eq(calls.at(-1)[0], 'run.answer', 'answered on its page');
    await act(() => settle());
    eq([a.router.route.value.wait, head()], ['t5', ['等你处理', '2 / 4']], 'the one now at its place took over');
    eq(a.history.entries(), ['/', '?wait=t5'], 'in the place of the one handled');

    await act(() => page().one('.back').dispatch('click'));
    eq([a.router.route.value, root.find('.xi').length > 0], [{page: 'home'}, true], 'back to the list');

    await act(() => a.router.go({page: 'home', wait: 't-gone'}));
    eq(root.one('.wait-gone').one('b').textContent, '已经不等你了', 'no longer waiting when it opened');
    await act(() => root.one('.wait-gone').find('button').find(b => b.textContent === '下一条').dispatch('click'));
    eq(a.router.route.value.wait, 't2', 'the first of what waits');

    await act(() => a.router.go({page: 'home'}));
    await act(() => root.find('.chip').find(c => c.textContent.startsWith('我派发的')).dispatch('click'));
    eq([titles(root), kept.get('tend-home-as')], [['Login rate limit'], 'dispatcher'], 'only what the viewer dispatched, remembered');
    const again = await mount(app(r, {wire, storage}).vnode());
    eq(titles(again), ['Login rate limit'], 'the next time too');

    await act(() => root.find('.going-row')[0].dispatch('click'));
    eq(a.router.route.value.page, 'tasks', 'a running row opens its task');
  } finally { form.value = 'desktop'; }
});

test('the phone list allows in one press; the page of a run to accept shows what it changed; the last one handled brings the list back', async () => {
  const r = await home();
  const listed = [];
  const calls = [];
  const wire = {...r.wire, call: (method, params) => { calls.push([method, params]); return new Promise(() => {}); }};
  const changes = {can: () => true, list: run => { listed.push(run); return Promise.resolve({total: {files: 4, add: 12, del: 3}, files: [
    {path: 'cart/round.go', op: 'modify', add: 9, del: 3}, {path: 'cart/round_test.go', op: 'add', add: 3, del: 0},
    {path: 'docs/cart.png', op: 'add', add: 0, del: 0, binary: true}, {path: 'cart/old.go', op: 'delete', add: 0, del: 0}]}); }};
  const a = app(r, {wire, changes});
  form.value = 'phone';
  try {
    const root = await mount(a.vnode());
    await act(() => root.find('.xi')[1].one('.xi-actions').one('button').dispatch('click'));
    eq(calls.at(-1), ['run.answer', {run: 'r3', request: 'p1', allow: true}], 'allow in one press, from the list');
    await act(() => a.router.go({page: 'home', wait: 't4'}));
    await act(() => settle());
    eq(listed, ['r4'], 'the changes of the run to accept, read once');
    eq(root.one('.wait-page').one('h1').textContent, '等你验收', 'to accept');
    eq(root.one('.wait-steps').one('.lbl').textContent, '改动 · 4 个文件 · +12 −3', 'how much it changed');
    eq(root.find('.step').map(x => x.textContent), ['~cart/round.go+9 −3', '+cart/round_test.go+3 −0', '+docs/cart.png'], 'its first three files');

    await act(() => a.router.go({page: 'home', wait: 't5'}, {replace: true}));
    await act(() => { r.store.inbox.value = r.store.inbox.value.filter(x => x.task === 't5'); });
    eq(root.one('.wait-next').textContent, '这是最后一条 · 处理完回到「等你」', 'the last one');
    await act(() => { r.store.inbox.value = []; });
    await act(() => settle());
    eq([a.router.route.value, a.toasts.list.value.map(x => x.text)], [{page: 'home'}, ['都处理完了']], 'the list, and why');
  } finally { form.value = 'desktop'; }
});

test('a notice opened on another page stands on the list: handled, the next takes its place, back is the list', async () => {
  const r = await home();
  const wire = {...r.wire, call: () => new Promise(() => {})};
  const a = app(r, {url: '/?page=runs', wire});
  form.value = 'phone';
  try {
    const root = await mount(a.vnode());
    await act(() => a.router.open('#wait-t3'));
    eq([a.history.entries(), root.one('.wait-name').one('b').textContent], [['/?page=runs', '/', '?wait=t3'], 'Refund webhook'], 'the list under it');
    const allow = root.one('.wait-page').find('button').find(b => b.textContent.startsWith('允许'));
    await act(() => allow.dispatch('click'));
    await act(() => settle());
    eq([a.history.entries(), root.one('.wait-name').one('b').textContent], [['/?page=runs', '/', '?wait=t5'], 'Login rate limit'], 'the next in its place');
    await act(() => root.one('.wait-page').one('.back').dispatch('click'));
    eq([a.router.route.value, root.find('.wait-page').length, root.find('.xi').length > 0], [{page: 'home'}, 0, true], 'the list');
  } finally { form.value = 'desktop'; }
});

test('on a desktop a notice about what waits opens its item in the list', async () => {
  const r = await home();
  const a = app(r, {url: '/?wait=t3'});
  const root = await mount(a.vnode());
  await act(() => settle());
  eq(a.router.route.value, {page: 'home'}, 'the address of the list');
  const open = root.find('.xi').filter(x => x.classList.contains('open'));
  eq(open.map(x => [x.one('.xi-head').getAttribute('aria-controls'), x.classList.contains('sel')]), [['expand-t3', true]], 'the item open and selected');
});

test('the sign-in pages draw in both forms and both languages', () => {
  const http = {logins: () => new Promise(() => {}), invite: () => new Promise(() => {}), device: () => new Promise(() => {})};
  const pages = {
    login: html`<${AuthFrame} onLang=${() => {}}><${Login} http=${http} /><//>`,
    invite: html`<${AuthFrame}><${Login} http=${http} auth=${{kind: 'invite', value: 'abc'}} /><//>`,
    problem: html`<${AuthFrame}><${Login} http=${http} auth=${{kind: 'signin', value: 'state'}} /><//>`,
    denied: html`<${AuthFrame}><${Login} http=${http} auth=${{kind: 'signin', value: 'not_admitted', params: {provider: 'github', username: 'bo'}}} /><//>`,
    device: html`<${AuthFrame}><${Device} http=${http} code="K7QX" onBack=${() => {}} /><//>`,
  };
  for (const [name, v] of Object.entries(pages)) for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) styled(drawn(v, f, lang), `${name} ${f}/${lang}`);
  ok(drawn(pages.problem, 'desktop', 'en').includes(words.both('signin.state')[1]), 'the sign-in problem');
  ok(drawn(pages.denied, 'desktop', 'zh').includes('你用 github 登录的是 bo'), 'who was refused');
  ok(!drawn(pages.denied, 'desktop', 'zh').includes('auth-form'), 'no token form when refused');
});

async function mount(vnode) {
  const root = install();
  await act(() => render(vnode, root));
  return root;
}
const key = (keys, k, more) => act(() => { keys.handle(press(k, more)); });
const selected = root => root.find('.xi').filter(x => x.className.includes('sel')).map(x => x.getAttribute('data-id'));
const titles = root => root.find('.xi-title').map(x => x.textContent);

test('the home by keys: done and undo, an answer by digit, a retry and a stop, each confirmed where it starts or stops', async () => {
  const r = await home();
  const a = app(r);
  const root = await mount(a.vnode());
  const k = (x, more) => key(a.keys, x, more);
  eq(root.find('.xi').length, 5, 'five waiting');
  ok(root.one('.kb-end').textContent.includes(words.t('act.palette')), 'the key bar points at the palette');
  await r.srv.play('home-commands', {
    async done() {
      for (let i = 0; i < 4; i++) await k('j');
      ok(titles(root)[3].includes('Prices round half-even'), 'the fourth waits to be accepted');
      await k('D', {shiftKey: true});
      r.flush();
    },
    async undo() {
      await act(() => settle());
      eq(root.find('.xi').length, 4, 'done: gone from the list');
      ok(root.one('.toast-text').textContent.includes('Cart price rounding'), 'the toast');
      await k('z', {metaKey: true});
    },
    async answer() {
      await act(() => settle());
      eq(root.find('.xi').length, 5, 'undone: back');
      for (let i = 0; i < 4; i++) await k('k');
      ok(titles(root)[0].includes('Which PDF library'), 'the question is first');
      await k('2');
    },
    async retry() {
      await act(() => settle());
      await k('j'); await k('j');
      await k('d');
      ok(root.find('.modal').length === 1 && root.one('.modal').textContent.includes('Login rate limit'), 'd asks first');
      await k('Enter', {metaKey: true});
    },
    async stop() {
      await act(() => settle());
      await k('k'); await k('k');
      await k('x');
      ok(root.one('.modal').textContent.includes(words.t('home.confirmStop')), 'x asks first');
      await k('Escape');
      eq(root.find('.modal').length, 0, 'Esc: nothing sent');
      const row = root.find('.run-row').find(x => x.textContent.includes('Pay timeout on retry'));
      await act(() => row.find('button').at(-1).dispatch('click'));
      await act(() => root.one('.modal').find('button').find(b => b.className.includes('primary')).dispatch('click'));
    },
    async stopped() { await act(() => settle()); },
  });
  eq(r.errors, [], 'errors');
});

test('an open item shows what its run just did, and its choices', async () => {
  const r = await home();
  const pages = [];
  const a = app(r, {fetchOutput: run => { pages.push(run); return r.wire.call('run.output.page', {run, before: -1, n: 20}); }});
  const root = await mount(a.vnode());
  await key(a.keys, 'j');
  await key(a.keys, ' ');
  await r.srv.play('output-page');
  await act(() => settle());
  eq(pages, ['r2'], 'asked once');
  eq(root.find('.step').map(s => s.textContent), ['+internal/receipt/pdf.go', '~internal/receipt/pdf.go', '+pdf library'], 'the last three tools');
  eq(root.find('.choice').map(c => c.find('.choice-n')[0].textContent + c.find('.choice-label')[0].textContent), ['1gofpdf', '2wkhtmltopdf', '3chromedp'], 'its options');
  eq(root.find('.choice').map(c => c.find('.choice-hint').map(h => h.textContent)), [['Pure Go, no binary to install'], ['Renders HTML; needs its binary on every machine'], []],
    'what each says, under its label');
});

test('what a run is doing: its node first, then its note, then what it said; queued runs say why', async () => {
  const store = {state: {tasks: {t: {id: 't', title: 'T', status: 'todo'}, q: {id: 'q', title: 'Q', status: 'todo'}},
    runs: {a: {id: 'a', task: 't', machine: 'm', agent: 'claude', state: 'running', started_at: '2026-09-30T14:00:00Z', doing: '$ go test ./...', note: 'n', last: 'l'},
      b: {id: 'b', task: 'q', machine: 'm', agent: 'claude', state: 'queued', queued_at: '2026-09-30T14:10:00Z'}}},
  rev: {runs: signal(0), tasks: signal(0)}, machines: signal([{name: 'm', state: 'connected', slots: 1, active: 1}]), inbox: signal([]),
  affordances: signal({runs: {}, tasks: {}})};
  const commands = createCommands({wire: {call: () => new Promise(() => {})}});
  const keys = createKeys();
  const s = drawn(html`<${KeysContext.Provider} value=${keys}><${Home} store=${store} commands=${commands} toasts=${createToasts()} clock=${() => NOW} onOpen=${() => {}} onNavigate=${() => {}} /><//>`, 'desktop', 'zh');
  ok(s.includes('class="run-doing ell mono">$ go test ./...'), 'doing, in mono');
  ok(s.includes('等机器接手（1/1 在用）'), 'queued for a slot');
  ok(!s.includes('>n<') && !s.includes('>l<'), 'not the note or the last');
});

test('the palette finds actions by either name and the page\'s own things; ? lists every action', async () => {
  const r = await home();
  const a = app(r);
  const root = await mount(a.vnode());
  await key(a.keys, 'k', {metaKey: true});
  const input = root.one('.palette-input');
  const type = async q => act(() => { input.value = q; input.dispatch('input'); });
  ok(root.find('.pi').length > 5, 'every runnable action');
  ok(!root.find('.pi-text').some(x => x.textContent === words.t('act.next')), 'moves are not listed');
  await type('密度');
  eq(root.find('.pi-text').map(x => x.textContent), [words.t('act.density')], 'by its Chinese name');
  await act(() => input.dispatch('keydown', {key: 'Enter'}));
  eq([root.find('.palette-input').length, a.prefs.density.value], [0, 'comfortable'], 'Enter ran it and closed');
  await key(a.keys, 'k', {metaKey: true});
  await act(() => { const i = root.one('.palette-input'); i.value = 'refund'; i.dispatch('input'); });
  eq(root.find('.pi-text').map(x => x.textContent), ['Refund webhook'], 'a task by title');
  await act(() => root.one('.palette-input').dispatch('keydown', {key: 'Enter'}));
  eq(a.router.route.value, {page: 'tasks', view: 'list', task: 't3'}, 'to the task');
  await key(a.keys, 'g'); await key(a.keys, 'h');
  eq(a.router.route.value.page, 'home', 'g h');
  await key(a.keys, '?', {shiftKey: true});
  eq(root.find('.help-group').length, 6, 'the groups');
  ok(root.one('.help').textContent.includes(words.t('act.unfold')), 'even what no page binds yet');
  await key(a.keys, 'Escape');
  await key(a.keys, 'T', {shiftKey: true});
  eq(a.prefs.theme.value, 'light', 'Shift+T');
});

test('signing in with a token, a wrong token, and a terminal allowed or gone', async () => {
  const signed = [];
  let good = false;
  const http = {logins: async () => [], invite: async () => null, session: async () => ({id: 'u_b', name: 'Bo'}),
    login: async tok => { if (tok !== 'tend_ok') throw Object.assign(new Error('401'), {status: 401, code: 'unauthorized'}); good = true; }};
  const root = await mount(html`<${Login} http=${http} onSignedIn=${s => signed.push(s)} />`);
  const input = root.one('input');
  const submit = async tok => {
    await act(() => { input.value = tok; input.dispatch('input'); });
    await act(() => root.one('form').dispatch('submit'));
    await act(() => settle());
  };
  await submit('tend_bad');
  eq([signed.length, root.one('.field-note').textContent], [0, words.t('auth.badToken')], 'a wrong token');
  await submit('tend_ok');
  eq([good, signed], [true, [{id: 'u_b', name: 'Bo'}]], 'signed in');

  const decided = [];
  const dev = {device: async code => ({code, name: 'mba', ip: '100.64.0.2', created: '2026-09-30T14:30:00Z'}),
    decideDevice: async (code, allow) => { decided.push([code, allow]); }};
  const d = await mount(html`<${Device} http=${dev} code="K7QX" onBack=${() => {}} />`);
  await act(() => settle());
  eq(d.one('.device-code').textContent, 'K7QX', 'the code to check');
  await act(() => d.find('button')[0].dispatch('click'));
  await act(() => settle());
  eq([decided, d.one('h1').textContent], [[['K7QX', true]], words.t('device.allowed')], 'allowed');
  const gone = {device: async () => { throw Object.assign(new Error('404'), {status: 404, code: 'not_found'}); }};
  const g = await mount(html`<${Device} http=${gone} code="OLD" onBack=${() => {}} />`);
  await act(() => settle());
  eq(g.one('.auth-problem').textContent, words.t('device.gone'), 'a gone code');
});

// A phone with no easy way to sign in (a home-screen app keeps its own cookies) shows a code another device allows,
// and is signed in once it is allowed; denied or expired, it can ask again. Installed, that way comes first; an
// invitation, which needs the invitee's own account, does not offer it.
test('signing in from another device', async () => {
  const clk = clock();
  const asked = [], polls = [];
  let answers = [];
  const signed = [];
  const http = {logins: async () => [{name: 'gitea', display: 'Gitea'}], invite: async () => ({inviter: 'Ann', role: 'member'}),
    session: async () => ({id: 'u_b', name: 'Bo'}),
    askDevice: async name => { asked.push(name); return {device_code: 'dc' + asked.length, user_code: 'K7QX-M2PA', verify_url: 'https://tend.test/#device-K7QX-M2PA', interval: 3, expires_in: 600}; },
    pollDevice: async code => { polls.push(code); return answers.shift() || {status: 'pending'}; }};
  const android = {kind: 'browser', os: 'android', name: 'Android'};
  const shared = [];
  const root = await mount(html`<${Login} http=${http} platform=${android} timers=${clk} onSignedIn=${x => signed.push(x)}
    share=${d => { shared.push(d); return Promise.resolve(); }} copy=${() => Promise.resolve()} />`);
  await act(() => settle());
  const other = root.find('button').find(b => b.textContent === words.t('auth.device'));
  ok(other, 'a way to sign in from another device');
  await act(() => other.dispatch('click'));
  await act(() => settle());
  eq(asked, ['Android'], 'asked for a code, named for the device');
  eq(root.one('.device-code').textContent, 'K7QX-M2PA', 'the code');
  ok(root.one('.auth-body').textContent.includes('https://tend.test/#device-K7QX-M2PA'), 'the link to open elsewhere');
  await act(() => root.find('button').find(b => b.textContent === words.t('auth.deviceShare')).dispatch('click'));
  eq(shared, [{title: 'tend', url: 'https://tend.test/#device-K7QX-M2PA'}], 'shared the link');

  await act(() => clk.advance(2999));
  eq(polls, [], 'asked before the interval');
  await act(() => clk.advance(1));
  await act(() => settle());
  eq(polls, ['dc1'], 'polled');
  answers = [{status: 'ok', user: 'Bo'}];
  await act(() => clk.advance(3000));
  await act(() => settle());
  eq(signed, [{id: 'u_b', name: 'Bo'}], 'signed in once allowed');

  for (const [status, text] of [['denied', 'auth.deviceDenied'], ['expired', 'auth.deviceExpired']]) {
    asked.length = 0;
    const r = await mount(html`<${Login} http=${http} platform=${android} timers=${clk} onSignedIn=${() => {}} />`);
    await act(() => settle());
    await act(() => r.find('button').find(b => b.textContent === words.t('auth.device')).dispatch('click'));
    await act(() => settle());
    answers = [{status}];
    await act(() => clk.advance(3000));
    await act(() => settle());
    eq(r.one('.auth-problem').textContent, words.t(text), status);
    await act(() => r.find('button').find(b => b.textContent === words.t('auth.deviceAgain')).dispatch('click'));
    await act(() => settle());
    eq(asked.length, 2, `${status}: asked again`);
    await act(() => r.find('button').find(b => b.textContent === words.t('auth.deviceCancel')).dispatch('click'));
  }

  const cancelled = await mount(html`<${Login} http=${http} platform=${android} timers=${clk} onSignedIn=${() => {}} />`);
  await act(() => settle());
  await act(() => cancelled.find('button').find(b => b.textContent === words.t('auth.device')).dispatch('click'));
  await act(() => settle());
  const before = polls.length;
  await act(() => cancelled.find('button').find(b => b.textContent === words.t('auth.deviceCancel')).dispatch('click'));
  await act(() => clk.advance(10000));
  eq([polls.length, cancelled.find('.device-code').length], [before, 0], 'cancelled: back, and no more polls');

  const pwa = await mount(html`<${Login} http=${http} platform=${{...android, kind: 'pwa'}} timers=${clk} onSignedIn=${() => {}} />`);
  await act(() => settle());
  const first = pwa.find('.btn')[0];
  eq([first.textContent, first.classList.contains('primary')], [words.t('auth.device'), true], 'installed: the first way');
  const invited = await mount(html`<${Login} http=${http} platform=${android} auth=${{kind: 'invite', value: 'abc'}} timers=${clk} onSignedIn=${() => {}} />`);
  await act(() => settle());
  ok(!invited.find('button').some(b => b.textContent === words.t('auth.device')), 'not for an invitation');
});

test('allowing another device signs it in as you', async () => {
  const decided = [];
  const dev = {device: async code => ({code, name: 'Android', ip: '100.64.0.9', created: '2026-09-30T14:30:00Z', session: true}),
    decideDevice: async (code, allow) => { decided.push([code, allow]); }};
  const d = await mount(html`<${Device} http=${dev} code="K7QX-M2PA" onBack=${() => {}} />`);
  await act(() => settle());
  eq([d.one('h1').textContent, d.one('.t-muted').textContent], [words.t('device.titleSession'), words.t('device.helpSession')], 'says what allowing does');
  await act(() => d.find('button')[0].dispatch('click'));
  await act(() => settle());
  eq([decided, d.one('h1').textContent], [[['K7QX-M2PA', true]], words.t('device.allowedSession')], 'allowed');
});

await run();
