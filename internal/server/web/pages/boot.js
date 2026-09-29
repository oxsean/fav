// boot starts the page: preferences, the page's form and keys, the route, and then either the sign-in page, a
// terminal's sign-in to allow, or the signed-in app with its connection. Everything outside the page (the socket, fetch,
// storage, the clock) comes in as options, so the preview and the tests run it on fakes.
import {render} from '../vendor/preact.mjs';
import {useState, useEffect} from '../vendor/hooks.mjs';
import {effect} from '../vendor/signals-core.mjs';
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
import {words} from '../core/i18n.js';
import {AuthFrame, Login, Device} from './auth.js';
import {App} from './app.js';

const localStore = () => { try { return globalThis.localStorage; } catch { return null; } };

// connect is what a signed-in page runs on: the wire to /client, the store fed by its watches, writes and notices.
function connect({open, location, clock, doc}) {
  const url = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/client`;
  const wire = createWire({url, ...(open ? {open} : {}), lang: words.lang.value});
  const store = createStore({wire});
  const commands = createCommands({wire});
  const toasts = createToasts();
  store.start();
  wire.start();
  doc.addEventListener('visibilitychange', () => wire.setVisible(doc.visibilityState !== 'hidden'));
  return {wire, store, commands, toasts, clock, fetchOutput: run => wire.call('run.output.page', {run, before: -1, n: 20})};
}

function Root({http, router, keys, nav, prefs, first, open, location, history, clock, doc}) {
  const [session, setSession] = useState(first);
  const [live, setLive] = useState(null);
  const route = router.route.value;
  const auth = route.auth;
  const dropAuth = () => { history.replaceState(null, '', location.pathname + location.search); router.popped(); };
  useEffect(() => {
    if (!session) return;
    const c = connect({open, location, clock, doc});
    setLive(c);
    if (auth && auth.kind !== 'device') dropAuth();
    return () => { c.store.stop(); c.wire.close(); };
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
  return html`<${App} ...${live} router=${router} keys=${keys} nav=${nav} prefs=${prefs} session=${session}
    onLogout=${async () => { try { await http.logout(); } finally { location.assign('/'); } }} />`;
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
  if (skin) skin.setAttribute('href', `theme/${prefs.skin}.css`);
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
    prefs=${prefs} first=${first} open=${open} location=${location} history=${history} clock=${clock} doc=${doc} /><//>`, root);
  effect(() => { void router.route.value; draw(); });
}
