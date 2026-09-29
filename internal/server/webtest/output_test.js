// output_test checks a conversation's timeline: the steps, turns and rows core/output.js lays out at each density,
// find and the errors; core/follow.js's cases (following, "N new", holding what is above, going back); a store paging
// a run's output back and taking affordances; the deep link; the timeline, the answer form and the composer drawn in
// both forms and both languages; and the task page's conversation and the home's waiting item played against the
// output frames. Given a file of events by name, it also lays them out for the Go test that compares output.Items.
process.env.TZ = 'UTC';
import {readFileSync} from 'node:fs';
import {render} from '../web/vendor/preact.mjs';
import renderToString from './vendor/render-to-string.mjs';
import {act} from './vendor/test-utils.mjs';
import {createKeys} from '../web/core/keys.js';
import {createRouter, parse, format, link} from '../web/core/router.js';
import {form, createNav} from '../web/core/layout.js';
import {createToasts} from '../web/core/toasts.js';
import {createCommands} from '../web/core/commands.js';
import {createPrefs} from '../web/core/prefs.js';
import {words} from '../web/core/i18n.js';
import * as out from '../web/core/output.js';
import {density as dt} from '../web/core/proto.js';
import * as fl from '../web/core/follow.js';
import {html, KeysContext, NamesContext} from '../web/ui/base.js';
import {signal} from '../web/vendor/signals-core.mjs';
import {Output} from '../web/ui/output.js';
import {Composer, modesOf} from '../web/ui/composer.js';
import {AnswerForm, answersOf, quickOf, allowsRun} from '../web/ui/answer.js';
import {App} from '../web/pages/app.js';
import {PANE_KEY} from '../web/pages/tasks.js';
import {install, layout} from './dom.js';
import {settle, readFrames, clock} from './fake.js';
import {NOW, outputs} from './rig.js';
import {test, eq, ok, run} from './check.js';

