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

test('every event fold takes names the tables it changes', () => {
  const src = readFileSync(new URL('../web/core/fold.js', import.meta.url), 'utf8');
  const cases = [...src.matchAll(/^ {4}case '([a-z_]+)':/gm)].map(m => m[1]);
  ok(cases.length > 20, `${cases.length} cases`);
  eq(cases.filter(c => !parts[c]), [], 'events without tables');
  eq(Object.keys(parts).filter(p => !cases.includes(p)), [], 'tables for events fold does not take');
});

run();
