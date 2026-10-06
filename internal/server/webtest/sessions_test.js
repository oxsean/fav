// sessions_test draws a machine's own sessions page in both forms and both languages from the sessions frames over the
// team frames, and drives it in a fake document: the list with who runs and its filter, a conversation read from the
// newest with the earlier page and a message's full text, the resume line copied, favorites, tags, summaries and
// archived sessions in the list and over a conversation, the favorites-only switch kept in the address, a machine
// sharing only its runs' sessions and a file that changed under a page, a machine shared with the viewer read only and
// taken back while read; the machines page's and the phone home's ways in for who may read
// them, and where its back goes. Its last case hands the resume lines to the Go test, which types them with internal/shell.
process.env.TZ = 'UTC';
import {readFileSync} from 'node:fs';
import {render} from '../web/vendor/preact.mjs';
import {signal} from '../web/vendor/signals-core.mjs';
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
import {ROW} from '../web/pages/sessions.js';
import {windowOf, VIRTUAL_ABOVE} from '../web/ui/table.js';
import * as ss from '../web/core/sessions.js';
import {parse, format} from '../web/core/router.js';
import {install} from './dom.js';
import {settle} from './fake.js';
import {NOW, team} from './rig.js';
import {test, eq, ok, run, until} from './check.js';

const css = ['base.css', 'components.css', 'pages.css'].map(f => readFileSync(new URL(`../web/css/${f}`, import.meta.url), 'utf8')).join('\n');
const cssClasses = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map(m => m[1]));
const wordsLeft = s => s.match(/\b(sess|mach|secret|api|app|ui|status|home)\.[a-zA-Z_]+\b/g);

const admin = {id: 'u_a', name: 'Ann Lee', role: 'admin'};
const bo = {id: 'u_b', name: 'Bo Lin', role: 'member'};
const noStore = {getItem: () => null, setItem() {}};
const names = signal({u_a: 'Ann Lee', u_b: 'Bo Lin'});

// seenBy gives r's machines as the coordinator tells who of them (team-state is the admin's): no machine's sessions
// are shared there, so each reads their own alone.
const seenBy = (r, who) => { r.store.machines.value = r.store.machines.value.map(m => ({...m, sessions: m.owner === who.id})); };

// fakeHistory keeps the page's entries; back() steps to the one before and tells popped, as the browser's popstate does.
function fakeHistory(url) {
  const loc = {pathname: '/', search: '', hash: ''};
  const set = u => { const x = new URL(u, 'http://tend.test'); loc.pathname = x.pathname; loc.search = x.search; loc.hash = x.hash; };
  const entries = [{state: null, url}];
  const history = {
    get state() { return entries.at(-1).state; },
    get length() { return entries.length; },
    pushState(state, _, u) { entries.push({state, url: u}); set(u); },
    replaceState(state, _, u) { entries[entries.length - 1] = {state, url: u}; set(u); },
    back() { entries.pop(); set(entries.at(-1).url); history.popped?.(); },
  };
  set(url);
  return {location: loc, history};
}

