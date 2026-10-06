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

// treesDone is the trees aff (the viewer's affordances) says are done that seen, the last call's, did not hold, and
// what aff holds, the next call's seen: a tree id → when it was done. The first call only sees: what was done already
// is not news; a tree reopened and done again is.
export function treesDone(seen, aff) {
  const now = new Map(Object.entries(aff?.tasks || {}).filter(([, a]) => a?.tree_done).map(([id, a]) => [id, a.tree_done.done_at || '']));
  return {seen: now, fresh: seen ? [...now].filter(([id, at]) => seen.get(id) !== at).map(([id]) => id) : []};
}
