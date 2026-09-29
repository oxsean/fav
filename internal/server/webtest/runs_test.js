// runs_test draws the runs page and a run's own page in both forms and both languages from the home frames, and drives
// them in a fake document: the filters kept in this browser, the picked run's preview, Enter opening a run and x asking
// before it stops; a phone's run with the previous and next at hand; and the changes tab of a task and of a run, their
// files opening on their first hunk, filtered, and a running run's list read again on asking.
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
import {html, KeysContext} from '../web/ui/base.js';
import {App} from '../web/pages/app.js';
import {RunPage, RUNS_FILTER_KEY, listed, machineCount} from '../web/pages/runs.js';
import {PANE_KEY} from '../web/pages/tasks.js';
import {install} from './dom.js';
import {settle} from './fake.js';
import {NOW, home, outputs} from './rig.js';
import {test, eq, ok, run} from './check.js';

const css = ['base.css', 'components.css', 'pages.css'].map(f => readFileSync(new URL(`../web/css/${f}`, import.meta.url), 'utf8')).join('\n');
const cssClasses = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map(m => m[1]));
const classesOf = s => new Set([...s.matchAll(/class="([^"]*)"/g)].flatMap(m => m[1].split(/\s+/).filter(Boolean)));
const wordsLeft = s => s.match(/\b(runs|chg|det|home|app|status|ui|out|cmp|conv|confirm)\.[a-zA-Z]+\b/g);
const press = (key, more = {}) => ({key, target: globalThis.document?.body, preventDefault() {}, ...more});

function memory(init = {}) {
  const m = new Map(Object.entries(init));
  return {getItem: k => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: k => m.delete(k), map: m};
}

function fakeHistory(url) {
  const loc = {pathname: '/', search: '', hash: ''};
  const set = u => { const x = new URL(u, 'http://tend.test'); loc.pathname = x.pathname; loc.search = x.search; loc.hash = x.hash; };
  set(url);
  return {location: loc, history: {pushState: (_, __, u) => set(u), replaceState: (_, __, u) => set(u)}};
}

// changesOf is a changes reader over fixed lists: lists[run] is what list gives (an Error with a code rejects), every
// file's first hunk a two-line diff; asked records the calls.
function changesOf(lists) {
  const asked = [];
  return {asked, can: () => true,
    list: (run, o) => { asked.push(['list', run, !!o?.ended]); const x = lists[run]; return x instanceof Error ? Promise.reject(x) : Promise.resolve(x); },
    diff: (run, path, snap) => { asked.push(['diff', run, path, snap]); return Promise.resolve({hunks: [{at: '@@ -1,2 +1,2 @@', lines: ['-old ' + path, '+new ' + path]}], of: 2}); }};
}

function app(r, {url = '/?page=runs', storage = memory(), changes} = {}) {
  const keys = createKeys({timers: r.clk});
  const toasts = createToasts({timers: r.clk});
  let n = 0;
  const commands = createCommands({wire: r.wire, newID: () => 'c' + ++n});
  const {location, history} = fakeHistory(url);
  const router = createRouter({location, history});
  const noStore = {getItem: () => null, setItem() {}};
  const props = {store: r.store, commands, toasts, wire: r.wire, router, keys, nav: createNav({storage: noStore, width: 1440}),
    prefs: createPrefs({storage: noStore, asked: 'zh'}), session: {id: 'u_b', name: 'Bo'}, clock: () => NOW, fetchOutput: () => Promise.resolve({events: []}),
    storage, onLogout() {}, changes};
  const vnode = () => html`<${KeysContext.Provider} value=${keys}><${App} ...${props} /><//>`;
  return {...props, vnode};
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
// painted waits out the effects preact defers to after a paint when a render happened outside act.
const painted = () => act(() => new Promise(res => setTimeout(res, 120))).then(settled);
const click = el => act(() => el.dispatch('click'));
const key = (keys, k, more) => act(() => { keys.handle(press(k, more)); });
const buttonOf = (root, text) => {
  const got = root.find('button').filter(b => b.textContent.trim() === text || b.textContent.trim().startsWith(text));
  if (!got.length) throw new Error(`no button ${text}: ${root.find('button').map(b => b.textContent.trim()).join(' | ')}`);
  return got[0];
};

test('which runs a filter keeps, the latest first, and how many machines are up', async () => {
  const r = await home();
  const st = r.store.state;
  eq(listed(st, {state: 'all'}).length, 14, 'all');
  eq(listed(st, {state: 'open'}).map(x => x.id), ['r6', 'r3', 'r2', 'r1'], 'not ended');
  eq(listed(st, {state: 'failed'}).map(x => x.id).sort(), ['r13', 'r5'], 'failed');
  eq(listed(st, {state: 'all', machine: 'win'}).map(x => x.id), ['r8'], 'on win');
  eq(machineCount(r.store.machines.value), {up: 2, down: 1});
});

test('the runs page and a run\'s own draw in both forms and both languages, styled and worded', async () => {
  const r = await home();
  const sizes = {};
  for (const url of ['/?page=runs', '/?page=runs&run=r5', '/?page=runs&run=r9']) for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
    const s = drawn(app(r, {url}).vnode(), f, lang);
    styled(s, `${url} ${f}/${lang}`);
    sizes[`${url} ${f}/${lang}`] = s.length;
  }
  const list = drawn(app(r).vnode(), 'desktop', 'zh');
  for (const want of ['class="runs-preview"', '未结束', '失败', '全部机器', '打开运行', '在任务里打开', 'Agent @ 机器']) ok(list.includes(want), `list: no ${want}`);
  const phone = drawn(app(r).vnode(), 'phone', 'zh');
  ok(phone.includes('2 台在线 · 1 台离线') && phone.includes('class="cards"'), 'a phone: the machines line and cards');
  const one = drawn(app(r, {url: '/?page=runs&run=r5'}).vnode(), 'desktop', 'en');
  for (const want of ['class="det-bar"', 'Output', 'Changes', 'Facts', 'Open in its task']) ok(one.includes(want), `a run: no ${want}`);
  ok(drawn(app(r, {url: '/?page=runs&run=r9'}).vnode(), 'desktop', 'zh').includes('没有这个运行，或者看不到它'), 'no such run');
  const keys = createKeys(), none = () => {};
  const a = app(r);
  for (const tab of ['changes', 'facts']) for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
    const s = drawn(html`<${KeysContext.Provider} value=${keys}><${RunPage} store=${r.store} commands=${a.commands} toasts=${a.toasts} prefs=${a.prefs}
      changes=${changesOf({r5: {files: [], total: {files: 0, add: 0, del: 0}, snapshot: '', git: true, hidden: 0, at: NOW}})} run=${r.store.state.runs.r5}
      now=${NOW} tab=${tab} onTab=${none} onTask=${none} onStop=${none} /><//>`, f, lang);
    styled(s, `${tab} ${f}/${lang}`);
    if (tab === 'facts' && f === 'desktop' && lang === 'zh') for (const want of ['在哪跑', '目录', '/w/t5', '时间']) ok(s.includes(want), `facts: no ${want}`);
  }
  return sizes;
});

