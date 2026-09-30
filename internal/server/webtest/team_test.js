// team_test draws the machines and team pages in both forms and both languages from the team frames and the /api
// answers in api.json, and drives them in a fake document. Machines: grouped as each viewer sees them, a machine's
// sharing changed by its owner, a machine added and its token shown once, a node token moved and revoked, Enter
// opening the runs page on a machine, a phone showing a machine's facts without the controls. Team: a person's role,
// disabling and handing over, an invitation and the sign-in rules, the log's filters, a project created and its
// members changed by who may, and a phone that only shows. A project's drawer: its settings saved as changed, its
// checkouts checked and its workflows; its issue sync (state, log, comment preview, settings, token, unbinding, binding).
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
import * as tm from '../web/core/team.js';
import {install} from './dom.js';
import {settle} from './fake.js';
import {NOW, team} from './rig.js';
import {test, eq, ok, run, until} from './check.js';

const css = ['base.css', 'components.css', 'pages.css'].map(f => readFileSync(new URL(`../web/css/${f}`, import.meta.url), 'utf8')).join('\n');
const cssClasses = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map(m => m[1]));
const classesOf = s => new Set([...s.matchAll(/class="([^"]*)"/g)].flatMap(m => m[1].split(/\s+/).filter(Boolean)));
const wordsLeft = s => s.match(/\b(mach|team|proj|secret|api|m|role|app|home|confirm|form|why|status|ui|picker)\.[a-zA-Z_]+\b/g);
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
    users: () => ans('GET /api/users'),
    admits: () => ans('GET /api/admits'),
    invites: () => ans('GET /api/invites'),
    audit: () => ans('GET /api/audit'),
    setUser: p => ans('POST /api/users', p),
    offboard: p => ans('POST /api/users/offboard', p),
    addAdmit: a => ans('POST /api/admits', a),
    removeAdmit: a => ans('DELETE /api/admits', a),
    makeInvite: p => ans('POST /api/invites', p),
    revokeInvite: id => ans('DELETE /api/invites', {id}),
    machineCreds: () => ans('GET /api/machines'),
    addMachine: name => ans('POST /api/machines', {name}),
    rebindMachine: id => ans('POST /api/machines/rebind', {id}),
    revokeMachine: id => ans('DELETE /api/machines', {id}),
    trackers: () => ans('GET /api/trackers'),
    bindTracker: p => ans('POST /api/trackers', p),
    unbindTracker: id => ans('DELETE /api/trackers', {id}),
    trackerSettings: (id, settings) => ans('POST /api/trackers/settings', {id, settings}),
    trackerToken: (id, token) => ans('POST /api/trackers/credential', {id, token}),
    rescanTracker: id => ans('POST /api/trackers/rescan', {id}),
    trackerIssues: id => ans('GET /api/trackers/issues', {id}),
    trackerPreview: (id, number) => ans('GET /api/trackers/preview', {id, number}),
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
  return {...props, http, vnode: () => html`<${KeysContext.Provider} value=${keys}><${App} ...${props} /><//>`};
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

test('project settings: only what changed is sent; paths, directory checks and workflow names read right', () => {
  const p = {id: 'p1', name: 'Shop', owner: 'u_a', context: 'Be careful.', repos: [{name: 'shop', remote: 'git@x:shop.git', base: 'main', dirs: {mba: '/w/shop'}, worktrees: true}],
    defaults: {workflow: 'fix', agent: 'claude', roles: {review: 'codex'}, machine: 'mba'}, hooks: {setup: ['make', 'deps']}};
  const d = tm.draftOf(p);
  eq(tm.projectEditOf(p, d), null, 'an untouched draft sends nothing');
  eq(d.hooks, {setup: 'make deps', before_run: '', check: '', cleanup: ''}, 'hooks as lines');
  eq(tm.projectEditOf(p, {...d, name: ' Shop ', context: 'Be careful.'}), null, 'a name only padded is the same');
  eq(tm.projectEditOf(p, {...d, hooks: {...d.hooks, check: '  go   test ./... ', setup: ''}}), {id: 'p1', hooks: {check: ['go', 'test', './...']}}, 'hooks split on spaces, an emptied one dropped');
  eq(tm.projectEditOf(p, {...d, roles: {...d.roles, review: '', planner: 'claude'}}).defaults, {agent: 'claude', machine: 'mba', roles: {planner: 'claude'}, workflow: 'fix'},
    'the defaults whole, keeping the agent outside the roles');
  const repos = structuredClone(d.repos);
  repos[0].dirs.push({machine: 'linux', path: ' /srv/shop '}, {machine: '', path: '/x'});
  repos.push({name: ' ', remote: '', base: '', worktrees: false, dirs: []});
  eq(tm.projectEditOf(p, {...d, repos}).repos, [{name: 'shop', remote: 'git@x:shop.git', base: 'main', dirs: {mba: '/w/shop', linux: '/srv/shop'}, worktrees: true}],
    'paths trimmed; a row without a machine or a repository without a name left out');
  eq(tm.projectEditOf({id: 'p2', name: 'Docs'}, tm.draftOf({id: 'p2', name: 'Docs'})), null, 'a bare project');
  eq(tm.dirsToCheck({...d, repos}), [{repo: 'shop', machine: 'mba', path: '/w/shop'}, {repo: 'shop', machine: 'linux', path: '/srv/shop'}], 'what to check');
  eq([tm.parentOf('/w/shop'), tm.parentOf('/w/shop/'), tm.parentOf('C:\\w\\shop'), tm.parentOf('/shop')], ['/w', '/w', 'C:\\w', '/'], 'parents');
  eq([tm.baseOf('/w/shop/'), tm.baseOf('C:\\w\\shop')], ['shop', 'shop'], 'names');
  const listed = {exists: true, dirs: [{name: 'shop', path: '/w/shop', git: true}, {name: 'docs', path: '/w/docs'}]};
  eq([tm.dirVerdict(listed, '/w/shop'), tm.dirVerdict(listed, '/w/docs'), tm.dirVerdict(listed, '/w/gone'), tm.dirVerdict({exists: false}, '/w/shop'),
    tm.dirVerdict({outside: true}, '/w/shop'), tm.dirVerdict(null, '/w/shop')], ['git', 'plain', 'missing', 'missing', 'outside', 'missing'], 'verdicts');
  eq([tm.flowName(tm.flowTemplate), tm.flowName('---\nname: "hot-fix"\n---\n'), tm.flowName('name: loose\n'), tm.flowName('---\ndescription: x\n---\nname: body\n')],
    ['my-flow', 'hot-fix', '', ''], 'workflow names from the front matter only');
  eq([tm.issueURL({base: 'https://github.com/', repo: 'shop/shop', kind: 'github'}, 7), tm.issueURL({base: 'https://gitlab.com', repo: 'g/sub/r', kind: 'gitlab'}, 7)],
    ['https://github.com/shop/shop/issues/7', 'https://gitlab.com/g/sub/r/-/issues/7'], 'issue links');
  eq(tm.shownComment('<!-- tend:progress c_1 t_2 -->\n**tend** · done\n<!-- a\nnote -->\nPart of #3'), '**tend** · done\nPart of #3', 'a comment as the tracker shows it');
});

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

// mounted draws the page into a fake document in form and lang, with its /api answers read, and checks its classes
// and words as styled does.
async function mounted(a, f, lang, what) {
  words.lang.value = lang;
  const root = await mount(a.vnode(), f);
  await until(() => a.http.calls.some(c => c[0] === 'GET /api/users'), 'the people');
  await settled();
  const classes = new Set(root.all().flatMap(e => e.className.split(' ').filter(Boolean)));
  eq([...classes].filter(c => !cssClasses.has(c)), [], `${what}: classes without a rule`);
  eq(wordsLeft(root.textContent), null, `${what}: words not found`);
  return root;
}
const writes = http => http.calls.filter(c => !c[0].startsWith('GET '));
const rowOf = (root, name) => root.find('.team-who').find(x => x.textContent.includes(name)).parentNode;
const menuOf = async (root, name, item) => {
  await click(rowOf(root, name).one('.menu-wrap').one('button'));
  await click(root.find('[role=menuitem]').find(b => b.textContent === item));
};

test('team: drawn in both forms and languages, for an admin and for a member', async () => {
  const r = await team();
  try {
    for (const session of [admin, bo]) for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
      await mounted(app(r, {url: '/?page=team', session}), f, lang, `${session.id} ${f}/${lang}`);
    }
  } finally { words.lang.value = 'zh'; form.value = 'desktop'; }
  const root = await mounted(app(r, {url: '/?page=team'}), 'desktop', 'zh', 'admin');
  eq(root.find('.team-tr').filter(x => !x.classList.contains('team-th')).map(x => x.one('b').textContent),
    [words.f('team.you', 'Ann Lee'), 'Bo Lin', 'Cy Park', 'Eve Ng', 'Di Wu'], 'active people by name, then the disabled');
  ok(rowOf(root, 'Bo Lin').textContent.includes(words.f('team.inProject', 'Docs', words.t('role.owner'))), 'the projects they take part in');
  ok(rowOf(root, 'Bo Lin').textContent.includes('bo-laptop'), 'the machines they own');
  eq(rowOf(root, 'Ann Lee').find('.menu-wrap').length, 0, 'nothing to change about oneself');
  const member = await mounted(app(r, {url: '/?page=team', session: bo}), 'desktop', 'zh', 'member');
  eq(member.find('.panel').map(x => x.one('h2').textContent), [words.t('team.members'), words.t('team.projects')], 'a member sees the people and the projects');
  eq(member.one('.team').find('.menu-wrap').length + member.one('.team').find('button').filter(b => [words.t('team.new'), words.t('team.invite')].includes(labelOf(b))).length, 0, 'and changes nothing there');
  eq(r.errors, [], 'errors');
});

