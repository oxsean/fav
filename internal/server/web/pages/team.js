// team is the team page: who is on this server and what they take part in, the projects the viewer sees, and for an
// admin the invitations not used yet, who may sign in without one, and the security log. An admin creates projects,
// invites, changes a person's role, disables them or hands their work on; a project's owner or an admin changes its
// members, settings and issue sync in the project's drawer (pages/project.js). On a phone the page only shows.
import {useState, useEffect} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useSignalValue, useName} from '../ui/base.js';
import {Panel} from '../ui/panel.js';
import {Modal} from '../ui/overlay.js';
import {Button, Chip, Chips, Segmented} from '../ui/controls.js';
import {TextInput} from '../ui/input.js';
import {Picker} from '../ui/picker.js';
import {Menu} from '../ui/menu.js';
import {Secret} from '../ui/secret.js';
import {useListKeys} from '../ui/table.js';
import {register} from '../core/i18n.js';
import {clock, day, duration} from '../core/format.js';
import * as tm from '../core/team.js';
import {apiText} from './words.js';
import {ProjectDrawer, AddMember} from './project.js';
import './taskwords.js';
import {OnDesktop} from '../ui/desk.js';
import {nowhere} from '../core/platform.js';

register('team', {
  'team.title': ['团队', 'Team'], 'team.summary': ['%d 人在用，%d 人已停用', '%d people active, %d disabled'],
  'team.youAdmin': ['你是管理员', 'you are an admin'], 'team.youMember': ['你是成员', 'you are a member'],
  'team.new': ['新建项目', 'New project'], 'team.invite': ['邀请', 'Invite'],
  'team.members': ['成员', 'People'], 'team.seenNote': ['最近活动 = 他的凭据最近一次被用', 'Last seen = the last use of one of their credentials'],
  'team.c.person': ['人', 'Person'], 'team.c.role': ['身份', 'Role'], 'team.c.logins': ['登录方式', 'Signs in with'], 'team.c.projects': ['项目', 'Projects'],
  'team.c.machines': ['名下机器', 'Machines'], 'team.c.seen': ['最近活动', 'Last seen'],
  'team.disabled': ['已停用', 'disabled'], 'team.you': ['%s（你）', '%s (you)'], 'team.inProject': ['%s %s', '%s %s'],
  'team.justNow': ['刚刚', 'just now'], 'team.ago': ['%s 前', '%s ago'],
  'team.more': ['更多', 'More'], 'team.makeAdmin': ['设为管理员', 'Make an admin'], 'team.makeMember': ['设为成员', 'Make a member'],
  'team.disable': ['停用', 'Disable'], 'team.enable': ['启用', 'Enable'], 'team.offboard': ['交接并停用…', 'Hand over and disable…'],
  'team.roleSet': ['%s 现在是%s', '%s is now %s'], 'team.enabled': ['%s 已启用', '%s is enabled'], 'team.disabledDone': ['%s 已停用', '%s is disabled'],
  'team.disableTitle': ['停用 %s？', 'Disable %s?'],
  'team.disableNote': ['他的网页会话和连接立刻断开，之后登录不了；他的任务、项目和机器不动。要把这些交给别人，用「交接并停用」。', 'Their browser sessions and connections close at once and they cannot sign in; their tasks, projects and machines stay as they are. To give those to someone, hand them over instead.'],
  'team.offTitle': ['把 %s 的工作交给谁？', 'Who takes over from %s?'], 'team.offTo': ['交给', 'Hand over to'],
  'team.offProjects': ['他负责的 %d 个项目交给 %s', 'The %d projects they own go to %s'],
  'team.offLeft': ['他离开参与的 %d 个项目', 'They leave the %d projects they take part in'],
  'team.offTasks': ['%d 个没结束的任务（他负责或验收的）交给各自项目的负责人，不在项目里的交给 %s', 'The %d unfinished tasks they own or accept go to their project\'s owner, or to %s outside a project'],
  'team.offDefs': ['他的 agent 定义 %s 交给 %s', 'Their agent definitions %s go to %s'],
  'team.offMachines': ['机器 %s 不再对别人开放', 'Machines %s close to everyone else'],
  'team.offCanceled': ['机器 %s 不再对别人开放，别人排在那里的 %d 个运行取消', 'Machines %s close to everyone else, and the %d runs others queued there are canceled'],
  'team.offEnd': ['最后停用他，吊销他的全部凭据（包括他机器的 token）', 'Last, they are disabled and every credential of theirs ends, their machines\' tokens too'],
  'team.offGo': ['交接并停用', 'Hand over and disable'], 'team.offDone': ['%s 的工作已交给 %s，账号已停用', 'The work of %s went to %s; they are disabled'],
  'team.projects': ['项目', 'Projects'], 'team.projectsNote': ['负责人管成员', 'Their owners manage the members'],
  'team.owner': ['负责人 %s', 'owner %s'], 'team.tasks': ['%d 个任务 · %d 未结束', '%d tasks · %d open'],
  'team.people': ['%d 人参与 · %d 人只读', '%d take part · %d read'], 'team.sync': ['工单同步 %s · %s', 'Issue sync %s · %s'],
  'team.sync.ok': ['同步中', 'syncing'], 'team.sync.stopped': ['已停', 'stopped'], 'team.sync.paused': ['限流中', 'rate limited'], 'team.noProjects': ['你还不在任何项目里', 'You are in no project yet'],
  'team.name': ['名称', 'Name'], 'team.projectOwner': ['负责人', 'Owner'], 'team.create': ['建立', 'Create'], 'team.created': ['项目 %s 已建立', 'Project %s is created'],
  'team.add': ['加成员', 'Add a member'], 'team.person': ['谁', 'Who'], 'team.role': ['角色', 'Role'],
  'team.added': ['%s 加进了 %s', '%s joined %s'], 'team.changed': ['%s 现在是%s', '%s is now a %s'],
  'team.remove': ['移出', 'Remove'], 'team.removeTitle': ['把 %s 移出 %s？', 'Remove %s from %s?'],
  'team.removeNote': ['之后看不到这个项目的任务和运行。', 'They no longer see the project\'s tasks and runs.'], 'team.removed': ['%s 已移出 %s', '%s left %s'],
  'team.invites': ['没用过的邀请', 'Invitations not used yet'], 'team.invitesNote': ['72 小时有效，用一次就作废', 'Good for 72 hours, once'],
  'team.noInvites': ['没有', 'None'], 'team.joins': ['登录后加入项目「%s」（%s）', 'Joins project %s as %s'], 'team.noJoin': ['不带项目', 'No project'],
  'team.inviteBy': ['%s %s 发出 · 还剩 %s', 'from %s %s · %s left'], 'team.revoke': ['作废', 'Revoke'], 'team.revoked': ['邀请 #%s 已作废', 'Invitation #%s is revoked'],
  'team.inviteTitle': ['邀请一个人', 'Invite someone'], 'team.inviteRole': ['身份', 'Role'], 'team.inviteProject': ['加入项目（可不选）', 'Join a project (optional)'],
  'team.inviteAccess': ['在项目里', 'In the project'], 'team.inviteGo': ['生成链接', 'Make the link'],
  'team.inviteLink': ['邀请链接（只显示这一次）', 'The link (shown only this once)'], 'team.done': ['完成', 'Done'],
  'team.inviteHow': ['把链接发给对方，%s 前有效，用一次就作废。', 'Send it to them: it works once, until %s.'],
  'team.admits': ['谁能直接登录进来', 'Who may sign in directly'], 'team.addAdmit': ['加规则', 'Add a rule'],
  'team.admitsNote': ['不开放注册。先认已关联的身份，再认邀请，最后才看这些规则；只认 provider 标明已验证的邮箱。', 'Sign-up is closed. A linked identity counts first, then an invitation, and only then these rules; only emails the provider marks as verified count.'],
  'team.noAdmits': ['没有规则：只有已关联的身份和受邀的人能登录', 'No rules: only linked identities and invited people sign in'],
  'team.admit.domain': ['邮箱域名', 'Email domain'], 'team.admit.email': ['邮箱', 'Email'], 'team.admit.login': ['账号', 'Account'],
  'team.admitKind': ['按什么认', 'Match by'], 'team.admitValue': ['值', 'Value'],
  'team.admitHint.domain': ['example.com', 'example.com'], 'team.admitHint.email': ['someone@example.com', 'someone@example.com'],
  'team.admitHint.login': ['github:用户名', 'github:username'], 'team.admitAdded': ['规则已加上', 'The rule is added'],
  'team.admitRemove': ['去掉规则 %s', 'Remove the rule %s'], 'team.admitRemoved': ['规则 %s 已去掉', 'The rule %s is removed'],
  'team.audit': ['审计', 'Audit'], 'team.auditNote': ['只追加 · 不记秘密，只记 id 和来源地址', 'Append-only · no secrets, only ids and source addresses'],
  'team.audit.all': ['全部', 'All'], 'team.audit.login': ['登录', 'Sign-ins'], 'team.audit.creds': ['凭据', 'Credentials'], 'team.audit.refused': ['被拒', 'Refused'],
  'team.noAudit': ['没有记录', 'Nothing logged'],
  'team.desktop': ['成员、邀请和准入在电脑上管理', 'People, invitations and admission are managed on a computer'],
});

