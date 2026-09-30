// me is the viewer's own page: who they are, how the page looks in this browser, how they hear that something needs
// them (browser notices while the page is open, pushes to this device and what it takes of them, their other devices,
// a personal webhook), the sign-in accounts linked to them, their personal tokens and their browser sessions. On a
// phone it keeps only the account, the ways into the team and agent pages (which only show there), pushes to this
// device, the devices, putting tend on the home screen, the pages to open on a computer and signing out. Where the
// address is not HTTPS it says so, on either form.
import {useState, useEffect} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useSignalValue} from '../ui/base.js';
import {Panel} from '../ui/panel.js';
import {Modal} from '../ui/overlay.js';
import {Button, Segmented} from '../ui/controls.js';
import {TextInput} from '../ui/input.js';
import {Secret} from '../ui/secret.js';
import {DeskLinks} from '../ui/desk.js';
import {register, t as say, f as fill} from '../core/i18n.js';
import {linkURL} from '../core/http.js';
import {themes, densities} from '../core/prefs.js';
import {clock, day, browserOf} from '../core/format.js';
import {nowhere} from '../core/platform.js';
import {noPush} from '../core/push.js';
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
  'me.push': ['推送到这台设备', 'Pushes to this device'],
  'me.push.note': ['有事等你、在网页上一会儿没人处理时推到这里，网页关着也收得到', 'When something needs you and the page leaves it a while, it is pushed here, even with the page closed'],
  'me.push.denied': ['浏览器拒绝了通知：在浏览器的网站设置里允许 tend 再打开', 'The browser refuses notices: allow tend in its site settings, then turn pushes on'],
  'me.push.none': ['这个浏览器收不到推送', 'This browser receives no pushes'],
  'me.push.install': ['装到主屏幕、从主屏幕打开 tend 以后才能打开推送', 'Pushes can be turned on once tend is on the home screen and opened from there'],
  'me.push.insecure': ['这个地址不是 HTTPS，收不到推送', 'This address is not HTTPS, so no pushes come'],
  'me.devNotices': ['这台设备的通知 · %s', 'Notices on this device · %s'],
  'me.dev.waiting': ['等你的事', 'What waits on you'], 'me.dev.waitingNote': ['权限、提问、待验收、失败', 'Permissions, questions, runs to accept, failures'],
  'me.dev.done': ['任务完成', 'Tasks done'], 'me.dev.doneNote': ['你负责、派发或验收的', 'Yours, dispatched by you or accepted by you'],
  'me.dev.hide': ['锁屏上隐藏内容', 'Hide the content'], 'me.dev.hideNote': ['只显示「tend：N 项等你」，不带按钮', 'Only "N waiting on you" shows, without buttons'],
  'me.dev.wait': ['升级前的等待', 'Before a push'], 'me.dev.waitNote': ['网页上一直没人处理多久才推到这里', 'How long something waits unhandled on the page before it is pushed here'],
  'me.dev.wait.0': ['默认', 'Default'], 'me.dev.wait.-1': ['立即', 'At once'], 'me.dev.wait.300': ['5 分钟', '5 min'], 'me.dev.wait.900': ['15 分钟', '15 min'],
  'me.dev.waitDefault': ['默认：权限请求 30 秒，其余 1 分钟', 'Default: 30 s for a permission, a minute for the rest'],
  'me.devices': ['收推送的设备', 'Devices that receive pushes'], 'me.devicesNote': ['去掉的设备下次打开 tend 时会重新登记', 'A device taken away registers again the next time it opens tend'],
  'me.dev.this': ['这台设备', 'This device'], 'me.dev.unnamed': ['没有名字的设备', 'A device without a name'],
  'me.dev.renewed': ['%s 续期', 'renewed %s'], 'me.dev.lastOK': ['%s 送达过', 'reached %s'], 'me.dev.failures': ['失败 %d 次', '%d failures'],
  'me.dev.remove': ['去掉', 'Remove'], 'me.dev.removed': ['%s 已去掉', '%s is removed'],
  'me.desk': ['设置和管理 · 在电脑上打开', 'Settings and management · on a computer'],
  'me.desk.agents': ['Agent 定义与 workflow', 'Agent definitions and workflows'], 'me.desk.team': ['项目、成员与邀请', 'Projects, people and invitations'],
  'me.desk.trackers': ['工单绑定与同步', 'Issue trackers and their sync'], 'me.desk.machines': ['机器分享与交接', 'Sharing and handing machines over'],
  'me.desk.tokens': ['token、浏览器会话与 webhook', 'Tokens, browser sessions and the webhook'], 'me.desk.look': ['外观与语言', 'Look and language'],
  'me.webhook': ['个人 webhook', 'Personal webhook'], 'me.webhookSave': ['保存', 'Save'],
  'me.webhookNote': ['有事等你或任务结束时 POST 一段带 text 的 JSON；ntfy、Slack、企业微信都能收。留空就不发。', 'When something needs you or a task ends, it receives a JSON POST with a text field, which ntfy, Slack and the like take. Empty sends nothing.'],
  'me.webhookTest': ['发一条试试', 'Send a test'], 'me.webhookTestNote': ['发到已保存的地址', 'Sent to the saved address'],
  'me.webhookSent': ['试发的一条送到了', 'The test went through'], 'me.webhookRefused': ['webhook 回了 HTTP %s', 'The webhook answered HTTP %s'],
  'me.webhookUnreachable': ['连不上这个 webhook', 'The webhook could not be reached'],
  'me.webhookSaved': ['webhook 已保存', 'The webhook is saved'], 'me.webhookGone': ['webhook 已去掉', 'The webhook is removed'],
  'me.logins': ['登录方式', 'Sign-in accounts'], 'me.loginsNote': ['关联后任一种都能登录这个账号', 'Once linked, any of them signs in as you'],
  'me.link': ['关联', 'Link'], 'me.unlinked': ['没关联', 'not linked'], 'me.noLogins': ['这台 server 只用 token 登录', 'This server signs in with tokens only'],
  'me.linked': ['账号已关联', 'The account is linked'], 'me.linkTaken': ['这个账号已经属于另一个用户，没有关联', 'That account belongs to someone else; it was not linked'],
  'me.linkFailed': ['没有关联：登录没有完成（%s）', 'Not linked: the sign-in did not finish (%s)'],
  'me.lastLogin': ['上次登录 %s', 'last signed in %s'], 'me.linkedAt': ['%s 关联', 'linked %s'], 'me.unlink': ['解除关联', 'Unlink'],
  'me.unlinkTitle': ['解除和 %s 的关联？', 'Unlink %s?'],
  'me.unlinkNote': ['之后不能再用它登录这个账号，再用它登录会按准入规则当成另一个人；工单里指派给它的 issue 也不再归你。已经登录的浏览器会话不受影响。', 'It no longer signs you in, and signing in with it again goes by the admission rules as someone else; issues assigned to it are no longer yours. Browser sessions already signed in stay.'],
  'me.unlinkedDone': ['已解除和 %s 的关联', '%s is unlinked'],
  'me.tokens': ['token', 'Tokens'], 'me.tokensNote': ['给 CLI 和 TUI 用；值只在新建时显示一次', 'For the CLI and the TUI; the value shows only when made'],
  'me.noTokens': ['还没有 token：tend login 会建一个，或在这里新建', 'No token yet: tend login makes one, or make one here'],
  'me.newToken': ['新建 token', 'New token'], 'me.tokenName': ['名称', 'Name'], 'me.tokenNameNote': ['让你认得出它用在哪，最多 64 个字符', 'So you know where it is used; up to 64 characters'],
  'me.make': ['生成', 'Make it'], 'me.tokenValue': ['token（只显示这一次）', 'The token (shown only this once)'],
  'me.tokenHow': ['存进一个只有你能读的文件，在 tend 的 config.json 里把 coordinator.token_file 指过去；关掉后不再显示。', 'Save it to a file only you can read and point coordinator.token_file in tend\'s config.json at it; once closed it is not shown again.'],
  'me.done': ['完成', 'Done'], 'me.made': ['%s 已生成', '%s is made'],
  'me.madeAt': ['%s 建立', 'made %s'], 'me.usedAt': ['%s 用过', 'used %s'], 'me.usedFrom': ['%s 从 %s 用过', 'used %s from %s'], 'me.unused': ['没用过', 'never used'], 'me.until': ['%s 到期', 'ends %s'],
  'me.revoke': ['吊销', 'Revoke'], 'me.revokeTitle': ['吊销 %s？', 'Revoke %s?'],
  'me.revokeNote': ['用它的 CLI 和 TUI 立刻断开，用它登录的网页会话一起失效，它们的推送也停，不能恢复。', 'The CLI and TUI using it disconnect at once, and so do the browser sessions it signed in, whose pushes stop; this cannot be undone.'],
  'me.revoked': ['%s 已吊销', '%s is revoked'],
  'me.sessions': ['浏览器会话', 'Browser sessions'], 'me.sessionsNote': ['30 天有效', 'Each lasts 30 days'],
  'me.thisBrowser': ['这个浏览器', 'This browser'], 'me.signedIn': ['浏览器登录', 'Browser sign-in'], 'me.viaToken': ['用 token %s 登录', 'Signed in with the token %s'], 'me.viaDevice': ['%s，由另一台设备允许登录', '%s, signed in from another device'],
  'me.current': ['就是这个', 'This one'], 'me.end': ['退出', 'Sign out'], 'me.endOthers': ['退出其他全部', 'Sign out all others'],
  'me.endTitle': ['让这个会话退出？', 'Sign this session out?'], 'me.endNote': ['那个浏览器的推送一起停，下次打开 tend 时回到登录页。', 'That browser\'s pushes stop too, and it is back at the sign-in page the next time it opens tend.'],
  'me.ended': ['会话已退出', 'The session is signed out'],
  'me.endOthersTitle': ['退出其他 %d 个会话？', 'Sign out the %d other sessions?'], 'me.endOthersNote': ['只留下这个浏览器，其他浏览器的推送一起停。', 'Only this browser stays signed in; the others\' pushes stop too.'],
  'me.endedN': ['已退出 %d 个会话', '%d sessions signed out'],
  'me.team': ['团队', 'Team'], 'me.teamNote': ['成员和项目，在手机上只看', 'People and projects; a phone only shows them'],
  'me.agents': ['Agent', 'Agents'], 'me.agentsNote': ['定义和能跑的机器，在手机上只看', 'Definitions and where they run; a phone only shows them'],
  'me.install': ['装到主屏幕', 'The home screen'],
  'me.installIOS': ['在 Safari 里把 tend 装到主屏幕，才收得到推送：', 'Put tend on the home screen from Safari to receive pushes:'],
  'me.installIOS1': ['点「分享」', 'Tap Share'], 'me.installIOS2': ['选「添加到主屏幕」', 'Choose Add to Home Screen'],
  'me.installIOS3': ['从主屏幕打开 tend 再登录', 'Open tend from the home screen and sign in'],
  'me.installAndroid': ['在浏览器菜单里选「安装应用」或「添加到主屏幕」，tend 就像 App 一样打开', 'Choose Install app or Add to Home screen in the browser menu, and tend opens like an app'],
  'me.https': ['不是 HTTPS', 'Not HTTPS'],
  'me.insecure': ['这个地址不是 HTTPS，不能装到主屏幕，也收不到推送。有两种办法换成 HTTPS 地址：', 'This address is not HTTPS: tend cannot go on the home screen and receives no pushes. Two ways to an HTTPS address:'],
  'me.insecureTailnet': ['在 tailnet 里：tailscale serve 把 server 放到 *.ts.net 的 HTTPS 地址上，或用 tailscale cert 取证书', 'On a tailnet: tailscale serve puts the server on an HTTPS *.ts.net address, or tailscale cert gets a certificate'],
  'me.insecureTLS': ['公网部署：tend-server 加 --tls-cert 和 --tls-key 启动', 'Deployed in public: start tend-server with --tls-cert and --tls-key'],
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

