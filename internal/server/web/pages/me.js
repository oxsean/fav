// me is the viewer's own page: who they are, how the page looks in this browser, how they hear that something needs
// them (browser notices while the page is open, a personal webhook), the sign-in accounts linked to them, their
// personal tokens and their browser sessions. On a phone it keeps only the account, the ways into the team and agent
// pages (which only show there) and signing out.
import {useState, useEffect} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useSignalValue} from '../ui/base.js';
import {Panel} from '../ui/panel.js';
import {Modal} from '../ui/overlay.js';
import {Button, Segmented} from '../ui/controls.js';
import {TextInput} from '../ui/input.js';
import {Secret} from '../ui/secret.js';
import {register, t as say, f as fill} from '../core/i18n.js';
import {linkURL} from '../core/http.js';
import {themes, densities} from '../core/prefs.js';
import {clock, day} from '../core/format.js';
import {apiText} from './words.js';

register('me', {
  'me.look': ['外观', 'Look'], 'me.lookNote': ['只存在这个浏览器里，改了立刻生效', 'Kept in this browser only; changes apply at once'],
  'me.theme': ['主题', 'Theme'], 'me.skin': ['皮肤', 'Skin'], 'me.accent': ['强调色', 'Accent'],
  'me.accentNote': ['#rrggbb；留空用皮肤自带的。状态色不跟着变。', '#rrggbb; empty for the skin\'s own. Status colours stay as they are.'],
  'me.accentBad': ['要写成 #rrggbb', 'Write it as #rrggbb'],
  'me.contrast': ['对比度', 'Contrast'], 'me.contrast.normal': ['标准', 'Standard'], 'me.contrast.high': ['高（文字 7:1）', 'High (text 7:1)'],
  'me.density': ['密度', 'Density'], 'me.rowHeight': ['行高 %d px', 'Rows %d px high'],
  'me.lang': ['语言', 'Language'], 'me.lang.zh': ['中文', '中文'], 'me.lang.en': ['English', 'English'], 'me.lang.auto': ['跟随浏览器', 'The browser\'s'],
  'me.notices': ['通知', 'Notices'], 'me.noticesNote': ['有事等你时', 'When something needs you'],
  'me.browser': ['浏览器通知', 'Browser notices'], 'me.on': ['开', 'On'], 'me.off': ['关', 'Off'],
  'me.browser.granted': ['页面开着、在后台时弹出', 'Shown while the page is open in the background'],
  'me.browser.default': ['打开时浏览器会问你要不要允许', 'Turning them on, the browser asks whether to allow them'],
  'me.browser.denied': ['浏览器拒绝了通知：在浏览器的网站设置里允许 tend 再打开', 'The browser refuses them: allow tend in its site settings, then turn them on'],
  'me.browser.insecure': ['这个地址不是 HTTPS，浏览器不给通知', 'This address is not HTTPS, so the browser gives no notices'],
  'me.browser.none': ['这个浏览器不支持通知', 'This browser has no notices'],
  'me.webhook': ['个人 webhook', 'Personal webhook'], 'me.webhookSave': ['保存', 'Save'],
  'me.webhookNote': ['有事等你或任务结束时 POST 一段带 text 的 JSON；ntfy、Slack、企业微信都能收。留空就不发。', 'When something needs you or a task ends, it receives a JSON POST with a text field, which ntfy, Slack and the like take. Empty sends nothing.'],
  'me.webhookSaved': ['webhook 已保存', 'The webhook is saved'], 'me.webhookGone': ['webhook 已去掉', 'The webhook is removed'],
  'me.logins': ['登录方式', 'Sign-in accounts'], 'me.loginsNote': ['关联后任一种都能登录这个账号', 'Once linked, any of them signs in as you'],
  'me.link': ['关联', 'Link'], 'me.unlinked': ['没关联', 'not linked'], 'me.noLogins': ['这台 server 只用 token 登录', 'This server signs in with tokens only'],
  'me.linked': ['账号已关联', 'The account is linked'], 'me.linkTaken': ['这个账号已经属于另一个用户，没有关联', 'That account belongs to someone else; it was not linked'],
  'me.linkFailed': ['没有关联：登录没有完成（%s）', 'Not linked: the sign-in did not finish (%s)'],
  'me.tokens': ['token', 'Tokens'], 'me.tokensNote': ['给 CLI 和 TUI 用；值只在新建时显示一次', 'For the CLI and the TUI; the value shows only when made'],
  'me.noTokens': ['还没有 token：tend login 会建一个，或在这里新建', 'No token yet: tend login makes one, or make one here'],
  'me.newToken': ['新建 token', 'New token'], 'me.tokenName': ['名称', 'Name'], 'me.tokenNameNote': ['让你认得出它用在哪，最多 64 个字符', 'So you know where it is used; up to 64 characters'],
  'me.make': ['生成', 'Make it'], 'me.tokenValue': ['token（只显示这一次）', 'The token (shown only this once)'],
  'me.tokenHow': ['存进一个只有你能读的文件，在 tend 的 config.json 里把 coordinator.token_file 指过去；关掉后不再显示。', 'Save it to a file only you can read and point coordinator.token_file in tend\'s config.json at it; once closed it is not shown again.'],
  'me.done': ['完成', 'Done'], 'me.made': ['%s 已生成', '%s is made'],
  'me.madeAt': ['%s 建立', 'made %s'], 'me.usedAt': ['%s 用过', 'used %s'], 'me.unused': ['没用过', 'never used'], 'me.until': ['%s 到期', 'ends %s'],
  'me.revoke': ['吊销', 'Revoke'], 'me.revokeTitle': ['吊销 %s？', 'Revoke %s?'],
  'me.revokeNote': ['用它的 CLI 和 TUI 立刻断开，用它登录的网页会话一起失效，不能恢复。', 'The CLI and TUI using it disconnect at once, and so do the browser sessions it signed in; this cannot be undone.'],
  'me.revoked': ['%s 已吊销', '%s is revoked'],
  'me.sessions': ['浏览器会话', 'Browser sessions'], 'me.sessionsNote': ['30 天有效', 'Each lasts 30 days'],
  'me.thisBrowser': ['这个浏览器', 'This browser'], 'me.signedIn': ['浏览器登录', 'Browser sign-in'], 'me.viaToken': ['用 token %s 登录', 'Signed in with the token %s'],
  'me.current': ['就是这个', 'This one'], 'me.end': ['退出', 'Sign out'], 'me.endOthers': ['退出其他全部', 'Sign out all others'],
  'me.endTitle': ['让这个会话退出？', 'Sign this session out?'], 'me.endNote': ['那个浏览器下次打开 tend 时回到登录页。', 'That browser is back at the sign-in page the next time it opens tend.'],
  'me.ended': ['会话已退出', 'The session is signed out'],
  'me.endOthersTitle': ['退出其他 %d 个会话？', 'Sign out the %d other sessions?'], 'me.endOthersNote': ['只留下这个浏览器。', 'Only this browser stays signed in.'],
  'me.endedN': ['已退出 %d 个会话', '%d sessions signed out'],
  'me.team': ['团队', 'Team'], 'me.teamNote': ['成员和项目，在手机上只看', 'People and projects; a phone only shows them'],
  'me.agents': ['Agent', 'Agents'], 'me.agentsNote': ['定义和能跑的机器，在手机上只看', 'Definitions and where they run; a phone only shows them'],
});

