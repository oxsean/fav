// base is what every component uses: htm bound to Preact, signals read as hook state, the page's form, its words and
// its key scopes.
import {h, createContext} from '../vendor/preact.mjs';
import {useState, useEffect, useContext, useRef} from '../vendor/hooks.mjs';
import htm from '../vendor/htm.mjs';
import {form, mac} from '../core/layout.js';
import {words} from '../core/i18n.js';
import {bindingsFor} from '../core/actions.js';
import './words.js';

export const html = htm.bind(h);

// useSignalValue is a signal's value, the component drawn again when it changes.
export function useSignalValue(sig) {
  const [, redraw] = useState(0);
  const last = useRef(sig.peek());
  useEffect(() => sig.subscribe(v => {
    if (Object.is(v, last.current)) return;
    last.current = v;
    redraw(n => n + 1);
  }), [sig]);
  last.current = sig.peek();
  return last.current;
}

export const useForm = () => useSignalValue(form);
export const usePhone = () => useSignalValue(form) === 'phone';

// useWords is t and f in the current language, the component drawn again when it changes.
export function useWords() {
  useSignalValue(words.lang);
  return words;
}

export const cx = (...names) => names.filter(Boolean).join(' ');

// KeysContext carries the page's keys (core/keys.js); the shell provides it.
export const KeysContext = createContext(null);

// ⚠️ The built-in user a server's own tokens act as.
const LOCAL_USER = 'local';

// NamesContext carries a signal of user id → name; the signed-in app provides it.
export const NamesContext = createContext(null);

// useNames is every user the server gave, id → name.
export function useNames() {
  return useSignalValue(useContext(NamesContext) || noNames);
}

// useName is how a user id reads: the person's name when the server gave it, the server admin for local, else the id.
export function useName() {
  const {t} = useWords();
  const known = useNames();
  return id => (id === LOCAL_USER ? t('ui.serverAdmin') : known[id] || id);
}
const noNames = {peek: () => ({}), subscribe: () => () => {}};

// useKeys pushes a scope of bindings while the component is mounted and active. The bindings may change on every draw;
// the scope is pushed again only when their keys or labels change.
export function useKeys(level, bindings, {blocks = false, active = true} = {}) {
  const keys = useContext(KeysContext);
  const current = useRef(bindings);
  current.current = bindings;
  // shape holds whether each binding is in force, so the key bar follows a when() that changes with the page.
  const shape = bindings.map(b => [b.key, b.id, b.label, b.bar, b.palette, b.alias, !b.when || b.when()].join('\t')).join('\n');
  useEffect(() => {
    if (!keys || !active) return;
    const proxied = current.current.map((b, i) => ({
      ...b,
      run: e => current.current[i]?.run(e),
      when: () => { const x = current.current[i]; return !!x && (!x.when || x.when()); },
    }));
    return keys.push(level, proxied, {blocks});
  }, [keys, active, level, blocks, shape]);
}

// useActions binds what the component can do, {id: {run(key, e), when?, label?}}, under the keys the action table gives.
export function useActions(level, impls, options) {
  useKeys(level, bindingsFor(impls), options);
}

const arrows = {ArrowUp: '↑', ArrowDown: '↓', ArrowLeft: '←', ArrowRight: '→'};

// keyParts spells a key as its caps show it: "Mod+K" is ⌘K on a Mac and Ctrl+K elsewhere, "Shift+ArrowDown" Shift+↓;
// "g h" is two caps; Mod+Backspace is ⌘⌫ on a Mac.
export function keyParts(key, onMac = mac.peek()) {
  return key.split(' ').map(k => k.replace(/^Mod\+/, onMac ? '⌘' : 'Ctrl+').replace(/Arrow(Up|Down|Left|Right)$/, a => arrows[a])
    .replace(/^⌘Backspace$/, '⌘⌫'));
}

export function useMac() {
  return useSignalValue(mac);
}
