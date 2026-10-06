// me_test draws the me page in both forms and both languages from the /api answers in api.json, and drives it in a
// fake document: returning from linking a sign-in account, the look chosen and kept, browser notices turned on and
// shown only for what is new while the page is hidden, the webhook saved, a token made and shown once, a token
// revoked and a session ended after asking, every other session ended, and a phone showing the account, the ways
// into the team and agent pages and signing out.
process.env.TZ = 'UTC';
import {readFileSync} from 'node:fs';
import {render} from '../web/vendor/preact.mjs';
import {act} from './vendor/test-utils.mjs';
import {createKeys} from '../web/core/keys.js';
import {createRouter} from '../web/core/router.js';
import {form, createNav} from '../web/core/layout.js';
import {createPrefs} from '../web/core/prefs.js';
import {words} from '../web/core/i18n.js';
import {html, KeysContext} from '../web/ui/base.js';
import {createToasts} from '../web/core/toasts.js';
import {createCommands} from '../web/core/commands.js';
import {noInbox} from '../web/core/store.js';
import {signal} from '../web/vendor/signals-core.mjs';
import {Root} from '../web/pages/boot.js';
import {App} from '../web/pages/app.js';
import {install} from './dom.js';
import {settle, server, clock} from './fake.js';
import {NOW, rig} from './rig.js';
import {test, eq, ok, run, until} from './check.js';

const api = JSON.parse(readFileSync(new URL('./api.json', import.meta.url), 'utf8'));
const bo = {id: 'u_b', name: 'Bo Lin', email: 'bo@example.com', username: 'bo', role: 'member', session: 'w1', joined: '2026-08-03T09:00:00Z'};

function memory(init = {}) {
  const m = new Map(Object.entries(init));
  return {getItem: k => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: k => m.delete(k), map: m};
}

// fakeHTTP answers from api.json and keeps what the page asked, as [method path, body]; refuse fails the calls it
// names with 500 internal.
function fakeHTTP({refuse = []} = {}) {
  const calls = [];
  const ans = (key, body) => {
    calls.push(body === undefined ? [key] : [key, body]);
    if (refuse.includes(key)) return Promise.reject(Object.assign(new Error('500'), {status: 500, code: 'internal'}));
    return Promise.resolve(structuredClone(api[key] ?? null));
  };
  return {
    calls,
    devices: () => ans('GET /api/push/devices'),
    setPrefs: (id, prefs) => ans('POST /api/push/prefs', {id, prefs}),
    removeDevice: id => ans('DELETE /api/push/devices', {id}),
    users: () => ans('GET /api/users'),
    logins: () => ans('GET /auth/logins'),
    tokens: () => ans('GET /api/tokens'),
    addToken: name => ans('POST /api/tokens', {name}),
    revokeToken: id => ans('DELETE /api/tokens', {id}),
    identities: () => ans('GET /api/identities'),
    unlinkIdentity: ({provider, issuer, subject}) => ans('DELETE /api/identities', {provider, issuer, subject}),
    webhook: () => ans('GET /api/me/webhook').then(v => v?.url || ''),
    setWebhook: url => ans('POST /api/me/webhook', {url}),
    testWebhook: () => ans('POST /api/me/webhook/test'),
    trackerAccounts: () => ans('GET /api/me/trackers'),
    presets: () => ans('GET /theme/presets.json'),
    logout: () => ans('POST /logout'),
  };
}

function fakeHistory(url) {
  const loc = {pathname: '/', search: '', hash: '', protocol: 'http:', host: 'tend.test'};
  const set = u => { const x = new URL(u, 'http://tend.test'); loc.pathname = x.pathname; loc.search = x.search; loc.hash = x.hash; };
  set(url);
  return {location: loc, history: {pushState: (_, __, u) => set(u), replaceState: (_, __, u) => set(u)}};
}

// root mounts the page as boot does once it knows who is signed in.
async function root({url = '/', session = bo, tab = memory(), f = 'desktop'} = {}) {
  const clk = clock(), srv = server(clk);
  const keys = createKeys({timers: clk});
  const {location, history} = fakeHistory(url);
  const router = createRouter({location, history});
  const noStore = {getItem: () => null, setItem() {}};
  const http = fakeHTTP();
  form.value = f;
  const el = install();
  const doc = el.ownerDocument;
  doc.addEventListener = () => {};
  doc.visibilityState = 'visible';
  const props = {http, router, keys, nav: createNav({storage: noStore, width: 1440}), prefs: createPrefs({storage: noStore, asked: 'zh'}),
    first: session, open: srv.open, location, history, clock: () => NOW, doc, storage: memory(), tab};
  await act(() => render(html`<${KeysContext.Provider} value=${keys}><${Root} ...${props} /><//>`, el));
  await act(() => settle());
  return {el, router, http, location, done: () => act(() => render(null, el))};
}

