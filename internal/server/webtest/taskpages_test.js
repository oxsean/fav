// taskpages_test draws the task pages (list, board, tree, a task, and each form) in both forms and both languages from
// the tasks frames, and drives them in a fake document against the command frames: a new task created and started, a
// dispatch with its preview, a plan saved and applied, a gate sent back and passed, an issue's version taken, a merge,
// a move undone, and a drop on the board. On a phone a task is its own page with the previous and next ones at hand.
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
import {DRAFT_KEY, TaskForm, Dispatch, Move, PlanReview, Gate} from '../web/pages/taskforms.js';
import {FILTER_KEY, PANE_KEY} from '../web/pages/tasks.js';
import {install} from './dom.js';
import {settle} from './fake.js';
import {NOW, tasks} from './rig.js';
import {test, eq, ok, run} from './check.js';

const css = ['base.css', 'components.css', 'pages.css'].map(f => readFileSync(new URL(`../web/css/${f}`, import.meta.url), 'utf8')).join('\n');
const cssClasses = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map(m => m[1]));
const classesOf = s => new Set([...s.matchAll(/class="([^"]*)"/g)].flatMap(m => m[1].split(/\s+/).filter(Boolean)));
const wordsLeft = s => s.match(/\b(tasks|col|board|do|toast|confirm|det|form|m|a|disp|adv|pv|plan|gate|move|picker|home|app|why|status|ui)\.[a-zA-Z_]+\b/g);
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

// app opens tasks on their overview unless the storage it is given says otherwise.
function app(r, {url = '/?page=tasks', storage = memory()} = {}) {
  if (storage.getItem(PANE_KEY) === null) storage.setItem(PANE_KEY, 'overview');
  const keys = createKeys({timers: r.clk});
  const toasts = createToasts({timers: r.clk});
  let n = 0;
  const commands = createCommands({wire: r.wire, newID: () => 'c' + ++n});
  const {location, history} = fakeHistory(url);
  const router = createRouter({location, history});
  const noStore = {getItem: () => null, setItem() {}};
  const props = {store: r.store, commands, toasts, wire: r.wire, router, keys, nav: createNav({storage: noStore, width: 1440}),
    prefs: createPrefs({storage: noStore, asked: 'zh'}), session: {id: 'u_b', name: 'Bo'}, clock: () => NOW, fetchOutput: () => Promise.resolve({events: []}),
    storage, onLogout() {}};
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
const buttonOf = (root, text) => {
  const got = root.find('button').filter(b => b.textContent.trim() === text || b.textContent.trim().startsWith(text + ' ') || b.textContent.replace(/⌘.*$|Ctrl\+.*$/, '').trim() === text);
  if (!got.length) throw new Error(`no button ${text}: ${root.find('button').map(b => b.textContent.trim()).join(' | ')}`);
  return got[0];
};

test('the task pages draw in both forms and both languages, styled and worded', async () => {
  const r = await tasks();
  const sizes = {};
  for (const url of ['/?page=tasks&task=q4', '/?page=tasks&view=board&task=q6', '/?page=tasks&view=tree&task=q1', '/?page=tasks&task=q10']) {
    const a = app(r, {url});
    for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
      const s = drawn(a.vnode(), f, lang);
      styled(s, `${url} ${f}/${lang}`);
      sizes[`${url} ${f}/${lang}`] = s.length;
    }
  }
  const list = drawn(app(r, {url: '/?page=tasks&task=q4'}).vnode(), 'desktop', 'zh');
  for (const want of ['拆解草稿等你确认', '等验收', '需求有变化', '合进父任务时冲突', '等派发', 'accept 阶段 · 第 1 轮', '人工验收', '等 u_b 验收',
    '结论：pass · Totals round half-even now.', 'The totals round the wrong way.', 'claude（项目默认） @ mba（项目默认）']) ok(list.includes(want), `list: no ${want}`);
  const board = drawn(app(r, {url: '/?page=tasks&view=board'}).vnode(), 'desktop', 'en');
  eq((board.match(/class="board-col/g) || []).length, 5, 'five columns');
  for (const want of ['Waiting on you', 'Not started', '↺1', '$1.10', '1/4']) ok(board.includes(want), `board: no ${want}`);
  const tree = drawn(app(r, {url: '/?page=tasks&view=tree&task=q1'}).vnode(), 'desktop', 'zh');
  for (const want of ['class="tree-row depth-2', 'Rewrite the checkout in <strong>three</strong> steps.', '<a href="https://git.example.com/shop/issues/42" target="_blank"',
    'issue 有了第 3 版：', '1 / 4 完成']) ok(tree.includes(want), `tree: no ${want}`);
  const phone = drawn(app(r, {url: '/?page=tasks'}).vnode(), 'phone', 'zh');
  for (const want of ['class="sect"', 'class="fab"', 'class="tabbar"']) ok(phone.includes(want), `phone: no ${want}`);
  ok(!phone.includes('class="board"') && !phone.includes('>看板<'), 'no board on a phone');
  return sizes;
});

test('each form draws in both forms and both languages, styled and worded', async () => {
  const r = await tasks();
  const st = r.store.state, machines = r.store.machines.value, none = () => {};
  const agents = [{name: 'claude', provider: 'claude'}, {name: 'codex', provider: 'codex', model: 'gpt-6'}];
  const keys = createKeys();
  const forms = {
    new: html`<${TaskForm} store=${r.store} agents=${agents} machines=${machines} storage=${memory()} onSubmit=${none} onClose=${none} />`,
    child: html`<${TaskForm} store=${r.store} agents=${agents} machines=${machines} mode="child" base=${st.tasks.q3} storage=${memory()} onSubmit=${none} onClose=${none} />`,
    edit: html`<${TaskForm} store=${r.store} agents=${agents} machines=${machines} mode="edit" task=${st.tasks.q1} storage=${memory()} onSubmit=${none} onClose=${none} />`,
    dispatch: html`<${Dispatch} store=${r.store} task=${st.tasks.q9} agents=${agents} machines=${machines} onDispatch=${none} onLater=${none} onClose=${none} />`,
    blocked: html`<${Dispatch} store=${r.store} task=${st.tasks.q7} agents=${agents} machines=${machines} onDispatch=${none} onClose=${none} />`,
    move: html`<${Move} store=${r.store} task=${st.tasks.q5} onMove=${none} onClose=${none} />`,
    plan: html`<${PlanReview} store=${r.store} task=${st.tasks.q6} onSave=${none} onApply=${none} onDiscard=${none} onReply=${none} onClose=${none} />`,
    gate: html`<${Gate} task=${st.tasks.q4} onBack=${none} onClose=${none} />`,
    reply: html`<${Gate} task=${st.tasks.q10} reply=${'r22'} onPass=${none} onBack=${none} onClose=${none} />`,
  };
  for (const [name, v] of Object.entries(forms)) for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
    const s = drawn(html`<${KeysContext.Provider} value=${keys}>${v}<//>`, f, lang);
    ok(s.length > 200, `${name} ${f}/${lang}: drawn`);
    styled(s, `${name} ${f}/${lang}`);
  }
});

test('n creates a task from the form: its directory picked, its draft kept on this device until it is sent, then started', async () => {
  const r = await tasks();
  const storage = memory();
  const a = app(r, {storage});
  const root = await mount(a.vnode());
  await r.srv.play('tasks-create', {
    async new() { await act(() => { a.keys.handle(press('n')); }); },
    async fill() {
      await settled();
      ok(root.find('.page-over').length + root.find('.modal').length === 1, 'the form is open');
      eq(root.find('.dir-pick').map(b => b.find('.mono')[0].textContent), ['/w/shop', '/w/search', '/srv/shop', '/w/docs'], 'the directories used lately');
      await type(root.one('.form-main').find('input')[0], 'Refund emails');
      await type(root.one('.form-main').find('textarea')[0], 'Send **one** email per refund.');
      await click(root.find('.dir-pick')[0]);
      eq(JSON.parse(storage.getItem(DRAFT_KEY)).title, 'Refund emails', 'the draft is kept');
      await act(() => { a.keys.handle(press('Enter', {metaKey: true})); });
    },
    async made() {
      await settled();
      eq([root.find('.modal').length, storage.getItem(DRAFT_KEY)], [0, null], 'closed, the draft gone');
      eq(a.router.route.value.task, 'q12', 'the new task is selected');
      ok(root.one('.toast-text').textContent.includes('Refund emails'), 'the toast');
    },
  });
  eq(r.errors, [], 'errors');
});

test('d dispatches the selected task where it says, after the preview', async () => {
  const r = await tasks();
  const a = app(r, {url: '/?page=tasks&task=q9'});
  const root = await mount(a.vnode());
  await r.srv.play('tasks-dispatch', {
    async dispatch() { await act(() => { a.keys.handle(press('d')); }); },
    // ⚠️ Preact runs effects after the next frame; outside act that is a 35 ms timer.
    async loaded() { await act(() => new Promise(res => setTimeout(res, 40))); },
    async queue() {
      await settled();
      const box = root.one('.modal');
      ok(box.textContent.includes('codex 0.50.0') && box.textContent.includes(words.t('pv.background')), 'the preview');
      await act(() => { a.keys.handle(press('Enter', {metaKey: true})); });
    },
    async queued() {
      await settled();
      eq(root.find('.modal').length, 0, 'closed');
      ok(root.one('.toast-text').textContent.includes('Coupon rounding'), 'queued');
    },
  });
  eq(r.errors, [], 'errors');
});

test('a plan: a task taken out, the draft saved, the tasks made; what the coordinator would refuse shows first', async () => {
  const r = await tasks();
  const a = app(r, {url: '/?page=tasks&task=q6'});
  const root = await mount(a.vnode());
  await r.srv.play('tasks-plan', {
    async review() {
      await click(root.one('.det-acts').find('.btn').find(b => b.className.includes('primary')));
      eq(root.find('.plan-row').map(b => b.find('.plan-title')[0].textContent), ['Facet index per category', 'Facets in the search API', 'Document the facet parameters', 'Facet filters in the shop'], 'the draft');
      ok(root.one('.modal').textContent.includes('Should empty facets be hidden?'), 'its question');
      await click(root.find('.plan-row')[1]);
      await type(root.one('.plan-item').find('input')[1], 'Bad Key');
      ok(root.one('.plan-list').textContent.includes(words.t('plan.err.key')), 'a bad key is shown');
      ok(buttonOf(root, words.t('plan.save')).disabled, 'and cannot be saved');
      await type(root.one('.plan-item').find('input')[1], 'api');
      await click(root.find('.plan-row')[2]);
      await click(buttonOf(root, words.t('plan.remove')));
      eq(root.find('.plan-row').length, 3, 'one fewer');
      await click(buttonOf(root, words.t('plan.save')));
    },
    async apply() {
      await settled();
      await click(buttonOf(root, words.t('plan.apply')));
    },
    async applied() {
      await settled();
      eq(root.find('.modal').length, 0, 'closed');
      ok(root.find('.toast-text').at(-1).textContent.includes(words.f('toast.applied', 4)), 'made');
    },
  });
  eq(r.errors, [], 'errors');
});

test('the task page\'s own writes: a gate sent back for rework needs notes, then passes after asking; an issue version, a merge, a move and its undo', async () => {
  const r = await tasks();
  const a = app(r, {url: '/?page=tasks&task=q4'});
  const root = await mount(a.vnode());
  const primary = () => click(root.one('.det-acts').find('.btn').find(b => b.className.includes('primary')));
  const to = async id => { await act(() => a.router.go({page: 'tasks', task: id})); };
  await r.srv.play('tasks-acts', {
    async back() {
      await click(buttonOf(root.one('.det-acts'), words.t('do.more')));
      await click(root.find('.menu-item').find(b => b.textContent === words.t('do.rework')));
      await click(buttonOf(root.one('.modal'), words.t('gate.back')));
      ok(root.one('.modal').textContent.includes(words.t('gate.needNotes')), 'notes first');
      await type(root.one('.modal').find('textarea')[0], 'Round the tax too.');
      await click(buttonOf(root.one('.modal'), words.t('gate.back')));
    },
    async pass() {
      await settled();
      ok(root.one('.det-acts').find('.btn').find(b => b.className.includes('primary')).textContent.includes(words.t('do.pass')), 'passing is its primary action');
      await primary();
      ok(root.one('.modal').textContent.includes(words.f('gate.confirmPass', 'Review step')), 'asks first');
      await click(buttonOf(root.one('.modal'), words.t('gate.pass')));
    },
    async ack() { await settled(); await to('q1'); await primary(); },
    async merge() { await settled(); await to('q10'); await primary(); },
    async move() {
      await settled();
      await to('q5');
      await click(buttonOf(root.one('.det-acts'), words.t('do.more')));
      await click(root.find('.menu-item').find(b => b.textContent === words.t('do.move')));
      await click(root.one('.modal').find('.picker-btn')[0]);
      await click(root.find('.pk').find(b => b.textContent.includes(words.t('form.top'))));
      await act(() => { a.keys.handle(press('Enter', {metaKey: true})); });
    },
    async undo() { await settled(); await act(() => { a.keys.handle(press('z', {metaKey: true})); }); },
    async done() { await settled(); },
  });
  eq(r.errors, [], 'errors');
});

test('the board takes the four drops it allows, undoes one, and says why it refuses another', async () => {
  const r = await tasks();
  const a = app(r, {url: '/?page=tasks&view=board'});
  const root = await mount(a.vnode());
  const card = id => root.find('.board-card').find(b => b.textContent.includes(id + ' ') || b.find('.mono')[0]?.textContent === id);
  const col = n => root.find('.board-col')[n];
  const drag = async (id, n) => {
    await act(() => card(id).dispatch('dragstart', {dataTransfer: {setData() {}}}));
    await act(() => col(n).dispatch('dragover'));
    ok(col(n).className.includes(n === 4 && id === 'q4' ? 'drop-no' : 'drop-ok'), `${id} over column ${n}`);
    await act(() => col(n).dispatch('drop'));
  };
  await r.srv.play('tasks-board', {
    async drop() { await drag('q9', 4); },
    async undo() {
      await settled();
      ok(root.one('.toast-text').textContent.includes('Coupon rounding'), 'the toast');
      await click(buttonOf(root.one('.toasts'), words.t('ui.undo')));
    },
    async refused() {
      await settled();
      await drag('q4', 4);
      ok(root.find('.toast-text').some(x => x.textContent === words.t('board.no.flow')), 'why not');
    },
  });
  eq(r.errors, [], 'errors');
});

test('offline, every action of a task greys, in both forms, and its keys do nothing', async () => {
  const r = await tasks();
  const shown = async f => {
    const a = app(r, {url: '/?page=tasks&task=q9'});
    const root = await mount(a.vnode(), f);
    await settled();
    return {a, buttons: root.one('.det-acts').find('button'), modals: () => root.find('.modal').length};
  };
  try {
    for (const f of ['desktop', 'phone']) ok((await shown(f)).buttons.every(b => !b.disabled), `${f}: online, nothing greyed`);
    form.value = 'desktop';
    await r.srv.play('tasks-offline', {async offline() { await settled(); }});
    eq(r.wire.status.value, 'offline', 'the wire is offline');
    for (const f of ['desktop', 'phone']) {
      const {a, buttons, modals} = await shown(f);
      ok(buttons.length > 1 && buttons.every(b => b.disabled), `${f}: every button greys`);
      await act(() => { a.keys.handle(press('d')); });
      eq(modals(), 0, `${f}: d opens no dispatch`);
    }
  } finally { form.value = 'desktop'; }
});

test('on a phone a task is its own page, with the previous and next of the list; sending back asks with its button first', async () => {
  const r = await tasks();
  const storage = memory({[FILTER_KEY]: JSON.stringify({column: 'waiting'})});
  const a = app(r, {url: '/?page=tasks&task=q4', storage});
  const root = await mount(a.vnode(), 'phone');
  try {
    ok(root.one('.page-over').textContent.includes('Review step'), 'q4 takes the screen');
    eq(root.find('.sect-h').map(h => h.textContent.split(' ')[0]), [words.t('col.waiting')], 'the kept filter: what waits');
    await click(root.one('.det-nav').find('button')[1]);
    eq(a.router.route.value.task, 'q1', 'next');
    await click(root.one('.det-nav').find('button')[0]);
    await click(root.one('.det-nav').find('button')[0]);
    eq(a.router.route.value.task, 'q6', 'previous, twice');
    ok(root.one('.det-nav').find('button')[0].disabled, 'the first has no previous');
    await act(() => a.router.go({page: 'tasks', task: 'q4'}));
    await click(buttonOf(root.one('.det-acts'), words.t('do.more')));
    await click(root.find('.menu-item').find(b => b.textContent === words.t('do.rework')));
    const sheet = root.one('.sheet');
    ok(sheet.children.findIndex(c => c.className === 'sheet-body') >= 0 && sheet.one('.gate-quick').find('button').length === 1, 'its button on top');
    ok(sheet.find('kbd').length === 0, 'no key caps on a phone');
  } finally { form.value = 'desktop'; }
});

await run();
