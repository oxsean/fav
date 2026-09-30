import {readFileSync} from 'node:fs';
import {createKeys, keyName} from '../web/core/keys.js';
import {parse, format, createRouter, legacy} from '../web/core/router.js';
import {createWords, pick} from '../web/core/i18n.js';
import {parts} from '../web/core/fold.js';
import {clock} from './fake.js';
import {test, eq, ok, throws, run} from './check.js';

const press = (key, more = {}) => ({key, ...more});

test('keyName spells presses as the table does', () => {
  eq([
    keyName(press('n')), keyName(press('D', {shiftKey: true})), keyName(press('k', {metaKey: true})), keyName(press('k', {ctrlKey: true})),
    keyName(press('?', {shiftKey: true})), keyName(press('Escape')), keyName(press(' ')), keyName(press('Enter', {metaKey: true})),
    keyName(press('Tab', {shiftKey: true})), keyName(press('x', {altKey: true})), keyName(press('Shift')),
    keyName(press('；')), keyName(press('，')), keyName(press('？')), keyName(press('、')),
  ], ['n', 'Shift+D', 'Mod+K', 'Mod+K', '?', 'Esc', 'Space', 'Mod+Enter', 'Shift+Tab', '', '', ';', ',', '?', '/'], 'names');
});

test('the first scope with the key runs it, modal before page before global', () => {
  const keys = createKeys({timers: clock()});
  const ran = [];
  keys.push('global', [{key: 'n', run: () => ran.push('global n')}, {key: '?', run: () => ran.push('help')}]);
  const page = keys.push('page', [{key: 'n', run: () => ran.push('page n')}]);
  keys.handle(press('n'));
  const modal = keys.push('modal', [{key: 'Esc', run: () => ran.push('close')}], {blocks: true});
  keys.handle(press('n'));
  keys.handle(press('Escape'));
  modal();
  page();
  keys.handle(press('n'));
  keys.handle(press('？'));
  eq(ran, ['page n', 'close', 'global n', 'help'], 'ran');
});

test('a binding whose when is false leaves the key to the scopes below', () => {
  const keys = createKeys({timers: clock()});
  const ran = [];
  keys.push('global', [{key: 'x', run: () => ran.push('global')}]);
  keys.push('list', [{key: 'x', when: () => false, run: () => ran.push('list')}]);
  eq([keys.handle(press('x')), ran], [true, ['global']], 'x');
});

test('g then a key, within the wait', async () => {
  const clk = clock();
  const keys = createKeys({timers: clk});
  const ran = [];
  keys.push('global', [{key: 'g h', run: () => ran.push('home')}, {key: 'g t', run: () => ran.push('tasks')}, {key: 'h', run: () => ran.push('h')}]);
  keys.handle(press('g'));
  keys.handle(press('h'));
  keys.handle(press('g'));
  await clk.advance(1200);
  keys.handle(press('h'));
  eq(ran, ['home', 'h'], 'ran');
});

test('typing and composing are left alone, except Esc and Mod+Enter in an input', () => {
  const keys = createKeys({timers: clock()});
  const ran = [];
  keys.push('global', [{key: 'n', run: () => ran.push('n')}, {key: 'Esc', run: () => ran.push('esc')}, {key: 'Mod+Enter', run: () => ran.push('send')}]);
  const input = {tagName: 'INPUT'};
  keys.handle(press('n', {target: input}));
  keys.handle(press('n', {isComposing: true}));
  keys.handle(press('Process', {keyCode: 229}));
  keys.handle(press('Escape', {target: input}));
  keys.handle(press('Enter', {metaKey: true, target: {tagName: 'DIV', isContentEditable: true}}));
  eq(ran, ['esc', 'send'], 'ran');
});

test('a scope holds a key once', async () => {
  const keys = createKeys({timers: clock()});
  await throws(() => keys.push('page', [{key: 'n', run() {}}, {key: 'n', run() {}}]), /n twice/);
  await throws(() => keys.push('page', [{key: 'g', run() {}}, {key: 'g h', run() {}}]), /both a key and the start/);
  await throws(() => keys.push('page', [{id: 'x', run() {}}]), /no key/);
  await throws(() => keys.push('sheet', []), /no level/);
});

