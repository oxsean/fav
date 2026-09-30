// controls are the small interactive pieces: Button, Kbd, Chip, Segmented and Tabs. On a phone they grow to touch
// size and drop the key caps, which a phone has no keys for.
import {html, cx, usePhone, keyParts, useMac} from './base.js';
import {Icon} from './icons.js';

// Kbd shows a key as the key table spells it ("Mod+K", "g h"); nothing on a phone.
export function Kbd({k}) {
  const phone = usePhone();
  const onMac = useMac();
  if (phone || !k) return null;
  return keyParts(k, onMac).map((p, i) => html`${i > 0 ? ' ' : ''}<kbd>${p}</kbd>`);
}

// Button: kind is primary, quiet or danger; on marks a pressed toggle; keyName shows its key; icon alone needs a label.
export function Button({kind = '', on = false, keyName = '', icon = '', label = '', children, onClick, disabled = false, title, type = 'button', wide = false}) {
  const text = children ?? (icon ? '' : label);
  const only = icon && (text === '' || text === undefined || text === null);
  return html`<button type=${type} class=${cx('btn', kind, on && 'on', wide && 'wide', only && 'icon-only')} onClick=${onClick}
    disabled=${disabled} aria-pressed=${on ? 'true' : undefined} aria-label=${only ? label || title : undefined} title=${title ?? (only ? label : undefined)}>
    ${icon && html`<${Icon} name=${icon} />`}${!only && text}<${Kbd} k=${keyName} />
  </button>`;
}

// Chip: a tag, or with onClick a filter that is on or off; count follows the label.
export function Chip({label, on = false, count, onClick}) {
  const inner = html`${label}${count !== undefined && html` <span class="mono count">${count}</span>`}`;
  if (!onClick) return html`<span class="chip">${inner}</span>`;
  return html`<button type="button" class=${cx('chip', 'filter', on && 'on')} aria-pressed=${on ? 'true' : 'false'} onClick=${onClick}>${inner}</button>`;
}

// Chips is a row of filter chips; on a phone it scrolls sideways.
export function Chips({label, children}) {
  return html`<div class="chips" role="group" aria-label=${label}>${children}</div>`;
}

// arrowStep moves an index by ←/→ (and ↑/↓), wrapping; null for any other key.
export function arrowStep(key, i, n) {
  if (key === 'ArrowRight' || key === 'ArrowDown') return (i + 1) % n;
  if (key === 'ArrowLeft' || key === 'ArrowUp') return (i + n - 1) % n;
  if (key === 'Home') return 0;
  if (key === 'End') return n - 1;
  return null;
}

// Segmented picks one of a few options ({value, label}); arrows move the pick.
export function Segmented({label, options, value, onChange}) {
  const at = Math.max(0, options.findIndex(o => o.value === value));
  const onKeyDown = e => {
    const j = arrowStep(e.key, at, options.length);
    if (j === null) return;
    e.preventDefault();
    onChange(options[j].value);
  };
  return html`<div class="seg" role="radiogroup" aria-label=${label} onKeyDown=${onKeyDown}>
    ${options.map((o, i) => html`<button type="button" role="radio" aria-checked=${i === at ? 'true' : 'false'} tabindex=${i === at ? 0 : -1}
      class=${cx(i === at && 'on')} onClick=${() => onChange(o.value)}>${o.label}</button>`)}
  </div>`;
}

// Check is a checkbox with its label beside it; note says more under it.
export function Check({label, on = false, onChange, note, disabled = false}) {
  return html`<button type="button" role="checkbox" aria-checked=${on ? 'true' : 'false'} class=${cx('check', on && 'on')} disabled=${disabled} onClick=${() => onChange(!on)}>
    <span class="check-box" aria-hidden="true">${on ? '✓' : ''}</span><span class="check-text"><span>${label}</span>${note && html`<span class="field-note">${note}</span>`}</span>
  </button>`;
}

// Tabs switches the panes of one page ({id, label, count}); arrows move between tabs, the picked one is in the tab order.
export function Tabs({label, tabs, value, onChange, idPrefix = 'tab'}) {
  const at = Math.max(0, tabs.findIndex(x => x.id === value));
  const onKeyDown = e => {
    const j = arrowStep(e.key, at, tabs.length);
    if (j === null) return;
    e.preventDefault();
    onChange(tabs[j].id);
  };
  return html`<div class="tabs" role="tablist" aria-label=${label} onKeyDown=${onKeyDown}>
    ${tabs.map((x, i) => html`<button type="button" role="tab" id=${idPrefix + '-' + x.id} aria-selected=${i === at ? 'true' : 'false'}
      aria-controls=${idPrefix + '-' + x.id + '-pane'} tabindex=${i === at ? 0 : -1} class=${cx('tab', i === at && 'on')} onClick=${() => onChange(x.id)}>
      ${x.label}${x.count !== undefined && html` <span class="mono count">${x.count}</span>`}
    </button>`)}
  </div>`;
}
