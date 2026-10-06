// Toasts shows core/toasts.js's notices; the newest that can be undone is taken back by Mod+Z or its button. They keep
// clear of the controls at the bottom: on a desktop above a composer under them, on a phone above the tab bar or, over
// a full-screen page, above what it keeps there (its foot, a composer); over a sheet, at the top, since the sheet fills
// the bottom.
import {useLayoutEffect, useRef} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useActions, useWords, useSignalValue} from './base.js';
import {Button} from './controls.js';

// ⚠️ How far in from the right a desktop toast reaches, in px: a composer ending left of it is not under it.
const DESK_REACH = 480;

// floorOf is how far above the bottom of the screen the controls under the toasts reach, in px: on a phone the
// topmost full-screen page's foot and composer, on a desktop a composer at the right.
export function floorOf(doc, phone = true) {
  const view = doc.defaultView, height = view?.innerHeight || 0;
  let parts;
  if (phone) {
    const overs = doc.querySelectorAll?.('.page-over') || [];
    const over = overs[overs.length - 1];
    parts = over ? [...over.querySelectorAll('.page-foot, .composer-phone')] : [];
  } else parts = [...(doc.querySelectorAll?.('.composer') || [])];
  const tops = parts.map(x => x.getBoundingClientRect?.()).filter(r => r?.height > 0 && (phone || r.right > (view?.innerWidth || 0) - DESK_REACH)).map(r => r.top);
  return tops.length && height ? Math.max(0, height - Math.min(...tops)) : 0;
}

export function Toasts({toasts}) {
  const phone = usePhone();
  const {t} = useWords();
  const list = useSignalValue(toasts.list);
  const box = useRef(null);
  useActions('global', {undo: {run: () => toasts.undo(), when: () => toasts.canUndo()}});
  useLayoutEffect(() => {
    const el = box.current;
    if (list.length && el?.ownerDocument) el.style?.setProperty?.('--toast-floor', floorOf(el.ownerDocument, phone) + 'px');
  }, [list, phone]);
  return html`<div class=${cx('toasts', phone && 'toasts-phone')} ref=${box} role="status" aria-live="polite">
    ${list.map(x => html`<div class=${cx('toast', x.tone && 'toast-' + x.tone)} key=${x.id}>
      <span class="toast-text">${x.text}</span>
      ${x.act && html`<${Button} kind="quiet" onClick=${() => { toasts.dismiss(x.id); x.act.run(); }}>${x.act.label}<//>`}
      ${x.undo ? html`<${Button} kind="quiet" keyName="Mod+Z" onClick=${() => toasts.undo(x.id)}>${t('ui.undo')}<//>`
        : html`<${Button} kind="quiet" onClick=${() => toasts.dismiss(x.id)}>${t('ui.dismiss')}<//>`}
    </div>`)}
  </div>`;
}
