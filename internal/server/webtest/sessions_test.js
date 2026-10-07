// sessions_test draws the sessions page (?page=sessions) in both forms and both languages from the sessions frames over
// the team frames, and drives it in a fake document: the page the coordinator's own frame answers (machines that did
// not answer, shared and outdated ones read only, owners named once people.names answers, the next page near the
// end); the viewer's own sessions changed from keys and from buttons (at once, then as the machine answers, undo, a row
// that no longer fits staying dimmed, an edit that met a change made elsewhere), a shared machine's conversation read
// only, a records_rev push and a reset (what she may see changed) reading the list again, a task made from a session,
// a query's tokens drawn as chips; message search with its marks, a hit opened at its place and the way between hits,
// a hit made a task; the side menu's, the machines page's and the phone home's ways in, old addresses, and where back
// goes; delete (only where the machine has a trash, confirmed, undone by restore, refused while running) and the trash
// view (dated by deletion, restored without asking).
// core/sessions.js is tested on its own. Its last case hands the resume lines to the Go test, which types them with
// internal/shell.
process.env.TZ = 'UTC';
import {readFileSync} from 'node:fs';
import {render, options} from '../web/vendor/preact.mjs';
import {signal} from '../web/vendor/signals-core.mjs';
import {act} from './vendor/test-utils.mjs';
import {createKeys} from '../web/core/keys.js';
import {createRouter} from '../web/core/router.js';
import {form, createNav} from '../web/core/layout.js';
import {createToasts} from '../web/core/toasts.js';
import {createCommands} from '../web/core/commands.js';
import {createPrefs} from '../web/core/prefs.js';
import {words} from '../web/core/i18n.js';
import {duration} from '../web/core/format.js';
import {html, KeysContext} from '../web/ui/base.js';
import {App} from '../web/pages/app.js';
import * as ss from '../web/core/sessions.js';
import {when} from '../web/pages/sessionview.js';
import {install} from './dom.js';
import {settle} from './fake.js';
import {NOW, team} from './rig.js';
import {test, eq, ok, run} from './check.js';

// ⚠️ Effects that follow an answer or a push run after the next paint, which Preact waits for up to 35 ms of real time
// outside act; the frames go on without real time passing, so here they run once the render the answer caused is done.
options.requestAnimationFrame = fn => queueMicrotask(fn);

const css = ['base.css', 'components.css', 'pages.css'].map(f => readFileSync(new URL(`../web/css/${f}`, import.meta.url), 'utf8')).join('\n');
const cssClasses = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map(m => m[1]));
const wordsLeft = s => s.match(/\b(sess|mach|secret|api|app|ui|status|home|act|nav|picker)\.[a-zA-Z_]+\b/g);

const admin = {id: 'u_a', name: 'Ann Lee', role: 'admin'};
const ann = {id: 'u_ann', name: 'ann', role: 'member'};
const noStore = {getItem: () => null, setItem() {}};
const known = () => signal({u_a: 'Ann Lee', u_b: 'Bo Lin'});

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

