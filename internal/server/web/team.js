'use strict';
// team.js holds what a team server adds to the page: ways to sign in and invitations, projects and their members,
// machines' owners and shares, the account, and for admins users, admission rules, invitations and the audit log.
// It runs on app.js's helpers (t, esc, ui, api, showModal, button, toast), which exist by the time anything here runs.
const teamWords = {
  projects: ['项目', 'Projects'], account: ['账号', 'Account'], admin: ['管理', 'Admin'],
  projectsSubtitle: ['项目决定谁能看见和操作其中的任务。', 'A project decides who sees and works on its tasks.'],
  noProjects: ['还没有你参与的项目', 'You are in no project yet'], noProjectsHelp: ['没有项目的任务只属于创建者。管理员可以新建项目。', 'A task outside projects belongs to whoever created it. Admins create projects.'],
  newProject: ['新建项目', 'New project'], projectID: ['项目 ID', 'Project ID'], projectIDHint: ['小写字母、数字、- 和 _，留空自动生成。', 'Lowercase letters, digits, - and _; empty makes one.'],
  projectName: ['名称', 'Name'], owner: ['负责人', 'Owner'], members: ['成员', 'Members'], role: ['角色', 'Role'],
  'role.participant': ['参与者', 'Participant'], 'role.reader': ['只读', 'Reader'], 'role.owner': ['负责人', 'Owner'],
  'role.admin': ['管理员', 'Admin'], 'role.member': ['成员', 'Member'],
  addMember: ['添加成员', 'Add member'], removeMember: ['移出', 'Remove'], editProject: ['编辑项目', 'Edit project'],
  project: ['项目', 'Project'], noProject: ['不属于项目（仅自己）', 'No project (only you)'], user: ['用户', 'User'],
  projectSaved: ['项目已保存', 'Project saved'], memberSaved: ['成员已更新', 'Members updated'],
  addMachine: ['添加机器', 'Add machine'], machineName: ['机器名', 'Machine name'],
  machineNameHint: ['字母、数字、- 和 _；节点用这个名字连接。', 'Letters, digits, - and _; the node connects under this name.'],
  machineAdded: ['机器已添加。token 只显示这一次：', 'Machine added. The token is shown only this once:'],
  machineCommand: ['在那台机器上运行（token 写进文件后）：', 'On that machine, with the token saved to the file, run:'],
  share: ['共享', 'Share'], shareTitle: ['共享机器', 'Share machine'], shareHelp: ['选中的用户，或选中项目的运行，可以使用这台机器。', 'The chosen users, and runs of the chosen projects, may use this machine.'],
  approve: ['也可以批准它们的权限请求', 'They may also approve its permission requests'], shared: ['已共享', 'Shared'], notShared: ['未共享', 'Not shared'],
  shareSaved: ['共享已保存', 'Sharing saved'], ownedBy: ['所有者', 'Owner'],
  machineTokens: ['机器凭据', 'Machine credentials'], rebind: ['换机', 'Rebind'], revoke: ['吊销', 'Revoke'],
  rebindHelp: ['下次用这个 token 连接的机器成为它的新主机。', 'The next machine that connects with this token becomes its host.'],
  revokeTitle: ['吊销凭据？', 'Revoke this credential?'], revokeHelp: ['使用它的连接会立即断开，不能恢复。', 'Connections using it close at once. This cannot be undone.'],
  boundTo: ['绑定', 'Bound to'], unbound: ['尚未连接', 'Not connected yet'], created: ['创建于', 'Created'], lastUsed: ['最近使用', 'Last used'],
  profile: ['个人信息', 'Profile'], identities: ['登录账号', 'Sign-in accounts'], linkAccount: ['关联', 'Link'],
  linkHelp: ['关联后，用这些账号都能登录到你。', 'Once linked, each of these accounts signs in as you.'],
  tokensTitle: ['token 与会话', 'Tokens and sessions'], tokensHelp: ['token 给 CLI 和 TUI 用；会话是登录过的浏览器。', 'Tokens are for the CLI and TUI; sessions are signed-in browsers.'],
  newToken: ['新建 token', 'New token'], tokenName: ['名称', 'Name'], tokenMade: ['token 只显示这一次：', 'The token is shown only this once:'],
  'kind.web': ['浏览器', 'Browser'], 'kind.token': ['token', 'Token'], 'kind.node': ['机器', 'Machine'], thisBrowser: ['当前浏览器', 'This browser'],
  users: ['用户', 'Users'], disable: ['停用', 'Disable'], enable: ['启用', 'Enable'], disabled: ['已停用', 'Disabled'],
  admits: ['准入规则', 'Admission rules'], admitsHelp: ['用这些方式登录的新账号会自动加入。', 'New accounts signing in these ways join on their own.'],
  'admit.email': ['邮箱', 'Email'], 'admit.domain': ['邮箱域名', 'Email domain'], 'admit.login': ['账号（provider:username）', 'Account (provider:username)'],
  addAdmit: ['添加规则', 'Add rule'], value: ['值', 'Value'], invite: ['邀请链接', 'Invitation link'], makeInvite: ['生成邀请', 'Make invitation'],
  inviteMade: ['发给对方；用一次后失效，3 天后过期：', 'Send it to them. It works once and expires in 3 days:'],
  audit: ['审计日志', 'Audit log'], actor: ['操作者', 'Actor'], event: ['事件', 'Event'], at: ['时间', 'Time'], copy: ['复制', 'Copy'], copied: ['已复制', 'Copied'],
  orToken: ['或使用 token', 'Or use a token'], signInWith: ['用 {0} 登录', 'Sign in with {0}'],
  invited: ['你收到了邀请：用下面任一方式登录即可加入。', 'You are invited: sign in any way below to join.'],
  'signin.state': ['登录已过期或来自另一个浏览器，请重新开始。', 'The sign-in expired or began in another browser. Start again.'],
  'signin.provider': ['登录服务没有确认这次登录。', 'The sign-in service did not confirm this sign-in.'],
  'signin.not_admitted': ['这个账号还没有被允许加入。请管理员邀请你或添加准入规则。', 'This account is not admitted yet. Ask an admin for an invitation or a rule.'],
  'signin.disabled': ['这个用户已被停用。', 'This user is disabled.'],
  'signin.linked': ['这个账号已经属于另一个用户。', 'This account already belongs to another user.'],
  'signin.internal': ['服务器出错，请稍后再试。', 'The server failed. Try again later.'],
  exists: ['名字已被占用。', 'That name is taken.'], name: ['名字不合法。', 'That name is not valid.'], self: ['不能改自己。', 'You cannot change yourself.'],
  csrf: ['请求被拒绝，请刷新页面。', 'The request was refused. Reload the page.'], forbidden: ['你没有这个权限。', 'You are not allowed to do that.'],
  internal: ['服务器出错。', 'The server failed.'], team: ['团队', 'Team'], make: ['创建', 'Create'], changeSaved: ['已保存', 'Saved'], add: ['添加', 'Add'], saveShare: ['保存共享', 'Save sharing'], nobody: ['无', 'Nobody'],
};

