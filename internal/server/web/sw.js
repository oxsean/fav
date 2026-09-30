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
// takes the permission back from a worker that shows none), one per task, the newer taking the older's place. The
// words follow the browser's language: the worker cannot read the page's choice.
const WORDS = {
  zh: {
    permission: '要你允许：%s', permissionAny: '要你允许它用一个工具', question: 'agent 有问题问你', continue: '运行停在一个问题上，等你回复后接着跑',
    gate: '等你验收', ended: '跑完了，等你标完成', failed: '运行没成功，等你处理', waiting: '等你处理', more: '%s · %d 项等你',
  },
  en: {
    permission: 'Asks to run: %s', permissionAny: 'Asks to use a tool', question: 'The agent has a question for you', continue: 'Its run stopped on a question; reply to go on',
    gate: 'Waiting for you to accept it', ended: 'Its run ended; mark it done', failed: 'Its run did not succeed', waiting: 'Waiting for you', more: '%s · %d waiting on you',
  },
};

function words() {
  return /^zh\b/i.test(self.navigator?.language || '') ? WORDS.zh : WORDS.en;
}

// noticeOf is how a push shows: its title, and the options of showNotification.
function noticeOf(m) {
  const w = words();
  const title = m.n > 1 ? w.more.replace('%s', m.title).replace('%d', String(m.n)) : m.title;
  const body = m.kind === 'permission' ? (m.what ? w.permission.replace('%s', m.what) : w.permissionAny) : w[m.kind] || w.waiting;
  return [title, {body, tag: m.task, renotify: true, icon: '/icon-192.png', data: {task: m.task, item: m.item, link: m.link}}];
}

self.addEventListener('push', e => {
  let m = null;
  try { m = e.data?.json(); } catch {}
  const [title, opts] = m?.v === 1 && m.task ? noticeOf(m) : ['tend', {body: words().waiting, tag: 'tend'}];
  e.waitUntil(self.registration.showNotification(title, opts));
});

// Clicking a notice brings up a window of the page on what it is about: one already open goes there, else a new one
// opens there.
self.addEventListener('notificationclick', e => {
  e.notification.close();
  const link = e.notification.data?.link || '';
  e.waitUntil(self.clients.matchAll({type: 'window', includeUncontrolled: true}).then(wins => {
    const win = wins.find(c => new URL(c.url).origin === self.location.origin);
    if (!win) return self.clients.openWindow('/' + link);
    win.postMessage({open: link});
    return win.focus();
  }));
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
