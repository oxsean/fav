'use strict';
// tree.js holds the pages of task trees and what runs them: where each task stands (Fold.situation), subtasks and
// dependencies, starting a subtree, the inbox of what waits for you, agent definitions and project settings. It runs on
// app.js's and team.js's helpers, which exist by the time anything here runs.
const treeWords = {
  backlog: ['待办', 'Backlog'], inbox: ['等你', 'Needs you'], agents: ['Agent 定义', 'Agents'],
  'sit.running': ['运行中', 'Running'], 'sit.queued': ['排队', 'Queued'], 'sit.waiting': ['等你', 'Needs you'],
  'why.after': ['等前置任务', 'Waits for what comes first'], 'why.children': ['等子任务', 'Waits for its subtasks'],
  'why.slot': ['等机器空位', 'Waits for a machine slot'], 'why.ready': ['即将派发', 'About to be dispatched'],
  'why.completing': ['即将完成', 'About to be done'], 'why.accept': ['等验收', 'To be accepted'],
  'why.dispatch': ['等派发', 'To be dispatched'], 'why.after_canceled': ['前置任务已取消', 'A task it comes after was canceled'],
  'why.held': ['派不出去', 'Cannot be dispatched'], 'why.ended': ['运行结束，待确认完成', 'Its run ended; mark it done'],
  'why.asked': ['在问你', 'Asks you'], 'why.permission': ['等你批准', 'Waits for your approval'],
  'why.unknown': ['结局不明', 'Its end is unknown'], 'why.failed': ['运行失败', 'Its run failed'], 'why.exited': ['非零退出', 'It exited with an error'],
  'why.stopped': ['已停止', 'Stopped'], 'why.canceled': ['已取消', 'Canceled'], 'why.abandoned': ['已放弃', 'Abandoned'],
  'why.access_revoked': ['派发人已无权使用这台机器', 'Its dispatcher lost access to the machine'],
  start: ['开始', 'Start'], startAgain: ['重新开始', 'Start again'], started: ['已开始，按依赖自动派发', 'Started: it dispatches by its dependencies'],
  parent: ['父任务', 'Parent'], noParent: ['无（顶层任务）', 'None (a top-level task)'], after: ['先完成', 'Comes after'],
  subtasks: ['子任务', 'Subtasks'], acceptance: ['验收标准', 'Acceptance criteria'], acceptanceHint: ['每行一条。', 'One per line.'],
  approver: ['验收人', 'Approver'], sameAsOwner: ['同负责人', 'Same as the owner'], taskOwner: ['负责人', 'Owner'],
  keepBacklog: ['先放进待办，不自动派发', 'Keep it in the backlog; nothing dispatches it'], moveTask: ['调整位置', 'Move'],
  moved: ['位置已调整', 'Moved'], heldBecause: ['原因', 'Why'], inboxEmpty: ['没有等你的事', 'Nothing waits for you'],
  inboxHelp: ['你负责、验收或派发的任务，需要你处理时出现在这里。', 'Tasks you own, accept or dispatched appear here when they need you.'],
  openTask: ['打开任务', 'Open task'], newAgent: ['新建定义', 'New definition'], editAgent: ['编辑定义', 'Edit definition'],
  agentsHelp: ['定义决定谁来做：类型、模型、强度、权限和说明书。Markdown 加 YAML frontmatter，和 Claude Code 的 subagent 同格式。', 'A definition says who does the work: type, model, effort, permissions and instructions. Markdown with a YAML front matter, like Claude Code subagents.'],
  noAgents: ['还没有定义', 'No definitions yet'], definition: ['定义', 'Definition'], shareAgent: ['分享定义', 'Share definition'],
  shareAll: ['所有人可用', 'Everyone may use it'], shareView: ['被分享的人也能查看内容', 'Those it is shared with may read it'],
  removeAgent: ['删除定义', 'Remove definition'], removeAgentHelp: ['已派发的 run 不受影响。', 'Runs already dispatched keep it.'],
  agentSaved: ['定义已保存', 'Definition saved'], readonlyDef: ['只分享给你使用，不能查看内容。', 'Shared with you for use, not for reading.'],
  giveToProject: ['归属', 'Owner'], me: ['我', 'Me'], projectSettings: ['项目设置', 'Project settings'],
  context: ['项目说明', 'Project context'], contextHint: ['每个 run 的任务书开头都会带上。', 'Put at the head of every run\'s brief.'],
  repos: ['仓库', 'Repositories'], reposHint: ['每行一个：名称 远端 基准分支 机器=目录 …', 'One per line: name remote base machine=dir …'],
  implementAgent: ['实现 agent', 'Implementing agent'], reviewAgent: ['评审 agent', 'Review agent'], testAgent: ['测试 agent', 'Test agent'],
  plannerAgent: ['拆解 agent', 'Planner agent'], checkHook: ['check hook（阶段结束后运行）', 'check hook (runs after a stage)'],
  setupHook: ['setup hook（worktree 建好后运行）', 'setup hook (runs once the worktree is made)'], settingsSaved: ['项目设置已保存', 'Project settings saved'],
  webhook: ['个人 webhook', 'Personal webhook'], webhookHint: ['有事等你时 POST 一段 JSON（含 text 字段，适配 ntfy、Slack、企业微信）。', 'When something needs you it receives a JSON POST (with a text field for ntfy, Slack and the like).'],
  browserNotify: ['浏览器通知', 'Browser notifications'], enableNotify: ['开启', 'Enable'], notifyOn: ['已开启', 'On'],
  notifyBlocked: ['浏览器拒绝了通知', 'The browser blocks notifications'], offboard: ['交接并停用', 'Hand over and disable'],
  offboardHelp: ['他负责的项目和没有项目的任务交给下面这个人，项目里的任务交给项目负责人；他的机器不再对别人开放，他的所有凭据立即失效。', 'Their projects and tasks outside projects go to the person below, their project tasks to each project\'s owner; their machines close to everyone else and every credential of theirs ends now.'],
  handTo: ['交给', 'Hand to'], offboarded: ['已交接并停用', 'Handed over and disabled'],
};