const Team = (() => {
  const pages = ['projects', 'account', 'admin'];
  const data = {users: [], logins: [], creds: [], tokens: [], identities: [], admits: [], audit: []};
  const roles = ['participant', 'reader'];

  async function rest(method, path, body) {
    const res = await fetch(path, {method, credentials: 'same-origin',
      headers: method === 'GET' ? {} : {'X-Tend': '1', 'Content-Type': 'application/json'},
      body: body === undefined ? undefined : JSON.stringify(body)});
    if (res.status === 204) return null;
    const v = await res.json().catch(() => null);
    if (!res.ok) throw Object.assign(new Error(v?.error || 'internal'), {code: v?.error || 'internal', detail: ''});
    return v;
  }
  const isAdmin = () => ui.me?.role === 'admin';
  const userName = id => { const u = data.users.find(x => x.id === id); return u ? u.name : id === ui.me?.id ? ui.me.name : id; };
  const fromHash = prefix => location.hash.startsWith('#' + prefix) ? decodeURIComponent(location.hash.slice(prefix.length + 1)) : '';
  const when = v => v && !v.startsWith('0001') ? date(v) : '—';
  const projects = () => Object.values(ui.state.projects || {}).sort((a, b) => a.name.localeCompare(b.name));
  const myRole = p => p.owner === ui.me?.id ? 'participant' : (p.members || {})[ui.me?.id] || '';
  const manages = p => isAdmin() || p.owner === ui.me?.id;
  const secretBox = (label, value) => `<p>${label}</p><div class="flex secret-row"><code class="secret" id="secret-value">${esc(value)}</code>${button('team-copy', t('copy'), `data-value="${esc(value)}"`)}</div>`;
  const section = (title, help, body, action = '') => `<section class="team-section"><div class="flex between"><h2>${title}</h2>${action}</div>${help ? `<p class="hint">${help}</p>` : ''}${body}</section>`;
  const rows = (items, empty) => `<div class="agent-list">${items.length ? items.join('') : `<div class="agent-row"><span>${empty}</span></div>`}</div>`;

  // Sign-in page.
  async function loadLogins() {
    try { data.logins = await rest('GET', '/auth/logins'); } catch (_) { data.logins = []; }
  }
  function loginExtras() {
    const invite = fromHash('invite-'), problem = fromHash('signin-');
    const q = invite ? '?invite=' + encodeURIComponent(invite) : '';
    const providers = data.logins.map(l => `<a class="button primary provider" href="/auth/${encodeURIComponent(l.name)}/start${q}">${esc(t('signInWith').replace('{0}', l.display || l.name))}</a>`).join('');
    return `${problem && words['signin.' + problem] ? `<div class="form-error" role="alert">${t('signin.' + problem)}</div>` : ''}${invite ? `<div class="notice">${t('invited')}</div>` : ''}${providers ? `<div class="stack providers">${providers}</div><div class="divider"><span>${t('orToken')}</span></div>` : ''}`;
  }
  // afterSignIn handles what the sign-in left in the address: a linked account, or a refused link.
  function afterSignIn() {
    const problem = fromHash('signin-');
    if (location.hash) history.replaceState(null, '', location.pathname);
    if (problem && words['signin.' + problem]) { ui.page = 'account'; toast(t('signin.' + problem)); }
  }

  function nav() {
    const item = (page, symbol) => `<button class="nav-item ${ui.page === page ? 'active' : ''}" data-action="page" data-page="${page}" ${ui.page === page ? 'aria-current="page"' : ''}>${icon(symbol)}${t(page)}</button>`;
    return `<span class="nav-label">${t('team')}</span>${item('projects', 'tasks')}${item('account', 'user')}${isAdmin() ? item('admin', 'shield') : ''}`;
  }

  // enter loads what page needs before it renders.
  async function enter(page) {
    const loads = [rest('GET', '/api/users').then(v => data.users = v)];
    if (page === 'machines') loads.push(rest('GET', '/api/machines').then(v => data.creds = v));
    if (page === 'account') loads.push(rest('GET', '/api/tokens').then(v => data.tokens = v), rest('GET', '/api/identities').then(v => data.identities = v), loadLogins(), Tree.loadWebhook());
    if (page === 'admin' && isAdmin()) loads.push(rest('GET', '/api/admits').then(v => data.admits = v), rest('GET', '/api/audit').then(v => data.audit = v));
    try { await Promise.all(loads); } catch (error) { toast(errorText(error)); }
    if (ui.page === page) renderPage();
  }

  function render() {
    const el = document.querySelector('#page-content'); if (!el) return;
    el.innerHTML = ({projects: projectsPage, account: accountPage, admin: adminPage})[ui.page]();
  }

  function projectsPage() {
    const list = projects();
    const cards = list.map(p => {
      const members = Object.entries(p.members || {}).sort((a, b) => userName(a[0]).localeCompare(userName(b[0])));
      const memberRows = [`<div class="agent-row"><strong>${esc(userName(p.owner))}</strong><span>${t('role.owner')}</span></div>`,
        ...members.map(([id, role]) => `<div class="agent-row"><strong>${esc(userName(id))}</strong>${manages(p)
          ? `<select data-team-role="${esc(p.id)}" data-user="${esc(id)}" aria-label="${t('role')}">${roles.map(r => `<option value="${r}" ${role === r ? 'selected' : ''}>${t('role.' + r)}</option>`).join('')}</select>${button('team-remove-member', t('removeMember'), `data-project="${esc(p.id)}" data-user="${esc(id)}"`, 'quiet danger')}`
          : `<span>${t('role.' + role)}</span>`}</div>`)];
      return `<article class="machine-card stack"><div class="flex between"><div><h2>${esc(p.name)}</h2><span class="mono muted">${esc(p.id)}</span></div>${badgeText(t('role.' + (p.owner === ui.me?.id ? 'owner' : myRole(p) || 'reader')))}</div>
        <div class="stack"><span class="meta-label">${t('members')}</span>${rows(memberRows, '')}</div>
        ${manages(p) ? `<div class="flex">${button('team-add-member', t('addMember'), `data-project="${esc(p.id)}"`)}${button('team-edit-project', t('editProject'), `data-project="${esc(p.id)}"`, 'quiet')}${button('tree-project', t('projectSettings'), `data-project="${esc(p.id)}"`, 'quiet')}</div>` : ''}</article>`;
    });
    return `<header class="page-heading"><div><h1>${t('projects')}</h1><p class="page-subtitle">${t('projectsSubtitle')}</p></div>${isAdmin() ? button('team-new-project', `${icon('plus')}${t('newProject')}`, ui.online ? '' : 'disabled', 'primary') : ''}</header>
      <div class="machine-page">${list.length ? `<div class="machine-grid">${cards.join('')}</div>` : statePanel('tasks', t('noProjects'), t('noProjectsHelp'))}</div>`;
  }
  const badgeText = text => `<span class="status"><span class="status-icon" aria-hidden="true">·</span>${esc(text)}</span>`;

  function accountPage() {
    const me = ui.me || {};
    const profile = rows([['name', me.name], ['username', me.username], ['email', me.email], ['role', t('role.' + me.role)]].filter(([, v]) => v)
      .map(([k, v]) => `<div class="agent-row"><strong>${t(k === 'name' ? 'projectName' : k === 'role' ? 'role' : k === 'email' ? 'admit.email' : 'user')}</strong><span class="mono">${esc(v)}</span></div>`), '');
    const ids = rows(data.identities.map(i => `<div class="agent-row"><strong>${esc(i.provider)}</strong><span class="mono">${esc(i.username || i.email || i.subject)}</span>${i.email && i.username ? `<span>${esc(i.email)}</span>` : ''}</div>`), t('nobody'));
    const link = data.logins.map(l => `<a class="button" href="/auth/${encodeURIComponent(l.name)}/start?link=1">${t('linkAccount')} ${esc(l.display || l.name)}</a>`).join('');
    const tokens = rows(data.tokens.map(c => `<div class="agent-row"><strong>${esc(c.kind === 'web' ? t('kind.web') : c.name)}</strong><span>${t('kind.' + c.kind)}</span><span>${t('created')} ${when(c.created)}</span><span>${t('lastUsed')} ${when(c.last_used)}</span>${c.current ? `<span>${t('thisBrowser')}</span>` : button('team-revoke', t('revoke'), `data-id="${esc(c.id)}" data-kind="tokens"`, 'quiet danger')}</div>`), t('nobody'));
    return `<header class="page-heading"><div><h1>${t('account')}</h1><p class="page-subtitle">${esc(me.name || '')}</p></div></header>
      <div class="machine-page stack">${section(t('profile'), '', profile)}${section(t('identities'), t('linkHelp'), ids + (link ? `<div class="flex mt-12">${link}</div>` : ''))}
      ${section(t('tokensTitle'), t('tokensHelp'), tokens, button('team-new-token', `${icon('plus')}${t('newToken')}`))}${Tree.account()}</div>`;
  }

  function adminPage() {
    if (!isAdmin()) return statePanel('error', t('forbidden'), '');
    const users = rows(data.users.map(u => `<div class="agent-row"><strong>${esc(u.name)}</strong><span class="mono">${esc(u.username || u.email || u.id)}</span>${u.id === ui.me?.id || u.id === 'local'
      ? `<span>${t('role.' + u.role)}</span>`
      : `<select data-team-user-role="${esc(u.id)}" aria-label="${t('role')}">${['member', 'admin'].map(r => `<option value="${r}" ${u.role === r ? 'selected' : ''}>${t('role.' + r)}</option>`).join('')}</select>${button('team-disable', t(u.disabled ? 'enable' : 'disable'), `data-id="${esc(u.id)}" data-disabled="${u.disabled ? '' : '1'}"`, u.disabled ? 'quiet' : 'quiet danger')}${u.disabled ? '' : button('tree-offboard', t('offboard'), `data-id="${esc(u.id)}"`, 'quiet danger')}`}${u.disabled ? `<span>${t('disabled')}</span>` : ''}</div>`), t('nobody'));
    const admits = rows(data.admits.map(a => `<div class="agent-row"><strong>${t('admit.' + a.kind)}</strong><span class="mono">${esc(a.value)}</span><span>${t('role.' + a.role)}</span>${button('team-remove-admit', t('removeMember'), `data-kind="${esc(a.kind)}" data-value="${esc(a.value)}"`, 'quiet danger')}</div>`), t('nobody'));
    const addAdmit = `<form id="team-admit-form" class="form-grid mt-12"><select name="kind" aria-label="${t('value')}">${['email', 'domain', 'login'].map(k => `<option value="${k}">${t('admit.' + k)}</option>`).join('')}</select><input name="value" required placeholder="corp.example" aria-label="${t('value')}"><select name="role" aria-label="${t('role')}"><option value="member">${t('role.member')}</option><option value="admin">${t('role.admin')}</option></select><button type="submit">${t('addAdmit')}</button></form>`;
    const invite = `<form id="team-invite-form" class="flex"><select name="role" aria-label="${t('role')}"><option value="member">${t('role.member')}</option><option value="admin">${t('role.admin')}</option></select><button type="submit">${t('makeInvite')}</button></form>`;
    const audit = rows(data.audit.map(e => `<div class="agent-row"><span class="mono">${date(e.at)}</span><strong>${esc(e.kind)}</strong><span>${esc(e.actor ? userName(e.actor) : '—')}</span><span class="mono">${esc(e.detail || '')}</span><span class="mono">${esc(e.ip || '')}</span></div>`), t('nobody'));
    return `<header class="page-heading"><div><h1>${t('admin')}</h1></div></header>
      <div class="machine-page stack">${section(t('users'), '', users)}${section(t('admits'), t('admitsHelp'), admits + addAdmit)}${section(t('invite'), '', invite)}${section(t('audit'), '', audit)}</div>`;
  }

  // Machines page additions.
  function machineHeader() { return button('team-add-machine', `${icon('plus')}${t('addMachine')}`, ui.online ? '' : 'disabled', 'primary'); }
  function machineCard(m) {
    const share = ui.state.shares?.[m.name], mine = isAdmin() || m.owner === ui.me?.id;
    const whom = share ? [...(share.users || []).map(userName), ...(share.projects || []).map(id => ui.state.projects?.[id]?.name || id)] : [];
    return `<div class="team-machine"><div><span class="meta-label">${t('ownedBy')}</span><span>${esc(m.owner ? userName(m.owner) : '—')}</span></div><div><span class="meta-label">${t('share')}</span><span>${whom.length ? esc(whom.join(', ')) : t('notShared')}</span></div>${mine && m.owner ? button('team-share', t('share'), `data-machine="${esc(m.name)}"`, 'quiet') : ''}</div>`;
  }
  function machineCreds() {
    if (!data.creds.length) return '';
    return section(t('machineTokens'), t('rebindHelp'), rows(data.creds.map(c => `<div class="agent-row"><strong class="mono">${esc(c.name)}</strong><span>${esc(userName(c.owner))}</span><span>${t('boundTo')} ${c.host ? `<code>${esc(c.host)}</code>` : t('unbound')}</span><span>${t('lastUsed')} ${when(c.last_used)}</span>${button('team-rebind', t('rebind'), `data-id="${esc(c.id)}"`, 'quiet')}${button('team-revoke', t('revoke'), `data-id="${esc(c.id)}" data-kind="machines"`, 'quiet danger')}</div>`), ''));
  }

  // Dialogs.
  const footer = label => `<footer class="modal-footer">${button('close-modal', t('cancel'))}<button type="submit" class="primary">${label}</button></footer>`;
  const userOptions = (value, skip = []) => data.users.filter(u => !u.disabled && !skip.includes(u.id)).map(u => `<option value="${esc(u.id)}" ${u.id === value ? 'selected' : ''}>${esc(u.name)}${u.username ? ' · ' + esc(u.username) : ''}</option>`).join('');
  function showSecret(title, body) {
    showModal('team-secret', title, `<div class="modal-body stack">${body}</div>`, `<footer class="modal-footer">${button('close-modal', t('close'), 'autofocus')}</footer>`, true);
  }
  function newProject() {
    showModal('team-form', t('newProject'), `<form id="team-project-form" data-command="${commandID()}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div><label>${t('projectName')}<input name="name" required autofocus></label><label>${t('projectID')}<input name="slug" class="mono" pattern="[a-z0-9][a-z0-9_\\-]{0,31}"><small>${t('projectIDHint')}</small></label><label>${t('owner')}<select name="owner">${userOptions(ui.me?.id)}</select></label></div>${footer(t('make'))}</form>`, '');
  }
  function editProject(id) {
    const p = ui.state.projects[id]; if (!p) return;
    const people = [p.owner, ...Object.keys(p.members || {}).filter(u => p.members[u] === 'participant')];
    showModal('team-form', t('editProject'), `<form id="team-project-form" data-id="${esc(id)}" data-command="${commandID()}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div><label>${t('projectName')}<input name="name" required value="${esc(p.name)}" autofocus></label><label>${t('owner')}<select name="owner">${people.map(u => `<option value="${esc(u)}" ${u === p.owner ? 'selected' : ''}>${esc(userName(u))}</option>`).join('')}</select></label></div>${footer(t('save'))}</form>`, '');
  }
  function addMember(id) {
    const p = ui.state.projects[id]; if (!p) return;
    showModal('team-form', t('addMember'), `<form id="team-member-form" data-project="${esc(id)}" data-command="${commandID()}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div><label>${t('user')}<select name="user" required autofocus>${userOptions('', [p.owner, ...Object.keys(p.members || {})])}</select></label><label>${t('role')}<select name="role">${roles.map(r => `<option value="${r}">${t('role.' + r)}</option>`).join('')}</select></label></div>${footer(t('add'))}</form>`, '');
  }
  function share(machine) {
    const m = ui.machines.find(x => x.name === machine), s = ui.state.shares?.[machine] || {};
    const check = (name, value, label, on) => `<label class="choice"><input type="checkbox" name="${name}" value="${esc(value)}" ${on ? 'checked' : ''}>${esc(label)}</label>`;
    const people = data.users.filter(u => !u.disabled && u.id !== m?.owner).map(u => check('users', u.id, u.name, (s.users || []).includes(u.id))).join('');
    const projs = projects().map(p => check('projects', p.id, p.name, (s.projects || []).includes(p.id))).join('');
    showModal('team-form', `${t('shareTitle')} · ${esc(machine)}`, `<form id="team-share-form" data-machine="${esc(machine)}" data-command="${commandID()}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div><p class="hint">${t('shareHelp')}</p>
      <fieldset class="stack"><legend>${t('users')}</legend>${people || `<span class="muted">${t('nobody')}</span>`}</fieldset><fieldset class="stack"><legend>${t('projects')}</legend>${projs || `<span class="muted">${t('nobody')}</span>`}</fieldset>
      <label class="choice"><input type="checkbox" name="approve" value="1" ${s.approve ? 'checked' : ''}>${t('approve')}</label></div>${footer(t('saveShare'))}</form>`, '', true);
  }
  function nameForm(id, title, label, hint) {
    showModal('team-form', title, `<form id="${id}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div><label>${label}<input name="name" required autofocus class="mono">${hint ? `<small>${hint}</small>` : ''}</label></div>${footer(t('make'))}</form>`, '');
  }
  function confirmRevoke(id, kind) {
    showModal('team-form', t('revokeTitle'), `<form id="team-revoke-form" data-id="${esc(id)}" data-kind="${esc(kind)}"><div class="modal-body"><div class="form-error" role="alert" hidden></div><p>${t('revokeHelp')}</p></div><footer class="modal-footer">${button('close-modal', t('cancel'), 'autofocus')}<button type="submit" class="primary danger">${t('revoke')}</button></footer></form>`, '');
  }

  // click answers the page's team buttons; false when action is not one of them.
  async function click(action, el) {
    const d = el.dataset;
    switch (action) {
      case 'team-new-project': newProject(); break;
      case 'team-edit-project': editProject(d.project); break;
      case 'team-add-member': addMember(d.project); break;
      case 'team-remove-member': await setMember(d.project, d.user, ''); break;
      case 'team-share': share(d.machine); break;
      case 'team-add-machine': nameForm('team-machine-form', t('addMachine'), t('machineName'), t('machineNameHint')); break;
      case 'team-new-token': nameForm('team-token-form', t('newToken'), t('tokenName'), ''); break;
      case 'team-revoke': confirmRevoke(d.id, d.kind); break;
      case 'team-rebind': await rest('POST', '/api/machines/rebind', {id: d.id}); toast(t('changeSaved')); await enter('machines'); break;
      case 'team-disable': await rest('POST', '/api/users', {id: d.id, disabled: !!d.disabled}); await enter('admin'); break;
      case 'team-remove-admit': await rest('DELETE', '/api/admits', {kind: d.kind, value: d.value}); await enter('admin'); break;
      case 'team-copy': copy(d.value); break;
      default: return false;
    }
    return true;
  }
  function copy(value) {
    navigator.clipboard?.writeText(value).then(() => toast(t('copied')), () => {
      const el = document.querySelector('#secret-value'); if (el) getSelection().selectAllChildren(el);
    });
  }
  async function setMember(project, user, role) {
    const p = await api.projectMember({project, user, role}, {command_id: commandID()});
    if (p) ui.state.projects[p.id] = p;
    renderPage(); toast(t('memberSaved'));
  }
  async function change(el) {
    if (el.dataset.teamRole) { await setMember(el.dataset.teamRole, el.dataset.user, el.value); return true; }
    if (el.dataset.teamUserRole) { await rest('POST', '/api/users', {id: el.dataset.teamUserRole, role: el.value}); toast(t('changeSaved')); await enter('admin'); return true; }
    return false;
  }

  // submit answers the team forms; false when form is not one of them.
  async function submit(form) {
    const f = Object.fromEntries(new FormData(form)), command = {command_id: form.dataset.command};
    switch (form.getAttribute('id')) {
      case 'team-project-form': {
        const p = form.dataset.id
          ? await api.projectEdit({id: form.dataset.id, name: f.name.trim(), owner: f.owner}, command)
          : await api.projectCreate({id: f.slug || undefined, name: f.name.trim(), owner: f.owner}, command);
        ui.state.projects[p.id] = p; closeModal(true); renderPage(); toast(t('projectSaved')); break;
      }
      case 'team-member-form': closeModal(true); await setMember(form.dataset.project, f.user, f.role); break;
      case 'team-share-form': {
        const fd = new FormData(form);
        const s = await api.machineShare({machine: form.dataset.machine, users: fd.getAll('users'), projects: fd.getAll('projects'), approve: fd.has('approve')}, command);
        ui.state.shares ||= {};
        if ((s.users || []).length || (s.projects || []).length) ui.state.shares[s.machine] = s; else delete ui.state.shares[s.machine];
        closeModal(true); renderPage(); toast(t('shareSaved')); break;
      }
      case 'team-machine-form': {
        const v = await rest('POST', '/api/machines', {name: f.name.trim()});
        showSecret(t('addMachine'), secretBox(t('machineAdded'), v.token) + `<p>${t('machineCommand')}</p><code class="secret">${esc(v.command)}</code>`);
        await enter('machines'); await refreshMachines(); break;
      }
      case 'team-token-form': {
        const v = await rest('POST', '/api/tokens', {name: f.name.trim()});
        showSecret(t('newToken'), secretBox(t('tokenMade'), v.token)); await enter('account'); break;
      }
      case 'team-revoke-form':
        await rest('DELETE', '/api/' + form.dataset.kind, {id: form.dataset.id}); closeModal(true); await enter(form.dataset.kind === 'tokens' ? 'account' : 'machines'); break;
      case 'team-admit-form': await rest('POST', '/api/admits', f); form.reset(); await enter('admin'); break;
      case 'team-invite-form': {
        const v = await rest('POST', '/api/invites', f);
        showSecret(t('invite'), secretBox(t('inviteMade'), v.url)); break;
      }
      default: return false;
    }
    return true;
  }

  function projectField(value) {
    const mine = projects().filter(p => myRole(p) === 'participant' || isAdmin());
    if (!mine.length && !value) return '';
    return `<label>${t('project')}<select name="project"><option value="">${t('noProject')}</option>${mine.map(p => `<option value="${esc(p.id)}" ${p.id === value ? 'selected' : ''}>${esc(p.name)}</option>`).join('')}</select></label>`;
  }

  return {pages, rest, name: userName, users: () => data.users, loadLogins, loginExtras, afterSignIn, nav, enter, render, machineHeader, machineCard, machineCreds, click, change, submit, projectField};
})();
