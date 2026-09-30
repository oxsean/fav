// boot starts the page: preferences, the page's form and keys, the route, and then either the sign-in page, a
// terminal's sign-in to allow, or the signed-in app with its connection. Everything outside the page (the socket, fetch,
// storage, the clock) comes in as options, so the preview and the tests run it on fakes.
import {render} from '../vendor/preact.mjs';
import {useState, useEffect} from '../vendor/hooks.mjs';
import {signal, effect} from '../vendor/signals-core.mjs';
import {html, KeysContext} from '../ui/base.js';
import {createKeys} from '../core/keys.js';
import {createRouter} from '../core/router.js';
import {createHTTP} from '../core/http.js';
import {createWire} from '../core/wire.js';
import {createStore} from '../core/store.js';
import {createCommands} from '../core/commands.js';
import {createToasts} from '../core/toasts.js';
import {createPrefs} from '../core/prefs.js';
import {form, mac, phoneQuery, narrowQuery, NAV_OPEN_FROM, createNav} from '../core/layout.js';
import {createPlatform, nowhere} from '../core/platform.js';
import {words} from '../core/i18n.js';
import {AuthFrame, Login, Device} from './auth.js';
import {App} from './app.js';
import {returnedFromLink} from './me.js';

const localStore = () => { try { return globalThis.localStorage; } catch { return null; } };
const tabStore = () => { try { return globalThis.sessionStorage; } catch { return null; } };

// connect is what a signed-in page runs on: the wire to /client, the store fed by its watches, writes and notices, and
// the people's names, read again when a project's members change. build is the page's own.
function connect({open, location, clock, doc, http, build}) {
  const url = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/client`;
  const wire = createWire({url, ...(open ? {open} : {}), lang: words.lang.value, build});
  const store = createStore({wire});
  const commands = createCommands({wire});
  const toasts = createToasts();
  const names = signal({});
  let asked = 0;
  const readNames = () => { const n = ++asked; http.users().then(us => { if (n === asked) names.value = Object.fromEntries(us.map(u => [u.id, u.name || u.username || u.id])); }, () => {}); };
  const stopNames = effect(() => { void store.rev.projects.value; readNames(); });
  store.start();
  wire.start();
  doc.addEventListener('visibilitychange', () => wire.setVisible(doc.visibilityState !== 'hidden'));
  return {wire, store, commands, toasts, clock, names, stopNames, fetchOutput: run => wire.call('run.output.page', {run, before: -1, n: 20})};
}

// Root is the page once boot has asked who is signed in: the sign-in page, a terminal's sign-in, or the app, on the me
// page when the tab comes back from linking an account. tab is the tab's storage, notices the browser's notices,
// platform where the page runs, build the page's own.
export function Root({http, router, keys, nav, prefs, first, open, location, history, clock, doc, storage, tab = null, notices = {},
  platform = nowhere, build = ''}) {
  const [session, setSession] = useState(first);
  const [live, setLive] = useState(null);
  const route = router.route.value;
  const auth = route.auth;
  const dropAuth = () => { history.replaceState(null, '', location.pathname + location.search); router.popped(); };
  useEffect(() => {
    if (!session) return;
    const c = connect({open, location, clock, doc, http, build});
    setLive(c);
    const back = returnedFromLink(auth, tab, clock());
    if (auth && auth.kind !== 'device') dropAuth();
    if (back) { router.go({page: 'me'}); c.toasts.show(back); }
    return () => { c.stopNames(); c.store.stop(); c.wire.close(); };
  }, [session?.id]);
  const lang = () => prefs.toggleLang();
  if (!session) {
    return html`<${AuthFrame} onLang=${lang}><${Login} http=${http} auth=${auth?.kind === 'device' ? null : auth}
      onSignedIn=${setSession} onSwitch=${() => { dropAuth(); setSession(null); }} /><//>`;
  }
  if (auth?.kind === 'device') {
    return html`<${AuthFrame} onLang=${lang}><${Device} http=${http} code=${auth.value} onBack=${dropAuth} /><//>`;
  }
  if (!live) return null;
  return html`<${App} ...${live} http=${http} router=${router} keys=${keys} nav=${nav} prefs=${prefs} session=${session} storage=${storage}
    tab=${tab} notices=${notices} doc=${doc} platform=${platform} onLogout=${async () => { try { await http.logout(); } finally { location.assign('/'); } }} />`;
}

// boot: root is where the page draws; open makes the socket (default: a WebSocket); fetch, storage, media (matchMedia)
// and clock are the browser's unless given.
export async function boot({root = globalThis.document.getElementById('app'), open, fetch, location = globalThis.location,
  history = globalThis.history, storage = localStore(), media = q => globalThis.matchMedia(q), clock = () => Date.now()} = {}) {
  const doc = root.ownerDocument;
  const prefs = createPrefs({storage, asked: globalThis.navigator?.language});
  effect(() => {
    words.lang.value = prefs.lang.value;
    doc.documentElement.lang = prefs.lang.value === 'zh' ? 'zh-Hans' : 'en';
    doc.documentElement.dataset.theme = prefs.theme.value;
    doc.documentElement.dataset.density = prefs.density.value;
  });
  const skin = doc.getElementById('skin');
  // The browser's bar takes the skin's background: its sheet loaded, the theme or the system's changed.
  const tint = doc.querySelector('meta[name="theme-color"]');
  const paintTint = () => {
    const bg = globalThis.getComputedStyle?.(doc.documentElement).getPropertyValue('--bg').trim();
    if (tint && bg) tint.setAttribute('content', bg);
  };
  skin?.addEventListener('load', paintTint);
  media('(prefers-color-scheme: dark)').addEventListener?.('change', paintTint);
  effect(() => { void prefs.theme.value; skin?.setAttribute('href', `theme/${prefs.skin.value}.css`); paintTint(); });
  const build = doc.querySelector('meta[name="tend-build"]')?.getAttribute('content') || '';
  const platform = createPlatform({media, build});
  platform.start();
  const phone = media(phoneQuery);
  const formNow = () => { form.value = phone.matches ? 'phone' : 'desktop'; };
  formNow();
  phone.addEventListener?.('change', formNow);
  mac.value = /Mac|iPhone|iPad/.test(globalThis.navigator?.platform || '');
  const keys = createKeys();
  doc.addEventListener('keydown', e => { if (keys.handle(e)) e.preventDefault(); });
  const router = createRouter({location, history});
  globalThis.addEventListener?.('popstate', () => router.popped());
  const http = createHTTP(fetch ? {fetch} : {});
  const nav = createNav({storage, width: media(narrowQuery).matches ? 0 : NAV_OPEN_FROM});
  let first = null;
  try { first = await http.session(); } catch {}
  const draw = () => render(html`<${KeysContext.Provider} value=${keys}><${Root} http=${http} router=${router} keys=${keys} nav=${nav}
    prefs=${prefs} first=${first} open=${open} location=${location} history=${history} clock=${clock} doc=${doc} storage=${storage}
    tab=${tabStore()} notices=${{Notification: globalThis.Notification, secure: !!globalThis.isSecureContext}} platform=${platform} build=${build} /><//>`, root);
  effect(() => { void router.route.value; draw(); });
}
