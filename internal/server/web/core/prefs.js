// prefs are how this viewer wants the page: language, theme, density and skin, kept in this browser under the keys
// the old page used, so a switch keeps them.
import {signal} from '../vendor/signals-core.mjs';
import {pick} from './i18n.js';

export const themes = ['system', 'light', 'dark'];
export const densities = ['compact', 'default', 'comfortable'];
// outputDensities are how much of a run's output shows (core/output.js); a setting of its own, apart from the page's.
export const outputDensities = ['brief', 'standard', 'detailed'];

// ⚠️ Storage keys shared with the old page (app.js, look.js).
const keys = {lang: 'tend-lang', theme: 'tend-theme', look: 'tend-look'};
// ⚠️ Where this browser keeps the output's density.
export const OUTPUT_KEY = 'tend-output-density';

// createPrefs: storage may be missing or refuse (a private window); asked is the browser's language.
export function createPrefs({storage, asked = ''} = {}) {
  const read = k => { try { return storage?.getItem(k) ?? null; } catch { return null; } };
  const write = (k, v) => { try { storage?.setItem(k, v); } catch {} };
  let look = {};
  try { look = JSON.parse(read(keys.look) || '{}') || {}; } catch {}
  const saved = read(keys.lang);
  const lang = signal(saved === 'zh' || saved === 'en' ? saved : pick(asked));
  const theme = signal(themes.includes(read(keys.theme)) ? read(keys.theme) : 'system');
  const density = signal(densities.includes(look.density) ? look.density : 'default');
  const output = signal(outputDensities.includes(read(OUTPUT_KEY)) ? read(OUTPUT_KEY) : 'standard');
  const skin = /^[a-z]+$/.test(look.skin || '') ? look.skin : 'tend';
  const accent = /^[0-9a-f]{6}$/.test(look.accent || '') ? '-' + look.accent : '';
  const next = (list, v) => list[(list.indexOf(v) + 1) % list.length];
  return {
    lang, theme, density, output,
    setOutput(v) { if (!outputDensities.includes(v)) return; output.value = v; write(OUTPUT_KEY, v); },
    // skin is the stylesheet's name, /theme/<skin>.css.
    skin: skin + accent + (look.high ? '-high' : ''),
    toggleLang() { lang.value = lang.value === 'zh' ? 'en' : 'zh'; write(keys.lang, lang.value); },
    cycleTheme() { theme.value = next(themes, theme.value); write(keys.theme, theme.value); },
    cycleDensity() {
      density.value = next(densities, density.value);
      write(keys.look, JSON.stringify({...look, density: density.value}));
    },
  };
}
