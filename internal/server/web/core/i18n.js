// i18n holds the page's words. Each module registers its own table of key → [zh, en]; a key two modules define is an
// error, never a silent override. Sentences are whole, with %s / %d filled in order, the same in both languages.
import {signal} from '../vendor/signals-core.mjs';

const langs = ['zh', 'en'];
const verbs = s => (s.match(/%[sd]/g) || []).join('');

export function createWords() {
  const table = new Map();
  const lang = signal('zh');

  function register(module, words) {
    for (const [key, pair] of Object.entries(words)) {
      const had = table.get(key);
      if (had) throw new Error(`i18n: ${module} defines ${key}, which ${had.module} has`);
      if (!Array.isArray(pair) || pair.length !== 2 || !pair[0] || !pair[1]) throw new Error(`i18n: ${module}.${key} needs a zh and an en text`);
      if (verbs(pair[0]) !== verbs(pair[1])) throw new Error(`i18n: ${module}.${key}: zh and en carry different verbs`);
      table.set(key, {module, zh: pair[0], en: pair[1]});
    }
  }

  const t = key => table.get(key)?.[lang.value] ?? key;

  function f(key, ...args) {
    let i = 0;
    return t(key).replace(/%[sd]/g, () => String(args[i++] ?? ''));
  }

  // both is a key's text in every language, for searching by either.
  const both = key => (table.has(key) ? langs.map(l => table.get(key)[l]) : [key]);

  return {lang, register, t, f, both, has: key => table.has(key), keys: () => [...table.keys()], moduleOf: key => table.get(key)?.module};
}

// pick is the language a browser asks for, of the two the page speaks.
export const pick = (asked = '') => (String(asked).toLowerCase().startsWith('zh') ? 'zh' : 'en');

export const languages = langs;

export const words = createWords();
export const {lang, register, t, f} = words;
