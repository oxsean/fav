// team_test draws the machines page in both forms and both languages from the team frames and the /api answers in
// api.json, and drives it in a fake document: grouped as each viewer sees them, a machine's sharing changed by its
// owner, a machine added and its token shown once, a node token moved and revoked, Enter opening the runs page on a
// machine, and a phone showing a machine's facts without the controls.
process.env.TZ = 'UTC';
import {readFileSync} from 'node:fs';
import {render} from '../web/vendor/preact.mjs';
import renderToString from './vendor/render-to-string.mjs';
import {act} from './vendor/test-utils.mjs';
import {createKeys} from '../web/core/keys.js';
import {createRouter} from '../web/core/router.js';
import {form, createNav} from '../web/core/layout.js';
import {createToasts} from '../web/core/toasts.js';
import {createCommands} from '../web/core/commands.js';
import {createPrefs} from '../web/core/prefs.js';
import {words} from '../web/core/i18n.js';
import {signal} from '../web/vendor/signals-core.mjs';
import {html, KeysContext} from '../web/ui/base.js';
import {App} from '../web/pages/app.js';
import {RUNS_FILTER_KEY} from '../web/pages/runs.js';
import {install} from './dom.js';
import {settle} from './fake.js';
import {NOW, team} from './rig.js';
import {test, eq, ok, run, until} from './check.js';