// PushSet turns pushes to this device on and off, or says why they cannot be had here: an address that is not HTTPS,
// an iPhone's Safari before tend is on the home screen, a browser without them, notices refused.
function PushSet({push, platform, toasts, onState = () => {}}) {
  const w = useWords();
  const {t} = w;
  const [state, setStateNow] = useState(null);
  const setState = v => { setStateNow(v); onState(v); };
  useEffect(() => { push.state().then(setState, () => setState('none')); }, []);
  const shown = !platform.secure ? 'insecure' : state === 'none' && platform.os === 'ios' && platform.kind !== 'pwa' ? 'install' : state;
  if (!shown) return null;
  const turn = v => (v === 'on' ? push.on() : push.off()).then(setState, e => toasts.show({text: apiText(w, e), tone: 'danger'}));
  const usable = shown === 'on' || shown === 'off';
  return html`<div class="me-set"><span class="me-k">${t('me.push')}</span>
    <span class="me-inline">${usable && html`<${Segmented} label=${t('me.push')} value=${shown} onChange=${turn}
      options=${[{value: 'on', label: t('me.on')}, {value: 'off', label: t('me.off')}]} />`}
      <span class=${cx(usable ? 't-muted' : 't-warning')}>${t(usable ? 'me.push.note' : 'me.push.' + shown)}</span></span></div>`;
}

