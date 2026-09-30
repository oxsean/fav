// TextInput and TextArea are the page's text fields: a label above, a note or an error under it.
import {useId} from '../vendor/hooks.mjs';
import {html, cx} from './base.js';

function field(id, label, note, error, control) {
  return html`<div class=${cx('field', error && 'field-error')}>
    ${label && html`<label for=${id}>${label}</label>`}
    ${control}
    ${error ? html`<span class="field-note t-failed" role="alert">${error}</span>` : note && html`<span class="field-note">${note}</span>`}
  </div>`;
}

// TextInput: value and onInput(text) keep it controlled; type is text or password.
export function TextInput({label, value, onInput, type = 'text', placeholder, note, error, autoFocus, onKeyDown, name, mono = false, inputRef}) {
  const id = useId();
  return field(id, label, note, error, html`<input id=${id} class=${cx('in', mono && 'mono')} type=${type} name=${name} value=${value}
    placeholder=${placeholder} autofocus=${autoFocus} autocomplete="off" spellcheck="false" ref=${inputRef}
    onInput=${e => onInput(e.currentTarget.value)} onKeyDown=${onKeyDown} aria-invalid=${error ? 'true' : undefined} />`);
}

export function TextArea({label, value, onInput, placeholder, note, error, rows = 3, onKeyDown, mono = false}) {
  const id = useId();
  return field(id, label, note, error, html`<textarea id=${id} class=${cx('in', mono && 'mono')} rows=${rows} value=${value} placeholder=${placeholder}
    spellcheck=${mono ? 'false' : undefined} onInput=${e => onInput(e.currentTarget.value)} onKeyDown=${onKeyDown}></textarea>`);
}
