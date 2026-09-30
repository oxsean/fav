// desk is a phone page's way to what only a computer does there: it hands the address of the page that does it on
// (the system's share sheet, else the clipboard) to be opened on a computer. OnDesktop is one line saying what that is,
// DeskLinks a list of them.
import {html, useWords} from './base.js';
import {Button} from './controls.js';
import {register} from '../core/i18n.js';
import {format} from '../core/router.js';

register('desk', {
  'desk.open': ['在电脑上打开', 'Open on a computer'], 'desk.web': ['打开网页 ↗', 'Open the page ↗'],
  'desk.copied': ['链接已复制，到电脑上打开它', 'The link is copied: open it on a computer'],
  'desk.failed': ['到电脑上打开这个地址：%s', 'Open this address on a computer: %s'],
});

// useHandOn answers the function that hands page's address on, saying how that went.
function useHandOn(platform, toasts) {
  const {t, f} = useWords();
  return page => {
    const q = format({page});
    const url = platform.link(q.startsWith('/') ? q : '/' + q);
    platform.share(url).then(r => { if (r === 'copied') toasts.show({text: t('desk.copied')}); },
      () => toasts.show({text: f('desk.failed', url)}));
  };
}

// OnDesktop: note says what the computer does on page.
export function OnDesktop({platform, toasts, page, note}) {
  const {t} = useWords();
  const handOn = useHandOn(platform, toasts);
  return html`<div class="desk"><span class="t-muted">${note}</span>
    <${Button} kind="quiet" onClick=${() => handOn(page)}>${t('desk.open')} ↗<//></div>`;
}

// DeskLinks: items [{label, page}].
export function DeskLinks({platform, toasts, items}) {
  const {t} = useWords();
  const handOn = useHandOn(platform, toasts);
  return html`<ul class="cards">${items.map(x => html`<li key=${x.label}><button type="button" class="card-row desk-row" onClick=${() => handOn(x.page)}>
    <span class="card-main"><span class="card-primary">${x.label}</span></span><span class="desk-web">${t('desk.web')}</span></button></li>`)}</ul>`;
}