const css = ['base.css', 'components.css', 'pages.css'].map(f => readFileSync(new URL(`../web/css/${f}`, import.meta.url), 'utf8')).join('\n');
const cssClasses = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map(m => m[1]));
const classesOf = s => new Set([...s.matchAll(/class="([^"]*)"/g)].flatMap(m => m[1].split(/\s+/).filter(Boolean)));
const wordsLeft = s => s.match(/\b(out|cmp|ans|conv|det|tasks|home|app|ui)\.[a-zA-Z_]+\b/g);
const press = (key, more = {}) => ({key, target: globalThis.document?.body, preventDefault() {}, ...more});

function styled(s, what) {
  eq([...classesOf(s)].filter(c => !cssClasses.has(c)), [], `${what}: classes without a rule`);
  eq(wordsLeft(s), null, `${what}: words not found`);
}

function drawn(vnode, f, lang) {
  form.value = f;
  words.lang.value = lang;
  try { return renderToString(vnode); } finally { form.value = 'desktop'; words.lang.value = 'zh'; }
}

// the output frames' data as parts: the first run's last page, then the second's page before its watch and what the
// watch pushed
const state = readFrames('output-state').find(l => l.s?.params?.part === 'runs').s.params.items;
const conv = readFrames('output-conv');
const pushed = conv.filter(l => l.s?.method === 'run.output').flatMap(l => l.s.params.events);
const [r2Early, r1Page] = conv.filter(l => l.s?.type === 'res').map(l => l.s.result.events);
const both = () => [{run: state.r1, events: r1Page, head: {start: true}}, {run: state.r2, events: [...r2Early, ...pushed], head: null}];
// p4: a command the run asks leave for, which it may be allowed for the rest of the run
const asked = readFrames('output-send').find(l => l.s?.method === 'journal').s.params.events[0].data.requests[0];
const shape = rows => rows.map(r => (r.type === 'step' ? r.step.kind + (r.open ? '+' : '') : r.type));

// Given events by name (the Go test's file), lay each out as output.Items does.
if (process.argv[2]) {
  test('items as output.Items lays them', () => {
    const byName = JSON.parse(readFileSync(process.argv[2], 'utf8'));
    return Object.fromEntries(Object.entries(byName).map(([k, evs]) => [k, out.items(evs)]));
  });
}

test('a conversation at each density: talk open, steps one line, failures and questions open, earlier turns folded', () => {
  const m = out.model(both());
  eq(shape(out.lay(m, {density: 'standard'})),
    ['head', 'turn', 'run', 'you', 'think', 'say', 'group', 'edit', 'shell+', 'plan', 'mark', 'you', 'ask+', 'ask+', 'temp', 'send'], 'standard');
  eq(shape(out.lay(m, {density: 'brief'})), ['head', 'turn', 'run', 'you', 'say', 'summary', 'shell+', 'you', 'ask+', 'ask+', 'temp', 'send'], 'brief');
  eq(shape(out.lay(m, {density: 'detailed'})),
    ['head', 'turn', 'run', 'you', 'think+', 'say', 'group+', 'edit+', 'shell+', 'plan+', 'mark', 'you', 'ask+', 'ask+', 'temp', 'send'], 'detailed');

  const rows = out.lay(m);
  const turn = rows.find(r => r.type === 'turn');
  eq([turn.key, turn.n, turn.steps, turn.shell, turn.dur, turn.last], ['r1:t1', 1, 1, 1, 680000, 'Which PDF library should I use?'], 'the folded turn');
  const group = rows.find(r => r.type === 'step' && r.step.kind === 'group').step;
  eq([group.reads, group.searches, group.members.length], [2, 1, 3], 'three reads and searches in a row are one step');
  const shell = rows.find(r => r.type === 'step' && r.step.kind === 'shell');
  eq([shell.auto, shell.step.failed, shell.step.lines], [true, true, 4], 'a failed command opens by itself');
  const [p3, q7] = rows.filter(r => r.type === 'step' && r.step.kind === 'ask').map(r => r.step);
  eq([p3.pending, p3.resolved?.decision, q7.pending, q7.questions], [false, 'allow', true, ['Which page size?', 'Which parts go on the cover?']], 'asks');
  eq(rows.filter(r => r.type === 'send').map(r => [r.id, r.state]), [['s_3', 'failed']], 'a message the output shows is not pending');
  eq(m.plan.plan.map(p => [p.text, p.done, p.now]), [['a test for the size', true, false], ['compress the fonts', false, true], ['a cover page', false, false]], 'the plan');
  const summary = out.lay(m, {density: 'brief'}).find(r => r.type === 'summary');
  eq([summary.reads, summary.searches, summary.files, summary.shell], [2, 1, 1, 0], 'a brief turn sums up the rest');

  const first = out.lay(out.model([{run: state.r1, events: r1Page}]));
  eq([first[0].type, first[0].brief, first[0].step.brief], ['step', true, true], 'the first run\'s first message is the brief, cut');
  ok(!out.lay(out.model([{run: state.r1, events: r1Page}]), {density: 'detailed'})[0].brief, 'shown whole when detailed');

  const opened = out.lay(m, {open: new Map([['r1:t1', true], [group.key, true]])});
  eq(shape(opened).slice(0, 8), ['head', 'turn', 'you', 'say', 'shell', 'say', 'result', 'run'], 'an earlier turn opened');
  ok(opened.find(r => r.key === group.key).open && opened.find(r => r.key === group.key).manual, 'a step the viewer opened');

  eq(shape(out.lay(m, {filter: 'shell'})), ['head', 'shell', 'run', 'shell+'], 'only commands, across the turns');
  eq(shape(out.lay(m, {filter: 'error'})), ['head', 'run', 'shell+'], 'only failures');
  eq(out.counted(rows).length, 11, 'what counts as a step: not headings, temp events or pending messages');
  eq(out.waits(rows), [shell.key, q7.key], 'what wants the viewer');
});

test('find: in folded turns and grouped steps, by filter, ASCII case folded; the next error', () => {
  const m = out.model(both());
  const hits = out.find(m, 'GOFPDF');
  eq(hits.map(h => out.textOf(m.runs[1].turns[0].steps.find(s => s.key === h.key)).split('\n')[0]),
    ['Use gofpdf and keep it under 1 MiB.', 'Using gofpdf. First the test for the size.', 'internal/receipt/pdf.go'], 'hits in order, a grouped member\'s title among them');
  eq(out.find(m, 'gofpdf', {filter: 'talk'}).length, 2, 'within a filter');
  const early = out.find(m, 'pdf library');
  eq(early.map(h => [h.run, h.turn]), [['r1', 'r1:t1'], ['r1', 'r1:t1']], 'in a folded turn');
  const rows = out.lay(m, {peek: out.peekOf(early[0])});
  ok(rows.find(r => r.key === 'r1:t1').open && rows.some(r => r.key === early[0].key), 'a hit opens its turn while shown');
  eq(out.find(m, '  '), [], 'nothing for blanks');
  eq(out.find(m, 'ＧＯＦＰＤＦ'), [], 'no folding past ASCII');
  const errs = out.errors(m);
  eq(errs.length, 1, 'one failure');
  eq(out.nextOf(errs, errs[0].key), errs[0], 'wraps round');
  eq(out.nextOf(hits, hits[0].key, -1), hits[2], 'back from the first is the last');
  eq(out.ends('1\n2\n3\n4\n5', 2), {head: ['1', '2'], cut: 1, tail: ['4', '5']}, 'head and tail');
  eq(out.editLines({input: {old_string: 'a', new_string: 'b\nc'}, diff: ''}), ['-a', '+b', '+c'], 'a claude edit as diff lines');
});

test('follow: sticks until the viewer moves up, then counts what is new and what waits, and follows again near the bottom', () => {
  let s = fl.initial();
  s = fl.step(s, {type: 'rows', keys: ['a', 'b'], waits: [], ended: false});
  eq(fl.badge(s), null, 'following: no badge');
  s = fl.step(s, {type: 'rows', keys: ['a', 'b', 'c'], waits: [], ended: false});
  eq(fl.badge(s), null, 'growth while following');
  s = fl.step(s, {type: 'up'});
  eq(fl.badge(s), {kind: 'latest', n: 0}, 'away with nothing new: back to the latest');
  s = fl.step(s, {type: 'rows', keys: ['a', 'b', 'c', 'd', 'e'], waits: [], ended: false});
  eq(fl.badge(s), {kind: 'new', n: 2, waiting: 0, first: ''}, 'two new');
  eq(s.divider, 'd', 'the new line is before the first new step');
  s = fl.step(s, {type: 'rows', keys: ['a', 'b', 'c', 'd', 'e', 'f'], waits: ['f'], ended: false});
  eq(fl.badge(s), {kind: 'waiting', n: 3, waiting: 1, first: 'f'}, 'one of them waits');
  eq(s.divider, 'd', 'the new line stays');
  eq(fl.step(s, {type: 'settled', fromBottom: 49}).mode, 'away', 'further than 48px stays');
  s = fl.step(s, {type: 'passed'});
  eq(s.divider, '', 'scrolled past the new line');
  const back = fl.step(s, {type: 'settled', fromBottom: 48});
  eq([back.mode, fl.badge(back)], ['follow', null], 'within 48px follows again');
  s = fl.step(fl.step(back, {type: 'up'}), {type: 'rows', keys: back.keys, waits: [], ended: true});
  eq(fl.badge(s).kind, 'ended', 'the run ended while away');
  let t = fl.step(fl.initial(), {type: 'rows', keys: ['a'], waits: [], ended: true});
  t = fl.step(fl.step(t, {type: 'up'}), {type: 'rows', keys: ['a'], waits: [], ended: true});
  eq(fl.badge(t).kind, 'latest', 'ended before leaving: not news');
  eq(fl.step(s, {type: 'bottom'}).mode, 'follow', 'the button goes back');
  eq(fl.initial(true).mode, 'away', 'a place left away opens away');

  const m = out.model(both());
  const rows = out.lay(m);
  const withTemp = out.model([both()[0], {...both()[1], events: [...both()[1].events, {off: 900, kind: 'say', temp: true, key: 'x:900', text: 'more'}]}]);
  eq(out.counted(out.lay(withTemp)), out.counted(rows), 'a temp event is not new');
});

test('follow: scrolling to the bottom, the anchor, what is held above, placeholders, the place', () => {
  const rowsOf = (from, n) => Array.from({length: n}, (_, i) => ({key: 'k' + (from + i)}));
  const firsts = ps => ps.map(p => [p[0].key, p.length]);
  const cut = fl.pages(rowsOf(0, 450));
  eq(firsts(cut), [['k0', 200], ['k200', 200], ['k400', 50]], 'pages of PAGE rows');
  const starts = new Set(cut.map(p => p[0].key));
  eq(firsts(fl.pages([...rowsOf(-30, 30), ...rowsOf(0, 470)], starts)), [['k-30', 30], ['k0', 200], ['k200', 200], ['k400', 70]],
    'rows put in front and behind leave the pages as they were');
  eq(firsts(fl.pages(rowsOf(0, 610), starts)), [['k0', 200], ['k200', 200], ['k400', 200], ['k600', 10]], 'the last page fills, then a new one');
  eq(fl.stick({grew: 0, screen: 800}), 'none', 'no growth');
  eq(fl.stick({grew: 300, screen: 800}), 'smooth', 'under a screen');
  eq(fl.stick({grew: 900, screen: 800}), 'jump', 'over a screen');
  eq(fl.stick({grew: 30, screen: 800, touching: true}), 'jump', 'under a finger');
  const tops = [{key: 'a', top: 0, height: 100}, {key: 'b', top: 100, height: 50}, {key: 'c', top: 150, height: 80}];
  const a = fl.anchorOf(tops, 120);
  eq(a, {key: 'b', offset: -20}, 'the row the viewport cuts');
  eq(fl.shift(a, 400, 120), 300, 'a row above grew by 300: scroll by as much');
  eq(fl.shift(a, 100, 120), 0, 'nothing moved');
  const shown = [{key: 'x', v: 1}, {key: 'b'}, {key: 'c'}];
  const latest = [{key: 'x', v: 2}, {key: 'y'}, {key: 'b'}, {key: 'c'}, {key: 'd'}];
  eq(fl.hold(shown, latest, 'b'), [{key: 'x', v: 1}, {key: 'b'}, {key: 'c'}, {key: 'd'}], 'above the anchor as drawn, from it the latest');
  ok(fl.held(shown, latest, 'b') && !fl.held(latest, latest, 'b'), 'held tells a difference');
  eq(fl.hold(shown, latest, 'gone'), latest, 'an anchor lost holds nothing');
  const pages = Array.from({length: 10}, (_, i) => ({top: i * 5000, height: 5000, keep: i === 1}));
  eq([...fl.placeholders({rows: 2000, pages, view: {top: 45000, bottom: 45800}, screen: 800})], [0, 2, 3, 4, 5, 6, 7], 'far pages; not one that keeps, not the last');
  eq(fl.placeholders({rows: 1500, pages, view: {top: 0, bottom: 800}, screen: 800}).size, 0, 'few rows: none');
  ok(fl.nearTop(1100, 800) && !fl.nearTop(1300, 800), 'the page before is fetched 1.5 screens from the top');
  const m = new Map();
  const storage = {getItem: k => m.get(k) ?? null, setItem: (k, v) => m.set(k, v)};
  fl.keepPlace(storage, 'r1', {follow: false, anchor: 'k', offset: 12});
  eq(fl.readPlace(storage, 'r1'), {follow: false, anchor: 'k', offset: 12}, 'kept by the conversation\'s first run');
  m.set('tend-out-r9', '{bad');
  eq(fl.readPlace(storage, 'r9'), null, 'a broken place is none');
  eq(fl.readPlace({getItem() { throw new Error('denied'); }}, 'r1'), null, 'storage that throws');
});

test('the deep link to a step: parsed, written back without its step', () => {
  const at = link('t1', 'r2', 'a2:3:650:0');
  eq(at, '#task-t1/r-r2/e-a2%3A3%3A650%3A0', 'the link');
  eq(parse('', at), {page: 'tasks', view: 'list', task: 't1', run: 'r2', event: 'a2:3:650:0'}, 'parsed');
  eq(parse('', '#task-t1'), {page: 'tasks', view: 'list', task: 't1'}, 'a task alone');
  eq(format(parse('', at)), '?page=tasks&task=t1&run=r2', 'the step is one-time');
  eq(parse('?page=tasks&run=r2', '').run, undefined, 'a run needs its task');
});

test('the timeline, the answer form and the composer draw in both forms and both languages, styled and worded', () => {
  const req = state.r2.requests[0];
  const vnode = html`<${Output} parts=${both()} density="standard" onDensity=${() => {}} onRaw=${() => {}} linkOf=${() => ''}
    renderAsk=${() => html`<${AnswerForm} req=${req} onAnswer=${() => {}} />`}>
    <${Composer} route=${{to: 'run', run: 'r2'}} acts=${['steer', 'interrupt']} machine="mba" sends=${state.r2.sends} onSend=${() => {}} onResend=${() => {}} /><//>`;
  const sizes = {};
  for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
    for (const density of dt.names) {
      const s = drawn(html`<${Output} parts=${both()} density=${density} />`, f, lang);
      styled(s, `${density} ${f}/${lang}`);
    }
    const s = drawn(vnode, f, lang);
    styled(s, `output ${f}/${lang}`);
    sizes[`${f}/${lang}`] = s.length;
  }
  const zh = drawn(vnode, 'desktop', 'zh');
  for (const want of ['第 1 轮 · 工作了', '第 2 次运行 · 续接 · mba', '读了 2 个文件 · 搜了 1 次', 'pdf_test.go:31: 1.4 MiB, want at most 1 MiB', '计划 1/3',
    '── check 开始 ──', 'u_b 允许了', 'Which parts go on the cover?', '可多选', '还有 2 个问题没答', 'Waiting for the page size', '没送到', '插进正在跑的这一轮'])
    ok(zh.includes(want), `zh: no ${want}`);
  const en = drawn(vnode, 'phone', 'en');
  for (const want of ['Turn 1', 'Run 2 · continued · mba', 'read 2 files · searched 1 times', 'Plan 1/3', 'Not delivered', 'Follow', 'Pause'].slice(0, 5))
    ok(en.includes(want), `en: no ${want}`);
  ok(en.includes('class="out out-phone"') && en.includes('composer-phone'), 'the phone form');
  const forRun = asked, perm = {...asked, allow_run: false};
  for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) styled(drawn(html`<${AnswerForm} req=${forRun} scope onAnswer=${() => {}} />`, f, lang), `permission ${f}/${lang}`);
  ok(drawn(html`<${AnswerForm} req=${forRun} scope onAnswer=${() => {}} />`, 'desktop', 'zh').includes('这次运行里这条命令都允许'), 'allow for the run when offered');
  ok(!drawn(html`<${AnswerForm} req=${perm} onAnswer=${() => {}} />`, 'desktop', 'zh').includes('这次运行里这条命令都允许'), 'not otherwise');
  return sizes;
});

