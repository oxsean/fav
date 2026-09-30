// prefs are how this viewer wants the page: language, theme, density, skin, browser notices, the output's density and
// the changes tab's layout and whitespace, kept in this browser.
import {signal, computed} from '../vendor/signals-core.mjs';
import {pick} from './i18n.js';
import {density as output} from './proto.js';

export const themes = ['system', 'light', 'dark'];
export const densities = ['compact', 'default', 'comfortable'];
// langs are what the language setting takes: one of the two the page speaks, or the browser's (auto).
export const langs = ['zh', 'en', 'auto'];

// ⚠️ Storage keys browsers already hold these settings under.
const keys = {lang: 'tend-lang', theme: 'tend-theme', look: 'tend-look', notify: 'tend-notify'};
// ⚠️ Where this browser keeps the output's density (proto.js), a setting of its own apart from the page's.
export const OUTPUT_KEY = 'tend-output-density';
// ⚠️ Where this browser keeps the changes tab's layout: unified or side by side (split).
export const DIFF_KEY = 'tend-diff-view';
export const diffViews = ['unified', 'split'];
// ⚠️ Where this browser keeps whether the changes tab ignores whitespace ('on', or nothing).
export const SPACE_KEY = 'tend-diff-space';

const skinName = /^[a-z]+$/;
const hex = /^[0-9a-f]{6}$/;

// createPrefs: storage may be missing or refuse (a private window); asked is the browser's language.
export function createPrefs({storage, asked = ''} = {}) {
  const read = k => { try { return storage?.getItem(k) ?? null; } catch { return null; } };
  const write = (k, v) => { try { storage?.setItem(k, v); } catch {} };
  const drop = k => { try { storage?.removeItem(k); } catch {} };
  let saved = {};
  try { saved = JSON.parse(read(keys.look) || '{}') || {}; } catch {}
  const savedLang = read(keys.lang);
  const langChoice = signal(savedLang === 'zh' || savedLang === 'en' ? savedLang : 'auto');
  const lang = signal(langChoice.value === 'auto' ? pick(asked) : langChoice.value);
  const theme = signal(themes.includes(read(keys.theme)) ? read(keys.theme) : 'system');
  const density = signal(densities.includes(saved.density) ? saved.density : 'default');
  const shown = signal(output.names.includes(read(OUTPUT_KEY)) ? read(OUTPUT_KEY) : output.default);
  // look is the skin: a preset's name, an accent of its own (rrggbb, '' for the preset's) and the high contrast.
  const look = signal({skin: skinName.test(saved.skin || '') ? saved.skin : 'tend', accent: hex.test(saved.accent || '') ? saved.accent : '', high: !!saved.high});
  const notify = signal(read(keys.notify) !== 'off');
  const diff = signal(diffViews.includes(read(DIFF_KEY)) ? read(DIFF_KEY) : 'unified');
  const ignoreSpace = signal(read(SPACE_KEY) === 'on');
  const keepLook = () => write(keys.look, JSON.stringify({...saved, density: density.value, ...look.value}));
  const next = (list, v) => list[(list.indexOf(v) + 1) % list.length];
  const setLang = v => {
    if (!langs.includes(v)) return;
    langChoice.value = v;
    lang.value = v === 'auto' ? pick(asked) : v;
    if (v === 'auto') drop(keys.lang); else write(keys.lang, v);
  };
  const setTheme = v => { if (!themes.includes(v)) return; theme.value = v; write(keys.theme, v); };
  const setDensity = v => { if (!densities.includes(v)) return; density.value = v; keepLook(); };
  return {
    lang, langChoice, theme, density, look, notify, output: shown, diff, ignoreSpace,
    setOutput(v) { if (!output.names.includes(v)) return; shown.value = v; write(OUTPUT_KEY, v); },
    setDiff(v) { if (!diffViews.includes(v)) return; diff.value = v; write(DIFF_KEY, v); },
    setIgnoreSpace(on) { ignoreSpace.value = !!on; if (on) write(SPACE_KEY, 'on'); else drop(SPACE_KEY); },
    // skin is the stylesheet's name, /theme/<skin>.css.
    skin: computed(() => look.value.skin + (look.value.accent ? '-' + look.value.accent : '') + (look.value.high ? '-high' : '')),
    setLang, setTheme, setDensity,
    // setLook changes some of the look ({skin?, accent?, high?}); a name or accent not well formed is left as it was.
    setLook(p) {
      const v = {...look.value};
      if (p.skin !== undefined && skinName.test(p.skin)) v.skin = p.skin;
      if (p.accent !== undefined && (p.accent === '' || hex.test(p.accent))) v.accent = p.accent;
      if (p.high !== undefined) v.high = !!p.high;
      look.value = v;
      keepLook();
    },
    setNotify(on) { notify.value = !!on; write(keys.notify, on ? 'on' : 'off'); },
    toggleLang() { setLang(lang.value === 'zh' ? 'en' : 'zh'); },
    cycleTheme() { setTheme(next(themes, theme.value)); },
    cycleDensity() { setDensity(next(densities, density.value)); },
  };
}