const css = ['base.css', 'components.css', 'pages.css'].map(f => readFileSync(new URL(`../web/css/${f}`, import.meta.url), 'utf8')).join('\n');
const cssClasses = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map(m => m[1]));
const press = (key, more = {}) => ({key, target: globalThis.document?.body, preventDefault() {}, ...more});
const settled = () => act(() => settle());
const click = el => act(() => el.dispatch('click'));
const type = (el, text) => act(() => { el.value = text; el.dispatch('input'); });
const labelOf = b => b.textContent.slice(0, b.textContent.length - b.find('kbd').map(k => k.textContent).join('').length).trim();
const buttonOf = (root, label) => {
  const got = root.find('button').filter(b => labelOf(b) === label);
  if (got.length !== 1) throw new Error(`${label}: ${got.length} buttons`);
  return got[0];
};
const valueOf = i => i.value ?? i.getAttribute('value');
const rowOf = (root, text) => {
  const got = root.find('.me-row').filter(r => r.textContent.includes(text));
  if (got.length !== 1) throw new Error(`${text}: ${got.length} rows`);
  return got[0];
};

// browser is a fake of the browser's notices: the permission it holds, what asking it answers, and what it showed.
function browser(permission = 'default', answer = 'granted') {
  const shown = [];
  class Notification {
    constructor(title, opts) { this.title = title; this.opts = opts; shown.push(this); }
    close() {}
    static requestPermission() { Notification.permission = answer; return Promise.resolve(answer); }
  }
  Notification.permission = permission;
  return {notices: {Notification, secure: true}, shown};
}

// fakePush is this browser's push (core/push.js) standing as state, turned on to on (the browser's answer), and
// fail the error turning it on meets; did is what the page asked of it.
function fakePush(state = 'none', {on = 'on', fail = null, device = 'd_mac'} = {}) {
  const did = [];
  return {did, state: async () => state, device: async () => (state === 'on' ? device : ''),
    on: () => { did.push('on'); return fail ? Promise.reject(fail) : Promise.resolve(state = on); },
    off: () => { did.push('off'); return Promise.resolve(state = 'off'); },
    clear: inbox => { did.push(['clear', inbox === noInbox ? 'none' : inbox.map(x => x.task)]); }};
}

// app mounts the signed-in page on the me page with a store no server feeds; its inbox is the test's to set.
async function app({f = 'desktop', lang = 'zh', session = bo, http = fakeHTTP(), notices = browser().notices, doc = {visibilityState: 'visible'},
  prefsStore = memory(), tab = memory(), url = '/?page=me', platform = {kind: 'browser', os: 'other', name: 'Mac', secure: true}, push = fakePush()} = {}) {
  const r = rig();
  const keys = createKeys({timers: r.clk});
  const toasts = createToasts({timers: r.clk});
  const commands = createCommands({wire: r.wire, newID: () => 'c1'});
  const {location, history} = fakeHistory(url);
  const router = createRouter({location, history});
  const prefs = createPrefs({storage: prefsStore, asked: 'zh'});
  const noStore = {getItem: () => null, setItem() {}};
  const out = {logouts: 0, copied: []};
  form.value = f;
  words.lang.value = lang;
  const el = install();
  const props = {store: r.store, commands, toasts, wire: r.wire, http, router, keys, nav: createNav({storage: noStore, width: 1440}), prefs, session,
    names: signal({u_b: 'Bo Lin'}), clock: () => NOW, fetchOutput: () => Promise.resolve({events: []}), storage: memory(),
    copy: text => { out.copied.push(text); return Promise.resolve(); }, onLogout() { out.logouts++; }, notices, doc, tab, platform, push};
  await act(() => render(html`<${KeysContext.Provider} value=${keys}><${App} ...${props} /><//>`, el));
  await settled();
  return Object.assign(out, {el, r, keys, router, http, prefs, prefsStore, tab, done: async () => {
    await act(() => render(null, el));
    form.value = 'desktop';
    words.lang.value = 'zh';
  }});
}

