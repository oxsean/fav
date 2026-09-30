// push is this browser's Web Push for the viewer signed in: how it stands here, turning it on and off, renewing the
// subscription each time the page opens (the server forgets a device nobody renewed for 90 days), and closing the
// notices of what no longer waits. It runs on the platform's push and the page's http.
import {noInbox} from './store.js';

const bytes = b64 => Uint8Array.from(atob(b64.replace(/-/g, '+').replace(/_/g, '/')), c => c.charCodeAt(0));
const sameKey = (buf, b64) => {
  if (!buf) return false;
  const a = new Uint8Array(buf), b = bytes(b64);
  return a.length === b.length && a.every((x, i) => x === b[i]);
};

// createPush: http is the page's (core/http.js), platform where it runs (core/platform.js).
export function createPush({http, platform}) {
  const p = platform.push;
  // mine is this browser's device id as the server last registered it ('' for none), set as a registration starts.
  let mine = Promise.resolve('');
  const register = sub => { mine = http.putDevice(sub.toJSON(), platform.name).then(r => r?.id || ''); return mine; };
  return {
    // device is the id of this browser's push device, '' while it has none.
    device: () => mine.catch(() => ''),
    // state is none (no push in this browser), denied (the browser refuses notices), on or off.
    async state() {
      if (!p) return 'none';
      if (p.permission() === 'denied') return 'denied';
      return (await p.current()) ? 'on' : 'off';
    },
    // on asks the browser, subscribes with the server's key and registers this device; it answers the state after.
    async on() {
      const asked = p.ask();
      const key = http.pushKey();
      key.catch(() => {});
      if ((await asked) !== 'granted') return p.permission() === 'denied' ? 'denied' : 'off';
      const sub = await p.subscribe(bytes(await key));
      await register(sub);
      return 'on';
    },
    async off() {
      mine = Promise.resolve('');
      const endpoint = await p?.unsubscribe();
      if (endpoint) await http.dropDevice(endpoint);
      return p ? 'off' : 'none';
    },
    // renew tells the server this device is still here; a subscription made with another key than the server's now is
    // made again.
    renew() {
      mine = (async () => {
        let sub = await p?.current();
        if (!sub) return '';
        const key = await http.pushKey();
        if (!sameKey(sub.options?.applicationServerKey, key)) {
          await sub.unsubscribe();
          if (p.permission() !== 'granted') return '';
          sub = await p.subscribe(bytes(key));
        }
        const r = await http.putDevice(sub.toJSON(), platform.name);
        return r?.id || '';
      })();
      return mine.then(() => {});
    },
    // clear closes the notices about things that no longer wait on the viewer, by the inbox's pending items: one
    // about an item, and the count of a hidden push once nothing waits. Others (a task done, how a deny went) stay.
    clear(inbox) {
      if (!p || inbox === noInbox) return;
      const keep = new Set(inbox.flatMap(x => (x.pending || []).map(q => q.id)));
      p.clear(n => (n.data?.item ? keep.has(n.data.item) : !n.data?.count || inbox.length > 0)).catch(() => {});
    },
  };
}

// noPush is the push of the preview and the tests: a browser without it.
export const noPush = createPush({http: null, platform: {push: null}});
