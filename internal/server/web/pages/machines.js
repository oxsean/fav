// machines is the machines page: every machine the viewer sees, grouped as theirs, shared with them and everyone
// else's, each with its state, room, agent CLIs and what is wrong with it; the picked one's day, what waits for it,
// who else may use it and its node token. Its owner or an admin changes who else may use it and ends or moves its
// token; anyone adds a machine of their own and gets its token once. On a phone the page only shows: a list of the
// machines, each opening its facts.
import {useState, useEffect} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useSignalValue, useName, useNames} from '../ui/base.js';
import {Panel} from '../ui/panel.js';
import {Modal, Drawer} from '../ui/overlay.js';
import {Button, Segmented} from '../ui/controls.js';
import {TextInput} from '../ui/input.js';
import {Picker} from '../ui/picker.js';
import {Status} from '../ui/status.js';
import {Timeline} from '../ui/charts.js';
import {Secret} from '../ui/secret.js';
import {useListKeys} from '../ui/table.js';
import {register} from '../core/i18n.js';
import {clock, day, duration} from '../core/format.js';
import * as tm from '../core/team.js';
import {apiText, why} from './words.js';
import {showMachine} from './runs.js';
import './taskwords.js';
import {OnDesktop} from '../ui/desk.js';
import {nowhere} from '../core/platform.js';