test('who decided: a person by name, a question answered even though its agent was told allow', () => {
  const ev = (id, more) => ({id, off: 0, turn: 1, ...more});
  const events = [ev('e1', {kind: 'tool', tool: 'AskUserQuestion', family: 'ask', request: 'q1', title: 'Which database?'}),
    ev('e2', {kind: 'resolved', request: 'q1', by: 'u_b', decision: 'allow'}),
    ev('e3', {kind: 'tool', tool: 'Bash', family: 'ask', request: 'p1', title: 'rm -rf ./out'}),
    ev('e4', {kind: 'resolved', request: 'p1', by: 'local', decision: 'allow'})];
  const names = signal({u_b: 'Bob'});
  const s = drawn(html`<${NamesContext.Provider} value=${names}><${Output} parts=${[{run: state.r1, events, head: {start: true}}]} density="standard" /><//>`, 'desktop', 'en');
  ok(s.includes('Bob answered'), 'the question, by name');
  ok(s.includes('Server admin allowed'), 'the permission, by the server admin');
  ok(!s.includes('u_b'), 'no id');
});

test('answers and routes: own words over picks, one press for one question, the modes a route offers', () => {
  const qs = state.r2.requests[0].questions;
  eq(answersOf(qs, {'Which page size?': ['A4'], 'Which parts go on the cover?': ['Logo', 'QR code']}, {'Which page size?': ' '}),
    {'Which page size?': 'A4', 'Which parts go on the cover?': 'Logo, QR code'}, 'picks');
  eq(answersOf(qs, {'Which page size?': ['A4']}, {'Which page size?': 'A5'}), {'Which page size?': 'A5'}, 'own words win');
  const t = words.t;
  eq(quickOf(t, asked, {scope: allowsRun(asked, ['answer', 'allow_run'])}).map(q => q.params), [{allow: true}, {allow: true, decision: 'allow_run'}, {allow: false}], 'a permission');
  eq([allowsRun(asked, ['answer']), allowsRun({...asked, allow_run: false}, ['allow_run'])], [false, false], 'not unless both the request and the run allow it');
  eq(quickOf(t, asked).map(q => q.params), [{allow: true}, {allow: false}], 'one the run cannot take for good');
  eq(quickOf(t, state.r2.requests[0]), [], 'two questions: no one press');
  eq(quickOf(t, {kind: 'question', questions: [{question: 'Q?', options: ['a', 'b']}]}).map(q => q.params.answers), [{'Q?': 'a'}, {'Q?': 'b'}], 'one question');
  eq(modesOf({to: 'run'}, ['steer', 'interrupt']), ['steer', 'after', 'interrupt'], 'a run that takes steering');
  eq(modesOf({to: 'run'}, []), ['after'], 'one that does not');
  eq(modesOf({to: 'reply'}, ['steer']), [], 'a reply has no modes');
});

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

