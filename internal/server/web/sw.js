// sw.js is the page's service worker: it keeps this build's own files (the page, its scripts and styles) so a phone
// opens tend at once, and shows the pushes tend-server sends. The API, the sockets, the skins and everything else go
// to the server as they are; there is no offline mode. A new build is a new sw.js: it installs beside the running one
// and waits, and the page tells it to take over (the "skip" message) when the server's hello names another build than
// the page's.
// ⚠️ tend-server writes the next line: the build and the files it keeps.
const BOOT = {"build":"","files":[]};
const CACHE = 'tend-' + BOOT.build;

self.addEventListener('install', e => {
  e.waitUntil(caches.open(CACHE).then(c => c.addAll(BOOT.files)));
});

self.addEventListener('activate', e => {
  e.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(k => k.startsWith('tend-') && k !== CACHE).map(k => caches.delete(k)))));
});

self.addEventListener('message', e => {
  if (e.data === 'skip') self.skipWaiting();
});

// keyOf is what a request is kept as: a visit to the page (whatever its query) is "/", a file of the build its path.
function keyOf(req) {
  if (req.method !== 'GET') return null;
  const u = new URL(req.url);
  if (u.origin !== self.location.origin) return null;
  if (req.mode === 'navigate') return u.pathname === '/' ? '/' : null;
  return BOOT.files.includes(u.pathname) && u.pathname !== '/' ? u.pathname : null;
}

self.addEventListener('fetch', e => {
  const key = keyOf(e.request);
  if (!key) return;
  e.respondWith(caches.open(CACHE).then(c => c.match(key)).then(hit => hit || fetch(e.request)));
});

// A push is what waits on the viewer about a task (tend-server's PushMessage): it always shows a notice (a browser
// takes the permission back from a worker that shows none), one per task, the newer taking the older's place, except
// that a tree done stays before its root's own done. A push
// with its content hidden says only how many things wait, all under one notice. What it says shows only once the
// page's session here is the person it is for (to): one the push service kept since they signed out, or since someone
// else signed in here, shows a plain notice. The words follow the browser's
// language: the worker cannot read the page's choice.
const WORDS = {
  zh: {
    permission: '要你允许：%s', permissionAny: '要你允许它用一个工具', question: 'agent 有问题问你', continue: '运行停在一个问题上，等你回复后接着跑',
    gate: '等你验收', ended: '跑完了，等你标完成', failed: '运行没成功，等你处理', waiting: '等你处理', more: '%s · %d 项等你',
    hidden: '%d 项等你', done: '任务完成了', tree: '完工了', treeDone: '完工', leaves: '%d/%d 项完成', canceled: '%d 项取消', runs: '%d 次运行', run: '1 次运行',
    took: '用时 %s', branch: '汇到 %s', plain: '打开 tend 查看', view: '查看', reject: '拒绝',
    rejected: '已拒绝', handledBy: '已被 %s 处理', gone: '已经不等你了', signIn: '请打开 tend 重新登录后处理', failedAct: '没能拒绝，打开 tend 处理',
  },
  en: {
    permission: 'Asks to run: %s', permissionAny: 'Asks to use a tool', question: 'The agent has a question for you', continue: 'Its run stopped on a question; reply to go on',
    gate: 'Waiting for you to accept it', ended: 'Its run ended; mark it done', failed: 'Its run did not succeed', waiting: 'Waiting for you', more: '%s · %d waiting on you',
    hidden: '%d waiting on you', done: 'The task is done', tree: 'A task tree is done', treeDone: 'All done', leaves: '%d of %d done', canceled: '%d canceled',
    runs: '%d runs', run: '1 run', took: 'took %s', branch: 'merged into %s', plain: 'Open tend to see it', view: 'View', reject: 'Deny',
    rejected: 'Denied', handledBy: 'Already handled by %s', gone: 'No longer waits on you', signIn: 'Open tend and sign in again to handle it', failedAct: 'Could not deny it; open tend to handle it',
  },
};

function words() {
  return /^zh\b/i.test(self.navigator?.language || '') ? WORDS.zh : WORDS.en;
}

const icon = '/icon-192.png';

// took is a span to the minute: 3h12m, 45m.
function took(ms) {
  const m = Math.floor(ms / 60000);
  return m >= 60 ? `${Math.floor(m / 60)}h${String(m % 60).padStart(2, '0')}m` : `${m}m`;
}

// summed is how a tree went (tend-server's task.TreeSummary) in one line.
function summed(w, s) {
  const fill = (text, ...vs) => vs.reduce((t, v) => t.replace(/%[ds]/, String(v)), text);
  const span = Date.parse(s.done_at) - Date.parse(s.started);
  return [w.treeDone, fill(w.leaves, s.done || 0, s.leaves || 0), s.canceled > 0 && fill(w.canceled, s.canceled), s.runs === 1 ? w.run : fill(w.runs, s.runs || 0),
    span > 0 && fill(w.took, took(span)), s.branch && fill(w.branch, s.branch)].filter(Boolean).join(' · ');
}