test('team: an admin changes a role, disables and enables, and hands someone\'s work over', async () => {
  const r = await team();
  const a = app(r, {url: '/?page=team'});
  const root = await mounted(a, 'desktop', 'zh', 'admin');
  await menuOf(root, 'Bo Lin', words.t('team.makeAdmin'));
  await settled();
  await menuOf(root, 'Cy Park', words.t('team.disable'));
  ok(root.one('.modal').textContent.includes(words.t('team.disableNote')), 'what disabling does');
  await click(buttonOf(root.one('.modal-foot'), words.t('team.disable')));
  await settled();
  await menuOf(root, 'Di Wu', words.t('team.enable'));
  await settled();
  await menuOf(root, 'Bo Lin', words.t('team.offboard'));
  const steps = root.one('.team-steps').textContent;
  ok(steps.includes(words.f('team.offProjects', 1, 'Ann Lee')) && steps.includes(words.f('team.offLeft', 1)) && steps.includes(words.t('team.offEnd')), 'the steps: ' + steps);
  await click(buttonOf(root.one('.modal-foot'), words.t('team.offGo')));
  await settled();
  eq(writes(a.http), [['POST /api/users', {id: 'u_b', role: 'admin'}], ['POST /api/users', {id: 'u_c', disabled: true}],
    ['POST /api/users', {id: 'u_d', disabled: false}], ['POST /api/users/offboard', {user: 'u_b', to: 'u_a'}]], 'the writes');
  ok(root.find('.toast-text').some(x => x.textContent === words.f('team.offDone', 'Bo Lin', 'Ann Lee')), 'said');
});