test('the router maps addresses to routes and back', () => {
  const cases = [
    ['', '', {page: 'home'}, '/'],
    ['?page=home', '', {page: 'home'}, '/'],
    ['?page=tasks&task=t1&view=board', '', {page: 'tasks', view: 'board', task: 't1'}, '?page=tasks&task=t1&view=board'],
    ['?page=tasks&view=nope', '', {page: 'tasks', view: 'list'}, '?page=tasks'],
    ['?page=runs', '', {page: 'runs'}, '?page=runs'],
    ['?page=runs&run=r2', '', {page: 'runs', run: 'r2'}, '?page=runs&run=r2'],
    ['?page=machines', '', {page: 'machines'}, '?page=machines'],
    ['?page=nope', '', {page: 'home'}, '/'],
    ['?page=inbox', '', {page: 'home'}, '/'],
    ['?page=settings', '', {page: 'me'}, '?page=me'],
    ['?page=projects', '', {page: 'team'}, '?page=team'],
    ['', '#task-t%2F9', {page: 'tasks', view: 'list', task: 't/9'}, '?page=tasks&task=t%2F9'],
    ['', '#device-ABCD-1234', {page: 'home', auth: {kind: 'device', value: 'ABCD-1234'}}, '/'],
    ['?page=runs', '#invite-s3cr3t', {page: 'runs', auth: {kind: 'invite', value: 's3cr3t'}}, '?page=runs'],
    ['', '#signin-not_admitted?provider=github&username=u&verified=1',
      {page: 'home', auth: {kind: 'signin', value: 'not_admitted', params: {provider: 'github', username: 'u', verified: '1'}}}, '/'],
    ['', '#other-x', {page: 'home'}, '/'],
    ['?wait=t%2F9', '', {page: 'home', wait: 't/9'}, '?wait=t%2F9'],
    ['?page=tasks&wait=t1', '', {page: 'tasks', view: 'list'}, '?page=tasks'],
    ['?page=tasks', '#wait-t%2F9', {page: 'home', wait: 't/9'}, '?wait=t%2F9'],
  ];
  for (const [search, hash, route, url] of cases) {
    eq(parse(search, hash), route, `${search}${hash}`);
    eq(format(route), url, `format ${search}${hash}`);
  }
  eq(legacy, {inbox: 'home', settings: 'me', projects: 'team'}, 'the old pages');
});

test('the router pushes addresses and follows the history', () => {
  const location = {search: '?page=runs', hash: '#invite-s'}, pushed = [];
  const history = {pushState: (_, __, u) => pushed.push(['push', u]), replaceState: (_, __, u) => pushed.push(['replace', u])};
  const r = createRouter({location, history});
  eq(r.route.value, {page: 'runs', auth: {kind: 'invite', value: 's'}}, 'first');
  r.go({page: 'tasks', task: 't1', auth: {kind: 'invite', value: 's'}});
  r.go({page: 'home'}, {replace: true});
  eq(pushed, [['push', '?page=tasks&task=t1'], ['replace', '/']], 'history');
  location.search = '?page=me';
  location.hash = '';
  r.popped();
  eq(r.route.value, {page: 'me'}, 'popped');
});

// fakeHistory is a page's address and history: the entries, where it stands, and the state of each.
function fakeHistory(search = '', hash = '') {
  const location = {search, hash};
  const entries = [{url: search + hash, state: null}];
  let at = 0;
  const set = url => { const [q, h = ''] = url.split('#'); location.search = q === '/' ? '' : q; location.hash = h ? '#' + h : ''; };
  const history = {
    get state() { return entries[at].state; },
    pushState(state, _, url) { entries.splice(at + 1); entries.push({url, state}); at++; set(url); },
    replaceState(state, _, url) { entries[at] = {url, state}; set(url); },
    back() { at--; set(entries[at].url); },
  };
  return {location, history, urls: () => entries.map(e => e.url), at: () => at};
}