// ⚠️ The events a device takes (store.EventWaiting, store.EventDone) and the waits offered, in seconds (0 the
// server's default, -1 at once).
const waitingEvent = 'task.needs_you', doneEvent = 'task.done';
const waits = [0, -1, 300, 900];
const eventsOf = p => p?.events ?? [waitingEvent];

// Switch is one on/off setting.
function Switch({label, on, onChange}) {
  return html`<button type="button" role="switch" class=${cx('switch', on && 'on')} aria-checked=${on ? 'true' : 'false'} aria-label=${label}
    onClick=${() => onChange(!on)}></button>`;
}

// useDevices is the viewer's push devices and this browser's among them: read once, and again when push is turned
// on or off here (state) and after a session or token was ended (ended counts them: its devices went with it); a
// change of settings is shown at once and put back if the server refuses it.
function useDevices({http, push, toasts, state, ended = 0}) {
  const w = useWords();
  const [list, setList] = useState(null);
  const [mine, setMine] = useState('');
  useEffect(() => {
    if (!http?.devices || state === null) return;
    let live = true;
    Promise.all([http.devices(), push.device()]).then(([ds, id]) => { if (live) { setList(ds || []); setMine(id); } }, () => live && setList([]));
    return () => { live = false; };
  }, [state, ended]);
  const failed = e => toasts.show({text: apiText(w, e), tone: 'danger'});
  const setPrefs = (d, prefs) => {
    const before = list;
    setList(list.map(x => (x.id === d.id ? {...x, prefs} : x)));
    http.setPrefs(d.id, prefs).catch(e => { setList(before); failed(e); });
  };
  const remove = d => http.removeDevice(d.id).then(() => {
    setList(l => l.filter(x => x.id !== d.id));
    toasts.show({text: w.f('me.dev.removed', d.name || w.t('me.dev.unnamed'))});
  }, failed);
  return {list, mine: list?.find(d => d.id === mine) || null, setPrefs, remove};
}

