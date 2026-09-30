// changes_test checks the changes tab's core: a run's files read in pages (run.changes) and taken again from the start
// when its workspace moves between them, an ended run's list kept, a file's diff in pages of hunks (run.diff) joined
// with its line numbers, more context, the files in directory order, the filters, what starts folded, side by side,
// picking lines and the quote they make; then the changes page itself in both forms and both languages.
process.env.TZ = 'UTC';
import {readFileSync} from 'node:fs';
import renderToString from './vendor/render-to-string.mjs';
import * as ch from '../web/core/changes.js';
import {form} from '../web/core/layout.js';
import {words} from '../web/core/i18n.js';
import {html} from '../web/ui/base.js';
import {Changes, DiffLines} from '../web/pages/changes.js';
import {Diff} from '../web/ui/output.js';
import {outputs, home} from './rig.js';
import {test, eq, ok, throws, run} from './check.js';

const css = ['base.css', 'components.css', 'pages.css'].map(f => readFileSync(new URL(`../web/css/${f}`, import.meta.url), 'utf8')).join('\n');
const cssClasses = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map(m => m[1]));
const classesOf = s => new Set([...s.matchAll(/class="([^"]*)"/g)].flatMap(m => m[1].split(/\s+/).filter(Boolean)));

function drawn(vnode, f, lang) {
  form.value = f;
  words.lang.value = lang;
  try { return renderToString(vnode); } finally { form.value = 'desktop'; words.lang.value = 'zh'; }
}

const file = (path, more = {}) => ({path, op: 'modify', add: 1, del: 0, bytes: 10, ...more});

test('files go in directory order, the root first, each directory by name', () => {
  const got = ch.ordered([file('web/b.js'), file('go.sum'), file('internal/x/a.go'), file('web/a.js'), file('README.md'), file('internal/a.go')]);
  eq(got.map(g => [g.dir, g.files.map(x => x.path)]), [
    ['', ['README.md', 'go.sum']], ['internal', ['internal/a.go']], ['internal/x', ['internal/x/a.go']], ['web', ['web/a.js', 'web/b.js']]]);
});

test('the filters: all, the agent tools\' own, generated; big, generated and binary files start folded', () => {
  const fs = [file('a', {agent: true}), file('b', {generated: true}), file('c', {binary: true}), file('d', {big: true}), file('e')];
  eq(ch.filtered(fs, 'all').map(x => x.path), ['a', 'b', 'c', 'd', 'e']);
  eq(ch.filtered(fs, 'agent').map(x => x.path), ['a']);
  eq(ch.filtered(fs, 'generated').map(x => x.path), ['b']);
  eq(fs.map(ch.folded), [false, true, true, true, false]);
  eq(ch.counts(fs), {all: 5, agent: 1, generated: 1});
  eq(['add', 'modify', 'delete', 'rename', 'nope'].map(ch.glyph), ['+', '~', '−', '→', '~']);
});

test('a diff\'s rows carry the old and new line numbers its hunk heads give', () => {
  const rows = ch.rowsOf([{at: '@@ -40,3 +40,4 @@ func Render() {', lines: [' a', '-b', '+c', '+d', ' e', '\\ No newline at end of file']},
    {at: '@@ -0,0 +1 @@', lines: ['+new']}]);
  eq(rows.map(r => [r.kind, r.old || 0, r.new || 0, r.h]), [
    ['hunk', 0, 0, 0], ['ctx', 40, 40, 0], ['del', 41, 0, 0], ['add', 0, 41, 0], ['add', 0, 42, 0], ['ctx', 42, 43, 0], ['note', 0, 0, 0],
    ['hunk', 0, 0, 1], ['add', 0, 1, 1]]);
  eq(rows[0].text, '@@ -40,3 +40,4 @@ func Render() {');
  eq(rows[2].text, '-b');
});

test('pages join into hunks: a page that goes on inside a hunk adds to it', () => {
  const got = ch.joined([
    {from: {hunk: 0}, page: {hunks: [{at: '@@ -1,2 +1,2 @@', lines: ['-a', '+b']}, {at: '@@ -9,3 +9,3 @@', lines: [' x']}], of: 3, next: {hunk: 1, line: 1}}},
    {from: {hunk: 1, line: 1}, page: {hunks: [{at: '@@ -9,3 +9,3 @@', lines: ['-y', '+z']}, {at: '@@ -20 +20 @@', lines: ['-p']}], of: 3}},
  ]);
  eq(got, {hunks: [{at: '@@ -1,2 +1,2 @@', lines: ['-a', '+b']}, {at: '@@ -9,3 +9,3 @@', lines: [' x', '-y', '+z']}, {at: '@@ -20 +20 @@', lines: ['-p']}], of: 3, next: null});
  eq(ch.joined([{from: {hunk: 0}, page: {hunks: [], of: 0}}]), {hunks: [], of: 0, next: null});
  eq(ch.joined([{from: {hunk: 0}, page: {hunks: [{at: '@@ -1 +1 @@', lines: ['-a']}], of: 12, next: {hunk: 1}}}]).next, {hunk: 1}, 'where the next page starts');
});

test('side by side pairs a run of removed lines with the added lines after it', () => {
  const rows = ch.rowsOf([{at: '@@ -1,4 +1,3 @@', lines: [' a', '-b', '-c', '-d', '+B', '+C', ' e', '+f', '\\ No newline at end of file']}]);
  eq(ch.sides(rows).map(p => [p.l, p.r, p.lo, p.hi]), [
    [0, 0, 0, 0], [1, 1, 1, 1], [2, 5, 2, 5], [3, 6, 3, 6], [4, -1, 4, 4], [7, 7, 7, 7], [-1, 8, 8, 8], [-1, 9, 9, 9]]);
  eq(ch.sides(ch.rowsOf([{at: '@@ -1 +1 @@', lines: ['-a', '\\ No newline at end of file', '+b']}])).map(p => [p.l, p.r]), [[0, 0], [1, 3], [2, -1]],
    'a note after a removed line stays on the left');
});

test('picking lines: one tap picks a line, a second tap in the same file reaches to it, Shift always reaches', () => {
  let s = ch.pick(null, 4, 4, false);
  eq(s, {a: 4, b: 4, at: [4, 4], one: true});
  s = ch.pick(s, 9, 9, false);
  eq(s, {a: 4, b: 9, at: [4, 4], one: false}, 'the second tap reaches');
  eq(ch.pick(s, 2, 2, false), {a: 2, b: 2, at: [2, 2], one: true}, 'past a range a tap starts again');
  eq(ch.pick(s, 1, 1, true), {a: 1, b: 4, at: [4, 4], one: false}, 'Shift reaches from where it started');
  eq(ch.pick(ch.pick(null, 4, 4, false), 4, 4, false), null, 'the same line again lets go');
  eq(ch.pick(ch.pick(null, 2, 5, false), 7, 7, false), {a: 2, b: 7, at: [2, 5], one: false}, 'a side-by-side row covers both its lines');
  eq(ch.pick(ch.pick(null, 6, 6, false), 2, 5, false), {a: 2, b: 6, at: [6, 6], one: false});
});

test('a quote is path:lines, the lines in a diff block, cut past its limits', () => {
  const rows = ch.rowsOf([{at: '@@ -40,3 +40,4 @@', lines: [' a', '-b', '+c', '+d', ' e']}]);
  const words = {before: s => `${s} (before)`, more: n => `… ${n} more lines`};
  eq(ch.quote({path: 'x/y.go', rows, a: 2, b: 4, ...words}), 'x/y.go:41-42\n```diff\n-b\n+c\n+d\n```');
  eq(ch.quote({path: 'x/y.go', rows, a: 1, b: 1, ...words}), 'x/y.go:40\n```diff\n a\n```', 'one line');
  eq(ch.quote({path: 'x/y.go', rows, a: 2, b: 2, ...words}), 'x/y.go:41 (before)\n```diff\n-b\n```', 'only removed lines: the old numbers');
  eq(ch.quote({path: 'x/y.go', rows, a: 0, b: 1, ...words}), 'x/y.go:40\n```diff\n@@ -40,3 +40,4 @@\n a\n```', 'a hunk head goes in as it is');
  const fenced = ch.rowsOf([{at: '@@ -1 +1 @@', lines: ['+```js', '+````']}]);
  eq(ch.quote({path: 'r.md', rows: fenced, a: 1, b: 2, ...words}), 'r.md:1-2\n`````diff\n+```js\n+````\n`````', 'a longer fence than any in the lines');
  const many = ch.rowsOf([{at: '@@ -1 +1,300 @@', lines: Array.from({length: 300}, (_, i) => '+' + i)}]);
  const q = ch.quote({path: 'big', rows: many, a: 1, b: 300, ...words});
  eq(q.split('\n').length, 1 + 1 + ch.QUOTE_LINES + 1 + 1, 'the head, the fence, the lines, the fence, the rest');
  ok(q.startsWith('big:1-300\n') && q.endsWith('```\n… 100 more lines'), 'the whole range named, the rest counted');
  const wide = ch.rowsOf([{at: '@@ -1 +1,3 @@', lines: ['+' + 'x'.repeat(10000), '+' + 'y'.repeat(10000), '+z']}]);
  ok(ch.quote({path: 'w', rows: wide, a: 1, b: 3, ...words}).endsWith('… 2 more lines'), 'cut past its bytes');
});

test('a quote goes after what is written, on a line of its own', () => {
  eq(ch.withQuote('', 'q'), 'q\n');
  eq(ch.withQuote('look at this  \n', 'q'), 'look at this\n\nq\n');
});

test('an ended run\'s changes are read in pages once, then kept; a file\'s diff in pages of hunks, each fetched once', async () => {
  const r = await outputs();
  const c = ch.createChanges({wire: r.wire, now: () => 1000});
  ok(c.can(), 'the server has run.changes');
  const pdf = 'internal/receipt/pdf.go', golden = 'internal/receipt/testdata/golden.txt';
  let got;
  const pages = {};
  await r.srv.play('changes-list', {
    list: () => { c.list('r1', {ended: true}).then(x => { got = x; }); },
    diff: () => {
      eq(got.files.map(x => x.path), ['go.sum', pdf, 'internal/receipt/pdf_test.go', 'assets/fonts/Inter.ttf',
        'internal/receipt/render.go', golden], 'both pages');
      eq([got.total, got.snapshot, got.git, got.hidden, got.at], [{files: 6, add: 3327, del: 1111}, 't-9a1', true, 0, 1000], 'the head');
      c.diff('r1', pdf, 't-9a1').then(x => { pages.first = x; });
    },
    more: () => {
      eq([pages.first.hunks.length, pages.first.of, pages.first.next], [10, 12, {hunk: 10}], 'the first page');
      c.diff('r1', pdf, 't-9a1', pages.first.next).then(x => { pages.second = x; });
    },
    big: () => {
      const all = ch.joined([{from: {hunk: 0}, page: pages.first}, {from: {hunk: 10}, page: pages.second}]);
      eq([all.hunks.length, all.next], [12, null], 'every hunk');
      c.diff('r1', golden, 't-9a1').then(one => c.diff('r1', golden, 't-9a1', one.next)
        .then(two => { pages.golden = ch.joined([{from: {hunk: 0}, page: one}, {from: one.next, page: two}]); }));
    },
    context: () => {
      eq(pages.golden.hunks.map(x => x.lines.length), [6], 'a hunk past a page goes on inside it');
      c.diff('r1', pdf, 't-9a1', {context: ch.CONTEXT + ch.MORE_CONTEXT}).then(x => { pages.wide = x; });
    },
    space: () => {
      eq(pages.wide.of, 2, 'more context, fewer hunks');
      c.diff('r1', pdf, 't-9a1', {ignoreSpace: true}).then(x => { pages.space = x; });
    },
    again: async () => {
      eq([pages.space.of, pages.space.next], [1, undefined], 'ignoring whitespace, what is left');
      const again = await c.list('r1', {ended: true});
      ok(again === got, 'kept');
      ok((await c.diff('r1', pdf, 't-9a1')) === pages.first, 'the page kept');
    },
  });
});

test('a running run\'s list is taken from the start when its workspace moves between pages, and again on asking', async () => {
  const r = await outputs();
  const c = ch.createChanges({wire: r.wire});
  let got, again, failed;
  await r.srv.play('changes-live', {
    list: () => { c.list('r2').then(x => { got = x; }); },
    refresh: () => { eq([got.snapshot, got.files.length], ['w-2', 2], 'taken again'); c.list('r2').then(x => { again = x; }); },
    diff: () => {
      eq(again.snapshot, 'w-3', 'not kept while it runs');
      c.diff('r2', 'internal/receipt/pdf_test.go', 'w-3').catch(e => { failed = e.code; });
    },
  });
  eq(failed, 'snapshot_changed', 'a hunk of a moved snapshot');
});

test('what cannot be shown: outside git, files only the owner sees, cleared changes and hunks', async () => {
  const r = await home();
  const c = ch.createChanges({wire: r.wire});
  const got = {};
  await r.srv.play('changes-gone', {
    lists: () => {
      c.list('r4', {ended: true}).then(x => { got.r4 = x; });
      c.list('r5', {ended: true}).then(x => { got.r5 = x; });
      c.list('r8', {ended: true}).catch(e => { got.r8 = e.code; });
    },
    diff: () => { c.diff('r4', 'notes.txt', '').catch(e => { got.diff = e.code; }); },
  });
  eq([got.r4.git, got.r4.snapshot, got.r5.hidden, got.r8, got.diff], [false, '', 4, 'gone', 'gone']);
});

test('a diff ignoring whitespace asks the node for it and is kept apart from the plain one', async () => {
  const calls = [];
  const wire = {has: () => true, call: (m, p) => { calls.push([m, p]); return Promise.resolve({hunks: [], of: calls.length}); }};
  const c = ch.createChanges({wire});
  const plain = await c.diff('r1', 'a.go', 't-1');
  const spaceless = await c.diff('r1', 'a.go', 't-1', {ignoreSpace: true});
  const next = await c.diff('r1', 'a.go', 't-1', {hunk: 10, ignoreSpace: true});
  eq(calls.map(x => x[1]), [{run: 'r1', path: 'a.go', snapshot: 't-1', hunk: 0, n: ch.HUNKS}, {run: 'r1', path: 'a.go', snapshot: 't-1', hunk: 0, n: ch.HUNKS, ignore_space: true},
    {run: 'r1', path: 'a.go', snapshot: 't-1', hunk: 10, n: ch.HUNKS, ignore_space: true}], 'asked of the node, every page');
  ok(plain !== spaceless && next.of === 3, 'each its own page');
  ok((await c.diff('r1', 'a.go', 't-1', {ignoreSpace: true})) === spaceless && (await c.diff('r1', 'a.go', 't-1')) === plain, 'both kept');
  eq(calls.length, 3, 'nothing asked again');
});

test('only whitespace changed: said so when ignoring it, not taken for a file with nothing to show', () => {
  eq(ch.onlySpace(file('a', {add: 2, del: 2}), {hunks: [], of: 0, ignoreSpace: true}), true);
  eq(ch.onlySpace(file('a', {add: 2, del: 2}), {hunks: [], of: 0}), false, 'not ignoring');
  eq(ch.onlySpace(file('a', {add: 0, del: 0, op: 'rename'}), {hunks: [], of: 0, ignoreSpace: true}), false, 'a rename with no lines changed');
  eq(ch.onlySpace(file('a', {add: 2, del: 2}), {hunks: [{at: '@@ -1 +1 @@', lines: ['-a', '+b']}], of: 1, ignoreSpace: true}), false, 'hunks left');
});

test('the list gives up after restarting a few times', async () => {
  let n = 0;
  const wire = {has: () => true, call: () => { n++; return Promise.reject(Object.assign(new Error('moved'), {code: 'snapshot_changed'})); }};
  const e = await throws(() => ch.createChanges({wire}).list('r9'), /moved/);
  eq([e.code, n], ['snapshot_changed', ch.RESTARTS + 1]);
});

const sample = {files: [file('go.sum', {generated: true, add: 14, del: 2}), file('internal/receipt/pdf.go', {agent: true, add: 48, del: 6}),
  file('internal/receipt/pdf_test.go', {op: 'add', agent: true, add: 62}), file('assets/fonts/Inter.ttf', {binary: true, bytes: 14336, old_bytes: 12288}),
  file('internal/receipt/render.go', {op: 'rename', from: 'internal/receipt/draw.go', add: 3, del: 3}),
  file('internal/receipt/testdata/golden.txt', {big: true, add: 3200, del: 1100})],
total: {files: 6, add: 3327, del: 1111}, snapshot: 't-9a1', git: true, hidden: 0, at: Date.parse('2026-09-30T14:20:00Z')};

test('a diff draws one line per line and nothing else when nothing is left out', () => {
  const s = drawn(html`<${Diff} lines=${['@@ -1 +1 @@', '-a', '+b']} />`, 'desktop', 'zh');
  eq(s, '<div class="out-pre-wrap"><pre class="out-pre out-diff"><span class="hunk">@@ -1 +1 @@\n</span><span class="del">-a\n</span><span class="add">+b\n</span></pre></div>');
  ok(drawn(html`<${Diff} lines=${['+1', '+2', '+3']} max=${2} />`, 'desktop', 'zh').includes(words.f('out.cut', 1)), 'cut past max');
});

test('the changes page draws in both forms and both languages, styled and worded', () => {
  const none = () => {};
  const hunks = [{at: '@@ -40,7 +40,8 @@ func Render(r Receipt) ([]byte, error) {', lines: [' \tpdf := gofpdf.New()', '-\tpdf.SetFont("Arial")', '+\tpdf.AddUTF8Font("Inter")', '+\tpdf.SetFont("Inter")', ' \tpdf.AddPage()']}];
  const shown = {'internal/receipt/pdf.go': {hunks, of: 3, next: {hunk: 1}, context: 3}};
  const open = new Set(['internal/receipt/pdf.go']);
  const states = {
    list: {data: sample, open, diffs: shown, onPick: none},
    split: {data: sample, open, diffs: shown, wide: true, view: 'split', onPick: none},
    picked: {data: sample, open, diffs: shown, onPick: none, notes: true, sel: {path: 'internal/receipt/pdf.go', a: 2, b: 4, at: [2, 2], one: false}},
    goesOn: {data: sample, open, diffs: {'internal/receipt/pdf.go': {hunks, of: 1, next: {hunk: 0, line: 5}, context: 23}}},
    running: {data: sample, running: true},
    outside: {data: {...sample, git: false, snapshot: ''}},
    hidden: {data: {...sample, hidden: 4}},
    space: {data: sample, open, diffs: {'internal/receipt/pdf.go': {hunks: [], of: 0, context: 3, ignoreSpace: true}}, ignoreSpace: true, onIgnoreSpace: none},
    spaceOld: {data: sample, open, diffs: shown, spaceOff: true, onIgnoreSpace: none},
    gone: {error: 'gone'}, unsupported: {unsupported: true}, loading: {}, empty: {data: {...sample, files: [], total: {files: 0, add: 0, del: 0}}},
  };
  const zh = {}, en = {};
  for (const [name, p] of Object.entries(states)) for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
    const s = drawn(html`<${Changes} ...${p} onToggle=${none} onRefresh=${none} />`, f, lang);
    eq([...classesOf(s)].filter(c => !cssClasses.has(c)), [], `${name} ${f}/${lang}: classes without a rule`);
    eq(s.match(/\bchg\.[a-zA-Z.]+/g), null, `${name} ${f}/${lang}: words not found`);
    (lang === 'zh' ? zh : en)[name + (f === 'phone' ? '/phone' : '')] = s;
  }
  for (const want of ['6 个文件', '+3,327', '−1,111', '只看 agent 用工具改的', '生成的文件', 'internal/receipt', 'internal/receipt/draw.go → internal/receipt/render.go',
    '二进制 · 12 KB → 14 KB', '改动很大：+3,200 −1,100', '@@ -40,7 +40,8 @@', '显示了 1 / 3 处', '显示后面 2 处', '多看上下文', '上下文 3 行',
    '点行号选中一行，再点另一行选到那里']) ok(zh.list.includes(want), `zh list: no ${want}`);
  ok(zh.list.includes('data-lo="3" data-hi="3"'), 'a line\'s numbers take a tap');
  ok(zh.list.includes('<span class="dv-n">41</span><span class="dv-n"></span>'), 'a removed line: its old number only');
  ok(zh.list.includes('<span class="dv-n"></span><span class="dv-n">42</span>'), 'an added line: its new number only');
  ok(!zh.list.includes('左右对照') && !zh['list/phone'].includes('左右对照'), 'side by side only where wide');
  ok(zh.split.includes('左右对照') && zh.split.includes('dv-split') && !zh['split/phone'].includes('dv-split'), 'side by side on a wide desktop, never on a phone');
  ok(zh.picked.includes('选了 3 行') && zh.picked.includes('发给 agent') && zh.picked.includes('写进退回意见') && zh.picked.includes('dv-row dv-del on'), 'picked lines');
  ok(!zh.list.includes('发给 agent'), 'nothing picked, nothing to send');
  ok(zh.goesOn.includes('这一处没显示完，接着显示') && zh.goesOn.includes('上下文 23 行') && !zh.goesOn.includes('显示了'), 'a hunk that goes on');
  ok(!zh.running.includes('点行号'), 'no hint while nothing is open');
  ok(zh.running.includes('截至 14:20') && zh.running.includes('刷新'), 'a running run says when it was taken');
  ok(zh.outside.includes('只包含 agent 用工具改的，命令改的看不到'), 'outside git');
  ok(zh.hidden.includes('这段时间这个目录里还有 4 个文件变了，只有机器主人能看'), 'only the owner');
  ok(zh.gone.includes('这次运行的改动已经清理'), 'cleared');
  ok(zh.unsupported.includes('这台 server 还不能看改动'), 'an older server');
  ok(zh.empty.includes('没有改动'), 'nothing changed');
  for (const f of ['list', 'list/phone']) ok(zh[f].includes('忽略空白') && zh[f].includes('role="checkbox" aria-checked="false"'), `${f}: the toggle, off`);
  ok(zh.space.includes('忽略空白') && zh.space.includes('aria-checked="true"'), 'the toggle, on');
  ok(zh.space.includes('只有空白改动') && !zh.space.includes('这个文件没有可显示的改动行'), 'a file changed only in its whitespace');
  ok(zh.spaceOld.includes('disabled') && zh.spaceOld.includes('这台机器上的 tend 太旧，不能忽略空白'), 'an older node');
  ok(!zh.unsupported.includes('忽略空白') && !zh.loading.includes('忽略空白'), 'no toggle without a list');
  for (const want of ['Files: 6', 'Only the agent\'s tools', 'Binary · 12 KB → 14 KB', 'Large change: +3,200 −1,100', 'Showing 1 of 3 hunks', 'Show the next 2 hunks',
    'More context']) ok(en.list.includes(want), `en list: no ${want}`);
  ok(en.list.includes('Ignore whitespace') && en.space.includes('Only whitespace changed') && en.spaceOld.includes('too old to ignore whitespace'), 'en whitespace');
  ok(en.split.includes('Side by side') && en.picked.includes('Lines picked: 3') && en.picked.includes('Add to the send-back notes'), 'en picked');
});

test('side by side keeps both sides level: one row a side for every pair', () => {
  const rows = ch.rowsOf([{at: '@@ -1,3 +1,2 @@', lines: ['-a', '-b', '+A', ' c', '\\ No newline at end of file']}]);
  const s = drawn(html`<${DiffLines} rows=${rows} split />`, 'desktop', 'zh');
  const cols = s.split('<div class="dv-col">').slice(1);
  eq(cols.map(c => (c.match(/class="dv-row/g) || []).length), [5, 5], 'as many rows a side');
  ok(cols[1].includes('dv-row dv-none'), 'the right side fills in where nothing was added');
});

run();
