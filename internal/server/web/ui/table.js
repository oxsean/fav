// Table lists records by a column table. On a desktop it is a grid of rows with sortable headers; on a phone each row
// is a card that shows the columns the table marks for it. Keys move the selection, which is kept by record id, so it
// survives a sort or a push; above VIRTUAL_ABOVE rows only those in view are drawn.
import {useState, useMemo, useRef, useEffect} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useKeys, useWords} from './base.js';

export const VIRTUAL_ABOVE = 200;
const overscan = 8;

// windowOf is the slice of count rows of rowHeight to draw for a viewport of height scrolled to top, with the space
// before and after it.
export function windowOf({count, rowHeight, top, height}) {
  const start = Math.max(0, Math.floor(top / rowHeight) - overscan);
  const end = Math.min(count, Math.ceil((top + height) / rowHeight) + overscan);
  return {start, end, before: start * rowHeight, after: Math.max(0, count - end) * rowHeight};
}

// scrollFor is where a viewport must scroll so row index shows, or null when it does already.
export function scrollFor({index, rowHeight, top, height}) {
  if (index * rowHeight < top) return index * rowHeight;
  if ((index + 1) * rowHeight > top + height) return (index + 1) * rowHeight - height;
  return null;
}

const digits = ['1', '2', '3', '4', '5', '6', '7', '8', '9'];

// useListKeys gives a list its keys: j / k (and the arrows) move the selection, Enter opens, Space expands, 1–9 pick.
export function useListKeys({ids, selected, onSelect, onOpen, onToggle, onPick, active = true}) {
  const move = step => {
    if (!ids.length) return;
    const i = ids.indexOf(selected);
    const j = i < 0 ? (step > 0 ? 0 : ids.length - 1) : Math.min(ids.length - 1, Math.max(0, i + step));
    onSelect?.(ids[j]);
  };
  const has = () => ids.includes(selected);
  useKeys('list', [
    {key: 'j', label: 'keys.move', run: () => move(1)},
    {key: 'k', label: 'keys.move', run: () => move(-1)},
    {key: 'ArrowDown', run: () => move(1)},
    {key: 'ArrowUp', run: () => move(-1)},
    ...(onOpen ? [{key: 'Enter', label: 'keys.open', run: () => onOpen(selected), when: has}] : []),
    ...(onToggle ? [{key: 'Space', label: 'keys.toggle', run: () => onToggle(selected), when: has}] : []),
    ...(onPick ? digits.map(d => ({key: d, label: 'keys.pick', run: () => onPick(Number(d), selected), when: has})) : []),
  ], {active});
}

const sortMark = {asc: '▲', desc: '▼'};