test('the runs page by keys and clicks: a filter kept, a run previewed, Enter opens it, x asks before it stops', async () => {
  const r = await home();
  const storage = memory();
  const a = app(r, {storage});
  const root = await mount(a.vnode());
  await settled();
  eq(root.find('.tr').length, 14, 'every run');
  await click(root.find('.chip').find(c => c.textContent.startsWith('失败')));
  eq(root.find('.tr').length, 2, 'the failed');
  eq(JSON.parse(storage.getItem(RUNS_FILTER_KEY)), {state: 'failed', machine: ''}, 'kept in this browser');
  await click(root.find('.chip').find(c => c.textContent.startsWith('全部') && !c.textContent.startsWith('全部机器')));
  await key(a.keys, 'j');
  ok(root.one('.runs-pv-head').textContent.includes(listed(r.store.state, {state: 'all'})[1].id), 'j moves the preview');
  await key(a.keys, 'Enter');
  eq(a.router.route.value.page, 'runs', 'still the runs page');
  const opened = a.router.route.value.run;
  ok(!!opened && root.find('.det-bar').length === 1, 'the run opened on its own page');
  a.router.go({page: 'runs', run: 'r1'});
  await settled();
  await key(a.keys, 'x');
  ok(root.one('.modal').textContent.includes(words.t('home.confirmStop')), 'x asks first');
  await key(a.keys, 'Escape');
  eq(root.find('.modal').length, 0, 'Esc: nothing sent');
  await click(buttonOf(root, words.t('runs.inTask')));
  eq(a.router.route.value, {page: 'tasks', view: 'list', task: 't1', run: 'r1'}, 'its task, at the run');
  eq(r.errors, [], 'errors');
});

