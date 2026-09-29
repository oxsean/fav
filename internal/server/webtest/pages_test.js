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
import {settle} from './fake.js';
import {NOW, home} from './rig.js';
import {test, eq, ok, run} from './check.js';

const css = ['base.css', 'components.css', 'pages.css'].map(f => readFileSync(new URL(`../web/css/${f}`, import.meta.url), 'utf8')).join('\n');
const cssClasses = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map(m => m[1]));
const classesOf = s => new Set([...s.matchAll(/class="([^"]*)"/g)].flatMap(m => m[1].split(/\s+/).filter(Boolean)));
const wordsLeft = s => s.match(/\b(auth|signin|denied|device|app|home|why|theme|density|role|act|group|palette|chart|shell|nav)\.[a-zA-Z]+\b/g);
const press = (key, more = {}) => ({key, target: globalThis.document?.body, preventDefault() {}, ...more});
const noStore = {getItem: () => null, setItem() {}};

function fakeHistory(url = '/') {
  const loc = {pathname: '/', search: '', hash: ''};
  const set = u => { const x = new URL(u, 'http://tend.test'); loc.pathname = x.pathname; loc.search = x.search; loc.hash = x.hash; };
  set(url);
  return {location: loc, history: {pushState: (_, __, u) => set(u), replaceState: (_, __, u) => set(u)}};
}

// app builds the signed-in page around a home rig; ids numbers the command ids c1, c2, …
function app(r, {url = '/', fetchOutput = () => Promise.resolve({events: []})} = {}) {
  const keys = createKeys({timers: r.clk});
  const toasts = createToasts({timers: r.clk});
  let n = 0;
  const commands = createCommands({wire: r.wire, newID: () => 'c' + ++n});
  const {location, history} = fakeHistory(url);
  const router = createRouter({location, history});
  const prefs = createPrefs({storage: noStore, asked: 'zh'});
  const nav = createNav({storage: noStore, width: 1440});
  const props = {store: r.store, commands, toasts, wire: r.wire, router, keys, nav, prefs, session: {id: 'u_b', name: 'Bo'},
    clock: () => NOW, fetchOutput, onLogout: () => {}};
  return {...props, vnode: () => html`<${KeysContext.Provider} value=${keys}><${App} ...${props} /><//>`};
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
    'go vet ./internal/pay/...', '等机器接手（2/2 在用）', '4 个位置里用了 3 个', '今天最多同时 3 个', '286k tok · 7 天 $12.47', '2 / 2 满', '离线',
    '869k tok · claude 估算 $12.47', '10 次', '最久一条等了 4h 32m']) ok(d.includes(want), `desktop: no ${want}`);
  const p = drawn(a.vnode(), 'phone', 'en');
  for (const not of ['home-stats', 'timeline', 'class="bars"', 'class="meter"', 'Ended lately']) ok(!p.includes(not), `phone: has ${not}`);
  for (const want of ['Waiting on you', 'Running / queued', 'class="tabbar"']) ok(p.includes(want), `phone: no ${want}`);
  return sizes;
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
  eq(root.find('.choice').map(c => c.textContent), ['1gofpdf', '2wkhtmltopdf', '3chromedp'], 'its options');
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
  eq(root.find('.help-group').length, 5, 'the groups');
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

await run();
