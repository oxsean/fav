// notices are what the page tells a browser about while it is hidden: the inbox items that wait on something new.
import {noInbox} from './store.js';

// keysOf is what an inbox item waits on: each pending thing at its version, else its task's reason.
export const keysOf = x => (x.pending?.length ? x.pending.map(p => `${p.id}@${p.version || 0}`) : [`${x.task}/${x.reason}`]);

// fresh is the items that wait on something seen did not hold, and what items hold, the next call's seen. Before the
// inbox's first push nothing is seen yet, and the first push is only seen: what waited already is not news.
export function fresh(seen, items) {
  if (items === noInbox) return {seen: null, fresh: []};
  const now = new Set(items.flatMap(keysOf));
  return {seen: now, fresh: seen ? items.filter(x => keysOf(x).some(k => !seen.has(k))) : []};
}
