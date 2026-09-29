// changes_test checks the changes tab's core: a run's files read in pages (run.changes) and taken again from the start
// when its workspace moves between them, an ended run's list kept, a file's first hunk (run.diff), the files in
// directory order, the filters, what starts folded, and the first hunk cut to its preview; then the changes page itself
// in both forms and both languages.
process.env.TZ = 'UTC';
import {readFileSync} from 'node:fs';
import renderToString from './vendor/render-to-string.mjs';
import * as ch from '../web/core/changes.js';
import {form} from '../web/core/layout.js';
import {words} from '../web/core/i18n.js';
import {html} from '../web/ui/base.js';
import {Changes} from '../web/pages/changes.js';
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

test('a file\'s first hunk is cut to the preview, saying how much more there is', () => {
  const lines = Array.from({length: 50}, (_, i) => '+' + i);
  eq(ch.preview({hunks: [{at: '@@ -1 +1,50 @@', lines}], of: 3}), {at: '@@ -1 +1,50 @@', lines: lines.slice(0, ch.PREVIEW_LINES), cut: 10, more: 2});
  eq(ch.preview({hunks: [{at: '@@ -1 +1 @@', lines: ['-a', '+b']}], of: 1}), {at: '@@ -1 +1 @@', lines: ['-a', '+b'], cut: 0, more: 0});
  eq(ch.preview({hunks: []}), null);
});

test('an ended run\'s changes are read in pages once, then kept; its first hunk is fetched once', async () => {
  const r = await outputs();
  const c = ch.createChanges({wire: r.wire, now: () => 1000});
  ok(c.can(), 'the server has run.changes');
  let got;
  await r.srv.play('changes-list', {
    list: () => { c.list('r1', {ended: true}).then(x => { got = x; }); },
    diff: () => {
      eq(got.files.map(x => x.path), ['go.sum', 'internal/receipt/pdf.go', 'internal/receipt/pdf_test.go', 'assets/fonts/Inter.ttf',
        'internal/receipt/render.go', 'internal/receipt/testdata/golden.txt'], 'both pages');
      eq([got.total, got.snapshot, got.git, got.hidden, got.at], [{files: 6, add: 3327, del: 1111}, 't-9a1', true, 0, 1000], 'the head');
      c.diff('r1', 'internal/receipt/pdf.go', 't-9a1').then(x => { got.diff = x; });
    },
    again: async () => {
      eq(got.diff.of, 3, 'the hunks');
      const again = await c.list('r1', {ended: true});
      ok(again === got, 'kept');
      ok((await c.diff('r1', 'internal/receipt/pdf.go', 't-9a1')) === got.diff, 'the hunk kept');
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
  const states = {
    list: {data: sample, open: new Set(['internal/receipt/pdf.go']), previews: {'internal/receipt/pdf.go': {at: '@@ -40,7 +40,12 @@', lines: ['-a', '+b', ' c'], cut: 0, more: 2}}},
    running: {data: sample, running: true},
    outside: {data: {...sample, git: false, snapshot: ''}},
    hidden: {data: {...sample, hidden: 4}},
    gone: {error: 'gone'}, unsupported: {unsupported: true}, loading: {}, empty: {data: {...sample, files: [], total: {files: 0, add: 0, del: 0}}},
  };
  const zh = {}, en = {};
  for (const [name, p] of Object.entries(states)) for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
    const s = drawn(html`<${Changes} ...${p} onToggle=${none} onRefresh=${none} />`, f, lang);
    eq([...classesOf(s)].filter(c => !cssClasses.has(c)), [], `${name} ${f}/${lang}: classes without a rule`);
    eq(s.match(/\bchg\.[a-zA-Z]+/g), null, `${name} ${f}/${lang}: words not found`);
    if (f === 'desktop') (lang === 'zh' ? zh : en)[name] = s;
  }
  for (const want of ['6 个文件', '+3,327', '−1,111', '只看 agent 用工具改的', '生成的文件', 'internal/receipt', 'internal/receipt/draw.go → internal/receipt/render.go',
    '二进制 · 12 KB → 14 KB', '改动很大：+3,200 −1,100', '@@ -40,7 +40,12 @@', '还有 2 处', '按处翻页以后再做']) ok(zh.list.includes(want), `zh list: no ${want}`);
  ok(zh.running.includes('截至 14:20') && zh.running.includes('刷新'), 'a running run says when it was taken');
  ok(zh.outside.includes('只包含 agent 用工具改的，命令改的看不到'), 'outside git');
  ok(zh.hidden.includes('这段时间这个目录里还有 4 个文件变了，只有机器主人能看'), 'only the owner');
  ok(zh.gone.includes('这次运行的改动已经清理'), 'cleared');
  ok(zh.unsupported.includes('这台 server 还不能看改动'), 'an older server');
  ok(zh.empty.includes('没有改动'), 'nothing changed');
  for (const want of ['Files: 6', 'Only the agent\'s tools', 'Binary · 12 KB → 14 KB', 'Large change: +3,200 −1,100', '2 more hunks']) ok(en.list.includes(want), `en list: no ${want}`);
});

run();