// ago is how long since at: just now under a minute, then the time it has been up to a day, then the date.
function ago(w, at, now) {
  if (!at) return '—';
  const ms = now - Date.parse(at);
  if (ms < 60e3) return w.t('team.justNow');
  return ms < 864e5 ? w.f('team.ago', duration(ms)) : day(at);
}
const when = at => (at ? day(at) + ' ' + clock(at) : '');
const byLabel = (a, b) => a.label.localeCompare(b.label);

function NewProject({people, me, busy, onCreate, onClose}) {
  const {t} = useWords();
  const [name, setName] = useState('');
  const [owner, setOwner] = useState(me);
  const ok = name.trim() && owner && !busy;
  return html`<${Modal} title=${t('team.new')} onClose=${onClose}
    actions=${[{label: t('home.cancel'), onClick: onClose}, {label: t('team.create'), kind: 'primary', keyName: 'Mod+Enter', disabled: !ok, onClick: () => onCreate(name.trim(), owner)}]}>
    <${TextInput} label=${t('team.name')} value=${name} onInput=${setName} autoFocus />
    <${Picker} label=${t('team.projectOwner')} value=${owner} onChange=${setOwner} options=${people} />
  <//>`;
}

function Invite({projects, busy, made, copy, onInvite, onClose}) {
  const {t, f} = useWords();
  const [role, setRole] = useState('member');
  const [project, setProject] = useState('');
  const [access, setAccess] = useState('participant');
  if (made) {
    return html`<${Modal} title=${t('team.inviteTitle')} onClose=${onClose} actions=${[{label: t('team.done'), kind: 'primary', keyName: 'Mod+Enter', onClick: onClose}]}>
      <${Secret} label=${t('team.inviteLink')} value=${made.url} copy=${copy} />
      <p class="mach-p">${f('team.inviteHow', when(made.expires))}</p>
    <//>`;
  }
  return html`<${Modal} title=${t('team.inviteTitle')} onClose=${onClose}
    actions=${[{label: t('home.cancel'), onClick: onClose}, {label: t('team.inviteGo'), kind: 'primary', keyName: 'Mod+Enter', disabled: busy,
      onClick: () => onInvite(project ? {role, project, access} : {role})}]}>
    <div class="field"><span class="brief-label">${t('team.inviteRole')}</span>
      <${Segmented} label=${t('team.inviteRole')} value=${role} onChange=${setRole} options=${tm.userRoles.map(r => ({value: r, label: t('role.' + r)}))} /></div>
    <${Picker} label=${t('team.inviteProject')} value=${project} onChange=${setProject} options=${[{value: '', label: t('team.noJoin')}, ...projects]} />
    ${project && html`<div class="field"><span class="brief-label">${t('team.inviteAccess')}</span>
      <${Segmented} label=${t('team.inviteAccess')} value=${access} onChange=${setAccess} options=${tm.accessRoles.map(r => ({value: r, label: t('role.' + r)}))} /></div>`}
  <//>`;
}