// DeviceSets is what this device takes of the pushes: what waits, tasks done, the content hidden, how long before.
function DeviceSets({device, onPrefs}) {
  const {t} = useWords();
  const p = device.prefs || {};
  const events = eventsOf(p);
  const turn = (event, on) => onPrefs({...p, events: [waitingEvent, doneEvent].filter(e => (e === event ? on : events.includes(e)))});
  const row = (key, on, onChange) => html`<div class="me-set"><span class="me-k">${t('me.dev.' + key)}</span>
    <span class="me-inline"><${Switch} label=${t('me.dev.' + key)} on=${on} onChange=${onChange} /><span class="t-muted">${t('me.dev.' + key + 'Note')}</span></span></div>`;
  return html`
    ${row('waiting', events.includes(waitingEvent), v => turn(waitingEvent, v))}
    ${row('done', events.includes(doneEvent), v => turn(doneEvent, v))}
    ${row('hide', !!p.hide, v => onPrefs({...p, events, hide: v}))}
    <div class="me-set me-set-stack"><span class="me-k">${t('me.dev.wait')}</span>
      <span class="me-inline"><${Segmented} label=${t('me.dev.wait')} value=${p.wait || 0} onChange=${v => onPrefs({...p, events, wait: v})}
        options=${waits.map(v => ({value: v, label: t('me.dev.wait.' + v)}))} />
        <span class="t-muted">${(p.wait || 0) === 0 ? t('me.dev.waitDefault') : t('me.dev.waitNote')}</span></span></div>`;
}

