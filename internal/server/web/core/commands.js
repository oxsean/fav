// commands sends the page's writes. What the state says stays what the journal folded; a write only marks what it is
// about: pending while it is out, hidden while a reversible one (marking a task done) is out, unknown when the
// answer never came (the wire timed out or dropped), in which case retry sends it again under the same command id,
// which the coordinator's receipts answer without doing it twice.

import {signal} from '../vendor/signals-core.mjs';
import {code} from './proto.js';

// ⚠️ The coordinator never saw an answer's fate for these codes: the command may or may not have run.
export const unsure = [code.timeout, code.offline, code.closed];

const commandID = () => (globalThis.crypto?.randomUUID?.() || Math.random().toString(36).slice(2) + Date.now().toString(36));

// createCommands: newID makes command ids (the tests pass their own).
export function createCommands({wire, newID = commandID}) {
  // pending: key → {method, params, id, state: pending | unknown, error}; key names what the write is about.
  const pending = signal(new Map());
  // hidden: the ids a reversible write takes out of the lists until it is answered.
  const hidden = signal(new Set());

  const put = (key, v) => {
    const m = new Map(pending.value);
    if (v) m.set(key, v); else m.delete(key);
    pending.value = m;
  };
  const hide = (id, on) => {
    if (!id) return;
    const s = new Set(hidden.value);
    if (on) s.add(id); else s.delete(id);
    hidden.value = s;
  };

  // A hidden id stays hidden after the answer until until (the list it is left out of) next changes: the list that
  // no longer holds it comes after the answer.
  function release(id, until) {
    if (!until) { hide(id, false); return; }
    const first = until.peek();
    const stop = until.subscribe(v => {
      if (v === first) return;
      queueMicrotask(() => stop());
      hide(id, false);
    });
  }

  async function attempt(key, c) {
    put(key, {...c, state: 'pending'});
    hide(c.hide, true);
    try {
      const res = await wire.call(c.method, c.params, {commandID: c.id});
      put(key, null);
      if (c.hide) release(c.hide, c.until);
      return res;
    } catch (e) {
      put(key, unsure.includes(e.code) ? {...c, state: 'unknown', error: e.code} : null);
      hide(c.hide, false);
      throw e;
    }
  }

  return {
    pending, hidden,
    // send writes method(params) as command id (default: a new one, which an undo names); key (default: the method and
    // params) names it for pending; hide is an id to leave out of the lists while it is out, and after it until the
    // signal until changes.
    send(method, params, {key = method + ' ' + JSON.stringify(params), hide: hideID = '', until = null, id = newID()} = {}) {
      return attempt(key, {method, params, id, hide: hideID, until});
    },
    newID,
    // show takes an id out of hidden at once (its write was undone).
    show: id => hide(id, false),
    // retry sends an unknown write again with its command id.
    retry(key) {
      const c = pending.value.get(key);
      if (!c || c.state !== 'unknown') return Promise.reject(new Error(`commands: nothing to retry for ${key}`));
      return attempt(key, c);
    },
    // forget drops an unknown write the viewer let go.
    forget(key) { if (pending.value.get(key)?.state === 'unknown') put(key, null); },
    state: key => pending.value.get(key)?.state || '',
  };
}
