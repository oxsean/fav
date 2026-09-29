// Shell is the frame of every page. On a desktop: the top bar, the sidebar (collapsed with [, kept in the browser)
// and the key bar; on a phone: a slimmer top bar and four tabs at the bottom. Both show a banner while the connection
// is down or the server has moved on, and the toasts.
import {html, cx, usePhone, useKeys, useWords, useSignalValue, KeysContext} from './base.js';
import {Button, Kbd} from './controls.js';
import {Status} from './status.js';
import {Icon} from './icons.js';
import {Toasts} from './toast.js';
import {format} from '../core/router.js';

export const navPages = ['home', 'tasks', 'runs', 'machines', 'agents', 'team', 'me'];
const icons = {home: 'home', tasks: 'tasks', runs: 'runs', machines: 'machines', agents: 'agents', team: 'team', me: 'me'};

// ⚠️ The phone has four tabs; the other pages sit under the tab they are reached from.
export const phoneTabs = [
  {id: 'home', label: 'nav.waiting', icon: 'inbox'},
  {id: 'tasks', label: 'nav.tasks', icon: 'tasks'},
  {id: 'runs', label: 'nav.runs', icon: 'runs'},
  {id: 'me', label: 'nav.me', icon: 'person'},
];
export const tabOf = page => ({machines: 'runs', agents: 'me', team: 'me'})[page] || page;

function link(page, onNavigate) {
  return {
    href: format({page}),
    onClick: e => {
      if (e.metaKey || e.ctrlKey || e.shiftKey) return;
      e.preventDefault();
      onNavigate(page);
    },
  };
}

// Banner says the connection is down (with a way to retry now) or the page is older than the server.
export function Banner({wire, onReload}) {
  const {t} = useWords();
  const status = useSignalValue(wire.status);
  if (status === 'offline') {
    return html`<div class="banner" role="alert"><span>${t('banner.offline')}</span><${Button} kind="quiet" onClick=${() => wire.reconnect()}>${t('banner.retry')}<//></div>`;
  }
  if (status === 'outdated') {
    return html`<div class="banner" role="alert"><span>${t('banner.outdated')}</span><${Button} kind="primary" onClick=${onReload}>${t('banner.reload')}<//></div>`;
  }
  return null;
}

// barGroups joins the keys in force that share a label: "j k" for one move, "1–9" for a run of digits.
export function barGroups(bindings) {
  const groups = [];
  for (const b of bindings) {
    if (!b.label) continue;
    const g = groups.find(x => x.label === b.label);
    if (g) g.keys.push(b.key); else groups.push({label: b.label, keys: [b.key]});
  }
  return groups.map(g => {
    const d = g.keys.filter(k => /^[1-9]$/.test(k));
    return d.length > 2 && d.length === g.keys.length ? {label: g.label, keys: [d[0]], to: d[d.length - 1]} : g;
  });
}

export function KeyBar({keys}) {
  const {t} = useWords();
  useSignalValue(keys.changed);
  const groups = barGroups(keys.active());
  return html`<footer class="keybar" aria-label=${t('shell.keys')}>
    ${groups.map(g => html`<span class="kb">${g.keys.map(k => html`<${Kbd} k=${k} />`)}${g.to && html`–<${Kbd} k=${g.to} />`} ${t(g.label)}</span>`)}
  </footer>`;
}

function Counts({counts}) {
  const {t, f} = useWords();
  const off = counts.offline || [];
  return html`<span class="top-counts">
    <span><${Status} state="running" /> ${t('shell.running')} <b class="mono">${counts.running || 0}</b></span>
    <span><${Status} state="queued" /> ${t('shell.queued')} <b class="mono">${counts.queued || 0}</b></span>
    ${off.length > 0 && html`<span class="t-failed"><${Status} state="offline" /> ${off.length === 1 ? f('shell.offlineOne', off[0]) : f('shell.offlineMany', off.length)}</span>`}
  </span>`;
}