function app(r, {url = '/', storage = memory()} = {}) {
  const keys = createKeys({timers: r.clk});
  const toasts = createToasts({timers: r.clk});
  let n = 0;
  const commands = createCommands({wire: r.wire, newID: () => 'c' + ++n});
  const {location, history} = fakeHistory(url);
  const router = createRouter({location, history});
  const noStore = {getItem: () => null, setItem() {}};
  const copied = [];
  const props = {store: r.store, commands, toasts, wire: r.wire, router, keys, nav: createNav({storage: noStore, width: 1440}),
    prefs: createPrefs({storage: noStore, asked: 'zh'}), session: {id: 'u_b', name: 'Bo'}, clock: () => NOW, fetchOutput: () => Promise.resolve({events: []}),
    storage, copy: text => copied.push(text), onLogout() {}};
  return {...props, copied, vnode: () => html`<${KeysContext.Provider} value=${keys}><${App} ...${props} /><//>`};
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
  const got = root.find('button').filter(b => b.textContent.trim() === text || b.textContent.replace(/⌘.*$|Ctrl\+.*$/, '').trim() === text);
  if (!got.length) throw new Error(`no button ${text}: ${root.find('button').map(b => b.textContent.trim()).join(' | ')}`);
  return got[0];
};
const rowKinds = root => root.find('[data-key]').map(r => r.getAttribute('data-key').replace(/^a\d:\d:/, '').replace(/:0$/, ''));