// Table: columns [{id, label, width, align, sort(a, b), render(row), mobile}], mobile being lead (before the text),
// primary, secondary, trailing or hidden (the default). rowHeight is the density's row in px; height is the
// viewport's until the page has measured it.
export function Table({label, columns, rows, rowKey = r => r.id, selected, onSelect, onOpen, onToggle, onPick, rowHeight, height = 480, active = true, empty}) {
  const phone = usePhone();
  const {t, f} = useWords();
  const [order, setOrder] = useState({by: '', dir: 'asc'});
  const [top, setTop] = useState(0);
  const body = useRef(null);
  const sorted = useMemo(() => {
    const col = columns.find(c => c.id === order.by);
    if (!col?.sort) return rows;
    const out = [...rows].sort(col.sort);
    return order.dir === 'desc' ? out.reverse() : out;
  }, [rows, columns, order.by, order.dir]);
  const ids = sorted.map(rowKey);
  useListKeys({ids, selected, onSelect, onOpen, onToggle, onPick, active});
  const rh = rowHeight || (phone ? 56 : 32);
  const view = body.current?.clientHeight || height;
  const virtual = sorted.length > VIRTUAL_ABOVE;
  const win = virtual ? windowOf({count: sorted.length, rowHeight: rh, top, height: view}) : {start: 0, end: sorted.length, before: 0, after: 0};
  useEffect(() => {
    const el = body.current, i = ids.indexOf(selected);
    if (!el || i < 0) return;
    const to = scrollFor({index: i, rowHeight: rh, top: el.scrollTop || 0, height: el.clientHeight || height});
    if (to !== null) { el.scrollTop = to; setTop(to); }
  }, [selected]);
  const onScroll = e => setTop(e.currentTarget.scrollTop);
  const cell = (c, r) => (c.render ? c.render(r) : r[c.id]);
  const shown = sorted.slice(win.start, win.end);
  const pad = h => h > 0 && html`<div class="spacer" aria-hidden="true" style=${{height: h + 'px'}}></div>`;
  if (!rows.length) return html`<div class="table-empty">${empty ?? t('ui.empty')}</div>`;

  if (phone) {
    const part = m => columns.filter(c => c.mobile === m);
    return html`<div class="cards-view" ref=${body} onScroll=${onScroll}>
      ${pad(win.before)}
      <ul class="cards" aria-label=${label}>
        ${shown.map(r => {
          const id = rowKey(r);
          return html`<li key=${id}><button type="button" class=${cx('card-row', id === selected && 'sel')} aria-current=${id === selected ? 'true' : undefined}
            onClick=${() => { onSelect?.(id); onOpen?.(id); }}>
            ${part('lead').map(c => html`<span class="card-lead">${cell(c, r)}</span>`)}
            <span class="card-main">
              <span class="card-primary">${part('primary').map((c, i) => html`${i > 0 ? ' · ' : ''}${cell(c, r)}`)}</span>
              ${part('secondary').length > 0 && html`<span class="card-secondary">${part('secondary').map((c, i) => html`${i > 0 ? ' · ' : ''}${cell(c, r)}`)}</span>`}
            </span>
            ${part('trailing').map(c => html`<span class="card-trailing mono">${cell(c, r)}</span>`)}
          </button></li>`;
        })}
      </ul>
      ${pad(win.after)}
    </div>`;
  }

  const grid = {gridTemplateColumns: columns.map(c => c.width || 'minmax(0, 1fr)').join(' ')};
  const head = c => {
    if (!c.sort) return c.label;
    const dir = order.by === c.id ? order.dir : '';
    const next = () => setOrder(dir === 'asc' ? {by: c.id, dir: 'desc'} : dir === 'desc' ? {by: '', dir: 'asc'} : {by: c.id, dir: 'asc'});
    return html`<button type="button" class="th-sort" onClick=${next} title=${f('ui.sortBy', c.label)}>${c.label}${dir && html` <span aria-hidden="true">${sortMark[dir]}</span>`}</button>`;
  };
  return html`<div class="table" role="grid" aria-label=${label} aria-rowcount=${sorted.length + 1}>
    <div class="thead" role="row" style=${grid}>
      ${columns.map(c => html`<div role="columnheader" class=${cx('th', c.align && 'align-' + c.align)}
        aria-sort=${order.by === c.id ? (order.dir === 'asc' ? 'ascending' : 'descending') : undefined}>${head(c)}</div>`)}
    </div>
    <div class="tbody" ref=${body} onScroll=${onScroll}>
      ${pad(win.before)}
      ${shown.map((r, i) => {
        const id = rowKey(r);
        return html`<div role="row" key=${id} class=${cx('tr', id === selected && 'sel')} aria-selected=${id === selected ? 'true' : 'false'}
          aria-rowindex=${win.start + i + 2} style=${grid} onClick=${() => onSelect?.(id)} onDblClick=${() => onOpen?.(id)}>
          ${columns.map(c => html`<div role="gridcell" class=${cx('td', c.align && 'align-' + c.align)}>${cell(c, r)}</div>`)}
        </div>`;
      })}
      ${pad(win.after)}
    </div>
  </div>`;
}
