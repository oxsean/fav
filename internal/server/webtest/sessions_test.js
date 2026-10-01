// sessions_test draws a machine's own sessions page in both forms and both languages from the sessions frames over the
// team frames, and drives it in a fake document: the list with who runs and its filter, a conversation read from the
// newest with the earlier page and a message's full text, the resume line copied, a machine sharing only its runs'
// sessions and a file that changed under a page; the machines page's way in for who may read them. Its last case
// hands the resume lines to the Go test, which types them with internal/shell.
process.env.TZ = 'UTC';
import {readFileSync} from 'node:fs';
import {render} from '../web/vendor/preact.mjs';
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
import * as ss from '../web/core/sessions.js';
import {install} from './dom.js';
import {settle} from './fake.js';
import {NOW, team} from './rig.js';
import {test, eq, ok, run, until} from './check.js';

const css = ['base.css', 'components.css', 'pages.css'].map(f => readFileSync(new URL(`../web/css/${f}`, import.meta.url), 'utf8')).join('\n');
const cssClasses = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map(m => m[1]));
const wordsLeft = s => s.match(/\b(sess|mach|secret|api|app|ui|status)\.[a-zA-Z_]+\b/g);

const admin = {id: 'u_a', name: 'Ann Lee', role: 'admin'};
const bo = {id: 'u_b', name: 'Bo Lin', role: 'member'};
const noStore = {getItem: () => null, setItem() {}};

function fakeHistory(url) {
  const loc = {pathname: '/', search: '', hash: ''};
  const set = u => { const x = new URL(u, 'http://tend.test'); loc.pathname = x.pathname; loc.search = x.search; loc.hash = x.hash; };
  set(url);
  return {location: loc, history: {state: null, pushState: (_, __, u) => set(u), replaceState: (_, __, u) => set(u)}};
}

// app is the signed-in page at url; copied keeps what the page put on the clipboard.
function app(r, {url = '/?page=machines&sessions=mba', session = admin} = {}) {
  const keys = createKeys({timers: r.clk});
  const toasts = createToasts({timers: r.clk});
  const commands = createCommands({wire: r.wire, newID: () => 'c1'});
  const {location, history} = fakeHistory(url);
  const router = createRouter({location, history});
  const copied = [];
  const http = {machineCreds: () => Promise.resolve([])};
  const props = {store: r.store, commands, toasts, wire: r.wire, http, router, keys, nav: createNav({storage: noStore, width: 1440}),
    prefs: createPrefs({storage: noStore, asked: 'zh'}), session, clock: () => NOW, fetchOutput: () => Promise.resolve({events: []}),
    storage: noStore, copy: text => { copied.push(text); return Promise.resolve(); }, onLogout() {}};
  return {...props, copied, vnode: () => html`<${KeysContext.Provider} value=${keys}><${App} ...${props} /><//>`};
}

async function mount(vnode, f) {
  form.value = f;
  const root = install();
  await act(() => render(vnode, root));
  return root;
}
const click = el => act(() => el.dispatch('click'));
const type = (el, text) => act(() => { el.value = text; el.dispatch('input'); });
const labelOf = b => b.textContent.slice(0, b.textContent.length - b.find('kbd').map(k => k.textContent).join('').length).trim();
const buttonOf = (root, label) => {
  const got = root.find('button').filter(b => labelOf(b) === label);
  if (got.length !== 1) throw new Error(`${label}: ${got.length} buttons`);
  return got[0];
};
const styled = (root, what) => {
  const classes = new Set(root.all().flatMap(e => e.className.split(' ').filter(Boolean)));
  eq([...classes].filter(c => !cssClasses.has(c)), [], `${what}: classes without a rule`);
  eq(wordsLeft(root.textContent), null, `${what}: words not found`);
};
// rows are the titles listed: a desktop's table rows or a phone's cards.
const rowsOf = root => root.find('.tr').map(x => x.find('.td')[1].textContent).concat(root.find('.card-row').map(x => x.one('.card-primary').textContent));
const msgsOf = root => root.find('.sess-msg').map(m => m.one('.sess-text').textContent);