test('team: an invitation made and one revoked, sign-in rules added and removed, the log filtered', async () => {
  const r = await team();
  const copied = [];
  const a = app(r, {url: '/?page=team', copy: text => { copied.push(text); return Promise.resolve(); }});
  const root = await mounted(a, 'desktop', 'en', 'admin');
  try {
    await click(buttonOf(root, words.t('team.invite')));
    await click(root.one('.modal').one('.picker-btn'));
    await click(root.find('[role=option]').find(o => o.textContent.includes('Docs')));
    await click(root.one('.modal').find('[role=radio]').find(b => b.textContent === words.t('role.reader')));
    await click(buttonOf(root.one('.modal-foot'), words.t('team.inviteGo')));
    await settled();
    eq(root.one('.modal').one('.secret-value').textContent, api['POST /api/invites'].url, 'the link, once');
    await click(root.one('.modal').one('.secret').one('button'));
    await settled();
    eq(copied, [api['POST /api/invites'].url], 'copied');
    await click(buttonOf(root.one('.modal-foot'), words.t('team.done')));
    await click(root.find('.team-row').find(x => x.textContent.includes('#a1f3c9e2b7d0')).one('button'));
    await settled();
    await click(buttonOf(root, words.t('team.addAdmit')));
    await click(root.one('.modal').find('[role=radio]').find(b => b.textContent === words.t('team.admit.email')));
    await type(root.one('.modal').find('input')[0], ' eve@example.com ');
    await click(buttonOf(root.one('.modal-foot'), words.t('team.addAdmit')));
    await settled();
    await click(root.find('button').find(b => b.getAttribute('aria-label') === words.f('team.admitRemove', 'example.com')));
    await settled();
    eq(writes(a.http), [['POST /api/invites', {role: 'member', project: 'p2', access: 'reader'}], ['DELETE /api/invites', {id: 'a1f3c9e2b7d0'}],
      ['POST /api/admits', {kind: 'email', value: 'eve@example.com', role: 'member'}], ['DELETE /api/admits', {kind: 'domain', value: 'example.com'}]], 'the writes');
    const kinds = () => root.one('.team-log').find('li').map(x => x.find('span')[2].textContent);
    eq(kinds().length, api['GET /api/audit'].length, 'every entry');
    await click(root.find('.chip').find(c => c.textContent.startsWith(words.t('team.audit.refused'))));
    eq(kinds(), ['login_refused', 'denied'], 'the refusals');
  } finally { words.lang.value = 'zh'; }
});