// app is the signed-in page at url; copied keeps what the page put on the clipboard.
function app(r, {url = '/?page=machines&sessions=mba', session = admin, wire = r.wire} = {}) {
  const keys = createKeys({timers: r.clk});
  const toasts = createToasts({timers: r.clk});
  const commands = createCommands({wire: r.wire, newID: () => 'c1'});
  const {location, history} = fakeHistory(url);
  const router = createRouter({location, history});
  history.popped = () => router.popped();
  const copied = [];
  const http = {machineCreds: () => Promise.resolve([])};
  const props = {store: r.store, commands, toasts, wire, http, router, keys, nav: createNav({storage: noStore, width: 1440}),
    prefs: createPrefs({storage: noStore, asked: 'zh'}), session, names, clock: () => NOW, fetchOutput: () => Promise.resolve({events: []}),
    storage: noStore, copy: text => { copied.push(text); return Promise.resolve(); }, onLogout() {}};
  return {...props, copied, history, vnode: () => html`<${KeysContext.Provider} value=${keys}><${App} ...${props} /><//>`};
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
const rowsOf = root => root.find('.tr').map(x => x.find('.td')[1].find('.ell')[0].textContent)
  .concat(root.find('.card-row').map(x => x.one('.card-primary').find('.ell')[0].textContent));
// labels are the task labels listed, by the title of their row.
const labelsOf = root => Object.fromEntries([...root.find('.tr').map(x => x.find('.td')[1]), ...root.find('.card-row').map(x => x.one('.card-primary'))]
  .filter(c => c.find('.sess-task').length).map(c => [c.find('.ell')[0].textContent, c.one('.sess-task').textContent]));
const msgsOf = root => root.find('.sess-msg').map(m => m.one('.sess-text').textContent);
// marksOf is each listed row by its title: starred, its tag chips as drawn (+N for the rest), its summary, dimmed.
const marksOf = root => Object.fromEntries([...root.find('.tr').map(x => [x, x.find('.td')[1]]), ...root.find('.card-row').map(x => [x, x])]
  .map(([row, c]) => [c.find('.ell')[0].textContent, {star: c.find('.sess-star').length === 1,
    tags: c.find('.sess-tags').flatMap(t => t.find('.chip')).map(x => x.textContent),
    sum: c.find('.sess-sum').map(x => x.textContent).join(''), archived: row.className.split(' ').includes('sess-archived')}]));
const BACKUP = 'Investigate why the nightly backup on old-box skips the uploads folder when the disk is nearly full';
const favSwitch = root => root.one('.sess-tools').find('button').at(-1);
// projectsOf is each listed row's project as drawn, by its title.
const projectsOf = root => Object.fromEntries([...root.find('.tr').map(x => x.find('.td')[1]), ...root.find('.card-row').map(x => x.one('.card-primary'))]
  .map(c => [c.find('.ell')[0].textContent, c.parentNode.find('.sess-proj').map(x => x.textContent).join('')]));
// pickProject opens the project picker and picks the option labelled label.
const pickProject = async (root, label) => {
  await click(root.one('.sess-pfield').one('.picker-btn'));
  await click(root.find('[role=option]').find(o => o.one('.pk-label').textContent === label));
};
const projectOptions = root => root.find('[role=option]').map(o => [o.one('.pk-label').textContent, o.find('.pk-sub').map(x => x.textContent).join(''), o.find('.pk-note').map(x => x.textContent).join('')]);

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
          eq(rowsOf(root), ['Draft the release notes', 'Fix the checkout total', 'Port the importer', BACKUP, 'Tidy the README'], 'newest first');
          ok(root.textContent.includes(words.f('sess.summary', 5, 2)), 'how many and how many run');
          eq(marksOf(root), {
            'Draft the release notes': {star: true, tags: ['#release', '#docs'], sum: 'Group the merged PRs since v0.9 by area and draft the notes; waits on the importer change.', archived: false},
            'Fix the checkout total': {star: false, tags: [], sum: '', archived: false},
            'Port the importer': {star: true, tags: ['#importer', '#csv', '+1'], sum: 'CSV 导入改成流式读取，内存从 1.2 GB 降到 80 MB；Windows 换行还没测。', archived: false},
            [BACKUP]: {star: true, tags: ['#backup', '#ops', '+3'], sum: 'The retry path checks free space before it mounts the share, so it sees the wrong disk and gives up quietly.', archived: false},
            'Tidy the README': {star: true, tags: ['#docs'], sum: 'Rewrote the install section.', archived: true},
          }, 'a star for a favorite, two tags and the count of the rest, one summary line, an archived one dimmed');
          eq(root.find('.sess-star').map(x => x.getAttribute('aria-label')), Array(4).fill(words.t('sess.favorited')), 'a star says what it means');
          eq(root.find('.sess-archived').map(x => x.find('.chip').some(c => c.textContent === words.t('sess.archived'))), [true], 'and an archived one says so');
          eq(root.find('.sess-star').filter(x => x.localName === 'button').length, 0, 'read only: a star is no button');
          eq(favSwitch(root).getAttribute('aria-pressed'), 'false', 'the favorites-only switch is off');
          ok(favSwitch(root).textContent.includes(words.t('sess.favOnly')) && favSwitch(root).textContent.includes('4'), 'and counts them');
          await click(favSwitch(root));
          eq(a.router.route.value, {page: 'machines', sessions: 'mba', fav: true}, 'kept in the address');
          eq(a.history.length, 1, 'in place of the address before');
          eq(rowsOf(root), ['Draft the release notes', 'Port the importer', BACKUP, 'Tidy the README'], 'only the favorites, the archived one too');
          ok(root.textContent.includes(words.f('sess.favSummary', 4, 5)), 'how many of how many');
          eq(favSwitch(root).getAttribute('aria-pressed'), 'true', 'on');
          await type(root.one('input'), 'shop fix');
          eq(rowsOf(root), [], 'the filter within the favorites');
          ok(root.textContent.includes(words.t('sess.noMatch')), 'none match');
          await type(root.one('input'), '');
          await click(favSwitch(root));
          eq(a.router.route.value, {page: 'machines', sessions: 'mba'}, 'off again');
          eq(rowsOf(root).length, 5, 'all of them');
          const none = f === 'phone' ? words.t('sess.projectNone') : '—';
          eq(projectsOf(root), {'Draft the release notes': 'Shop', 'Fix the checkout total': 'Shop', 'Port the importer': none, [BACKUP]: 'Docs', 'Tidy the README': 'Docs'},
            'each row\'s project as sessions.list tells it');
          await click(root.one('.sess-pfield').one('.picker-btn'));
          eq(projectOptions(root), [[words.t('sess.projectAll'), '', '5'], ['Docs', words.f('sess.projectTeam', 'Bo Lin'), '2'], ['Shop', words.f('sess.projectTeam', 'Ann Lee'), '2'],
            ['side', words.t('sess.projectPersonal'), '0'], [words.t('sess.projectNone'), words.t('sess.projectNoneSub'), '1']], 'every project seen, with its sessions here');
          await click(root.find('[role=option]').find(o => o.one('.pk-label').textContent === 'Shop'));
          eq(a.router.route.value, {page: 'machines', sessions: 'mba', project: 'p1'}, 'the project in the address');
          eq(rowsOf(root), ['Draft the release notes', 'Fix the checkout total'], 'its sessions only');
          ok(root.textContent.includes(words.f('sess.projectSummary', 'Shop', 2, 5)), 'how many of how many');
          await click(favSwitch(root));
          eq(a.router.route.value, {page: 'machines', sessions: 'mba', project: 'p1', fav: true}, 'with favorites only too');
          eq(rowsOf(root), ['Draft the release notes'], 'its favorites');
          await click(favSwitch(root));
          await pickProject(root, words.t('sess.projectNone'));
          eq([a.router.route.value.project, rowsOf(root)], ['none', ['Port the importer']], 'in no project');
          await pickProject(root, 'side');
          eq(rowsOf(root), [], 'side has none here');
          ok(root.textContent.includes(words.f('sess.projectEmpty', 'side', 'mba')) && root.textContent.includes(words.f('sess.projectEmptyWhy', 'side', 'mba')), 'and says why');
          await pickProject(root, words.t('sess.projectAll'));
          eq(a.router.route.value, {page: 'machines', sessions: 'mba'}, 'all again');
          eq(root.find('.sess-share').length, 0, 'a machine sharing all says nothing of it');
          eq(labelsOf(root), {'Draft the release notes': 't1 Refund emails', 'Fix the checkout total': 't2 Cart totals'}, 'the tasks whose runs used them');
          styled(root, `${f}/${lang} list`);
          await type(root.one('input'), 'shop fix');
          eq(rowsOf(root), ['Fix the checkout total'], 'every word of the filter');
          await type(root.one('input'), 'codex');
          eq(rowsOf(root), ['Port the importer'], 'by agent');
          await type(root.one('input'), 'cart totals');
          eq(rowsOf(root), ['Fix the checkout total'], 'by its task\'s title');
          await type(root.one('input'), 'T1');
          eq(rowsOf(root), ['Draft the release notes'], 'by its task\'s id');
          await type(root.one('input'), '#ops');
          eq(rowsOf(root), [BACKUP], 'by a tag, its # left out');
          await type(root.one('input'), 'perf');
          eq(rowsOf(root), ['Port the importer'], 'by a tag the row folds away');
          await type(root.one('input'), 'install section');
          eq(rowsOf(root), ['Tidy the README'], 'by its summary');
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
          eq(root.one('.sess-conv-task').textContent, 't2 Cart totals', 'its task over the conversation');
          eq([root.find('.sess-conv-meta').length, root.find('.sess-conv-sum').length], [0, 0], 'no favorite, tags or summary to tell');
          eq(root.one('.sess-conv-proj').textContent, words.f('sess.projectVia', 'Shop', '/Users/ann/dev/shop'), 'its project and the directory that puts it there');
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
  seenBy(r, bo);
  let a, root;
  await r.srv.play('sessions-share', {
    async mount() {
      a = app(r, {url: '/?page=machines&sessions=bo-laptop&session=claude:c-run&fav=1', session: bo, wire: {...r.wire, has: m => m !== 'sessions.list' && r.wire.has(m)}});
      root = await mount(a.vnode(), 'desktop');
    },
    async listed() {
      await act(() => settle());
      eq(rowsOf(root), [], 'favorites only, from the address');
      ok(root.one('.sess-list').textContent.includes(words.f('sess.favNone', 'bo-laptop')), 'none favorited');
      ok(root.textContent.includes(words.f('sess.favSummary', 0, 1)), 'none of one');
      await click(buttonOf(root, words.t('sess.showAll')));
      eq(a.router.route.value, {page: 'machines', sessions: 'bo-laptop', session: 'claude:c-run'}, 'show all turns the switch off');
      eq(rowsOf(root), ['Add the search box'], 'listed though its tend is too old for live');
      eq([root.find('.sess-pfield').length, root.find('.sess-proj').length], [0, 0], 'a server without sessions.list reads the node itself and has no projects');
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
      ok(root.find('.sess-owner').length === 0 && root.find('.secret').length === 1, 'his own: no read-only line, the resume line');
    },
    async shared() {
      a = app(r, {url: '/?page=machines&sessions=bo-laptop&session=claude:c-run'});
      root = await mount(a.vnode(), 'desktop');
    },
    async sharedListed() { await until(() => r.srv.current().sent.length > 0, 'the newest page asked'); },
    async readonly() {
      await act(() => settle());
      const t = words.t;
      eq(root.one('.sess-owner').textContent, words.f('sess.sharedBy', 'Bo Lin', 'bo-laptop') + t('sess.sharedNote'), 'whose, shared with Ann, read only');
      eq(root.one('.sess-share').textContent, words.f('sess.shareRunsShared', 'bo-laptop', 'Bo Lin'), 'the node\'s own setting, only its owner changes it');
      eq(marksOf(root)['Add the search box'], {star: true, tags: ['#docs', '#search'], sum: 'Search box on the docs site, with tests; the index rebuild is still slow.', archived: false}, 'favorite, tags and summary as for the owner');
      eq(msgsOf(root), ['Add the search box.', 'The search box is in, with tests.'], 'the conversation');
      eq([root.find('.secret').length, root.one('.sess-readonly').textContent], [0, t('sess.readonly')], 'no resume line, a word why');
      styled(root, 'shared');
    },
    async revoked() {
      await act(() => settle());
      const gone = root.one('.sess-gone');
      eq(gone.find('p').map(x => x.textContent), [words.f('sess.cannot', 'bo-laptop'), words.f('sess.cannotWhy', 'Bo Lin')], 'taken back while read');
      eq([root.find('.sess-msg').length, root.find('.tr').length], [0, 0], 'what was on the screen goes with it');
      await click(buttonOf(gone, words.t('sess.backToMachines')));
      eq(a.router.route.value, {page: 'machines'}, 'back to the machines');
    },
  });
  eq(r.errors, [], 'errors');
});

