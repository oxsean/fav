// ExpandItem is one entry of a list that opens in place (what waits on you): a row with the state, what is asked and
// how long it has waited, quick actions while closed, and the page's own content once open.
import {html, cx, usePhone, useWords} from './base.js';
import {Status} from './status.js';
import {Button} from './controls.js';

// actions [{label, kind, keyName, onClick}] show while it is closed; agePct (0–100) fills the waiting bar.
export function ExpandItem({id, open = false, selected = false, onToggle, state, title, sub, age, agePct, actions = [], children}) {
  const phone = usePhone();
  const {t} = useWords();
  const bodyID = id ? `expand-${id}` : undefined;
  const late = agePct >= 100;
  return html`<div class=${cx('xi', open && 'open', selected && 'sel', phone && 'xi-phone')}>
    <div class="xi-row">
      <button type="button" class="xi-head" aria-expanded=${open ? 'true' : 'false'} aria-controls=${bodyID} onClick=${onToggle}>
        ${!phone && html`<span class="xi-chev mono" aria-hidden="true">${open ? '▾' : '▸'}</span>`}
        <${Status} state=${state} />
        <span class="xi-text"><span class="xi-title" title=${open ? undefined : title}>${title}</span>${sub && html`<span class="xi-sub">${sub}</span>`}</span>
        <span class="xi-age">
          <span class=${cx('mono', late && 't-unknown')}>${age}</span>
          ${phone ? html`<span class="xi-toggle">${open ? t('ui.collapse') + ' ▴' : t('ui.expand') + ' ▾'}</span>`
            : agePct !== undefined && html`<span class="age-bar" aria-hidden="true"><span style=${{width: Math.min(100, Math.max(0, agePct)) + '%'}}></span></span>`}
        </span>
      </button>
      ${!open && actions.length > 0 && html`<span class="xi-actions">
        ${(phone ? actions.slice(0, 1) : actions).map(a => html`<${Button} kind=${phone ? 'quick' : a.kind} keyName=${a.keyName} onClick=${a.onClick}>${a.label}<//>`)}
      </span>`}
    </div>
    ${open && html`<div class="xi-body" id=${bodyID}>${children}</div>`}
  </div>`;
}
