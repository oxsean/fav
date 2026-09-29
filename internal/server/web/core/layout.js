// layout is how wide the page is and what follows from it: the form every component draws (desktop or phone), whether
// the sidebar starts open, and the key names of the platform. main.js sets them from media queries; tests set them.
import {signal} from '../vendor/signals-core.mjs';

// ⚠️ Under 720px wide the page takes its phone form; under 1200px the desktop sidebar starts collapsed.
export const PHONE_BELOW = 720;
export const NAV_OPEN_FROM = 1200;

export const phoneQuery = `(max-width: ${PHONE_BELOW - 1}px)`;
export const narrowQuery = `(max-width: ${NAV_OPEN_FROM - 1}px)`;

export const formOf = width => (width < PHONE_BELOW ? 'phone' : 'desktop');

export const form = signal('desktop');

// mac: the platform spells Mod as ⌘ rather than Ctrl.
export const mac = signal(false);

const navKey = 'tend-nav';

// createNav is the desktop sidebar's state: open or collapsed, kept in storage once someone chose; until then it
// follows the width.
export function createNav({storage, width}) {
  let saved = null;
  try { saved = storage?.getItem(navKey); } catch {}
  const open = signal(saved === 'open' ? true : saved === 'closed' ? false : width >= NAV_OPEN_FROM);
  return {
    open,
    toggle() {
      open.value = !open.value;
      try { storage?.setItem(navKey, open.value ? 'open' : 'closed'); } catch {}
    },
  };
}