test('a shared machine\'s conversation on a phone says read only under its title', async () => {
  const r = await team();
  try {
    const a = app(r, {url: '/?page=machines&sessions=bo-laptop&session=claude:c-fix', wire: listOnly(r)});
    const root = await mount(a.vnode(), 'phone');
    await act(() => settle());
    eq(root.find('.sess-owner').map(x => x.textContent), [words.f('sess.sharedBy', 'Bo Lin', 'bo-laptop') + words.t('sess.sharedNote'), words.f('sess.readonlyShort', 'Bo Lin', 'bo-laptop')], 'the page and the conversation');
    styled(root, 'phone shared');
  } finally { form.value = 'desktop'; }
});

// listOnly answers a sessions page from the sessions frames' list and live, and never a conversation.
function listOnly(r) {
  const frames = readFileSync(new URL('./frames/sessions.jsonl', import.meta.url), 'utf8').split('\n').filter(Boolean).map(l => JSON.parse(l));
  const answer = id => frames.find(x => x.s?.id === id).s.result;
  return {...r.wire, call: (method, p) => (method === 'sessions.list' ? Promise.resolve(answer(5)) : method !== 'node.call' ? r.wire.call(method, p)
    : new Promise(() => {}))};
}

for (const f of ['desktop', 'phone']) {
  test(`a favorite's conversation on a ${f} is headed by its star, when it was favorited, every tag and the whole summary`, async () => {
    const r = await team();
    try {
      const a = app(r, {url: '/?page=machines&sessions=mba&session=codex:x-port&fav=1', wire: listOnly(r)});
      const root = await mount(a.vnode(), f);
      await act(() => settle());
      eq(favSwitch(root).getAttribute('aria-pressed'), 'true', 'favorites only, from the address');
      eq(rowsOf(root).length, 4, 'the favorites');
      const meta = root.one('.sess-conv-meta');
      eq(meta.find('.sess-star').length, 1, 'starred');
      ok(meta.textContent.includes(words.f('sess.favoritedAt', '9-29')), 'when');
      eq(meta.find('.sess-tag').map(x => x.textContent), ['#importer', '#csv', '#perf'], 'every tag');
      eq(root.one('.sess-conv-sum').textContent, 'CSV 导入改成流式读取，内存从 1.2 GB 降到 80 MB；Windows 换行还没测。', 'the whole summary');
      styled(root, `${f} favorite`);
      if (f === 'phone') await act(() => a.router.back({page: 'machines', sessions: 'mba', fav: true}));
      await act(() => a.router.go({page: 'machines', sessions: 'mba', session: 'claude:c-docs', fav: true}, {replace: true}));
      eq(root.one('.sess-conv-meta').find('.chip').map(x => x.textContent), ['#docs', words.t('sess.archived')], 'an archived one says so over its conversation');
      eq(root.find('.sess-conv-meta').length, 1, 'one conversation');
    } finally { form.value = 'desktop'; }
  });
}

