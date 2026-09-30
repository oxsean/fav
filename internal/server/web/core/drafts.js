// drafts are what the viewer has written and not sent yet, per task: the message to its agent (the conversation's
// box) and the send-back notes (the accept dialog), kept while the page is open so a tab or dialog closed and opened
// again finds them. A quote of changed lines goes into one of them and asks its box for the focus.
import {signal} from '../vendor/signals-core.mjs';
import {withQuote} from './changes.js';

export const kinds = ['message', 'notes'];

export function createDrafts() {
  const all = signal({});
  // wants is the box a quote went into ({task, kind}) until that box takes the focus.
  const wants = signal(null);
  const key = (task, kind) => kind + ':' + task;
  const set = (task, kind, text) => {
    const k = key(task, kind);
    if ((all.value[k] || '') === text) return;
    const next = {...all.value};
    if (text) next[k] = text; else delete next[k];
    all.value = next;
  };
  return {
    all, wants,
    of: (task, kind) => all.value[key(task, kind)] || '',
    set,
    // quote puts q after what task's kind of draft holds and asks that box for the focus.
    quote(task, kind, q) {
      set(task, kind, withQuote(all.value[key(task, kind)] || '', q));
      wants.value = {task, kind};
    },
    // focused says the box asked for took the focus.
    focused(task, kind) { if (wants.value?.task === task && wants.value?.kind === kind) wants.value = null; },
  };
}