register('machines', {
  'mach.title': ['机器', 'Machines'], 'mach.none': ['还没有机器接入', 'No machine has connected yet'],
  'mach.summary': ['%d 台 · %d 台在线 · 运行位 %d/%d 在用 · %d 个排队', '%d {machine|machines} · %d up · %d/%d {slot|slots} in use · %d queued'],
  'mach.mine': ['我的机器', 'My machines'], 'mach.toMe': ['分享给我的', 'Shared with me'], 'mach.others': ['其他人的', 'Other people\'s'],
  'mach.state.connected': ['在线', 'online'], 'mach.state.connecting': ['连接中', 'connecting'], 'mach.state.offline': ['离线', 'offline'],
  'mach.offlineSince': ['离线 · %s 起', 'offline since %s'],
  'mach.state.idle': ['空闲断开', 'idle'], 'mach.retired': ['已退役', 'retired'],
  'mach.via.local': ['本机', 'this machine'], 'mach.via.ssh': ['ssh', 'ssh'], 'mach.via.dial': ['连入服务器', 'dialed in'],
  'mach.slots': ['运行位', 'Slots'], 'mach.slotsOf': ['%d / %d', '%d / %d'], 'mach.queuedN': ['排队 %d', '%d queued'],
  'mach.note.retired': ['主人已停用：这台机器不再运行任何东西', 'Its owner is disabled: nothing runs here again'],
  'mach.note.error': ['连不上：%s', 'Cannot be reached: %s'], 'mach.note.auth': ['%s 没登录：派给它的运行会被拒绝', '%s not signed in: runs sent to it are refused'],
  'mach.note.missing': ['它的 tend 缺 %s，要用这些的运行不会派到这里；在协调器上运行 tend hosts install 更新', 'Its tend lacks %s, so runs needing it do not go here; update it with tend hosts install'],
  'mach.today': ['%s 今天', '%s today'], 'mach.todayNote': ['每行一个运行位', 'A row per slot'],
  'mach.owner': ['主人', 'Owner'], 'mach.link': ['连接', 'Connection'], 'mach.host': ['主机名', 'Hostname'], 'mach.os': ['系统', 'System'],
  'mach.version': ['tend', 'tend'], 'mach.queue': ['排队', 'Queued'], 'mach.noQueue': ['没有', 'None'],
  'mach.retryAt': ['%s，%s 重试', '%s; retries at %s'],
  'mach.clis': ['Agent CLI', 'Agent CLIs'], 'mach.noClis': ['还没有检查过', 'Not checked yet'],
  'mach.check': ['检查', 'Check'], 'mach.checkAll': ['全部检查', 'Check all'], 'mach.checkedAgo': ['%s 前检查', 'checked %s ago'],
  'mach.checked': ['%s 已检查', '%s is checked'], 'mach.checkedN': ['检查了 %d 台', '%d {machine|machines} checked'],
  'mach.checkFailed': ['%s 检查不了：%s', '%s cannot be checked: %s'],
  'mach.checkWhy.unsupported': ['它的 tend 太旧，没有这个检查', 'its tend is too old for this check'],
  'mach.checkWhy.offline': ['它不在线', 'it is offline'], 'mach.checkWhy.timeout': ['节点没有及时回答', 'its node did not answer in time'],
  'mach.drain': ['停止接新运行', 'Stop new runs'], 'mach.undrain': ['恢复接新运行', 'Take new runs again'],
  'mach.drained': ['%s 停止接新运行：在跑的会跑完', '%s takes no new runs: those running finish'], 'mach.undrained': ['%s 恢复接新运行', '%s takes new runs again'],
  'mach.note.drain': ['停止接新运行 · %s %s 起', 'Takes no new runs · %s since %s'],
  'mach.redials': ['节点断线后自己重连，最多隔 60 秒', 'its node dials in again by itself, within a minute'],
  'mach.cli.ok': ['已装 · 已登录', 'installed · signed in'], 'mach.cli.auth': ['已装 · 没登录', 'installed · not signed in'],
  'mach.cli.unknown': ['已装', 'installed'], 'mach.cli.missing': ['没装', 'not installed'],
  'mach.shared': ['分享给', 'Shared with'], 'mach.private': ['只有主人和管理员能用', 'Only its owner and admins use it'],
  'mach.canRun': ['可以派发', 'may dispatch'], 'mach.canApprove': ['可以派发 · 可以批准权限请求', 'may dispatch and grant permission requests'],
  'mach.inProject': ['项目 %s', 'project %s'],
  'mach.share': ['改分享', 'Change sharing'], 'mach.shareTitle': ['谁还能用 %s', 'Who else may use %s'], 'mach.users': ['人', 'People'],
  'mach.projects': ['项目（项目里的参与者）', 'Projects (their participants)'], 'mach.approveField': ['权限请求', 'Permission requests'],
  'mach.approveNo': ['只能派发', 'Dispatch only'], 'mach.approveYes': ['也能批准', 'May grant them too'],
  'mach.approveHelp': ['只能派发时，执行命令、写文件这类请求只有主人和运行的派发人能批；提问类的，项目参与者都能答。', 'With dispatch only, commands and file writes are granted by the owner and the run\'s dispatcher alone; any project participant answers questions.'],
  'mach.trust': ['这台机器跑在主人的账号下：派来的 agent 能读主人 home 下的文件，用主人的 claude / codex 额度，提交的 committer 是主人（作者记为派发人）。要长期共享，给它单独开一个 OS 用户或容器，登录团队自己的 CLI 账号。',
    'This machine runs under its owner\'s account: agents sent here read files in the owner\'s home, spend the owner\'s claude / codex quota and commit as the owner (the dispatcher is the author). To share it for long, give it its own OS user or container signed in to the team\'s CLI accounts.'],
  'mach.sharedDone': ['%s 的分享已保存', 'Who may use %s is saved'],
  'mach.runs': ['看这台的运行', 'Its runs'],
  'mach.add': ['添加机器', 'Add a machine'], 'mach.name': ['机器名', 'Machine name'],
  'mach.nameNote': ['字母、数字、- _ .；节点用这个名字连上来。', 'Letters, digits, - _ .; the node connects under this name.'],
  'mach.addGo': ['生成 token', 'Make its token'], 'mach.added': ['%s 已添加', '%s is added'],
  'mach.token': ['节点 token（只显示这一次）', 'Its node token (shown only this once)'],
  'mach.tokenHow': ['在那台机器上把 token 存进 ~/.config/tend/node-token（只让自己可读），再运行下面的命令。它连上以后就出现在这里，只有你能往它派发，直到你分享它。',
    'On that machine, save the token to ~/.config/tend/node-token (readable by you alone), then run the command below. Once it connects it shows here; only you dispatch to it until you share it.'],
  'mach.command': ['在那台机器上运行', 'Run on that machine'], 'mach.doneAdd': ['完成', 'Done'],
  'mach.cred': ['节点 token', 'Node token'], 'mach.credBound': ['绑定 %s', 'bound to %s'], 'mach.credUnbound': ['还没有机器用它连上', 'no machine has connected with it'],
  'mach.credMade': ['%s 创建', 'made %s'], 'mach.credUsed': ['%s 用过', 'used %s'],
  'mach.rebind': ['换机', 'Move to a new machine'], 'mach.revoke': ['吊销', 'Revoke'],
  'mach.rebindTitle': ['把 %s 换到另一台机器？', 'Move %s to another machine?'],
  'mach.rebindNote': ['下次用这个 token 连上的机器成为 %s。现在这台下次重连会被拒绝。', 'The next machine to connect with this token becomes %s. The one now connected is refused when it reconnects.'],
  'mach.rebound': ['%s 等新机器连上', '%s waits for its new machine'],
  'mach.revokeTitle': ['吊销 %s 的 token？', 'Revoke the token of %s?'],
  'mach.revokeNote': ['用它的连接立刻断开，不能恢复；要再接入就重新添加。', 'Connections using it close at once, for good; add the machine again to bring it back.'],
  'mach.revoked': ['%s 的 token 已吊销', 'The token of %s is revoked'],
  'mach.otherCreds': ['还没接入的机器', 'Machines not connected yet'],
  'mach.desktop': ['添加机器、分享和 token 在电脑上管理', 'Adding machines, sharing and tokens are managed on a computer'],
});