for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
  test(`a machine's sessions on a ${f} in ${lang}: listed, filtered, one read from the newest, earlier and in full, its resume line copied`, async () => {
    const r = await team();
    let a, root;
    words.lang.value = lang;
    try {
      await r.srv.play('sessions', {
        async mount() { a = app(r); root = await mount(a.vnode(), f); },
        async listed() {
          await act(() => settle());
          eq(rowsOf(root), ['Draft the release notes', 'Fix the checkout total', 'Port the importer', 'Tidy the README'], 'newest first, a running one the index lacks too');
          ok(root.textContent.includes(words.f('sess.summary', 4, 2)), 'how many and how many run');
          eq(root.find('.sess-share').length, 0, 'a machine sharing all says nothing of it');
          styled(root, `${f}/${lang} list`);
          await type(root.one('input'), 'shop fix');
          eq(rowsOf(root), ['Fix the checkout total'], 'every word of the filter');
          await type(root.one('input'), 'codex');
          eq(rowsOf(root), ['Port the importer'], 'by agent');
          await type(root.one('input'), '');
        },
        async open() {
          const row = f === 'phone' ? root.find('.card-row')[1] : root.find('.tr')[1];
          await click(row);
          eq(a.router.route.value, {page: 'machines', sessions: 'mba', session: 'claude:c-fix'}, 'the address');
        },
        async opened() {
          await act(() => settle());
          eq(msgsOf(root).length, 3, 'the newest page, oldest at the top');
          ok(msgsOf(root)[0].startsWith('I read the cart code'), 'oldest first');
          ok(root.textContent.includes(words.f('sess.steps', 2, 'Read · Bash')), 'its steps folded');
          await click(root.one('.sess-steps'));
          ok(root.one('.sess-step-list').textContent.includes('go test ./internal/cart'), 'its steps open');
          const resume = root.one('.secret');
          eq(resume.one('.secret-value').textContent, 'cd /Users/ann/dev/shop && claude --resume c-fix', 'the resume line');
          ok(resume.textContent.includes(words.f('sess.resume', 'mba')), 'where it resumes');
          await click(resume.one('button'));
          eq(a.copied, ['cd /Users/ann/dev/shop && claude --resume c-fix'], 'copied');
          styled(root, `${f}/${lang} conversation`);
        },
        async earlier() { await click(buttonOf(root, words.t('sess.earlier'))); },
        async older() {
          await act(() => settle());
          eq(msgsOf(root)[0], 'Why is the checkout total off by a cent?', 'the earlier page above');
          ok(root.textContent.includes(words.t('sess.start')), 'the head reached');
          eq(root.find('button').filter(b => labelOf(b) === words.t('sess.earlier')).length, 0, 'nothing earlier');
        },
        async full() { await click(buttonOf(root, words.t('sess.full'))); },
        async fulled() {
          await act(() => settle());
          const last = root.find('.sess-msg').at(-1);
          eq(last.find('li').map(x => x.textContent), ['the tax is added per line', 'it is rounded once at the end'], 'the full text as Markdown');
          ok(buttonOf(root, words.t('sess.fold')), 'and folds again');
        },
      });
      eq(r.errors, [], 'errors');
    } finally { form.value = 'desktop'; words.lang.value = 'zh'; }
  });
}

test('a machine sharing only its runs\' sessions says how to share them all; a page of a changed file is read again', async () => {
  const r = await team();
  let a, root;
  await r.srv.play('sessions-share', {
    async mount() { a = app(r, {url: '/?page=machines&sessions=bo-laptop&session=claude:c-run', session: bo}); root = await mount(a.vnode(), 'desktop'); },
    async listed() {
      await act(() => settle());
      eq(rowsOf(root), ['Add the search box'], 'listed though its tend is too old for live');
      ok(root.one('.sess-share').textContent.includes('"share_sessions": "all"'), 'how to share them all');
      eq(root.one('.sess-share').textContent, words.f('sess.shareRuns', 'bo-laptop'), 'the hint');
    },
    async open() { await until(() => r.srv.current().sent.length > 0, 'the newest page asked'); },
    async opened() { await act(() => settle()); eq(msgsOf(root), ['The search box is in.'], 'opened from the address'); },
    async earlier() { await click(buttonOf(root, words.t('sess.earlier'))); },
    async stale() { await until(() => r.srv.current().sent.length > 0, 'the newest page asked again'); },
    async reread() {
      await act(() => settle());
      eq(msgsOf(root), ['Add the search box.', 'The search box is in, with tests.'], 'read again from the newest');
      ok(root.textContent.includes(words.t('sess.reread')), 'and says so');
    },
  });
  eq(r.errors, [], 'errors');
});