const Tree = (() => {
  const pages = ['inbox', 'agents'];
  const data = {inbox: [], defs: [], webhook: '', seenInbox: null};
  const finished = s => s === 'done' || s === 'canceled';
  const sit = task => Fold.situation(ui.state, task);
  const whyText = s => s.reason ? (words['why.' + s.reason] ? t('why.' + s.reason) : s.reason) : '';

  function sitBadge(task) {
    const s = sit(task);
    if (['backlog', 'done', 'canceled', 'running'].includes(s.kind) || s.reason === 'dispatch') return '';
    const icon = {running: '●', queued: '◷', waiting: '!'}[s.kind] || '·';
    return `<span class="status sit-${esc(s.kind)}"><span class="status-icon" aria-hidden="true">${icon}</span>${esc(whyText(s) || t('sit.' + s.kind))}</span>`;
  }

  // order puts subtasks under their parents (depth counts from 0) when tasks holds whole trees; the rest stay flat.
  function order(tasks) {
    const ids = new Set(tasks.map(x => x.id)), out = [], seen = new Set();
    const kids = id => tasks.filter(x => x.parent === id);
    const walk = (x, depth) => { if (seen.has(x.id)) return; seen.add(x.id); out.push({task: x, depth}); for (const k of kids(x.id)) walk(k, depth + 1); };
    for (const x of tasks) if (!x.parent || !ids.has(x.parent)) walk(x, 0);
    return out;
  }

  function detail(task) {
    const s = sit(task), tasks = ui.state.tasks, kids = Object.values(tasks).filter(x => x.parent === task.id);
    const link = id => tasks[id] ? `<button type="button" class="link-button" data-action="select-task" data-id="${esc(id)}">${esc(tasks[id].title)}</button>` : `<code>${esc(id)}</code>`;
    const rows = [];
    if (!finished(task.status) && s.reason !== 'dispatch') rows.push(`<div><span class="meta-label">${t('sit.' + (s.kind === 'backlog' ? 'queued' : s.kind))}</span><span class="meta-value">${task.status === 'backlog' ? t('backlog') : esc(whyText(s))}${s.reason === 'held' && task.held ? ` · <code>${esc(task.held)}</code>` : ''}</span></div>`);
    if (task.parent) rows.push(`<div><span class="meta-label">${t('parent')}</span><span class="meta-value">${link(task.parent)}</span></div>`);
    if ((task.after || []).length) rows.push(`<div><span class="meta-label">${t('after')}</span><span class="meta-value">${task.after.map(link).join(' · ')}</span></div>`);
    if (task.owner && Team.name) rows.push(`<div><span class="meta-label">${t('taskOwner')}</span><span class="meta-value">${esc(Team.name(task.owner))}${task.approver && task.approver !== task.owner ? ` · ${t('approver')} ${esc(Team.name(task.approver))}` : ''}</span></div>`);
    const acc = (task.acceptance || []).length ? `<div class="tree-block"><span class="meta-label">${t('acceptance')}</span><ul class="plain">${task.acceptance.map(a => `<li>${esc(a)}</li>`).join('')}</ul></div>` : '';
    const sub = kids.length ? `<div class="tree-block"><span class="meta-label">${t('subtasks')}</span>${kids.map(k => `<div class="flex">${link(k.id)}${badge(k.status)}${sitBadge(k)}</div>`).join('')}</div>` : '';
    const can = ui.online && !finished(task.status);
    const actions = can ? `<div class="task-actions">${button('tree-start', `${icon('play')}${t(task.auto ? 'startAgain' : 'start')}`, `data-id="${esc(task.id)}"`, task.status === 'backlog' ? 'primary' : 'quiet')}${button('tree-move', t('moveTask'), `data-id="${esc(task.id)}"`, 'quiet')}</div>` : '';
    return `${rows.length ? `<div class="metadata">${rows.join('')}</div>` : ''}${acc}${sub}${actions}`;
  }

  // formFields are a task form's tree fields; a new task may go under any open task, whose project it then joins.
  function formFields(task, edit) {
    const others = Object.values(ui.state.tasks).filter(x => !finished(x.status)).sort((a, b) => (a.project || '').localeCompare(b.project || '') || a.title.localeCompare(b.title));
    const label = x => esc(x.project ? `${ui.state.projects?.[x.project]?.name || x.project} · ${x.title}` : x.title);
    const tree = edit ? '' : `<div class="form-grid"><label>${t('parent')}<select name="parent"><option value="">${t('noParent')}</option>${others.map(x => `<option value="${esc(x.id)}">${label(x)}</option>`).join('')}</select></label>
      <label class="choice"><input type="checkbox" name="backlog" value="1">${t('keepBacklog')}</label></div>
      ${others.length ? `<fieldset class="stack"><legend>${t('after')}</legend>${others.map(x => `<label class="choice"><input type="checkbox" name="after" value="${esc(x.id)}">${label(x)}</label>`).join('')}</fieldset>` : ''}`;
    return `${tree}<label>${t('acceptance')}<textarea name="acceptance" rows="3">${esc((task.acceptance || []).join('\n'))}</textarea><small>${t('acceptanceHint')}</small></label>`;
  }

  // formData turns the tree fields of form into task.create / task.edit params in data.
  function formData(form, data, edit) {
    const fd = new FormData(form);
    data.acceptance = String(fd.get('acceptance') || '').split('\n').map(x => x.trim()).filter(Boolean);
    delete data.backlog;
    if (edit) { delete data.parent; delete data.after; return; }
    data.after = fd.getAll('after');
    if (!data.parent) delete data.parent;
    if (fd.has('backlog')) data.status = 'backlog';
  }

  function nav() {
    const item = (page, symbol, count) => `<button class="nav-item ${ui.page === page ? 'active' : ''}" data-action="page" data-page="${page}" ${ui.page === page ? 'aria-current="page"' : ''}>${icon(symbol)}${t(page)}${count !== undefined ? `<span class="count" id="count-${page}">${count || ''}</span>` : ''}</button>`;
    return `${item('inbox', 'error', data.inbox.length)}${item('agents', 'user')}`;
  }

  async function enter(page) {
    try {
      if (page === 'inbox') await refreshInbox();
      if (page === 'agents') data.defs = (await api.agentDefList()).defs;
    } catch (error) { toast(errorText(error)); }
    if (ui.page === page) renderPage();
  }

  function render() {
    const el = document.querySelector('#page-content'); if (!el) return;
    el.innerHTML = ui.page === 'inbox' ? inboxPage() : agentsPage();
  }

  function inboxPage() {
    const rows = data.inbox.map(x => `<div class="agent-row"><strong>${esc(x.title)}</strong><span>${esc(whyText({reason: x.reason}))}</span><span>${date(x.since)}</span>${x.project ? `<span class="mono">${esc(x.project)}</span>` : ''}${button('tree-open', t('openTask'), `data-id="${esc(x.task)}"`, 'quiet')}</div>`);
    return `<header class="page-heading"><div><h1>${t('inbox')}</h1><p class="page-subtitle">${t('inboxHelp')}</p></div></header>
      <div class="machine-page">${rows.length ? `<div class="agent-list">${rows.join('')}</div>` : statePanel('check', t('inboxEmpty'), '')}</div>`;
  }

  function agentsPage() {
    const rows = data.defs.map(d => `<div class="agent-row"><strong class="mono">${esc(d.name)}</strong><span>${esc(d.role || '—')}</span><span>${esc(d.provider || '—')} · ${esc(d.model || '—')}${d.effort ? ' · ' + esc(d.effort) : ''}</span><span>${esc(d.owner.startsWith('project:') ? d.owner : Team.name(d.owner))}</span>
      ${d.text ? button('tree-edit-agent', t('edit'), `data-name="${esc(d.name)}"`, 'quiet') : `<span class="muted">${t('readonlyDef')}</span>`}${d.manage ? button('tree-share-agent', t('share'), `data-name="${esc(d.name)}"`, 'quiet') + button('tree-remove-agent', t('removeMember'), `data-name="${esc(d.name)}"`, 'quiet danger') : ''}</div>`);
    return `<header class="page-heading"><div><h1>${t('agents')}</h1><p class="page-subtitle">${t('agentsHelp')}</p></div>${button('tree-new-agent', `${icon('plus')}${t('newAgent')}`, ui.online ? '' : 'disabled', 'primary')}</header>
      <div class="machine-page">${rows.length ? `<div class="agent-list">${rows.join('')}</div>` : statePanel('user', t('noAgents'), '')}</div>`;
  }

  const template = `---\nname: my-agent\ndescription: what it is for\nrole: implement\nprovider: claude\nmodel: sonnet\neffort: high\npermission: acceptEdits\ntools: {deny: [WebFetch]}\n---\nHow it works, what it must not do.\n`;
  const footer = label => `<footer class="modal-footer">${button('close-modal', t('cancel'))}<button type="submit" class="primary">${label}</button></footer>`;

  function agentForm(d) {
    const owners = Object.values(ui.state.projects || {}).filter(p => ui.me?.role === 'admin' || p.owner === ui.me?.id);
    const owner = d ? '' : `<label>${t('giveToProject')}<select name="owner"><option value="">${t('me')}</option>${owners.map(p => `<option value="project:${esc(p.id)}">${esc(p.name)}</option>`).join('')}</select></label>`;
    showModal('tree-form', t(d ? 'editAgent' : 'newAgent'), `<form id="tree-agent-form" data-command="${commandID()}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div>
      <label>${t('definition')}<textarea name="text" rows="18" class="mono" spellcheck="false" autofocus>${esc(d ? d.text : template)}</textarea></label>${owner}
      ${(d?.warnings || []).map(w => `<div class="notice">${esc(w)}</div>`).join('')}</div>${footer(t('save'))}</form>`, '', true);
  }

  function shareAgent(d) {
    const s = d.share || {};
    const check = (name, value, label, on) => `<label class="choice"><input type="checkbox" name="${name}" value="${esc(value)}" ${on ? 'checked' : ''}>${esc(label)}</label>`;
    showModal('tree-form', `${t('shareAgent')} · ${esc(d.name)}`, `<form id="tree-share-form" data-name="${esc(d.name)}" data-command="${commandID()}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div>
      ${check('all', '1', t('shareAll'), s.all)}
      <fieldset class="stack"><legend>${t('users')}</legend>${Team.users().filter(u => !u.disabled && u.id !== d.owner).map(u => check('users', u.id, u.name, (s.users || []).includes(u.id))).join('') || `<span class="muted">${t('nobody')}</span>`}</fieldset>
      <fieldset class="stack"><legend>${t('projects')}</legend>${Object.values(ui.state.projects || {}).map(p => check('projects', p.id, p.name, (s.projects || []).includes(p.id))).join('') || `<span class="muted">${t('nobody')}</span>`}</fieldset>
      ${check('view', '1', t('shareView'), s.view)}</div>${footer(t('saveShare'))}</form>`, '', true);
  }

  function moveTask(task) {
    const others = Object.values(ui.state.tasks).filter(x => x.id !== task.id && !finished(x.status) && (x.project || '') === (task.project || ''));
    showModal('tree-form', `${t('moveTask')} · ${esc(task.title)}`, `<form id="tree-move-form" data-id="${esc(task.id)}" data-command="${commandID()}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div>
      <label>${t('parent')}<select name="parent" autofocus><option value="">${t('noParent')}</option>${others.map(x => `<option value="${esc(x.id)}" ${x.id === task.parent ? 'selected' : ''}>${esc(x.title)}</option>`).join('')}</select></label>
      <fieldset class="stack"><legend>${t('after')}</legend>${others.map(x => `<label class="choice"><input type="checkbox" name="after" value="${esc(x.id)}" ${(task.after || []).includes(x.id) ? 'checked' : ''}>${esc(x.title)}</label>`).join('') || `<span class="muted">${t('nobody')}</span>`}</fieldset></div>${footer(t('save'))}</form>`, '');
  }

  function offboard(user) {
    const heirs = Team.users().filter(u => !u.disabled && u.id !== user).map(u => `<option value="${esc(u.id)}" ${u.id === ui.me?.id ? 'selected' : ''}>${esc(u.name)}</option>`).join('');
    showModal('tree-form', `${t('offboard')} · ${esc(Team.name(user))}`, `<form id="tree-offboard-form" data-id="${esc(user)}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div><p>${t('offboardHelp')}</p>
      <label>${t('handTo')}<select name="to" required autofocus>${heirs}</select></label></div><footer class="modal-footer">${button('close-modal', t('cancel'))}<button type="submit" class="primary danger">${t('offboard')}</button></footer></form>`, '');
  }

  // Project settings: the text forms of repos (one per line) and hooks (argv split on spaces).
  const repoLine = r => [r.name, r.remote || '-', r.base || '-', ...Object.entries(r.dirs || {}).map(([m, d]) => `${m}=${d}`)].join(' ');
  function parseRepos(text) {
    return text.split('\n').map(l => l.trim()).filter(Boolean).map(l => {
      const [name, remote, base, ...dirs] = l.split(/\s+/);
      const r = {name, dirs: {}};
      if (remote && remote !== '-') r.remote = remote;
      if (base && base !== '-') r.base = base;
      for (const d of dirs) { const i = d.indexOf('='); if (i > 0) r.dirs[d.slice(0, i)] = d.slice(i + 1); }
      return r;
    });
  }
  function projectSettings(id) {
    const p = ui.state.projects[id]; if (!p) return;
    const agentOpts = v => `<option value="">—</option>${ui.agents.map(a => `<option value="${esc(a.name)}" ${a.name === v ? 'selected' : ''}>${esc(a.name)}</option>`).join('')}`;
    const roles = p.defaults?.roles || {}, hooks = p.hooks || {};
    showModal('tree-form', `${t('projectSettings')} · ${esc(p.name)}`, `<form id="tree-project-form" data-id="${esc(id)}" data-command="${commandID()}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div>
      <label>${t('context')}<textarea name="context" rows="5" autofocus>${esc(p.context || '')}</textarea><small>${t('contextHint')}</small></label>
      <label>${t('repos')}<textarea name="repos" rows="3" class="mono" spellcheck="false">${esc((p.repos || []).map(repoLine).join('\n'))}</textarea><small>${t('reposHint')}</small></label>
      <div class="form-grid"><label>${t('implementAgent')}<select name="implement">${agentOpts(roles.implement || p.defaults?.agent)}</select></label><label>${t('defaultMachine')}<select name="machine">${machineOptions(p.defaults?.machine || '', true)}</select></label>
      <label>${t('reviewAgent')}<select name="review">${agentOpts(roles.review)}</select></label><label>${t('testAgent')}<select name="test">${agentOpts(roles.test)}</select></label>
      <label>${t('plannerAgent')}<select name="planner">${agentOpts(roles.planner)}</select></label></div>
      <label>${t('setupHook')}<input name="setup" class="mono" value="${esc((hooks.setup || []).join(' '))}"></label>
      <label>${t('checkHook')}<input name="check" class="mono" value="${esc((hooks.check || []).join(' '))}"></label></div>${footer(t('save'))}</form>`, '', true);
  }

  async function click(action, el) {
    const d = el.dataset;
    switch (action) {
      case 'tree-start': await api.taskStart({id: d.id}, {command_id: commandID()}); toast(t('started')); break;
      case 'tree-move': moveTask(ui.state.tasks[d.id]); break;
      case 'tree-open': ui.page = 'tasks'; renderShell(); await selectTask(d.id); break;
      case 'tree-new-agent': agentForm(null); break;
      case 'tree-edit-agent': agentForm(await api.agentDefGet({name: d.name})); break;
      case 'tree-share-agent': shareAgent(await api.agentDefGet({name: d.name})); break;
      case 'tree-remove-agent': await api.agentDefRemove({name: d.name}, {command_id: commandID()}); await enter('agents'); break;
      case 'tree-project': projectSettings(d.project); break;
      case 'tree-notify': askNotify(); break;
      case 'tree-offboard': offboard(d.id); break;
      default: return false;
    }
    return true;
  }

  async function submit(form) {
    const fd = new FormData(form), command = {command_id: form.dataset.command};
    switch (form.getAttribute('id')) {
      case 'tree-move-form':
        await api.taskMove({id: form.dataset.id, parent: fd.get('parent') || '', after: fd.getAll('after')}, command);
        closeModal(true); toast(t('moved')); break;
      case 'tree-agent-form':
        await api.agentDefSave({text: fd.get('text'), owner: fd.get('owner') || undefined}, command);
        closeModal(true); toast(t('agentSaved')); await enter('agents'); await refreshAgents(); break;
      case 'tree-share-form':
        await api.agentDefShare({name: form.dataset.name, share: {users: fd.getAll('users'), projects: fd.getAll('projects'), all: fd.has('all'), view: fd.has('view')}}, command);
        closeModal(true); toast(t('shareSaved')); await enter('agents'); break;
      case 'tree-project-form': {
        const roles = {};
        for (const r of ['implement', 'review', 'test', 'planner']) if (fd.get(r)) roles[r] = fd.get(r);
        const old = ui.state.projects[form.dataset.id] || {}, hooks = {...old.hooks};
        for (const h of ['setup', 'check']) { const a = String(fd.get(h) || '').trim().split(/\s+/).filter(Boolean); if (a.length) hooks[h] = a; else delete hooks[h]; }
        const p = await api.projectEdit({id: form.dataset.id, context: fd.get('context') || '', repos: parseRepos(String(fd.get('repos') || '')),
          defaults: {workflow: old.defaults?.workflow, roles, machine: fd.get('machine') || undefined}, hooks}, command);
        if (p) ui.state.projects[p.id] = p;
        closeModal(true); renderPage(); toast(t('settingsSaved')); break;
      }
      case 'tree-offboard-form':
        await Team.rest('POST', '/api/users/offboard', {user: form.dataset.id, to: fd.get('to')});
        closeModal(true); toast(t('offboarded')); await Team.enter('admin'); break;
      case 'tree-webhook-form':
        await Team.rest('POST', '/api/me/webhook', {url: fd.get('url') || ''}); toast(t('changeSaved')); break;
      default: return false;
    }
    return true;
  }

  async function refreshAgents() {
    try { ui.agents = (await api.agentList()).agents; } catch (_) {}
  }

  // refreshInbox reads what waits for the viewer, shows the count and, when the browser allows, tells of new items.
  async function refreshInbox() {
    const items = (await api.inboxList()).items || [];
    const before = data.seenInbox;
    data.inbox = items;
    data.seenInbox = new Set(items.map(x => x.task + '/' + x.reason));
    const el = document.querySelector('#count-inbox'); if (el) el.textContent = items.length || '';
    if (before && globalThis.Notification?.permission === 'granted' && document.hidden) {
      for (const x of items) if (!before.has(x.task + '/' + x.reason)) new Notification(x.title, {body: whyText({reason: x.reason}), tag: x.task});
    }
  }

  let inboxTimer;
  function stateChanged() {
    clearTimeout(inboxTimer);
    inboxTimer = setTimeout(() => refreshInbox().then(() => { if (ui.page === 'inbox') renderPage(); }, () => {}), 300);
  }

  function askNotify() {
    if (!globalThis.Notification) return toast(t('notifyBlocked'));
    Notification.requestPermission().then(p => { toast(t(p === 'granted' ? 'notifyOn' : 'notifyBlocked')); renderPage(); });
  }

  // account is the account page's part of this file: the personal webhook and browser notifications.
  async function loadWebhook() { try { data.webhook = (await Team.rest('GET', '/api/me/webhook')).url || ''; } catch (_) {} }
  function account() {
    const state = globalThis.Notification?.permission;
    return `<section class="team-section"><h2>${t('webhook')}</h2><p class="hint">${t('webhookHint')}</p><form id="tree-webhook-form" class="flex"><input name="url" class="mono grow" value="${esc(data.webhook)}" placeholder="https://ntfy.sh/…" aria-label="${t('webhook')}"><button type="submit">${t('save')}</button></form></section>
      <section class="team-section"><h2>${t('browserNotify')}</h2>${state === 'granted' ? `<p class="hint">${t('notifyOn')}</p>` : button('tree-notify', t('enableNotify'))}</section>`;
  }

  return {pages, sitBadge, order, detail, formFields, formData, nav, enter, render, click, submit, stateChanged, refreshInbox, account, loadWebhook};
})();