test('team: a project created for someone; an admin changes another\'s members', async () => {
  const r = await team();
  const a = app(r, {url: '/?page=team'});
  const root = await mounted(a, 'desktop', 'zh', 'admin');
  await r.srv.play('team-project', {
    async create() {
      await click(buttonOf(root, words.t('team.new')));
      await type(root.one('.modal').find('input')[0], ' Billing ');
      await click(root.one('.modal').one('.picker-btn'));
      await click(root.find('[role=option]').find(o => o.textContent.includes('Bo Lin')));
      await click(buttonOf(root.one('.modal-foot'), words.t('team.create')));
    },
    async add() {
      await settled();
      eq(root.find('.modal').length, 0, 'created');
      await click(root.find('.team-project').find(b => b.textContent.includes('Shop')));
      const drawer = root.one('.drawer');
      ok(drawer.textContent.includes('Shop'), 'Shop opens');
      await click(buttonOf(drawer, words.t('team.add')));
      await click(root.one('.modal').one('.picker-btn'));
      eq(root.find('[role=option]').map(o => o.textContent.includes('Eve Ng')).filter(Boolean).length, 1, 'those not in it yet');
      await click(root.find('[role=option]').find(o => o.textContent.includes('Eve Ng')));
      await click(root.one('.modal').find('[role=radio]').find(b => b.textContent === words.t('role.reader')));
      await click(buttonOf(root.one('.modal-foot'), words.t('team.add')));
    },
    async role() {
      await settled();
      const cy = root.one('.drawer').find('.team-row').find(x => x.textContent.includes('Cy Park'));
      await click(cy.find('[role=radio]').find(b => b.textContent === words.t('role.participant')));
    },
    async remove() {
      await settled();
      const bo = root.one('.drawer').find('.team-row').find(x => x.textContent.includes('Bo Lin'));
      await click(buttonOf(bo, words.t('team.remove')));
      await click(buttonOf(root.one('.modal-foot'), words.t('team.remove')));
    },
    async done() { await settled(); },
  });
  eq(r.errors, [], 'errors');
});

test('team: j and Enter open a project; a member manages the one they own and only reads the others', async () => {
  const r = await team();
  const a = app(r, {url: '/?page=team', session: bo});
  const root = await mounted(a, 'desktop', 'zh', 'member');
  await act(() => { a.keys.handle(press('j')); });
  await act(() => { a.keys.handle(press('Enter')); });
  ok(root.one('.drawer').textContent.includes('Shop'), 'the second project opens');
  eq(root.one('.drawer').find('[role=radio]').length, 0, 'Shop is Ann\'s');
  await click(root.one('.drawer').find('button').find(b => b.getAttribute('aria-label') === words.t('ui.close')));
  await click(root.find('.team-project').find(b => b.textContent.includes('Docs')));
  ok(buttonOf(root.one('.drawer'), words.t('team.add')), 'Docs is Bo\'s');
});

