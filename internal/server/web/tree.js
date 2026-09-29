'use strict';
// tree.js holds the pages of task trees and what runs them: where each task stands (Fold.situation), subtasks and
// dependencies, starting a subtree, the inbox of what waits for you, agent definitions and project settings. It runs on
// app.js's and team.js's helpers, which exist by the time anything here runs.
const treeWords = {
  badAgentStart: ['名字只用小写字母、数字和连字符；模型名不能有空格', 'A name has lowercase letters, digits and hyphens; a model name has no spaces'],
  agentName: ['名字', 'Name'], agentType: ['类型', 'Type'], agentModel: ['模型', 'Model'], agentRole: ['角色', 'Role'], next: ['下一步', 'Next'],
  installedOn: ['已装在 {0}', 'installed on {0}'], notInstalled: ['还没有机器装了它', 'no machine has it yet'], modelDefault: ['留空用 CLI 的默认模型', "Empty: the CLI's default"],
  modelWhy: ['节点只报告 CLI 装没装、登没登录，不报告有哪些模型，所以这里按名字或别名填；下一步可以再改整份定义。', "Nodes report whether each CLI is installed and signed in, not its models, so type a model name or alias; the next step edits the whole definition."],
  stagedOn: ['进入 {0} 阶段', 'Moved to {0}'], stagedBack: ['退回 {0} 阶段（第 {1} 轮）', 'Sent back to {0} (round {1})'],
  backlog: ['待办', 'Backlog'], inbox: ['等你', 'Needs you'], agents: ['Agent 定义', 'Agents'],
  'sit.running': ['运行中', 'Running'], 'sit.queued': ['排队', 'Queued'], 'sit.waiting': ['等你', 'Needs you'],
  'why.after': ['等前置任务', 'Waits for what comes first'], 'why.children': ['等子任务', 'Waits for its subtasks'],
  'why.slot': ['等机器接手', 'Waits for its machine'], 'why.dir': ['等同一目录的运行结束', 'Waits for the run in its directory'], 'why.ready': ['即将派发', 'About to be dispatched'],
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
  allKinds: ['全部', 'All'], 'inbox.answer': ['要回答', 'To answer'], 'inbox.accept': ['待验收或收尾', 'To accept or close'],
  'inbox.trouble': ['出了问题', 'In trouble'], 'inbox.other': ['其他', 'Other'], anyRole: ['任何身份', 'Any role'],
  'as.owner': ['我负责', 'I own it'], 'as.approver': ['我验收', 'I accept it'], 'as.dispatcher': ['我派发', 'I dispatched it'], 'as.admin': ['我是管理员，它的机器已退役', 'I am an admin and its machine retired'],
  whyYou: ['为什么是你：{0}', 'Why you: {0}'], inboxKind: ['按类型', 'By kind'], inboxAs: ['按身份', 'By role'],
  inboxNoneShown: ['没有符合筛选的事', 'Nothing matches the filters'],
  usedByProjects: ['项目 {0} 在用', 'Used by {0}'], usedByTasks: ['{0} 个未完成任务', '{0} open tasks'], unused: ['还没有用到', 'Not used yet'],
  preview: ['预览', 'Preview'], launchPreview: ['运行时启动的命令', 'What a run starts'], launchHelp: ['<dir> 和 <brief> 在运行时换成工作目录和任务书。', '<dir> and <brief> become the run\'s directory and brief.'],
  importAgent: ['导入 .md', 'Import .md'], exportAgent: ['导出 .md', 'Export .md'],
  inboxHelp: ['你负责、验收或派发的任务，需要你处理时出现在这里。', 'Tasks you own, accept or dispatched appear here when they need you.'],
  openTask: ['打开任务', 'Open task'], newAgent: ['新建定义', 'New definition'], editAgent: ['编辑定义', 'Edit definition'],
  agentsHelp: ['定义决定谁来做：类型、模型、强度、权限和说明书。Markdown 加 YAML frontmatter，和 Claude Code 的 subagent 同格式。', 'A definition says who does the work: type, model, effort, permissions and instructions. Markdown with a YAML front matter, like Claude Code subagents.'],
  noAgents: ['还没有定义', 'No definitions yet'], definition: ['定义', 'Definition'], shareAgent: ['分享定义', 'Share definition'],
  shareAll: ['所有人可用', 'Everyone may use it'], shareView: ['被分享的人也能查看内容', 'Those it is shared with may read it'],
  removeAgent: ['删除定义', 'Remove definition'], removeAgentHelp: ['已派发的 run 不受影响。', 'Runs already dispatched keep it.'],
  agentSaved: ['定义已保存', 'Definition saved'], readonlyDef: ['只分享给你使用，不能查看内容。', 'Shared with you for use, not for reading.'],
  giveToProject: ['归属', 'Owner'], me: ['我', 'Me'], projectSettings: ['项目设置', 'Project settings'],
  context: ['项目说明', 'Project context'], contextHint: ['每个 run 的任务书开头都会带上。', 'Put at the head of every run\'s brief.'],
  repos: ['仓库', 'Repositories'], reposHint: ['每行一个：名称 远端 基准分支 [worktrees] 机器=目录 …；写 worktrees 则每个任务在自己的分支和工作区里做，子任务合进父任务的分支', 'One per line: name remote base [worktrees] machine=dir …; with worktrees each task works on its own branch and worktree, and subtasks merge into their parent’s branch'],
  implementAgent: ['实现 agent', 'Implementing agent'], reviewAgent: ['评审 agent', 'Review agent'], testAgent: ['测试 agent', 'Test agent'],
  plannerAgent: ['拆解 agent', 'Planner agent'],
  'hook.setup': ['setup hook（worktree 建好后运行一次）', 'setup hook (runs once the worktree is made)'],
  'hook.before_run': ['before_run hook（每次运行开工前在 worktree 里运行，失败则运行失败）', 'before_run hook (runs in the worktree before each run; failing fails the run)'],
  'hook.check': ['check hook（阶段结束后运行）', 'check hook (runs after a stage)'],
  'hook.cleanup': ['cleanup hook（合并后删 worktree 前运行）', 'cleanup hook (runs before a merged worktree is removed)'],
  checkDirs: ['检查目录', 'Check the directories'], noDirsToCheck: ['仓库还没写目录', 'No repository lists a directory'],
  dirMissing: ['不在，或不在允许的目录里', 'Not there, or outside the allowed directories'], dirGit: ['git 仓库', 'git checkout'], dirNotGit: ['在，但不是 git 仓库', 'There, but not a git checkout'], settingsSaved: ['项目设置已保存', 'Project settings saved'],
  'why.source_changed': ['需求有变化', 'Its issue changed'], 'why.source_closed': ['issue 已在外面关闭', 'Its issue was closed outside tend'],
  source: ['来源', 'Source'], sourceRev: ['第 {0} 版', 'revision {0}'], sourceChanged: ['issue 有新版本，本轮仍按第 {0} 版做。', 'The issue has a newer revision; this round still follows revision {0}.'],
  takeChange: ['采用新版本', 'Take the new revision'], keepScope: ['维持本轮范围', 'Keep this round\'s scope'],
  sourceClosed: ['issue 已在外面关闭。继续做，还是取消这个任务？', 'The issue was closed outside tend. Keep going, or cancel the task?'],
  keepGoing: ['继续做', 'Keep going'], acked: ['已记下', 'Noted'], requirement: ['需求', 'Requirement'],
  trackers: ['工单同步', 'Issue sync'], trackersHelp: ['打了标签的 issue 自动成为这个项目的需求；tend 在 issue 上维护一条进度评论，需求完成后关单。', 'Issues with the label become requirements of this project; tend keeps one progress comment on each and closes it once the requirement is done.'],
  noTrackers: ['还没有绑定仓库', 'No repository bound yet'], bindRepo: ['绑定仓库', 'Bind a repository'], trackerBase: ['地址', 'Address'],
  trackerRepo: ['仓库（owner/name，GitLab 可带子组）', 'Repository (owner/name; GitLab subgroups too)'], trackerToken: ['机器人账号的 token', "The bot account's token"],
  trackerTokenHint: ['只存在 server 上，加密保存；需要读写 issue 的权限。', 'Kept on the server only, encrypted; it needs read and write access to issues.'],
  trackerLabel: ['导入标签', 'Import label'], trackerAssigned: ['也导入指派给项目成员的 issue', 'Also import issues assigned to members of the project'],
  trackerComment: ['维护进度评论', 'Keep a progress comment'], trackerDetail: ['评论里列出子任务（仓库的读者都能看到）', 'List subtasks in it (every reader of the repository sees them)'],
  trackerOnAccept: ['需求完成后', 'Once a requirement is done'], 'accept.close': ['关闭 issue', 'Close the issue'], 'accept.label': ['只打标签', 'Only add a label'],
  trackerSubIssues: ['每个子任务开一个子 issue（GitHub 挂成 sub-issue，其余写明属于哪条）', 'Open a sub-issue for each subtask (a sub-issue on GitHub, a reference elsewhere)'],
  trackerPR: ['需求的分支推送后，等验收或完成时开 PR / MR', 'Open a PR / MR from a requirement\'s pushed branch once it awaits acceptance or is done'],
  subIssue: ['子 issue', 'Sub-issue'], trackerSettings: ['同步设置', 'Sync settings'], trackerScanned: ['上次扫描', 'Last scan'], trackerHook: ['Webhook 地址', 'Webhook address'], trackerSaved: ['同步设置已保存', 'Sync settings saved'],
  trackerPoll: ['轮询间隔（秒）', 'Poll interval (seconds)'], rescan: ['重新同步', 'Sync again'], replaceToken: ['换凭据', 'Replace token'], unbind: ['解绑', 'Unbind'],
  trackerKind: ['工单系统', 'Tracker'],
  'trackerBound.gitea': ['已绑定。要更快收到变化，可在仓库设置 → Webhooks 加这个 Gitea webhook（事件选 Issues 和 Issue Comment），密钥只显示这一次：', 'Bound. For faster updates, add this Gitea webhook under the repository settings → Webhooks (events Issues and Issue Comment); the secret is shown only this once:'],
  'trackerBound.github': ['已绑定。要更快收到变化，可在仓库 Settings → Webhooks 加这个 webhook（Content type 选 application/json，事件选 Issues 和 Issue comments，Secret 填下面的密钥），密钥只显示这一次：', 'Bound. For faster updates, add this webhook under the repository Settings → Webhooks (content type application/json, events Issues and Issue comments, the secret below as Secret); the secret is shown only this once:'],
  'trackerBound.gitlab': ['已绑定。要更快收到变化，可在项目 Settings → Webhooks 加这个 webhook（触发器选 Issues events 和 Comments，Secret token 填下面的密钥），密钥只显示这一次：', 'Bound. For faster updates, add this webhook under the project Settings → Webhooks (triggers Issues events and Comments, the secret below as Secret token); the secret is shown only this once:'],
  trackerOK: ['正常', 'Syncing'], trackerStopped: ['已停：凭据被拒，换凭据后继续', 'Stopped: the token was refused; replace it to go on'],
  trackerPaused: ['限流中，稍后继续', 'Rate limited; it goes on later'], trackerLastOK: ['上次成功', 'Last success'], syncedIssues: ['{0} 条需求', '{0} requirements'],
  failingIssues: ['{0} 条出错', '{0} failing'], tracker_auth: ['token 被拒或权限不够。', 'The token was refused or lacks access.'],
  tracker_repo: ['找不到这个仓库。', 'That repository was not found.'], tracker_unreachable: ['连不上工单系统。', 'The tracker could not be reached.'],
  no_sync: ['这个 server 没有开启同步。', 'This server does not sync trackers.'],
  webhook: ['个人 webhook', 'Personal webhook'], webhookHint: ['有事等你时 POST 一段 JSON（含 text 字段，适配 ntfy、Slack、企业微信）。', 'When something needs you it receives a JSON POST (with a text field for ntfy, Slack and the like).'],
  browserNotify: ['浏览器通知', 'Browser notifications'], enableNotify: ['开启', 'Enable'], notifyOn: ['已开启', 'On'],
  notifyBlocked: ['浏览器拒绝了通知', 'The browser blocks notifications'], offboard: ['交接并停用', 'Hand over and disable'],
  offboardHelp: ['他负责的项目和没有项目的任务交给下面这个人，项目里的任务交给项目负责人；他的机器不再对别人开放，他的所有凭据立即失效。', 'Their projects and tasks outside projects go to the person below, their project tasks to each project\'s owner; their machines close to everyone else and every credential of theirs ends now.'],
  'off.projects': ['他负责的项目：{0}', 'Projects they own: {0}'], 'off.tasks': ['不在项目里、或在这些项目里的未完成任务', 'Open tasks outside projects or in those'],
  'off.projectTasks': ['其他项目里他负责或验收的未完成任务', 'Open tasks they own or accept in other projects'], 'off.defs': ['他的 agent 定义：{0}', 'Their agent definitions: {0}'],
  'off.machines': ['他的机器：{0}', 'Their machines: {0}'], 'off.member': ['项目成员身份', 'Project memberships'], 'off.creds': ['他所有的 token 和登录', 'Every token and sign-in of theirs'],
  'off.heir': ['下面选的接手人', 'the person chosen below'], 'off.closed': ['停止分享并标为已退役；上面没结束的 run 进管理员的「等你」', 'no longer shared, and retired; runs still open there wait for an admin'], 'off.removed': ['移除', 'removed'], 'off.revoked': ['立即失效', 'ended now'],
  'off.audit': ['停用和每一项转移都记进安全审计。以后可以重新启用，吊销的 token 不会恢复。', 'The disabling and every handover go into the audit log. They can be enabled again later; revoked tokens stay revoked.'],
  syncLog: ['同步记录', 'Sync log'], notMirrored: ['没有对应任务', 'No task'], subOf: ['{0} 的子工单', 'sub-issue of {0}'],
  commentWritten: ['评论写于 {0}', 'Comment written {0}'], noComment: ['还没写评论', 'No comment yet'], trackerClosed: ['已关闭', 'closed'], toRead: ['待重读', 'to read again'],
  'sync.sync_pending': ['待同步', 'Sync pending'], 'sync.sync_failed': ['同步失败', 'Sync failed'], syncedAt: ['上次同步 {0}', 'Synced {0}'], syncRetry: ['{0} 重试', 'Retries {0}'],
  previewComment: ['预览评论', 'Preview the comment'], commentFor: ['{0} 的进度评论（现在写会是这样）', 'The progress comment on {0}, as it would be written now'], back: ['返回', 'Back'],
  handTo: ['交给', 'Hand to'], offboarded: ['已交接并停用', 'Handed over and disabled'],
  'why.advance': ['本阶段完成，即将进入下一阶段', 'Its stage is done; it moves on next'], 'why.rework': ['被退回，即将返工', 'Sent back; it goes back next'],
  'why.max_loops': ['退回次数到上限', 'Sent back as often as its workflow allows'], 'why.blocked': ['本阶段没有给出结论', 'Its stage reached no verdict'],
  'why.budget': ['用完了预算', 'Its budget is spent'],
  workflow: ['工作流', 'Workflow'], projectDefault: ['项目默认', "The project's default"], noWorkflow: ['不用工作流（一次一个 run）', 'None (one run at a time)'],
  workflowHint: ['工作流把任务分成阶段：实现、评审或测试、验收；被退回时带着意见返工。', 'A workflow takes a task through stages: implement, review or test, accept; a stage that sends it back says why.'],
  round: ['第 {0} 轮', 'Round {0}'], budget: ['预算', 'Budget'], maxLoops: ['最多退回 {0} 次', 'Sent back at most {0} times'], gateHuman: ['人工验收', 'human gate'],
  gateWaits: ['等 {0} 验收。', 'Waits for {0} to accept it.'], pass: ['通过', 'Pass'], roleHint: ['由哪类 agent 做', 'Done by'], machineHint: ['建议机器', 'Machine hint'], sendBack: ['退回返工', 'Send back'],
  sendBackTitle: ['退回返工', 'Send back'], reworkNotes: ['要改什么', 'What to change'], reworkHint: ['原话带给下一轮的 agent。', 'Passed word for word to the next round.'],
  passed: ['已通过', 'Passed'], sentBack: ['已退回', 'Sent back'], workpad: ['工作记录', 'Workpad'], workpadEmpty: ['还没有记录', 'Nothing yet'],
  'note.message': ['留言', 'message'], 'note.gate': ['验收', 'gate'], 'note.rework': ['退回', 'rework'],
  'verdict.pass': ['通过', 'pass'], 'verdict.rework': ['要返工', 'rework'], 'verdict.blocked': ['无法判断', 'blocked'],
  checkFailed: ['check 失败（exit {0}）', 'check failed (exit {0})'], runEnded: ['结束：{0}', 'ended: {0}'],
  messageTask: ['给这个任务留言', 'Message this task'], send: ['发送', 'Send'],
  'to.run': ['会发进正在运行的 run。', 'Goes into the running run.'], 'to.reply': ['会作为回复，接着运行等你的 run。', 'Goes as the reply that continues the run that waits.'],
  'to.workpad': ['会记进工作记录，下一个阶段的 agent 会看到。', 'Goes on the workpad; the next stage sees it.'], 'to.none': ['现在发不进去：run 还没开始，或者不收消息。', 'Nothing takes a message now: the run has not started, or takes none.'],
  'sent.run': ['已发进 run', 'Sent into the run'], 'sent.reply': ['已回复，run 继续', 'Replied; the run goes on'], 'sent.workpad': ['已记进工作记录', 'Put on the workpad'],
  defaultWorkflow: ['默认工作流', 'Default workflow'], customWorkflows: ['自定义工作流', 'Custom workflows'], newWorkflow: ['新建工作流', 'New workflow'],
  editWorkflow: ['编辑工作流', 'Edit workflow'], workflowSaved: ['工作流已保存', 'Workflow saved'], noCustomWorkflows: ['没有自定义工作流；内置 feature、fix、docs。', 'None; feature, fix and docs are built in.'],
  workflowNameMissing: ['frontmatter 里要有 name。', 'The front matter needs a name.'],
  'why.merge_conflict': ['合进父任务时冲突', 'Merging into its parent conflicted'], 'why.stale': ['分支有了新提交，重新评审', 'Its branch moved on; judged again'],
  'why.setup_failed': ['setup hook 失败', 'The setup hook failed'], 'why.work': ['建不了工作区', 'Its worktree could not be made'],
  branch: ['分支', 'Branch'], mergeable: ['可合并', 'Ready to merge'], mergedIn: ['已合进父任务', 'Merged into its parent'],
  commits: ['{0} 个提交', '{0} commits'], discarded: ['改了 {0} 个文件，已丢弃', '{0} changed files thrown away'],
  conflictIn: ['合并冲突的文件：', 'Files in conflict:'], resolveHint: ['在 {0} 里合并并提交，然后重试。', 'Merge and commit in {0}, then try again.'],
  retryMerge: ['重试合并', 'Merge again'], merging: ['正在合并', 'Merging'], pullRequest: ['PR', 'Pull request'],
  'why.draft': ['拆解草稿等你确认', 'A plan waits for you'], 'why.no_plan': ['拆解没有给出计划', 'Its planner handed in no plan'],
  planTask: ['拆解', 'Plan it'], planning: ['拆解 agent 正在起草', 'A planner is drafting'], draft: ['拆解草稿', 'Plan draft'],
  draftHelp: ['确认之前不会建任何任务；建出来的子任务都在待办里，点「开始」才派发。', 'Nothing is made until you apply it; the subtasks go to the backlog and run once you start them.'],
  questions: ['需要你决定', 'To decide'], answer: ['回答', 'Answer'], answerHint: ['回答后拆解 agent 在同一会话里重出草稿。', 'The planner goes on in its session and drafts again.'],
  applyDraft: ['按草稿建任务', 'Make the subtasks'], discardDraft: ['丢弃草稿', 'Drop the draft'], editDraft: ['编辑草稿', 'Edit the draft'],
  saveDraft: ['保存草稿', 'Save the draft'], addPlanTask: ['加一个任务', 'Add a task'], removePlanTask: ['删除', 'Remove'],
  replanWith: ['补充意见，让拆解 agent 重出草稿', 'Feedback for the planner to draft again'], replan: ['重新拆解', 'Plan again'],
  sourceRev: ['第 {0} 版', 'revision {0}'], planKey: ['键', 'Key'], partOf: ['属于', 'Part of'], size: ['规模', 'Size'], draftSaved: ['草稿已保存', 'Draft saved'],
  applied: ['子任务已建好，放在待办里', 'The subtasks are in the backlog'], draftDropped: ['草稿已丢弃', 'Draft dropped'],
  draftStale: ['需求改过了：这份草稿是按第 {0} 版做的。看过后保存一次，再建任务。', 'The issue changed: this draft was made for revision {0}. Save it once you have checked it, then apply.'],
};

