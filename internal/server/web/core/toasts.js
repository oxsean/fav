// toasts are the short notices at the bottom of the page. One that carries undo keeps it for UNDO_WAIT, the time a
// person has to take the action back (Mod+Z, or its button).
import {signal} from '../vendor/signals-core.mjs';

export const UNDO_WAIT = 6000;
const plainWait = 4000;

export function createToasts({timers = globalThis} = {}) {
  const list = signal([]);
  let next = 1;
  const timersOf = new Map();

  function dismiss(id) {
    timers.clearTimeout(timersOf.get(id));
    timersOf.delete(id);
    list.value = list.value.filter(t => t.id !== id);
  }

  // show adds a notice {text, tone?, undo?, act?} and returns its id; tone is danger or warning, undo a function, act
  // {label, run} a way on from it (the task a session became).
  function show({text, tone = '', undo = null, act = null}) {
    const id = next++;
    list.value = [...list.value, {id, text, tone, undo, act}];
    timersOf.set(id, timers.setTimeout(() => dismiss(id), undo ? UNDO_WAIT : plainWait));
    return id;
  }

  const latest = () => [...list.value].reverse().find(t => t.undo);

  // undo takes back the newest notice that can be, or the one with id.
  function undo(id) {
    const t = id ? list.value.find(x => x.id === id && x.undo) : latest();
    if (!t) return false;
    dismiss(t.id);
    t.undo();
    return true;
  }

  return {list, show, dismiss, undo, canUndo: () => !!latest()};
}