test('the timeline follows the bottom, leaves it when the viewer scrolls up, keeps its place as steps open above, and comes back', async () => {
  const clk = clock();
  const live = pushed.filter(e => e.off < 740), rest = pushed.filter(e => e.off >= 740);
  const parts = events => [{run: {...state.r2, sends: []}, events: [...r2Early, ...events], head: {start: true}}];
  const tall = el => 40 + (el.find('.out-body').length + el.find('.out-members').length + el.find('.out-plan').length > 0 ? 160 : 0);
  const undo = layout('out-scroll', tall);
  try {
    const root = await mount(html`<div />`);
    const draw = (events, density = 'standard') => act(() => render(html`<${Output} parts=${parts(events)} density=${density} timers=${clk} />`, root));
    await draw(live);
    const sc = root.one('.out-scroll');
    sc.clientHeight = 200;
    await draw(live);
    const bottom = () => sc.scrollHeight - sc.clientHeight;
    eq(sc.scrollTop, bottom(), 'follows: at the bottom');
    await draw([...live, rest[0]]);
    eq([sc.scrollTop, root.find('.out-new-btn').length], [bottom(), 0], 'growth while following stays at the bottom, no badge');
    sc.dispatch('scroll');
    await act(() => clk.advance(fl.SETTLE_MS));
    eq(root.find('.out-new-btn').length, 0, 'its own scroll does not leave the bottom');

    sc.dispatch('wheel', {deltaY: -120});
    sc.scrollTop = 150;
    sc.dispatch('scroll');
    await draw([...live, rest[0]]);
    eq(root.one('.out-new-btn').textContent, words.t('out.latest'), 'up: away, nothing new yet');
    const anchor = root.find('[data-key]').find(r => r.offsetTop + r.offsetHeight > sc.scrollTop);
    const offset = anchor.offsetTop - sc.scrollTop;
    await draw([...live, ...rest.slice(0, 2)]);
    eq(sc.scrollTop, 150, 'a step below: the view keeps still');
    eq(root.one('.out-new-btn').textContent, words.f('out.new', 1), 'one new');
    ok(root.find('.out-newline').length === 1, 'the new line');
    await draw([...live, ...rest.slice(0, 2)], 'detailed');
    const above = () => root.find('[data-key]').filter(r => r.offsetTop < same().offsetTop && tall(r) > 40).length;
    const same = () => root.find('[data-key]').find(r => r.getAttribute('data-key') === anchor.getAttribute('data-key'));
    const opened = above();
    eq(sc.scrollTop, 150, 'while scrolling, what is above is held as it was drawn');
    await act(() => clk.advance(fl.SETTLE_MS));
    ok(above() > opened && sc.scrollTop > 150, `settled: steps above open (${opened} → ${above()}), and the view moved with them`);
    eq(same().offsetTop - sc.scrollTop, offset, 'the row in view stays where it was');
    ok(root.find('.out-new-btn').length === 1, 'settled far from the bottom: still away');
    await draw([...live, ...rest]);
    eq(root.find('.out-new-btn').map(b => b.textContent), [words.f('out.newWaiting', 3, 1), words.t('out.toWaiting')], 'what waits, and the way to it');
    sc.scrollTop = bottom() - fl.NEAR;
    sc.dispatch('scroll');
    await act(() => clk.advance(fl.SETTLE_MS));
    eq(root.find('.out-new-btn').length, 0, 'within 48px of the bottom: follows again');
    sc.dispatch('wheel', {deltaY: -120});
    sc.scrollTop = 0;
    await draw([...live, ...rest]);
    await click(root.find('.out-new-btn')[0]);
    eq([sc.scrollTop, root.find('.out-new-btn').length], [bottom(), 0], 'the button goes back to the bottom');
  } finally { undo(); }
});