// many answers a sessions page with n sessions, every other one with a summary and tags, none running.
function many(r, n) {
  const sessions = Array.from({length: n}, (_, i) => ({provider: 'claude', session_id: `s${i}`, title: `Session ${i}`, cwd: '/w',
    last_at: new Date(NOW - i * 60000).toISOString(), ...(i % 2 ? {summary: `Summary ${i}`, tags: ['ops', 'db', 'x']} : {})}));
  return {...r.wire, call: (method, p) => (method === 'sessions.list' ? Promise.resolve({sessions, live: {}, projects: {}}) : method !== 'node.call' ? r.wire.call(method, p)
    : new Promise(() => {}))};
}
const press = k => ({key: k, target: globalThis.document?.body, preventDefault() {}});

test(`over ${VIRTUAL_ABOVE} sessions the list is drawn in windows of the page's own row height, and j keeps the last in view`, async () => {
  const r = await team();
  const n = VIRTUAL_ABOVE + 50;
  try {
    for (const [sel, h] of [['.sess-list .tr', ROW.desktop], ['.sess-phone .card-row', ROW.phone - 1]]) {
      ok(new RegExp(`${sel.replace(/\./g, '\\.')} \\{[^}]*\\bheight: ${h}px`).test(css), `${sel} is ${h}px tall in the CSS`);
    }
    const a = app(r, {wire: many(r, n)});
    const root = await mount(a.vnode(), 'desktop');
    await act(() => settle());
    const top = windowOf({count: n, rowHeight: ROW.desktop, top: 0, height: 480});
    eq(root.find('.tr').length, top.end - top.start, 'the window at the top');
    eq(root.find('.spacer').map(x => x.style.height), [top.after + 'px'], 'the space below, by the sessions\' row height');
    for (let i = 0; i < n; i++) await act(() => { a.keys.handle(press('j')); });
    eq(a.router.route.value.session, `claude:s${n - 1}`, 'the last selected');
    const body = root.one('.tbody');
    eq(body.scrollTop, n * ROW.desktop - 480, 'scrolled so the last row shows at the bottom');
    const end = windowOf({count: n, rowHeight: ROW.desktop, top: body.scrollTop, height: 480});
    eq(root.find('.spacer').map(x => x.style.height), [end.before + 'px'], 'the space above');
    eq(root.find('.tr').at(-1).getAttribute('aria-selected'), 'true', 'the last row is drawn, selected');
    const phone = await mount(app(r, {wire: many(r, n)}).vnode(), 'phone');
    await act(() => settle());
    const card = windowOf({count: n, rowHeight: ROW.phone, top: 0, height: 480});
    eq([phone.find('.card-row').length, phone.find('.spacer').map(x => x.style.height)], [card.end - card.start, [card.after + 'px']], 'a phone\'s cards by theirs');
  } finally { form.value = 'desktop'; }
});