const css = ['base.css', 'components.css', 'pages.css'].map(f => readFileSync(new URL(`../web/css/${f}`, import.meta.url), 'utf8')).join('\n');
const cssClasses = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map(m => m[1]));
const classesOf = s => new Set([...s.matchAll(/class="([^"]*)"/g)].flatMap(m => m[1].split(/\s+/).filter(Boolean)));
const wordsLeft = s => s.match(/\b(mach|team|secret|api|m|role|app|home|confirm|form|why|status|ui|picker)\.[a-zA-Z_]+\b/g);
const press = (key, more = {}) => ({key, target: globalThis.document?.body, preventDefault() {}, ...more});
const api = JSON.parse(readFileSync(new URL('./api.json', import.meta.url), 'utf8'));

const admin = {id: 'u_a', name: 'Ann Lee', role: 'admin'};
const bo = {id: 'u_b', name: 'Bo Lin', role: 'member'};
const names = signal(Object.fromEntries(api['GET /api/users'].map(u => [u.id, u.name])));

function memory(init = {}) {
  const m = new Map(Object.entries(init));
  return {getItem: k => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: k => m.delete(k), map: m};
}

// fakeHTTP answers from api.json and keeps what the page asked, as [method path, body].
function fakeHTTP() {
  const calls = [];
  const ans = (key, body) => { calls.push(body === undefined ? [key] : [key, body]); return Promise.resolve(structuredClone(api[key] ?? null)); };
  return {
    calls,
    machineCreds: () => ans('GET /api/machines'),
    addMachine: name => ans('POST /api/machines', {name}),
    rebindMachine: id => ans('POST /api/machines/rebind', {id}),
    revokeMachine: id => ans('DELETE /api/machines', {id}),
  };
}

function fakeHistory(url) {
  const loc = {pathname: '/', search: '', hash: ''};
  const set = u => { const x = new URL(u, 'http://tend.test'); loc.pathname = x.pathname; loc.search = x.search; loc.hash = x.hash; };
  set(url);
  return {location: loc, history: {pushState: (_, __, u) => set(u), replaceState: (_, __, u) => set(u)}};
}

function app(r, {url = '/?page=machines', session = admin, http = fakeHTTP(), storage = memory(), copy = () => Promise.resolve()} = {}) {
  const keys = createKeys({timers: r.clk});
  const toasts = createToasts({timers: r.clk});
  let n = 0;
  const commands = createCommands({wire: r.wire, newID: () => 'c' + ++n});
  const {location, history} = fakeHistory(url);
  const router = createRouter({location, history});
  const noStore = {getItem: () => null, setItem() {}};
  const props = {store: r.store, commands, toasts, wire: r.wire, http, router, keys, nav: createNav({storage: noStore, width: 1440}),
    prefs: createPrefs({storage: noStore, asked: 'zh'}), session, names, clock: () => NOW, fetchOutput: () => Promise.resolve({events: []}),
    storage, copy, onLogout() {}};
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

async function mount(vnode, f = 'desktop') {
  form.value = f;
  const root = install();
  await act(() => render(vnode, root));
  return root;
}
const settled = () => act(() => settle());
const click = el => act(() => el.dispatch('click'));
const type = (el, text) => act(() => { el.value = text; el.dispatch('input'); });
// buttonOf is the one button labelled label, its key hint left out.
const labelOf = b => b.textContent.slice(0, b.textContent.length - b.find('kbd').map(k => k.textContent).join('').length).trim();
const buttonOf = (root, label) => {
  const got = root.find('button').filter(b => labelOf(b) === label);
  if (got.length !== 1) throw new Error(`${label}: ${got.length} buttons`);
  return got[0];
};
const groupsOf = root => root.find('.mach-group').map(g => [g.one('h2').childNodes[0].textContent.trim(), g.find('.mach-card').map(c => c.one('b').textContent)]);

test('machines: drawn in both forms and languages, grouped as each viewer sees them', async () => {
  const r = await team();
  for (const session of [admin, bo]) for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
    styled(drawn(app(r, {session}).vnode(), f, lang), `${session.id} ${f}/${lang}`);
  }
  const t = words.t;
  eq(groupsOf(await mount(app(r).vnode())), [[t('mach.mine'), ['linux', 'mba', 'win']], [t('mach.others'), ['bo-laptop', 'old-box']]], 'an admin\'s');
  eq(groupsOf(await mount(app(r, {session: bo}).vnode())), [[t('mach.mine'), ['bo-laptop']], [t('mach.toMe'), ['linux', 'mba']],
    [t('mach.others'), ['old-box', 'win']]], 'a member\'s: shared by the project they are in');
  const s = drawn(app(r).vnode(), 'desktop', 'en');
  ok(s.includes('4 machines · 3 up · 2/5 slots in use · 1 queued'), 'the summary line: ' + /\d machines[^<]*/.exec(s)?.[0]);
  eq(r.errors, [], 'errors');
});

test('machines: the picked machine\'s facts, its notes, and who may change its sharing', async () => {
  const r = await team();
  const root = await mount(app(r).vnode());
  await click(root.find('.mach-card').find(c => c.one('b').textContent === 'win'));
  const aside = root.one('.mach-aside');
  ok(aside.textContent.includes(words.f('mach.note.missing', 'files')), 'what its tend lacks');
  ok(buttonOf(aside, words.t('mach.share')), 'its owner shares it');
  await click(root.find('.mach-card').find(c => c.one('b').textContent === 'mba'));
  ok(root.one('.mach-aside').textContent.includes(words.f('mach.note.auth', 'codex')), 'a CLI not signed in');
  ok(root.one('.mach-aside').textContent.includes(words.t('mach.canApprove')), 'who it is shared with');
  await click(root.find('.mach-card').find(c => c.one('b').textContent === 'old-box'));
  eq(root.one('.mach-aside').find('button').filter(b => labelOf(b) === words.t('mach.share')).length, 0, 'a retired machine is not shared');
  const other = await mount(app(r, {session: bo}).vnode());
  await click(other.find('.mach-card').find(c => c.one('b').textContent === 'mba'));
  eq(other.one('.mach-aside').find('button').filter(b => labelOf(b) === words.t('mach.share')).length, 0, 'nobody else shares it');
});

test('machines: its owner changes who else may use it', async () => {
  const r = await team();
  const root = await mount(app(r).vnode());
  await r.srv.play('team-share', {
    async share() {
      await click(root.find('.mach-card').find(c => c.one('b').textContent === 'linux'));
      await click(buttonOf(root.one('.mach-aside'), words.t('mach.share')));
      ok(root.one('.modal').textContent.includes(words.t('mach.trust').slice(0, 12)), 'what sharing means');
      await click(root.one('.modal').find('.picker-btn')[0]);
      await click(root.find('[role=option]').find(o => o.textContent.includes('Bo Lin')));
      await click(buttonOf(root, words.t('picker.done')));
      await click(root.one('.modal').find('[role=radio]').find(b => b.textContent === words.t('mach.approveYes')));
      await click(buttonOf(root.one('.modal-foot'), words.t('form.save')));
    },
    async shared() {
      await settled();
      eq(root.find('.modal').length, 0, 'closed');
      ok(root.one('.toast-text').textContent === words.f('mach.sharedDone', 'linux'), 'said');
    },
  });
  eq(r.errors, [], 'errors');
});

test('machines: a machine added shows its token once; a node token is moved and revoked after a confirm', async () => {
  const r = await team();
  const http = fakeHTTP();
  const copied = [];
  const root = await mount(app(r, {http, copy: text => { copied.push(text); return Promise.resolve(); }}).vnode());
  await until(() => http.calls.length > 0, 'the node tokens');
  await settled();
  await click(buttonOf(root, words.t('mach.add')));
  const go = () => buttonOf(root.one('.modal-foot'), words.t('mach.addGo'));
  await type(root.one('.modal').find('input')[0], 'bad name');
  eq(go().getAttribute('disabled') !== null, true, 'a name the server refuses');
  await type(root.one('.modal').find('input')[0], 'lab-1');
  await click(go());
  await settled();
  const box = root.one('.modal');
  eq(box.find('.secret-value').map(x => x.textContent), [api['POST /api/machines'].token, api['POST /api/machines'].command], 'the token and the command');
  await click(box.find('.secret')[0].one('button'));
  await settled();
  eq(copied, [api['POST /api/machines'].token], 'copied');
  await click(buttonOf(root.one('.modal-foot'), words.t('mach.doneAdd')));

  await click(root.find('.mach-card').find(c => c.one('b').textContent === 'mba'));
  await click(buttonOf(root.one('.mach-aside'), words.t('mach.rebind')));
  await click(buttonOf(root.one('.modal-foot'), words.t('mach.rebind')));
  await settled();
  await click(buttonOf(root.one('.mach-aside'), words.t('mach.revoke')));
  await click(buttonOf(root.one('.modal-foot'), words.t('confirm.keep')));
  await click(buttonOf(root.one('.mach-aside'), words.t('mach.revoke')));
  await click(buttonOf(root.one('.modal-foot'), words.t('mach.revoke')));
  await settled();
  eq(http.calls.filter(c => c[0] !== 'GET /api/machines'), [['POST /api/machines', {name: 'lab-1'}], ['POST /api/machines/rebind', {id: 'n1'}],
    ['DELETE /api/machines', {id: 'n1'}]], 'the writes');
  ok(root.find('.toast-text').some(x => x.textContent === words.f('mach.revoked', 'mba')), 'said');
  await click(root.find('.mach-card').find(c => c.one('b').textContent === 'old-box'));
  eq(root.one('.mach-aside').find('button').filter(b => labelOf(b) === words.t('mach.rebind')).length, 0, 'a retired machine\'s token is only revoked');
});

test('machines: a machine that connects has its node tokens read again', async () => {
  const r = await team();
  const http = fakeHTTP();
  await mount(app(r, {http}).vnode());
  await until(() => http.calls.length > 0, 'the node tokens');
  await r.srv.play('team-connect');
  await until(() => http.calls.filter(c => c[0] === 'GET /api/machines').length === 2, 'read again');
  eq(r.errors, [], 'errors');
});

test('machines: j moves the pick and Enter opens the runs page on its runs', async () => {
  const r = await team();
  const storage = memory({[RUNS_FILTER_KEY]: JSON.stringify({state: 'failed', machine: ''})});
  const a = app(r, {storage});
  const root = await mount(a.vnode());
  await act(() => { a.keys.handle(press('j')); });
  eq(root.find('.mach-card').filter(c => c.classList.contains('sel')).map(c => c.one('b').textContent), ['mba'], 'the next machine');
  await act(() => { a.keys.handle(press('Enter')); });
  eq(JSON.parse(storage.getItem(RUNS_FILTER_KEY)), {state: 'failed', machine: 'mba'}, 'the filter');
  eq(a.router.route.value.page, 'runs', 'the page');
});

test('machines: a phone lists them and shows one\'s facts, the controls left to a computer', async () => {
  const r = await team();
  const http = fakeHTTP();
  const root = await mount(app(r, {http}).vnode(), 'phone');
  try {
    ok(root.textContent.includes(words.t('mach.desktop')), 'where they are managed');
    await click(root.find('.card-row').find(b => b.textContent.includes('win')));
    const page = root.find('.drawer')[0] || root.find('.page-over')[0];
    ok(page && page.textContent.includes(words.f('mach.note.missing', 'files')), 'its facts');
    eq(page.find('button').filter(b => [words.t('mach.share'), words.t('mach.rebind'), words.t('mach.revoke'), words.t('mach.add')].includes(labelOf(b))).length, 0, 'no controls');
    eq(http.calls, [], 'no token read');
  } finally { form.value = 'desktop'; }
});

await run();