const valueOf = el => el.value ?? el.getAttribute('value');
const tabOf = (root, label) => root.one('.drawer').find('[role=tab]').find(b => b.textContent.trim() === label);
const fieldOf = (root, label) => {
  const f = root.find('.field').find(x => x.find('label')[0]?.textContent === label);
  return f && [...f.find('input'), ...f.find('textarea')][0];
};

test('project settings: saved as changed, the checkouts checked, a workflow without a name refused and one removed', async () => {
  const r = await team();
  const a = app(r, {url: '/?page=team'});
  const root = await mounted(a, 'desktop', 'zh', 'admin');
  await r.srv.play('team-settings', {
    async open() {
      await click(root.find('.team-project').find(b => b.textContent.includes('Shop')));
      await click(tabOf(root, words.t('proj.settings')));
    },
    async save() {
      await settled();
      eq(valueOf(fieldOf(root, words.t('proj.context'))), 'The shop service: Go, Postgres.', 'the project\'s context');
      eq(valueOf(fieldOf(root, words.t('proj.hook.check'))), 'go test ./...', 'a hook as one line');
      ok(root.one('.drawer').textContent.includes('gpt-6') || root.one('.drawer').textContent.includes('codex'), 'the agents listed');
      await type(fieldOf(root, words.t('proj.context')), 'The shop service: Go, Postgres. Money is in cents.');
      await type(fieldOf(root, words.t('proj.hook.check')), 'go test -race ./...');
      await click(buttonOf(root.one('.drawer'), words.t('proj.save')));
    },
    async check() {
      await settled();
      ok(root.textContent.includes(words.f('proj.saved', 'Shop')), 'saved');
      await click(buttonOf(root.one('.drawer'), words.t('proj.check')));
    },
    async flow() {
      await settled();
      eq(root.find('.proj-dir-said').map(x => x.textContent), [words.t('proj.dir.git'), words.t('proj.dir.outside')], 'what each checkout is');
      await click(buttonOf(root.one('.drawer'), words.t('proj.newFlow')));
      eq(valueOf(root.one('.modal').one('textarea')), tm.flowTemplate, 'a new one starts from the template');
      await type(root.one('.modal').one('textarea'), '## implement\n');
      await click(buttonOf(root.one('.modal-foot'), words.t('form.save')));
      ok(root.one('.modal').textContent.includes(words.t('proj.flowNameless')), 'a definition without a name is refused');
      await click(buttonOf(root.one('.modal-foot'), words.t('home.cancel')));
      const row = root.one('.drawer').find('.team-row').find(x => x.textContent.includes('shop-fix'));
      await click(buttonOf(row, words.t('proj.removeFlow')));
      await click(buttonOf(root.one('.modal-foot'), words.t('proj.removeFlow')));
    },
    async done() { await settled(); ok(root.textContent.includes(words.f('proj.flowRemoved', 'shop-fix')), 'removed'); },
  });
  eq(r.errors, [], 'errors');
});