// noticeOf is how a push shows: its title, and the options of showNotification. A notice's data keeps what a click
// needs: the link to open and the token of its deny button.
function noticeOf(m) {
  const w = words();
  if (!m.task) {
    if (m.event === 'task.done' || m.event === 'task.tree_done') return ['tend', {body: m.event === 'task.done' ? w.done : w.tree, tag: 'tend-done', renotify: true, icon, data: {link: ''}}];
    return ['tend', {body: w.hidden.replace('%d', String(Math.max(1, m.n || 0))), tag: 'tend', renotify: true, icon, data: {count: true, link: ''}}];
  }
  if (m.event === 'task.tree_done') return [m.title, {body: m.summary ? summed(w, m.summary) : w.tree, tag: m.task, renotify: true, icon, data: {task: m.task, link: m.link, tree: true}}];
  if (m.event === 'task.done') return [m.title, {body: w.done, tag: m.task, renotify: true, icon, data: {task: m.task, link: m.link}}];
  const title = m.n > 1 ? w.more.replace('%s', m.title).replace('%d', String(m.n)) : m.title;
  const body = m.kind === 'permission' ? (m.what ? w.permission.replace('%s', m.what) : w.permissionAny) : w[m.kind] || w.waiting;
  const deny = (m.actions || []).find(a => a.action === 'reject' && a.token);
  const actions = [{action: 'view', title: w.view}, ...(deny ? [{action: 'reject', title: w.reject}] : [])];
  return [title, {body, tag: m.task, renotify: true, icon, actions, data: {task: m.task, item: m.item, link: m.link, ...(deny ? {act: deny.token} : {})}}];
}

// forViewer: m is for whoever is signed in here now; false when that cannot be told.
async function forViewer(m) {
  if (!m.to) return false;
  try {
    const res = await fetch('/session', {credentials: 'same-origin'});
    return res.ok && (await res.json()).id === m.to;
  } catch {
    return false;
  }
}

self.addEventListener('push', e => {
  let m = null;
  try { m = e.data?.json(); } catch {}
  e.waitUntil((async () => {
    let [title, opts] = m?.v !== 1 ? ['tend', {body: words().waiting, tag: 'tend', icon, data: {count: true, link: ''}}]
      : await forViewer(m) ? noticeOf(m) : ['tend', {body: words().plain, tag: 'tend', icon, data: {count: true, link: ''}}];
    if (m?.event === 'task.done') { // a tree's own done, come after its tree done, keeps that one in its place
      const tree = (await self.registration.getNotifications?.({tag: opts.tag}) || []).find(n => n.data?.tree);
      if (tree) [title, opts] = [tree.title, {body: tree.body, tag: tree.tag, renotify: false, icon, data: tree.data}];
    }
    await self.registration.showNotification(title, opts);
  })());
});

// open brings up a window of the page on link: one already open goes there, else a new one opens there.
function open(link) {
  return self.clients.matchAll({type: 'window', includeUncontrolled: true}).then(wins => {
    const win = wins.find(c => new URL(c.url).origin === self.location.origin);
    if (!win) return self.clients.openWindow('/' + link);
    win.postMessage({open: link});
    return win.focus();
  });
}

// deny sends the notice's token to /api/act with the page's cookie, then shows in its place how that went: done,
// handled by someone else first, no longer waiting, signed out, or not done (a click on that opens the page).
async function deny(n) {
  const w = words(), data = n.data || {};
  let body = w.failedAct;
  try {
    const res = await fetch('/api/act', {method: 'POST', credentials: 'same-origin', headers: {'Content-Type': 'application/json', 'X-Tend': '1'},
      body: JSON.stringify({token: data.act})});
    if (res.status === 204) body = w.rejected;
    else if (res.status === 401) body = w.signIn;
    else if (res.status === 409) {
      const out = await res.json().catch(() => ({}));
      body = out.by ? w.handledBy.replace('%s', out.by) : w.gone;
    }
  } catch {}
  await self.registration.showNotification(n.title, {body, tag: n.tag, icon, data: {task: data.task, link: data.link}});
}

// Clicking a notice or its view button opens what it is about; its deny button denies without opening anything.
self.addEventListener('notificationclick', e => {
  e.notification.close();
  if (e.action === 'reject' && e.notification.data?.act) {
    e.waitUntil(deny(e.notification));
    return;
  }
  e.waitUntil(open(e.notification.data?.link || ''));
});

// A browser that renews a subscription on its own asks the worker to keep it: it subscribes again with the same key
// and registers the new one in the page's stead.
self.addEventListener('pushsubscriptionchange', e => {
  const key = e.oldSubscription?.options?.applicationServerKey;
  e.waitUntil((async () => {
    const sub = e.newSubscription || (key && await self.registration.pushManager.subscribe({userVisibleOnly: true, applicationServerKey: key}));
    if (!sub) return;
    await fetch('/api/push/device', {method: 'PUT', credentials: 'same-origin', headers: {'Content-Type': 'application/json', 'X-Tend': '1'},
      body: JSON.stringify({subscription: sub.toJSON(), name: ''})});
  })().catch(() => {}));
});
