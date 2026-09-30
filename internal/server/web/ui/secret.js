// Secret shows a value the server gives only once (a token, an invitation link, a webhook secret) with a way to copy it.
// Where the clipboard is refused the value is selected for the viewer to copy by hand.
import {useRef, useState} from '../vendor/hooks.mjs';
import {html, useWords} from './base.js';
import {Button} from './controls.js';
import {register} from '../core/i18n.js';

register('secret', {'secret.copy': ['复制', 'Copy'], 'secret.copied': ['已复制', 'Copied'], 'secret.select': ['已选中，请手动复制', 'Selected: copy it by hand']});

const clipboard = text => globalThis.navigator?.clipboard?.writeText(text) ?? Promise.reject(new Error('no clipboard'));

// Secret: label says what it is; copy(text) is the clipboard's (a test passes its own).
export function Secret({label, value, copy = clipboard}) {
  const {t} = useWords();
  const box = useRef(null);
  const [said, setSaid] = useState('');
  const onCopy = () => Promise.resolve().then(() => copy(value)).then(() => setSaid(t('secret.copied')), () => {
    const sel = box.current?.ownerDocument?.getSelection?.();
    sel?.selectAllChildren?.(box.current);
    setSaid(t('secret.select'));
  });
  return html`<div class="secret">
    ${label && html`<span class="brief-label">${label}</span>`}
    <div class="secret-row"><code class="mono secret-value" ref=${box}>${value}</code><${Button} onClick=${onCopy}>${t('secret.copy')}<//></div>
    ${said && html`<span class="field-note" role="status">${said}</span>`}
  </div>`;
}