test('the page draws in both forms and both languages, styled and worded', async () => {
  for (const f of ['desktop', 'phone']) for (const lang of ['zh', 'en']) {
    const a = await app({f, lang});
    if (f === 'desktop') await until(() => a.el.textContent.includes('ci-bot') && a.el.textContent.includes('Company SSO') && a.el.find('.me-skin').length === 4, 'what the page reads');
    const classes = new Set(a.el.all().flatMap(e => e.className.split(' ').filter(Boolean)));
    eq([...classes].filter(c => !cssClasses.has(c)), [], `${f}/${lang}: classes without a rule`);
    eq(a.el.textContent.match(/\bme\.[a-zA-Z.]+\b/g), null, `${f}/${lang}: words not found`);
    const text = a.el.textContent;
    if (f === 'phone') {
      ok(text.includes('Bo Lin') && text.includes(words.t('me.team')) && text.includes(words.t('me.agentsNote')) && text.includes(words.t('app.logout')),
        `${lang}: the account, the team, the agents and signing out`);
      eq([a.el.find('.me-skin').length, text.includes(words.t('me.newToken')), text.includes(words.t('me.webhookSave'))], [0, false, false], `${lang}: nothing managed on a computer`);
      eq(a.http.calls, [['GET /api/push/devices']], `${lang}: a phone reads only the devices`);
    } else {
      for (const k of ['me.look', 'me.notices', 'me.logins', 'me.tokens', 'me.sessions']) ok(text.includes(words.t(k)), `${lang}: ${k}`);
      eq(a.el.find('.me-preview-row').map(r => r.find('.st')[0].className), ['st s-unknown', 'st s-running', 'st s-failed'], `${lang}: the look previewed in the status colours`);
      ok(text.includes('bo@example.com · ' + words.t('role.member') + ' · ' + words.f('me.joined', '8-03')), `${lang}: who, and since when`);
      ok(text.includes(words.f('me.viaToken', 'login:tend on mba')), `${lang}: a session a token signed in`);
      const trackers = a.el.find('.me-row').filter(r => r.textContent.endsWith('github.com') || r.textContent.endsWith('git.example.com'));
      eq(trackers.map(r => r.textContent.includes(words.t('me.trackerUnmatched'))), [false, true], `${lang}: matched on one tracker, not on the other`);
      ok(text.includes(words.f('me.viaDevice', 'Android')), `${lang}: a session another device allowed`);
      eq(rowOf(a.el, words.t('me.thisBrowser')).find('button').length, 0, `${lang}: this browser signs out at the top`);
      eq(a.el.find('.me-link').map(l => [l.textContent, l.getAttribute('href')]), [[words.t('me.link'), '/auth/github/start?link=1']], `${lang}: only what is not linked links`);
    }
    await a.done();
  }
});

test('the look: a skin, an accent, the contrast, the density and the language apply at once and are kept', async () => {
  const a = await app();
  await until(() => a.el.find('.me-skin').length === 4, 'the presets');
  await click(a.el.find('.me-skin').find(b => b.textContent.includes('ember')));
  eq(a.prefs.skin.value, 'ember', 'a preset');
  const accent = a.el.find('.me-sets')[0].find('input')[0];
  await type(accent, '#AA3300');
  eq(a.prefs.skin.value, 'ember-aa3300', 'an accent of its own');
  await type(accent, '#zz');
  eq([a.prefs.skin.value, a.el.textContent.includes(words.t('me.accentBad'))], ['ember-aa3300', true], 'one not well formed says so and changes nothing');
  await type(accent, '');
  eq(a.prefs.skin.value, 'ember', 'emptied: the skin\'s own');
  await click(a.el.find('[role=radio]').find(b => b.textContent === words.t('me.contrast.high')));
  await click(a.el.find('[role=radio]').find(b => b.textContent === words.t('density.compact')));
  await click(a.el.find('[role=radio]').find(b => b.textContent === words.t('theme.dark')));
  eq([a.prefs.skin.value, a.prefs.density.value, a.prefs.theme.value, a.el.textContent.includes(words.f('me.rowHeight', 28))], ['ember-high', 'compact', 'dark', true], 'contrast, density and theme');
  eq(JSON.parse(a.prefsStore.getItem('tend-look')), {skin: 'ember', accent: '', high: true, density: 'compact'}, 'kept');
  await click(a.el.find('[role=radio]').find(b => b.textContent === 'English'));
  eq([a.prefs.lang.value, a.prefsStore.getItem('tend-lang')], ['en', 'en'], 'a language');
  await click(a.el.find('[role=radio]').find(b => b.textContent === words.t('me.lang.auto')));
  eq([a.prefs.langChoice.value, a.prefs.lang.value, a.prefsStore.getItem('tend-lang')], ['auto', 'zh', null], 'the browser\'s');
  await a.done();
});

const item = (task, reason, pending = []) => ({task, title: 'Task ' + task, reason, since: '2026-09-30T14:00:00Z', as: ['owner'], pending});