test('a notice lands on what waits, with the list of what waits behind it', () => {
  const h = fakeHistory('', '#wait-t1');
  const r = createRouter(h);
  eq([r.route.value, h.urls(), h.at()], [{page: 'home', wait: 't1'}, ['/', '?wait=t1'], 1], 'a page opened on it');
  r.back({page: 'home'});
  r.popped();
  eq([r.route.value, h.at()], [{page: 'home'}, 0], 'back to what waits');

  const open = fakeHistory('?page=runs');
  const o = createRouter(open);
  o.open('#wait-t2');
  eq([o.route.value, open.urls()], [{page: 'home', wait: 't2'}, ['?page=runs', '/', '?wait=t2']], 'a page open elsewhere');
  o.open('#wait-t3');
  eq(open.urls(), ['?page=runs', '/', '?wait=t2', '/', '?wait=t3'], 'from one it waits on to another');
  const home = fakeHistory('');
  const m = createRouter(home);
  m.open('#wait-t2');
  eq(home.urls(), ['', '?wait=t2'], 'a page on what waits');
  m.open('#task-t9');
  eq([m.route.value.task, home.urls().at(-1)], ['t9', '/#task-t9'], 'another link');

  const fresh = fakeHistory('?wait=t1');
  const f = createRouter(fresh);
  f.back({page: 'home'});
  eq([f.route.value, fresh.urls()], [{page: 'home'}, ['/']], 'nothing of the page behind it: back goes to the list in its place');
});

test('words: one owner per key, both languages, the same verbs', async () => {
  const w = createWords();
  w.register('home', {greet: ['你好，%s', 'Hello, %s'], count: ['%d 个任务', '%d tasks']});
  await throws(() => w.register('tasks', {greet: ['嗨', 'Hi']}), /tasks defines greet, which home has/);
  await throws(() => w.register('tasks', {half: ['只有中文']}), /zh and an en/);
  await throws(() => w.register('tasks', {mixed: ['%s 的 %d', '%d of %s']}), /different verbs/);
  eq([w.t('greet'), w.f('greet', 'Ann'), w.f('count', 3), w.t('missing')], ['你好，%s', '你好，Ann', '3 个任务', 'missing'], 'zh');
  w.lang.value = 'en';
  eq([w.f('greet', 'Ann'), w.moduleOf('count')], ['Hello, Ann', 'home'], 'en');
  eq([pick('zh-CN'), pick('en-GB'), pick('')], ['zh', 'en', 'en'], 'pick');
});

test('words: a {one|other} takes its form from the %d before it', () => {
  const w = createWords();
  w.register('home', {
    files: ['改了 %d 个文件', 'changed %d {file|files}'],
    two: ['%s：%d 个任务等你，%d 次运行', '%s: %d {task|tasks} {needs|need} you, %d {run|runs}'],
  });
  w.lang.value = 'en';
  eq([w.f('files', 1), w.f('files', 0), w.f('files', 2), w.f('two', 'a', 1, 3), w.f('two', 'a', 2, 1)],
    ['changed 1 file', 'changed 0 files', 'changed 2 files', 'a: 1 task needs you, 3 runs', 'a: 2 tasks need you, 1 run'], 'en');
  w.lang.value = 'zh';
  eq(w.f('files', 1), '改了 1 个文件', 'zh');
});

test('every event fold takes names the tables it changes', () => {
  const src = readFileSync(new URL('../web/core/fold.js', import.meta.url), 'utf8');
  const cases = [...src.matchAll(/^ {4}case '([a-z_]+)':/gm)].map(m => m[1]);
  ok(cases.length > 20, `${cases.length} cases`);
  eq(cases.filter(c => !parts[c]), [], 'events without tables');
  eq(Object.keys(parts).filter(p => !cases.includes(p)), [], 'tables for events fold does not take');
});

run();
