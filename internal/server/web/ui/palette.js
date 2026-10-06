// Palette is ⌘K: every action the page can do now, found by its Chinese or English name, its id or its key, and
// what the page adds (tasks, runs, machines by title or id). Help is ?: the whole action table by group, every action that has a key.
import {useState} from '../vendor/hooks.mjs';
import {html, cx, useWords} from './base.js';
import {Modal} from './overlay.js';
import {Kbd} from './controls.js';
import {actions, groups, rank} from '../core/actions.js';
import {words} from '../core/i18n.js';

const limit = 12;

// Palette: entries are the actions runnable when it opened (runnable(keys.active())); find(q) gives the page's own
// hits [{id, title, sub, kind, run}] for a query.
export function Palette({entries, find = () => [], onClose}) {
  const {t} = useWords();
  const [q, setQ] = useState('');
  const [at, setAt] = useState(0);
  const acts = rank(entries.map(e => ({...e, kind: 'action', words: [...words.both(e.label), e.id, e.key]})), q);
  const items = [...acts, ...(q.trim() ? find(q.trim()).slice(0, limit) : [])];
  const picked = Math.min(at, Math.max(0, items.length - 1));
  const choose = i => {
    const it = items[i];
    if (!it) return;
    onClose();
    it.run();
  };
  const onKeyDown = e => {
    if (e.isComposing) return;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (items.length) setAt((picked + (e.key === 'ArrowDown' ? 1 : items.length - 1)) % items.length);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      choose(picked);
    }
  };
  return html`<${Modal} title=${t('act.palette')} onClose=${onClose} full>
    <input class="in palette-input" role="combobox" aria-expanded="true" aria-controls="palette-list" aria-autocomplete="list"
      aria-activedescendant=${items.length ? 'pal-' + picked : undefined} autocomplete="off" spellcheck="false" autofocus
      placeholder=${t('palette.hint')} aria-label=${t('palette.hint')} value=${q}
      onInput=${e => { setQ(e.currentTarget.value); setAt(0); }} onKeyDown=${onKeyDown} />
    <ul class="palette-list" id="palette-list" role="listbox" aria-label=${t('act.palette')}>
      ${items.length ? items.map((it, i) => html`<li role="option" id=${'pal-' + i} aria-selected=${i === picked ? 'true' : 'false'}
        class=${cx('pi', i === picked && 'on')} onClick=${() => choose(i)}>
        ${it.kind === 'action' ? html`<span class="pi-text">${t(it.label)}</span><${Kbd} k=${it.key} />`
          : html`<span class="pi-kind">${t('palette.' + it.kind)}</span><span class="pi-text ell">${it.title}</span><span class="mono pi-sub">${it.sub}</span>`}
      </li>`) : html`<li class="pi pi-none">${t('palette.none')}</li>`}
    </ul>
  <//>`;
}

// keysOf spells an action's keys for the shortcuts page: the digits as one range.
const keysOf = a => (a.keys.length > 2 && a.keys.every(k => /^[1-9]$/.test(k)) ? [a.keys[0], a.keys[a.keys.length - 1]] : a.keys);

export function Help({onClose}) {
  const {t} = useWords();
  return html`<${Modal} title=${t('act.help')} onClose=${onClose} full>
    <div class="help">
      ${groups.map(g => html`<section class="help-group"><h3>${t('group.' + g)}</h3>
        ${actions.filter(a => a.group === g && a.keys.length).map(a => {
          const ks = keysOf(a), range = ks.length === 2 && ks[0] === '1';
          return html`<div class="help-row"><span>${t('act.' + a.id)}</span><span class="help-keys">${range
            ? html`<${Kbd} k=${ks[0]} />–<${Kbd} k=${ks[1]} />` : ks.map(k => html`<${Kbd} k=${k} />`)}</span></div>`;
        })}
      </section>`)}
    </div>
  <//>`;
}