// app is the signed-in page at url; rows is a page of the sessions list; copied keeps what the page put on the
// clipboard.
function app(r, {url = '/?page=sessions', session = admin, names = known(), rows} = {}) {
  const keys = createKeys({timers: r.clk});
  const toasts = createToasts({timers: r.clk});
  const commands = createCommands({wire: r.wire, newID: () => 'c1'});
  const {location, history} = fakeHistory(url);
  const router = createRouter({location, history});
  history.popped = () => router.popped();
  const copied = [];
  const http = {machineCreds: () => Promise.resolve([])};
  const props = {store: r.store, commands, toasts, wire: r.wire, http, router, keys, nav: createNav({storage: noStore, width: 1440}),
    prefs: createPrefs({storage: noStore, asked: 'zh'}), session, names, clock: () => NOW, fetchOutput: () => Promise.resolve({events: []}),
    storage: noStore, copy: text => { copied.push(text); return Promise.resolve(); }, onLogout() {}, timers: r.clk, sessionRows: rows};
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
const key = (a, k, more = {}) => act(() => { a.keys.handle({key: k, target: globalThis.document?.body, preventDefault() {}, ...more}); });
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
const classesOf = el => el.className.split(' ');
// valueOf is an input's value: Preact writes it as an attribute until something has set the property.
const valueOf = el => ('value' in el ? el.value : el.getAttribute('value'));
// rows are what is listed: a desktop's table rows or a phone's cards; titles their titles.
const rowsOf = root => [...root.find('.tr'), ...root.find('.card-row')];
const titlesOf = root => root.find('.sess-titled').map(x => x.find('.ell')[0].textContent);
const starredOf = root => rowsOf(root).map(x => x.find('.sess-star').length === 1);
const linesOf = root => root.find('.sv-mline').map(l => l.one('.msg').textContent);
const msgsOf = root => root.find('.sess-msg').map(m => m.one('.sess-text').textContent);
const toastsOf = root => root.find('.toast-text').map(x => x.textContent);
const markedOf = el => el.find('mark').map(x => x.textContent);
// overOf is the topmost of what covers the page: a phone's full page or sheet, a desktop's dialog.
const overOf = root => root.all(e => ['modal', 'sheet', 'page-over'].some(c => classesOf(e).includes(c))).at(-1);
// back leaves a phone's full page as its back button does.
const back = root => click(root.find('.page-over').at(-1).one('.back'));
const optionOf = (root, label) => root.find('[role=option]').find(o => o.one('.pk-label').textContent === label);
const LONG = 'The notes group the merged PRs by area. '.repeat(20);

test('core/sessions: the tokens Go read are dropped, replaced and read back whole, never parsed', () => {
  const tok = (kind, text, value) => ({kind, text, value});
  const toks = [tok('tag', '#docs', 'docs'), tok('project', 'p:shop', 'shop'), tok('word', 'notes', 'notes'), tok('unknown', 'owner:bo', 'owner:bo'),
    tok('status', 'status:done', 'done'), tok('tag', '#Ops', 'ops')];
  const q = '#docs p:shop notes  owner:bo status:done #Ops';
  eq(ss.chosen(toks), {tags: ['docs', 'ops'], unknown: ['owner:bo'], project: toks[1], status: toks[4]}, 'what they pick');
  eq(ss.replaced(q, toks, [ss.kind.project], ['project:none']), '#docs notes owner:bo status:done #Ops project:none', 'a kind replaced, as Go spelled it');
  eq(ss.replaced(q, toks, [ss.kind.tag]), 'p:shop notes owner:bo status:done', 'every tag dropped');
  eq(ss.replaced('> total #docs', [toks[0]], [ss.kind.tag], ['#ops']), '> total #ops', 'a message search keeps its >');
  eq(ss.dropped('notes notes #docs', toks[2]), 'notes #docs', 'one token, not its twin');
  eq([ss.stateOf(toks), ss.stateOf([]), ss.stateOf([], true)], ['done', 'open', 'all'], 'the state: status:, else open, else all for a message search');
  eq([ss.stateToken('open'), ss.stateToken('archived')], [[], ['status:archived']], 'open is written by no token');
  eq(ss.times(NOW).map(x => x.token), ['last:2026-09-30', 'last:7d', 'last:30d', 'last:2026-01-01'], 'today and this year as local dates');
  eq([ss.searching('> x'), ss.searching(' 》x'), ss.searching('x > y'), ss.searchText(' >  chekout total')], [true, true, false, 'chekout total'], '> or 》 first');
  eq([ss.refOf('mba/claude:c-1:x'), ss.refOf('nope'), ss.refOf('/claude:x'), ss.keyOf({machine: 'mba', provider: 'codex', session_id: 'x'})],
    [{machine: 'mba', provider: 'claude', session_id: 'c-1:x'}, null, null, 'mba/codex:x'], 'a row in the address');
});

test('core/sessions: a switch\'s patch, its undo and the row until its machine answers, as tend.Patch has them', () => {
  const at = '2026-09-30T11:00:00Z', iso = new Date(NOW).toISOString();
  const row = {machine: 'mba', writable: true, task: {id: 't1', title: 'T'}, make: {agents: ['claude']}, project_id: 'p1', provider: 'claude', session_id: 'c',
    title: 'T', favorited_at: at, tags: ['a'], status: 'doing'};
  const plain = {provider: 'claude', session_id: 'c'};
  eq([ss.toggles.favorite(row), ss.toggles.favorite(plain), ss.toggles.archive(row), ss.toggles.done(row), ss.toggles.done({...row, status: 'done'})],
    [{favorite: false}, {favorite: true}, {archived: true}, {status: 'done'}, {status: 'doing'}], 'set, not toggle; done goes back to doing');
  eq(ss.applied(row, {favorite: false}, NOW).favorited_at, undefined, 'unfavorited at once');
  eq([ss.applied(plain, {favorite: true}, NOW).favorited_at, ss.applied(plain, {favorite: true, favorited_at: at}, NOW).favorited_at, ss.applied(row, {favorite: true}, NOW).favorited_at],
    [iso, at, at], 'favorited now, at the time an undo gives, or since it was');
  eq([ss.applied(plain, {archived: true}, NOW).archived_at, ss.applied({...row, archived_at: at}, {archived: false}, NOW).archived_at], [iso, undefined], 'archived');
  const edited = ss.applied(row, {title: '  New  ', tags: ['#B', 'b', ' ', 'A'], summary: ' '}, NOW);
  eq([edited.title, edited.tags, 'summary' in edited], ['New', ['b', 'a'], false], 'trimmed, tags normalized, a blank left out');
  eq(ss.undoOf(row, {favorite: false}), {favorite: true, favorited_at: at}, 'a favorite back from when it was');
  eq(ss.undoOf(row, {archived: true, status: 'done', title: 'x', tags: [], summary: 's'}), {archived: false, status: 'doing', title: 'T', tags: ['a'], summary: ''}, 'each field it set');
  const got = {provider: 'claude', session_id: 'c', title: 'T2', updated_at: at};
  eq(ss.merged(row, got), {...got, machine: 'mba', writable: true, task: row.task, make: row.make, project_id: 'p1'}, 'the answer, with the coordinator\'s parts and the project kept');
  const tk = (kind, value) => ({kind, text: kind + ':' + value, value});
  const arch = {...row, archived_at: at};
  eq([ss.fits(row, [], true), ss.fits(arch, [], true), ss.fits(arch, [tk('status', 'archived')], true), ss.fits(row, [tk('status', 'archived')], true),
    ss.fits({...row, status: 'done'}, [tk('status', 'active')], true), ss.fits(plain, [], false), ss.fits(row, [tk('tag', 'b')], true), ss.fits(arch, [tk('status', 'all')], true)],
  [true, false, true, false, false, false, false, true], 'whether a row changed in place still fits the query');
});

test('core/sessions: what may be deleted and restored, and how many days a deleted session has left', () => {
  const mba = {name: 'mba', trash: true, trash_days: 30}, mini = {name: 'mini', writable: true};
  const mine = {machine: 'mba', writable: true}, shared = {machine: 'mba'};
  eq([ss.deletable(mine, mba), ss.deletable(shared, mba), ss.deletable({machine: 'mini', writable: true}, mini), ss.deletable(mine, undefined)],
    [true, false, false, false], 'a row its machine\'s trash takes and the viewer may change');
  eq([ss.restorable(shared, mba), ss.restorable(shared, mini)], [true, false], 'a trashed row by its machine alone');
  const day = 864e5;
  eq([ss.purgeIn(new Date(NOW - 60e3).toISOString(), 30, NOW), ss.purgeIn(new Date(NOW - 16.2 * day).toISOString(), 30, NOW),
    ss.purgeIn(new Date(NOW - 40 * day).toISOString(), 30, NOW), ss.purgeIn(new Date(NOW + 60e3).toISOString(), 30, NOW), ss.purgeIn(new Date(NOW).toISOString(), 0, NOW)],
  [30, 14, 1, 30, 0], 'days left, at least one while it is there, never more than the trash keeps; 0 for a trash that keeps');
  eq(ss.states.at(-1), 'trash', 'the trash is the last state');
});

test('core/sessions: marks cut text at the byte spans Go gives, CJK too', () => {
  const s = '结账的 total 少了一分：checkout 在加总前';
  const span = w => { const from = Buffer.byteLength(s.slice(0, s.indexOf(w))); return [from, from + Buffer.byteLength(w)]; };
  eq(ss.marks(s, [span('checkout'), span('total')]), [['结账的 ', false], ['total', true], [' 少了一分：', false], ['checkout', true], [' 在加总前', false]], 'in order, whatever order given');
  eq(ss.marks(s, [span('结账')]), [['结账', true], ['的 total 少了一分：checkout 在加总前', false]], 'a CJK word');
  eq(ss.marks('abc', [[1, 99]]), [['a', false], ['bc', true]], 'a span past the end stops there');
  eq(ss.marks('abc', [[0, 2], [1, 3]]), [['ab', true], ['c', true]], 'overlapping spans repeat nothing');
  eq([ss.marks('abc'), ss.marks('', [[0, 1]])], [[['abc', false]], []], 'no spans, no text');
});

test('core/sessions: what it asks the coordinator and the nodes', async () => {
  const sent = [];
  const wire = {call: (m, p) => { sent.push([m, p]); return Promise.resolve({text: 'whole'}); }};
  const row = {machine: 'mba', provider: 'claude', session_id: 'c', title: 'x'};
  ss.query(wire, {q: '#a', all: true, sort: 'turns', after: {key: 'k'}, fresh: true});
  ss.query(wire, {sort: 'active'});
  ss.grep(wire, {q: '> chekout total', all: true});
  ss.put(wire, row, {favorite: true});
  ss.put(wire, row, {title: 'y'}, 'T1');
  ss.hits(wire, row, '》 total');
  ss.readPage(wire, 'mba', row);
  ss.readPage(wire, 'mba', row, {before: 1201, file: 'f', find: '> total'});
  ss.trash(wire, row);
  ss.restore(wire, row);
  eq(await ss.readText(wire, 'mba', row, 120, 'f'), 'whole', 'a message\'s full text');
  const node = (method, params) => ['node.call', {machine: 'mba', method, params: {provider: 'claude', session_id: 'c', ...params}}];
  eq(sent, [
    ['sessions.query', {q: '#a', all: true, sort: 'turns', limit: 100, after: {key: 'k'}, fresh: true}],
    ['sessions.query', {q: '', limit: 100}],
    ['sessions.grep', {q: 'chekout total', all: true}],
    node('put', {patch: {favorite: true}}), node('put', {patch: {title: 'y'}, expect: 'T1'}),
    node('hits', {q: 'total', limit: 500}),
    node('messages', {before: -1, n: 40}), node('messages', {before: 1201, n: 40, file: 'f', find: 'total'}),
    node('trash'), node('restore'), node('text', {off: 120, file: 'f'}),
  ], 'the default sort and empty parts left out, the > left out of what is searched');
});

test('core/sessions: owners\' names are asked once a connection, again after a reset, never of a server without people.names', async () => {
  const sent = [];
  const status = signal('open'), phase = signal('live');
  let has = true, fail = false;
  const wire = {status, has: () => has, call: (m, p) => {
    sent.push(p.ids);
    return fail ? Promise.reject(new Error('no')) : Promise.resolve({names: Object.fromEntries(p.ids.map(i => [i, i.toUpperCase()]))});
  }};
  const p = ss.createPeople({wire, phase});
  await p.ask(['u_b', 'u_a', 'u_b', '']);
  await p.ask(['u_a', 'u_c']);
  eq([sent, p.names.value], [[['u_a', 'u_b'], ['u_c']], {u_a: 'U_A', u_b: 'U_B', u_c: 'U_C'}], 'each once');
  status.value = 'connecting';
  eq(p.names.value, {}, 'a new connection forgets them');
  await p.ask(['u_a']);
  phase.value = 'snapshot';
  await p.ask(['u_a']);
  eq(sent.length, 4, 'asked again after each');
  fail = true;
  await p.ask(['u_d']);
  fail = false;
  await p.ask(['u_d']);
  eq(sent.slice(4), [['u_d'], ['u_d']], 'one that failed is asked again');
  has = false;
  await p.ask(['u_e']);
  eq(sent.length, 6, 'not without the method');
});

for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
  test(`every machine's sessions on a ${f} in ${lang}, as the coordinator answers: what did not answer, what is read only, owners named, the next page`, async () => {
    const r = await team();
    let a, root;
    words.lang.value = lang;
    const desk = f === 'desktop';
    try {
      await r.srv.play('sessions-query', {
        async mount() { a = app(r, {session: ann, names: signal({}), rows: 3}); root = await mount(a.vnode(), f); },
        async listed() {
          await act(() => settle());
          eq(titlesOf(root), ['Fix the checkout total', 'Roll out the cache', 'Back up the photos'], 'the first page, every machine\'s');
          ok(root.textContent.includes(words.f('sess.counts', 6, 6, 1)), 'how many, how many match and run, over the machines that answered');
          const gone = words.f('sess.ago', duration(NOW - Date.parse('2026-09-30T06:00:00Z')));
          eq(linesOf(root), [words.f('sess.m.offline', 'gone', gone), words.f('sess.m.shared', 'linux', 'u_bob'),
            desk ? words.f('sess.m.old', 'old', '—', 'old') : words.f('sess.m.oldShort', 'old'), words.f('sess.m.timeout', 'slow', 5)],
          'a line for each machine offline, shared, outdated or late, the owner by id until named');
          ok(buttonOf(root.one('.sv-machines'), words.t('sess.m.retry')), 'one late is asked again');
          eq(starredOf(root), [true, true, true], 'favorites starred');
          if (desk) {
            eq(root.find('.tr').map(x => x.one('.sv-rowact').find('button').length), [2, 0, 0], 'a star and ⋯ on her own, nothing on a shared or an outdated machine\'s');
            eq(root.find('.tr').map(x => x.one('.sv-rowact').getAttribute('title')), [null, words.f('sess.readOnlyKey', 'u_bob', 'linux'), words.f('sess.m.oldShort', 'old')], 'a lock says why');
            eq(root.find('.tr')[0].one('.sess-task').getAttribute('aria-label'), words.f('sess.openTask', 't-1'), 'a row\'s task is a button');
          } else {
            eq(root.find('.card-row').flatMap(c => c.find('button')).length, 0, 'a card is one button');
            eq(root.find('.sv-rowact').length, 0, 'switches only over a conversation');
          }
          eq(root.find('.sv-mach').map(x => classesOf(x).includes('shared')), [false, true, false], 'a shared machine says so');
          styled(root, `${f}/${lang} list`);
        },
        async names() {},
        async named() {
          await act(() => settle());
          eq(linesOf(root)[1], words.f('sess.m.shared', 'linux', 'bob'), 'the owner by name once people.names answers');
          eq(root.find('.sv-mach')[1].getAttribute('title'), words.f('sess.sharedRO', 'bob'), 'on the row too');
        },
        async more() {
          const list = root.one(desk ? '.tbody' : '.cards-view');
          Object.assign(list, {clientHeight: 480, scrollHeight: 3 * (desk ? 50 : 100), scrollTop: 0});
          await act(() => list.dispatch('scroll'));
        },
        async paged() {
          await act(() => settle());
          eq(titlesOf(root), ['Fix the checkout total', 'Roll out the cache', 'Back up the photos', 'Port the importer', 'Draft the release notes', 'Size the cache'],
            'the next page after it');
          styled(root, `${f}/${lang} paged`);
        },
      });
      eq(r.errors, [], 'errors');
      await click(desk ? root.find('.tr')[0].one('.sess-task') : root.find('.card-row')[0]);
      if (desk) eq(a.router.route.value, {page: 'tasks', view: 'list', task: 't-1'}, 'a row\'s task opens the task, not the row');
      else {
        await act(() => settle());
        await click(root.one('.sess-conv-task').one('button'));
        eq(a.router.route.value, {page: 'tasks', view: 'list', task: 't-1'}, 'on a phone the conversation\'s task does');
      }
    } finally { form.value = 'desktop'; words.lang.value = 'zh'; }
  });
}