function AddAdmit({busy, error, onAdd, onClose}) {
  const {t} = useWords();
  const [kind, setKind] = useState('domain');
  const [value, setValue] = useState('');
  const [role, setRole] = useState('member');
  return html`<${Modal} title=${t('team.addAdmit')} onClose=${onClose}
    actions=${[{label: t('home.cancel'), onClick: onClose}, {label: t('team.addAdmit'), kind: 'primary', keyName: 'Mod+Enter', disabled: !value.trim() || busy,
      onClick: () => onAdd({kind, value: value.trim(), role})}]}>
    <p class="field-note">${t('team.admitsNote')}</p>
    <div class="field"><span class="brief-label">${t('team.admitKind')}</span>
      <${Segmented} label=${t('team.admitKind')} value=${kind} onChange=${setKind} options=${tm.admitKinds.map(k => ({value: k, label: t('team.admit.' + k)}))} /></div>
    <${TextInput} label=${t('team.admitValue')} value=${value} onInput=${setValue} mono autoFocus placeholder=${t('team.admitHint.' + kind)} error=${error} />
    <div class="field"><span class="brief-label">${t('team.inviteRole')}</span>
      <${Segmented} label=${t('team.inviteRole')} value=${role} onChange=${setRole} options=${tm.userRoles.map(r => ({value: r, label: t('role.' + r)}))} /></div>
  <//>`;
}