// ⚠️ Where the page tells a machine's owner to keep its node token (the command the server gives reads it there).
// stateWord says how m stands; an offline one since when this coordinator last had it (today's by the clock, earlier
// ones with the day).
const stateWord = (w, m, now) => (m.retired ? w.t('mach.retired')
  : m.state === 'offline' && m.last_seen ? w.f('mach.offlineSince', (day(m.last_seen) === day(now) ? '' : day(m.last_seen) + ' ') + clock(m.last_seen))
  : w.has('mach.state.' + m.state) ? w.t('mach.state.' + m.state) : m.state);
const viaWord = (w, m) => (m.via ? (w.has('mach.via.' + m.via) ? w.t('mach.via.' + m.via) : m.via) : '');

function noteText(w, n, name) {
  if (n.kind === 'retired') return w.t('mach.note.retired');
  if (n.kind === 'drain') return w.f('mach.note.drain', n.by ? name(n.by) : '—', n.at ? clock(n.at) : '');
  if (n.kind === 'error') return w.f('mach.note.error', n.detail || n.error);
  if (n.kind === 'auth') return w.f('mach.note.auth', n.agents.join(', '));
  return w.f('mach.note.missing', n.features.join(', '));
}
// ⚠️ Why machine.check could not check a machine (wire codes), as the page words them; others read as any failure.
const checkWhys = ['unsupported', 'offline', 'timeout'];
const checkWhy = (w, code) => (checkWhys.includes(code) ? w.t('mach.checkWhy.' + code) : apiText(w, {code}));

const noteTone = n => (n.kind === 'error' || n.kind === 'retired' ? 'failed' : 'warning');

function SlotBar({m}) {
  const n = Math.max(1, m.slots || 0);
  return html`<span class="mach-bar" aria-hidden="true">${Array.from({length: n}, (_, i) => html`<span class=${cx(i < (m.active || 0) && 'used')}></span>`)}</span>`;
}