for (const f of ['desktop', 'phone']) {
  test(`her own sessions on a ${f} change in place from keys and buttons, a shared one is read only, a push reads them again, one becomes a task, a query is chips`, async () => {
    const r = await team();
    let a, root;
    const desk = f === 'desktop';
    const conv = () => root.one('.sess-conv');
    const acts = label => buttonOf(root.one(desk ? '.sv-acts' : '.sv-phone-acts'), label);
    const linux = r.store.machines.value.find(m => m.name === 'linux')?.version || '—';
    const typed = '#docs project:p1 notes owner:bo status:all';
    try {
      await r.srv.play('sessions-share', {
        async mount() { a = app(r); root = await mount(a.vnode(), f); },
        async listed() {
          await act(() => settle());
          eq(titlesOf(root), ['Draft the release notes', 'Fix the checkout total', 'Port the importer', 'Add the search box', 'Back up the photos'], 'hers and Bo\'s');
          eq(linesOf(root), [words.f('sess.m.sharedRuns', 'bo-laptop', 'Bo Lin'),
            desk ? words.f('sess.m.old', 'linux', linux, 'linux') : words.f('sess.m.oldShort', 'linux'), words.f('sess.m.offline', 'win', words.f('sess.ago', duration(12 * 60e3)))],
          'Bo shares only his runs\' sessions; linux is outdated, win offline');
          if (desk) eq(root.find('.tr').map(x => x.one('.sv-rowact').find('button').length), [2, 2, 2, 0, 0], 'her mba\'s rows change');
          styled(root, `${f} share list`);
        },
        async open() { await click(rowsOf(root)[0]); },
        async opened() {
          await act(() => settle());
          eq(a.router.route.value, {page: 'sessions', open: 'mba/claude:c-notes'}, 'one open');
          eq(a.history.length, desk ? 1 : 2, desk ? 'a desktop picks in place' : 'a phone opens it over the list');
          eq(msgsOf(root), ['Draft the release notes.', LONG.slice(0, 600)], 'the newest page, oldest first');
          ok(conv().textContent.includes(words.f('sess.resume', 'mba')), 'her own resumes');
          eq(conv().one('.sess-conv-proj').textContent, words.f('sess.inProject', 'mba', 'Shop'), 'where it is');
        },
        async full() { await click(buttonOf(root, words.t('sess.full'))); },
        async unfavorite() {
          await act(() => settle());
          eq(msgsOf(root)[1].trim(), LONG.trim(), 'the full text');
          if (desk) await key(a, 'f');
          else await click(acts(words.t('sess.a.faved')));
        },
        async pending() {
          eq(starredOf(root)[0], false, 'the star goes at once');
          ok(classesOf(rowsOf(root)[0]).includes('sv-pending'), 'while its machine has not answered');
          eq(acts(words.t('sess.a.fav')).getAttribute('aria-pressed'), 'false', 'and from the conversation');
        },
        async unfavorited() {
          await act(() => settle());
          ok(!classesOf(rowsOf(root)[0]).includes('sv-pending'), 'answered');
          eq(toastsOf(root), [words.f('sess.toast.unfav', 'Draft the release notes')], 'said, with undo');
          if (desk) await click(buttonOf(root.one('.toasts'), words.t('ui.undo')));
          else await key(a, 'z', {metaKey: true});
        },
        async undone() {
          await act(() => settle());
          eq(starredOf(root)[0], true, 'undo favorites it again, since it was');
          await click(acts(words.t('sess.a.archive')));
        },
        async archived() {
          await act(() => settle());
          eq(titlesOf(root)[0], 'Draft the release notes', 'archived, it stays where it was');
          ok(classesOf(rowsOf(root)[0]).includes('sess-archived'), 'dimmed: the list is of unarchived ones');
          eq(acts(words.t('sess.a.archived')).getAttribute('aria-pressed'), 'true', 'the switch is on');
          if (desk) await key(a, 'D', {shiftKey: true});
          else await click(acts(words.t('sess.a.done')));
        },
        async done() {
          await act(() => settle());
          eq(acts(words.t('sess.a.doneOn')).getAttribute('aria-pressed'), 'true', 'done');
          if (desk) await key(a, 'e');
          else await click(acts(words.t('sess.a.edit')));
          const box = overOf(root);
          eq(valueOf(box.find('input')[0]), 'Draft the release notes', 'the edit dialog');
          styled(root, `${f} edit`);
          await type(box.find('input')[0], 'Draft the v1.0 release notes');
          await click(buttonOf(box, words.t('sess.edit.save')));
        },
        async stale() {
          await act(() => settle());
          const box = overOf(root);
          eq(box.one('.sv-stale').textContent, words.t('sess.edit.stale'), 'changed elsewhere: the dialog stays and says so');
          eq([valueOf(box.find('input')[0]), box.one('.sv-tagin').find('.chip').map(x => x.textContent)], ['Draft the 1.0 notes', ['#release ×', '#docs ×', '#v1 ×']],
            'with what it is now');
          await type(box.find('input')[0], 'Draft the v1.0 release notes');
          await click(buttonOf(box, words.t('sess.edit.save')));
        },
        async edited() {
          await act(() => settle());
          eq(root.find('.sv-stale').length, 0, 'saved and closed');
          eq(titlesOf(root)[0], 'Draft the v1.0 release notes', 'the answer in its place');
          ok(toastsOf(root).includes(words.f('sess.toast.edited', 'Draft the v1.0 release notes')), 'said');
          if (!desk) {
            await back(root);
            eq([a.router.route.value, a.history.length], [{page: 'sessions'}, 1], 'back closes the conversation');
          }
        },
        async shared() { await click(rowsOf(root)[3]); },
        async readonly() {
          await act(() => settle());
          eq(conv().one('.sv-ro').textContent, words.f('sess.readOnly', 'Bo Lin', 'bo-laptop'), 'a line instead of the switches');
          eq([root.find('.sv-acts').length, root.find('.sv-phone-acts').length], [0, 0], 'no switches');
          ok(!conv().textContent.includes(words.f('sess.resume', 'bo-laptop')), 'nor its resume line');
          await key(a, 'f');
          ok(toastsOf(root).includes(words.f('sess.readOnlyKey', 'Bo Lin', 'bo-laptop')), 'a key says why it does nothing');
          await click(buttonOf(root, words.t('sess.earlier')));
        },
        async reread() {
          await act(() => settle());
          ok(toastsOf(root).includes(words.t('sess.reread')), 'its file changed: read again from the newest');
          eq(msgsOf(root), ['Add the search box.', 'The search box is in, with tests.'], 'the newest');
          styled(root, `${f} shared conversation`);
          if (!desk) await back(root);
        },
        async rev() {},
        async again() {
          await act(() => settle());
          eq(titlesOf(root)[0], 'Draft the v1.0 release notes', 'read again once mba says its records changed');
          if (desk) eq(rowsOf(root)[0].one('.sess-tags').find('.chip').map(x => x.textContent), ['#release', '#docs', '+1'], 'as it is now');
        },
        async port() { await click(rowsOf(root)[2]); },
        async make() {
          await act(() => settle());
          if (desk) {
            ok(a.keys.active().some(b => b.id === 'makeTask' && !b.key), 'the palette makes it a task');
            await click(rowsOf(root)[2].one('.sv-rowact').find('button')[1]);
          } else await click(overOf(root).one('.page-head').one('.menu-wrap').one('button'));
          await click(root.find('[role=menuitem]').find(x => x.textContent === words.t('sess.make')));
          const box = overOf(root);
          eq(box.find('.t-muted')[0].textContent, words.f('sess.makeSub', 'Port the importer', 'mba', 'Codex'), 'what it goes on with');
          styled(root, `${f} make`);
          await type(box.one('textarea'), 'Add a test for Windows line endings');
          await click(box.find('.picker-btn')[1]);
          await click(optionOf(root, 'Shop'));
          await click(buttonOf(box, words.t('sess.make')));
        },
        async made() {
          await act(() => settle());
          ok(toastsOf(root).includes(words.f('sess.made', 'mba')), 'made, going on on mba');
          ok(buttonOf(root.one('.toasts'), words.t('sess.madeGo')), 'with a way to it');
          if (!desk) await back(root);
        },
        async typed() {
          await type(root.one('.sv-q').one('input'), typed);
          await act(() => r.clk.advance(250));
        },
        async chips() {
          await act(() => settle());
          eq(a.router.route.value.q, typed, 'the query in the address');
          eq(root.one('.sv-unknown').textContent, words.f('sess.unknown', 'owner:bo'), 'what Go did not understand');
          const set = root.find('.sv-chip').filter(c => classesOf(c).includes('set')).map(labelOf);
          if (desk) {
            eq(set, [words.t('sess.f.project') + 'Shop', words.t('sess.f.tag') + '#docs'], 'the chips show Go\'s tokens');
            eq(root.one('.seg').find('[role=radio]').filter(x => x.getAttribute('aria-checked') === 'true').map(x => x.textContent), [words.t('sess.st.all')], 'the state');
            await click(root.find('.sv-chip').find(c => c.one('.k').textContent === words.t('sess.f.project')));
            await click(optionOf(root, words.t('sess.f.any')));
          } else {
            eq(set, [words.t('sess.f.state') + words.t('sess.st.all'), words.t('sess.moreFilters')], 'the chips show Go\'s tokens');
            await click(buttonOf(root, words.t('sess.moreFilters')));
            await click(overOf(root).one('.sv-filters').find('.sv-opt').find(o => o.find('span')[0].textContent === words.t('sess.f.any')));
          }
        },
        async dropped() {
          await act(() => settle());
          eq(valueOf(root.one('.sv-q').one('input')), '#docs notes owner:bo status:all', 'a choice drops its token');
          eq(titlesOf(root).slice(0, 2), ['Draft the v1.0 release notes', 'Add the search box'], 'and reads again');
        },
        async revoke() {},
        async revoked() {
          await act(() => settle());
          eq(titlesOf(root), ['Draft the v1.0 release notes'], 'what she may see changed: the page asks again by itself');
          eq(linesOf(root).filter(l => l.includes('bo-laptop')), [], 'bo-laptop is gone from the lines too');
        },
      });
      eq(r.errors, [], 'errors');
    } finally { form.value = 'desktop'; }
  });
}