test('the machines page opens a machine\'s sessions for whom the coordinator says reads them', async () => {
  const r = await team();
  const a = app(r, {url: '/?page=machines'});
  const root = await mount(a.vnode(), 'desktop');
  await click(root.find('.mach-card').find(c => c.one('b').textContent === 'mba'));
  ok(root.one('.mach-aside').textContent.includes(words.t('mach.share.all')), 'what it shares');
  await click(buttonOf(root.one('.mach-aside'), words.t('mach.sessions')));
  eq(a.router.route.value, {page: 'machines', sessions: 'mba'}, 'its owner opens them');
  seenBy(r, bo);
  const b = app(r, {url: '/?page=machines', session: bo});
  const other = await mount(b.vnode(), 'desktop');
  await click(other.find('.mach-card').find(c => c.one('b').textContent === 'mba'));
  eq(other.one('.mach-aside').find('button').filter(x => labelOf(x) === words.t('mach.sessions')).length, 0, 'a machine shared with Bo is not his to read');
  await click(other.find('.mach-card').find(c => c.one('b').textContent === 'bo-laptop'));
  ok(other.one('.mach-aside').textContent.includes(words.t('mach.share.runs')), 'his shares only its runs');
  ok(buttonOf(other.one('.mach-aside'), words.t('mach.sessions')), 'his own he reads');
  const c = app(r, {url: '/?page=machines&sessions=mba', session: bo});
  const refused = await mount(c.vnode(), 'desktop');
  ok(refused.textContent.includes(words.f('sess.cannot', 'mba')), 'the address of another\'s machine reads nothing');
  seenBy(r, admin);
  try {
    const phone = await mount(app(r, {url: '/?page=machines'}).vnode(), 'phone');
    await click(phone.find('.card-row').find(x => x.textContent.includes('linux')));
    ok(buttonOf(phone, words.t('mach.sessions')), 'a phone opens them from a machine\'s facts');
  } finally { form.value = 'desktop'; }
});