// ⚠️ Where this tab remembers, across the sign-in's round trip, that it went to link an account (sessionStorage), and
// for how long that holds: the server forgets a sign-in it started after 10 minutes.
export const LINKING_KEY = 'tend-linking';
const LINKING_FOR = 10 * 60e3;

export const linking = (tab, now) => { try { tab?.setItem(LINKING_KEY, String(now)); } catch {} };

// returnedFromLink is what to say when this tab comes back from linking an account ({text, tone}), or null when it did
// not go to link one lately; auth is the route's fragment, tab the tab's storage. It says so once.
export function returnedFromLink(auth, tab, now) {
  let went = null;
  try { went = tab?.getItem(LINKING_KEY); tab?.removeItem(LINKING_KEY); } catch {}
  if (!went || !(now - Number(went) < LINKING_FOR)) return null;
  const problem = auth?.kind === 'signin' ? auth.value : '';
  if (!problem) return {text: say('me.linked')};
  if (problem === 'linked') return {text: say('me.linkTaken'), tone: 'danger'};
  return {text: fill('me.linkFailed', problem), tone: 'danger'};
}

const rowHeights = {compact: 28, default: 32, comfortable: 40};
const when = at => (at ? day(at) + ' ' + clock(at) : '');
const accentOf = v => v.trim().replace(/^#/, '').toLowerCase();

// permissionOf is where the browser stands on notices: granted, default, denied, insecure (not HTTPS) or none.
const permissionOf = api => (!api.secure ? 'insecure' : !api.Notification ? 'none' : api.Notification.permission || 'default');

function Look({prefs, presets}) {
  const {t, f} = useWords();
  const theme = useSignalValue(prefs.theme), density = useSignalValue(prefs.density), look = useSignalValue(prefs.look);
  const lang = useSignalValue(prefs.langChoice);
  const [accent, setAccent] = useState(look.accent ? '#' + look.accent : '');
  const bad = accent.trim() !== '' && !/^[0-9a-f]{6}$/.test(accentOf(accent));
  const onAccent = v => {
    setAccent(v);
    const a = accentOf(v);
    if (a === '' || /^[0-9a-f]{6}$/.test(a)) prefs.setLook({accent: a});
  };
  return html`<${Panel} title=${t('me.look')} actions=${html`<span class="t-muted">${t('me.lookNote')}</span>`}>
    <div class="me-sets">
      <div class="me-set"><span class="me-k">${t('me.theme')}</span>
        <${Segmented} label=${t('me.theme')} value=${theme} onChange=${prefs.setTheme} options=${themes.map(v => ({value: v, label: t('theme.' + v)}))} /></div>
      <div class="me-set"><span class="me-k">${t('me.skin')}</span>
        <span class="me-skins" role="radiogroup" aria-label=${t('me.skin')}>${presets.map(p => html`<button type="button" key=${p.name} role="radio"
          aria-checked=${p.name === look.skin ? 'true' : 'false'} class=${cx('me-skin', p.name === look.skin && 'on')} onClick=${() => prefs.setLook({skin: p.name})}>
          <span class="me-swatch" aria-hidden="true"><i style=${{background: p.input.base}}></i><i style=${{background: p.input.accent}}></i></span>${p.name}</button>`)}</span></div>
      <div class="me-set"><span class="me-k">${t('me.accent')}</span>
        <${TextInput} label=${t('me.accent')} value=${accent} onInput=${onAccent} mono placeholder="#315fa5" note=${bad ? null : t('me.accentNote')} error=${bad ? t('me.accentBad') : null} /></div>
      <div class="me-set"><span class="me-k">${t('me.contrast')}</span>
        <${Segmented} label=${t('me.contrast')} value=${look.high ? 'high' : 'normal'} onChange=${v => prefs.setLook({high: v === 'high'})}
          options=${['normal', 'high'].map(v => ({value: v, label: t('me.contrast.' + v)}))} /></div>
      <div class="me-set"><span class="me-k">${t('me.density')}</span>
        <span class="me-inline"><${Segmented} label=${t('me.density')} value=${density} onChange=${prefs.setDensity} options=${densities.map(v => ({value: v, label: t('density.' + v)}))} />
          <span class="t-muted mono">${f('me.rowHeight', rowHeights[density])}</span></span></div>
      <div class="me-set"><span class="me-k">${t('me.lang')}</span>
        <${Segmented} label=${t('me.lang')} value=${lang} onChange=${prefs.setLang} options=${['zh', 'en', 'auto'].map(v => ({value: v, label: t('me.lang.' + v)}))} /></div>
    </div>
  <//>`;
}

function Notices({prefs, notices, webhook, onWebhook, toasts}) {
  const {t} = useWords();
  const on = useSignalValue(prefs.notify);
  const [permission, setPermission] = useState(() => permissionOf(notices));
  const [url, setUrl] = useState(webhook ?? '');
  useEffect(() => { if (webhook !== null) setUrl(webhook); }, [webhook]);
  const shown = on && permission === 'granted' ? 'on' : 'off';
  const turn = v => {
    if (v === 'off') { prefs.setNotify(false); return; }
    if (permission === 'granted') { prefs.setNotify(true); return; }
    if (permission !== 'default') return;
    Promise.resolve(notices.Notification.requestPermission()).then(p => {
      setPermission(p);
      if (p === 'granted') prefs.setNotify(true); else toasts.show({text: t('me.browser.denied'), tone: 'danger'});
    }, () => {});
  };
  const usable = permission === 'granted' || permission === 'default';
  return html`<${Panel} title=${t('me.notices')} actions=${html`<span class="t-muted">${t('me.noticesNote')}</span>`}>
    <div class="me-sets">
      <div class="me-set"><span class="me-k">${t('me.browser')}</span>
        <span class="me-inline">${usable && html`<${Segmented} label=${t('me.browser')} value=${shown} onChange=${turn}
          options=${[{value: 'on', label: t('me.on')}, {value: 'off', label: t('me.off')}]} />`}
          <span class=${cx(usable ? 't-muted' : 't-warning')}>${t('me.browser.' + permission)}</span></span></div>
      <div class="me-set me-set-top"><span class="me-k">${t('me.webhook')}</span>
        <span class="me-hook"><span class="me-hook-row"><${TextInput} label=${t('me.webhook')} value=${url} onInput=${setUrl} mono placeholder="https://ntfy.sh/…" />
          <${Button} disabled=${webhook === null || url.trim() === (webhook || '')} onClick=${() => onWebhook(url.trim())}>${t('me.webhookSave')}<//></span>
          <span class="field-note">${t('me.webhookNote')}</span></span></div>
    </div>
  <//>`;
}

function Logins({logins, identities, onLink}) {
  const {t} = useWords();
  const display = p => logins.find(l => l.name === p)?.display || p;
  const loose = logins.filter(l => !identities.some(i => i.provider === l.name));
  return html`<${Panel} title=${t('me.logins')} actions=${html`<span class="t-muted">${t('me.loginsNote')}</span>`}>
    <ul class="me-rows">
      ${identities.map(i => html`<li class="me-row" key=${i.provider + i.subject}><span class="st s-success" aria-hidden="true">✓</span>
        <span class="me-main"><b>${display(i.provider)}</b><span class="mono t-muted">${i.username || i.email || i.subject}</span></span>
        ${i.username && i.email && html`<span class="t-muted me-side">${i.email}</span>`}</li>`)}
      ${loose.map(l => html`<li class="me-row" key=${l.name}><span class="st s-muted" aria-hidden="true">·</span>
        <span class="me-main"><b>${l.display || l.name}</b><span class="t-muted">${t('me.unlinked')}</span></span>
        <a class="btn me-link" href=${linkURL(l.name)} onClick=${onLink}>${t('me.link')}</a></li>`)}
      ${!identities.length && !logins.length && html`<li class="me-row t-muted">${t('me.noLogins')}</li>`}
    </ul>
  <//>`;
}

const facts = (w, c) => [w.f('me.madeAt', when(c.created)), c.last_used ? w.f('me.usedAt', when(c.last_used)) : w.t('me.unused'),
  c.expires && w.f('me.until', when(c.expires))].filter(Boolean).join(' · ');

function Tokens({tokens, onNew, onRevoke}) {
  const w = useWords();
  const {t} = w;
  return html`<${Panel} title=${t('me.tokens')} count=${tokens.length} actions=${html`<${Button} icon="plus" onClick=${onNew}>${t('me.newToken')}<//>`}>
    <p class="t-muted me-note">${t('me.tokensNote')}</p>
    <ul class="me-rows">
      ${tokens.map(c => html`<li class="me-row" key=${c.id}><span class="me-main"><span class="mono">${c.name}</span><span class="t-muted">${facts(w, c)}</span></span>
        <${Button} kind="quiet danger" onClick=${() => onRevoke(c)}>${t('me.revoke')}<//></li>`)}
      ${!tokens.length && html`<li class="me-row t-muted">${t('me.noTokens')}</li>`}
    </ul>
  <//>`;
}

function Sessions({sessions, tokens, onEnd, onEndOthers}) {
  const w = useWords();
  const {t, f} = w;
  const title = c => {
    if (c.current) return t('me.thisBrowser');
    const parent = c.name?.startsWith('token:') ? c.name.slice(6) : '';
    return parent ? f('me.viaToken', tokens.find(x => x.id === parent)?.name || parent) : t('me.signedIn');
  };
  const others = sessions.filter(c => !c.current);
  return html`<${Panel} title=${t('me.sessions')} count=${sessions.length}
    actions=${html`<span class="t-muted">${t('me.sessionsNote')}</span>${others.length > 0 && html`<${Button} kind="quiet" onClick=${onEndOthers}>${t('me.endOthers')}<//>`}`}>
    <ul class="me-rows">
      ${sessions.map(c => html`<li class="me-row" key=${c.id}><span class=${cx('st', c.current ? 's-success' : 's-muted')} aria-hidden="true">${c.current ? '●' : '○'}</span>
        <span class="me-main"><span>${title(c)}</span><span class="t-muted">${facts(w, c)}</span></span>
        ${c.current ? html`<span class="me-side me-current">${t('me.current')}</span>` : html`<${Button} kind="quiet" onClick=${() => onEnd(c)}>${t('me.end')}<//>`}</li>`)}
    </ul>
  <//>`;
}

function NewToken({busy, error, made, copy, onMake, onClose}) {
  const {t, f} = useWords();
  const [name, setName] = useState('');
  if (made) {
    return html`<${Modal} title=${f('me.made', made.name)} onClose=${onClose} actions=${[{label: t('me.done'), kind: 'primary', keyName: 'Mod+Enter', onClick: onClose}]}>
      <${Secret} label=${t('me.tokenValue')} value=${made.token} copy=${copy} />
      <p class="me-note">${t('me.tokenHow')}</p>
    <//>`;
  }
  const ok = name.trim() && name.trim().length <= 64 && !busy;
  return html`<${Modal} title=${t('me.newToken')} onClose=${onClose}
    actions=${[{label: t('home.cancel'), onClick: onClose}, {label: t('me.make'), kind: 'primary', keyName: 'Mod+Enter', disabled: !ok, onClick: () => onMake(name.trim())}]}>
    <${TextInput} label=${t('me.tokenName')} value=${name} onInput=${setName} mono autoFocus note=${t('me.tokenNameNote')} error=${error} />
  <//>`;
}

function Confirm({title, note, label, onGo, onClose}) {
  const {t} = useWords();
  return html`<${Modal} title=${title} onClose=${onClose} actions=${[{label: t('confirm.keep'), onClick: onClose},
    {label, kind: 'primary', keyName: 'Mod+Enter', onClick: () => { onClose(); onGo(); }}]}><p>${note}</p><//>`;
}

function Avatar({name}) {
  return html`<span class="me-avatar" aria-hidden="true">${(name || '?').slice(0, 1).toUpperCase()}</span>`;
}

// Me: session is who is signed in; http reads and changes their tokens, sessions, accounts and webhook; prefs are this
// browser's; notices are the browser's notices ({Notification, secure}); tab is the tab's storage (linking an account
// goes through it); clock is now; onLogout signs out; copy is the clipboard's.
export function Me({session, http, prefs, toasts, router, notices, tab, clock: now = () => Date.now(), copy, onLogout}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  const [creds, setCreds] = useState([]);
  const [identities, setIdentities] = useState([]);
  const [logins, setLogins] = useState([]);
  const [webhook, setWebhook] = useState(null);
  const [presets, setPresets] = useState([]);
  const [modal, setModal] = useState(null);
  const [busy, setBusy] = useState(false);
  const failed = e => toasts.show({text: apiText(w, e), tone: 'danger'});
  const readCreds = () => http.tokens().then(setCreds, failed);
  useEffect(() => {
    if (phone) return;
    readCreds();
    http.identities().then(setIdentities, () => {});
    http.logins().then(setLogins, () => {});
    http.webhook().then(setWebhook, () => {});
    http.presets().then(setPresets, () => {});
  }, [phone]);

  const role = session?.role ? t('role.' + session.role) : '';
  const sub = [session?.email || session?.username, role].filter(Boolean).join(' · ');

  if (phone) {
    return html`<div class="me me-phone">
      <ul class="cards">
        <li><div class="card-row me-card"><span class="card-lead"><${Avatar} name=${session?.name} /></span>
          <span class="card-main"><span class="card-primary">${session?.name}</span><span class="card-secondary">${sub}</span></span></div></li>
        <li><button type="button" class="card-row" onClick=${() => router.go({page: 'team'})}>
          <span class="card-main"><span class="card-primary">${t('me.team')}</span><span class="card-secondary">${t('me.teamNote')}</span></span></button></li>
        <li><button type="button" class="card-row" onClick=${() => router.go({page: 'agents'})}>
          <span class="card-main"><span class="card-primary">${t('me.agents')}</span><span class="card-secondary">${t('me.agentsNote')}</span></span></button></li>
      </ul>
      <${Button} kind="danger" wide onClick=${onLogout}>${t('app.logout')}<//>
    </div>`;
  }

  const tokens = creds.filter(c => c.kind === 'token');
  const sessions = creds.filter(c => c.kind === 'web').sort((a, b) => (b.current ? 1 : 0) - (a.current ? 1 : 0) || String(b.last_used || b.created).localeCompare(String(a.last_used || a.created)));
  const close = () => setModal(null);
  const make = name => {
    setBusy(true);
    http.addToken(name).then(v => { setModal({kind: 'new', made: {name, token: v.token}}); readCreds(); },
      e => setModal({kind: 'new', error: apiText(w, e)})).finally(() => setBusy(false));
  };
  const revoke = (c, text) => http.revokeToken(c.id).then(() => { toasts.show({text}); readCreds(); }, failed);
  const endOthers = async () => {
    let n = 0;
    try {
      for (const c of sessions.filter(x => !x.current)) { await http.revokeToken(c.id); n++; }
      toasts.show({text: f('me.endedN', n)});
    } catch (e) { failed(e); }
    readCreds();
  };
  const saveHook = url => http.setWebhook(url).then(() => { setWebhook(url); toasts.show({text: t(url ? 'me.webhookSaved' : 'me.webhookGone')}); }, failed);
  const others = sessions.filter(c => !c.current).length;
  const dialog = modal && ({
    new: () => html`<${NewToken} busy=${busy} error=${modal.error} made=${modal.made} copy=${copy} onMake=${make} onClose=${close} />`,
    revoke: () => html`<${Confirm} title=${f('me.revokeTitle', modal.cred.name)} note=${t('me.revokeNote')} label=${t('me.revoke')}
      onGo=${() => revoke(modal.cred, f('me.revoked', modal.cred.name))} onClose=${close} />`,
    end: () => html`<${Confirm} title=${t('me.endTitle')} note=${t('me.endNote')} label=${t('me.end')} onGo=${() => revoke(modal.cred, t('me.ended'))} onClose=${close} />`,
    others: () => html`<${Confirm} title=${f('me.endOthersTitle', others)} note=${t('me.endOthersNote')} label=${t('me.endOthers')} onGo=${endOthers} onClose=${close} />`,
  })[modal.kind]?.();

  return html`<div class="me">
    <div class="me-head"><${Avatar} name=${session?.name} />
      <span class="me-who"><h1 class="tasks-title">${session?.name}</h1><span class="mono t-muted">${sub}</span></span>
      <span class="me-head-acts"><${Button} onClick=${onLogout}>${t('app.logout')}<//></span></div>
    <div class="me-body">
      <div class="me-col">
        <${Look} prefs=${prefs} presets=${presets} />
        <${Notices} prefs=${prefs} notices=${notices} webhook=${webhook} onWebhook=${saveHook} toasts=${toasts} />
      </div>
      <div class="me-col">
        <${Logins} logins=${logins} identities=${identities} onLink=${() => linking(tab, now())} />
        <${Tokens} tokens=${tokens} onNew=${() => setModal({kind: 'new'})} onRevoke=${c => setModal({kind: 'revoke', cred: c})} />
        <${Sessions} sessions=${sessions} tokens=${tokens} onEnd=${c => setModal({kind: 'end', cred: c})} onEndOthers=${() => setModal({kind: 'others'})} />
      </div>
    </div>
    ${dialog}
  </div>`;
}
