// notify shows a browser notice for each inbox item that waits on something new while the page is hidden, when the
// viewer turned notices on and the browser allows them. Clicking one brings the page up on its task.
import {useRef, useEffect} from '../vendor/hooks.mjs';
import {useWords, useSignalValue} from '../ui/base.js';
import {fresh, treesDone} from '../core/notices.js';
import {why} from './words.js';

// useNotices: notices are the browser's ({Notification}), doc the document whose visibility counts; active is false
// where the page shows none (a phone); onOpen(task) opens a task.
export function useNotices({store, prefs, notices, doc, active, onOpen}) {
  const w = useWords();
  const items = useSignalValue(store.inbox);
  const seen = useRef(null);
  useEffect(() => {
    const got = fresh(seen.current, items);
    seen.current = got.seen;
    const N = notices?.Notification;
    if (!active || !prefs.notify.peek() || N?.permission !== 'granted' || doc?.visibilityState !== 'hidden') return;
    for (const x of got.fresh) {
      try {
        const n = new N(x.title, {body: why(w, x.reason), tag: x.task});
        n.onclick = () => { globalThis.focus?.(); n.close?.(); onOpen(x.task); };
      } catch {}
    }
  }, [items]);
}

// useTreesDone shows a toast for each tree that comes to be done while the page is live (toasts are core's).
export function useTreesDone({store, toasts}) {
  const {f} = useWords();
  const aff = useSignalValue(store.affordances);
  const phase = useSignalValue(store.phase);
  const seen = useRef(null);
  useEffect(() => {
    if (phase !== 'live') return;
    const got = treesDone(seen.current, aff);
    seen.current = got.seen;
    for (const id of got.fresh) {
      const s = aff.tasks[id].tree_done;
      toasts.show({text: f('toast.treeDone', store.state.tasks[id]?.title || id, s.done || 0, s.leaves || 0)});
    }
  }, [aff, phase]);
}