for (const f of ['desktop', 'phone']) {
  test(`message search on a ${f}: on Enter only, every machine, marked; a hit opens at its place with a way between hits`, async () => {
    const r = await team();
    let a, root;
    const desk = f === 'desktop';
    const nav = () => root.one('.sv-hitnav');
    const linux = r.store.machines.value.find(m => m.name === 'linux')?.version || '—';
    words.lang.value = desk ? 'zh' : 'en';
    try {
      await r.srv.play('sessions-grep', {
        async mount() { a = app(r); root = await mount(a.vnode(), f); },
        async listed() { await act(() => settle()); eq(titlesOf(root), ['Draft the release notes', 'Fix the checkout total'], 'the list first'); },
        async search() {
          const input = root.one('.sv-q').one('input');
          if (desk) await key(a, '>', {shiftKey: true});
          else await click(buttonOf(root, words.t('sess.msgSearch')));
          eq(valueOf(input), '> ', 'a message search starts');
          await type(input, '> chekout total');
          await act(() => r.clk.advance(1000));
          await act(() => input.dispatch('keydown', {key: 'Enter'}));
        },
        async found() {
          await act(() => settle());
          eq(a.router.route.value, {page: 'sessions', q: '> chekout total'}, 'in the address');
          ok(root.textContent.includes(words.f('sess.msgHead', 2, 4)), 'how many sessions and hits');
          eq(root.find('.sv-hit').map(h => h.one('.sess-titled').find('.ell')[0].textContent), ['Fix the checkout total', 'Port the importer'], 'the best first');
          eq(root.find('.sv-snip').map(markedOf), [['total', 'checkout'], ['checkout']], 'snippets marked at Go\'s spans');
          eq(linesOf(root), [desk ? words.f('sess.m.oldNoSearch', 'linux', linux, 'linux') : words.f('sess.m.oldShort', 'linux'),
            words.f('sess.m.building', 'mba', 120, 400), words.f('sess.fixes', 'checkout')], 'not searched, still building, and a spelling also searched');
          styled(root, `${f} hits`);
        },
        async open() { await click(rowsOf(root)[0]); },
        async at() {
          await act(() => settle());
          eq(root.one('.sv-at').getAttribute('data-off'), '1200', 'open at the best hit');
          eq(root.find('.sess-msg').map(markedOf), [['checkout', 'total'], ['Checkout', 'total']], 'its words marked');
          eq(nav().find('span')[0].textContent, words.f('sess.hitNav', 2, 3), 'which of its hits');
          await click(nav().find('button')[1]);
        },
        async next() {
          await act(() => settle());
          eq([root.one('.sv-at').getAttribute('data-off'), nav().find('span')[0].textContent], ['2000', words.f('sess.hitNav', 3, 3)], 'the next, read from a page ending on it');
          eq(nav().find('button')[1].disabled, true, 'the last');
          await click(nav().find('button')[0]);
        },
        async back() {
          await act(() => settle());
          eq([root.one('.sv-at').getAttribute('data-off'), nav().find('span')[0].textContent], ['1200', words.f('sess.hitNav', 2, 3)], 'one already read is marked in place');
          styled(root, `${f} hit`);
          if (!desk) await back(root);
        },
        async other() { await click(rowsOf(root)[1]); },
        async unseen() {
          await act(() => settle());
          eq(root.one('.sv-stale').textContent, words.t('sess.unseen'), 'a hit in the file before the session went on is not shown');
          eq([msgsOf(root), root.find('.sv-at').length], [['The import streams now.'], 0], 'the newest part is');
          ok(a.keys.active().some(b => b.id === 'makeTask'), 'a hit whose session the list did not load becomes a task: the hit says whether it can');
          if (desk) eq(buttonOf(root.one('.sv-acts'), words.t('sess.make')).disabled, false, 'from the conversation\'s head');
          if (!desk) await back(root);
          await click(buttonOf(root.one('.sv-machines'), words.t('sess.m.searchAgain')));
        },
        async again() {
          await act(() => settle());
          eq(linesOf(root).length, 2, 'built: no line for it');
        },
      });
      eq(r.errors, [], 'errors');
    } finally { form.value = 'desktop'; words.lang.value = 'zh'; }
  });
}