function Offboard({user, people, me, plan, busy, onGo, onClose}) {
  const {t, f} = useWords();
  const name = useName();
  const [to, setTo] = useState(me);
  const p = plan(to);
  const heir = name(to);
  return html`<${Modal} title=${f('team.offTitle', name(user))} onClose=${onClose}
    actions=${[{label: t('confirm.keep'), onClick: onClose}, {label: t('team.offGo'), kind: 'primary', keyName: 'Mod+Enter', disabled: !to || busy, onClick: () => onGo(to)}]}>
    <${Picker} label=${t('team.offTo')} value=${to} onChange=${setTo} options=${people.filter(o => o.value !== user)} />
    <ol class="team-steps">
      ${p.projects.length > 0 && html`<li>${f('team.offProjects', p.projects.length, heir)}</li>`}
      ${p.left.length > 0 && html`<li>${f('team.offLeft', p.left.length)}</li>`}
      ${p.tasks.length > 0 && html`<li>${f('team.offTasks', p.tasks.length, heir)}</li>`}
      ${p.defs.length > 0 && html`<li>${f('team.offDefs', p.defs.join(', '), heir)}</li>`}
      ${p.machines.length > 0 && html`<li>${p.canceled ? f('team.offCanceled', p.machines.join(', '), p.canceled) : f('team.offMachines', p.machines.join(', '))}</li>`}
      <li>${t('team.offEnd')}</li>
    </ol>
  <//>`;
}