test('browser notices: turning them on asks the browser; only what is new while the page is hidden shows, and opens its task', async () => {
  const {notices, shown} = browser();
  const doc = {visibilityState: 'visible'};
  const a = await app({notices, doc});
  const onOff = () => a.el.find('[role=radiogroup]').find(g => g.getAttribute('aria-label') === words.t('me.browser'));
  ok(a.el.textContent.includes(words.t('me.browser.default')), 'the browser will ask');
  eq(onOff().find('[aria-checked=true]')[0].textContent, words.t('me.off'), 'off until the browser allows them');
  await click(onOff().find('button').find(b => b.textContent === words.t('me.on')));
  await settled();
  eq([notices.Notification.permission, a.prefs.notify.value, onOff().find('[aria-checked=true]')[0].textContent], ['granted', true, words.t('me.on')], 'asked, and on');
  const inbox = a.r.store.inbox;
  eq(inbox.value, noInbox, 'no inbox yet');
  const set = async v => { await act(() => { inbox.value = v; }); await settled(); };
  doc.visibilityState = 'hidden';
  await set([item('t1', 'asked', [{id: 'p1', kind: 'question', task: 't1', request: 'q1', version: 1}])]);
  eq(shown.length, 0, 'what waited already is not news');
  await set([item('t1', 'asked', [{id: 'p1', kind: 'question', task: 't1', request: 'q1', version: 1}]), item('t2', 'failed')]);
  eq(shown.map(n => [n.title, n.opts.tag, n.opts.body]), [['Task t2', 't2', words.t('why.failed')]], 'a new item');
  await set([item('t1', 'asked', [{id: 'p1', kind: 'question', task: 't1', request: 'q1', version: 1}]), item('t2', 'failed')]);
  eq(shown.length, 1, 'the same again is not');
  doc.visibilityState = 'visible';
  await set([item('t1', 'asked', [{id: 'p1', kind: 'question', task: 't1', request: 'q1', version: 1}]), item('t2', 'failed'), item('t3', 'accept')]);
  eq(shown.length, 1, 'none while the page is in sight');
  doc.visibilityState = 'hidden';
  await set([item('t1', 'asked', [{id: 'p2', kind: 'permission', task: 't1', request: 'q2', version: 2}]), item('t2', 'failed'), item('t3', 'accept')]);
  eq(shown.map(n => n.title), ['Task t2', 'Task t1'], 'a new request on a task already waiting');
  await act(() => shown[1].onclick());
  eq([a.router.route.value.page, a.router.route.value.task], ['tasks', 't1'], 'a click opens its task');
  await act(() => a.router.go({page: 'me'}));
  await click(onOff().find('button').find(b => b.textContent === words.t('me.off')));
  await set([item('t4', 'failed')]);
  eq([shown.length, a.prefsStore.getItem('tend-notify')], [2, 'off'], 'turned off');
  await a.done();
});

test('browser notices: refused or not HTTPS, the page says so and offers no switch', async () => {
  for (const [notices, key] of [[browser('denied').notices, 'me.browser.denied'], [{...browser().notices, secure: false}, 'me.browser.insecure'], [{secure: true}, 'me.browser.none']]) {
    const a = await app({notices});
    ok(a.el.textContent.includes(words.t(key)), key);
    eq(a.el.find('[role=radiogroup]').filter(g => g.getAttribute('aria-label') === words.t('me.browser')).length, 0, key + ': no switch');
    await a.done();
  }
});

test('the webhook is saved as typed, and removed when emptied', async () => {
  const a = await app();
  await until(() => a.el.find('input').some(i => valueOf(i) === 'https://ntfy.example.com/tend-bo'), 'the webhook');
  const input = a.el.find('input').find(i => valueOf(i) === 'https://ntfy.example.com/tend-bo');
  const save = () => buttonOf(a.el, words.t('me.webhookSave'));
  ok(save().disabled, 'nothing to save');
  const test = () => buttonOf(a.el, words.t('me.webhookTest'));
  await click(test());
  await settled();
  eq(a.http.calls.at(-1), ['POST /api/me/webhook/test'], 'a test goes to the saved one');
  ok(a.el.textContent.includes(words.f('me.webhookRefused', '404')), 'what it answered');
  await type(input, ' https://ntfy.example.com/bo2 ');
  ok(test().disabled, 'not while an address is unsaved');
  await click(save());
  await settled();
  eq(a.http.calls.at(-1), ['POST /api/me/webhook', {url: 'https://ntfy.example.com/bo2'}], 'saved trimmed');
  ok(a.el.textContent.includes(words.t('me.webhookSaved')), 'said');
  await type(input, '');
  await click(save());
  await settled();
  eq(a.http.calls.at(-1), ['POST /api/me/webhook', {url: ''}], 'removed');
  ok(a.el.textContent.includes(words.t('me.webhookGone')), 'said so');
  await a.done();
});