test('the task page\'s conversation: watched, paged back into the run before, written to, then carried on', async () => {
  const r = await outputs();
  const a = app(r, {url: '/?page=tasks&task=t1'});
  let root, form;
  const kept = what => {
    ok(root.one('.answer') === form, `${what} prepended: the form is the one written in`);
    eq([form.find('button').find(b => b.textContent === 'A4').getAttribute('aria-checked'), form.find('fieldset')[0].one('input').value],
      ['true', 'A5, lands'], `${what} prepended: the pick and the half-written answer stay`);
  };
  await r.srv.play('output-conv', {
    async open() { root = await mount(a.vnode()); },
    async watched() {
      await settled(); r.flush(); await settled();
      ok(root.find('.det-out').length === 1, 'the output tab opens first when the task has runs');
      eq(rowKinds(root), ['head:r2', '400', '500', '600', '650', '720', 'send:s_3'], 'the watched page, and the message not delivered');
      ok(root.one('.out-fail').textContent.includes('1.4 MiB'), 'the failed command is open');
    },
    async more() {
      await settled(); r.flush(); await settled();
      eq(rowKinds(root).slice(-6), ['740', '760', '780', '820', 'temp:a2:3:880', 'send:s_3'], 'the rest pushed: a hook, a message, the asks, a temp event');
      eq([root.find('.out-ask').map(x => x.className), root.find('.answer').length], [['out-ask', 'out-ask pending'], 1], 'the question waits with its form');
      form = root.one('.answer');
      await click(form.find('button').find(b => b.textContent === 'A4'));
      await type(form.find('fieldset')[0].one('input'), 'A5, lands');
      await click(buttonOf(root, words.t('out.more')));
    },
    async earlier() {
      await settled();
      eq(rowKinds(root).slice(0, 3), ['head:r2', '0', '200'], 'the page before, and more before it: the run before');
      kept('the page before');
      await click(buttonOf(root, words.t('out.more')));
    },
    async done() {
      await settled(); r.flush(); await settled();
      eq(rowKinds(root).slice(0, 4), ['head:r1', 'r1:t1', 'run:r2', '0'], 'the run before, its turn folded');
      kept('the run before');
      await click(form.find('button').find(b => b.textContent === 'A4'));
      await type(form.find('fieldset')[0].one('input'), '');
      await click(root.find('[data-key]').find(x => x.getAttribute('data-key') === 'r1:t1').one('button'));
      eq(rowKinds(root).slice(0, 4), ['head:r1', 'r1:t1', '0', '300'], 'opened');
    },
  });
  const composer = () => root.one('.cmp-in');
  await r.srv.play('output-send', {
    async steer() {
      await type(composer(), 'Use A4.');
      ok(root.one('.cmp-where').textContent.includes(words.t('cmp.to.steer')), 'where it goes, before it is sent');
      await click(buttonOf(root, words.t('cmp.send')));
    },
    async changed() {
      await settled();
      eq(composer().value, '', 'sent: the box is empty');
      await click(root.one('.cmp-modes').find('button').find(b => b.textContent === words.t('cmp.after')));
      await type(composer(), 'And landscape.');
      await click(buttonOf(root, words.t('cmp.send')));
    },
    async answer() {
      await settled();
      ok(root.find('.toast-text').some(x => x.textContent.includes(words.t('conv.routeChanged'))), 'the route changed: said, not sent');
      eq(composer().value, 'And landscape.', 'kept to send again');
      const form = root.one('.answer');
      const [size, cover] = form.find('fieldset');
      await type(size.one('input'), 'A5, landscape');
      ok(form.textContent.includes(words.f('ans.left', 1)), 'one left');
      await click(cover.find('button').find(b => b.textContent === 'Totals'));
      await act(() => form.dispatch('submit'));
    },
    async interrupt() {
      await settled();
      await click(buttonOf(root, words.t('conv.interrupt')));
      ok(root.one('.modal').textContent.includes(words.t('conv.confirmInterrupt')), 'asks first');
      await click(root.one('.modal').find('button').find(b => b.className.includes('danger') || b.className.includes('primary')));
    },
    async allow() {
      await settled(); r.flush(); await settled();
      const form = root.find('.answer').find(x => x.textContent.includes('go test ./internal/receipt/...'));
      ok(form, 'the command asked about has its form');
      await click(form.find('button').find(b => b.textContent.includes(words.t('ans.allowRun'))));
    },
    async done() { await settled(); },
  });
  await click(root.one('[data-key=a2:3:650:0]').one('.out-line'));
  await click(buttonOf(root, words.t('out.copyLink')));
  ok(a.copied.at(-1)?.endsWith(link('t1', 'r2', 'a2:3:650:0')), `the step's link: ${a.copied.at(-1)}`);
  await r.srv.play('output-carry', {
    async after() {
      await settled();
      await click(root.one('.cmp-modes').find('button').find(b => b.textContent === words.t('cmp.after')));
      await type(composer(), 'Then add a footer with the page number.');
      await click(buttonOf(root, words.t('cmp.send')));
    },
    async ended() { await settled(); r.flush(); await settled(); },
    async continued() {
      await settled(); r.flush(); await settled();
      const you = root.one('[data-key=a3:1:0:0]');
      ok(you.textContent.includes(words.f('out.by', 'u_b')) && you.textContent.includes('Then add a footer'), `r3's prompt is u_b's message: ${you.textContent}`);
      ok(!you.textContent.includes(words.t('out.you')), 'not a prompt of nobody');
      eq(root.find('[data-key]').filter(x => x.getAttribute('data-key') === 'send:m_a1').length, 0, 'the message is not left waiting in r2');
      ok(root.one('.cmp-where').textContent.includes(words.t('cmp.to.steer')), 'what is typed next goes into r3, the way picked for r2 forgotten');
    },
  });
  eq(r.errors, [], 'errors');
});

