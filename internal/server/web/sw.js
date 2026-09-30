// sw.js is the page's service worker: it keeps this build's own files (the page, its scripts and styles) so a phone
// opens tend at once. The API, the sockets, the skins and everything else go to the server as they are; there is no
// offline mode. A new build is a new sw.js: it installs beside the running one and waits, and the page tells it to
// take over (the "skip" message) when the server's hello names another build than the page's.
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