// homeRows are the phone home's machine rows: each machine's name, whose it is when another's, and the word of its state.
const homeRows = root => root.find('.home-sess-list').flatMap(l => l.find('button'))
  .map(b => [b.one('.home-sess-name').textContent, b.find('.home-sess-who').map(x => x.textContent).join(''), b.one('.status-word').textContent]);

for (const lang of ['zh', 'en']) {
  test(`the phone home in ${lang} has a row per machine whose sessions the viewer opens, leading to them`, async () => {
    const r = await team();
    words.lang.value = lang;
    try {
      const a = app(r, {url: '/'});
      const root = await mount(a.vnode(), 'phone');
      styled(root, `home/${lang}`);
      eq(root.one('.home-sess-head').textContent, words.t('home.sessions'), 'headed');
      eq(homeRows(root), [['bo-laptop', words.f('home.sessShared', 'Bo Lin'), words.t('status.online')], ['linux', '', words.t('status.online')],
        ['mba', '', words.t('status.online')], ['win', '', words.t('status.offline')]], 'Ann\'s: her own and the one Bo shares with her, by name, with their state');
      await click(root.one('.home-sess-list').find('button')[2]);
      eq(a.router.route.value, {page: 'machines', sessions: 'mba'}, 'a row opens its sessions');
      eq([root.find('.sess-phone').length, root.find('.home-sess').length], [1, 0], 'the sessions page in its place');
      seenBy(r, bo);
      const b = await mount(app(r, {url: '/', session: bo}).vnode(), 'phone');
      eq(homeRows(b), [['bo-laptop', '', words.t('status.online')]], 'Bo\'s: only Bo\'s own, no other shared with Bo');
      r.store.machines.value = r.store.machines.value.map(m => ({...m, sessions: m.owner === 'u_b' || m.name === 'win'}));
      const c = await mount(app(r, {url: '/', session: bo}).vnode(), 'phone');
      eq(homeRows(c), [['bo-laptop', '', words.t('status.online')], ['win', words.f('home.sessShared', 'Ann Lee'), words.t('status.offline')]], 'and once Ann shares win with him');
    } finally { form.value = 'desktop'; words.lang.value = 'zh'; }
  });
}

test('the sessions page goes back where it came from: a phone to its home, a desktop past the session it picked, an address to the machines', async () => {
  const r = await team();
  try {
    let d, desk;
    await r.srv.play('sessions', {
      async mount() {
        d = app(r, {url: '/?page=machines'});
        desk = await mount(d.vnode(), 'desktop');
        await click(desk.find('.mach-card').find(c => c.one('b').textContent === 'mba'));
        await click(buttonOf(desk.one('.mach-aside'), words.t('mach.sessions')));
      },
      async listed() { await act(() => settle()); },
      async open() { await click(desk.find('.tr')[1]); },
      async opened() {
        eq(d.router.route.value, {page: 'machines', sessions: 'mba', session: 'claude:c-fix'}, 'one picked');
        eq(d.history.length, 2, 'picking a session on a desktop replaces the address');
      },
      async earlier() { await click(buttonOf(desk, words.t('sess.earlier'))); },
      async older() {},
      async full() { await click(buttonOf(desk, words.t('sess.full'))); },
      async fulled() {
        await click(buttonOf(desk, words.t('ui.back')));
        eq(d.router.route.value, {page: 'machines'}, 'back past the session picked');
      },
    });
    eq(r.errors, [], 'errors');
    const a = app(r, {url: '/'});
    const root = await mount(a.vnode(), 'phone');
    await click(root.one('.home-sess-list').find('button')[2]);
    eq(a.router.route.value, {page: 'machines', sessions: 'mba'}, 'the list');
    await click(buttonOf(root, words.t('ui.back')));
    eq(a.router.route.value, {page: 'home'}, 'its back goes to the home it came from');
    eq(root.find('.home-sess').length, 1, 'the home again');
    const e = app(r);
    const typed = await mount(e.vnode(), 'desktop');
    await click(buttonOf(typed, words.t('ui.back')));
    eq(e.router.route.value, {page: 'machines'}, 'an address typed in goes back to the machines');
  } finally { form.value = 'desktop'; }
});

