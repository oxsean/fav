// Picker chooses one value, or several, from a list found by typing: a parent task, the tasks one comes after, a
// machine or an agent. On a desktop it opens under its button; on a phone it takes the whole screen. Each option may
// carry a state (online, busy) and a note under it.
import {useState, useRef, useEffect, useId} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useKeys} from './base.js';
import {Modal} from './overlay.js';
import {Status} from './status.js';
import {Icon} from './icons.js';
import {rank} from '../core/actions.js';

function Choices({options, value, multi, onPick, query, setQuery, box, onKeyDown, at, placeholder}) {
  const {t} = useWords();
  const on = v => (multi ? (value || []).includes(v) : value === v);
  return html`<div class="picker-in">
    <input class="in picker-q" type="search" autocomplete="off" spellcheck="false" autofocus value=${query} placeholder=${placeholder || t('picker.find')}
      aria-label=${placeholder || t('picker.find')} onInput=${e => setQuery(e.currentTarget.value)} onKeyDown=${onKeyDown} />
    <ul class="picker-list" role="listbox" aria-multiselectable=${multi ? 'true' : undefined} ref=${box}>
      ${options.length ? options.map((o, i) => html`<li key=${o.value}>
        <button type="button" role="option" aria-selected=${on(o.value) ? 'true' : 'false'} disabled=${o.disabled}
          class=${cx('pk', on(o.value) && 'on', i === at && 'at')} onClick=${() => onPick(o.value)}>
          ${multi && html`<span class="pk-box mono" aria-hidden="true">${on(o.value) ? '✓' : ''}</span>`}
          ${o.state && html`<${Status} state=${o.state} />`}
          <span class="pk-text"><span class="pk-label">${o.label}</span>${o.sub && html`<span class="pk-sub">${o.sub}</span>`}</span>
          ${o.note && html`<span class=${cx('pk-note', o.tone && 't-' + o.tone)}>${o.note}</span>`}
        </button>
      </li>`) : html`<li class="pk-none">${t('picker.none')}</li>`}
    </ul>
  </div>`;
}

function Pop({close, children}) {
  useKeys('modal', [{key: 'Esc', run: close}], {blocks: true});
  return html`<div class="menu-scrim" onClick=${close}></div><div class="picker-pop" role="dialog">${children}</div>`;
}

// Picker: options [{value, label, sub, state, note, tone, disabled, words}]; value is one value, or an array when
// multi; onChange gets the new value. empty names the choice of nothing ("" is a value too when an option has it).
export function Picker({label, options, value, onChange, multi = false, placeholder, empty, note, error}) {
  const phone = usePhone();
  const {t} = useWords();
  const id = useId();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [at, setAt] = useState(0);
  const button = useRef(null), box = useRef(null);
  const found = rank(options.map(o => ({...o, words: [o.label, o.value, o.sub || '', ...(o.words || [])]})), query);
  useEffect(() => setAt(0), [query]);
  const close = () => { setOpen(false); setQuery(''); button.current?.focus?.(); };
  const pick = v => {
    if (!multi) { onChange(v); close(); return; }
    const list = value || [];
    onChange(list.includes(v) ? list.filter(x => x !== v) : [...list, v]);
  };
  const onKeyDown = e => {
    if (e.isComposing) return;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (found.length) setAt((at + (e.key === 'ArrowDown' ? 1 : found.length - 1)) % found.length);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const o = found[at];
      if (o && !o.disabled) pick(o.value);
    }
  };
  const chosen = multi ? options.filter(o => (value || []).includes(o.value)) : options.filter(o => o.value === value);
  const shown = chosen.length ? chosen.map(o => o.label).join(', ') : (empty || t('picker.nothing'));
  const choices = html`<${Choices} options=${found} value=${value} multi=${multi} onPick=${pick} query=${query} setQuery=${setQuery}
    box=${box} onKeyDown=${onKeyDown} at=${at} placeholder=${placeholder} />`;
  return html`<div class=${cx('field', error && 'field-error')}>
    ${label && html`<label for=${id}>${label}</label>`}
    <span class="picker-wrap">
      <button type="button" id=${id} class=${cx('in', 'picker-btn', !chosen.length && 't-muted')} ref=${button} aria-haspopup="listbox"
        aria-expanded=${open ? 'true' : 'false'} onClick=${() => setOpen(!open)}>
        ${!multi && chosen[0]?.state && html`<${Status} state=${chosen[0].state} />`}<span class="ell">${shown}</span><${Icon} name="down" />
      </button>
      ${open && !phone && html`<${Pop} close=${close}>${choices}${multi && html`<div class="picker-foot"><button type="button" class="btn primary" onClick=${close}>${t('picker.done')}</button></div>`}<//>`}
    </span>
    ${error ? html`<span class="field-note t-failed" role="alert">${error}</span>` : note && html`<span class="field-note">${note}</span>`}
    ${open && phone && html`<${Modal} title=${label || t('picker.find')} onClose=${close} full actions=${multi ? [{label: t('picker.done'), kind: 'primary', onClick: close}] : []}>${choices}<//>`}
  </div>`;
}
