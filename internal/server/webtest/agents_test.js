// agents_test draws the agent page in both forms and both languages from the agent frames, and drives it in a fake
// document: the list grouped by where each agent comes from, the machines it runs on and what uses it, a definition's
// text, command and sharing as far as the viewer may see them, j / k and Enter, a definition refused and saved, a new
// one from its first step, a Claude Code subagent read from a file and imported, one copied and one exported, sharing changed, one removed
// after a confirm, and a phone that only shows.
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
import * as ag from '../web/core/agents.js';
import {install} from './dom.js';
import {settle, readFrames} from './fake.js';
import {NOW, agents} from './rig.js';
import {test, eq, ok, run} from './check.js';

const css = ['base.css', 'components.css', 'pages.css'].map(f => readFileSync(new URL(`../web/css/${f}`, import.meta.url), 'utf8')).join('\n');
const cssClasses = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map(m => m[1]));
const classesOf = s => new Set([...s.matchAll(/class="([^"]*)"/g)].flatMap(m => m[1].split(/\s+/).filter(Boolean)));
const wordsLeft = s => s.match(/\b(ag|me|api|role|app|home|confirm|form|status|ui|picker|nav)\.[a-zA-Z_]+\b/g);
const press = (key, more = {}) => ({key, target: globalThis.document?.body, preventDefault() {}, ...more});
const api = JSON.parse(readFileSync(new URL('./api.json', import.meta.url), 'utf8'));

const bo = {id: 'u_b', name: 'Bo Lin', role: 'member'};
const names = signal(Object.fromEntries(api['GET /api/users'].map(u => [u.id, u.name])));
const t = k => words.t(k), f = (k, ...a) => words.f(k, ...a);

// listed is what the coordinator answered the page's first read (agents-state).
const first = readFrames('agents-state').filter(l => l.s?.type === 'res' && l.s.id >= 5).map(l => l.s.result);
const listed = {defs: first[0].defs, agents: first[1].agents};
const textOf = name => listed.defs.find(d => d.name === name).text;

function fakeHistory(url) {
  const loc = {pathname: '/', search: '', hash: ''};
  const set = u => { const x = new URL(u, 'http://tend.test'); loc.pathname = x.pathname; loc.search = x.search; loc.hash = x.hash; };
  set(url);
  return {location: loc, history: {pushState: (_, __, u) => set(u), replaceState: (_, __, u) => set(u)}};
}

function app(r, {session = bo, download = () => {}} = {}) {
  const keys = createKeys({timers: r.clk});
  const toasts = createToasts({timers: r.clk});
  let n = 0;
  const commands = createCommands({wire: r.wire, newID: () => 'c' + ++n});
  const {location, history} = fakeHistory('/?page=agents');
  const router = createRouter({location, history});
  const noStore = {getItem: () => null, setItem() {}};
  const props = {store: r.store, commands, toasts, wire: r.wire, http: {users: () => Promise.resolve([])}, router, keys, nav: createNav({storage: noStore, width: 1440}),
    prefs: createPrefs({storage: noStore, asked: 'zh'}), session, names, clock: () => NOW, fetchOutput: () => Promise.resolve({events: []}),
    storage: noStore, copy: () => Promise.resolve(), onLogout() {}, agentDefs: ag.createAgentDefs({wire: r.wire}), download};
  return {...props, vnode: () => html`<${KeysContext.Provider} value=${keys}><${App} ...${props} /><//>`};
}

function drawn(vnode, fm, lang) {
  form.value = fm;
  words.lang.value = lang;
  try { return renderToString(vnode); } finally { form.value = 'desktop'; words.lang.value = 'zh'; }
}

function styled(root, what) {
  const classes = new Set(root.all().flatMap(e => e.className.split(' ').filter(Boolean)));
  eq([...classes].filter(c => !cssClasses.has(c)), [], `${what}: classes without a rule`);
  eq(wordsLeft(root.textContent), null, `${what}: words not found`);
}

async function mount(vnode, fm = 'desktop') {
  form.value = fm;
  const root = install();
  await act(() => render(vnode, root));
  return root;
}
const settled = () => act(() => settle());
const click = el => act(() => el.dispatch('click'));
const type = (el, text) => act(() => { el.value = text; el.dispatch('input'); });
// valueOf is a field's value as typed, or as the page drew it (the fake document keeps that as an attribute).
const valueOf = el => el.value ?? el.getAttribute('value');
const labelOf = b => b.textContent.slice(0, b.textContent.length - b.find('kbd').map(k => k.textContent).join('').length).trim();
const buttonsOf = (root, label) => root.find('button').filter(b => labelOf(b) === label);
const buttonOf = (root, label) => {
  const got = buttonsOf(root, label);
  if (got.length !== 1) throw new Error(`${label}: ${got.length} buttons`);
  return got[0];
};
const rowNames = root => root.find('[role=row]').slice(1).map(x => x.one('b').textContent);
const rowOf = (root, name) => root.find('[role=row]').find(x => x.find('b').some(b => b.textContent === name));
const pick = (root, name) => click(rowOf(root, name));
const aside = root => root.one('.ag-aside');
const tabTo = (root, id) => click(root.find('[role=tab]').find(b => b.textContent.trim() === t('ag.tab.' + id)));
const toasts = root => root.find('.toast-text').map(x => x.textContent);

// opened is the page drawn in form fm after it read its lists; steps are more of agents-state's.
async function opened({fm = 'desktop', session = bo, download} = {}) {
  let a = null, root = null;
  const r = await agents({async mount(r) { a = app(r, {session, download}); root = await mount(a.vnode(), fm); }, read: settled});
  await settled();
  return {r, a, root};
}

test('where an agent comes from, what it runs as and on, what uses it and what the viewer may do with it', () => {
  const views = [{name: 'a-own', owner: 'u_a', manage: true, text: '---\nname: a-own\nprovider: claude\n---\n', warnings: ['x']},
    {name: 'b-own', owner: 'u_b', manage: true}, {name: 'p-def', owner: 'project:p1', manage: true, text: 't'}, {name: 'c-view', owner: 'u_c', text: 't'}];
  const picks = [{name: 'claude', provider: 'claude'}, {name: 'a-own', provider: 'claude'}, {name: 'p-def', provider: 'codex', model: 'gpt-6', machine: 'linux'},
    {name: 'c-view', provider: 'claude'}];
  const list = ag.rows(views, picks, 'u_a');
  eq(list.map(r => [r.name, r.kind, r.source]), [['a-own', 'def', 'mine'], ['b-own', 'def', 'others'], ['c-view', 'def', 'shared'], ['claude', 'profile', 'profile'],
    ['p-def', 'def', 'project']], 'an admin\'s: another\'s definition not shared with them is only managed');
  eq(ag.counts(list), {all: 5, mine: 1, project: 1, shared: 1, others: 1, profile: 1}, 'counts');
  eq(ag.spec(list[4]), {provider: 'codex', model: 'gpt-6', effort: '', permission: '', machine: 'linux', deny: []}, 'what it compiles to');
  eq(list.map(r => ag.may(r)), [
    {edit: true, remove: true, share: true, copy: false, export: true},
    {edit: false, remove: true, share: true, copy: false, export: false},
    {edit: false, remove: false, share: false, copy: true, export: true},
    {edit: false, remove: false, share: false, copy: true, export: false},
    {edit: true, remove: true, share: true, copy: false, export: true}], 'what may be done');

  const ms = [{name: 'a', state: 'connected', agents: {claude: {installed: true, auth: 'ok'}, codex: {installed: true, auth: 'missing'}}},
    {name: 'b', state: 'offline', agents: {claude: {installed: true, auth: 'ok'}, codex: {installed: false}}}, {name: 'c', state: 'connected'},
    {name: 'd', state: 'connected', agents: {claude: {installed: true}}}];
  eq(ag.runsOn({provider: 'claude'}, ms), [{name: 'a', state: 'ok'}, {name: 'b', state: 'offline'}, {name: 'c', state: 'unknown'}, {name: 'd', state: 'unknown'}], 'claude');
  eq(ag.runsOn({provider: 'codex'}, ms).map(x => x.state), ['auth', 'missing', 'unknown', 'unknown'], 'codex');
  eq(ag.runsOn({provider: 'claude', machine: 'b'}, ms).map(x => x.state), ['pinned', 'offline', 'pinned', 'pinned'], 'held to one machine');
  eq(ag.runnable(ag.runsOn({provider: 'codex'}, ms)), ['c', 'd'], 'where a run can go');
  const st = {projects: {p1: {id: 'p1', name: 'Shop', owner: 'u_a', members: {u_b: 'participant'}}, p2: {id: 'p2', name: 'Docs', owner: 'u_b'}},
    shares: {a: {machine: 'a', projects: ['p1']}, x: {machine: 'x', users: ['u_c']}}};
  const machines = [{name: 'x', owner: 'u_a'}, {name: 'a', owner: 'u_a'}, {name: 'mine', owner: 'u_b'}, {name: 'old', owner: 'u_b', retired: true}];
  eq(ag.usableMachines(machines, st, {id: 'u_b'}).map(m => m.name), ['mine', 'a'], 'a member\'s: their own, then shared through a project');
  eq(ag.usableMachines(machines, st, {id: 'u_a', role: 'admin'}).map(m => m.name), ['a', 'mine', 'x'], 'an admin\'s: all that run');
  eq(ag.ownersFor(st, {id: 'u_b'}), ['', 'project:p2'], 'a new one goes to the viewer or a project they own');
  eq(ag.ownersFor(st, {id: 'u_a', role: 'admin'}), ['', 'project:p2', 'project:p1'], 'any project for an admin');
  const used = ag.usedBy({projects: {p1: {id: 'p1', name: 'Shop', defaults: {agent: 'dev', roles: {review: 'dev', implement: 'dev', planner: 'x'}}}},
    tasks: {t1: {id: 't1', agent: 'dev', status: 'todo'}, t2: {id: 't2', agent: 'dev', status: 'done'}, t3: {id: 't3', status: 'todo'}}}, 'dev');
  eq([used.projects.map(x => [x.project.id, x.roles]), used.tasks.map(x => x.id)], [[['p1', ['', 'implement', 'review']]], ['t1']], 'what uses it');
});

test('the Markdown a definition starts from: a draft, an import, a copy, a new name', () => {
  eq(ag.draft({name: 'x', provider: 'claude', model: ' haiku ', role: 'test'}), '---\nname: x\nrole: test\nprovider: claude\nmodel: haiku\n---\n', 'a draft');
  eq(ag.draft({name: 'x', provider: 'codex'}), '---\nname: x\nprovider: codex\n---\n', 'without a model or a role');
  eq(ag.imported('﻿---\r\nname: r\r\nmodel: inherit\r\ntools: Read, Grep\r\n---\r\nBe brief.\r\n'), '---\nname: r\ntools: Read, Grep\nprovider: claude\n---\nBe brief.\n',
    'claude by default, the inherited model left out');
  eq(ag.imported('---\nname: r\nmodel: "inherit"\nprofile: quick\n---\n'), '---\nname: r\nprofile: quick\n---\n', 'a profile is kept, a quoted inherit left out');
  eq(ag.imported('---\nname: r\nprovider: codex\nmodel: gpt-6\n---'), '---\nname: r\nprovider: codex\nmodel: gpt-6\n---\n', 'a provider and a model are kept');
  eq(ag.imported('name: r\n'), 'name: r\n', 'no front matter: left for the coordinator to refuse');
  eq([ag.nameOf('---\nname: "a-b"\n---\nname: body\n'), ag.nameOf('---\ndescription: x\n---\nname: body\n'), ag.nameOf('name: loose\n'), ag.nameOf('---\r\nname: w\r\n---\r\n')],
    ['a-b', '', '', 'w'], 'names from the front matter only');
  eq(ag.renamed('---\nname: a\nrole: any\n---\nname: a\n', 'b'), '---\nname: b\nrole: any\n---\nname: a\n', 'renamed in the front matter');
  eq(ag.renamed('---\nrole: any\n---\n', 'b'), '---\nname: b\nrole: any\n---\n', 'a name given');
  eq([ag.copyName('a', ['a']), ag.copyName('a', ['a', 'a-copy', 'a-copy-2'])], ['a-copy', 'a-copy-3'], 'a copy\'s name');
  eq(ag.copied({kind: 'profile', name: 'quick'}, 'quick-copy'), '---\nname: quick-copy\nprofile: quick\n---\n', 'a copy of a profile starts from it');
  eq([ag.defName.test('a.b_c-1'), ag.defName.test('A'), ag.defName.test('-a'), ag.defName.test('a'.repeat(65))], [true, false, false, false], 'the name rule');
});

test('drawn in both forms and languages, styled and worded', async () => {
  const {a, root} = await opened();
  for (const fm of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
    const s = drawn(a.vnode(), fm, lang);
    eq([...classesOf(s)].filter(c => !cssClasses.has(c)), [], `${fm}/${lang}: classes without a rule`);
    eq(wordsLeft(s), null, `${fm}/${lang}: words not found`);
  }
  for (const lang of ['zh', 'en']) {
    words.lang.value = lang;
    try {
      for (const name of rowNames(root)) {
        await pick(root, name);
        for (const id of ['def', 'launch', 'share']) { await tabTo(root, id); styled(root, `${lang} ${name} ${id}`); }
      }
      for (const [label, kind] of [[t('ag.new'), 'new'], [t('ag.subagent'), 'subagent']]) {
        await click(buttonOf(root, label));
        styled(root, `${lang} ${kind}`);
        await act(() => { a.keys.handle(press('Escape')); });
        eq(root.find('.modal').length, 0, `${kind} closes`);
      }
    } finally { words.lang.value = 'zh'; }
  }
});

test('the list: by where each comes from, with what it runs as, where and its check', async () => {
  const {r, root} = await opened();
  eq(root.find('.filter').map(c => c.textContent), [`${t('ag.src.all')} 11`, `${t('ag.src.mine')} 2`, `${t('ag.src.project')} 2`, `${t('ag.src.shared')} 2`,
    `${t('ag.src.profile')} 5`], 'the kinds, none of other people\'s for a member');
  eq(rowNames(root), ['ann-plan', 'bo-dev', 'claude', 'codex', 'codex-high', 'cy-docs', 'docs-quick', 'docs-review', 'fake', 'quick', 'shop-review'], 'by name');
  const cells = name => rowOf(root, name).find('[role=gridcell]').map(c => c.textContent.trim());
  eq(cells('bo-dev'), ['bo-devFixes bugs in Docs', 'implement', 'claude · sonnet · high', t('ag.from.mine'), 'bo-laptop linux mba', '✓'], 'his own');
  eq(cells('docs-quick').slice(2, 5), ['claude · haiku', t('ag.from.mine'), 'bo-laptop linux mba'], 'what a profile it starts from gives');
  eq(cells('shop-review').slice(2, 6), ['codex · gpt-6 · xhigh', f('ag.from.project', 'Shop'), 'linux', '!'], 'where codex is signed in; its warning');
  eq(cells('cy-docs').slice(3, 5), [f('ag.from.shared', 'Cy Park'), 'linux'], 'held to linux');
  eq(cells('ann-plan').slice(3), [f('ag.from.shared', 'Ann Lee'), 'bo-laptop linux mba', ''], 'shared for use only: no check to show');
  eq(cells('quick').slice(3, 5), [t('ag.from.profile'), 'bo-laptop linux mba'], 'a profile');
  await click(root.find('.filter').find(c => c.textContent.startsWith(t('ag.src.shared'))));
  eq(rowNames(root), ['ann-plan', 'cy-docs'], 'only those shared with him');
  eq(r.errors, [], 'errors');
});

test('the picked one: its facts, notes, text, command and sharing as far as the viewer may see them', async () => {
  const {root} = await opened();
  await pick(root, 'shop-review');
  let text = aside(root).textContent;
  for (const s of [f('ag.note.auth', 'mba', 'codex'), f('ag.note.missing', 'bo-laptop', 'codex'), t('ag.note.codex'), f('ag.note.kept', 'output'), t('ag.warn.claudeOnly'),
    f('ag.usedRoles', 'Shop', 'review'), t('ag.usedTask'), f('ag.rev', 3, '9-28 10:00')]) ok(text.includes(s), 'shop-review: ' + s);
  eq(aside(root).find('button').map(labelOf).filter(l => ![t('ag.tab.def'), t('ag.tab.launch'), t('ag.tab.share')].includes(l)), [t('ag.copy'), t('ag.export')],
    'a project\'s he may read: copied or exported');
  await tabTo(root, 'launch');
  ok(aside(root).one('pre').textContent.startsWith('codex app-server -c approval_policy=on-request -c model=gpt-6'), 'its command');
  await tabTo(root, 'share');
  eq(aside(root).one('.ag-pane').one('.ag-list').find('li').map(x => x.textContent), [f('ag.projectOwned', 'Shop'), t('ag.youRead')], 'owned by Shop');

  await pick(root, 'ann-plan');
  text = aside(root).textContent;
  ok(text.includes(f('ag.hidden', 'Ann Lee')), 'its text is hidden');
  await tabTo(root, 'launch');
  ok(aside(root).textContent.includes(t('ag.launchHidden')), 'and its command');
  await tabTo(root, 'share');
  eq(aside(root).one('.ag-pane').one('.ag-list').find('li').map(x => x.textContent), [f('ag.toYou', 'Ann Lee'), t('ag.youUse')], 'shared for use');
  eq(aside(root).find('button').filter(b => [t('ag.edit'), t('ag.copy'), t('ag.export'), t('ag.remove')].includes(labelOf(b))).length, 0, 'nothing to do with it');

  await pick(root, 'bo-dev');
  text = aside(root).textContent;
  ok(text.includes(f('ag.usedRoles', 'Docs', t('ag.default'))) && text.includes(t('ag.usedTask')), 'Docs\'s default, one task not finished');
  ok(text.includes('WebFetch') && text.includes('acceptEdits'), 'its permissions and denied tools');
  eq(aside(root).one('pre').textContent, textOf('bo-dev'), 'its text');
  await tabTo(root, 'share');
  eq(aside(root).one('.ag-pane').one('.ag-list').find('li').map(x => x.textContent), [t('ag.shareNone')], 'not shared');
  eq(aside(root).find('button').map(labelOf).filter(l => ![t('ag.tab.def'), t('ag.tab.launch'), t('ag.tab.share')].includes(l)),
    [t('ag.edit'), t('ag.share'), t('ag.export'), t('ag.remove')], 'his own: edited, shared, exported, removed');

  await pick(root, 'cy-docs');
  ok(aside(root).textContent.includes(f('ag.pinned', 'linux')), 'held to linux');
  await pick(root, 'quick');
  ok(aside(root).one('pre').textContent.startsWith('# config.json → agents.quick\n{\n  "provider": "claude"'), 'a profile as config.json has it');
  ok(aside(root).textContent.includes(t('ag.inConfig')) && buttonOf(aside(root), t('ag.copy')), 'changed in config.json, copied here');
});

test('j and k move the pick; Enter edits what the viewer manages', async () => {
  const {a, root} = await opened();
  await pick(root, 'ann-plan');
  await act(() => { a.keys.handle(press('j')); });
  eq(aside(root).one('b').textContent, 'bo-dev', 'the next');
  await act(() => { a.keys.handle(press('Enter')); });
  eq(valueOf(root.one('.modal').one('textarea')), textOf('bo-dev'), 'its editor');
  await act(() => { a.keys.handle(press('Escape')); });
  await act(() => { a.keys.handle(press('k')); });
  await act(() => { a.keys.handle(press('Enter')); });
  eq(root.find('.modal').length, 0, 'ann-plan is not his to edit');
});

test('a definition refused, then saved; a new one, an import and a copy, each read again after the journal says so', async () => {
  const {r, a, root} = await opened();
  const modal = () => root.one('.modal');
  const save = () => click(buttonOf(modal().one('.modal-foot'), t('form.save')));
  const again = async () => { await settled(); r.flush(); await settled(); };
  await r.srv.play('agents-edit', {
    async bad() {
      await pick(root, 'bo-dev');
      await click(buttonOf(aside(root), t('ag.edit')));
      await type(modal().one('textarea'), textOf('bo-dev').replace('effort: high', 'effort: huge'));
      await save();
    },
    async refused() {
      await settled();
      ok(modal().textContent.includes(f('ag.refused', 'effort "huge": one of low, medium, high, xhigh, max')), 'why not: ' + modal().textContent);
      await type(modal().one('textarea'), textOf('bo-dev').replace('effort: high', 'effort: xhigh') + 'Say what you did not test.\n');
      await save();
    },
    async edited() {
      await settled();
      eq(root.find('.modal').length, 0, 'closed');
      ok(toasts(root).includes(f('ag.saved', 'bo-dev')), 'said');
      await again();
    },
    async new() {
      await settled();
      ok(aside(root).textContent.includes(f('ag.rev', 3, '9-30 14:33')), 'the new version, read again');
      await click(buttonOf(root, t('ag.new')));
      const go = () => buttonOf(modal().one('.modal-foot'), t('ag.next'));
      await type(modal().find('input')[0], 'bo-dev');
      ok(modal().textContent.includes(f('ag.nameTaken', 'bo-dev')) && go().disabled, 'a name taken');
      await type(modal().find('input')[0], 'docs-check');
      ok(modal().textContent.includes(f('ag.installedOn', 'bo-laptop linux mba')), 'where claude is');
      await click(modal().find('.filter').find(c => c.textContent === 'haiku'));
      await click(modal().find('[role=radio]').find(b => b.textContent === 'test'));
      await click(go());
      eq(valueOf(modal().one('textarea')), ag.draft({name: 'docs-check', provider: 'claude', model: 'haiku', role: 'test'}), 'the draft');
      await type(modal().one('textarea'), valueOf(modal().one('textarea')) + 'Check every link and every command on the page.\n');
      await click(modal().find('[role=radio]').find(b => b.textContent === f('ag.from.project', 'Docs')));
      await save();
    },
    async made() { await settled(); ok(toasts(root).includes(f('ag.saved', 'docs-check')), 'said'); await again(); },
    async import() {
      await settled();
      ok(rowNames(root).includes('docs-check'), 'listed');
      await click(buttonOf(root, t('ag.subagent')));
      ok(modal().textContent.includes(t('ag.importNote')), 'what an import does');
      const input = modal().one('input');
      input.files = [{text: () => Promise.resolve('---\nname: code-reviewer\ndescription: Reviews code for quality and security\ntools: Read, Grep, Glob\nmodel: inherit\n---\nYou are a senior code reviewer. Point out bugs, not style.\n')}];
      await act(() => input.dispatch('change'));
      await settled();
      ok(valueOf(modal().one('textarea')).startsWith('---\nname: code-reviewer\n'), 'the file read into the editor');
      await save();
    },
    async imported() { await settled(); ok(toasts(root).includes(f('ag.savedWarning', 'code-reviewer')), 'said, with its warning'); await again(); },
    async copy() {
      await settled();
      await pick(root, 'code-reviewer');
      ok(aside(root).textContent.includes(t('ag.warn.allow')), 'what it will not apply');
      await pick(root, 'cy-docs');
      await click(buttonOf(aside(root), t('ag.copy')));
      eq(valueOf(modal().one('textarea')), ag.renamed(textOf('cy-docs'), 'cy-docs-copy'), 'renamed');
      await type(modal().one('textarea'), ag.renamed(textOf('cy-docs'), 'cy-docs'));
      ok(modal().textContent.includes(f('ag.taken', 'cy-docs')) && buttonOf(modal().one('.modal-foot'), t('form.save')).disabled, 'Cy\'s is not his to replace');
      await type(modal().one('textarea'), ag.renamed(textOf('cy-docs'), 'cy-docs-copy'));
      await save();
    },
    async copied() { await settled(); await again(); },
  });
  await settled();
  ok(['code-reviewer', 'cy-docs-copy', 'docs-check'].every(n => rowNames(root).includes(n)), 'all listed');
  eq(root.find('.filter').find(c => c.textContent.startsWith(t('ag.src.mine'))).textContent, `${t('ag.src.mine')} 4`, 'his own now');
  eq(r.errors, [], 'errors');
  void a;
});

test('the editor says what saving a changed name does', async () => {
  const {root} = await opened();
  await pick(root, 'bo-dev');
  await click(buttonOf(aside(root), t('ag.edit')));
  const box = () => root.one('.modal');
  await type(box().one('textarea'), ag.renamed(textOf('bo-dev'), 'bo-fix'));
  ok(box().textContent.includes(f('ag.renamed', 'bo-fix', 'bo-dev')), 'saved as a new one');
  await type(box().one('textarea'), ag.renamed(textOf('bo-dev'), 'docs-quick'));
  ok(box().textContent.includes(f('ag.overwrites', 'docs-quick')), 'replacing his other one');
  await type(box().one('textarea'), ag.renamed(textOf('bo-dev'), 'quick'));
  ok(box().textContent.includes(f('ag.shadows', 'quick')), 'a profile\'s name');
  await type(box().one('textarea'), 'no front matter');
  ok(box().textContent.includes(t('ag.nameless')) && buttonOf(box().one('.modal-foot'), t('form.save')).disabled, 'no name');
});

test('sharing changed and a definition removed after a confirm, each read again from a new snapshot', async () => {
  const {r, root} = await opened();
  const modal = () => root.one('.modal');
  await r.srv.play('agents-share', {
    async share() {
      await pick(root, 'docs-quick');
      await click(buttonOf(aside(root), t('ag.share')));
      await click(modal().find('.picker-btn')[1]);
      await click(root.find('[role=option]').find(o => o.textContent.includes('Docs')));
      await click(buttonOf(root, t('picker.done')));
      await click(modal().find('[role=checkbox]').find(b => b.textContent.includes(t('ag.viewField'))));
      await click(buttonOf(modal().one('.modal-foot'), t('form.save')));
    },
    async shared() {
      await settled();
      eq(root.find('.modal').length, 0, 'closed');
      ok(toasts(root).includes(f('ag.sharedDone', 'docs-quick')), 'said');
      r.flush();
      await settled();
    },
    async remove() {
      await settled();
      await tabTo(root, 'share');
      eq(aside(root).one('.ag-pane').one('.ag-list').find('li').map(x => x.textContent), [f('ag.shareUser', 'Cy Park'), f('ag.shareProject', 'Docs'), t('ag.shareView')], 'read again');
      await pick(root, 'docs-review');
      await click(buttonOf(aside(root), t('ag.remove')));
      ok(modal().textContent.includes(t('ag.removeNote')) && modal().textContent.includes(f('ag.removeUsed', 'Docs')), 'what it does, and who names it');
      await click(buttonOf(modal().one('.modal-foot'), t('ag.remove')));
    },
    async removed() {
      await settled();
      ok(toasts(root).includes(f('ag.removed', 'docs-review')), 'said');
      r.flush();
      await settled();
    },
  });
  await settled();
  ok(!rowNames(root).includes('docs-review'), 'gone');
  eq(r.errors, [], 'errors');
});

test('a definition exported as its Markdown', async () => {
  const saved = [];
  const {root} = await opened({download: (name, text) => saved.push([name, text])});
  await pick(root, 'bo-dev');
  await click(buttonOf(aside(root), t('ag.export')));
  eq(saved, [['bo-dev.md', textOf('bo-dev')]], 'saved');
});

test('a phone lists them and shows one\'s facts and text, the controls left to a computer', async () => {
  const {root} = await opened({fm: 'phone'});
  try {
    ok(root.textContent.includes(t('ag.desktop')), 'where they are managed');
    eq(root.find('.card-row').length, 11, 'every agent');
    await click(root.find('.card-row').find(b => b.textContent.includes('bo-dev')));
    const page = root.find('.page-over')[0];
    ok(page && page.one('pre').textContent === textOf('bo-dev') && page.textContent.includes(f('ag.usedTasks', 1)), 'its facts and text');
    styled(root, 'phone bo-dev');
    eq(root.find('button').filter(b => [t('ag.edit'), t('ag.share'), t('ag.copy'), t('ag.export'), t('ag.remove'), t('ag.new'), t('ag.subagent')].includes(labelOf(b))).length, 0,
      'no controls');
  } finally { form.value = 'desktop'; }
});

await run();