for (const f of ['desktop', 'phone']) {
  test(`a session's task label on a ${f} opens the task`, async () => {
    const r = await team();
    let a, root;
    try {
      await r.srv.play('sessions', {
        async mount() { a = app(r); root = await mount(a.vnode(), f); },
        async listed() {
          await act(() => settle());
          if (f === 'phone') eq(root.find('.card-row').flatMap(c => c.find('button')).length, 0, 'a card is one button: its label is text');
          else eq(root.find('.tr')[1].one('.sess-task').localName, 'button', 'a row\'s label is a button');
        },
        async open() { await click(f === 'phone' ? root.find('.card-row')[1] : root.find('.tr')[1]); },
        async opened() {
          await act(() => settle());
          eq(root.one('.sess-conv-task').one('button').getAttribute('aria-label'), words.f('sess.openTask', 't2'), 'named for what it does');
        },
        async earlier() { await click(buttonOf(root, words.t('sess.earlier'))); },
        async older() {},
        async full() { await click(buttonOf(root, words.t('sess.full'))); },
        async fulled() { await act(() => settle()); },
      });
      eq(r.errors, [], 'errors');
      await click(f === 'phone' ? root.one('.sess-conv-task').one('button') : root.find('.tr')[1].one('.sess-task'));
      eq(a.router.route.value, {page: 'tasks', view: 'list', task: 't2'}, f === 'phone' ? 'the conversation\'s label opens it' : 'the row\'s label opens it, not the row');
      ok(root.find('.sess').length === 0 && root.textContent.includes('Cart totals'), 'the task page in its place');
    } finally { form.value = 'desktop'; }
  });
}