test('issue sync in label mode says it labels, in its help, its log and the bind form', async () => {
  const r = await team();
  const http = fakeHTTP();
  const bound = api['GET /api/trackers'][0];
  http.trackers = () => Promise.resolve([{...bound, settings: {...bound.settings, on_accept: 'label', accept_label: 'shipped'}}]);
  const a = app(r, {url: '/?page=team', http});
  const root = await mounted(a, 'desktop', 'zh', 'admin');
  await until(() => root.find('.team-project').length > 0, 'the projects');
  await click(root.find('.team-project').find(b => b.textContent.includes('Shop')));
  await click(tabOf(root, words.t('proj.sync')));
  await until(() => root.find('.proj-issue').length === 3, 'the sync log');
  const pane = root.one('.proj-sync').textContent;
  ok(pane.includes(words.f('proj.syncHelp.label', 'shipped')) && !pane.includes(words.t('proj.syncHelp')), `the help says it labels: ${pane}`);
  ok(root.find('.proj-issue')[2].textContent.includes(words.f('proj.labelled', 'shipped')) && !root.find('.proj-issue')[2].textContent.includes(words.t('proj.closed')),
    'the log says labelled, not closed');
  await click(root.one('.drawer').find('button').find(b => b.getAttribute('aria-label') === words.t('ui.close')));
  await click(root.find('.team-project').find(b => b.textContent.includes('Docs')));
  await click(tabOf(root, words.t('proj.sync')));
  await until(() => root.one('.drawer').find('button').some(b => labelOf(b) === words.t('proj.bind')), 'Docs has none');
  await click(buttonOf(root.one('.drawer'), words.t('proj.bind')));
  ok(root.one('.modal').textContent.includes(words.t('proj.syncHelp')), 'the form starts closing issues');
  await click(root.one('.modal').find('[role=radio]').find(b => b.textContent === words.t('proj.accept.label')));
  ok(root.one('.modal').textContent.includes(words.f('proj.syncHelp.label', 'tend:accepted')), 'and follows the choice');
  eq(r.errors, [], 'errors');
});

test('issue sync: a binding\'s state, log and comment preview; its settings, token, rescan and unbinding; a project bound', async () => {
  const r = await team();
  const a = app(r, {url: '/?page=team'});
  const root = await mounted(a, 'desktop', 'zh', 'admin');
  await until(() => root.find('.team-sync').length === 1, 'the binding on the list');
  eq(root.one('.team-sync').textContent.trim(), words.f('team.sync', 'shop/shop', words.t('team.sync.ok')), 'Shop syncs');
  await click(root.find('.team-project').find(b => b.textContent.includes('Shop')));
  await click(tabOf(root, words.t('proj.sync')));
  await until(() => root.find('.proj-issue').length === 3, 'the sync log');
  const drawer = () => root.one('.drawer');
  ok(drawer().textContent.includes(words.f('proj.failing', 1)), 'one failing');
  eq(root.find('.proj-issue').map(x => x.find('a')[0].getAttribute('href')), [42, 43, 40].map(n => 'https://github.com/shop/shop/issues/' + n), 'links to the issues');
  ok(root.find('.proj-issue')[1].textContent.includes('422 Unprocessable Entity'), 'an issue\'s error');
  ok(root.find('.proj-issue')[2].textContent.includes(words.t('proj.noTask')) && root.find('.proj-issue')[2].textContent.includes(words.t('proj.dirty')), 'one without a task, to read again');
  eq(drawer().find('button').filter(b => labelOf(b) === words.t('proj.bind')).length, 0, 'a project binds one repository');

  await click(buttonOf(root.find('.proj-issue')[0], words.t('proj.preview')));
  await until(() => root.find('.proj-preview').length === 1, 'the comment');
  ok(root.one('.proj-preview').textContent.includes('Cart totals'), 'the comment as it would be written');
  ok(!root.one('.proj-preview').textContent.includes('tend:progress'), 'without its hidden marker, as the tracker shows it');
  await click(buttonOf(root.one('.modal-foot'), words.t('team.done')));

  await click(buttonOf(drawer(), words.t('proj.syncSettings')));
  await click(root.one('.modal').find('[role=radio]').find(b => b.textContent === words.t('proj.accept.label')));
  await type(fieldOf(root.one('.modal'), words.t('proj.acceptLabel')), '');
  eq(buttonOf(root.one('.modal-foot'), words.t('form.save')).disabled, true, 'a label to add is needed');
  await type(fieldOf(root.one('.modal'), words.t('proj.acceptLabel')), 'shipped');
  await click(root.one('.modal').find('[role=checkbox]').find(b => b.textContent.includes(words.t('proj.assigned'))));
  await click(buttonOf(root.one('.modal-foot'), words.t('form.save')));
  await settled();
  await click(buttonOf(drawer(), words.t('proj.replaceToken')));
  await type(root.one('.modal').one('input'), 'example-token');
  await click(buttonOf(root.one('.modal-foot'), words.t('form.save')));
  await settled();
  await click(buttonOf(drawer(), words.t('proj.rescan')));
  await settled();
  await click(buttonOf(drawer(), words.t('proj.unbind')));
  await click(buttonOf(root.one('.modal-foot'), words.t('proj.unbind')));
  await settled();
  eq(writes(a.http), [
    ['POST /api/trackers/settings', {id: 'k1', settings: {label: 'tend', comment: true, on_accept: 'label', accept_label: 'shipped', poll: 60, pr: true, assigned: true}}],
    ['POST /api/trackers/credential', {id: 'k1', token: 'example-token'}], ['POST /api/trackers/rescan', {id: 'k1'}], ['DELETE /api/trackers', {id: 'k1'}],
  ], 'what was sent');

  await click(root.one('.drawer').find('button').find(b => b.getAttribute('aria-label') === words.t('ui.close')));
  await click(root.find('.team-project').find(b => b.textContent.includes('Docs')));
  await click(tabOf(root, words.t('proj.sync')));
  await until(() => drawer().find('button').some(b => labelOf(b) === words.t('proj.bind')), 'Docs has none');
  await click(buttonOf(drawer(), words.t('proj.bind')));
  await click(root.one('.modal').find('[role=radio]').find(b => b.textContent === 'Gitea'));
  eq(valueOf(fieldOf(root.one('.modal'), words.t('proj.trackerBase'))), '', 'Gitea has no public address');
  await type(fieldOf(root.one('.modal'), words.t('proj.trackerBase')), 'https://git.example.com');
  await type(fieldOf(root.one('.modal'), words.t('proj.trackerRepo')), 'docs/site');
  eq(buttonOf(root.one('.modal-foot'), words.t('proj.bind')).disabled, true, 'a token is needed');
  await type(fieldOf(root.one('.modal'), words.t('proj.token')), 'example-token');
  await click(buttonOf(root.one('.modal-foot'), words.t('proj.bind')));
  await settled();
  eq(writes(a.http).at(-1), ['POST /api/trackers', {project: 'p2', kind: 'gitea', base: 'https://git.example.com', repo: 'docs/site', token: 'example-token',
    settings: {label: 'tend', comment: true, on_accept: 'close', accept_label: 'tend:accepted', poll: 60}}], 'the binding');
  ok(root.one('.modal').textContent.includes(words.t('proj.hook.gitea')), 'how to add the webhook');
  eq(root.one('.modal').find('.secret').length, 2, 'the webhook address and its secret');
  eq(r.errors, [], 'errors');
});