// Devices lists the viewer's push devices, this one marked; another is taken away here (it registers again when it
// next opens tend), this one by turning pushes off.
function Devices({devices, mine, onRemove}) {
  const w = useWords();
  const {t, f} = w;
  const facts = d => [d.renewed && f('me.dev.renewed', when(d.renewed)), d.last_ok && f('me.dev.lastOK', when(d.last_ok)),
    d.failures > 0 && f('me.dev.failures', d.failures)].filter(Boolean).join(' · ');
  return html`<${Panel} title=${t('me.devices')} count=${devices.length}>
    <p class="t-muted me-note">${t('me.devicesNote')}</p>
    <ul class="me-rows">${devices.map(d => html`<li class="me-row" key=${d.id}>
      <span class=${cx('st', d.id === mine?.id ? 's-success' : 's-muted')} aria-hidden="true">${d.id === mine?.id ? '●' : '○'}</span>
      <span class="me-main"><span>${d.name || t('me.dev.unnamed')}</span><span class="t-muted">${facts(d)}</span></span>
      ${d.id === mine?.id ? html`<span class="me-side me-current">${t('me.dev.this')}</span>`
        : html`<${Button} kind="quiet danger" onClick=${() => onRemove(d)}>${t('me.dev.remove')}<//>`}</li>`)}</ul>
  <//>`;
}

function Notices({prefs, notices, webhook, onWebhook, toasts, push, platform, http, ended}) {
  const w = useWords();
  const {t, f} = w;
  const on = useSignalValue(prefs.notify);
  const [permission, setPermission] = useState(() => permissionOf(notices));
  const [url, setUrl] = useState(webhook ?? '');
  const [testing, setTesting] = useState(false);
  useEffect(() => { if (webhook !== null) setUrl(webhook); }, [webhook]);
  const test = () => {
    setTesting(true);
    http.testWebhook().then(v => toasts.show(v.ok ? {text: t('me.webhookSent')}
      : {text: v.status === 'unreachable' ? t('me.webhookUnreachable') : f('me.webhookRefused', v.status), tone: 'danger'}),
    e => toasts.show({text: apiText(w, e), tone: 'danger'})).finally(() => setTesting(false));
  };
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
  const [pushed, setPushed] = useState(null);
  const devs = useDevices({http, push, toasts, state: pushed, ended});
  return html`<${Panel} title=${t('me.notices')} actions=${html`<span class="t-muted">${t('me.noticesNote')}</span>`}>
    <div class="me-sets">
      <div class="me-set"><span class="me-k">${t('me.browser')}</span>
        <span class="me-inline">${usable && html`<${Segmented} label=${t('me.browser')} value=${shown} onChange=${turn}
          options=${[{value: 'on', label: t('me.on')}, {value: 'off', label: t('me.off')}]} />`}
          <span class=${cx(usable ? 't-muted' : 't-warning')}>${t('me.browser.' + permission)}</span></span></div>
      <${PushSet} push=${push} platform=${platform} toasts=${toasts} onState=${setPushed} />
      ${pushed === 'on' && devs.mine && html`<${DeviceSets} device=${devs.mine} onPrefs=${p => devs.setPrefs(devs.mine, p)} />`}
      <div class="me-set me-set-top"><span class="me-k">${t('me.webhook')}</span>
        <span class="me-hook"><span class="me-hook-row"><${TextInput} label=${t('me.webhook')} value=${url} onInput=${setUrl} mono placeholder="https://ntfy.sh/…" />
          <${Button} disabled=${webhook === null || url.trim() === (webhook || '')} onClick=${() => onWebhook(url.trim())}>${t('me.webhookSave')}<//>
          <${Button} kind="quiet" disabled=${!webhook || testing || url.trim() !== webhook} title=${t('me.webhookTestNote')} onClick=${test}>${t('me.webhookTest')}<//></span>
          <span class="field-note">${t('me.webhookNote')}</span></span></div>
    </div>
  <//>
  ${devs.list?.length > 0 && html`<${Devices} devices=${devs.list} mine=${devs.mine} onRemove=${devs.remove} />`}`;
}