test('the phone home has no machine rows for a viewer who reads none, nor without node.call; the desktop has none', async () => {
  const r = await team();
  try {
    const cy = {id: 'u_c', name: 'Cy', role: 'member'};
    seenBy(r, cy);
    const carol = await mount(app(r, {url: '/', session: cy}).vnode(), 'phone');
    eq([carol.find('.home-going').length, carol.find('.home-sess').length], [1, 0], 'a member owning no machine');
    seenBy(r, admin);
    const wire = {...r.wire, has: m => m !== 'node.call' && r.wire.has(m)};
    const old = await mount(app(r, {url: '/', wire}).vnode(), 'phone');
    eq([old.find('.home-going').length, old.find('.home-sess').length], [1, 0], 'a server without node.call');
    const desk = await mount(app(r, {url: '/'}).vnode(), 'desktop');
    eq(desk.find('.home-sess').length, 0, 'the desktop');
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

test('the favorites-only switch keeps the favorites, archived ones too; the filter finds tags with or without #', () => {
  const rows = [{key: 'a', title: 'One', favorited_at: '2026-09-30T10:00:00Z', tags: ['ops', 'db']}, {key: 'b', title: 'Two', tags: ['ops']},
    {key: 'c', title: 'Three', favorited_at: '2026-09-29T10:00:00Z', archived_at: '2026-09-30T08:00:00Z'}, {key: 'd', title: 'Four', favorited_at: ''}];
  eq(ss.favorites(rows).map(r => r.key), ['a', 'c'], 'favorited, archived or not');
  eq(ss.matches(rows, 'OPS').map(r => r.key), ['a', 'b'], 'a tag, any case');
  eq(ss.matches(rows, '#db one').map(r => r.key), ['a'], 'a #tag and a word');
  eq(ss.matches(rows, '#').map(r => r.key), ['a', 'b', 'c', 'd'], 'a lone # is no word');
  eq(ss.tagsShown(['a', 'b', 'c', 'd']), {shown: ['a', 'b'], more: 2}, 'two tags, the rest counted');
  eq(ss.tagsShown(undefined), {shown: [], more: 0}, 'none');
  const placed = ss.rowsOf({sessions: [{provider: 'claude', session_id: 'a'}, {provider: 'codex', session_id: 'b'}]}, {c: {Agent: 'claude'}}, {'claude:a': 'p1', 'claude:c': 'p2'});
  eq(placed.map(r => [r.key, r.project]), [['claude:a', 'p1'], ['codex:b', ''], ['claude:c', 'p2']].sort(), 'each row\'s project by provider:id, a running one too');
  eq(['', 'p1', 'none', 'p9'].map(p => ss.inProject(placed, p).map(r => r.key).sort()), [['claude:a', 'claude:c', 'codex:b'], ['claude:a'], ['codex:b'], []], 'all, one project, none, one not seen');
  for (const [url, route] of [['?page=machines&sessions=mba&fav=1', {page: 'machines', sessions: 'mba', fav: true}],
    ['?page=machines&sessions=mba&session=claude%3Ac-1&fav=1', {page: 'machines', sessions: 'mba', session: 'claude:c-1', fav: true}],
    ['?page=machines&sessions=mba&project=none&fav=1', {page: 'machines', sessions: 'mba', project: 'none', fav: true}],
    ['?page=machines&sessions=mba&project=p1', {page: 'machines', sessions: 'mba', project: 'p1'}],
    ['?page=machines&sessions=mba', {page: 'machines', sessions: 'mba'}]]) {
    eq(parse(url), route, `${url} read`);
    eq(format(route), url, `${url} written`);
  }
  eq(parse('?page=machines&fav=1'), {page: 'machines'}, 'only with a machine\'s sessions');
});

test('the list is read once the connection is open, through sessions.list where the server has it', async () => {
  const status = signal('connecting');
  let known = new Set();
  const asked = [];
  const wire = {status, has: m => known.has(m), call: (method, p) => {
    asked.push(method === 'node.call' ? p.method : method);
    return Promise.resolve(method === 'sessions.list' ? {sessions: [{provider: 'claude', session_id: 'a'}], live: {}, projects: {'claude:a': 'p1'}}
      : p.method === 'list' ? {sessions: [{provider: 'claude', session_id: 'a'}]} : {live: {}});
  }};
  const read = ss.readList(wire, 'mba');
  await Promise.resolve();
  eq(asked, [], 'nothing asked while connecting');
  known = new Set(['sessions.list']);
  status.value = 'open';
  eq(await read, {rows: [{provider: 'claude', session_id: 'a', key: 'claude:a', live: null, project: 'p1'}], byProject: true}, 'its projects');
  eq(asked, ['sessions.list'], 'one call');
  known = new Set();
  eq((await ss.readList(wire, 'mba')).byProject, false, 'an older server: no projects');
  eq(asked.slice(1).sort(), ['list', 'live'], 'read of the node');
});

test('a session links to the task of the newest run that used it, and is found by it', () => {
  const state = {
    tasks: {old: {id: 'old', title: 'Old try'}, neu: {id: 'neu', title: 'New try', stage: 'review'}, many: {id: 'many', title: 'Many runs'}, none: {id: 'none', title: 'No session'}},
    runs: {
      r1: {id: 'r1', task: 'old', session: 's1', seq: 1, queued_at: '2026-09-30T08:00:00Z'},
      r2: {id: 'r2', task: 'neu', session: 's1', seq: 5, queued_at: '2026-09-30T09:00:00Z'},
      r3: {id: 'r3', task: 'old', seq: 6, queued_at: '2026-09-30T10:00:00Z'},
      r4: {id: 'r4', task: 'many', session: 's2', seq: 2}, r5: {id: 'r5', task: 'many', session: 's2', seq: 3},
      r6: {id: 'r6', task: 'none', seq: 4}, r7: {id: 'r7', task: 'gone', session: 's3', seq: 7},
    },
  };
  const ls = ss.links(state);
  eq(ls, {s1: {id: 'neu', title: 'New try', stage: 'review'}, s2: {id: 'many', title: 'Many runs', stage: ''}},
    'the newest run decides; runs without a session and a task not held link nothing');
  eq(ss.links({tasks: {}, runs: {}}), {}, 'no runs');
  const rows = ss.linked([{key: 'claude:s1', session_id: 's1', title: 'One'}, {key: 'claude:s9', session_id: 's9', title: 'Nine'}], ls);
  eq(rows.map(r => r.task?.id || null), ['neu', null], 'a session no run used has no task');
  eq(ss.matches(rows, 'new TRY').map(r => r.key), ['claude:s1'], 'by the task\'s title');
  eq(ss.matches(rows, 'neu one').map(r => r.key), ['claude:s1'], 'by its id and the session\'s own words together');
  eq(ss.matches(rows, 'review').map(r => r.key), [], 'not by the stage');
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