// homeRows are the phone home's session rows: the first for every machine, then one a machine.
const homeRows = root => root.one('.home-sess-list').find('button');

for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
  test(`delete and the trash on a ${f} in ${lang}: only where the machine has a trash, confirmed, undone by restore, refused while running; the trash view restores`, async () => {
    const r = await team();
    let a, root;
    words.lang.value = lang;
    const desk = f === 'desktop';
    const t = words.t, w = words.f;
    const conv = () => root.one('.sess-conv');
    const menuOf = async row => {
      if (desk) await click(rowsOf(root)[row].one('.sv-rowact').find('button')[1]);
      else await click(overOf(root).one('.page-head').one('.menu-wrap').one('button'));
      return root.find('[role=menuitem]');
    };
    const askDelete = async row => click((await menuOf(row)).find(x => labelOf(x) === t('sess.del')));
    const dialog = () => root.all(e => classesOf(e).includes('sv-del'))[0];
    const deleteKey = () => key(a, 'Backspace', {metaKey: true});
    const writes = ['favorite', 'archive', 'done', 'edit', 'makeTask'];
    const bound = () => a.keys.active().map(b => b.id);
    const port = 'Port the importer', regex = 'Scratch: regex for log lines';
    try {
      await r.srv.play('sessions-trash', {
        async mount() { a = app(r); root = await mount(a.vnode(), f); },
        async listed() {
          await act(() => settle());
          eq(titlesOf(root), ['Draft the release notes', 'Fix the checkout total', port, 'Add the search box', 'Rename the cart helpers', 'Back up the photos'], 'listed');
          ok(root.textContent.includes(w('sess.counts', 6, 6, 1)), 'counted');
          if (desk) {
            await click(rowsOf(root)[4].one('.sv-rowact').find('button')[1]);
            eq(root.find('[role=menuitem]').map(labelOf), [t('sess.a.archive'), t('sess.a.edit'), t('sess.make')], 'mini has no trash: no delete in its menu');
            await key(a, 'Escape');
          }
          styled(root, `${f}/${lang} trash list`);
        },
        async open() { await click(rowsOf(root)[2]); },
        async ask() {
          await act(() => settle());
          const items = await menuOf(2);
          eq(items.map(labelOf), desk ? [t('sess.a.archive'), t('sess.a.edit'), t('sess.make'), t('sess.del')] : [t('sess.make'), t('sess.del')], 'delete in the menu, last');
          await click(items.find(x => labelOf(x) === t('sess.del')));
          const box = overOf(root);
          eq(box.one('h2').textContent, t('sess.delTitle'), 'confirmed first');
          eq(dialog().find('p').map(x => x.textContent), [w('sess.delBody', port, 'mba'), w('sess.delNote', 30)], 'where it goes and when it is purged');
          const del = buttonOf(box, t('sess.delBtn'));
          ok(classesOf(del).includes('primary') && classesOf(del).includes('danger-fill'), 'a danger primary');
          eq(globalThis.document.activeElement, del, 'which Enter presses');
          styled(root, `${f}/${lang} delete dialog`);
          await key(a, 'Escape');
          eq(dialog(), undefined, 'Esc cancels');
          if (desk) await deleteKey();
          else await askDelete(2);
          ok(dialog(), desk ? 'the key asks too' : 'asked again');
          if (desk) await key(a, 'Enter', {metaKey: true});
          else await click(buttonOf(overOf(root), t('sess.delBtn')));
        },
        async pending() {
          eq(dialog(), undefined, 'the dialog closes');
          ok(classesOf(rowsOf(root)[2]).includes('sv-pending'), 'the row waits for its machine');
        },
        async deleted() {
          await act(() => settle());
          eq(titlesOf(root).includes(port), false, 'it leaves the list at once');
          ok(root.textContent.includes(w('sess.counts', 5, 5, 1)), 'and the counts');
          eq(a.router.route.value, {page: 'sessions'}, 'its conversation closes');
          eq(root.find('.page-over').length, 0, 'on a phone too');
          eq(toastsOf(root), [w('sess.deleted', port, 'mba')], 'said, with undo');
          if (desk) await click(buttonOf(root.one('.toasts'), t('ui.undo')));
          else await key(a, 'z', {metaKey: true});
        },
        async undone() {
          await act(() => settle());
          eq(titlesOf(root)[2], port, 'undo restores it and reads the list again');
          await click(rowsOf(root)[1]);
        },
        async fix() {
          await act(() => settle());
          if (desk) await deleteKey();
          else await askDelete(1);
          await click(buttonOf(overOf(root), t('sess.delBtn')));
        },
        async busy() {
          await act(() => settle());
          ok(toastsOf(root).includes(t('sess.delBusy')), 'a running session is not deleted');
          eq(titlesOf(root)[1], 'Fix the checkout total', 'and stays');
          if (!desk) await back(root);
          await click(rowsOf(root)[3]);
        },
        async shared() {
          await act(() => settle());
          eq((desk ? conv() : overOf(root).one('.page-head')).find('.menu-wrap').length, 0, 'Bo\'s session has no ⋯');
          await deleteKey();
          ok(toastsOf(root).includes(w('sess.readOnlyKey', 'Bo Lin', 'bo-laptop')), 'the key says why it does nothing');
          if (!desk) await back(root);
        },
        async toTrash() {
          if (desk) await click(root.one('.seg').find('[role=radio]').find(x => x.textContent === t('sess.st.trash')));
          else {
            await click(root.find('.sv-chip').find(c => c.find('.k')[0]?.textContent === t('sess.f.state')));
            await click(optionOf(root, t('sess.st.trash')));
          }
        },
        async trash() {
          await act(() => settle());
          eq(a.router.route.value.q, 'status:trash', 'the trash is status:trash');
          eq(titlesOf(root), [port, regex], 'what was deleted');
          ok(root.textContent.includes(w('sess.trashHead', 2)), 'counted');
          eq(linesOf(root), [w('sess.m.sharedRuns', 'bo-laptop', 'Bo Lin'), w('sess.m.oldTrash', 'linux'), w('sess.m.oldTrash', 'mini')], 'an outdated machine\'s trash is in its TUI');
          eq(rowsOf(root).map(x => x.textContent.includes(w('sess.deletedAt', ['14:31', '9-14'][rowsOf(root).indexOf(x)]))), [true, true], 'dated by deletion: the clock today, else the date');
          if (desk) eq(rowsOf(root).map(x => x.one('.sv-rowact').find('button').map(labelOf)), [[t('sess.restore')], [t('sess.restore')]], 'only restore at the row\'s end');
          styled(root, `${f}/${lang} trash view`);
          await click(rowsOf(root)[0]);
        },
        async trashOpen() {
          await act(() => settle());
          const bar = conv().one('.sv-trash');
          ok(bar.textContent.includes(w('sess.inTrash', 'mba', when('2026-09-30T14:31:00Z', NOW), 30)), 'in the trash, since when, purged when');
          eq([conv().find('.sv-acts').length, conv().find('.sv-phone-acts').length, conv().find('.sv-ro').length], [0, 0, 0], 'no favorite, done, archive or edit');
          ok(!conv().textContent.includes(w('sess.resume', 'mba')), 'no resume line');
          if (!desk) eq(overOf(root).one('.page-head').find('.menu-wrap').length, 0, 'no ⋯');
          eq(bound().filter(id => writes.includes(id)), [], 'nor their keys');
          ok(bound().includes('trash'), 'the delete key restores');
          styled(root, `${f}/${lang} trash conversation`);
          await click(desk ? rowsOf(root)[0].one('.sv-rowact').one('button') : buttonOf(bar, t('sess.restore')));
        },
        async restored() {
          await act(() => settle());
          ok(toastsOf(root).includes(w('sess.restored', port)), 'restored, without asking');
          eq(titlesOf(root), [regex], 'it leaves the trash');
          ok(root.textContent.includes(w('sess.trashHead', 1)), 'counted');
          eq([a.router.route.value.open, root.find('.page-over').length], [undefined, 0], 'its conversation closes');
          await click(rowsOf(root)[0]);
        },
        async regex() {
          await act(() => settle());
          ok(conv().one('.sv-trash').textContent.includes(w('sess.inTrash', 'mba', when('2026-09-14T10:00:00Z', NOW), 14)), 'purged in what is left of 30 days');
          await deleteKey();
        },
        async emptied() {
          await act(() => settle());
          const box = root.one('.sv-empty');
          eq(box.find('p').map(x => x.textContent), [t('sess.e.trash'), w('sess.e.trashSub', 30)], 'an empty trash says how long it keeps');
          ok(buttonOf(box, t('sess.e.open')), 'and leads back');
          styled(root, `${f}/${lang} empty trash`);
        },
      });
      eq(r.errors, [], 'errors');
    } finally { form.value = 'desktop'; words.lang.value = 'zh'; }
  });
}

