// follow is how a long conversation scrolls (docs/design/runs/clients.md, 「对话与输出」): it sticks to the bottom until the
// viewer moves up on their own, then keeps still, counts the new steps below, and holds back what would change above
// the viewport until the scrolling stops. Pure functions: ui/output.js's useFollow feeds them the scroller's events.

// ⚠️ Back within this many pixels of the bottom, the view follows again.
export const NEAR = 48;
// ⚠️ The DOM keeps about this many rows; pages of PAGE rows further than SCREENS screens from the view become
// placeholders of their height once the scrolling stopped.
export const KEEP_ROWS = 1500;
export const PAGE = 200;
export const SCREENS = 5;
// ⚠️ Scrolling has stopped when no scroll came for SETTLE_MS, and on a touch screen TOUCH_MS after the finger left
// (a guess to check on the devices).
export const SETTLE_MS = 100;
export const TOUCH_MS = 150;
// ⚠️ An earlier page is fetched once the view is this many screens from the top.
export const PREFETCH_SCREENS = 1.5;

// initial is the state of a view opening: following unless it comes back to a place it was left away from.
export const initial = (away = false) => ({mode: away ? 'away' : 'follow', keys: [], seen: new Set(), fresh: [], waiting: [], over: false,
  overAtLeave: false, ended: false, divider: ''});

// step is the state after ev:
//   {type: 'rows', keys, waits, ended}  the timeline was laid again: its counted keys, those that want the viewer,
//                                        whether the run has ended
//   {type: 'up'}                         the viewer moved up (wheel, finger, ↑ PageUp Home, the scrollbar, a find
//                                        jump, a link to an earlier step): leave the bottom
//   {type: 'settled', fromBottom}        the scrolling stopped this far from the bottom
//   {type: 'bottom'}                     "N new", End, or back to the latest: follow again
//   {type: 'passed'}                     the viewer scrolled past the "new" line: it goes
export function step(s, ev) {
  switch (ev.type) {
    case 'rows': {
      const over = !!ev.ended;
      if (s.mode === 'follow') return {...s, keys: ev.keys, seen: new Set(ev.keys), fresh: [], waiting: [], over, ended: false};
      const fresh = ev.keys.filter(k => !s.seen.has(k));
      const waits = new Set(ev.waits || []);
      return {...s, keys: ev.keys, fresh, waiting: fresh.filter(k => waits.has(k)), over, ended: over && !s.overAtLeave,
        divider: s.divider && fresh.includes(s.divider) ? s.divider : fresh[0] || ''};
    }
    case 'up':
      if (s.mode === 'away') return s;
      return {...s, mode: 'away', seen: new Set(s.keys), fresh: [], waiting: [], ended: false, overAtLeave: s.over, divider: ''};
    case 'settled':
      return s.mode === 'away' && ev.fromBottom <= NEAR ? back(s) : s;
    case 'bottom':
      return back(s);
    case 'passed':
      return {...s, divider: ''};
  }
  return s;
}

const back = s => ({...s, mode: 'follow', seen: new Set(s.keys), fresh: [], waiting: [], ended: false});

// badge is what the "N new" button says: nothing while following; the new steps, those that want the viewer, or that
// the run ended.
export function badge(s) {
  if (s.mode === 'follow') return null;
  if (s.ended) return {kind: 'ended', n: s.fresh.length};
  if (!s.fresh.length) return {kind: 'latest', n: 0};
  return {kind: s.waiting.length ? 'waiting' : 'new', n: s.fresh.length, waiting: s.waiting.length, first: s.waiting[0] || ''};
}

// stick is how a following view goes to the bottom when its content grew by grew pixels: smoothly under a screen,
// at once over one or while a finger is down.
export function stick({grew, screen, touching = false}) {
  if (grew <= 0) return 'none';
  return touching || grew > screen ? 'jump' : 'smooth';
}

// anchorOf is the first row still in view (top at or below the viewport's top, or the one it cuts) and how far its top
// is from the viewport's top: tops is [{key, top, height}] in order, top measured from the content's top.
export function anchorOf(tops, scrollTop) {
  for (const r of tops) if (r.top + r.height > scrollTop) return {key: r.key, offset: r.top - scrollTop};
  return null;
}

// shift is how far to move scrollTop so the anchor row stays where it was, once its top moved to top.
export const shift = (anchor, top, scrollTop) => (anchor ? top - scrollTop - anchor.offset : 0);

// hold keeps, while the viewer is away and scrolling, the rows above the anchor as they are drawn (shown) and takes
// the newest ones (latest) from the anchor on; with no anchor, or one latest lost, it is latest. Rows are matched by
// key; a row drawn above that is gone from latest stays until the hold ends.
export function hold(shown, latest, anchorKey) {
  if (!anchorKey) return latest;
  const a = shown.findIndex(r => r.key === anchorKey), b = latest.findIndex(r => r.key === anchorKey);
  if (a < 0 || b < 0) return latest;
  return [...shown.slice(0, a), ...latest.slice(b)];
}

// held is whether hold changes anything: what is above the anchor in latest differs from what is drawn.
export function held(shown, latest, anchorKey) {
  const kept = hold(shown, latest, anchorKey);
  return kept.length !== latest.length || kept.some((r, i) => r !== latest[i]);
}

// placeholders are the pages (of PAGE rows) to draw as blocks of their height: with more than KEEP_ROWS rows, those
// further than SCREENS screens from the view, never one that keeps (focus, selected text, an answer being written).
// view is {top, bottom} in pixels; pages [{top, height, keep}].
export function placeholders({rows, pages, view, screen}) {
  const out = new Set();
  if (rows <= KEEP_ROWS) return out;
  const far = SCREENS * screen;
  pages.forEach((p, i) => {
    if (p.keep || i === pages.length - 1) return;
    if (p.top + p.height < view.top - far || p.top > view.bottom + far) out.add(i);
  });
  return out;
}

// nearTop tells a view scrolled to scrollTop to fetch the page before it.
export const nearTop = (scrollTop, screen) => scrollTop < PREFETCH_SCREENS * screen;

// place is where a conversation's view was left, kept in the tab's session storage: the anchor, and whether it
// followed. The key is the conversation's first run.
export const placeKey = root => 'tend-out-' + root;

export function readPlace(storage, root) {
  try {
    const p = JSON.parse(storage?.getItem(placeKey(root)) || 'null');
    return p && typeof p === 'object' && typeof p.follow === 'boolean' ? p : null;
  } catch { return null; }
}

export function keepPlace(storage, root, place) {
  try { storage?.setItem(placeKey(root), JSON.stringify(place)); } catch {}
}