// PhoneNotices is the phone's: pushes to this device and what it takes of them, and the devices.
function PhoneNotices({http, push, platform, toasts}) {
  const {f} = useWords();
  const [pushed, setPushed] = useState(null);
  const devs = useDevices({http, push, toasts, state: pushed});
  return html`<${Panel} title=${f('me.devNotices', platform.name)}><div class="me-sets">
      <${PushSet} push=${push} platform=${platform} toasts=${toasts} onState=${setPushed} />
      ${pushed === 'on' && devs.mine && html`<${DeviceSets} device=${devs.mine} onPrefs=${p => devs.setPrefs(devs.mine, p)} />`}
    </div><//>
    ${devs.list?.length > 0 && html`<${Devices} devices=${devs.list} mine=${devs.mine} onRemove=${devs.remove} />`}`;
}

function Logins({logins, identities, onLink, onUnlink}) {
  const {t, f} = useWords();
  const display = p => logins.find(l => l.name === p)?.display || p;
  const loose = logins.filter(l => !identities.some(i => i.provider === l.name));
  return html`<${Panel} title=${t('me.logins')} actions=${html`<span class="t-muted">${t('me.loginsNote')}</span>`}>
    <ul class="me-rows">
      ${identities.map(i => html`<li class="me-row" key=${i.provider + i.subject}><span class="st s-success" aria-hidden="true">✓</span>
        <span class="me-main"><b>${display(i.provider)}</b><span class="mono t-muted">${i.username || i.email || i.subject}</span></span>
        <span class="t-muted me-side">${[i.username && i.email, i.last_login ? f('me.lastLogin', when(i.last_login)) : i.linked && f('me.linkedAt', when(i.linked))]
          .filter(Boolean).join(' · ')}</span>
        ${identities.length > 1 && html`<${Button} kind="quiet danger" onClick=${() => onUnlink(i)}>${t('me.unlink')}<//>`}</li>`)}
      ${loose.map(l => html`<li class="me-row" key=${l.name}><span class="st s-muted" aria-hidden="true">·</span>
        <span class="me-main"><b>${l.display || l.name}</b><span class="t-muted">${t('me.unlinked')}</span></span>
        <a class="btn me-link" href=${linkURL(l.name)} onClick=${onLink}>${t('me.link')}</a></li>`)}
      ${!identities.length && !logins.length && html`<li class="me-row t-muted">${t('me.noLogins')}</li>`}
    </ul>
  <//>`;
}