test('a token made shows once; revoking one asks first', async () => {
  const a = await app();
  await until(() => a.el.textContent.includes('ci-bot'), 'the tokens');
  await click(buttonOf(a.el, words.t('me.newToken')));
  const make = () => buttonOf(a.el.one('.modal-foot'), words.t('me.make'));
  ok(make().disabled, 'a name first');
  await type(a.el.one('.modal').find('input')[0], ' laptop tui ');
  await click(make());
  await settled();
  eq(a.http.calls.filter(c => c[0] === 'POST /api/tokens'), [['POST /api/tokens', {name: 'laptop tui'}]], 'made');
  ok(a.el.one('.modal').textContent.includes('tend_c_preview0000000000'), 'its value, once');
  await click(buttonOf(a.el.one('.modal'), words.t('secret.copy')));
  eq(a.copied, ['tend_c_preview0000000000'], 'copied');
  await click(buttonOf(a.el.one('.modal-foot'), words.t('me.done')));
  eq([a.el.find('.modal').length, a.el.textContent.includes('tend_c_preview0000000000')], [0, false], 'gone once closed');
  eq(a.http.calls.filter(c => c[0] === 'GET /api/tokens').length, 2, 'read again');
  await click(buttonOf(rowOf(a.el, 'ci-bot'), words.t('me.revoke')));
  ok(a.el.one('.modal').textContent.includes(words.t('me.revokeNote')), 'what revoking does');
  await act(() => { a.keys.handle(press('Escape')); });
  eq([a.el.find('.modal').length, a.http.calls.filter(c => c[0] === 'DELETE /api/tokens').length], [0, 0], 'Esc keeps it');
  await click(buttonOf(rowOf(a.el, 'ci-bot'), words.t('me.revoke')));
  await click(buttonOf(a.el.one('.modal-foot'), words.t('me.revoke')));
  await settled();
  eq(a.http.calls.filter(c => c[0] === 'DELETE /api/tokens'), [['DELETE /api/tokens', {id: 'k2'}]], 'revoked');
  eq(a.http.calls.filter(c => c[0] === 'GET /api/push/devices').length, 2, 'the devices read again: the sessions it signed in went');
  ok(a.el.textContent.includes(words.f('me.revoked', 'ci-bot')), 'said');
  await a.done();
});

test('a session is signed out after asking; so are all the others, this one kept', async () => {
  const a = await app();
  await until(() => a.el.textContent.includes(words.t('me.thisBrowser')), 'the sessions');
  const rows = () => a.el.find('.me-row').filter(r => r.textContent.includes(words.t('me.signedIn')));
  const reads = () => a.http.calls.filter(c => c[0] === 'GET /api/push/devices').length;
  await until(() => reads() > 0, 'the devices');
  eq(rows().length, 1, 'one signed in by a browser besides this one');
  ok(rows()[0].textContent.includes('Safari · iPhone') && rows()[0].textContent.includes('100.64.0.9'), 'its browser and where it was last used from');
  ok(a.el.find('.me-row').some(r => r.textContent.includes('login:tend on mba') && r.textContent.includes('100.64.0.5')), 'a token\'s last address');
  await click(buttonOf(rows()[0], words.t('me.end')));
  ok(a.el.one('.modal').textContent.includes(words.t('me.endNote')), 'its pushes stop too');
  await click(buttonOf(a.el.one('.modal-foot'), words.t('me.end')));
  await settled();
  eq([a.http.calls.filter(c => c[0] === 'DELETE /api/tokens'), reads()], [[['DELETE /api/tokens', {id: 'w3'}]], 2], 'the one picked; the devices read again');
  await click(buttonOf(a.el, words.t('me.endOthers')));
  ok(a.el.one('.modal').textContent.includes(words.f('me.endOthersTitle', 3)), 'how many');
  await click(buttonOf(a.el.one('.modal-foot'), words.t('me.endOthers')));
  await settled();
  eq(a.http.calls.filter(c => c[0] === 'DELETE /api/tokens').slice(1), [['DELETE /api/tokens', {id: 'w2'}], ['DELETE /api/tokens', {id: 'w3'}],
    ['DELETE /api/tokens', {id: 'w4'}]], 'every other, not this one');
  ok(a.el.textContent.includes(words.f('me.endedN', 3)), 'said');
  eq(reads(), 3, 'the devices read again');
  await a.done();
});