// Team: http reads and changes people, invitations, rules and the log (/api); session is who signed in (an admin sees
// and does the rest); copy is the clipboard's (a test passes its own); platform hands the page's address on to a
// computer from a phone.
export function Team({store, commands, toasts, session, http, wire, clock: now = () => Date.now(), copy, platform = nowhere}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  const name = useName();
  const machines = useSignalValue(store.machines);
  useSignalValue(store.rev.projects);
  useSignalValue(store.rev.tasks);
  useSignalValue(commands.pending);
  const [users, setUsers] = useState([]);
  const [admits, setAdmits] = useState([]);
  const [invites, setInvites] = useState([]);
  const [audit, setAudit] = useState([]);
  const [filter, setFilter] = useState('all');
  const [modal, setModal] = useState(null);
  const [open, setOpen] = useState('');
  const [picked, setPicked] = useState('');
  const [busy, setBusy] = useState(false);
  const [trackers, setTrackers] = useState([]);
  const st = store.state, at = now();
  const me = session?.id || '';
  const admin = tm.isAdmin(session);
  const manages = !phone && admin;
  const quiet = () => {};
  const readUsers = () => http?.users().then(setUsers, quiet);
  const readAdmin = () => {
    if (!admin || !http) return;
    http.admits().then(setAdmits, quiet);
    http.invites().then(setInvites, quiet);
    http.audit().then(setAudit, quiet);
  };
  useEffect(() => { readUsers(); readAdmin(); }, [admin]);
  useEffect(() => { if (!phone && !open) http?.trackers().then(setTrackers, quiet); }, [phone, open]);

  const close = () => setModal(null);
  const failed = e => toasts.show({text: apiText(w, e), tone: 'danger'});
  const said = text => toasts.show({text});
  const call = (p, text, after) => { setBusy(true); return p.then(v => { close(); if (text) said(text); after?.(v); readAdmin(); }, failed).finally(() => setBusy(false)); };
  const send = (method, params, key, text) => commands.send(method, params, {key}).then(v => { if (text) said(text); return v; }, e => { failed(e); throw e; });

  const list = tm.people(users, st, machines);
  const active = list.filter(u => !u.disabled);
  const options = active.map(u => ({value: u.id, label: u.name || u.id, sub: u.id})).sort(byLabel);
  const projects = Object.values(st.projects).sort((a, b) => a.name.localeCompare(b.name));
  const projectOptions = projects.map(p => ({value: p.id, label: p.name, sub: p.id}));
  const ids = projects.map(p => p.id);
  const cur = picked && ids.includes(picked) ? picked : ids[0];
  const drawerOf = st.projects[open];
  useListKeys({ids, selected: cur, onSelect: setPicked, onOpen: setOpen, active: !modal && !drawerOf});
  const summary = [f('team.summary', active.length, list.length - active.length), admin ? t('team.youAdmin') : t('team.youMember')].join(' · ');

  const setUser = (u, change, text) => call(http.setUser({id: u.id, ...change}), text, readUsers);
  const menu = u => (u.id === me ? [] : [
    ...(u.disabled ? [{label: t('team.enable'), onClick: () => setUser(u, {disabled: false}, f('team.enabled', u.name))}] : [
      u.role === 'admin' ? {label: t('team.makeMember'), onClick: () => setUser(u, {role: 'member'}, f('team.roleSet', u.name, t('role.member')))}
        : {label: t('team.makeAdmin'), onClick: () => setUser(u, {role: 'admin'}, f('team.roleSet', u.name, t('role.admin')))},
      {label: t('team.disable'), kind: 'danger', onClick: () => setModal({kind: 'disable', user: u})},
      {label: t('team.offboard'), kind: 'danger', onClick: () => setModal({kind: 'offboard', user: u.id})},
    ]),
  ]);
  const member = (p, user, role, text) => send('project.member', {project: p.id, user, role}, 'project:' + p.id, text).catch(quiet);
  const pending = p => commands.state('project:' + p.id) === 'pending';

  const dialog = modal && ({
    new: () => html`<${NewProject} people=${options} me=${me} busy=${commands.state('project:new') === 'pending'} onClose=${close}
      onCreate=${(n, owner) => send('project.create', owner === me ? {name: n} : {name: n, owner}, 'project:new', f('team.created', n))
        .then(p => { close(); if (p?.id) setOpen(p.id); }, quiet)} />`,
    invite: () => html`<${Invite} projects=${projectOptions} busy=${busy} made=${modal.made} copy=${copy} onClose=${close}
      onInvite=${p => { setBusy(true); http.makeInvite(p).then(made => { setModal({kind: 'invite', made}); readAdmin(); }, failed).finally(() => setBusy(false)); }} />`,
    admit: () => html`<${AddAdmit} busy=${busy} error=${modal.error} onClose=${close}
      onAdd=${a => { setBusy(true); http.addAdmit(a).then(() => { close(); said(t('team.admitAdded')); readAdmin(); }, e => setModal({kind: 'admit', error: apiText(w, e)})).finally(() => setBusy(false)); }} />`,
    disable: () => html`<${Modal} title=${f('team.disableTitle', modal.user.name)} onClose=${close} actions=${[{label: t('confirm.keep'), onClick: close},
      {label: t('team.disable'), kind: 'primary', keyName: 'Mod+Enter', onClick: () => setUser(modal.user, {disabled: true}, f('team.disabledDone', modal.user.name))}]}>
      <p>${t('team.disableNote')}</p><//>`,
    offboard: () => html`<${Offboard} user=${modal.user} people=${options} me=${me} busy=${busy} onClose=${close}
      plan=${to => tm.offboardPlan(st, machines, modal.user, to)}
      onGo=${to => call(http.offboard({user: modal.user, to}), f('team.offDone', name(modal.user), name(to)), readUsers)} />`,
    add: () => {
      const p = st.projects[modal.project];
      const people = options.filter(o => o.value !== p.owner && !(p.members || {})[o.value]);
      return html`<${AddMember} project=${p} people=${people} busy=${pending(p)} onClose=${close}
        onAdd=${(user, role) => member(p, user, role, f('team.added', name(user), p.name)).then(close)} />`;
    },
    remove: () => {
      const p = st.projects[modal.project];
      return html`<${Modal} title=${f('team.removeTitle', name(modal.user), p.name)} onClose=${close} actions=${[{label: t('confirm.keep'), onClick: close},
        {label: t('team.remove'), kind: 'primary', keyName: 'Mod+Enter', onClick: () => { close(); member(p, modal.user, '', f('team.removed', name(modal.user), p.name)); }}]}>
        <p>${t('team.removeNote')}</p><//>`;
    },
  })[modal.kind]?.();

  const drawer = drawerOf && html`<${ProjectDrawer} project=${drawerOf} st=${st} machines=${machines} people=${options} wire=${wire} http=${http}
    commands=${commands} toasts=${toasts} copy=${copy} manages=${!phone && tm.mayManage(session, drawerOf)} busy=${pending(drawerOf)}
    onClose=${() => setOpen('')} onAdd=${() => setModal({kind: 'add', project: drawerOf.id})}
    onRemove=${m => setModal({kind: 'remove', project: drawerOf.id, user: m.id})}
    onRole=${(m, role) => member(drawerOf, m.id, role, f('team.changed', m.label, t('role.' + role)))} />`;

  const roleOf = u => (u.disabled ? t('team.disabled') : t('role.' + u.role));
  const projectsText = u => u.projects.map(p => f('team.inProject', p.name, t('role.' + p.role))).join(' · ') || '—';
  const projectRows = html`<ul class="team-projects">${projects.map(p => {
    const n = tm.projectFacts(st, p);
    return html`<li key=${p.id}><button type="button" class=${cx('team-project', !phone && p.id === cur && 'sel')} onClick=${() => { setPicked(p.id); setOpen(p.id); }}>
      <span class="team-project-head"><b>${p.name}</b><span class="t-muted">${f('team.owner', name(p.owner))}</span><span class="mono t-muted team-project-n">${f('team.tasks', n.tasks, n.open)}</span></span>
      <span class="t-muted">${f('team.people', n.participants, n.readers)}</span>
      ${trackers.filter(x => x.project === p.id).map(x => html`<span class=${cx('team-sync', tm.trackerState(x) === 'ok' ? 't-muted' : 't-failed')}>
        ${f('team.sync', x.repo, t('team.sync.' + tm.trackerState(x)))}</span>`)}
    </button></li>`;
  })}</ul>`;

  if (phone) {
    return html`<div class="team team-phone">
      <p class="t-muted mach-sum">${summary}</p>
      <${Panel} title=${t('team.members')} count=${active.length}>
        <ul class="cards">${list.map(u => html`<li key=${u.id} class="card-row team-card">
          <span class="card-main"><span class="card-primary">${u.id === me ? f('team.you', u.name) : u.name}</span>
            <span class="card-secondary">${[roleOf(u), projectsText(u)].join(' · ')}</span></span>
        </li>`)}</ul>
      <//>
      <${Panel} title=${t('team.projects')} count=${projects.length}>
        ${projects.length ? projectRows : html`<p class="empty">${t('team.noProjects')}</p>`}
      <//>
      <${OnDesktop} platform=${platform} toasts=${toasts} page="team" note=${t('team.desktop')} />
      ${drawer}
    </div>`;
  }

  const cols = admin ? 'team-grid-admin' : 'team-grid';
  return html`<div class="team">
    <div class="mach-head">
      <h1 class="tasks-title">${t('team.title')}</h1><span class="t-muted">${summary}</span>
      ${admin && html`<span class="mach-head-acts"><${Button} icon="plus" onClick=${() => setModal({kind: 'new'})}>${t('team.new')}<//>
        <${Button} kind="primary" onClick=${() => setModal({kind: 'invite'})}>${t('team.invite')}<//></span>`}
    </div>
    <div class="team-body">
      <div class="team-main-col">
        <${Panel} title=${t('team.members')} count=${active.length} actions=${admin && html`<span class="t-muted">${t('team.seenNote')}</span>`}>
          <div class=${cx('team-table', cols)} role="table" aria-label=${t('team.members')}>
            <div class="team-tr team-th" role="row">
              <span role="columnheader">${t('team.c.person')}</span><span role="columnheader">${t('team.c.role')}</span>
              ${admin && html`<span role="columnheader">${t('team.c.logins')}</span>`}
              <span role="columnheader">${t('team.c.projects')}</span><span role="columnheader">${t('team.c.machines')}</span>
              ${admin && html`<span role="columnheader">${t('team.c.seen')}</span><span role="columnheader"></span>`}
            </div>
            ${list.map(u => html`<div key=${u.id} class=${cx('team-tr', u.disabled && 'off')} role="row">
              <span role="cell" class="team-who"><b class="ell">${u.id === me ? f('team.you', u.name) : u.name}</b>${u.email && html`<span class="t-muted ell">${u.email}</span>`}</span>
              <span role="cell"><span class=${cx('chip', u.role === 'admin' && !u.disabled && 'on')}>${roleOf(u)}</span></span>
              ${admin && html`<span role="cell" class="t-muted">${(u.logins || []).join(' · ') || '—'}</span>`}
              <span role="cell" class="team-wrap">${projectsText(u)}</span>
              <span role="cell" class="mono t-muted">${u.machines.join(' ') || '—'}</span>
              ${admin && html`<span role="cell" class="t-muted">${ago(w, u.seen, at)}</span>
                <span role="cell">${u.id !== me && html`<${Menu} label="⋯" items=${menu(u)} />`}</span>`}
            </div>`)}
          </div>
        <//>
        ${admin && html`<${Panel} title=${t('team.audit')} count=${audit.length} actions=${html`<span class="t-muted">${t('team.auditNote')}</span>`}>
          <div class="panel-body team-audit">
            <${Chips} label=${t('team.audit')}>${tm.auditFilters.map(x => html`<${Chip} label=${t('team.audit.' + x)} on=${filter === x} onClick=${() => setFilter(x)} />`)}<//>
            ${audit.filter(e => tm.auditIn(filter, e)).length ? html`<ul class="team-log">${audit.filter(e => tm.auditIn(filter, e)).map((e, i) => html`<li key=${i}>
              <span class="mono t-muted">${Date.parse(e.at) > at - 864e5 ? clock(e.at) : day(e.at)}</span><span>${e.actor ? name(e.actor) : '—'}</span>
              <span class=${cx('mono', tm.auditRefused(e) ? 't-failed' : 't-muted')}>${e.kind}</span><span class="ell">${e.detail}</span><span class="mono t-muted">${e.ip}</span>
            </li>`)}</ul>` : html`<p class="empty">${t('team.noAudit')}</p>`}
          </div>
        <//>`}
      </div>
      <div class="team-side">
        <${Panel} title=${t('team.projects')} count=${projects.length} actions=${html`<span class="t-muted">${t('team.projectsNote')}</span>`}>
          ${projects.length ? projectRows : html`<p class="empty">${t('team.noProjects')}</p>`}
        <//>
        ${admin && html`<${Panel} title=${t('team.invites')} count=${invites.length} actions=${html`<span class="t-muted">${t('team.invitesNote')}</span>`}>
          ${invites.length ? html`<ul class="team-rows">${invites.map(v => html`<li class="team-row" key=${v.id}>
            <span class="team-main"><span><span class="mono">#${v.id}</span> ${t('role.' + v.role)} · ${v.project ? f('team.joins', st.projects[v.project]?.name || v.project, t('role.' + (v.access || 'participant'))) : t('team.noJoin')}</span>
              <span class="team-sub">${f('team.inviteBy', name(v.created_by), when(v.created), duration(Date.parse(v.expires) - at))}</span></span>
            <${Button} kind="quiet danger" onClick=${() => call(http.revokeInvite(v.id), f('team.revoked', v.id))}>${t('team.revoke')}<//>
          </li>`)}</ul>` : html`<p class="empty">${t('team.noInvites')}</p>`}
        <//>
        <${Panel} title=${t('team.admits')} count=${admits.length} actions=${html`<${Button} kind="quiet" icon="plus" onClick=${() => setModal({kind: 'admit'})}>${t('team.addAdmit')}<//>`}>
          <p class="team-note t-muted">${t('team.admitsNote')}</p>
          ${admits.length ? html`<ul class="team-rows">${admits.map(a => html`<li class="team-row" key=${a.kind + a.value}>
            <span class="chip">${t('team.admit.' + a.kind)}</span><span class="team-main mono">${a.value}</span><span class="t-muted">${t('role.' + a.role)}</span>
            <${Button} kind="quiet danger" icon="close" label=${f('team.admitRemove', a.value)}
              onClick=${() => call(http.removeAdmit({kind: a.kind, value: a.value}), f('team.admitRemoved', a.value))} />
          </li>`)}</ul>` : html`<p class="empty">${t('team.noAdmits')}</p>`}
        <//>`}
      </div>
    </div>
    ${drawer}
    ${dialog}
  </div>`;
}