test('a deep link opens the conversation at its step, found in a group, away from the bottom', async () => {
  const r = await outputs();
  const a = app(r, {url: '/?page=tasks&task=t1#task-t1/r-r2/e-a2%3A3%3A520%3A0'});
  let root;
  await r.srv.play('output-conv', {
    async open() { root = await mount(a.vnode()); },
    async watched() { await settled(); r.flush(); await act(() => r.clk.advance(1)); },
    async more() {
      await settled(); r.flush(); await settled();
      eq(root.find('.out-flash').map(x => x.getAttribute('data-key')), ['a2:3:500:0'], 'the group its event is in');
      ok(root.find('.out-new-btn').length === 1, 'away from the bottom: the way back');
      await click(buttonOf(root, words.t('out.more')));
    },
    async earlier() { await settled(); await click(buttonOf(root, words.t('out.more'))); },
    async done() { await settled(); },
  });
  eq(a.router.route.value.event, 'a2:3:520:0', 'the route holds the step');
  eq(r.errors, [], 'errors');
});

test('the home answers a waiting item\'s two questions together', async () => {
  const r = await outputs();
  const a = app(r);
  const root = await mount(a.vnode());
  await act(() => { a.keys.handle(press('j')); });
  await act(() => { a.keys.handle(press(' ')); });
  await settled();
  const form = root.one('.answer');
  eq(form.find('fieldset').length, 2, 'both questions');
  eq(root.find('.choice-n').length, 0, 'no digits for two questions');
  await r.srv.play('output-answer', {
    async answer() {
      await click(form.find('button').find(b => b.textContent === 'A4'));
      await click(form.find('button').find(b => b.textContent === 'Logo'));
      await click(form.find('button').find(b => b.textContent === 'QR code'));
      ok(!form.textContent.includes(words.f('ans.left', 1)), 'nothing left');
      await act(() => form.dispatch('submit'));
    },
    async done() { await settled(); },
  });
  for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) styled(drawn(a.vnode(), f, lang), `home ${f}/${lang}`);
  eq(r.errors, [], 'errors');
});