test('an account says when it last signed in, and is unlinked after asking', async () => {
  const a = await app();
  const rows = () => a.el.find('.me-row').filter(r => r.textContent.includes(words.t('me.unlink')));
  await until(() => rows().length === 2, 'both accounts');
  ok(rows()[0].textContent.includes(words.f('me.lastLogin', '9-30 08:55')), 'the last sign-in');
  ok(rows()[1].textContent.includes(words.f('me.linkedAt', '9-20 09:00')), 'linked, never signed in with');
  await click(buttonOf(rows()[1], words.t('me.unlink')));
  ok(a.el.one('.modal').textContent.includes(words.t('me.unlinkNote')), 'what it means');
  await click(buttonOf(a.el.one('.modal-foot'), words.t('me.unlink')));
  await settled();
  eq(a.http.calls.filter(c => c[0] === 'DELETE /api/identities'), [['DELETE /api/identities', {provider: 'gitea', issuer: 'https://git.example.com', subject: '17'}]], 'that one');
  eq(a.http.calls.filter(c => c[0] === 'GET /api/identities').length, 2, 'read again');
  await a.done();
});

test('linking an account goes to the server and remembers it in this tab', async () => {
  const a = await app();
  await until(() => a.el.find('.me-link').length === 1, 'the link');
  await click(a.el.one('.me-link'));
  eq(a.tab.getItem('tend-linking'), String(NOW), 'remembered');
  await a.done();
});

// switchOf is the on/off setting of this device labelled key.
const switchOf = (el, key) => el.find('[role=switch]').find(b => b.getAttribute('aria-label') === words.t(key));
const on = b => b.getAttribute('aria-checked') === 'true';

test("this device's settings go to the server as they change, one refused is put back; the devices, this one marked, another removed", async () => {
  for (const f of ['desktop', 'phone']) {
    const http = fakeHTTP();
    const a = await app({f, http, push: fakePush('on'), platform: {kind: 'pwa', os: 'android', name: 'Android', secure: true}});
    await until(() => switchOf(a.el, 'me.dev.waiting'), `${f}: the settings`);
    eq(['me.dev.waiting', 'me.dev.done', 'me.dev.treeDone', 'me.dev.hide'].map(k => on(switchOf(a.el, k))), [true, false, true, false], `${f}: the defaults`);
    const wait = a.el.find('[role=radiogroup]').find(g => g.getAttribute('aria-label') === words.t('me.dev.wait'));
    eq(wait.find('[aria-checked=true]')[0].textContent, words.t('me.dev.wait.0'), `${f}: the default wait`);
    await click(switchOf(a.el, 'me.dev.done'));
    await click(switchOf(a.el, 'me.dev.treeDone'));
    await click(switchOf(a.el, 'me.dev.waiting'));
    await click(switchOf(a.el, 'me.dev.hide'));
    await click(wait.find('button').find(b => b.textContent === words.t('me.dev.wait.-1')));
    await settled();
    eq(http.calls.filter(c => c[0] === 'POST /api/push/prefs').map(c => c[1]), [
      {id: 'd_mac', prefs: {events: ['task.needs_you', 'task.done', 'task.tree_done']}},
      {id: 'd_mac', prefs: {events: ['task.needs_you', 'task.done']}},
      {id: 'd_mac', prefs: {events: ['task.done']}},
      {id: 'd_mac', prefs: {events: ['task.done'], hide: true}},
      {id: 'd_mac', prefs: {events: ['task.done'], hide: true, wait: -1}},
    ], `${f}: each change sent`);
    eq(['me.dev.waiting', 'me.dev.done', 'me.dev.treeDone', 'me.dev.hide'].map(k => on(switchOf(a.el, k))), [false, true, false, true], `${f}: as changed`);

    const devices = a.el.find('.panel').find(p => p.textContent.includes(words.t('me.devices')));
    eq(devices.find('.me-row').map(r => [r.one('.me-main').find('span')[0].textContent, r.textContent.includes(words.t('me.dev.this'))]),
      [['Mac', true], ['Android', false], [words.t('me.dev.unnamed'), false]], `${f}: the devices, this one marked`);
    const devRow = name => devices.find('.me-row').find(r => r.one('.me-main').find('span')[0].textContent === name);
    ok(devRow(words.t('me.dev.unnamed')).textContent.includes(words.f('me.dev.failures', 2)), `${f}: its failures`);
    eq(devRow('Mac').find('button').length, 0, `${f}: this one is turned off with the switch`);
    await click(devRow('Android').one('button'));
    await settled();
    eq([http.calls.at(-1), devices.find('.me-row').length], [['DELETE /api/push/devices', {id: 'd_phone'}], 2], `${f}: another removed`);
    await a.done();
  }
  const refusing = fakeHTTP({refuse: ['POST /api/push/prefs']});
  const r = await app({http: refusing, push: fakePush('on')});
  await until(() => switchOf(r.el, 'me.dev.done'), 'the settings');
  await click(switchOf(r.el, 'me.dev.done'));
  await settled();
  eq([on(switchOf(r.el, 'me.dev.done')), r.el.textContent.includes(words.t('api.internal'))], [false, true], 'refused: put back and said');
  await r.done();
  const off = await app({push: fakePush('off')});
  await settled();
  eq([switchOf(off.el, 'me.dev.waiting'), off.el.textContent.includes(words.t('me.devices'))], [undefined, true], 'pushes off here: no settings, the devices still listed');
  await off.done();
});