test('project drawer: a member of a project sees its members only; a phone shows no settings', async () => {
  const r = await team();
  const a = app(r, {url: '/?page=team', session: bo});
  const root = await mounted(a, 'desktop', 'zh', 'member');
  await click(root.find('.team-project').find(b => b.textContent.includes('Shop')));
  eq(root.one('.drawer').find('[role=tab]').length, 0, 'Shop is Ann\'s: no tabs');
  await click(root.one('.drawer').find('button').find(b => b.getAttribute('aria-label') === words.t('ui.close')));
  await click(root.find('.team-project').find(b => b.textContent.includes('Docs')));
  eq(root.one('.drawer').find('[role=tab]').map(b => b.textContent.trim()), [words.t('proj.members'), words.t('proj.settings'), words.t('proj.sync')], 'Docs is Bo\'s');
});

test('team: a phone lists the people and the projects, and changes nothing', async () => {
  const r = await team();
  const a = app(r, {url: '/?page=team'});
  try {
    const root = await mounted(a, 'phone', 'zh', 'phone');
    ok(root.textContent.includes(words.t('team.desktop')), 'where they are managed');
    eq(root.find('.menu-wrap').length + root.find('.team-log').length, 0, 'no controls, no log');
    await click(root.find('.team-project').find(b => b.textContent.includes('Shop')));
    eq(root.find('[role=radio]').length + root.find('button').filter(b => labelOf(b) === words.t('team.add')).length, 0, 'the members only read');
    eq(writes(a.http), [], 'no writes');
  } finally { form.value = 'desktop'; }
});

await run();
