// ui_test draws the shared components in both forms and both languages, and drives them in a fake document: the keys
// of a list, sorting and the virtual window, a modal's keys and focus, toasts' undo, the sidebar and the tab bar.
import {readFileSync} from 'node:fs';
import {h, render} from '../web/vendor/preact.mjs';
import {useState} from '../web/vendor/hooks.mjs';
import {signal} from '../web/vendor/signals-core.mjs';
import renderToString from './vendor/render-to-string.mjs';
import {act} from './vendor/test-utils.mjs';
import {createKeys} from '../web/core/keys.js';
import {form, mac, createNav, formOf, PHONE_BELOW, NAV_OPEN_FROM} from '../web/core/layout.js';
import {createToasts, UNDO_WAIT} from '../web/core/toasts.js';
import {words} from '../web/core/i18n.js';
import {html, KeysContext} from '../web/ui/base.js';
import {Button, Chip, Chips, Segmented, Tabs, Kbd} from '../web/ui/controls.js';
import {Status, statuses} from '../web/ui/status.js';
import {Panel, Stat} from '../web/ui/panel.js';
import {Modal, Drawer} from '../web/ui/overlay.js';
import {Toasts, floorOf} from '../web/ui/toast.js';
import {Table, windowOf, VIRTUAL_ABOVE} from '../web/ui/table.js';
import {ExpandItem} from '../web/ui/expand.js';
import {Shell, KeyBar, barGroups, navPages, phoneTabs, tabOf} from '../web/ui/shell.js';
import {iconNames} from '../web/ui/icons.js';
import {Markdown, safeHref} from '../web/ui/markdown.js';
import {Picker} from '../web/ui/picker.js';
import {TreeList} from '../web/ui/tree.js';
import {Board} from '../web/ui/board.js';
import {install} from './dom.js';
import {clock} from './fake.js';
import {test, eq, ok, run} from './check.js';

const press = (key, more = {}) => ({key, target: globalThis.document?.body, preventDefault() {}, ...more});
const fakeWire = (status = 'open') => ({status: signal(status), reconnects: 0, reconnect() { this.reconnects++; }});
const noStore = {getItem: () => null, setItem() {}};

const rows = n => Array.from({length: n}, (_, i) => ({id: 'r' + (i + 1), name: 'task ' + String(i + 1).padStart(4, '0'), state: i % 3 ? 'running' : 'failed', age: n - i}));
const columns = [
  {id: 'state', label: 'S', width: '14px', mobile: 'lead', render: r => html`<${Status} state=${r.state} />`},
  {id: 'name', label: 'Name', mobile: 'primary', sort: (a, b) => a.name.localeCompare(b.name)},
  {id: 'age', label: 'Age', width: '56px', align: 'right', mobile: 'trailing', sort: (a, b) => a.age - b.age},
];

const pickOptions = [{value: 'mba', label: 'mba', state: 'online', sub: 'darwin'}, {value: 'win', label: 'win', state: 'offline', note: 'offline', tone: 'muted'},
  {value: 'linux', label: 'linux', words: ['container'], disabled: false}];
const treeRows = [{id: 'a', depth: 0, kids: true, open: true, title: 'Parent', sub: 'p1', trail: '1/2'},
  {id: 'b', depth: 1, title: 'Child'}, {id: 'c', depth: 4, kids: true, open: false, title: 'Deep'}];