test('a phone hands on the pages only a computer has: to the share sheet, else the clipboard', async () => {
  const shared = [];
  const platform = (share) => ({kind: 'pwa', os: 'android', name: 'Android', secure: true, link: path => 'https://tend.test' + path, share});
  const a = await app({f: 'phone', platform: platform(async url => { shared.push(url); return 'shared'; })});
  const panel = a.el.find('.panel').find(p => p.textContent.includes(words.t('me.desk')));
  eq(panel.find('.desk-row').map(b => b.one('.card-primary').textContent), ['me.desk.agents', 'me.desk.team', 'me.desk.trackers', 'me.desk.machines',
    'me.desk.tokens', 'me.desk.look'].map(k => words.t(k)), 'what a computer does');
  for (const b of panel.find('.desk-row')) await click(b);
  await settled();
  eq(shared, ['agents', 'team', 'team', 'machines', 'me', 'me'].map(p => 'https://tend.test/?page=' + p), 'shared');
  await a.done();
  for (const [page, key] of [['agents', 'ag.desktop'], ['machines', 'mach.desktop'], ['team', 'team.desktop']]) {
    const c = await app({f: 'phone', url: '/?page=' + page, platform: platform(async () => 'copied')});
    const desk = c.el.one('.desk');
    ok(desk.textContent.includes(words.t(key)), `${page}: what a computer does`);
    await click(desk.one('button'));
    await settled();
    ok(c.el.textContent.includes(words.t('desk.copied')), `${page}: copied`);
    await c.done();
  }
  const none = await app({f: 'phone', platform: platform(async () => { throw new Error('no clipboard'); })});
  await click(none.el.find('.desk-row')[0]);
  await settled();
  ok(none.el.textContent.includes(words.f('desk.failed', 'https://tend.test/?page=agents')), 'neither: the address to type');
  await none.done();
});

test('a phone: the account, the team and agent pages and signing out', async () => {
  const a = await app({f: 'phone'});
  await click(a.el.find('.card-row').find(b => b.textContent.includes(words.t('me.team'))));
  eq(a.router.route.value.page, 'team', 'the team page');
  await act(() => a.router.go({page: 'me'}));
  await click(a.el.find('.card-row').find(b => b.textContent.includes(words.t('me.agentsNote'))));
  eq(a.router.route.value.page, 'agents', 'the agent page');
  await act(() => a.router.go({page: 'me'}));
  await click(buttonOf(a.el, words.t('app.logout')));
  eq(a.logouts, 1, 'signed out');
  await a.done();
});

test('returning from linking an account opens the me page and says it is linked', async () => {
  const tab = memory({'tend-linking': String(NOW - 60e3)});
  const r = await root({tab});
  await until(() => r.el.textContent.includes(words.t('me.linked')), 'the linked notice');
  eq(r.router.route.value.page, 'me', 'the page');
  eq(tab.getItem('tend-linking'), null, 'said once');
  await r.done();
});

test('returning from linking an account someone else has says so, and drops the fragment', async () => {
  const tab = memory({'tend-linking': String(NOW - 60e3)});
  const r = await root({url: '/#signin-linked', tab});
  await until(() => r.el.textContent.includes(words.t('me.linkTaken')), 'the refusal');
  eq([r.router.route.value.page, r.location.hash], ['me', ''], 'the page and the address');
  await r.done();
});

test('a sign-in fragment without a link in flight, or long after one, is dropped as before', async () => {
  for (const tab of [memory(), memory({'tend-linking': String(NOW - 11 * 60e3)})]) {
    const r = await root({url: '/#signin-linked', tab});
    eq([r.router.route.value.page, r.location.hash], ['home', ''], 'the page and the address');
    ok(!r.el.textContent.includes(words.t('me.linkTaken')), 'no notice');
    eq(tab.getItem('tend-linking'), null, 'forgotten');
    await r.done();
  }
});