function Card({m, drain, picked, now, onPick}) {
  const w = useWords();
  const {t, f} = w;
  const name = useName();
  const clis = tm.agentChecks(m).filter(c => c.state !== 'missing').map(c => c.name);
  const notes = tm.machineNotes(m, drain);
  return html`<button type="button" class=${cx('mach-card', picked && 'sel', m.retired && 'retired')} aria-pressed=${picked ? 'true' : 'false'} onClick=${onPick}>
    <span class="mach-card-head"><${Status} state=${tm.machineState(m)} /><b class="mono ell">${m.name}</b><span class="t-muted">${stateWord(w, m, now)}</span>
      ${m.via && html`<span class="chip">${viaWord(w, m)}</span>`}</span>
    <span class="mach-card-slots"><span class="t-muted">${t('mach.slots')}</span><span class="mono">${f('mach.slotsOf', m.active || 0, m.slots || 0)}${m.queued ? ' · ' + f('mach.queuedN', m.queued) : ''}</span><${SlotBar} m=${m} /></span>
    <span class="mach-card-meta mono t-muted">${[m.os, m.version && 'tend ' + m.version, clis.join(' ')].filter(Boolean).join(' · ')}</span>
    ${notes.slice(0, 1).map(n => html`<span class=${cx('mach-card-note', 't-' + noteTone(n))}>${noteText(w, n, name)}</span>`)}
  </button>`;
}

