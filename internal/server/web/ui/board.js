// Board lays records out in columns; a card is dragged onto another column where the page allows it. Only on a
// desktop: a phone lists the same columns as sections. The keys are a list's, across the columns in order.
import {useState} from '../vendor/hooks.mjs';
import {html, cx, useWords} from './base.js';
import {useListKeys} from './table.js';

// Board: columns [{id, label, cards: [{id, content}]}]; canDrop(card, column) says whether a drop is allowed, and
// onDrop(card, column) makes it; onRefused(card, column) says why when it is not.
export function Board({label, columns, selected, onSelect, onOpen, canDrop = () => false, onDrop, onRefused, active = true}) {
  const {t} = useWords();
  const [drag, setDrag] = useState('');
  const [over, setOver] = useState('');
  const ids = columns.flatMap(c => c.cards.map(x => x.id));
  useListKeys({ids, selected, onSelect, onOpen, active});
  const end = () => { setDrag(''); setOver(''); };
  return html`<div class="board" role="group" aria-label=${label}>
    ${columns.map(c => {
      const ok = drag && canDrop(drag, c.id);
      return html`<section key=${c.id} class=${cx('board-col', drag && (ok ? 'drop-ok' : 'drop-no'), over === c.id && 'drop-over')} aria-label=${c.label}
        onDragOver=${e => { if (!drag) return; if (ok) e.preventDefault(); setOver(c.id); }}
        onDragLeave=${() => setOver(o => (o === c.id ? '' : o))}
        onDrop=${e => { e.preventDefault(); const id = drag; end(); if (!id) return; if (canDrop(id, c.id)) onDrop(id, c.id); else onRefused?.(id, c.id); }}>
        <h3 class="board-head">${c.label}<span class="mono count">${c.cards.length}</span></h3>
        <ul class="board-cards">
          ${c.cards.length ? c.cards.map(x => html`<li key=${x.id}>
            <button type="button" draggable="true" class=${cx('board-card', x.id === selected && 'sel', x.id === drag && 'dragging')}
              aria-current=${x.id === selected ? 'true' : undefined}
              onDragStart=${e => { e.dataTransfer?.setData?.('text/plain', x.id); setDrag(x.id); }} onDragEnd=${end}
              onClick=${() => onSelect?.(x.id)} onDblClick=${() => onOpen?.(x.id)}>${x.content}</button>
          </li>`) : html`<li class="board-none">${t('ui.empty')}</li>`}
        </ul>
      </section>`;
    })}
  </div>`;
}