// facts are what the page knows of a credential's use: the browser it signed in with (a session), when it was made,
// when and from where it was last used, when it ends.
const facts = (w, c) => [browserOf(c.agent), w.f('me.madeAt', when(c.created)),
  !c.last_used ? w.t('me.unused') : c.last_ip ? w.f('me.usedFrom', when(c.last_used), c.last_ip) : w.f('me.usedAt', when(c.last_used)),
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
    if (c.name?.startsWith('device:')) return f('me.viaDevice', c.name.slice(7));
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

// Install tells this device what it lacks: on an address that is not HTTPS, the two ways to one; on a phone's browser,
// how to put tend on the home screen (Safari's three steps, the Android browser's menu). An installed page, or a
// computer on HTTPS, lacks nothing.
function Install({platform, phone}) {
  const {t} = useWords();
  if (!platform.secure) {
    return html`<${Panel} title=${t('me.https')}>
      <p class="me-note">${t('me.insecure')}</p>
      <ul class="me-steps"><li>${t('me.insecureTailnet')}</li><li>${t('me.insecureTLS')}</li></ul>
    <//>`;
  }
  if (!phone || platform.kind === 'pwa') return null;
  if (platform.os === 'ios') {
    return html`<${Panel} title=${t('me.install')}>
      <p class="me-note">${t('me.installIOS')}</p>
      <ol class="me-steps"><li>${t('me.installIOS1')}</li><li>${t('me.installIOS2')}</li><li>${t('me.installIOS3')}</li></ol>
    <//>`;
  }
  if (platform.os === 'android') return html`<${Panel} title=${t('me.install')}><p class="me-note">${t('me.installAndroid')}</p><//>`;
  return null;
}

// Me: session is who is signed in; http reads and changes their tokens, sessions, accounts and webhook; prefs are this
// browser's; notices are the browser's notices ({Notification, secure}); tab is the tab's storage (linking an account
// goes through it); platform is this device's (core/platform.js), push its Web Push (core/push.js); clock is now;
// onLogout signs out; copy is the clipboard's.
export function Me({session, http, prefs, toasts, router, notices, tab, platform = nowhere, push = noPush, clock: now = () => Date.now(), copy, onLogout}) {
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
      <${PhoneNotices} http=${http} push=${push} platform=${platform} toasts=${toasts} />
      <${Install} platform=${platform} phone />
      <${Panel} title=${t('me.desk')}><${DeskLinks} platform=${platform} toasts=${toasts} items=${[['agents', 'agents'], ['team', 'team'], ['trackers', 'team'],
        ['machines', 'machines'], ['tokens', 'me'], ['look', 'me']].map(([k, page]) => ({label: t('me.desk.' + k), page}))} /><//>
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
  const [ended, setEnded] = useState(0);
  const signedOut = () => { readCreds(); setEnded(n => n + 1); };
  const revoke = (c, text) => http.revokeToken(c.id).then(() => { toasts.show({text}); signedOut(); }, failed);
  const endOthers = async () => {
    let n = 0;
    try {
      for (const c of sessions.filter(x => !x.current)) { await http.revokeToken(c.id); n++; }
      toasts.show({text: f('me.endedN', n)});
    } catch (e) { failed(e); }
    signedOut();
  };
  const saveHook = url => http.setWebhook(url).then(() => { setWebhook(url); toasts.show({text: t(url ? 'me.webhookSaved' : 'me.webhookGone')}); }, failed);
  const others = sessions.filter(c => !c.current).length;
  const loginName = i => [logins.find(l => l.name === i.provider)?.display || i.provider, i.username || i.email].filter(Boolean).join(' ');
  const unlink = i => http.unlinkIdentity(i).then(() => { toasts.show({text: f('me.unlinkedDone', loginName(i))}); http.identities().then(setIdentities, () => {}); }, failed);
  const dialog = modal && ({
    new: () => html`<${NewToken} busy=${busy} error=${modal.error} made=${modal.made} copy=${copy} onMake=${make} onClose=${close} />`,
    revoke: () => html`<${Confirm} title=${f('me.revokeTitle', modal.cred.name)} note=${t('me.revokeNote')} label=${t('me.revoke')}
      onGo=${() => revoke(modal.cred, f('me.revoked', modal.cred.name))} onClose=${close} />`,
    end: () => html`<${Confirm} title=${t('me.endTitle')} note=${t('me.endNote')} label=${t('me.end')} onGo=${() => revoke(modal.cred, t('me.ended'))} onClose=${close} />`,
    unlink: () => html`<${Confirm} title=${f('me.unlinkTitle', loginName(modal.id))} note=${t('me.unlinkNote')} label=${t('me.unlink')}
      onGo=${() => unlink(modal.id)} onClose=${close} />`,
    others: () => html`<${Confirm} title=${f('me.endOthersTitle', others)} note=${t('me.endOthersNote')} label=${t('me.endOthers')} onGo=${endOthers} onClose=${close} />`,
  })[modal.kind]?.();

  return html`<div class="me">
    <div class="me-head"><${Avatar} name=${session?.name} />
      <span class="me-who"><h1 class="tasks-title">${session?.name}</h1><span class="mono t-muted">${sub}</span></span>
      <span class="me-head-acts"><${Button} onClick=${onLogout}>${t('app.logout')}<//></span></div>
    <${Install} platform=${platform} />
    <div class="me-body">
      <div class="me-col">
        <${Look} prefs=${prefs} presets=${presets} />
        <${Notices} prefs=${prefs} notices=${notices} webhook=${webhook} onWebhook=${saveHook} toasts=${toasts} push=${push} platform=${platform} http=${http} ended=${ended} />
      </div>
      <div class="me-col">
        <${Logins} logins=${logins} identities=${identities} onLink=${() => linking(tab, now())} onUnlink=${i => setModal({kind: 'unlink', id: i})} />
        <${Tokens} tokens=${tokens} onNew=${() => setModal({kind: 'new'})} onRevoke=${c => setModal({kind: 'revoke', cred: c})} />
        <${Sessions} sessions=${sessions} tokens=${tokens} onEnd=${c => setModal({kind: 'end', cred: c})} onEndOthers=${() => setModal({kind: 'others'})} />
      </div>
    </div>
    ${dialog}
  </div>`;
}