test('an answer someone gave first: said where it was asked, on the home and in the conversation, with no failure', async () => {
  const saysGone = (root, what) => {
    const form = root.one('.answer-gone');
    ok(form.textContent.includes(words.f('ans.goneBy', 'u_a')), `${what}: who answered first`);
    ok(root.textContent.includes('Which page size?'), `${what}: what was asked stays`);
    eq([root.find('.answer-gone button').length, root.find('.answer-gone input').length], [0, 0], `${what}: no way to answer it`);
    ok(!root.find('.toast-text').some(x => x.textContent.includes('request_gone')), `${what}: no failure`);
  };
  const answerAll = async form => {
    await click(form.find('button').find(b => b.textContent === 'A4'));
    await click(form.find('button').find(b => b.textContent === 'Logo'));
    await click(form.find('button').find(b => b.textContent === 'QR code'));
    await act(() => form.dispatch('submit'));
  };
  {
    const r = await outputs();
    const a = app(r);
    const root = await mount(a.vnode());
    await act(() => { a.keys.handle(press('j')); });
    await act(() => { a.keys.handle(press(' ')); });
    await settled();
    await r.srv.play('output-answer-gone', {
      async answer() { await answerAll(root.one('.answer')); },
      async gone() { await settled(); saysGone(root, 'home'); },
      async done() { await settled(); r.flush(); await settled(); },
    });
    eq(r.errors, [], 'errors on the home');
  }
  {
    const r = await outputs();
    const a = app(r, {url: '/?page=tasks&task=t1'});
    let root;
    await r.srv.play('output-conv', {
      async open() { root = await mount(a.vnode()); },
      async watched() { await settled(); r.flush(); await settled(); },
      async more() { await settled(); r.flush(); await settled(); await click(buttonOf(root, words.t('out.more'))); },
      async earlier() { await settled(); await click(buttonOf(root, words.t('out.more'))); },
      async done() { await settled(); },
    });
    await r.srv.play('output-gone', {
      async answer() { await answerAll(root.one('.answer')); },
      async gone() { await settled(); saysGone(root, 'conversation'); },
      async done() { await settled(); r.flush(); await settled(); },
    });
    eq(r.errors, [], 'errors in the conversation');
  }
});

test('the task page in both forms with its conversation, styled and worded', async () => {
  const r = await outputs();
  for (const url of ['/?page=tasks&task=t1', '/?page=tasks&task=t1&run=r2']) for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en'])
    styled(drawn(app(r, {url}).vnode(), f, lang), `${url} ${f}/${lang}`);
  const over = drawn(app(r, {url: '/?page=tasks&task=t1', storage: memory({[PANE_KEY]: 'overview'})}).vnode(), 'desktop', 'zh');
  ok(over.includes('aria-selected="true"') && over.includes('>概览<') && !over.includes('det-out'), 'the overview when last chosen');
});

run();