test('a phone\'s run takes the screen with the previous and next of the list; its tabs switch', async () => {
  const r = await home();
  const a = app(r, {url: '/?page=runs&run=r2', changes: null});
  const root = await mount(a.vnode(), 'phone');
  await settled();
  ok(root.find('.run-phone').length === 1, 'the run page');
  await click(root.find('button').find(b => b.getAttribute('aria-label') === words.t('ui.next')));
  const ids = listed(r.store.state, {state: 'all'}).map(x => x.id);
  eq(a.router.route.value.run, ids[ids.indexOf('r2') + 1], 'next in the list');
  await click(root.find('button').find(b => b.textContent.trim() === words.t('runs.tab.facts')));
  ok(root.find('.run-facts').length === 1, 'the facts');
  form.value = 'desktop';
  eq(r.errors, [], 'errors');
});

const files = [
  {path: 'go.sum', op: 'modify', add: 14, del: 2, bytes: 9310, generated: true},
  {path: 'internal/receipt/pdf.go', op: 'modify', add: 48, del: 6, bytes: 6120, agent: true},
  {path: 'internal/receipt/pdf_test.go', op: 'add', add: 62, del: 0, bytes: 1840, agent: true},
  {path: 'internal/receipt/testdata/golden.txt', op: 'modify', add: 3200, del: 1100, bytes: 402113, big: true},
];
const list = snapshot => ({files, total: {files: 4, add: 3324, del: 1108}, snapshot, git: true, hidden: 0, at: NOW});

test('a task\'s changes tab: its latest run\'s files, the unfolded ones open on their first hunk, filtered, read again, another run picked', async () => {
  const r = await outputs();
  const ch = changesOf({r2: list('w-1'), r1: list('t-9a1')});
  const a = app(r, {url: '/?page=tasks&task=t1', storage: memory({[PANE_KEY]: 'changes'}), changes: ch});
  const root = await mount(a.vnode());
  await painted();
  eq(ch.asked[0], ['list', 'r2', false], 'the latest run, still running');
  eq(ch.asked.filter(x => x[0] === 'diff').map(x => x[2]).sort(), ['internal/receipt/pdf.go', 'internal/receipt/pdf_test.go'], 'the unfolded open on their own');
  const text = () => root.one('.chg').textContent;
  ok(text().includes('+new internal/receipt/pdf.go') && text().includes('还有 1 处'), 'the first hunk and what is left');
  ok(text().includes('改动很大：+3,200 −1,100'), 'the big one folded');
  const row = path => root.find('.chg-row').find(b => b.textContent.includes(path));
  await click(row('golden.txt'));
  await painted();
  ok(ch.asked.some(x => x[0] === 'diff' && x[2].endsWith('golden.txt')), 'opened on asking');
  await click(root.find('.chip').find(c => c.textContent.startsWith('生成的文件')));
  eq(root.find('.chg-file').length, 1, 'the generated only');
  await click(buttonOf(root, '刷新'));
  await painted();
  eq(ch.asked.filter(x => x[0] === 'list').length, 2, 'read again');
  await click(root.find('button').find(b => b.textContent.trim() === 'r1'));
  await painted();
  eq(ch.asked.filter(x => x[0] === 'list').at(-1), ['list', 'r1', true], 'an ended run, kept');
  eq(r.errors, [], 'errors');
});

run();