function Nav({nav, page, onNavigate, navCounts, waiting, onNew}) {
  const {t} = useWords();
  const open = useSignalValue(nav.open);
  return html`<nav class=${cx('nav', open ? 'nav-open' : 'nav-closed')} aria-label=${t('nav.label')}>
    ${navPages.map(p => {
      const label = t('nav.' + p), count = p === 'home' ? waiting : navCounts?.[p], alert = p === 'home' && waiting > 0;
      return html`<a class=${cx('nav-item', p === page && 'on')} ...${link(p, onNavigate)} aria-current=${p === page ? 'page' : undefined}
        title=${open ? undefined : label} aria-label=${open ? undefined : label}>
        <${Icon} name=${icons[p]} />
        ${open && html`<span class="nav-label">${label}</span>${count !== undefined && count !== 0 && html`<span class=${cx('mono', 'nav-count', alert && 'alert')}>${count}</span>`}`}
        ${!open && alert && html`<span class="badge mono">${waiting}</span>`}
      </a>`;
    })}
    <div class="nav-foot">
      ${onNew && html`<${Button} kind="primary" icon="plus" keyName=${open ? 'n' : ''} wide=${open} label=${t('shell.new')} onClick=${onNew}>${open ? t('shell.new') : undefined}<//>`}
      <${Button} kind="quiet" icon=${open ? 'collapse' : 'expand'} keyName=${open ? '[' : ''} label=${t(open ? 'nav.collapse' : 'nav.expand')} onClick=${nav.toggle}>${open ? t('nav.collapse') : undefined}<//>
    </div>
  </nav>`;
}

function TabBar({page, onNavigate, waiting}) {
  const {t} = useWords();
  const at = tabOf(page);
  return html`<nav class="tabbar" aria-label=${t('nav.label')}>
    ${phoneTabs.map(x => html`<a class=${cx('tab-item', x.id === at && 'on')} ...${link(x.id, onNavigate)} aria-current=${x.id === at ? 'page' : undefined}>
      <${Icon} name=${x.icon} size=${20} />${t(x.label)}${x.id === 'home' && waiting > 0 && html`<span class="badge mono">${waiting}</span>`}
    </a>`)}
  </nav>`;
}

// Shell: keys and wire are core's; nav is core/layout's createNav; counts {running, queued, offline: [machine],
// waiting}; navCounts by page; onServers opens the server list (the app only), onSearch the palette.
export function Shell({keys, wire, nav, toasts, page, onNavigate, counts = {}, navCounts, user, server, onServers, onSearch, onNew, onReload, children}) {
  return html`<${KeysContext.Provider} value=${keys}><${Frame} keys=${keys} wire=${wire} nav=${nav} toasts=${toasts} page=${page} onNavigate=${onNavigate}
    counts=${counts} navCounts=${navCounts} user=${user} server=${server} onServers=${onServers} onSearch=${onSearch} onNew=${onNew} onReload=${onReload}>${children}<//><//>`;
}

function Frame({keys, wire, nav, toasts, page, onNavigate, counts, navCounts, user, server, onServers, onSearch, onNew, onReload, children}) {
  const phone = usePhone();
  const {t, f} = useWords();
  const open = useSignalValue(nav.open);
  useKeys('global', [{key: '[', label: open ? 'nav.collapse' : 'nav.expand', run: nav.toggle}], {active: !phone});
  const waiting = counts.waiting || 0;
  if (phone) {
    const off = (counts.offline || []).length;
    return html`<div class="shell" data-form="phone">
      <${Banner} wire=${wire} onReload=${onReload} />
      <header class="topbar">
        <span class="mark mono" aria-hidden="true">>_</span>
        ${onServers ? html`<button type="button" class="srv" aria-haspopup="dialog" onClick=${onServers} title=${t('shell.servers')}>${server?.name || 'tend'} <small>▾</small></button>`
          : html`<b class="brand mono">tend</b>`}
        <span class="top-counts mono" title=${f('shell.counts', counts.running || 0, counts.queued || 0)}>
          <${Status} state="running" /> ${counts.running || 0} <${Status} state="queued" /> ${counts.queued || 0}${off > 0 && html` <${Status} state="offline" /> ${off}`}
        </span>
      </header>
      <main class="main" id="main">${children}</main>
      <${TabBar} page=${page} onNavigate=${onNavigate} waiting=${waiting} />
      <${Toasts} toasts=${toasts} />
    </div>`;
  }
  return html`<div class="shell" data-form="desktop">
    <${Banner} wire=${wire} onReload=${onReload} />
    <header class="topbar">
      <a class="brand mono" ...${link('home', onNavigate)}><span class="mark" aria-hidden="true">>_</span>tend</a>
      <button type="button" class="btn search" onClick=${onSearch}><${Icon} name="search" /><span class="search-text">${t('shell.search')}</span><${Kbd} k="Mod+K" /></button>
      <div class="top-right">
        <${Counts} counts=${counts} />
        <span class="divider" aria-hidden="true"></span>
        ${user && html`<${Button} kind="quiet" icon="person" onClick=${() => onNavigate('me')}>${user.name}<//>`}
      </div>
    </header>
    <div class="frame">
      <${Nav} nav=${nav} page=${page} onNavigate=${onNavigate} navCounts=${navCounts} waiting=${waiting} onNew=${onNew} />
      <main class="main" id="main">${children}</main>
    </div>
    <${KeyBar} keys=${keys} />
    <${Toasts} toasts=${toasts} />
  </div>`;
}
