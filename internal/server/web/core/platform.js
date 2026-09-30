// platform is what the page asks of where it runs, behind one interface: a browser tab (browser) or an app installed
// from the home screen (pwa). Pages ask it and never look for themselves: which system the device is (for how to
// install), a name for it (for signing it in from another device), whether the address is a secure context (without
// it there is no service worker and no push), starting the service worker and loading a newer build.
// Everything it reads comes in as options, so it runs in node on fakes.

// createPlatform: nav is the browser's navigator, media answers a media query (matchMedia), secure is
// isSecureContext, reload loads the page again, build is the page's own (index.html's tend-build; empty in the preview
// and the tests, which start no service worker).
export function createPlatform({nav = globalThis.navigator || {}, media = q => globalThis.matchMedia?.(q), secure = !!globalThis.isSecureContext,
  reload = () => globalThis.location.reload(), build = ''} = {}) {
  const ua = nav.userAgent || '';
  const iPad = /iPad/.test(ua) || nav.platform === 'MacIntel' && nav.maxTouchPoints > 1;
  const os = iPad || /iPhone|iPod/.test(ua) ? 'ios' : /Android/.test(ua) ? 'android' : 'other';
  const kind = media('(display-mode: standalone)')?.matches || nav.standalone === true ? 'pwa' : 'browser';
  const name = os === 'ios' ? (iPad ? 'iPad' : 'iPhone') : os === 'android' ? 'Android'
    : /Mac/.test(ua) ? 'Mac' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : 'Browser';
  const sw = secure && build ? nav.serviceWorker : null;

  return {
    kind, os, name, secure,
    // start registers the service worker; a browser that refuses it runs the page as it is.
    start() {
      sw?.register('/sw.js', {scope: '/'}).catch(() => {});
    },
    // refresh loads the server's newer build: the new service worker, installed beside the running one, takes over
    // first, or the page would come back from the old one's files.
    async refresh() {
      const reg = await sw?.getRegistration?.().catch(() => null);
      if (!reg || !sw.controller) return reload();
      if (!reg.waiting && !reg.installing) await reg.update().catch(() => {});
      const next = reg.waiting || reg.installing;
      if (!next) return reload();
      sw.addEventListener('controllerchange', () => reload(), {once: true});
      const go = () => {
        if (next.state === 'installed') next.postMessage('skip');
        else if (next.state === 'redundant') reload();
      };
      next.addEventListener('statechange', go);
      go();
    },
  };
}

// nowhere is the platform of the preview and the tests: a browser tab on an address that is not secure.
export const nowhere = createPlatform({nav: {}, media: () => undefined, secure: false, reload: () => {}});
