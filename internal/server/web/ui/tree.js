// TreeList is a list of records under their parents, indented, each with subtasks folded or not. The keys are a
// list's: j / k move, Enter opens, Space folds or unfolds the selected one.
import {html, cx, usePhone, useWords} from './base.js';
import {useListKeys} from './table.js';

// ⚠️ A task tree is three levels deep at most (task.MaxDepth).
const depths = 3;

// TreeList: rows [{id, depth, kids, open, lead, title, sub, trail}]; onFold(id) folds or unfolds one.
export function TreeList({label, rows, selected, onSelect, onOpen, onFold, active = true, empty}) {
  const phone = usePhone();
  const {t} = useWords();
  const ids = rows.map(r => r.id);
  useListKeys({ids, selected, onSelect, onOpen, onToggle: id => { if (rows.find(r => r.id === id)?.kids) onFold(id); }, active});
  if (!rows.length) return html`<div class="table-empty">${empty ?? t('ui.empty')}</div>`;
  return html`<ul class=${cx('tree', phone && 'tree-phone')} role="tree" aria-label=${label}>
    ${rows.map(r => html`<li key=${r.id} role="treeitem" aria-level=${r.depth + 1} aria-expanded=${r.kids ? (r.open ? 'true' : 'false') : undefined}
      aria-selected=${r.id === selected ? 'true' : 'false'} class=${cx('tree-row', 'depth-' + Math.min(r.depth, depths - 1), r.id === selected && 'sel')}>
      ${r.kids ? html`<button type="button" class="tree-fold mono" aria-label=${t(r.open ? 'ui.collapse' : 'ui.expand')} onClick=${() => onFold(r.id)}>${r.open ? '▾' : '▸'}</button>`
        : html`<span class="tree-fold" aria-hidden="true"></span>`}
      <button type="button" class="tree-main" onClick=${() => { onSelect?.(r.id); if (phone) onOpen?.(r.id); }} onDblClick=${() => onOpen?.(r.id)}>
        ${r.lead}<span class="tree-text"><span class="tree-title">${r.title}</span>${r.sub && html`<span class="tree-sub">${r.sub}</span>`}</span>
        ${r.trail && html`<span class="tree-trail mono">${r.trail}</span>`}
      </button>
    </li>`)}
  </ul>`;
}