// Facts is one machine: how it is reached, its room and queue, its agent CLIs and when they were checked, who else may
// use it and its token. The buttons are there only where their handlers are given.
function Facts({m, st, creds, now, checking, onCheck, draining, onDrain, onShare, onRebind, onRevoke, onRuns}) {
  const w = useWords();
  const {t, f} = w;
  const name = useName();
  const share = st.shares?.[m.name];
  const queue = tm.queueOn(st, m.name);
  const down = m.state !== 'connected';
  const link = [viaWord(w, m), down && m.error ? (m.retry_at ? f('mach.retryAt', m.detail || m.error, clock(m.retry_at)) : m.detail || m.error) : '',
    down && m.via === 'dial' && !m.retired ? t('mach.redials') : ''].filter(Boolean).join(' · ');
  const who = [...(share?.users || []).map(u => ({who: name(u), what: share.approve ? t('mach.canApprove') : t('mach.canRun')})),
    ...(share?.projects || []).map(p => ({who: f('mach.inProject', st.projects?.[p]?.name || p), what: share.approve ? t('mach.canApprove') : t('mach.canRun')}))];
  return html`<div class="mach-facts">
    <dl class="facts">
      <dt>${t('mach.owner')}</dt><dd>${m.owner ? name(m.owner) : '—'}</dd>
      <dt>${t('mach.link')}</dt><dd>${link || '—'}</dd>
      ${m.hostname && html`<dt>${t('mach.host')}</dt><dd class="mono">${m.hostname}</dd>`}
      ${m.os && html`<dt>${t('mach.os')}</dt><dd class="mono">${m.os}</dd>`}
      ${m.version && html`<dt>${t('mach.version')}</dt><dd class="mono">${m.version}</dd>`}
      <dt>${t('mach.slots')}</dt><dd class="mono">${f('mach.slotsOf', m.active || 0, m.slots || 0)}</dd>
      <dt>${t('mach.queue')}</dt><dd>${queue.length ? html`<ul class="mach-queue">${queue.map(x => html`<li key=${x.run.id}><span class="ell">${x.task?.title || x.run.task}</span>
        <span class="t-muted">${x.why ? why(w, x.why) : duration(now - Date.parse(x.run.queued_at))}</span></li>`)}</ul>` : t('mach.noQueue')}</dd>
    </dl>
    ${tm.machineNotes(m, st.drains?.[m.name]).map(n => html`<p class=${cx('mach-note', 't-' + noteTone(n))}>${noteText(w, n, name)}</p>`)}
    <section class="det-sec"><h3 class="det-h mach-h"><span>${t('mach.clis')}${m.checked_at && html`<span class="t-muted mach-when"> · ${f('mach.checkedAgo', duration(now - Date.parse(m.checked_at)))}</span>`}</span>
      ${onCheck && html`<${Button} kind="quiet" disabled=${checking} onClick=${onCheck}>${t('mach.check')}<//>`}</h3>
      ${tm.agentChecks(m).length ? html`<ul class="mach-clis">${tm.agentChecks(m).map(c => html`<li key=${c.name}>
        <span class=${cx('st', 's-' + ({ok: 'success', auth: 'unknown', unknown: 'muted', missing: 'muted'})[c.state])} aria-hidden="true">${({ok: '✓', auth: '!', unknown: '·', missing: '·'})[c.state]}</span>
        <span class="mono">${c.name}</span><span class="t-muted">${t('mach.cli.' + c.state)}</span><span class="mono t-muted">${c.version}</span></li>`)}</ul>`
        : html`<p class="t-muted mach-p">${t('mach.noClis')}</p>`}
    </section>
    <section class="det-sec"><h3 class="det-h mach-h">${t('mach.shared')}${onShare && html`<${Button} kind="quiet" onClick=${onShare}>${t('mach.share')}<//>`}</h3>
      ${who.length ? html`<ul class="mach-who">${who.map(x => html`<li><span>${x.who}</span><span class="t-muted">${x.what}</span></li>`)}</ul>`
        : html`<p class="t-muted mach-p">${t('mach.private')}</p>`}
    </section>
    ${creds.length > 0 && html`<section class="det-sec"><h3 class="det-h">${t('mach.cred')}</h3>
      ${creds.map(c => html`<${Cred} key=${c.id} c=${c} onRebind=${onRebind} onRevoke=${onRevoke} />`)}
    </section>`}
    ${(onRuns || onDrain) && html`<div class="det-acts">
      ${onDrain && html`<${Button} disabled=${draining} onClick=${onDrain}>${st.drains?.[m.name] ? t('mach.undrain') : t('mach.drain')}<//>`}
      ${onRuns && html`<${Button} keyName="Enter" onClick=${onRuns}>${t('mach.runs')}<//>`}</div>`}
  </div>`;
}

function Cred({c, onRebind, onRevoke}) {
  const {t, f} = useWords();
  const when = at => (at ? day(at) + ' ' + clock(at) : '');
  return html`<div class="mach-cred">
    <span class="mach-cred-main"><span class="mono">${c.name}</span>
      <span class="t-muted">${[c.host ? f('mach.credBound', c.host) : t('mach.credUnbound'), c.created && f('mach.credMade', when(c.created)), c.last_used && f('mach.credUsed', when(c.last_used))].filter(Boolean).join(' · ')}</span></span>
    ${onRebind && html`<${Button} kind="quiet" onClick=${() => onRebind(c)}>${t('mach.rebind')}<//>`}
    ${onRevoke && html`<${Button} kind="quiet danger" onClick=${() => onRevoke(c)}>${t('mach.revoke')}<//>`}
  </div>`;
}

function Share({machine, share, owner, people, projects, busy, onSave, onClose}) {
  const {t, f} = useWords();
  const [users, setUsers] = useState(share?.users || []);
  const [ps, setPs] = useState(share?.projects || []);
  const [approve, setApprove] = useState(share?.approve ? 'yes' : 'no');
  return html`<${Modal} title=${f('mach.shareTitle', machine)} onClose=${onClose}
    actions=${[{label: t('home.cancel'), onClick: onClose}, {label: t('form.save'), kind: 'primary', keyName: 'Mod+Enter', disabled: busy,
      onClick: () => onSave({machine, users, projects: ps, approve: approve === 'yes'})}]}>
    <p class="box mach-trust">${t('mach.trust')}</p>
    <${Picker} label=${t('mach.users')} multi value=${users} onChange=${setUsers} options=${people.filter(o => o.value !== owner)} />
    <${Picker} label=${t('mach.projects')} multi value=${ps} onChange=${setPs} options=${projects} />
    <div class="field"><span class="brief-label">${t('mach.approveField')}</span>
      <${Segmented} label=${t('mach.approveField')} value=${approve} onChange=${setApprove}
        options=${[{value: 'no', label: t('mach.approveNo')}, {value: 'yes', label: t('mach.approveYes')}]} />
      <span class="field-note">${t('mach.approveHelp')}</span></div>
  <//>`;
}

function Add({busy, error, onAdd, onClose}) {
  const {t} = useWords();
  const [name, setName] = useState('');
  const ok = tm.machineName.test(name.trim()) && !busy;
  return html`<${Modal} title=${t('mach.add')} onClose=${onClose}
    actions=${[{label: t('home.cancel'), onClick: onClose}, {label: t('mach.addGo'), kind: 'primary', keyName: 'Mod+Enter', disabled: !ok, onClick: () => onAdd(name.trim())}]}>
    <${TextInput} label=${t('mach.name')} value=${name} onInput=${setName} mono autoFocus note=${t('mach.nameNote')} error=${error} />
  <//>`;
}

function Added({name, token, command, copy, onClose}) {
  const {t, f} = useWords();
  return html`<${Modal} title=${f('mach.added', name)} onClose=${onClose} actions=${[{label: t('mach.doneAdd'), kind: 'primary', keyName: 'Mod+Enter', onClick: onClose}]}>
    <${Secret} label=${t('mach.token')} value=${token} copy=${copy} />
    <p class="mach-p">${t('mach.tokenHow')}</p>
    <${Secret} label=${t('mach.command')} value=${command} copy=${copy} />
  <//>`;
}

function Confirm({title, note, label, onGo, onClose}) {
  const {t} = useWords();
  return html`<${Modal} title=${title} onClose=${onClose} actions=${[{label: t('confirm.keep'), onClick: onClose},
    {label, kind: 'primary', keyName: 'Mod+Enter', onClick: () => { onClose(); onGo(); }}]}><p>${note}</p><//>`;
}

// Machines: http reads and ends node tokens (/api/machines); router and storage open the runs page on a machine's runs;
// copy is the clipboard's (a test passes its own); platform hands the page's address on to a computer from a phone.
export function Machines({store, commands, toasts, session, http, wire, router, storage, clock: now = () => Date.now(), copy, platform = nowhere}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  const names = useNames();
  const name = useName();
  const machines = useSignalValue(store.machines);
  useSignalValue(store.rev.shares);
  useSignalValue(store.rev.drains);
  useSignalValue(store.rev.projects);
  useSignalValue(store.rev.runs);
  useSignalValue(commands.pending);
  const [picked, setPicked] = useState('');
  const [modal, setModal] = useState(null);
  const [creds, setCreds] = useState([]);
  const [busy, setBusy] = useState(false);
  const [checking, setChecking] = useState(null);
  const st = store.state, at = now();
  const me = session?.id || '';
  const readCreds = () => http?.machineCreds().then(setCreds, () => {});
  // A node token records the machine it first connected from: read them again when a machine connects.
  const online = machines.filter(m => m.state === 'connected').map(m => m.name).sort().join(' ');
  useEffect(() => { if (!phone) readCreds(); }, [phone, online]);

  const groups = tm.machineGroups(machines, st, me);
  const ids = groups.flatMap(([, xs]) => xs.map(m => m.name));
  const cur = machines.find(m => m.name === picked) || machines.find(m => m.name === ids[0]);
  const close = () => setModal(null);
  const toRuns = name => { showMachine(storage, name); router?.go({page: 'runs'}); };
  const failed = e => toasts.show({text: apiText(w, e), tone: 'danger'});
  useListKeys({ids, selected: cur?.name, onSelect: setPicked, onOpen: phone ? null : toRuns, active: !modal && !(phone && picked)});

  const drain = m => {
    const on = !st.drains?.[m.name];
    commands.send('machine.drain', {machine: m.name, on}, {key: 'drain:' + m.name})
      .then(() => toasts.show({text: f(on ? 'mach.drained' : 'mach.undrained', m.name)}), failed);
  };
  const share = p => commands.send('machine.share', p, {key: 'machine:' + p.machine})
    .then(() => { close(); toasts.show({text: f('mach.sharedDone', p.machine)}); }, failed);
  const add = name => {
    setBusy(true);
    http.addMachine(name).then(v => { setModal({kind: 'added', name, token: v.token, command: v.command}); readCreds(); },
      e => setModal({kind: 'add', error: apiText(w, e)})).finally(() => setBusy(false));
  };
  const rebind = c => http.rebindMachine(c.id).then(() => { toasts.show({text: f('mach.rebound', c.name)}); readCreds(); }, failed);
  const revoke = c => http.revokeMachine(c.id).then(() => { toasts.show({text: f('mach.revoked', c.name)}); readCreds(); }, failed);
  const checks = !!wire?.has?.('machine.check');
  const check = name => {
    setChecking(name || '*');
    wire.call('machine.check', name ? {machine: name} : {}).then(res => {
      toasts.show({text: name ? f('mach.checked', name) : f('mach.checkedN', res?.machines?.length || 0)});
      for (const [n, code] of Object.entries(res?.failed || {})) toasts.show({text: f('mach.checkFailed', n, checkWhy(w, code)), tone: 'danger'});
    }, e => toasts.show({text: f('mach.checkFailed', name, checkWhy(w, e?.code)), tone: 'danger'})).finally(() => setChecking(null));
  };
  const credsOf = m => tm.credsOf(creds, m.name);
  const unmatched = creds.filter(c => !machines.some(m => m.name === c.name));
  const s = tm.machineSummary(machines);
  const summary = f('mach.summary', s.count, s.up, s.active, s.slots, s.queued);

  const people = Object.entries(names).filter(([id]) => id !== 'local').map(([id, n]) => ({value: id, label: n, sub: id})).sort((a, b) => a.label.localeCompare(b.label));
  const projects = Object.values(st.projects).map(p => ({value: p.id, label: p.name, sub: p.id})).sort((a, b) => a.label.localeCompare(b.label));
  const shareOf = m => modal?.kind === 'share' && html`<${Share} machine=${m.name} share=${st.shares?.[m.name]} owner=${m.owner} people=${people} projects=${projects}
    busy=${commands.state('machine:' + m.name) === 'pending'} onClose=${close} onSave=${share} />`;
  const dialog = modal && ({
    share: () => cur && shareOf(machines.find(m => m.name === modal.machine) || cur),
    add: () => html`<${Add} busy=${busy} error=${modal.error} onAdd=${add} onClose=${close} />`,
    added: () => html`<${Added} name=${modal.name} token=${modal.token} command=${modal.command} copy=${copy} onClose=${close} />`,
    rebind: () => html`<${Confirm} title=${f('mach.rebindTitle', modal.cred.name)} note=${f('mach.rebindNote', modal.cred.name)} label=${t('mach.rebind')}
      onGo=${() => rebind(modal.cred)} onClose=${close} />`,
    revoke: () => html`<${Confirm} title=${f('mach.revokeTitle', modal.cred.name)} note=${t('mach.revokeNote')} label=${t('mach.revoke')}
      onGo=${() => revoke(modal.cred)} onClose=${close} />`,
  })[modal.kind]?.();

  if (phone) {
    const open = picked && machines.find(m => m.name === picked);
    return html`<div class="mach mach-phone">
      <p class="t-muted mach-sum">${summary}</p>
      ${!machines.length && html`<p class="empty">${t('mach.none')}</p>`}
      ${groups.map(([g, xs]) => html`<${Panel} key=${g} title=${t('mach.' + g)} count=${xs.length}>
        <ul class="cards">${xs.map(m => html`<li key=${m.name}><button type="button" class="card-row" onClick=${() => setPicked(m.name)}>
          <span class="card-lead"><${Status} state=${tm.machineState(m)} /></span>
          <span class="card-main"><span class="card-primary mono">${m.name}</span>
            <span class="card-secondary">${[stateWord(w, m, at), viaWord(w, m), f('m.inUse', m.active || 0, m.slots || 0)].filter(Boolean).join(' · ')}</span>
            ${tm.machineNotes(m, st.drains?.[m.name]).slice(0, 1).map(n => html`<span class=${cx('card-secondary', 't-' + noteTone(n))}>${noteText(w, n, name)}</span>`)}</span>
        </button></li>`)}</ul>
      <//>`)}
      <${OnDesktop} platform=${platform} toasts=${toasts} page="machines" note=${t('mach.desktop')} />
      ${open && html`<${Drawer} title=${open.name} onClose=${() => setPicked('')}>
        <div class="mach-phone-head"><${Status} state=${tm.machineState(open)} word label=${stateWord(w, open, at)} /></div>
        <${Facts} m=${open} st=${st} creds=${[]} now=${at} />
      <//>`}
    </div>`;
  }

  const lane = cur && tm.laneOf(st, machines, cur.name, at);
  const mine = m => tm.mayShare(session, m);
  return html`<div class="mach">
    <div class="mach-head">
      <h1 class="tasks-title">${t('mach.title')}</h1><span class="t-muted">${summary}</span>
      <span class="mach-head-acts">
        ${checks && machines.some(m => m.state === 'connected') && html`<${Button} disabled=${!!checking} onClick=${() => check('')}>${t('mach.checkAll')}<//>`}
        <${Button} kind="primary" icon="plus" onClick=${() => setModal({kind: 'add'})}>${t('mach.add')}<//></span>
    </div>
    <div class="mach-body">
      <div class="mach-main">
        ${!machines.length && html`<${Panel} title=${t('mach.title')} count=${0}><p class="empty">${t('mach.none')}</p><//>`}
        ${groups.map(([g, xs]) => html`<section class="mach-group" key=${g}>
          <h2 class="sect-h">${t('mach.' + g)} <span class="mono count">${xs.length}</span></h2>
          <div class="mach-cards">${xs.map(m => html`<${Card} key=${m.name} m=${m} drain=${st.drains?.[m.name]} picked=${m.name === cur?.name} now=${at} onPick=${() => setPicked(m.name)} />`)}</div>
        </section>`)}
        ${lane && html`<${Panel} title=${f('mach.today', cur.name)} actions=${html`<span class="t-muted">${t('mach.todayNote')}</span>`}>
          <div class="panel-body"><${Timeline} from=${lane.from} to=${lane.to} label=${f('mach.today', cur.name)} lanes=${[{
            name: cur.name, note: f('m.inUse', cur.active || 0, cur.slots || 0),
            rows: lane.lane.rows.map(row => row.map(x => ({...x, title: `${st.tasks[x.task]?.title || x.task} · ${x.run}`}))),
          }]} /></div>
        <//>`}
        ${unmatched.length > 0 && html`<${Panel} title=${t('mach.otherCreds')} count=${unmatched.length}><div class="panel-body">
          ${unmatched.map(c => html`<${Cred} key=${c.id} c=${c} onRevoke=${x => setModal({kind: 'revoke', cred: x})} />`)}</div><//>`}
      </div>
      ${cur && html`<aside class="mach-aside panel" aria-label=${cur.name}>
        <header class="mach-aside-head"><${Status} state=${tm.machineState(cur)} /><b class="mono">${cur.name}</b><span class="t-muted">${stateWord(w, cur, at)}</span></header>
        <${Facts} m=${cur} st=${st} creds=${credsOf(cur)} now=${at} onRuns=${() => toRuns(cur.name)}
          draining=${commands.state('drain:' + cur.name) === 'pending'} onDrain=${mine(cur) ? () => drain(cur) : null}
          checking=${!!checking} onCheck=${checks && cur.state === 'connected' && !cur.retired ? () => check(cur.name) : null}
          onShare=${mine(cur) ? () => setModal({kind: 'share', machine: cur.name}) : null}
          onRebind=${cur.retired ? null : c => setModal({kind: 'rebind', cred: c})} onRevoke=${c => setModal({kind: 'revoke', cred: c})} />
      </aside>`}
    </div>
    ${dialog}
  </div>`;
}
