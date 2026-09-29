// Toasts shows core/toasts.js's notices; the newest that can be undone is taken back by Mod+Z or its button.
import {html, cx, usePhone, useKeys, useWords, useSignalValue} from './base.js';
import {Button} from './controls.js';

export function Toasts({toasts}) {
  const phone = usePhone();
  const {t} = useWords();
  const list = useSignalValue(toasts.list);
  useKeys('global', [{key: 'Mod+Z', label: 'ui.undo', run: () => toasts.undo(), when: () => toasts.canUndo()}]);
  return html`<div class=${cx('toasts', phone && 'toasts-phone')} role="status" aria-live="polite">
    ${list.map(x => html`<div class=${cx('toast', x.tone && 'toast-' + x.tone)} key=${x.id}>
      <span class="toast-text">${x.text}</span>
      ${x.undo ? html`<${Button} kind="quiet" keyName="Mod+Z" onClick=${() => toasts.undo(x.id)}>${t('ui.undo')}<//>`
        : html`<${Button} kind="quiet" onClick=${() => toasts.dismiss(x.id)}>${t('ui.dismiss')}<//>`}
    </div>`)}
  </div>`;
}