const Tree = (() => {
  const pages = ['inbox', 'agents'];
  const data = {inbox: [], defs: [], webhook: '', seenInbox: null, inboxKind: '', inboxAs: ''};
  const inboxKinds = {answer: ['permission', 'asked'], accept: ['accept', 'ended'], trouble: ['failed', 'unknown', 'merge_conflict']};
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
    const src = task.source ? sourceBlock(task) : task.issue ? `<div class="tree-block"><div class="flex"><span class="meta-label">${t('subIssue')}</span><a href="${esc(task.issue)}" target="_blank" rel="noreferrer noopener">${esc(task.issue)}</a></div>${syncLine(task)}</div>` : '';
    const sub = kids.length ? `<div class="tree-block"><span class="meta-label">${t('subtasks')}</span>${kids.map(k => `<div class="flex">${link(k.id)}${badge(k.status)}${sitBadge(k)}</div>`).join('')}</div>` : '';
    const can = ui.online && !finished(task.status);
    const flow = (task.flow ? flowBlock(task) : '') + workBlock(task) + (task.draft ? draftBlock(task) : '');
    const planning = Object.values(ui.state.runs).some(r => r.task === task.id && r.planner && openStates.has(r.state));
    const mayPlan = can && !kids.length && !task.draft && !planning;
    const actions = can ? `<div class="task-actions">${planning ? `<span class="status sit-running">${t('planning')}</span>` : ''}${mayPlan ? button('tree-plan', t('planTask'), `data-id="${esc(task.id)}"`, 'quiet') : ''}${button('tree-start', `${icon('play')}${t(task.auto ? 'startAgain' : 'start')}`, `data-id="${esc(task.id)}"`, task.status === 'backlog' ? 'primary' : 'quiet')}${button('tree-move', t('moveTask'), `data-id="${esc(task.id)}"`, 'quiet')}</div>` : '';
    return `${rows.length ? `<div class="metadata">${rows.join('')}</div>` : ''}${src}${flow}${acc}${sub}${actions}`;
  }

  const short = h => (h || '').slice(0, 10);
  // workLine is what run r did to its branch, in one line (render.RunWork).
  function workLine(r) {
    const w = r.work, d = r.worked;
    if (!w || w.merge || !d) return '';
    const parts = [];
    if (w.read_only) { if (d.discarded) parts.push(t('discarded').replace('{0}', d.discarded)); }
    else if (d.head) parts.push(`${t('commits').replace('{0}', d.commits || 0)}${d.diffstat ? ' · ' + esc(d.diffstat) : ''}`);
    if (d.pr) parts.push(`<a href="${esc(d.pr)}" target="_blank" rel="noreferrer noopener">${t('pullRequest')}</a>`);
    for (const x of d.warnings || []) parts.push(`<span class="muted">${esc(x)}</span>`);
    return parts.join(' · ');
  }

  // draftBlock is a planner's draft for a task: its tasks as a tree, its questions, and what to do with it.
  function draftBlock(task) {
    const plan = task.draft.plan || {tasks: []}, id = esc(task.id);
    const byKey = Object.fromEntries(plan.tasks.map(x => [x.key, x]));
    const row = x => `<li><strong>${esc(x.title)}</strong>${x.size ? ` <span class="muted">${esc(x.size)}</span>` : ''}${x.workflow ? ` <span class="mono muted">${esc(x.workflow)}</span>` : ''}${(x.after || []).length ? ` <span class="muted">← ${x.after.map(a => esc(byKey[a]?.title || a)).join(', ')}</span>` : ''}
      ${plan.tasks.some(k => k.parent === x.key) ? `<ul class="plain">${plan.tasks.filter(k => k.parent === x.key).map(row).join('')}</ul>` : ''}</li>`;
    const stale = task.source && task.draft.source_rev && task.draft.source_rev !== task.source.rev;
    const asks = (plan.questions || []).length, by = ui.state.runs[task.draft.run];
    const qs = `${asks ? `<div class="notice"><strong>${t('questions')}</strong><ul class="plain">${plan.questions.map(q => `<li>${esc(q)}</li>`).join('')}</ul></div>` : ''}
      ${by?.session && ui.online ? `<form id="tree-answer-form" class="stack" data-run="${esc(by.id)}" data-command="${commandID()}"><div class="form-error" role="alert" hidden></div><label>${t(asks ? 'answer' : 'replanWith')}<textarea name="text" rows="2"></textarea><small>${t('answerHint')}</small></label>${sendHint()}<div class="flex"><button type="submit">${t(asks ? 'answer' : 'replan')}</button></div></form>` : ''}`;
    const made = [by ? `${esc(by.agent)}${by.started_at ? ' · ' + elapsed(by) : ''}${money(by.usage?.cost_usd) ? ' · ' + money(by.usage.cost_usd) : ''}` : '', task.draft.by ? esc(task.draft.by) : '',
      task.source && task.draft.source_rev ? `${task.source.url ? `<a href="${esc(task.source.url)}" target="_blank" rel="noreferrer">#${task.source.number}</a>` : '#' + task.source.number} ${esc(t('sourceRev').replace('{0}', task.draft.source_rev))}` : ''].filter(Boolean).join(' · ');
    return `<div class="tree-block"><span class="meta-label">${t('draft')}</span><p class="hint">${t('draftHelp')}</p>${made ? `<p class="muted mono num">${made}</p>` : ''}${stale ? `<div class="notice">${t('draftStale').replace('{0}', task.draft.source_rev)}</div>` : ''}
      <ul class="plain">${plan.tasks.filter(x => !x.parent).map(row).join('')}</ul>${qs}
      ${ui.online ? `<div class="flex">${button('tree-draft-apply', t('applyDraft'), `data-id="${id}"`, stale ? '' : 'primary')}${button('tree-draft-edit', t('editDraft'), `data-id="${id}"`)}${button('tree-draft-discard', t('discardDraft'), `data-id="${id}"`, 'quiet danger')}</div>` : ''}</div>`;
  }

  // draftForm edits a draft: a row per task.
  function draftForm(task, plan) {
    const keys = plan.tasks.map(x => x.key);
    const opts = (list, v, none) => `${none !== undefined ? `<option value="">${none}</option>` : ''}${list.map(k => `<option value="${esc(k)}" ${k === v ? 'selected' : ''}>${esc(k)}</option>`).join('')}`;
    const rows = plan.tasks.map((x, i) => `<fieldset class="stack plan-row" data-row="${i}"><legend class="mono">${esc(x.key)}</legend><input type="hidden" name="key" value="${esc(x.key)}">
      <div class="form-grid"><label>${t('title')}<input name="title" value="${esc(x.title)}" required></label><label>${t('partOf')}<select name="parent">${opts(keys.filter(k => k !== x.key && !plan.tasks.find(p => p.key === k)?.parent), x.parent, '—')}</select></label>
      <label>${t('workflow')}<select name="workflow"><option value="">${t('projectDefault')}</option>${opts(['none', ...flowNames()], x.workflow || '')}</select></label><label>${t('size')}<select name="size">${opts(['', 'S', 'M', 'L'], x.size || '')}</select></label>
      <label>${t('roleHint')}<select name="role_hint">${opts(['implement', 'review', 'test', 'planner'], x.role_hint || '', '—')}</select></label><label>${t('machineHint')}<select name="machine_hint">${opts([...new Set([...ui.machines.map(m => m.name), ...(x.machine_hint ? [x.machine_hint] : [])])], x.machine_hint || '', '—')}</select></label></div>
      <label>${t('brief')}<textarea name="brief" rows="3">${esc(x.brief || '')}</textarea></label>
      <label>${t('acceptance')}<textarea name="acceptance" rows="2">${esc((x.acceptance || []).join('\n'))}</textarea></label>
      <div class="flex plan-after"><span class="meta-label">${t('after')}</span>${keys.filter(k => k !== x.key).map(k => `<label class="choice"><input type="checkbox" name="after" value="${esc(k)}" ${(x.after || []).includes(k) ? 'checked' : ''}>${esc(k)}</label>`).join('')}</div>
      <div>${button('tree-draft-remove', t('removePlanTask'), `data-row="${i}"`, 'quiet danger')}</div></fieldset>`).join('');
    showModal('tree-form', `${t('editDraft')} · ${esc(task.title)}`, `<form id="tree-draft-form" data-id="${esc(task.id)}" data-rev="${task.rev}" data-command="${commandID()}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div>
      ${rows}<div>${button('tree-draft-add', `${icon('plus')}${t('addPlanTask')}`, `data-id="${esc(task.id)}"`, 'quiet')}</div></div>${footer(t('saveDraft'))}</form>`, '', true);
    data.draft = {task: task.id, questions: plan.questions};
  }

  // draftOf reads the draft form back into a plan.
  function draftOf(form) {
    const tasks = [...form.querySelectorAll('.plan-row')].map(f => {
      const v = n => f.querySelector(`[name="${n}"]`).value.trim();
      const x = {key: v('key'), title: v('title'), brief: v('brief'), parent: v('parent'), workflow: v('workflow'), size: v('size'), role_hint: v('role_hint'), machine_hint: v('machine_hint'),
        acceptance: v('acceptance').split('\n').map(a => a.trim()).filter(Boolean), after: [...f.querySelectorAll('[name="after"]:checked')].map(c => c.value)};
      for (const k of Object.keys(x)) if (x[k] === '' || Array.isArray(x[k]) && !x[k].length) delete x[k];
      return x;
    });
    const keys = new Set(tasks.map(x => x.key));
    for (const x of tasks) { if (x.parent && !keys.has(x.parent)) delete x.parent; if (x.after) x.after = x.after.filter(a => keys.has(a)); }
    return {tasks, questions: data.draft?.questions};
  }

  // workBlock is where a task's work is in git: its branch, what its latest run left there, a merge conflict to resolve.
  function workBlock(task) {
    if (!task.branch) return '';
    const runs = Object.values(ui.state.runs).filter(r => r.task === task.id && r.work).sort((a, b) => (a.seq || 0) - (b.seq || 0));
    const lastWork = runs.filter(r => !r.work.merge && !r.work.read_only && r.worked).at(-1), lastMerge = runs.filter(r => r.work.merge).at(-1);
    const state = task.merged ? `<span class="status exited">${t('mergedIn')}</span>` : task.status === 'done' && !task.parent ? `<span class="status done">${t('mergeable')}</span>` : '';
    let body = `<div class="flex"><span class="meta-label">${t('branch')}</span><code>${esc(task.branch)}</code>${task.head ? `<span class="muted mono">${esc(short(task.head))}</span>` : ''}${state}</div>`;
    if (lastWork) { const line = workLine(lastWork); if (line) body += `<div class="hint">${line}</div>`; }
    if (task.pr) body += `<div class="flex"><span class="meta-label">${t('pullRequest')}</span><a href="${esc(task.pr)}" target="_blank" rel="noreferrer noopener">${esc(task.pr)}</a></div>`;
    if (sit(task).reason === 'merge_conflict' && lastMerge?.worked) {
      body += `<div class="notice">${t('conflictIn')} <code>${(lastMerge.worked.conflict || []).map(esc).join(', ')}</code><br>${t('resolveHint').replace('{0}', `<code>${esc(lastMerge.worked.dir || '')}</code>`)}</div>
        ${ui.online ? `<div class="flex">${button('tree-merge', t('retryMerge'), `data-id="${esc(task.id)}"`, 'primary')}</div>` : ''}`;
    }
    return `<div class="tree-block">${body}</div>`;
  }

  // budgetLine is what a workflow task's runs spent against its budget.
  function budgetLine(task) {
    const b = task.flow.budget;
    if (!b || !b.usd && !b.minutes) return '';
    let usd = 0, minutes = 0;
    for (const r of Object.values(ui.state.runs)) {
      if (r.task !== task.id) continue;
      usd += r.usage?.cost_usd || 0;
      if (r.started_at) minutes += ((r.ended_at ? new Date(r.ended_at) : Date.now()) - new Date(r.started_at)) / 6e4;
    }
    const parts = [b.usd ? `$${usd.toFixed(2)} / $${b.usd}` : '', b.minutes ? `${Math.round(minutes)} / ${b.minutes} min` : ''].filter(Boolean);
    return `<span class="muted num">${t('budget')} ${parts.join(' · ')}</span>`;
  }

  // flowBlock is a workflow task's stages with where it stands, its human gate, its workpad and a message box.
  function flowBlock(task) {
    const f = task.flow, at = f.stages.findIndex(s => s.name === task.stage), open = ui.online && !finished(task.status);
    const stages = f.stages.map((s, i) => `<li class="${i === at && !finished(task.status) ? 'current' : i < at || task.status === 'done' ? 'past' : ''}"><span class="mono">${esc(s.name)}</span><small>${esc(s.gate ? t('gateHuman') : s.agent || s.role || '')}</small></li>`).join('');
    const head = `<div class="flex"><span class="meta-label">${t('workflow')}</span><span class="mono">${esc(task.workflow)}</span><span class="muted">${t('round').replace('{0}', (task.loops || 0) + 1)} · ${t('maxLoops').replace('{0}', f.max_loops || 0)}</span>${budgetLine(task)}</div><ol class="stages">${stages}</ol>`;
    const stage = f.stages[at];
    let gate = '';
    if (open && stage?.gate === 'human') {
      const approver = task.approver || task.owner, mayPass = !approver || approver === ui.me?.id || ui.me?.role === 'admin';
      gate = `<div class="notice">${t('gateWaits').replace('{0}', esc(approver ? Team.name(approver) : t('taskOwner')))}</div><div class="flex">${mayPass ? button('tree-pass', t('pass'), `data-id="${esc(task.id)}"`, 'primary') : ''}${button('tree-rework', t('sendBack'), `data-id="${esc(task.id)}"`)}</div>`;
    }
    return `<div class="tree-block">${head}${gate}${workpad(task)}${open ? messageBox(task) : ''}</div>`;
  }

  // workpad lists what the stages said, oldest first: each stage run's verdict or end, failed checks, and the notes.
  function workpad(task) {
    const items = [];
    for (const r of Object.values(ui.state.runs)) {
      if (r.task !== task.id || !r.stage || openStates.has(r.state)) continue;
      let what = r.verdict ? `<span class="status verdict-${esc(r.verdict.verdict)}">${t('verdict.' + r.verdict.verdict)}</span> ${esc(r.verdict.summary || '')}`
        : r.state === 'exited' && r.exit_code === 0 ? esc(r.last || t('exited')) : esc(t('runEnded').replace('{0}', t(r.state) + (r.reason ? ' · ' + r.reason : '')));
      if (r.checked && r.checked.exit !== 0) what += `<details><summary class="pointer">${t('checkFailed').replace('{0}', r.checked.exit)} <code>${esc(r.checked.argv.join(' '))}</code></summary><pre class="tail">${esc(r.checked.tail || '')}</pre></details>`;
      const work = workLine(r), spent = [r.started_at ? elapsed(r) : '', money(r.usage?.cost_usd)].filter(Boolean).join(' · ');
      items.push({at: r.queued_at, html: `<li><span class="mono">[${esc(r.stage)}]</span> <button type="button" class="link-button mono" data-action="select-run" data-id="${esc(r.id)}">${esc(r.id)}</button> ${what}${work ? ` <span class="hint">· ${work}</span>` : ''}${spent ? ` <span class="muted mono num">· ${spent}</span>` : ''}</li>`});
    }
    for (const n of (task.notes || []).filter(n => n.kind !== 'rework')) items.push({at: n.at, html: `<li><span class="mono">[${esc(n.stage || '')}]</span> <strong>${t('note.' + n.kind)}</strong>${n.by ? ` · ${esc(Team.name(n.by))}` : ''} <span class="pre-line">${esc(n.text)}</span></li>`});
    for (const m of task.stages || []) items.push({at: m.at, html: `<li class="muted"><span class="mono">[${esc(m.stage)}]</span> ${esc(t(m.back ? 'stagedBack' : 'stagedOn').replace('{0}', m.stage).replace('{1}', m.loops || 0))}</li>`});
    items.sort((a, b) => String(a.at).localeCompare(String(b.at)));
    return `<details data-tool="workpad" ${items.length ? 'open' : ''}><summary class="pointer meta-label">${t('workpad')} (${items.length})</summary>${items.length ? `<ol class="plain workpad">${items.map(x => x.html).join('')}</ol>` : `<p class="muted">${t('workpadEmpty')}</p>`}</details>`;
  }

  // messageTo is where a task message goes now, as task.Route decides it.
  function messageTo(task) {
    const runs = Object.values(ui.state.runs).filter(r => r.task === task.id);
    const open = runs.find(r => openStates.has(r.state));
    if (open) {
      const caps = open.caps || {};
      return {to: open.state === 'running' && (caps.steer && open.stream || caps.after) ? 'run' : 'none'};
    }
    const last = runs.filter(r => r.stage === task.stage && r.stage !== 'merge' && (r.seq || 0) > (task.stage_seq || 0)).sort((a, b) => b.seq - a.seq)[0];
    return {to: last && last.session && last.caps?.continue ? 'reply' : 'workpad'};
  }

  function messageBox(task) {
    const where = messageTo(task);
    return `<form id="tree-message-form" class="stack" data-id="${esc(task.id)}" data-command="${commandID()}"><div class="form-error" role="alert" hidden></div>
      <label>${t('messageTask')}<textarea name="text" id="task-message-text" rows="2">${esc(ui.drafts.get('task:' + task.id) || '')}</textarea><small id="message-to">${t('to.' + where.to)}</small></label>${sendHint()}
      <div class="flex"><button type="submit" ${where.to === 'none' ? 'disabled' : ''}>${t('send')}</button></div></form>`;
  }

  function gateForm(task) {
    showModal('tree-form', `${t('sendBackTitle')} · ${esc(task.title)}`, `<form id="tree-gate-form" data-id="${esc(task.id)}" data-rev="${task.rev}" data-command="${commandID()}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div>
      <label>${t('reworkNotes')}<textarea name="notes" rows="6" required autofocus></textarea><small>${t('reworkHint')}</small></label></div>${footer(t('sendBack'))}</form>`, '');
  }

  // sourceBlock is where a requirement comes from, and what its issue asks of someone now.
  function sourceBlock(task) {
    const src = task.source, can = ui.online && !finished(task.status);
    const where = `${esc(src.repo)}#${esc(src.number)}`;
    const head = `<div class="flex"><span class="meta-label">${t('source')}</span>${src.url ? `<a href="${esc(src.url)}" target="_blank" rel="noreferrer noopener">${where}</a>` : where}<span class="muted">${t('sourceRev').replace('{0}', src.rev)}</span></div>`;
    let body = '';
    if (src.pending) {
      body = `<div class="notice">${t('sourceChanged').replace('{0}', src.rev)}</div><details><summary class="pointer">${esc(src.pending.title)}</summary><article class="brief">${markdown(src.pending.text)}</article></details>
        ${can ? `<div class="flex">${button('tree-source', t('takeChange'), `data-id="${esc(task.id)}" data-accept="1"`, 'primary')}${button('tree-source', t('keepScope'), `data-id="${esc(task.id)}"`)}</div>` : ''}`;
    } else if (src.closed && !src.closed_acked && can) {
      body = `<div class="notice">${t('sourceClosed')}</div><div class="flex">${button('tree-source', t('keepGoing'), `data-id="${esc(task.id)}"`)}${button('cancel-task', t('cancelTask'), '', 'quiet')}</div>`;
    }
    return `<div class="tree-block">${head}${syncLine(task)}${body}</div>`;
  }

  const syncs = {};
  // syncLine is how the issue task mirrors syncs, for who manages its project; the states come from the server, at most
  // every half minute, and the detail draws again once they arrive.
  function syncLine(task) {
    const p = ui.state.projects?.[task.project];
    if (!Team.rest || !p || !(ui.me?.role === 'admin' || p.owner === ui.me?.id)) return '';
    const c = syncs[task.project] ||= {at: 0, rows: {}};
    if (!c.loading && Date.now() - c.at > 30000) {
      c.loading = true;
      Team.rest('GET', '/api/trackers/tasks?project=' + encodeURIComponent(task.project))
        .then(rows => { c.rows = Object.fromEntries(rows.map(r => [r.task, r])); }, () => {})
        .finally(() => { c.at = Date.now(); c.loading = false; if (currentTask()?.project === task.project) renderDetail(); });
    }
    const st = c.rows[task.id];
    if (!st) return '';
    const when = [st.synced ? t('syncedAt').replace('{0}', date(st.synced)) : '', st.next ? t('syncRetry').replace('{0}', date(st.next)) : ''].filter(Boolean).join(' · ');
    const mark = st.state === 'ok' ? '' : `<span class="status ${st.state === 'sync_failed' ? 'failed' : 'queued'}">${t('sync.' + st.state)}</span>`;
    return `<div class="flex">${mark}${when ? `<span class="muted">${when}</span>` : ''}${st.error ? `<code class="form-error">${esc(st.error)}</code>` : ''}</div>`;
  }

  // formFields are a task form's tree fields; a new task may go under any open task, whose project it then joins.
  function formFields(task, edit) {
    const others = Object.values(ui.state.tasks).filter(x => !finished(x.status)).sort((a, b) => (a.project || '').localeCompare(b.project || '') || a.title.localeCompare(b.title));
    const label = x => esc(x.project ? `${ui.state.projects?.[x.project]?.name || x.project} · ${x.title}` : x.title);
    const tree = edit ? '' : `<div class="form-grid"><label>${t('parent')}<select name="parent"><option value="">${t('noParent')}</option>${others.map(x => `<option value="${esc(x.id)}">${label(x)}</option>`).join('')}</select></label>
      <label class="choice"><input type="checkbox" name="backlog" value="1">${t('keepBacklog')}</label></div>
      ${others.length ? `<fieldset class="stack"><legend>${t('after')}</legend>${others.map(x => `<label class="choice"><input type="checkbox" name="after" value="${esc(x.id)}">${label(x)}</label>`).join('')}</fieldset>` : ''}`;
    return `${tree}${workflowField(task, edit)}<label>${t('acceptance')}<textarea name="acceptance" rows="3">${esc((task.acceptance || []).join('\n'))}</textarea><small>${t('acceptanceHint')}</small></label>`;
  }

  // builtinFlows mirrors internal/workflow/builtin.
  const builtinFlows = ['docs', 'feature', 'fix'];
  const flowNames = () => [...new Set([...builtinFlows, ...Object.values(ui.state.projects || {}).flatMap(p => Object.keys(p.workflows || {}))])].sort();
  function workflowField(task, edit) {
    const cur = edit ? task.workflow || 'none' : '';
    return `<label>${t('workflow')}<select name="workflow">${edit ? '' : `<option value="">${t('projectDefault')}</option>`}<option value="none" ${cur === 'none' ? 'selected' : ''}>${t('noWorkflow')}</option>${flowNames().map(n => `<option value="${esc(n)}" ${n === cur ? 'selected' : ''}>${esc(n)}</option>`).join('')}</select><small>${t('workflowHint')}</small></label>`;
  }

  // formData turns the tree fields of form into task.create / task.edit params in data.
  function formData(form, data, edit) {
    const fd = new FormData(form);
    data.acceptance = String(fd.get('acceptance') || '').split('\n').map(x => x.trim()).filter(Boolean);
    delete data.backlog;
    if (!data.workflow || edit && data.workflow === (ui.state.tasks[form.dataset.id]?.workflow || 'none')) delete data.workflow;
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

  // inboxPage lists what waits for the viewer, filtered by kind and by why it is theirs, each handled in place.
  function inboxPage() {
    const kindOf = x => Object.keys(inboxKinds).find(k => inboxKinds[k].includes(x.reason)) || 'other';
    const shown = data.inbox.filter(x => (!data.inboxKind || kindOf(x) === data.inboxKind) && (!data.inboxAs || (x.as || []).includes(data.inboxAs)));
    const chip = (key, value, label, n) => `<button type="button" data-action="tree-inbox-filter" data-key="${key}" data-value="${value}" aria-pressed="${data[key] === value}" class="${data[key] === value ? 'active' : ''}">${label}${n !== undefined ? ` <span class="num">${n}</span>` : ''}</button>`;
    const count = (key, fn) => data.inbox.filter(fn).length;
    const kinds = [chip('inboxKind', '', t('allKinds'), data.inbox.length), ...[...Object.keys(inboxKinds), 'other'].map(k => chip('inboxKind', k, t('inbox.' + k), count('inboxKind', x => kindOf(x) === k)))];
    const roles = [chip('inboxAs', '', t('anyRole')), ...['owner', 'approver', 'dispatcher', 'admin'].map(r => chip('inboxAs', r, t('as.' + r), count('inboxAs', x => (x.as || []).includes(r))))];
    const rows = shown.map(x => Home.waitRow(x, `<p class="hint inbox-why">${esc(t('whyYou').replace('{0}', (x.as || []).map(r => t('as.' + r)).join(' · ')))}${x.project ? ` · <span class="mono">${esc(x.project)}</span>` : ''}</p>`));
    return `<header class="page-heading"><div><h1>${t('inbox')}</h1><p class="page-subtitle">${t('inboxHelp')}</p></div></header>
      <div class="machine-page"><div class="inbox-filters"><div class="view-toggle" role="group" aria-label="${t('inboxKind')}">${kinds.join('')}</div><div class="view-toggle" role="group" aria-label="${t('inboxAs')}">${roles.join('')}</div></div>
      ${rows.length ? `<div class="home-waits inbox-list">${rows.join('')}</div>` : statePanel('check', t(data.inbox.length ? 'inboxNoneShown' : 'inboxEmpty'), '')}</div>`;
  }

  // usedBy is where a definition is picked: the projects whose roles name it and the unfinished tasks set to it.
  function usedBy(name) {
    const projects = Object.values(ui.state.projects || {}).filter(p => p.defaults?.agent === name || Object.values(p.defaults?.roles || {}).includes(name)).map(p => p.name);
    const tasks = Object.values(ui.state.tasks).filter(x => x.agent === name && !finished(x.status)).length;
    return [projects.length ? t('usedByProjects').replace('{0}', projects.map(esc).join(', ')) : '', tasks ? t('usedByTasks').replace('{0}', tasks) : ''].filter(Boolean).join(' · ') || t('unused');
  }

  function agentsPage() {
    const rows = data.defs.map(d => `<div class="agent-row"><strong class="mono">${esc(d.name)}</strong><span>${esc(d.role || '—')}</span><span>${esc(d.provider || '—')} · ${esc(d.model || '—')}${d.effort ? ' · ' + esc(d.effort) : ''}</span><span>${esc(d.owner.startsWith('project:') ? d.owner : Team.name(d.owner))}</span>
      <span class="muted num">${d.rev ? 'v' + d.rev : ''}${d.updated_at ? ' · ' + date(d.updated_at) : ''}</span><span class="muted">${usedBy(d.name)}</span>
      ${d.text ? button('tree-view-agent', t('preview'), `data-name="${esc(d.name)}"`, 'quiet') + button('tree-edit-agent', t('edit'), `data-name="${esc(d.name)}"`, 'quiet') : `<span class="muted">${t('readonlyDef')}</span>`}${d.manage ? button('tree-share-agent', t('share'), `data-name="${esc(d.name)}"`, 'quiet') + button('tree-remove-agent', t('removeMember'), `data-name="${esc(d.name)}"`, 'quiet danger') : ''}</div>`);
    return `<header class="page-heading"><div><h1>${t('agents')}</h1><p class="page-subtitle">${t('agentsHelp')}</p></div><div class="flex"><label class="button quiet file-button">${t('importAgent')}<input type="file" id="tree-import-agent" accept=".md,text/markdown,text/plain" ${ui.online ? '' : 'disabled'}></label>${button('tree-new-agent', `${icon('plus')}${t('newAgent')}`, ui.online ? '' : 'disabled', 'primary')}</div></header>
      <div class="machine-page">${rows.length ? `<div class="agent-list">${rows.join('')}</div>` : statePanel('user', t('noAgents'), '')}</div>`;
  }

  const template = `---\nname: my-agent\ndescription: what it is for\nrole: implement\nprovider: claude\nmodel: sonnet\neffort: high\npermission: acceptEdits\ntools: {deny: [WebFetch]}\n---\nHow it works, what it must not do.\n`;
  const footer = label => `<footer class="modal-footer">${button('close-modal', t('cancel'))}<button type="submit" class="primary">${label}</button></footer>`;

  // agentStart is a new definition's first step: who runs it and with which model; the Markdown comes next.
  function agentStart() {
    const providers = ['claude', 'codex'].map(p => {
      const on = ui.machines.filter(m => m.agents?.[p]?.installed).map(m => m.name);
      return `<option value="${p}">${p}${on.length ? ' · ' + esc(t('installedOn').replace('{0}', on.join(', '))) : ' · ' + t('notInstalled')}</option>`;
    }).join('');
    const models = {claude: ['opus', 'sonnet', 'haiku', 'fable'], codex: []};
    showModal('tree-form', t('newAgent'), `<form id="tree-agent-start" data-command="${commandID()}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div>
      <div class="form-grid"><label>${t('agentName')}<input name="name" required pattern="[a-z0-9][a-z0-9-]*" value="my-agent" autofocus></label>
      <label>${t('agentType')}<select name="provider" id="agent-provider">${providers}</select></label>
      <label>${t('agentModel')}<input name="model" list="agent-models" placeholder="${t('modelDefault')}"><datalist id="agent-models">${models.claude.map(m => `<option value="${m}">`).join('')}</datalist></label>
      <label>${t('agentRole')}<select name="role">${['implement', 'review', 'test', 'planner', 'any'].map(r => `<option value="${r}">${r}</option>`).join('')}</select></label></div>
      <p class="hint">${t('modelWhy')}</p></div>${footer(t('next'))}</form>`, '');
    document.querySelector('#agent-provider').addEventListener('change', e => {
      document.querySelector('#agent-models').innerHTML = (models[e.target.value] || []).map(m => `<option value="${m}">`).join('');
    });
  }

  function agentForm(d, text = template) {
    const owners = Object.values(ui.state.projects || {}).filter(p => ui.me?.role === 'admin' || p.owner === ui.me?.id);
    const owner = d ? '' : `<label>${t('giveToProject')}<select name="owner"><option value="">${t('me')}</option>${owners.map(p => `<option value="project:${esc(p.id)}">${esc(p.name)}</option>`).join('')}</select></label>`;
    showModal('tree-form', t(d ? 'editAgent' : 'newAgent'), `<form id="tree-agent-form" data-command="${commandID()}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div>
      <label>${t('definition')}<textarea name="text" rows="18" class="mono" spellcheck="false" autofocus>${esc(d ? d.text : text)}</textarea></label>${owner}
      ${(d?.warnings || []).map(w => `<div class="notice">${esc(w)}</div>`).join('')}</div>${footer(t('save'))}</form>`, '', true);
  }

  // viewAgent shows what a definition starts, its warnings and where it is used, and exports its Markdown.
  function viewAgent(d) {
    showModal('tree-form', `${esc(d.name)}${d.rev ? ' · v' + d.rev : ''}`, `<div class="modal-body stack">
      <p class="muted">${usedBy(d.name)}</p>${(d.warnings || []).map(w => `<div class="notice">${esc(w)}</div>`).join('')}
      <span class="meta-label">${t('launchPreview')}</span><pre class="mono launch-preview"><code>${esc((d.launch || []).join(' ') || '—')}</code></pre>${(d.launch || []).some(x => x.includes('<')) ? `<p class="hint">${esc(t('launchHelp'))}</p>` : ''}
      <span class="meta-label">${t('definition')}</span><pre class="mono launch-preview"><code>${esc(d.text)}</code></pre></div>
      <footer class="modal-footer">${button('close-modal', t('close'))}${button('tree-export-agent', t('exportAgent'), `data-name="${esc(d.name)}"`)}</footer>`, '', true);
    data.viewing = d;
  }

  // importAgent opens the editor on a Markdown file's definition.
  async function importAgent(input) {
    const file = input.files?.[0]; input.value = '';
    if (!file) return;
    agentForm(null, await file.text());
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

  // offboardSteps are what cannot end with user and where each goes: projects and loose tasks to the heir, project
  // tasks to each project's owner, machines closed, memberships and credentials ended.
  function offboardSteps(user) {
    const projects = Object.values(ui.state.projects || {}), owned = projects.filter(p => p.owner === user);
    const tasks = Object.values(ui.state.tasks).filter(x => !finished(x.status) && (x.owner === user || x.approver === user));
    const loose = tasks.filter(x => !x.project || owned.some(p => p.id === x.project)), inProjects = tasks.filter(x => !loose.includes(x));
    const machines = (ui.machines || []).filter(m => m.owner === user), member = projects.filter(p => (p.members || {})[user]);
    const defs = data.defs.filter(d => d.owner === user);
    const step = (n, what, to) => n ? `<li><strong class="num">${n}</strong> ${what}<span class="muted"> → ${to}</span></li>` : '';
    const byOwner = [...new Set(inProjects.map(x => Team.name(ui.state.projects[x.project]?.owner || '')))].map(esc).join(', ');
    return [step(owned.length, t('off.projects').replace('{0}', owned.map(p => esc(p.name)).join(', ')), t('off.heir')),
      step(loose.length, t('off.tasks'), t('off.heir')), step(inProjects.length, t('off.projectTasks'), byOwner),
      step(defs.length, t('off.defs').replace('{0}', defs.map(d => esc(d.name)).join(', ')), t('off.heir')),
      step(machines.length, t('off.machines').replace('{0}', machines.map(m => esc(m.name)).join(', ')), t('off.closed')),
      step(member.length, t('off.member'), t('off.removed')), `<li>${t('off.creds')}<span class="muted"> → ${t('off.revoked')}</span></li>`].join('');
  }

  function offboard(user) {
    const heirs = Team.users().filter(u => !u.disabled && u.id !== user).map(u => `<option value="${esc(u.id)}" ${u.id === ui.me?.id ? 'selected' : ''}>${esc(u.name)}</option>`).join('');
    showModal('tree-form', `${t('offboard')} · ${esc(Team.name(user))}`, `<form id="tree-offboard-form" data-id="${esc(user)}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div><p>${t('offboardHelp')}</p>
      <ul class="plain offboard-steps">${offboardSteps(user)}</ul>
      <label>${t('handTo')}<select name="to" required autofocus>${heirs}</select></label><p class="hint">${t('off.audit')}</p></div><footer class="modal-footer">${button('close-modal', t('cancel'))}<button type="submit" class="primary danger">${t('offboard')}</button></footer></form>`, '');
  }

  const trackerKinds = {gitea: 'Gitea', github: 'GitHub', gitlab: 'GitLab'};

  // trackers shows project's bindings with how each is syncing, and a form to bind another repository.
  async function trackers(project) {
    const all = await Team.rest('GET', '/api/trackers'), mine = all.filter(x => x.project === project);
    const state = x => x.stopped ? `<span class="status failed">${t('trackerStopped')}</span>` : x.paused_until ? `<span class="status queued">${t('trackerPaused')}</span>` : `<span class="status exited">${t('trackerOK')}</span>`;
    const rows = mine.map(x => `<div class="agent-row"><strong class="mono">${esc(x.repo)}</strong><span>${esc(trackerKinds[x.kind] || x.kind)} · ${esc(x.base)} · @${esc(x.bot)}</span>${state(x)}
      <span>${t('syncedIssues').replace('{0}', x.issues)}${x.failing ? ' · ' + t('failingIssues').replace('{0}', x.failing) : ''}</span><span>${t('trackerLastOK')} ${x.last_ok ? date(x.last_ok) : '—'} · ${t('trackerScanned')} ${x.polled ? date(x.polled) : '—'}</span>
      ${x.hook ? `<span>${t('trackerHook')} <code>${esc(x.hook)}</code></span>` : ''}${button('tree-tracker-settings', t('trackerSettings'), `data-id="${esc(x.id)}" data-project="${esc(project)}"`, 'quiet')}${button('tree-tracker-log', t('syncLog'), `data-id="${esc(x.id)}" data-project="${esc(project)}"`, 'quiet')}
      ${x.last_error ? `<code class="muted">${esc(x.last_error)}</code>` : ''}${button('tree-rescan', t('rescan'), `data-id="${esc(x.id)}" data-project="${esc(project)}"`, 'quiet')}${button('tree-token', t('replaceToken'), `data-id="${esc(x.id)}" data-project="${esc(project)}"`, 'quiet')}${button('tree-unbind', t('unbind'), `data-id="${esc(x.id)}" data-project="${esc(project)}"`, 'quiet danger')}</div>`);
    showModal('tree-form', `${t('trackers')} · ${esc(ui.state.projects[project]?.name || project)}`, `<form id="tree-tracker-form" data-project="${esc(project)}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div>
      <p class="hint">${t('trackersHelp')}</p><div class="agent-list">${rows.join('') || `<div class="agent-row"><span>${t('noTrackers')}</span></div>`}</div>
      <h3>${t('bindRepo')}</h3><div class="form-grid"><label>${t('trackerKind')}<select name="kind">${Object.entries(trackerKinds).map(([k, v]) => `<option value="${k}">${v}</option>`).join('')}</select></label><label>${t('trackerBase')}<input name="base" required class="mono" placeholder="https://git.example"></label><label>${t('trackerRepo')}<input name="repo" required class="mono" placeholder="team/app"></label></div>
      <label>${t('trackerToken')}<input name="token" type="password" required autocomplete="off"><small>${t('trackerTokenHint')}</small></label>
      ${syncFields({label: 'tend', poll: 60, comment: true, on_accept: 'close'})}</div>${footer(t('bindRepo'))}</form>`, '', true);
  }

  // syncLog lists the issues a binding follows: which task, when its comment was written, what failed; each one's
  // progress comment can be previewed as the sync would write it now.
  async function syncLog(id, project) {
    const x = (await Team.rest('GET', '/api/trackers')).find(v => v.id === id), rows = await Team.rest('GET', '/api/trackers/issues?id=' + encodeURIComponent(id));
    if (!x) return;
    const link = n => `<a href="${esc(x.base.replace(/\/$/, ''))}/${esc(x.repo)}/${x.kind === 'gitlab' ? '-/' : ''}issues/${n}" target="_blank" rel="noreferrer">#${n}</a>`;
    const row = i => `<div class="agent-row"><strong class="mono">${link(i.number)}</strong><span>${i.task ? `<button type="button" class="link-button mono" data-action="tree-open" data-id="${esc(i.task)}">${esc(i.task)}</button>` : `<span class="muted">${t('notMirrored')}</span>`}${i.parent ? ' · ' + t('subOf').replace('{0}', '#' + i.parent) : ''}</span>
      <span class="muted">${i.written ? t('commentWritten').replace('{0}', date(i.written)) : t('noComment')}${i.closed ? ' · ' + t('trackerClosed') : ''}${i.dirty ? ' · ' + t('toRead') : ''}</span>${i.pr ? `<a href="${esc(i.pr)}" target="_blank" rel="noreferrer">PR</a>` : ''}
      ${i.task ? button('tree-tracker-preview', t('previewComment'), `data-id="${esc(id)}" data-number="${i.number}"`, 'quiet') : ''}${i.last_error ? `<code class="form-error">${esc(i.last_error)}</code>` : ''}</div>`;
    showModal('tree-form', `${t('syncLog')} · ${esc(x.repo)}`, `<div class="modal-body stack"><div class="agent-list">${rows.map(row).join('') || `<div class="agent-row"><span>${t('nobody')}</span></div>`}</div>
      <div id="tree-comment-preview"></div></div><footer class="modal-footer">${button('tree-trackers', t('back'), `data-project="${esc(project)}"`)}</footer>`, '', true);
  }

  async function commentPreview(id, number) {
    const box = document.querySelector('#tree-comment-preview'); if (!box) return;
    box.innerHTML = `<p class="muted">${t('loading')}</p>`;
    try {
      const v = await Team.rest('GET', `/api/trackers/preview?id=${encodeURIComponent(id)}&number=${number}`);
      box.innerHTML = `<span class="meta-label">${esc(t('commentFor').replace('{0}', '#' + number))}</span><pre class="mono launch-preview"><code>${esc(v.body)}</code></pre>`;
    } catch (error) { box.innerHTML = `<p class="form-error">${esc(errorText(error))}</p>`; }
  }

  // syncFields are the inputs of a binding's settings set.
  function syncFields(set) {
    const check = (name, label) => `<label class="choice"><input type="checkbox" name="${name}" value="1" ${set[name] ? 'checked' : ''}>${label}</label>`;
    return `<div class="form-grid"><label>${t('trackerLabel')}<input name="label" value="${esc(set.label || '')}" class="mono"></label><label>${t('trackerPoll')}<input name="poll" type="number" min="30" max="3600" value="${set.poll || 60}"></label></div>
      ${check('assigned', t('trackerAssigned'))}${check('comment', t('trackerComment'))}${check('detail', t('trackerDetail'))}${check('sub_issues', t('trackerSubIssues'))}${check('pr', t('trackerPR'))}
      <input type="hidden" name="accept_label" value="${esc(set.accept_label || 'tend:accepted')}"><label>${t('trackerOnAccept')}<select name="on_accept">${['close', 'label'].map(v => `<option value="${v}" ${set.on_accept === v ? 'selected' : ''}>${t('accept.' + v)}</option>`).join('')}</select></label>`;
  }
  const syncSettings = fd => ({label: String(fd.get('label') || '').trim(), assigned: fd.has('assigned'), comment: fd.has('comment'), detail: fd.has('detail'),
    sub_issues: fd.has('sub_issues'), pr: fd.has('pr'), on_accept: fd.get('on_accept'), accept_label: String(fd.get('accept_label') || 'tend:accepted'), poll: Number(fd.get('poll')) || 60});

  async function trackerSettings(id, project) {
    const x = (await Team.rest('GET', '/api/trackers')).find(v => v.id === id);
    if (!x) return;
    showModal('tree-form', `${t('trackerSettings')} · ${esc(x.repo)}`, `<form id="tree-tracker-settings-form" data-id="${esc(id)}" data-project="${esc(project)}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div>
      ${syncFields(x.settings)}</div>${footer(t('save'))}</form>`, '');
  }

  function tokenForm(id, project) {
    showModal('tree-form', t('replaceToken'), `<form id="tree-token-form" data-id="${esc(id)}" data-project="${esc(project)}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div>
      <label>${t('trackerToken')}<input name="token" type="password" required autocomplete="off" autofocus></label></div>${footer(t('save'))}</form>`, '');
  }

  // Project settings: the text forms of repos (one per line) and hooks (argv split on spaces).
  const hookNames = ['setup', 'before_run', 'check', 'cleanup'];
  const repoLine = r => [r.name, r.remote || '-', r.base || '-', ...(r.worktrees ? ['worktrees'] : []), ...Object.entries(r.dirs || {}).map(([m, d]) => `${m}=${d}`)].join(' ');
  function parseRepos(text) {
    return text.split('\n').map(l => l.trim()).filter(Boolean).map(l => {
      const [name, remote, base, ...dirs] = l.split(/\s+/);
      const r = {name, dirs: {}};
      if (remote && remote !== '-') r.remote = remote;
      if (base && base !== '-') r.base = base;
      for (const d of dirs) { const i = d.indexOf('='); if (i > 0) r.dirs[d.slice(0, i)] = d.slice(i + 1); else if (d === 'worktrees') r.worktrees = true; }
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
      <label>${t('plannerAgent')}<select name="planner">${agentOpts(roles.planner)}</select></label>
      <label>${t('defaultWorkflow')}<select name="workflow"><option value="">${t('noWorkflow')}</option>${[...new Set([...builtinFlows, ...Object.keys(p.workflows || {})])].sort().map(n => `<option value="${esc(n)}" ${n === p.defaults?.workflow ? 'selected' : ''}>${esc(n)}</option>`).join('')}</select></label></div>
      <fieldset class="stack"><legend>${t('customWorkflows')}</legend>${Object.keys(p.workflows || {}).sort().map(n => `<div class="flex"><span class="mono grow">${esc(n)}</span>${button('tree-edit-flow', t('edit'), `data-project="${esc(id)}" data-name="${esc(n)}"`, 'quiet')}${button('tree-remove-flow', t('removeMember'), `data-project="${esc(id)}" data-name="${esc(n)}"`, 'quiet danger')}</div>`).join('') || `<span class="muted">${t('noCustomWorkflows')}</span>`}
      <div>${button('tree-edit-flow', `${icon('plus')}${t('newWorkflow')}`, `data-project="${esc(id)}"`, 'quiet')}</div></fieldset>
      <div>${button('tree-check-dirs', t('checkDirs'), `data-project="${esc(id)}"`, 'quiet')}<ul id="tree-dir-checks" class="plain"></ul></div>
      ${hookNames.map(h => `<label>${t('hook.' + h)}<input name="${h}" class="mono" value="${esc((hooks[h] || []).join(' '))}"></label>`).join('')}</div>${footer(t('save'))}</form>`, '', true);
  }

  // checkDirs asks each machine a repo is checked out on whether that directory is there and is a git checkout.
  async function checkDirs() {
    const box = document.querySelector('#tree-dir-checks'), text = document.querySelector('#tree-project-form textarea[name=repos]');
    if (!box || !text) return;
    const pairs = parseRepos(text.value).flatMap(r => Object.entries(r.dirs).map(([machine, dir]) => ({repo: r.name, machine, dir})));
    if (!pairs.length) { box.innerHTML = `<li class="muted">${t('noDirsToCheck')}</li>`; return; }
    box.innerHTML = pairs.map((p, i) => `<li id="dir-check-${i}"><span class="mono">${esc(p.machine)}:${esc(p.dir)}</span> <span class="muted">${t('loading')}</span></li>`).join('');
    await Promise.all(pairs.map(async (p, i) => {
      const parent = p.dir.replace(/[\\/][^\\/]+[\\/]?$/, ''), name = p.dir.split(/[\\/]/).filter(Boolean).pop();
      let said;
      try {
        const v = await api.nodeCall({machine: p.machine, method: 'node.dirs', params: {path: parent}});
        const d = (v.dirs || []).find(x => x.name === name);
        said = !d ? `<span class="form-error">${t('dirMissing')}</span>` : d.git ? `<span class="ok">✓ ${t('dirGit')}</span>` : `<span class="warn">${t('dirNotGit')}</span>`;
      } catch (error) { said = `<span class="form-error">${esc(errorText(error))}</span>`; }
      const li = document.querySelector('#dir-check-' + i); if (li) li.innerHTML = `<span class="mono">${esc(p.machine)}:${esc(p.dir)}</span> ${said}`;
    }));
  }

  const flowTemplate = `---\nname: my-flow\ndescription: what it is for\nmax_loops: 2\nstages:\n  - {name: implement, role: implement, check: true}\n  - {name: review, role: review, output: verdict, on_rework: implement}\n  - {name: accept, gate: human}\n---\n## implement\n\n{{task.brief}}\n\n{{#rework}}Round {{loops}}: fix this first:\n\n{{rework.notes}}\n{{/rework}}\n`;
  function flowForm(project, name) {
    const text = name ? ui.state.projects[project]?.workflows?.[name] : flowTemplate;
    showModal('tree-form', t(name ? 'editWorkflow' : 'newWorkflow'), `<form id="tree-flow-form" data-project="${esc(project)}" data-name="${esc(name || '')}" data-command="${commandID()}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div>
      <label>${t('definition')}<textarea name="text" rows="20" class="mono" spellcheck="false" autofocus>${esc(text || '')}</textarea></label></div>${footer(t('save'))}</form>`, '', true);
  }
  async function setFlows(project, workflows, command) {
    const p = await api.projectEdit({id: project, workflows}, command);
    if (p) ui.state.projects[p.id] = p;
  }

  async function click(action, el) {
    const d = el.dataset;
    switch (action) {
      case 'tree-start': await api.taskStart({id: d.id}, {command_id: commandID()}); toast(t('started')); break;
      case 'tree-move': moveTask(ui.state.tasks[d.id]); break;
      case 'tree-open': if (modal.open) closeModal(true); ui.page = 'tasks'; renderShell(); await selectTask(d.id); break;
      case 'tree-inbox-filter': data[d.key] = d.value; render(); break;
      case 'tree-new-agent': agentStart(); break;
      case 'tree-edit-agent': agentForm(await api.agentDefGet({name: d.name})); break;
      case 'tree-view-agent': viewAgent(await api.agentDefGet({name: d.name})); break;
      case 'tree-export-agent': {
        const a = document.createElement('a'), url = URL.createObjectURL(new Blob([data.viewing.text], {type: 'text/markdown'}));
        a.href = url; a.download = data.viewing.name + '.md'; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000); break;
      }
      case 'tree-share-agent': shareAgent(await api.agentDefGet({name: d.name})); break;
      case 'tree-remove-agent': await api.agentDefRemove({name: d.name}, {command_id: commandID()}); await enter('agents'); break;
      case 'tree-project': projectSettings(d.project); break;
      case 'tree-check-dirs': await checkDirs(); break;
      case 'tree-source': await api.taskSourceAck({id: d.id, accept: !!d.accept}, {command_id: commandID()}); toast(t('acked')); break;
      case 'tree-trackers': await trackers(d.project); break;
      case 'tree-tracker-log': await syncLog(d.id, d.project); break;
      case 'tree-tracker-preview': await commentPreview(d.id, d.number); break;
      case 'tree-tracker-settings': await trackerSettings(d.id, d.project); break;
      case 'tree-rescan': await Team.rest('POST', '/api/trackers/rescan', {id: d.id}); toast(t('changeSaved')); await trackers(d.project); break;
      case 'tree-unbind': await Team.rest('DELETE', '/api/trackers', {id: d.id}); await trackers(d.project); break;
      case 'tree-token': tokenForm(d.id, d.project); break;
      case 'tree-notify': askNotify(); break;
      case 'tree-offboard': try { data.defs = (await api.agentDefList()).defs; } catch (_) {} offboard(d.id); break;
      case 'tree-pass': { const task = ui.state.tasks[d.id]; await api.taskGate({id: d.id, pass: true, expected_rev: task.rev}, {command_id: commandID()}); toast(t('passed')); break; }
      case 'tree-rework': gateForm(ui.state.tasks[d.id]); break;
      case 'tree-merge': await api.taskMerge({id: d.id}, {command_id: commandID()}); toast(t('merging')); break;
      case 'tree-plan': await api.taskPlan({id: d.id}, {command_id: commandID()}); toast(t('planning')); break;
      case 'tree-draft-edit': { const tk = ui.state.tasks[d.id]; draftForm(tk, structuredClone(tk.draft.plan)); break; }
      case 'tree-draft-add': case 'tree-draft-remove': {
        const form = document.querySelector('#tree-draft-form'), plan = draftOf(form), tk = ui.state.tasks[form.dataset.id];
        if (action === 'tree-draft-remove') plan.tasks.splice(Number(d.row), 1);
        else { let n = plan.tasks.length + 1; while (plan.tasks.some(x => x.key === 'task-' + n)) n++; plan.tasks.push({key: 'task-' + n, title: ''}); }
        draftForm(tk, plan); break;
      }
      case 'tree-draft-apply': { const tk = ui.state.tasks[d.id]; await api.taskPlanApply({id: d.id, expected_rev: tk.rev}, {command_id: commandID()}); toast(t('applied')); break; }
      case 'tree-draft-discard': { const tk = ui.state.tasks[d.id]; await api.taskPlanSave({id: d.id, expected_rev: tk.rev}, {command_id: commandID()}); toast(t('draftDropped')); break; }
      case 'tree-edit-flow': flowForm(d.project, d.name); break;
      case 'tree-remove-flow': {
        const flows = {...ui.state.projects[d.project]?.workflows}; delete flows[d.name];
        await setFlows(d.project, flows, {command_id: commandID()}); projectSettings(d.project); break;
      }
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
      case 'tree-agent-start': {
        const name = String(fd.get('name') || '').trim(), model = String(fd.get('model') || '').trim();
        if (!/^[a-z0-9][a-z0-9-]*$/.test(name) || model && !/^[\w.:\[\]-]+$/.test(model)) { modalError(t('badAgentStart')); return true; }
        const text = `---\nname: ${name}\ndescription: what it is for\nrole: ${fd.get('role')}\nprovider: ${fd.get('provider')}\n${model ? `model: ${model}\n` : ''}effort: high\npermission: acceptEdits\n---\nHow it works, what it must not do.\n`;
        closeModal(true); agentForm(null, text); return true;
      }
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
        for (const h of hookNames) { const a = String(fd.get(h) || '').trim().split(/\s+/).filter(Boolean); if (a.length) hooks[h] = a; else delete hooks[h]; }
        const p = await api.projectEdit({id: form.dataset.id, context: fd.get('context') || '', repos: parseRepos(String(fd.get('repos') || '')),
          defaults: {workflow: fd.get('workflow') || undefined, roles, machine: fd.get('machine') || undefined}, hooks}, command);
        if (p) ui.state.projects[p.id] = p;
        closeModal(true); renderPage(); toast(t('settingsSaved')); break;
      }
      case 'tree-draft-form':
        await api.taskPlanSave({id: form.dataset.id, plan: draftOf(form), expected_rev: Number(form.dataset.rev)}, command);
        closeModal(true); toast(t('draftSaved')); break;
      case 'tree-answer-form': {
        const text = String(fd.get('text') || '').trim();
        if (!text) { const e = form.querySelector('.form-error'); e.hidden = false; e.textContent = t('replyEmpty'); break; }
        await api.runContinue({run: form.dataset.run, text}, command);
        toast(t('planning')); break;
      }
      case 'tree-gate-form':
        await api.taskGate({id: form.dataset.id, pass: false, notes: fd.get('notes'), expected_rev: Number(form.dataset.rev)}, command);
        closeModal(true); toast(t('sentBack')); break;
      case 'tree-message-form': {
        const text = String(fd.get('text') || '').trim();
        if (!text) { const e = form.querySelector('.form-error'); e.hidden = false; e.textContent = t('replyEmpty'); break; }
        const r = await api.taskMessage({id: form.dataset.id, text}, command);
        ui.drafts.delete('task:' + form.dataset.id); form.reset(); form.dataset.command = commandID(); toast(t('sent.' + r.to));
        if (r.run) ui.run = r.run;
        break;
      }
      case 'tree-flow-form': {
        const text = String(fd.get('text') || ''), name = (text.match(/^name:\s*["']?([^"'\s]+)/m) || [])[1];
        if (!name) { const e = form.querySelector('.form-error'); e.hidden = false; e.textContent = t('workflowNameMissing'); break; }
        const flows = {...ui.state.projects[form.dataset.project]?.workflows};
        if (form.dataset.name && form.dataset.name !== name) delete flows[form.dataset.name];
        flows[name] = text;
        await setFlows(form.dataset.project, flows, command);
        toast(t('workflowSaved')); projectSettings(form.dataset.project); break;
      }
      case 'tree-offboard-form':
        await Team.rest('POST', '/api/users/offboard', {user: form.dataset.id, to: fd.get('to')});
        closeModal(true); toast(t('offboarded')); await Team.enter('admin'); break;
      case 'tree-tracker-form': {
        const v = await Team.rest('POST', '/api/trackers', {project: form.dataset.project, kind: fd.get('kind'), base: fd.get('base'), repo: fd.get('repo'), token: fd.get('token'),
          settings: syncSettings(fd)});
        showModal('tree-form', t('trackers'), `<div class="modal-body stack"><p>${t('trackerBound.' + fd.get('kind'))}</p>${v.hook ? `<code class="secret">${esc(v.hook)}</code>` : ''}<code class="secret" id="secret-value">${esc(v.hook_secret)}</code></div>`,
          `<footer class="modal-footer">${button('tree-trackers', t('close'), `data-project="${esc(form.dataset.project)}" autofocus`)}</footer>`, true);
        break;
      }
      case 'tree-tracker-settings-form':
        await Team.rest('POST', '/api/trackers/settings', {id: form.dataset.id, settings: syncSettings(fd)});
        toast(t('trackerSaved')); await trackers(form.dataset.project); break;
      case 'tree-token-form':
        await Team.rest('POST', '/api/trackers/credential', {id: form.dataset.id, token: fd.get('token')});
        toast(t('changeSaved')); await trackers(form.dataset.project); break;
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
    inboxTimer = setTimeout(() => refreshInbox().then(() => { if (ui.page === 'inbox' || ui.page === 'home') preserveRender(); }, () => {}), 300);
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

  return {pages, sitBadge, order, detail, formFields, formData, nav, enter, render, click, submit, stateChanged, refreshInbox, account, loadWebhook,
    importAgent, inbox: () => data.inbox, why: reason => whyText({reason})};
})();
