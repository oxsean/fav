// keys dispatches key presses through a stack of scopes: modal, drawer, list, page, global. A component pushes its
// scope when it mounts and pops it when it unmounts; the first scope with a binding for the key that applies runs it.
// Keys are spelled as the action table spells them: "n", "Shift+D", "Mod+K", "g h" (g, then h), "Esc", "Space".
import {signal} from '../vendor/signals-core.mjs';

export const levels = ['modal', 'drawer', 'list', 'page', 'global'];

// ⚠️ A CJK input method types these marks for the keys the table names.
export const imeMarks = {'；': ';', '，': ',', '？': '?', '、': '/', '／': '/'};

const named = {Escape: 'Esc', ' ': 'Space', Spacebar: 'Space'};

// keyName is a press as the table spells it, '' for one no binding can name (Alt, a bare modifier).
export function keyName(e) {
  if (e.altKey || ['Shift', 'Control', 'Meta', 'Alt', 'CapsLock'].includes(e.key)) return '';
  const k = imeMarks[e.key] || named[e.key] || e.key;
  if (e.metaKey || e.ctrlKey) return 'Mod+' + (k.length === 1 ? k.toUpperCase() : k);
  if (/^[a-zA-Z]$/.test(k)) return e.shiftKey ? 'Shift+' + k.toUpperCase() : k.toLowerCase();
  return e.shiftKey && k.length > 1 ? 'Shift+' + k : k;
}

const editable = t => !!t && (t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName));

// ⚠️ Inside a text input only these reach the scopes; everything else is typing.
const inInput = new Set(['Esc', 'Mod+Enter']);

export function createKeys({timers = globalThis, sequenceWait = 1200} = {}) {
  let scopes = [], pending = '', timer = null, nextID = 1;
  // changed goes up whenever a scope comes or goes, for what shows the keys in force (the key bar).
  const changed = signal(0);

  // push adds a scope of bindings [{key, run, when?, label?}] at a level and returns what removes it. blocks: no scope below
  // it sees a key (a modal keeps the page's keys from acting behind it).
  function push(level, bindings, {blocks = false} = {}) {
    if (!levels.includes(level)) throw new Error(`keys: no level ${level}`);
    const seen = new Set();
    for (const b of bindings) {
      if (!b.key) throw new Error(`keys: ${b.id || 'a binding'} has no key`);
      if (seen.has(b.key)) throw new Error(`keys: ${b.key} twice in one ${level} scope`);
      seen.add(b.key);
    }
    for (const b of bindings) {
      const first = b.key.split(' ')[0];
      if (b.key.includes(' ') && seen.has(first)) throw new Error(`keys: ${first} is both a key and the start of ${b.key}`);
    }
    const scope = {id: nextID++, level, bindings, blocks};
    scopes.push(scope);
    changed.value++;
    return () => {
      scopes = scopes.filter(s => s !== scope);
      changed.value++;
    };
  }

  // ordered: modal first, and within a level the latest pushed first; a blocking scope hides those after it.
  function ordered() {
    const out = [];
    for (const level of levels) {
      for (const s of scopes.filter(x => x.level === level).reverse()) {
        out.push(s);
        if (s.blocks) return out;
      }
    }
    return out;
  }

  const find = key => {
    for (const s of ordered()) {
      const b = s.bindings.find(x => x.key === key && (!x.when || x.when()));
      if (b) return b;
    }
    return null;
  };
  const starts = key => ordered().some(s => s.bindings.some(x => x.key.startsWith(key + ' ') && (!x.when || x.when())));

  // handle runs the binding a key event stands for; true when it did (the caller prevents the default).
  function handle(e) {
    if (e.isComposing || e.keyCode === 229) return false;
    const name = keyName(e);
    if (!name) return false;
    if (editable(e.target) && !inInput.has(name)) return false;
    timers.clearTimeout(timer);
    if (pending) {
      const full = pending + ' ' + name;
      pending = '';
      const b = find(full);
      if (b) b.run(e);
      return !!b;
    }
    if (starts(name)) {
      pending = name;
      timer = timers.setTimeout(() => { pending = ''; }, sequenceWait);
      return true;
    }
    const b = find(name);
    if (b) b.run(e);
    return !!b;
  }

  // active is the bindings a press would run now, each key once, in the order the scopes are searched.
  function active() {
    const seen = new Set();
    return ordered().flatMap(s => s.bindings.filter(b => (!b.when || b.when()) && !seen.has(b.key) && seen.add(b.key)));
  }

  return {push, handle, changed, active};
}