test('the side menu, g c, the machines page and the phone home lead to the sessions page; old addresses become its query', async () => {
  const r = await team();
  try {
    const a = app(r, {url: '/'});
    const root = await mount(a.vnode(), 'desktop');
    eq(root.find('.nav-item').map(x => x.getAttribute('href')).slice(0, 3), ['/', '?page=tasks', '?page=sessions'], 'after the tasks');
    await click(root.find('.nav-item')[2]);
    eq(a.router.route.value, {page: 'sessions'}, 'the menu');
    eq(root.find('.sess').length, 1, 'the page');
    await key(a, 'g'); await key(a, 'h');
    await key(a, 'g'); await key(a, 'c');
    eq(a.router.route.value, {page: 'sessions'}, 'g c');
    const m = app(r, {url: '/?page=machines'});
    const machines = await mount(m.vnode(), 'desktop');
    await click(machines.find('.mach-card').find(c => c.one('b').textContent === 'mba'));
    await click(buttonOf(machines.one('.mach-aside'), words.t('mach.sessions')));
    eq(m.router.route.value, {page: 'sessions', q: 'host:mba'}, 'a machine\'s: the page with its host:');
    eq(valueOf(machines.one('.sv-q').one('input')), 'host:mba', 'written in the query');
    for (const [i, want] of [[0, {page: 'sessions'}], [3, {page: 'sessions', q: 'host:mba'}]]) {
      const h = app(r, {url: '/'});
      const home = await mount(h.vnode(), 'phone');
      eq(labelOf(homeRows(home)[0]).startsWith(words.t('home.sessAll')), true, 'the phone home\'s first row is every machine');
      await click(homeRows(home)[i]);
      eq(h.router.route.value, want, i ? 'then one a machine' : 'all');
      eq(h.history.length, 2, 'over the home');
      await act(() => h.history.back());
      eq(h.router.route.value, {page: 'home'}, 'back is the home');
    }
    const old = app(r, {url: '/?page=machines&sessions=mba&session=claude:c-fix&project=none&fav=1'});
    await mount(old.vnode(), 'desktop');
    eq(old.router.route.value, {page: 'sessions', q: 'host:mba project:none', fav: true, open: 'mba/claude:c-fix'}, 'an old address');
  } finally { form.value = 'desktop'; }
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