function gallery() {
  const keys = createKeys({timers: clock()});
  const toasts = createToasts({timers: clock()});
  toasts.show({text: 'Stopped run r1', undo: () => {}});
  const nav = createNav({storage: noStore, width: 1440});
  return html`<${Shell} keys=${keys} wire=${fakeWire('offline')} nav=${nav} toasts=${toasts} page="home" onNavigate=${() => {}}
    counts=${{running: 3, queued: 2, offline: ['win'], waiting: 4}} navCounts=${{tasks: 12, runs: 5}} user=${{name: 'me'}}
    server=${{name: 'lab'}} onServers=${() => {}} onSearch=${() => {}} onNew=${() => {}} onReload=${() => {}}>
    <${Panel} title="Waiting" count=${4} actions=${html`<${Button} kind="quiet" keyName="g i">All<//>`}>
      <${ExpandItem} id="a" state="asked" title="Which branch?" sub="task 12 · claude" age="12m" agePct=${40}
        actions=${[{label: 'Answer', kind: 'primary', keyName: 'a', onClick() {}}, {label: 'Open', onClick() {}}]} />
      <${ExpandItem} id="b" open selected state="permission" title="Run the migration?" age="1h" agePct=${120}>body<//>
    <//>
    <${Stat} label="Running" value="3" tone="running" note="of 8" onClick=${() => {}} />
    <${Stat} label="Failed" value="1" tone="failed" />
    <${Chips} label="Filter"><${Chip} label="all" on count=${5} onClick=${() => {}} /><${Chip} label="mine" onClick=${() => {}} /><${Chip} label="tag" /><//>
    <${Segmented} label="View" value="list" options=${[{value: 'list', label: 'List'}, {value: 'board', label: 'Board'}]} onChange=${() => {}} />
    <${Tabs} label="Panes" value="out" tabs=${[{id: 'out', label: 'Output', count: 3}, {id: 'brief', label: 'Brief'}]} onChange=${() => {}} />
    ${Object.keys(statuses).map(s => html`<${Status} state=${s} word />`)}
    <${Button} kind="danger" icon="stop" keyName="Shift+X">Stop<//>
    <${Button} icon="close" label="Close" />
    <${Button} kind="primary" on keyName="Mod+Enter" disabled>Send<//>
    <${Table} label="Runs" columns=${columns} rows=${rows(5)} selected="r2" />
    <${Table} label="Empty" columns=${columns} rows=${[]} />
    <${Modal} title="New task" onClose=${() => {}} actions=${[{label: 'Cancel', onClick() {}}, {label: 'Start', kind: 'primary', keyName: 'Mod+Enter', onClick() {}}]}>form<//>
    <${Modal} title="Long form" full onClose=${() => {}}>form<//>
    <${Drawer} title="Run r1" onClose=${() => {}} actions=${[{label: 'Stop', kind: 'danger', onClick() {}}]}>detail<//>
    <${Markdown} text=${'### Plan\n\n1. **cart**\n2. `pay`\n\n> quoted\n\n```\ngo test\n```\n\nSee https://example.com and [docs](/docs).'} />
    <${Markdown} text="" empty="No brief" />
    <${Picker} label="Machine" value="mba" onChange=${() => {}} options=${pickOptions} note="where it runs" />
    <${Picker} label="After" multi value=${[]} onChange=${() => {}} options=${pickOptions} error="a cycle" />
    <${TreeList} label="Tree" selected="b" onFold=${() => {}} rows=${treeRows} />
    <${Board} label="Board" selected="x" columns=${[{id: 'a', label: 'A', cards: [{id: 'x', content: 'card x'}]}, {id: 'b', label: 'B', cards: []}]} />
  <//>`;
}

const css = ['base.css', 'components.css'].map(f => readFileSync(new URL(`../web/css/${f}`, import.meta.url), 'utf8')).join('\n');
const cssClasses = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map(m => m[1]));
const classesOf = s => new Set([...s.matchAll(/class="([^"]*)"/g)].flatMap(m => m[1].split(/\s+/).filter(Boolean)));

function drawn(f, lang) {
  form.value = f;
  words.lang.value = lang;
  try { return renderToString(gallery()); } finally { form.value = 'desktop'; words.lang.value = 'zh'; }
}

test('the gallery draws in both forms and both languages with every class styled and every word found', () => {
  const seen = {};
  for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
    const s = drawn(f, lang);
    const missing = [...classesOf(s)].filter(c => !cssClasses.has(c));
    eq(missing, [], `${f}/${lang}: classes without a rule`);
    const keysLeft = s.match(/\b(nav|shell|ui|status|banner|keys)\.[a-zA-Z]+/g);
    eq(keysLeft, null, `${f}/${lang}: words not found`);
    seen[f + '/' + lang] = s;
  }
  ok(seen['desktop/zh'].includes('收起菜单') && seen['desktop/en'].includes('Collapse the menu'), 'the language changes the words');
  return Object.fromEntries(Object.entries(seen).map(([k, v]) => [k, v.length]));
});

