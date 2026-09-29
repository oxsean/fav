// Modal and Drawer lay something over the page. On a desktop a modal is a centred dialog and a drawer a panel at the
// right; on a phone a modal rises from the bottom as a sheet, and a drawer, or a modal holding a long form (full),
// takes the whole screen with a way back.
import {useEffect, useRef, useId} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useKeys, useWords} from './base.js';
import {Button} from './controls.js';
import {Icon} from './icons.js';

const focusTags = new Set(['BUTTON', 'INPUT', 'TEXTAREA', 'SELECT']);

// focusables are the elements under root that Tab reaches, in order.
export function focusables(root) {
  const out = [];
  const walk = n => {
    for (const c of n?.childNodes || []) {
      if (c.nodeType !== 1) continue;
      const tab = c.getAttribute('tabindex');
      const reach = tab !== null ? Number(tab) >= 0
        : focusTags.has(c.tagName) ? !c.disabled && c.getAttribute('type') !== 'hidden'
          : c.tagName === 'A' && c.getAttribute('href') !== null;
      if (reach) out.push(c);
      walk(c);
    }
  };
  walk(root);
  return out;
}

// useFocusTrap moves focus into the box when it opens, keeps Tab inside it, and gives focus back when it closes.
function useFocusTrap(ref) {
  useEffect(() => {
    const box = ref.current;
    if (!box) return;
    const before = box.ownerDocument?.activeElement;
    (focusables(box)[0] || box).focus?.();
    return () => before?.focus?.();
  }, []);
  return e => {
    if (e.key !== 'Tab') return;
    const list = focusables(ref.current);
    if (!list.length) return;
    const doc = ref.current.ownerDocument, first = list[0], last = list[list.length - 1];
    if (e.shiftKey && doc.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && doc.activeElement === last) { e.preventDefault(); first.focus(); }
  };
}

function actionButtons(actions, phone) {
  const ordered = phone ? [...actions].sort((a, b) => (b.kind === 'primary') - (a.kind === 'primary')) : actions;
  return ordered.map(a => html`<${Button} kind=${a.kind} keyName=${a.keyName} wide=${phone} disabled=${a.disabled} onClick=${a.onClick}>${a.label}<//>`);
}

function FullPage({titleID, title, onClose, onKeyDown, box, actions, children}) {
  const {t} = useWords();
  return html`<div class="page-over" role="dialog" aria-modal="true" aria-labelledby=${titleID} ref=${box} tabindex="-1" onKeyDown=${onKeyDown}>
    <header class="page-head"><button type="button" class="back" onClick=${onClose}><${Icon} name="back" />${t('ui.back')}</button><h2 id=${titleID}>${title}</h2></header>
    <div class="page-body">${children}</div>
    ${actions.length > 0 && html`<footer class="page-foot">${actionButtons(actions, true)}</footer>`}
  </div>`;
}

// Modal: actions [{label, kind, keyName, onClick, disabled}]; the primary one also runs on Mod+Enter. Esc and the
// backdrop close it; while open, no key reaches the page behind it.
export function Modal({title, onClose, actions = [], children, full = false}) {
  const phone = usePhone();
  const {t} = useWords();
  const titleID = useId();
  const box = useRef(null);
  const trap = useFocusTrap(box);
  const primary = actions.find(a => a.kind === 'primary' && !a.disabled);
  useKeys('modal', [{key: 'Esc', run: onClose}, ...(primary ? [{key: 'Mod+Enter', run: primary.onClick}] : [])], {blocks: true});
  if (phone && full) return html`<${FullPage} titleID=${titleID} title=${title} onClose=${onClose} onKeyDown=${trap} box=${box} actions=${actions}>${children}<//>`;
  if (phone) {
    return html`<div class="sheet-layer">
      <div class="scrim" onClick=${onClose}></div>
      <div class="sheet" role="dialog" aria-modal="true" aria-labelledby=${titleID} ref=${box} tabindex="-1" onKeyDown=${trap}>
        <div class="grab" aria-hidden="true"></div>
        <header class="sheet-head"><h2 id=${titleID}>${title}</h2></header>
        <div class="sheet-body">${children}</div>
        ${actions.length > 0 && html`<footer class="sheet-foot">${actionButtons(actions, true)}</footer>`}
      </div>
    </div>`;
  }
  return html`<div class="overlay" onClick=${e => { if (e.target === e.currentTarget) onClose(); }}>
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby=${titleID} ref=${box} tabindex="-1" onKeyDown=${trap}>
      <header class="modal-head"><h2 id=${titleID}>${title}</h2><${Button} kind="quiet" icon="close" label=${t('ui.close')} onClick=${onClose} /></header>
      <div class="modal-body">${children}</div>
      ${actions.length > 0 && html`<footer class="modal-foot">${actionButtons(actions, false)}</footer>`}
    </div>
  </div>`;
}

// Drawer shows one thing beside the page (a run, a machine) without leaving it; Esc closes it.
export function Drawer({title, onClose, actions = [], children}) {
  const phone = usePhone();
  const {t} = useWords();
  const titleID = useId();
  const box = useRef(null);
  const trap = useFocusTrap(box);
  useKeys('drawer', [{key: 'Esc', run: onClose}]);
  if (phone) return html`<${FullPage} titleID=${titleID} title=${title} onClose=${onClose} onKeyDown=${trap} box=${box} actions=${actions}>${children}<//>`;
  return html`<aside class="drawer" role="dialog" aria-labelledby=${titleID} ref=${box} tabindex="-1" onKeyDown=${trap}>
    <header class="modal-head"><h2 id=${titleID}>${title}</h2><${Button} kind="quiet" icon="close" label=${t('ui.close')} onClick=${onClose} /></header>
    <div class="modal-body">${children}</div>
    ${actions.length > 0 && html`<footer class="modal-foot">${actionButtons(actions, false)}</footer>`}
  </aside>`;
}
