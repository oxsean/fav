// Panel is a bordered block with a heading; Stat is one figure of a metric strip.
import {html, cx, usePhone} from './base.js';

export function Panel({title, count, actions, children, level = 2}) {
  const phone = usePhone();
  const heading = level === 3 ? html`<h3>${title}</h3>` : html`<h2>${title}</h2>`;
  return html`<section class=${cx('panel', phone && 'card')}>
    ${title && html`<div class="ph">${heading}${count !== undefined && html`<span class="mono count">${count}</span>`}${actions && html`<span class="ph-actions">${actions}</span>`}</div>`}
    ${children}
  </section>`;
}

// Stat: label over value (tone colours it), a line of detail, and room for a small chart (children). With onClick the
// whole figure is a button.
export function Stat({label, value, tone = '', note, children, onClick}) {
  const phone = usePhone();
  const body = html`<span class="kl">${label}</span>
    <span class="kv-row"><span class=${cx('kv', tone && 't-' + tone)}>${value}</span>${note && html`<span class="kn">${note}</span>`}</span>
    ${children}`;
  const cls = cx('panel', 'kpi', phone && 'kpi-phone');
  return onClick ? html`<button type="button" class=${cls} onClick=${onClick}>${body}</button>` : html`<div class=${cls}>${body}</div>`;
}