test('the desktop form has the frame, dialogs and key caps', () => {
  const s = drawn('desktop', 'zh');
  for (const want of ['data-form="desktop"', 'class="topbar"', 'class="nav nav-open"', 'class="keybar"', 'role="grid"', 'class="modal"',
    'class="drawer"', '<kbd>', 'class="banner"', 'aria-current="page"', 'class="age-bar"']) ok(s.includes(want), `no ${want}`);
  for (const not of ['class="tabbar"', 'class="sheet"', 'class="cards"', 'class="page-over"']) ok(!s.includes(not), `has ${not}`);
  eq((s.match(/class="nav-item/g) || []).length, navPages.length, 'nav items');
});

test('the phone form has the tab bar, sheets, cards and no key caps', () => {
  const s = drawn('phone', 'zh');
  for (const want of ['data-form="phone"', 'class="tabbar"', 'class="sheet"', 'class="grab"', 'class="cards"', 'class="page-over"',
    'class="srv"', 'class="xi-toggle"', 'class="btn quick"']) ok(s.includes(want), `no ${want}`);
  for (const not of ['<kbd>', 'class="keybar"', 'class="nav ', 'class="modal"', 'class="drawer"', 'role="grid"', 'class="age-bar"']) ok(!s.includes(not), `has ${not}`);
  eq((s.match(/class="tab-item/g) || []).length, phoneTabs.length, 'tabs');
});

test('the words every table-built label needs are there in both languages', () => {
  const need = [...navPages.map(p => 'nav.' + p), ...phoneTabs.map(x => x.label), ...Object.keys(statuses).map(s => 'status.' + s)];
  eq(need.filter(k => !words.has(k)), [], 'missing');
  const src = ['base', 'charts', 'controls', 'expand', 'input', 'menu', 'overlay', 'palette', 'panel', 'shell', 'status', 'table', 'toast'].map(f => readFileSync(new URL(`../web/ui/${f}.js`, import.meta.url), 'utf8')).join('\n');
  const used = [...src.matchAll(/\b(?:t|f)\('([a-z]+\.[\w.]+)'|label: '([a-z]+\.[\w.]+)'/g)].map(m => m[1] || m[2]);
  ok(used.length > 15, `${used.length} words used`);
  eq([...new Set(used)].filter(k => !words.has(k)), [], 'unregistered');
  eq(iconNames.filter(n => !/^[a-z]+$/.test(n)), [], 'icon names');
});

test('the breakpoints are core/layout constants', () => {
  eq([formOf(PHONE_BELOW - 1), formOf(PHONE_BELOW), PHONE_BELOW, NAV_OPEN_FROM], ['phone', 'desktop', 720, 1200], 'forms');
});

test('Kbd spells Mod by the platform', () => {
  mac.value = true;
  const onMac = renderToString(html`<${Kbd} k="Mod+K" /> <${Kbd} k="g h" />`);
  mac.value = false;
  const elsewhere = renderToString(html`<${Kbd} k="Mod+K" />`);
  eq([onMac, elsewhere], ['<kbd>⌘K</kbd> <kbd>g</kbd> <kbd>h</kbd>', '<kbd>Ctrl+K</kbd>'], 'caps');
});

// mount draws vnode into a fresh document inside the keys' context, flushing effects.
async function mount(vnode, keys = createKeys({timers: clock()})) {
  const root = install();
  await act(() => render(h(KeysContext.Provider, {value: keys}, vnode), root));
  return {root, keys, unmount: () => act(() => render(null, root))};
}
const key = (keys, k, more) => act(() => { keys.handle(press(k, more)); });

function ListRig({n, log}) {
  const [sel, setSel] = useState('r1');
  log.sel = sel;
  return html`<${Table} label="Runs" columns=${columns} rows=${rows(n)} selected=${sel} onSelect=${setSel}
    onOpen=${id => log.push('open ' + id)} onToggle=${id => log.push('toggle ' + id)} onPick=${(d, id) => log.push(`pick ${d} ${id}`)} />`;
}

test('a table moves its selection by key and keeps it by id through a sort', async () => {
  const log = [];
  const {root, keys} = await mount(html`<${ListRig} n=${5} log=${log} />`);
  await key(keys, 'j'); await key(keys, 'j'); await key(keys, 'ArrowDown'); await key(keys, 'k');
  eq(log.sel, 'r3', 'after j j ↓ k');
  await key(keys, 'Enter'); await key(keys, ' '); await key(keys, '4');
  eq(log.splice(0), ['open r3', 'toggle r3', 'pick 4 r3'], 'actions');
  await act(() => root.find('.th-sort')[0].dispatch('click'));
  await act(() => root.find('.th-sort')[0].dispatch('click'));
  const names = () => root.find('.tr').map(r => r.children[1].textContent);
  eq(names()[0], 'task 0005', 'sorted by name, descending');
  eq(root.one('.sel').children[1].textContent, 'task 0003', 'the selected row is still r3');
  eq(root.find('[aria-sort=descending]').length, 1, 'the header says so');
  await key(keys, 'j');
  eq(log.sel, 'r2', 'j moves in the sorted order');
  const bar = barGroups(keys.active());
  eq(bar.map(g => [g.label, g.keys.join(' '), g.to || '']), [['act.move', 'j k', ''], ['act.open', 'Enter', ''], ['act.toggle', 'Space', ''], ['act.pick', '1', '9']], 'key bar groups');
});

test(`a table over ${VIRTUAL_ABOVE} rows draws only its window and scrolls to the selection`, async () => {
  const log = [];
  const {root, keys} = await mount(html`<${ListRig} n=${1000} log=${log} />`);
  const w = windowOf({count: 1000, rowHeight: 32, top: 0, height: 480});
  eq(root.find('.tr').length, w.end - w.start, 'rows drawn at the top');
  eq(root.find('.spacer').map(s => s.style.height), [w.after + 'px'], 'the space below');
  for (let i = 0; i < 40; i++) await key(keys, 'j');
  eq(log.sel, 'r41', 'selected');
  const body = root.one('.tbody');
  eq(body.scrollTop, 41 * 32 - 480, 'scrolled so the selection shows');
  ok(root.find('.tr').some(r => r.getAttribute('aria-selected') === 'true'), 'the selected row is drawn');
  ok(root.find('.tr').length < 50, `${root.find('.tr').length} rows drawn`);
  await act(() => { body.scrollTop = 16000; body.dispatch('scroll'); });
  eq(root.find('.tr')[0].getAttribute('aria-rowindex'), String(500 - 8 + 2), 'the window follows the scroll');
});

test('a table becomes cards when the page turns into a phone', async () => {
  const {root} = await mount(html`<${ListRig} n=${3} log=${[]} />`);
  ok(root.find('[role=grid]').length === 1, 'grid on a desktop');
  await act(() => { form.value = 'phone'; });
  eq([root.find('[role=grid]').length, root.find('.card-row').length], [0, 3], 'cards on a phone');
  eq(root.find('.card-trailing').map(c => c.textContent), ['3', '2', '1'], 'trailing column');
  await act(() => { form.value = 'desktop'; });
});

function ModalRig({log, open}) {
  return html`<button type="button" class="opener">open</button>${open.value && html`<${Modal} title="New" onClose=${() => log.push('close')}
    actions=${[{label: 'Cancel', onClick: () => log.push('cancel')}, {label: 'Go', kind: 'primary', onClick: () => log.push('go')}]}><input class="field" /><//>`}`;
}

test('a modal takes the keys, keeps focus inside and gives it back', async () => {
  const log = [];
  const keys = createKeys({timers: clock()});
  keys.push('page', [{key: 'n', run: () => log.push('page n')}]);
  const open = signal(false);
  const rig = () => html`<${ModalRig} log=${log} open=${open} />`;
  const {root} = await mount(rig(), keys);
  const opener = root.one('.opener');
  opener.focus();
  open.value = true;
  await act(() => render(h(KeysContext.Provider, {value: keys}, rig()), root));
  const modal = root.one('.modal');
  ok(modal.contains(document.activeElement), 'focus moved into the modal');
  await key(keys, 'n');
  eq(log, [], 'the page behind gets no key');
  await key(keys, 'Enter', {metaKey: true});
  await key(keys, 'Escape');
  eq(log.splice(0), ['go', 'close'], 'Mod+Enter runs the primary action, Esc closes');
  const list = modal.all(e => ['BUTTON', 'INPUT'].includes(e.tagName));
  list[list.length - 1].focus();
  const e = modal.dispatch('keydown', {key: 'Tab'});
  ok(e.defaultPrevented && document.activeElement === list[0], 'Tab wraps to the first control');
  await act(() => root.one('.overlay').dispatch('click'));
  eq(log.splice(0), ['close'], 'the backdrop closes');
  open.value = false;
  await act(() => render(h(KeysContext.Provider, {value: keys}, rig()), root));
  eq(document.activeElement, opener, 'focus is back on the opener');
  await key(keys, 'n');
  eq(log, ['page n'], 'the page has its keys again');
});

test('a toast keeps above the controls under it: a phone page\'s foot and composer, a desktop composer at the right', () => {
  const box = (top, height) => ({getBoundingClientRect: () => ({top, height})});
  const doc = (...overs) => ({defaultView: {innerHeight: 844}, querySelectorAll: () => overs.map(parts => ({querySelectorAll: () => parts}))});
  eq(floorOf(doc()), 0, 'no full-screen page: the tab bar rule');
  eq(floorOf(doc([])), 0, 'a page with nothing at its bottom: the bottom of the screen');
  eq(floorOf(doc([box(641, 135), box(776, 68)])), 203, 'above the composer, the highest of its bottom controls');
  eq(floorOf(doc([box(700, 144)], [box(780, 64), box(0, 0)])), 64, 'the topmost page counts, a hidden part does not');
  const desk = (...parts) => ({defaultView: {innerHeight: 800, innerWidth: 1440}, querySelectorAll: () => parts});
  const at = (top, right) => ({getBoundingClientRect: () => ({top, height: 60, right})});
  eq(floorOf(desk(at(685, 1416)), false), 115, 'a desktop toast keeps above a composer under it');
  eq(floorOf(desk(at(685, 640)), false), 0, 'not one left of it');
});

test('a toast with undo waits six seconds; Mod+Z takes it back', async () => {
  const clk = clock();
  const toasts = createToasts({timers: clk});
  const {root, keys} = await mount(html`<${Toasts} toasts=${toasts} />`);
  const undone = [];
  await act(() => { toasts.show({text: 'plain'}); toasts.show({text: 'stopped', undo: () => undone.push('stopped')}); });
  eq(root.find('.toast').length, 2, 'both shown');
  await act(() => clk.advance(4000));
  eq(root.find('.toast-text').map(x => x.textContent), ['stopped'], 'the plain one went');
  await act(() => clk.advance(UNDO_WAIT - 4000 - 1));
  eq(root.find('.toast').length, 1, 'undo still there');
  await key(keys, 'z', {metaKey: true});
  eq([undone, root.find('.toast').length], [['stopped'], 0], 'Mod+Z undid it');
  await key(keys, 'z', {metaKey: true});
  eq(undone.length, 1, 'nothing left to undo');
  await act(() => toasts.show({text: 'again', undo: () => undone.push('again')}));
  await act(() => root.one('.toast').one('button').dispatch('click'));
  eq(undone, ['stopped', 'again'], 'its button undoes too');
  await act(() => toasts.show({text: 'late', undo: () => undone.push('late')}));
  await act(() => clk.advance(UNDO_WAIT));
  eq([root.find('.toast').length, undone.length], [0, 2], 'gone after six seconds, not undone');
});

test('the sidebar starts by width, is kept once chosen, and [ toggles it', async () => {
  const saved = new Map();
  const storage = {getItem: k => saved.get(k) ?? null, setItem: (k, v) => saved.set(k, v)};
  eq([createNav({storage, width: NAV_OPEN_FROM - 1}).open.value, createNav({storage, width: NAV_OPEN_FROM}).open.value], [false, true], 'by width');
  const broken = {getItem() { throw new Error('denied'); }, setItem() { throw new Error('denied'); }};
  const b = createNav({storage: broken, width: 1300});
  b.toggle();
  eq(b.open.value, false, 'works without storage');
  const nav = createNav({storage, width: 1440});
  const keys = createKeys({timers: clock()});
  const {root} = await mount(html`<${Shell} keys=${keys} wire=${fakeWire()} nav=${nav} toasts=${createToasts({timers: clock()})} page="tasks" onNavigate=${() => {}}
    counts=${{waiting: 2}}>page<//>`, keys);
  ok(root.find('.nav-open').length === 1 && root.find('.nav-label').length === navPages.length, 'open with labels');
  await key(keys, '[');
  eq([root.find('.nav-closed').length, root.find('.nav-label').length, saved.get('tend-nav')], [1, 0, 'closed'], 'collapsed and kept');
  eq(root.find('.badge').map(x => x.textContent), ['2'], 'the waiting badge while collapsed');
  eq(root.one('.nav-foot').find('button').map(x => [x.textContent.trim(), x.getAttribute('aria-label')]), [['', words.t('nav.expand')]], 'the collapsed buttons are icons with names');
  ok(root.one('.keybar').textContent.includes(words.t('nav.expand')), 'the key bar names what [ does now');
  eq(createNav({storage, width: 1440}).open.value, false, 'the choice outlives the width');
  await act(() => root.find('.nav-foot')[0].find('button').at(-1).dispatch('click'));
  eq(nav.open.value, true, 'its button opens it again');
});

test('the phone shell has four tabs, the page under its tab, and no [ key', async () => {
  form.value = 'phone';
  try {
    const went = [];
    const nav = createNav({storage: noStore, width: 390});
    const keys = createKeys({timers: clock()});
    const wire = fakeWire('offline');
    const {root} = await mount(html`<${Shell} keys=${keys} wire=${wire} nav=${nav} toasts=${createToasts({timers: clock()})} page="machines"
      onNavigate=${p => went.push(p)} counts=${{waiting: 3, running: 1}}>page<//>`, keys);
    eq(root.find('.tab-item').map(a => a.getAttribute('aria-current') || ''), ['', '', 'page', ''], 'machines sit under runs');
    eq([tabOf('agents'), tabOf('team'), tabOf('home')], ['me', 'me', 'home'], 'tab of a page');
    eq(root.find('.badge').map(x => x.textContent), ['3'], 'the waiting badge');
    const e = root.find('.tab-item')[1].dispatch('click');
    eq([went, e.defaultPrevented], [['tasks'], true], 'a tab navigates in the page');
    eq(root.find('.nav').length + root.find('.keybar').length, 0, 'no sidebar or key bar');
    eq(keys.handle(press('[')), false, '[ is not bound');
    await act(() => root.one('.banner').one('button').dispatch('click'));
    eq(wire.reconnects, 1, 'the banner reconnects');
    await act(() => { wire.status.value = 'open'; });
    eq(root.find('.banner').length, 0, 'the banner goes once connected');
  } finally { form.value = 'desktop'; }
});

test('tabs and segments move by arrows', async () => {
  const got = [];
  const tabs = [{id: 'a', label: 'A'}, {id: 'b', label: 'B'}, {id: 'c', label: 'C'}];
  const {root} = await mount(html`<${Tabs} label="T" tabs=${tabs} value="a" onChange=${v => got.push(v)} />
    <${Segmented} label="S" options=${[{value: 'x', label: 'X'}, {value: 'y', label: 'Y'}]} value="y" onChange=${v => got.push(v)} />`);
  const list = root.one('[role=tablist]');
  for (const k of ['ArrowRight', 'ArrowLeft', 'End', 'Home', 'q']) list.dispatch('keydown', {key: k});
  root.one('[role=radiogroup]').dispatch('keydown', {key: 'ArrowRight'});
  eq(got, ['b', 'c', 'c', 'a', 'x'], 'picked');
  eq(root.find('[role=tab]').map(b => b.getAttribute('tabindex')), ['0', '-1', '-1'], 'only the picked tab is in the tab order');
});

test('markdown draws a brief\'s blocks as elements, and a link only to http(s) or this site', async () => {
  const s = renderToString(html`<${Markdown} text=${'# Top\n\n- one\n- **two**\n\n[x](javascript:alert(1)) [y](//evil.test/a) [z](/docs?a=1) <b>raw</b>'} />`);
  ok(s.includes('<h3 class="md-h">Top</h3>') && s.includes('<li><strong>two</strong></li>'), 'heading and list');
  ok(s.includes('<a href="/docs?a=1"') && !s.includes('href="javascript') && !s.includes('href="//evil'), 'only safe links');
  ok(s.includes('[x](javascript:alert(1))') && s.includes('&lt;b>raw&lt;/b>'), 'the rest as text');
  eq(['https://a.test/x', 'http://a.test', '/p', '?q', '#f', '//a.test', 'javascript:x', 'data:text/html,x', ' /p'].map(u => !!safeHref(u)),
    [true, true, true, true, true, false, false, false, true], 'safeHref');
});

test('a picker opens under its button on a desktop and over the page on a phone; typing finds, arrows and Enter pick', async () => {
  for (const f of ['desktop', 'phone']) {
    form.value = f;
    try {
      const got = [];
      const {root} = await mount(html`<${Picker} label="Machine" value="mba" onChange=${v => got.push(v)} options=${pickOptions} />`);
      eq(root.one('.picker-btn').one('.ell').textContent, 'mba', `${f}: the chosen one`);
      await act(() => root.one('.picker-btn').dispatch('click'));
      eq([root.find('.picker-pop').length, root.find('.page-over').length], f === 'phone' ? [0, 1] : [1, 0], `${f}: where it opens`);
      const q = root.one('.picker-q');
      await act(() => { q.value = 'contai'; q.dispatch('input'); });
      eq(root.find('.pk-label').map(x => x.textContent), ['linux'], `${f}: found by its words`);
      await act(() => { q.value = ''; q.dispatch('input'); });
      await act(() => q.dispatch('keydown', {key: 'ArrowDown', preventDefault() {}}));
      await act(() => q.dispatch('keydown', {key: 'Enter', preventDefault() {}}));
      eq([got, root.find('.picker-q').length], [['win'], 0], `${f}: picked and closed`);
    } finally { form.value = 'desktop'; }
  }
  const got = [];
  const {root} = await mount(html`<${Picker} label="After" multi value=${['mba']} onChange=${v => got.push(v)} options=${pickOptions} />`);
  await act(() => root.one('.picker-btn').dispatch('click'));
  await act(() => root.find('.pk')[2].dispatch('click'));
  eq([got, root.find('.picker-q').length], [[['mba', 'linux']], 1], 'several stay open');
});

test('a tree folds by Space and by its button, and caps the indent', async () => {
  const folded = [], picked = [];
  const {root, keys} = await mount(html`<${TreeList} label="Tree" rows=${treeRows} selected="a" onSelect=${id => picked.push(id)} onFold=${id => folded.push(id)} />`);
  eq(root.find('.tree-row').map(r => r.className.split(' ')[1]), ['depth-0', 'depth-1', 'depth-2'], 'indent');
  eq(root.find('[role=treeitem]').map(r => r.getAttribute('aria-expanded')), ['true', null, 'false'], 'expanded');
  await key(keys, ' ');
  await act(() => root.find('.tree-fold')[2].dispatch('click'));
  await key(keys, 'j');
  eq([folded, picked], [['a', 'c'], ['b']], 'folded and moved');
});

test('a board drops a card only where it may, and says why elsewhere', async () => {
  const dropped = [], refused = [];
  const cols = [{id: 'a', label: 'A', cards: [{id: 'x', content: 'card x'}]}, {id: 'b', label: 'B', cards: []}, {id: 'c', label: 'C', cards: []}];
  const {root} = await mount(html`<${Board} label="Board" columns=${cols} canDrop=${(_, c) => c === 'b'} onDrop=${(id, c) => dropped.push(id + c)} onRefused=${(id, c) => refused.push(id + c)} />`);
  await act(() => root.one('.board-card').dispatch('dragstart', {dataTransfer: {setData() {}}}));
  eq(root.find('.board-col').map(c => c.className), ['board-col drop-no', 'board-col drop-ok', 'board-col drop-no'], 'where it may go');
  const over = root.find('.board-col')[1].dispatch('dragover');
  ok(over.defaultPrevented, 'dragover allows the drop');
  ok(!root.find('.board-col')[2].dispatch('dragover').defaultPrevented, 'not there');
  await act(() => root.find('.board-col')[1].dispatch('drop'));
  await act(() => root.one('.board-card').dispatch('dragstart', {dataTransfer: {setData() {}}}));
  await act(() => root.find('.board-col')[2].dispatch('drop'));
  eq([dropped, refused, root.find('.board-col').map(c => c.className)], [['xb'], ['xc'], ['board-col', 'board-col', 'board-col']], 'dropped, refused, cleared');
});

run();
