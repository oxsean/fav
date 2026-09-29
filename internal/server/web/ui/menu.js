// Menu is a button that opens a short list of choices under it. While open it holds the keys: arrows move, Esc and a
// click outside close it, and focus goes back to its button.
import {useState, useRef, useEffect} from '../vendor/hooks.mjs';
import {html, cx, useKeys} from './base.js';
import {Icon} from './icons.js';
import {arrowStep} from './controls.js';
import {focusables} from './overlay.js';

function Items({items, close, box}) {
  useKeys('modal', [{key: 'Esc', run: close}], {blocks: true});
  useEffect(() => { focusables(box.current)[0]?.focus(); }, []);
  const onKeyDown = e => {
    const list = focusables(box.current);
    const j = arrowStep(e.key, list.indexOf(box.current.ownerDocument.activeElement), list.length);
    if (j === null || !list.length) return;
    e.preventDefault();
    list[j].focus();
  };
  return html`<div class="menu-scrim" onClick=${close}></div>
    <div class="menu" role="menu" ref=${box} onKeyDown=${onKeyDown}>
      ${items.map(x => html`<button type="button" role="menuitem" class=${cx('menu-item', x.kind)} onClick=${() => { close(); x.onClick(); }}>${x.label}</button>`)}
    </div>`;
}

// Menu: items [{label, onClick, kind}]; label and icon draw its button, which disabled greys.
export function Menu({label, icon, items, disabled = false}) {
  const [open, setOpen] = useState(false);
  const button = useRef(null), box = useRef(null);
  const close = () => { setOpen(false); button.current?.focus?.(); };
  return html`<span class="menu-wrap">
    <button type="button" class="btn quiet" ref=${button} aria-haspopup="menu" aria-expanded=${open ? 'true' : 'false'} disabled=${disabled} onClick=${() => setOpen(!open)}>
      ${icon && html`<${Icon} name=${icon} />`}${label}
    </button>
    ${open && !disabled && html`<${Items} items=${items} close=${close} box=${box} />`}
  </span>`;
}