test('the machines page opens a machine\'s sessions for its owner and admins only', async () => {
  const r = await team();
  const a = app(r, {url: '/?page=machines'});
  const root = await mount(a.vnode(), 'desktop');
  await click(root.find('.mach-card').find(c => c.one('b').textContent === 'mba'));
  ok(root.one('.mach-aside').textContent.includes(words.t('mach.share.all')), 'what it shares');
  await click(buttonOf(root.one('.mach-aside'), words.t('mach.sessions')));
  eq(a.router.route.value, {page: 'machines', sessions: 'mba'}, 'an admin opens them');
  const b = app(r, {url: '/?page=machines', session: bo});
  const other = await mount(b.vnode(), 'desktop');
  await click(other.find('.mach-card').find(c => c.one('b').textContent === 'mba'));
  eq(other.one('.mach-aside').find('button').filter(x => labelOf(x) === words.t('mach.sessions')).length, 0, 'a machine shared with Bo is not his to read');
  await click(other.find('.mach-card').find(c => c.one('b').textContent === 'bo-laptop'));
  ok(other.one('.mach-aside').textContent.includes(words.t('mach.share.runs')), 'his shares only its runs');
  ok(buttonOf(other.one('.mach-aside'), words.t('mach.sessions')), 'his own he reads');
  const c = app(r, {url: '/?page=machines&sessions=mba', session: bo});
  const refused = await mount(c.vnode(), 'desktop');
  ok(refused.textContent.includes(words.t('sess.cannot')), 'the address of another\'s machine reads nothing');
  try {
    const phone = await mount(app(r, {url: '/?page=machines'}).vnode(), 'phone');
    await click(phone.find('.card-row').find(x => x.textContent.includes('linux')));
    ok(buttonOf(phone, words.t('mach.sessions')), 'a phone opens them from a machine\'s facts');
  } finally { form.value = 'desktop'; }
});

test('the list merges who runs and filters by every word', () => {
  const rows = ss.rowsOf({sessions: [{provider: 'claude', session_id: 'a', title: 'One', cwd: '/w/a', last_at: '2026-09-30T10:00:00Z'},
    {provider: 'codex', session_id: 'b', title: 'Two', cwd: '/w/b', last_at: '2026-09-30T12:00:00Z'}]},
  {a: {Status: 'blocked', Agent: 'claude'}, c: {Agent: 'claude', Title: 'Three', Cwd: '/w/c', Since: '2026-09-30T11:00:00Z'}, d: {Title: 'no agent'}});
  eq(rows.map(r => [r.key, !!r.live]), [['codex:b', false], ['claude:c', true], ['claude:a', true]], 'newest first; live only with an agent');
  eq(ss.matches(rows, 'W/A one').map(r => r.key), ['claude:a'], 'case and every word');
  eq(ss.older([{Off: 9}], {Msgs: [{Off: 5}, {Off: 1}]}).map(m => m.Off), [1, 5, 9], 'an earlier page goes above, oldest first');
  eq([ss.cut({Text: 'abc', Chars: 3}), ss.cut({Text: 'ab', Chars: 9})], [false, true], 'a shortened text');
  eq(ss.refOf('claude:x:y'), {provider: 'claude', session_id: 'x:y'}, 'a key back to its ref');
});

// The resume lines for the Go test to type with internal/shell: [os, dir, argv, the page's line].
test('resume lines', () => {
  const dirs = ['/Users/ann/dev/shop', '/Users/ann/dev/my shop', "/w/it's", '/w/$HOME', 'C:\\Users\\Ann\\dev\\shop', 'C:\\Users\\Ann\\my shop', "C:\\w\\it's", 'C:\\w\\a,b', ''];
  const rows = [{provider: 'claude', session_id: 'c-1'}, {provider: 'codex', session_id: 'x-1'}, {provider: 'claude', session_id: 'c-2', live: {BackgroundID: 'bg7'}}];
  const out = [];
  for (const os of ['darwin', 'linux', 'windows']) for (const cwd of dirs) for (const row of rows) {
    const argv = ss.resumeArgv(row);
    out.push([os, cwd, argv, ss.resumeLine({...row, cwd}, os)]);
  }
  eq(ss.resumeLine({provider: 'fake', session_id: 'f'}, 'linux'), '', 'an agent tend cannot resume');
  return out;
});

await run();