// This device: on a phone, how to put tend on the home screen where it is not there yet (Safari's three steps, the
// Android browser's menu), and on an address that is not HTTPS, why it cannot be and the two ways to make it so, on
// a computer too.
test('this device: installing to the home screen, and an address that is not HTTPS', async () => {
  const cases = [
    ['phone', 'ios', 'browser', true, ['me.installIOS', 'me.installIOS1', 'me.installIOS2', 'me.installIOS3'], ['me.insecure']],
    ['phone', 'android', 'browser', true, ['me.installAndroid'], ['me.installIOS', 'me.insecure']],
    ['phone', 'ios', 'pwa', true, [], ['me.installIOS', 'me.installAndroid', 'me.insecure']],
    ['phone', 'android', 'browser', false, ['me.insecure', 'me.insecureTailnet', 'me.insecureTLS'], ['me.installAndroid']],
    ['desktop', 'other', 'browser', false, ['me.insecure', 'me.insecureTailnet', 'me.insecureTLS'], []],
    ['desktop', 'other', 'browser', true, [], ['me.insecure', 'me.installAndroid', 'me.installIOS']],
  ];
  for (const lang of ['zh', 'en']) for (const [f, os, kind, secure, shown, hidden] of cases) {
    const a = await app({f, lang, platform: {kind, os, name: 'x', secure}});
    const text = a.el.textContent;
    for (const k of shown) ok(text.includes(words.t(k)), `${lang} ${f} ${os} ${kind} ${secure}: ${k}`);
    for (const k of hidden) ok(!text.includes(words.t(k)), `${lang} ${f} ${os} ${kind} ${secure}: no ${k}`);
    const classes = new Set(a.el.all().flatMap(e => e.className.split(' ').filter(Boolean)));
    eq([...classes].filter(c => !cssClasses.has(c)), [], `${lang} ${f} ${os} ${kind} ${secure}: classes without a rule`);
    await a.done();
  }
});

test('pushes to this device: on and off in both forms, and why they cannot be had here', async () => {
  for (const f of ['desktop', 'phone']) {
    const push = fakePush('off');
    const a = await app({f, push, platform: {kind: 'pwa', os: 'android', name: 'Android', secure: true}});
    const onOff = () => a.el.find('[role=radiogroup]').find(g => g.getAttribute('aria-label') === words.t('me.push'));
    ok(onOff(), `${f}: a switch`);
    eq(onOff().find('[aria-checked=true]')[0].textContent, words.t('me.off'), `${f}: off`);
    await click(onOff().find('button').find(b => b.textContent === words.t('me.on')));
    await settled();
    eq([push.did.filter(x => typeof x === 'string'), onOff().find('[aria-checked=true]')[0].textContent], [['on'], words.t('me.on')], `${f}: turned on`);
    await click(onOff().find('button').find(b => b.textContent === words.t('me.off')));
    await settled();
    eq(push.did.filter(x => typeof x === 'string'), ['on', 'off'], `${f}: turned off`);
    await a.done();
  }
  const refused = fakePush('off', {on: 'denied'});
  const r = await app({push: refused});
  await click(r.el.find('[role=radiogroup]').find(g => g.getAttribute('aria-label') === words.t('me.push')).find('button').find(b => b.textContent === words.t('me.on')));
  await settled();
  ok(r.el.textContent.includes(words.t('me.push.denied')), 'the browser refused');
  await r.done();
  const nokey = await app({push: fakePush('off', {fail: {status: 503, code: 'push_key'}})});
  await click(nokey.el.find('[role=radiogroup]').find(g => g.getAttribute('aria-label') === words.t('me.push')).find('button').find(b => b.textContent === words.t('me.on')));
  await settled();
  ok(nokey.el.textContent.includes(words.t('api.push_key')), 'a server without a push key');
  await nokey.done();
  for (const [state, platform, key] of [['none', {kind: 'browser', os: 'ios', name: 'iPhone', secure: true}, 'me.push.install'],
    ['none', {kind: 'browser', os: 'other', name: 'Mac', secure: true}, 'me.push.none'], ['denied', {kind: 'browser', os: 'other', name: 'Mac', secure: true}, 'me.push.denied'],
    ['none', {kind: 'browser', os: 'other', name: 'Mac', secure: false}, 'me.push.insecure']]) {
    for (const f of ['desktop', 'phone']) {
      const a = await app({f, push: fakePush(state), platform});
      ok(a.el.textContent.includes(words.t(key)), `${f}: ${key}`);
      eq(a.el.find('[role=radiogroup]').filter(g => g.getAttribute('aria-label') === words.t('me.push')).length, 0, `${f} ${key}: no switch`);
      await a.done();
    }
  }
});

test('the notices of what no longer waits close as the inbox changes', async () => {
  const push = fakePush('on');
  const a = await app({push});
  await act(() => { a.r.store.inbox.value = [item('t1', 'asked', [{id: 'p1', kind: 'question', task: 't1', request: 'q1', version: 1}])]; });
  await settled();
  eq(push.did.filter(x => typeof x !== 'string'), [['clear', 'none'], ['clear', ['t1']]], 'cleared by the inbox');
  await a.done();
});

await run();
