'use strict';

const words = {
  asked: ['等你回复', 'Waiting for you'], permission: ['等你批准', 'Needs permission'], stalled: ['长时间无输出', 'No output lately'],
  question: ['它问', 'It asked'], itAsks: ['它在问你', 'It asks you'], wantsTool: ['它想用 {0}：', 'It wants to use {0}:'],
  allow: ['允许', 'Allow'], deny: ['拒绝', 'Deny'], answer: ['回答', 'Answer'], noAnswer: ['不回答', "Don't answer"], answered: ['已回答', 'Answered'],
  sendMessage: ['发消息', 'Send a message'], sendPlaceholder: ['在它这一轮里告诉它…', 'Tell it, in its current turn…'], messageQueued: ['消息已排队', 'Message queued'],
  message: ['消息', 'Message'], 'send.queued': ['排队中', 'queued'], 'send.sent': ['已送达', 'sent'], 'send.failed': ['没送到', 'not delivered'], progress: ['进展', 'Progress'], lastSaid: ['最后说', 'Said'],
  usage: ['输入 {0} tokens，缓存命中 {1}，输出 {2} · {3} 轮', '{0} tokens in, {1} from cache, {2} out · {3} turns'], usageCost: [' · 约 ${0}', ' · about ${0}'], nextStep: ['下一步', 'Next'], reply: ['回复', 'Reply'],
  replyPlaceholder: ['你的回答；会作为新的后台运行进入同一个会话', 'Your answer; it goes to the same session as a new background run'],
  replied: ['回复已排队', 'Reply queued'], replyEmpty: ['先写回复内容', 'Write a reply first'], checking: ['正在检查机器…', 'Checking the machine…'],
  'reason.cli_missing': ['那台机器上没装这个 agent 的命令行', "The agent's CLI is not installed on that machine"],
  'reason.auth_missing': ['那台机器上 agent 的命令行没登录', "The agent's CLI is not logged in on that machine"],
  'reason.auth': ['agent 的登录被拒（过期或无效）', "The agent's login was refused (expired or invalid)"],
  'reason.quota': ['账号用量或额度用完了', "The account's usage limit or credit ran out"],
  'reason.rate_limit': ['接口限流', 'The API rate-limited it'], 'reason.overloaded': ['模型服务过载', 'The model service was overloaded'],
  'reason.context_overflow': ['对话超出了模型的上下文', "The conversation outgrew the model's context"],
  'reason.network': ['连模型服务的网络出错', 'The network to the model service failed'],
  'reason.session_missing': ['那台机器上找不到要续的会话', 'The session to continue is not on that machine'],
  'reason.permission_denied': ['它要用的工具被拒了（后台没人批准）', 'A tool it needed was denied (no one approves prompts in the background)'],
  'reason.node_outdated': ['那台机器上的 tend 太旧', 'tend on that machine is too old for this'],
  'hint.cli_missing': ['在那台机器上装好命令行，再运行一次', 'Install the CLI on that machine, then run the task again'],
  'hint.auth': ['在那台机器上登录，再运行一次', 'Log in on that machine, then run the task again'],
  'hint.quota': ['等额度恢复或换账号后再运行', 'Wait for the limit to reset or switch accounts, then run again'],
  'hint.later': ['过一会儿再运行', 'Run it again in a while'], 'hint.context_overflow': ['缩小任务书重新运行，不要续聊', 'Start a new run with a narrower brief instead of continuing'],
  'hint.session_missing': ['改为新运行，不要续聊', 'Start a new run instead of continuing'], 'hint.node_outdated': ['在协调器上运行 tend hosts install 更新那台机器', 'Update that machine: tend hosts install'],
  'why.cli_missing': ['那台机器没装 {0}：运行会直接失败', '{0} is not installed there: the run would fail'],
  'why.auth_missing': ['那台机器上 {0} 没登录：运行会直接失败', '{0} is not logged in there: the run would fail'],
  'why.node_outdated': ['那台机器的 tend（{0}）太旧，不能续聊', 'tend there ({0}) is too old to continue a session'],
  'why.offline': ['机器离线（{0}）：运行先排队', 'The machine is offline ({0}): the run waits in the queue'],
  'why.connecting': ['正在连接机器：连上前运行先排队', 'Connecting to the machine: the run waits until it answers'],
  'why.slots': ['槽位已满（{0}）：运行等空出来', 'All slots are busy ({0}): the run waits for one'],
  'why.dir_busy': ['运行 {0} 占着同一目录：等它结束', 'Run {0} uses the same directory: this one waits for it to end'],
  'why.auth_unknown': ['看不出 {0} 是否已登录', 'Could not tell whether {0} is logged in'],
  'why.unchecked': ['没连上机器，没检查它的 agent 命令行', 'The machine was not reached, so its agent CLI is not checked'],
  'why.herdr': ['那边有 Herdr 工作区包含该目录时在标签页里打开，否则后台运行', 'Opens in a Herdr tab when a workspace there holds the directory, else runs in the background'],
  'why.background': ['后台运行，输出会保留', 'Runs in the background; its output is kept'],
  'why.continues': ['续会话 {0}', 'Continues session {0}'],
  tasks: ['任务', 'Tasks'], machines: ['机器', 'Machines'], sessions: ['会话', 'Sessions'], later: ['稍后', 'Later'],
  workspace: ['个人工作区', 'Personal workspace'], operations: ['工作区', 'Workspace'],
  newTask: ['新建任务', 'New task'], editTask: ['编辑任务', 'Edit task'], search: ['搜索标题、ID、目录', 'Search title, ID, directory'],
  taskSubtitle: ['从任务书到执行，持续跟进每一次运行。', 'A clear view of every brief and every run.'],
  allStatus: ['全部任务状态', 'All task statuses'], allMachines: ['全部机器', 'All machines'], allRuns: ['全部运行状态', 'All run states'],
  todo: ['未完成', 'To do'], done: ['已完成', 'Done'], canceled: ['已取消', 'Canceled'], queued: ['排队', 'Queued'], starting: ['启动中', 'Starting'],
  running: ['运行中', 'Running'], unknown: ['失联', 'Unknown'], exited: ['已退出', 'Exited'], stopped: ['已停止', 'Stopped'],
  failed: ['失败', 'Failed'], abandoned: ['已放弃', 'Abandoned'], connected: ['在线', 'Connected'], connecting: ['连接中', 'Connecting'],
  offline: ['离线', 'Offline'], idle: ['未连接', 'Idle'], noRun: ['尚未运行', 'No runs yet'], active: ['进行中', 'Active'], needsAttention: ['待处理', 'Attention'], needsYou: ['等你处理', 'Needs you'],
  taskFirst: ['等你处理的在前，再到未完成', 'Needs you first, then to do'], results: ['个任务', 'tasks'], latest: ['最近一次', 'Latest run'],
  live: ['实时连接', 'Live connection'], logout: ['登出', 'Log out'], lang: ['切换到 English', 'Switch to 中文'],
  system: ['跟随系统', 'System theme'], light: ['浅色', 'Light'], dark: ['深色', 'Dark'], theme: ['主题', 'Theme'],
  dispatch: ['派发', 'Dispatch'], dispatchRun: ['派发运行', 'Dispatch run'], enqueue: ['确认并排队', 'Confirm & queue'],
  edit: ['编辑', 'Edit'], markDone: ['标记完成', 'Mark done'], reopen: ['重新打开', 'Reopen'], cancelTask: ['取消任务', 'Cancel task'],
  brief: ['任务书', 'Brief'], history: ['运行历史', 'Run history'], output: ['输出', 'Output'], conversation: ['对话', 'Conversation'],
  directory: ['工作目录', 'Working directory'], defaultMachine: ['默认机器', 'Default machine'], defaultAgent: ['默认档案', 'Default profile'],
  machine: ['机器', 'Machine'], agent: ['Agent 档案', 'Agent profile'], title: ['标题', 'Title'], optional: ['可选', 'Optional'],
  unspecified: ['未指定', 'Not set'], stop: ['停止运行', 'Stop run'], stopping: ['已请求停止', 'Stop requested'], abandon: ['放弃运行', 'Abandon run'],
  exitCode: ['退出码', 'Exit code'], frozenBrief: ['派发时任务书', 'Brief at dispatch'], taskBrief: ['当前任务书', 'Current brief'],
  follow: ['跟随中', 'Following'], paused: ['已暂停', 'Paused'], resume: ['继续跟随', 'Resume follow'], pause: ['暂停跟随', 'Pause follow'],
  raw: ['原始行', 'Raw lines'], timeline: ['时间线', 'Timeline'], older: ['加载更早输出', 'Load earlier output'], beginning: ['已到开头', 'Beginning of output'],
  newEvents: ['条新事件', 'new events'], waiting: ['等待输出', 'Waiting for output'], readonly: ['只读', 'Read only'],
  olderChat: ['加载更早对话', 'Load earlier messages'], chatBeginning: ['已到会话开头', 'Beginning of conversation'],
  noSession: ['会话尚未绑定', 'No session bound'], noSessionHelp: ['运行绑定会话后，这里将显示对话。普通命令可能不产生会话。', 'Messages appear once a session is bound. Command profiles may not create one.'],
  noOutput: ['还没有输出', 'No output yet'], noOutputHelp: ['排队或启动中的运行可能暂时没有输出。状态会持续更新。', 'Queued or starting runs may have no output yet. Status updates continue.'],
  empty: ['暂无任务', 'No tasks yet'], emptyHelp: ['写一份任务书，再选择机器开始运行。', 'Create a brief, then choose a machine to run it.'],
  noMatches: ['没有匹配的任务', 'No matching tasks'], noMatchesHelp: ['试试其他关键词或清除筛选。', 'Try another keyword or clear the filters.'],
  clearFilters: ['清除筛选', 'Clear filters'], selectTask: ['选择一个任务', 'Select a task'], selectHelp: ['查看任务书、运行记录和实时输出。', 'Inspect its brief, runs, and live output.'],
  loading: ['正在加载…', 'Loading…'], loadError: ['加载失败', 'Could not load'], retry: ['重试', 'Retry'],
  lost: ['与服务器的连接已断开', 'Connection to server lost'], lostHelp: ['保留最后数据；写操作暂不可用。正在自动重连。', 'Showing last known data. Writes are unavailable. Reconnecting automatically.'],
  reconnect: ['立即重连', 'Reconnect now'], reconnecting: ['正在重连…', 'Reconnecting…'], lastSync: ['上次同步', 'Last synced'],
  machineSubtitle: ['连接、负载和节点健康状况。', 'Connections, capacity, and node health.'], refresh: ['刷新', 'Refresh'],
  slots: ['活跃 / 并发上限', 'Active / slots'], queueCount: ['排队数', 'Queued'], os: ['系统', 'OS'], version: ['版本', 'Version'],
  hostname: ['主机名', 'Hostname'], nextRetry: ['下次重试', 'Next retry'], noRetry: ['等待节点主动连接', 'Waiting for node connection'],
  agentsTitle: ['Agent 档案', 'Agent profiles'], configNote: ['档案和并发上限由服务器 config.json 管理。', 'Profiles and slot limits are managed in the server config.json.'],
  allHosts: ['任意机器', 'Any machine'], viewTasks: ['查看任务', 'View tasks'],
  close: ['关闭', 'Close'], cancel: ['返回', 'Back'], save: ['保存', 'Save'], create: ['创建任务', 'Create task'], confirm: ['确认', 'Confirm'],
  titlePlaceholder: ['例如：修复输出分页边界', 'e.g. Fix output paging boundaries'], briefPlaceholder: ['说明目标、范围与验收条件…', 'Describe the goal, scope, and acceptance criteria…'],
  dirHint: ['使用目标机器上的路径；派发前必须填写。', 'Use a path on the target machine. Required before dispatch.'],
  briefHint: ['Markdown · 上限 256 KiB（UTF-8）', 'Markdown · 256 KiB maximum (UTF-8)'], preview: ['预览', 'Preview'],
  editHint: ['修改只影响下次派发。正在运行的任务书和档案已冻结。', 'Changes apply to future dispatches. The current run keeps its frozen brief and profile.'],
  saved: ['已保存任务', 'Task saved'], created: ['已创建任务', 'Task created'], dispatched: ['已加入队列', 'Run queued'], statusSaved: ['任务状态已更新', 'Task status updated'],
  pendingStop: ['已请求停止；等待节点确认。', 'Stop requested; waiting for node confirmation.'], abandonedToast: ['运行已放弃；节点仍可能在执行。', 'Run abandoned; the node may still be executing.'],
  stopTitle: ['停止这次运行？', 'Stop this run?'], stopHelp: ['将请求节点停止进程。收到节点确认前，运行仍占用任务和目录。此操作不能撤销。', 'The node will be asked to stop the process. The task and directory remain held until confirmed. This cannot be undone.'],
  stopQueuedHelp: ['此运行尚未开始。确认后将从队列取消，不会启动进程。', 'This run has not started. Confirming cancels it in the queue without starting a process.'],
  abandonTitle: ['放弃这次失联运行？', 'Abandon this unknown run?'], abandonHelp: ['将释放任务以便重新派发，并要求节点停止。失联机器上的进程可能仍在运行；放弃不等于确认进程已停止。', 'Releases the task for dispatch and requests a node stop. The disconnected process may still be running; abandonment is not proof it stopped.'],
  cancelTitle: ['取消这个任务？', 'Cancel this task?'], cancelHelp: ['保留任务书和全部运行记录，可以重新打开。已有运行会继续；需要停止时请单独停止运行。', 'Keeps the brief and all runs. You can reopen it. Any open run continues; use Stop run separately.'],
  doneHelp: ['标记完成不会停止已有运行。运行会继续，请确认这符合预期。', 'Marking the task done does not stop its open run. Confirm that it should continue.'],
  openRunReason: ['此任务已有未结束运行：', 'This task already has an open run: '], resolveRun: ['请等待结束，或停止运行；失联时可放弃。', 'Wait for it to finish or stop it. An unknown run can be abandoned.'],
  reopenReason: ['请先重新打开任务，再派发。', 'Reopen the task before dispatching.'], missingDir: ['请先编辑任务并填写工作目录。', 'Edit the task and set a working directory first.'],
  offlineQueue: ['机器未在线，可以排队；恢复连接后才会启动。', 'The machine is not online. You can queue; it will start after reconnection.'],
  capacityQueue: ['并发已满或同目录被占用，运行将等待可用位置。', 'Slots are full or the directory is occupied. The run will wait for capacity.'],
  frozenHelp: ['本次派发冻结任务书、目录和档案；不会自动将任务标记完成。', 'This dispatch freezes the brief, directory, and profile. Finishing a run does not mark the task done.'],
  agentMismatch: ['该档案只能在指定机器上运行。', 'This profile is restricted to its assigned machine.'],
  unknownReason: ['节点上未能确认运行。任务仍被占用。', 'The node cannot confirm this run. The task is still held.'],
  reasonSupervisor: ['监督进程已丢失', 'Supervisor is gone'], reasonMissing: ['节点上查不到运行', 'Run missing on node'],
  stale: ['文件已更换，请重新加载最新内容。', 'The file changed. Reload the latest content.'],
  loginTitle: ['连接你的工作区', 'Connect to your workspace'], loginDescription: ['管理任务、运行与自己的机器。', 'Manage tasks, runs, and your own machines.'],
  token: ['客户端 token', 'Client token'], tokenHint: ['生产版使用命令创建客户端 token：', 'Create a client token for production with:'],
  signIn: ['登录', 'Sign in'], tokenPlaceholder: ['粘贴客户端 token', 'Paste a client token'],
  loggedOut: ['已登出', 'You are signed out'], logoutHelp: ['连接已关闭，页面内的会话状态已清除。', 'The connection is closed and the page session is cleared.'],
  logoutTitle: ['登出工作区？', 'Log out of the workspace?'], logoutConfirm: ['只关闭浏览器连接，机器上的运行会继续。', 'Closes this browser session. Runs on your machines continue.'], pushed: ['已收到其他客户端更新', 'Update received from another client'], backTasks: ['返回任务列表', 'Back to tasks'],
  dirtyTitle: ['放弃未保存的修改？', 'Discard unsaved changes?'], dirtyHelp: ['关闭后将丢失本次表单输入。', 'Closing will discard the current form input.'],
  keepEditing: ['继续编辑', 'Keep editing'], discard: ['放弃修改', 'Discard changes'], badTitle: ['请输入标题。', 'Enter a title.'],
  badBrief: ['任务书超过 256 KiB，请缩短后保存。', 'The brief exceeds 256 KiB. Shorten it before saving.'],
  conflict: ['状态已变化，请检查当前任务再操作。', 'State changed. Review the current task before retrying.'], unauthorized: ['登录已失效，请重新登录。', 'Your session expired. Sign in again.'],
  timeout: ['请求超时，可重试同一次操作。', 'Request timed out. Retry the same operation.'], busy: ['服务器繁忙，请稍后重试。', 'Server is busy. Try again shortly.'],
  not_found: ['对象已不存在，请刷新。', 'This item no longer exists. Refresh the view.'], bad_request: ['参数不完整，请检查输入。', 'Check the form for missing or invalid values.'],
  authInvalid: ['token 无效或已吊销。', 'The token is not valid or was revoked.'],
  user: ['用户', 'User'], assistant: ['助手', 'Assistant'], systemEvent: ['系统', 'System'], tool: ['工具', 'Tool'], result: ['结果', 'Result'],
  chars: ['字', 'characters'], steps: ['步骤', 'steps'], duration: ['用时', 'Duration'], cost: ['费用', 'Cost'], noCost: ['未提供费用', 'Cost not provided'],
  stoppedHelp: ['运行已结束；任务状态需要手动更新。', 'This run has ended. Update the task status separately.'],
  noMachines: ['暂无机器', 'No machines'], noMachinesHelp: ['在服务器配置节点后，连接状态会出现在这里。', 'Configure nodes on the server to see connection states here.'],
  historyHelp: ['选择记录查看这次运行的输出与对话。', 'Select a run to inspect its output and conversation.'],
  none: ['无', 'None'], attention: ['注意', 'Notice'], emptyBrief: ['任务书为空，派发时使用标题。', 'The brief is empty. Dispatch uses the title.'],
  loadErrorHelp: ['读取超时，保留已有内容；可以重试。', 'The read timed out. Existing content is kept; you can retry.'],
  frozenDir: ['运行目录', 'Run directory'], pollNote: ['每 5 秒刷新机器', 'Machines refresh every 5s'], stoppedReceipt: ['节点已确认停止', 'Node confirmed the stop'],
  outputSource: ['实时输出', 'Live output'], permissions: ['权限', 'Permission'], profileModel: ['模型', 'Model'], waitForRun: ['派发后这里会显示输出和运行记录。', 'Dispatch this task to see output and run history.'], loaded: ['已加载', 'Loaded'], safeMarkdown: ['支持标题、列表、引用、代码与链接；HTML 作为文本。', 'Headings, lists, quotes, code, and links. HTML is displayed as text.'],
  keyboard: ['快捷键', 'Keyboard shortcuts'], shortcuts: ['快捷键', 'Shortcuts'], keyboardHelp: ['输入框内只使用标准编辑键。', 'Text fields keep their standard editing keys.'],
  viewDetails: ['打开详情', 'Open details'], move: ['移动选择', 'Move selection'], shortcutClose: ['关闭弹窗 / 返回列表', 'Close dialog / return to list'],
  snapshot: ['运行快照', 'Run snapshot'], automatic: ['自动', 'Automatic'], cancelledQueue: ['已取消排队', 'Queued run canceled'],
  readonlyChat: ['仅查看已有对话，不在这里发送消息。', 'Read existing messages. Sending is not available here.']
};
let lang = (navigator.language || 'zh').startsWith('zh') ? 'zh' : 'en';
let theme = 'system';
try { lang = localStorage.getItem('tend-lang') || lang; theme = localStorage.getItem('tend-theme') || theme; } catch (_) {}
const t = key => words[key] ? words[key][lang === 'zh' ? 0 : 1] : key;
const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const icon = name => `<svg class="icon" viewBox="0 0 24 24" aria-hidden="true">${({
  tasks:'<rect x="4" y="3" width="16" height="18" rx="2"/><path d="M8 8h8M8 12h8M8 16h5"/>',
  machine:'<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 21h8m-4-5v5"/>',
  search:'<circle cx="10" cy="10" r="6"/><path d="m15 15 5 5"/>',
  plus:'<path d="M12 5v14M5 12h14"/>', play:'<path d="m8 5 11 7-11 7z"/>',
  edit:'<path d="m15 4 5 5M4 20l5-1L21 7a2 2 0 0 0-5-5L4 14z"/>',
  stop:'<rect x="6" y="6" width="12" height="12" rx="1"/>', close:'<path d="m6 6 12 12M6 18 18 6"/>',
  back:'<path d="m10 5-7 7 7 7M3 12h18"/>', check:'<path d="m5 12 4 4L19 6"/>',
  offline:'<path d="m3 3 18 18M4 9a14 14 0 0 1 16 0M7 13a8 8 0 0 1 10 0m-7 4h4"/>',
  refresh:'<path d="M20 7v5h-5M4 17v-5h5M6 6a8 8 0 0 1 13 3M5 15a8 8 0 0 0 13 3"/>',
  chat:'<path d="M4 4h16v12H9l-5 4z"/>', error:'<path d="m12 3 10 18H2zM12 9v5m0 3h.01"/>',
  pause:'<path d="M8 5v14M16 5v14"/>', logout:'<path d="M10 3H4v18h6M9 12h12m-5-5 5 5-5 5"/>'
})[name] || '<circle cx="12" cy="12" r="8"/>'}</svg>`;
const openStates = new Set(['queued', 'starting', 'running', 'unknown']);
const statusSymbols = {queued:'◷', starting:'◌', running:'●', unknown:'?', exited:'✓', stopped:'■', failed:'!', canceled:'×', abandoned:'⊘', todo:'○', done:'✓', connected:'●', connecting:'◌', offline:'×', idle:'○', asked:'?', permission:'!', stalled:'…'};
const why = w => t('why.'+w.code).replace('{0}', w.detail || '');
const waiting = run => run && !openStates.has(run.state) && ['asked', 'permission'].includes(run.attention);
const attention = run => run && (waiting(run) || openStates.has(run.state) && ['asked', 'stalled', 'permission'].includes(run.attention)) ? run.attention : '';
const runNeedsYou = run => !!run && (waiting(run) || ['failed', 'unknown'].includes(run.state) || run.state === 'exited' && run.exit_code != null && run.exit_code !== 0 || openStates.has(run.state) && ['asked', 'stalled', 'permission'].includes(run.attention));
const tokens = n => n>=1e6?`${(n/1e6).toFixed(1)}M`:n>=1000?`${Math.floor(n/1000)}k`:String(n||0);
const usageText = u => !u||!(u.input||u.output||u.cache_read||u.cache_write)?'':[tokens((u.input||0)+(u.cache_write||0)),tokens(u.cache_read),tokens(u.output),u.turns||0].reduce((s,v,i)=>s.replace(`{${i}}`,v),t('usage'))+(u.cost_usd?t('usageCost').replace('{0}',u.cost_usd.toFixed(2)):'');
const since = run => run.ended_at || run.started_at || run.queued_at;
const badge = state => `<span class="status ${esc(state)}"><span class="status-icon" aria-hidden="true">${statusSymbols[state] || '·'}</span>${t(state)}</span>`;
const clone = value => JSON.parse(JSON.stringify(value));
const time = value => value ? new Date(value).toLocaleTimeString(lang === 'zh' ? 'zh-CN' : 'en-GB', {hour:'2-digit', minute:'2-digit', second:'2-digit'}) : '—';
const date = value => value ? new Date(value).toLocaleString(lang === 'zh' ? 'zh-CN' : 'en-GB', {month:'2-digit', day:'2-digit', hour:'2-digit', minute:'2-digit'}) : '—';
const elapsed = run => {
  if (!run) return '—';
  const seconds = Math.max(0, Math.floor(((run.ended_at ? new Date(run.ended_at).getTime() : Date.now()) - new Date(run.started_at || run.queued_at).getTime()) / 1000));
  return `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, '0')}s`;
};



const ui = {
  authenticated:false, loggedOut:false, online:true, reconnecting:false, page:'tasks', loading:false, error:'',
  state:{seq:0,tasks:{},runs:{}}, machines:[], agents:[], task:'t_7a21', run:'', tab:'output', mobileDetail:false,
  search:'', status:'', machine:'', runState:'', who:'', raw:false, follow:true, pending:0,
  briefs:new Map(), drafts:new Map(), choices:new Map(), outputs:new Map(), chats:new Map(), busy:new Set(), lastSync:new Date(), unsubscribe:null,
  modalType:'', modalTask:'', modalDirty:false, modalReturn:null, requestGeneration:0, viewReadLoading:false,
  detailError:'', modalOpener:null, focusTask:'t_7a21', toastTimer:null, reconnectTimer:null, frozenOutput:null, machineSync:new Date()
};
const app=document.querySelector('#app'), modal=document.querySelector('#modal');
const currentTask=()=>ui.state.tasks[ui.task];
const taskRuns=id=>Object.values(ui.state.runs).filter(r=>r.task===id).sort((a,b)=>b.queued_at.localeCompare(a.queued_at));
const latestRun=id=>taskRuns(id)[0];
const selectedRun=()=>ui.state.runs[ui.run] || latestRun(ui.task);
const openRun=id=>taskRuns(id).find(r=>openStates.has(r.state));
const commandID=()=>globalThis.crypto?.randomUUID?.() || `cmd_${Date.now()}_${Math.random().toString(36).slice(2)}`;
const errorText=error=>t(error.code||'loadError')+(error.detail?` · ${error.detail}`:'');
const button=(action,label,extra='',className='')=>`<button type="button" data-action="${action}" class="${className}" ${extra}>${label}</button>`;

function markdown(source) {
  const inline=text=>esc(text).replace(/`([^`]+)`/g,'<code>$1</code>').replace(/\*\*([^*]+)\*\*/g,'<strong>$1</strong>').replace(/\[([^\]]+)\]\(([^\s)]+)\)/g,(all,label,url)=>/^(https?:|mailto:)/i.test(url)?`<a href="${url}" rel="noreferrer noopener" target="_blank">${label}</a>`:all);
  let html='',list='',code=false,codeLines=[];
  const endList=()=>{if(list){html+=`</${list}>`;list='';}};
  for(const line of (source||'').split('\n')){
    if(line.startsWith('```')) {endList();if(code){html+=`<pre>${esc(codeLines.join('\n'))}</pre>`;codeLines=[];}code=!code;continue;}
    if(code){codeLines.push(line);continue;}
    const heading=line.match(/^(#{1,3})\s+(.+)$/), item=line.match(/^\s*(-|\d+\.)\s+(.+)$/);
    if(item){const type=item[1]==='-'?'ul':'ol';if(list!==type){endList();list=type;html+=`<${list}>`;}html+=`<li>${inline(item[2])}</li>`;continue;}
    endList();if(heading){html+=`<h${heading[1].length}>${inline(heading[2])}</h${heading[1].length}>`;}
    else if(line.startsWith('> '))html+=`<blockquote>${inline(line.slice(2))}</blockquote>`;
    else if(line.trim())html+=`<p>${inline(line)}</p>`;
  }
  endList();if(code)html+=`<pre>${esc(codeLines.join('\n'))}</pre>`;return html;
}
function normalizeOutput(text,provider) {
  const events=[], toolsByID=new Map();
  text.split('\n').filter(Boolean).forEach((line,index)=>{
    const base={index:index+1,raw:line};let event;
    try {event=JSON.parse(line);}catch(_){events.push({...base,kind:'text',text:line});return;}
    if(provider==='claude') {
      if(event.type==='assistant'||event.type==='user') {
        for(const block of event.message?.content||[]) {
          if(block.type==='text')events.push({...base,kind:event.type==='user'?'user':'assistant',text:block.text});
          else if(block.type==='tool_use'){const item={...base,kind:'tool',name:block.name,input:JSON.stringify(block.input,null,2),result:''};events.push(item);toolsByID.set(block.id,item);}
          else if(block.type==='tool_result'){const item=toolsByID.get(block.tool_use_id);const result=typeof block.content==='string'?block.content:JSON.stringify(block.content,null,2);if(item)item.result=result;else events.push({...base,kind:'tool',name:block.tool_use_id,input:'',result});}
          else events.push({...base,kind:'raw',text:JSON.stringify(block)});
        }
      } else if(event.type==='system')events.push({...base,kind:'systemEvent',text:`${event.subtype||event.type} · ${event.model||''}`});
      else if(event.type==='result')events.push({...base,kind:'result',text:event.result||event.subtype,cost:event.total_cost_usd,duration:event.duration_ms});
      else if(event.type==='stream_event'&&event.event?.delta?.text)events.push({...base,kind:'assistant',text:event.event.delta.text});
      else events.push({...base,kind:'raw',text:line});
    } else if(provider==='codex') {
      const item=event.item;
      if(item?.type==='agent_message'||item?.type==='reasoning')events.push({...base,kind:'assistant',text:item.text});
      else if(item?.type==='command_execution')events.push({...base,kind:'tool',name:'command_execution',input:item.command,result:item.aggregated_output,exit:item.exit_code});
      else if(item?.type==='file_change'||item?.type==='mcp_tool_call')events.push({...base,kind:'tool',name:item.type,input:JSON.stringify(item,null,2)});
      else if(event.type==='turn.completed')events.push({...base,kind:'result',text:`${event.usage?.input_tokens??'—'} input · ${event.usage?.cached_input_tokens??'—'} cached · ${event.usage?.output_tokens??'—'} output tokens`});
      else if(['thread.started','turn.started'].includes(event.type))events.push({...base,kind:'systemEvent',text:event.type});
      else events.push({...base,kind:'raw',text:line});
    } else events.push({...base,kind:'text',text:line});
  });
  return events;
}
function renderEvent(event) {
  const header=`<div class="event-meta"><strong>${t(event.kind==='text'||event.kind==='raw'?'output':event.kind)}</strong><span class="mono">#${event.index}</span></div>`;
  let body;
  if(event.kind==='tool')body=`<details class="tool-card" data-tool="${event.index}"><summary>${esc(event.name)}${event.exit!==undefined?` · exit ${event.exit}`:''}</summary><pre>${esc(event.input)}${event.result?`\n\n${esc(event.result)}`:''}</pre></details>`;
  else if(event.kind==='result')body=`<div class="result-card">${esc(event.text)}<div class="muted">${event.cost!==undefined?`${t('cost')} $${Number(event.cost).toFixed(4)}`:t('noCost')}${event.duration!==undefined?` · ${t('duration')} ${(event.duration/1000).toFixed(1)}s`:''}</div></div>`;
  else if(event.kind==='raw')body=`<details class="tool-card"><summary>${t('raw')}</summary><pre>${esc(event.text)}</pre></details>`;
  else body=`<div class="event-body">${esc(event.text).replace(/\n/g,'<br>')}</div>`;
  return `<article class="timeline-event ${event.kind}">${header}${body}</article>`;
}
function toast(message) {
  const el=document.querySelector('#toast');el.textContent=message;el.hidden=false;clearTimeout(ui.toastTimer);ui.toastTimer=setTimeout(()=>el.hidden=true,4200);
}
function applyPreferences() {
  document.documentElement.lang=lang==='zh'?'zh-Hans':'en';document.documentElement.dataset.theme=theme;
  try{localStorage.setItem('tend-lang',lang);localStorage.setItem('tend-theme',theme);}catch(_){}
}
function utilities(loggedIn=true) {
  return `<div class="flex">${loggedIn?`<span class="connection" id="connection-status">${ui.online?'<span class="dot"></span>'+t('live'):t('offline')}</span>`:''}
    <button type="button" data-action="language" aria-label="${t('lang')}">${lang==='zh'?'EN':'中文'}</button>
    <label><span class="sr-only">${t('theme')}</span><select id="theme-select" aria-label="${t('theme')}">${['system','light','dark'].map(v=>`<option value="${v}" ${theme===v?'selected':''}>${t(v)}</option>`).join('')}</select></label>
    ${loggedIn?button('logout',icon('logout'),`aria-label="${t('logout')}" title="${t('logout')}"`,'quiet'):''}</div>`;
}
function renderLogin() {
  app.innerHTML=`<div class="login-page"><header class="topbar"><div class="brand"><span class="brand-mark">&gt;_</span>tend</div>${utilities(false)}</header>
    <main id="main" class="login-main"><section class="login-card"><div class="eyebrow">TEND / PERSONAL OPERATIONS</div><h1>${t(ui.loggedOut?'loggedOut':'loginTitle')}</h1><p class="login-description">${t(ui.loggedOut?'logoutHelp':'loginDescription')}</p>
    <form id="login-form" class="login-form"><div id="login-error" role="alert" hidden class="form-error"></div><label>${t('token')}<input type="password" name="token" id="token-input" autocomplete="off" spellcheck="false" placeholder="${t('tokenPlaceholder')}" required autofocus></label>
    <div class="hint">${t('tokenHint')}<code>tend server token add --client web</code></div><button class="primary" type="submit">${t('signIn')}</button></form></section></main></div>`;
}
function renderShell() {
  if(!ui.authenticated){renderLogin();return;}
  app.innerHTML=`<header class="topbar"><div class="flex"><div class="brand"><span class="brand-mark">&gt;_</span>tend</div><span class="workspace-label">${t('workspace')}</span></div>${utilities()}</header>
    <div class="shell"><aside class="sidebar"><nav class="nav-section" aria-label="${t('workspace')}"><span class="nav-label">${t('operations')}</span>
      <button class="nav-item ${ui.page==='tasks'?'active':''}" data-action="page" data-page="tasks" ${ui.page==='tasks'?'aria-current="page"':''}>${icon('tasks')}${t('tasks')}<span class="count" id="count-tasks"></span></button>
      <button class="nav-item ${ui.page==='machines'?'active':''}" data-action="page" data-page="machines" ${ui.page==='machines'?'aria-current="page"':''}>${icon('machine')}${t('machines')}<span class="count" id="count-machines"></span></button>
      <button class="nav-item future-nav" disabled title="${t('later')}">${icon('chat')}${t('sessions')}<small>${t('later')}</small></button></nav>
      <div class="sidebar-bottom">${button('keyboard',`${t('shortcuts')} <kbd>?</kbd>`,'','quiet')}<div class="sidebar-note">tend server · ${esc(ui.who)}</div></div></aside>
    <main id="main" class="main"><div id="connection-banner"></div><div id="page-content"></div></main></div>`;
  renderCounts();renderBanner();renderPage();
}
function renderCounts() {
  const tasks=document.querySelector('#count-tasks'),machines=document.querySelector('#count-machines');
  if(tasks)tasks.textContent=Object.keys(ui.state.tasks).length;
  if(machines)machines.textContent=`${ui.machines.filter(m=>m.state==='connected').length}/${ui.machines.length}`;
}
function renderBanner() {
  const el=document.querySelector('#connection-banner');if(!el)return;
  el.innerHTML=ui.online?'':`<div class="banner" role="alert">${icon('offline')}<p><strong>${t('lost')}</strong><br>${t('lostHelp')} <small>${t('lastSync')} ${time(ui.lastSync)}</small></p>${button('reconnect',t(ui.reconnecting?'reconnecting':'reconnect'),ui.reconnecting?'disabled':'')}</div>`;
}
const needsYou=task=>task.status==='todo'&&runNeedsYou(latestRun(task.id));
function filteredTasks() {
  const rank=task=>needsYou(task)?0:task.status==='todo'?1:2;
  return Object.values(ui.state.tasks).filter(task=>{
    const run=latestRun(task.id), search=`${task.title} ${task.id} ${task.dir}`.toLowerCase();
    return (!ui.status||task.status===ui.status)&&(!ui.machine||(run?.machine||task.machine)===ui.machine)&&(!ui.runState||(ui.runState==='needs'?needsYou(task):(run?.state||'none')===ui.runState))&&(!ui.search||search.includes(ui.search.toLowerCase()));
  }).sort((a,b)=>rank(a)-rank(b)||(rank(a)===0?since(latestRun(a.id)).localeCompare(since(latestRun(b.id))):b.updated_at.localeCompare(a.updated_at)));
}
function renderRows() {
  if(ui.loading)return `<div aria-busy="true" aria-label="${t('loading')}">${Array.from({length:5},()=>'<div class="skeleton-row"><div class="skeleton"></div><div class="skeleton"></div><div class="skeleton"></div></div>').join('')}</div>`;
  if(ui.error)return statePanel('error',t('loadError'),t('loadErrorHelp'),'retry-data',t('retry'));
  const none=!Object.keys(ui.state.tasks).length,tasks=filteredTasks();if(!tasks.length)return statePanel('tasks',t(none?'empty':'noMatches'),t(none?'emptyHelp':'noMatchesHelp'),none?'new':'clear-filters',t(none?'newTask':'clearFilters'));
  return tasks.map(task=>{const run=latestRun(task.id);return `<button class="task-row ${ui.task===task.id?'selected':''}" id="row-${task.id}" data-action="select-task" data-id="${task.id}" aria-current="${ui.task===task.id?'true':'false'}" tabindex="${ui.focusTask===task.id?'0':'-1'}"><div class="row-top"><span class="row-id">${task.id}</span>${badge(task.status)}</div><div class="row-title">${esc(task.title)}</div><div class="row-meta"><span class="machine-agent">${esc(run?.machine||task.machine||'—')} / ${esc(run?.agent||task.agent||'—')}</span><span>${elapsed(run)}</span></div><div class="mt-5">${run?badge(run.state)+(attention(run)?' '+badge(attention(run)):''):`<span class="muted fs-11">${t('noRun')}</span>`}</div></button>`;}).join('');
}
function statePanel(symbol,title,body,action,label) {return `<div class="state-panel">${icon(symbol)}<h3>${title}</h3><p>${body}</p>${action?button(action,label):''}</div>`;}
function renderPage() {
  const el=document.querySelector('#page-content');if(!el)return;
  if(ui.page==='machines'){renderMachines();return;}
  const runs=Object.values(ui.state.runs), unfinished=Object.values(ui.state.tasks).filter(task=>task.status==='todo').length;
  el.innerHTML=`<header class="page-heading"><div><h1>${t('tasks')}</h1><p class="page-subtitle">${t('taskSubtitle')}</p></div><div class="flex"><span class="heading-right">${date(new Date())}</span>${button('new',`${icon('plus')}${t('newTask')} <kbd>n</kbd>`,ui.online?'':'disabled','primary')}</div></header>
    <div class="metric-strip"><span class="metric"><strong>${unfinished}</strong>${t('todo')}</span><span class="metric">${badge('running')}<strong>${runs.filter(r=>['running','starting'].includes(r.state)).length}</strong></span><span class="metric">${badge('queued')}<strong>${runs.filter(r=>r.state==='queued').length}</strong></span><button type="button" class="metric" data-action="needs-you">${t('needsAttention')}<strong>${Object.values(ui.state.tasks).filter(needsYou).length}</strong></button></div>
    <div class="task-workspace ${ui.mobileDetail?'detail-open':''}"><section class="task-list-panel" aria-label="${t('tasks')}"><div class="list-tools"><label class="search-box">${icon('search')}<input id="search-input" value="${esc(ui.search)}" placeholder="${t('search')}" aria-label="${t('search')}"><kbd>/</kbd></label>
    <div class="filter-row"><select id="status-filter" aria-label="${t('allStatus')}"><option value="">${t('allStatus')}</option>${['todo','done','canceled'].map(v=>`<option value="${v}" ${ui.status===v?'selected':''}>${t(v)}</option>`).join('')}</select>
    <select id="machine-filter" aria-label="${t('allMachines')}"><option value="">${t('allMachines')}</option>${ui.machines.map(m=>`<option value="${m.name}" ${ui.machine===m.name?'selected':''}>${m.name}</option>`).join('')}</select></div>
    <select id="run-filter" aria-label="${t('allRuns')}"><option value="">${t('allRuns')}</option><option value="needs" ${ui.runState==='needs'?'selected':''}>${t('needsYou')}</option>${['queued','starting','running','unknown','exited','stopped','failed','canceled','abandoned'].map(v=>`<option value="${v}" ${ui.runState===v?'selected':''}>${t(v)}</option>`).join('')}</select>
    <div class="list-caption"><span id="result-count">${filteredTasks().length} ${t('results')}</span><span>${t('taskFirst')}</span></div></div><div class="task-list" id="task-list">${renderRows()}</div></section>
    <section class="task-detail" id="task-detail" aria-label="${t('viewDetails')}"></section></div>`;
  renderDetail();
}
function renderTaskList() {
  const el=document.querySelector('#task-list');if(!el)return;
  const rows=filteredTasks();if(!rows.some(task=>task.id===ui.focusTask))ui.focusTask=rows[0]?.id||'';
  el.innerHTML=renderRows();document.querySelector('#result-count').textContent=`${rows.length} ${t('results')}`;
}
function renderDetail() {
  const el=document.querySelector('#task-detail');if(!el)return;
  if(ui.loading){el.innerHTML=statePanel('refresh',t('loading'),'');return;}
  if(ui.error){el.innerHTML=statePanel('tasks',t('selectTask'),t('selectHelp'));return;}
  const task=currentTask();if(!task){el.innerHTML=statePanel('tasks',t('selectTask'),t('selectHelp'));return;}
  const runs=taskRuns(task.id),run=selectedRun(),writeDisabled=ui.online?'':'disabled',typing=document.activeElement?.id==='reply-text';
  el.innerHTML=`${button('back-tasks',`${icon('back')}${t('backTasks')}`,'','mobile-back')}<div class="detail-eyebrow"><span class="mono">${task.id}</span><span>·</span>${badge(task.status)}<span>· rev ${task.rev}</span></div>
    <div class="detail-header"><h2>${esc(task.title)}</h2><div class="detail-actions">${button('edit',`${icon('edit')}${t('edit')}`,writeDisabled)}${button('dispatch',`${icon('play')}${t('dispatch')}`,writeDisabled,'primary')}</div></div>
    <div class="task-actions">${button(task.status==='todo'?'done':'reopen',`${icon(task.status==='todo'?'check':'refresh')}${t(task.status==='todo'?'markDone':'reopen')}`,writeDisabled,'quiet')}${task.status!=='canceled'?button('cancel-task',t('cancelTask'),writeDisabled,'quiet'):''}</div>
    <div class="metadata"><div><span class="meta-label">${t('directory')}</span><code class="meta-value">${esc(task.dir||'—')}</code></div><div><span class="meta-label">${t('defaultMachine')}</span><span class="meta-value mono">${esc(task.machine||t('unspecified'))}</span></div><div><span class="meta-label">${t('defaultAgent')}</span><span class="meta-value mono">${esc(task.agent||t('unspecified'))}</span></div></div>
    <div class="tabs" role="tablist" aria-label="${t('viewDetails')}">${['output','conversation','brief','history'].map(tab=>`<button class="tab ${ui.tab===tab?'active':''}" role="tab" id="tab-${tab}" aria-selected="${ui.tab===tab}" aria-controls="detail-body" tabindex="${ui.tab===tab?'0':'-1'}" data-action="tab" data-tab="${tab}">${t(tab)}${tab==='history'?`<span class="count">${runs.length}</span>`:''}</button>`).join('')}</div>
    <div id="detail-body" role="tabpanel" aria-labelledby="tab-${ui.tab}"></div>`;
  const body=document.querySelector('#detail-body');
  if(ui.tab==='brief') {const brief=ui.briefs.get(task.id);body.innerHTML=ui.detailError?statePanel('error',t('loadError'),esc(ui.detailError),'retry-detail',t('retry')):brief===undefined?statePanel('refresh',t('loading'),''):`<div class="brief-panel"><p class="hint">${t('taskBrief')} · rev ${task.rev}</p><article class="brief">${markdown(brief)||`<p class="muted">${t('emptyBrief')}</p>`}</article></div>`;return;}
  if(ui.tab==='history'){body.innerHTML=`<p class="hint mb-12">${t('historyHelp')}</p><div class="history-list">${runs.map(r=>`<button class="history-row ${r.id===run?.id?'active':''}" data-action="select-run" data-id="${r.id}"><div class="stack"><span class="mono">${r.id} · ${esc(r.machine)} / ${esc(r.agent)}</span><span class="hint">${date(r.queued_at)} · ${elapsed(r)}${r.exit_code!==undefined?` · exit ${r.exit_code}`:''}</span></div>${badge(r.state)}</button>`).join('')||statePanel('tasks',t('noRun'),t('waitForRun'))}</div>`;return;}
  if(!run){body.innerHTML=statePanel('play',t('noRun'),t('waitForRun'),'dispatch',t('dispatchRun'));return;}
  const terminal=!openStates.has(run.state);
  body.innerHTML=`<div class="run-context"><div class="grow"><label class="sr-only" for="run-select">${t('history')}</label><select id="run-select">${runs.map((r,i)=>`<option value="${r.id}" ${r.id===run.id?'selected':''}>${r.id} · ${t(r.state)}${i===0?' · '+t('latest'):''}</option>`).join('')}</select></div>${badge(run.state)}${openStates.has(run.state)?button('stop',`${icon('stop')}${t(run.want==='stop'?'stopping':'stop')}`,`${writeDisabled} ${run.want==='stop'?'disabled':''}`,'danger'):''}${run.state==='unknown'?button('abandon',t('abandon'),writeDisabled,'danger'):''}</div>
    <div class="run-details"><span class="mono">${esc(run.machine)} / ${esc(run.agent)}</span><span>${elapsed(run)}</span><span>${run.exit_code!==undefined?`${t('exitCode')} ${run.exit_code}`:esc(run.runner)}</span></div>
    ${run.state==='unknown'?`<div class="run-alert">${t('unknownReason')} ${run.reason==='supervisor_gone'?t('reasonSupervisor'):t('reasonMissing')} · <code>${esc(run.reason)}</code></div>`:run.want==='stop'&&!terminal?`<div class="run-alert">${t('pendingStop')}</div>`:''}
    ${runFacts(run,terminal,writeDisabled)}
    ${ui.tab==='conversation'?renderChatPanel(run):renderOutputPanel(run)}
    <details class="history-note"><summary>${t('snapshot')} · ${esc(run.id)}</summary><p class="hint mt-10">${t('frozenDir')} <code>${esc(run.dir)}</code><br>${t('profileModel')}: ${esc(run.profile?.model||'—')} · ${t('permissions')}: ${esc(run.profile?.permission||'—')}</p><article class="brief">${markdown(run.brief)}</article></details>`;
  if(typing){const r=document.querySelector('#reply-text');if(r){r.focus();r.setSelectionRange(r.value.length,r.value.length);}}
  bindOutputScroll();
}
function renderOutputPanel(run) {
  return `<section class="output-panel mt-12" aria-label="${t('output')}"><div class="output-toolbar"><span class="flex">${icon('tasks')}<strong>${t('outputSource')}</strong></span><div class="flex">${button('toggle-raw',t(ui.raw?'timeline':'raw'),`aria-pressed="${ui.raw}"`)}${button('toggle-follow',`${icon(ui.follow?'pause':'play')}${t(ui.follow?'pause':'resume')}`,`id="follow-button" aria-pressed="${ui.follow}"`)}</div></div><div class="output-view" id="output-view" tabindex="0" aria-label="${t('output')}">${outputContents(run)}</div><div class="output-footer"><span>${esc(run.provider)} · output.log</span><span id="output-status">${t(ui.follow?'follow':'paused')}${ui.pending?` · ${ui.pending} ${t('newEvents')}`:''}</span></div></section>`;
}
function outputContents(run) {
  const data=!ui.follow&&ui.frozenOutput?.id===run.id?ui.frozenOutput.data:ui.outputs.get(run.id);
  if(!data)return statePanel('refresh',t('loading'),'');
  if(data.error)return statePanel('error',t('loadError'),esc(data.error),'retry-output',t('retry'));
  if(!data.text)return statePanel('tasks',t('noOutput'),t('noOutputHelp'));
  const top=`<div class="load-older">${data.done?`<small class="muted">${t('beginning')}</small>`:button('older-output',t('older'),ui.busy.has('older-output')?'disabled':'')}</div>`;
  return top+(ui.raw?`<pre class="raw-output">${esc(data.text)}</pre>`:normalizeOutput(data.text,run.provider).map(renderEvent).join(''));
}
function renderOutput() {
  const el=document.querySelector('#output-view'),run=selectedRun();if(!el||!run)return;
  const oldTop=el.scrollTop,oldHeight=el.scrollHeight,opened=[...el.querySelectorAll('details[open]')].map(x=>x.dataset.tool);
  el.innerHTML=outputContents(run);opened.forEach(key=>el.querySelector(`[data-tool="${key}"]`)?.setAttribute('open',''));
  if(ui.follow)el.scrollTop=el.scrollHeight;
  else el.scrollTop=ui.prepending?oldTop+(el.scrollHeight-oldHeight):oldTop;
  ui.prepending=false;updateFollowLabel();
}
function pauseFollow() {
  if(ui.follow){const run=selectedRun();ui.frozenOutput=run?{id:run.id,data:clone(ui.outputs.get(run.id)||{})}:null;}
  ui.follow=false;updateFollowLabel();
}
function updateFollowLabel() {
  const button=document.querySelector('#follow-button');if(button){button.innerHTML=`${icon(ui.follow?'pause':'play')}${t(ui.follow?'pause':'resume')}`;button.setAttribute('aria-pressed',String(ui.follow));}
  const status=document.querySelector('#output-status');if(status)status.textContent=`${t(ui.follow?'follow':'paused')}${ui.pending?' · '+ui.pending+' '+t('newEvents'):''}`;
}
function bindOutputScroll() {
  const el=document.querySelector('#output-view');if(!el)return;if(ui.follow)el.scrollTop=el.scrollHeight;
  el.addEventListener('wheel',event=>{if(event.deltaY<0&&ui.follow)pauseFollow();},{passive:true});
  el.addEventListener('touchstart',()=>{if(ui.follow)pauseFollow();},{passive:true});
  el.addEventListener('keydown',event=>{if(['ArrowUp','PageUp','Home'].includes(event.key))pauseFollow();});
  el.addEventListener('pointerdown',event=>{if(event.offsetX>=el.clientWidth-18)pauseFollow();});
}
function renderChatPanel(run) {
  if(!run.session)return statePanel('chat',t('noSession'),t('noSessionHelp'));
  const data=ui.chats.get(run.id);
  const content=!data?statePanel('refresh',t('loading'),''):data.error?statePanel('error',t('loadError'),esc(data.error),'retry-chat',t('retry')):`<div class="load-older">${data.done?`<small class="muted">${t('chatBeginning')}</small>`:button('older-chat',t('olderChat'),ui.busy.has('older-chat')?'disabled':'')}</div>${data.messages.map(message=>`<article class="chat-message ${message.role}"><div class="chat-meta"><strong>${t(message.role)}</strong><span>${time(message.at)}</span><span>${message.chars} ${t('chars')} · ${message.steps} ${t('steps')}</span></div><div class="chat-body">${esc(message.text)}</div></article>`).join('')}`;
  return `<section class="output-panel mt-12"><div class="output-toolbar"><span>${icon('chat')} <strong>${t('conversation')}</strong></span><span class="mono">${esc(run.provider)} · ${esc(run.session.slice(0,18))}</span></div><div class="chat-list" id="chat-list" tabindex="0">${content}</div><div class="output-footer">${t('readonlyChat')}</div></section>`;
}
function renderMachines() {
  const el=document.querySelector('#page-content');if(!el)return;
  el.innerHTML=`<header class="page-heading"><div><h1>${t('machines')}</h1><p class="page-subtitle">${t('machineSubtitle')}</p></div>${button('refresh-machines',`${icon('refresh')}${t('refresh')}`,ui.online?'':'disabled')}</header><div class="machine-page"><p class="hint mb-16">${t('pollNote')} · ${t('lastSync')} ${time(ui.machineSync)}</p><div class="machine-grid">${ui.machines.map(m=>`<article class="machine-card"><div class="flex between"><h2>${esc(m.name)}</h2>${badge(m.state)}</div><div class="machine-specs"><div><span class="meta-label">${t('slots')}</span><span class="mono">${m.active} / ${m.slots}</span><div class="load"><span data-fill="${Math.min(m.active/m.slots*100,100)}"></span></div></div><div><span class="meta-label">${t('queueCount')}</span><span class="mono">${m.queued}</span></div><div><span class="meta-label">${t('os')}</span><span class="mono">${esc(m.os||'—')}</span></div><div><span class="meta-label">${t('version')}</span><span class="mono">${esc(m.version||'—')}</span></div><div><span class="meta-label">${t('hostname')}</span><span class="mono">${esc(m.hostname||'—')}</span></div><div><span class="meta-label">${t('nextRetry')}</span><span class="hint">${m.retry_at?time(m.retry_at):m.state==='connected'?'—':t('noRetry')}</span></div></div>${m.error?`<div class="machine-error"><strong>${esc(m.error)}</strong> · ${esc(m.detail)}</div>`:''}<div class="mt-12">${button('machine-tasks',t('viewTasks'),`data-machine="${m.name}"`,'quiet')}</div></article>`).join('')||statePanel('machine',t('noMachines'),t('noMachinesHelp'))}</div><section class="agent-section"><div class="flex between"><h2>${t('agentsTitle')}</h2><small class="muted">${t('readonly')}</small></div><p class="hint">${t('configNote')}</p><div class="agent-list">${ui.agents.map(a=>`<div class="agent-row"><strong class="mono">${esc(a.name)}</strong><span>${esc(a.provider)} · ${esc(a.model||'—')}</span><span>${t('permissions')}: ${esc(a.permission||'—')}</span><span>${a.machine?esc(a.machine):t('allHosts')}</span></div>`).join('')}</div></section></div>`;
  for(const bar of el.querySelectorAll('[data-fill]'))bar.style.width=`${bar.dataset.fill}%`; // CSP blocks style attributes
}

function showModal(type,title,body,footer,wide=false) {
  if(!modal.open)ui.modalOpener=document.activeElement;
  ui.modalType=type;ui.modalDirty=false;ui.modalReturn=null;
  modal.style.width=wide?'min(700px, calc(100vw - 20px))':'';
  modal.innerHTML=`<div class="modal-header"><h2 id="modal-title">${title}</h2>${button('close-modal',icon('close'),`aria-label="${t('close')}"`,'quiet icon-button')}</div>${body}${footer}`;
  if(!modal.open)modal.showModal();
  queueMicrotask(()=>modal.querySelector('[autofocus]')?.focus());
}
function restoreTaskForm() {
  const previous=ui.modalReturn;if(!previous)return;modal.replaceChildren(previous.fragment);ui.modalType=previous.type;ui.modalTask=previous.task;ui.modalDirty=true;ui.modalReturn=null;modal.querySelector('input')?.focus();
}
function closeModal(force=false) {
  if(ui.modalSubmitting&&!force)return;
  if(ui.modalType==='discard'&&ui.modalReturn&&!force){restoreTaskForm();return;}
  if(ui.modalDirty&&!force) {
    const fragment=document.createDocumentFragment();while(modal.firstChild)fragment.append(modal.firstChild);
    const restore={fragment,type:ui.modalType,task:ui.modalTask};
    showModal('discard',t('dirtyTitle'),`<div class="modal-body"><p>${t('dirtyHelp')}</p></div>`,`<footer class="modal-footer">${button('keep-editing',t('keepEditing'),'autofocus')}${button('discard',t('discard'),'','danger')}</footer>`);ui.modalReturn=restore;return;
  }
  modal.close();ui.modalType='';ui.modalDirty=false;ui.modalReturn=null;const opener=ui.modalOpener;if(opener?.isConnected)opener.focus();else document.querySelector(`#row-${ui.task}`)?.focus();
}
function modalError(message) {const el=modal.querySelector('.form-error');if(el){el.hidden=false;el.textContent=message;el.scrollIntoView({block:'nearest'});}}
function machineOptions(value,optional=false) {
  return (optional?`<option value="">${t('unspecified')}</option>`:'')+ui.machines.map(m=>`<option value="${m.name}" ${value===m.name?'selected':''}>${m.name} · ${t(m.state)} · ${m.active}/${m.slots}</option>`).join('');
}
function agentOptions(value,optional=false) {
  return (optional?`<option value="">${t('unspecified')}</option>`:'')+ui.agents.map(a=>`<option value="${a.name}" ${value===a.name?'selected':''}>${a.name} · ${a.provider}${a.machine?' · '+a.machine:''}</option>`).join('');
}
async function openTaskForm(edit=false) {
  if(!ui.online)return toast(t('offline'));
  let task=edit?currentTask():{title:'',brief:'',dir:'',machine:'',agent:''};
  if(edit){try{task=await api.taskGet({id:task.id});ui.briefs.set(task.id,task.brief||'');}catch(error){toast(errorText(error));return;}}
  ui.modalTask=edit?task.id:'';
  showModal('task-form',t(edit?'editTask':'newTask'),`<form id="task-form" data-edit="${edit}" data-id="${edit?task.id:''}" data-command="${commandID()}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div>
    ${edit?`<p class="notice">${t('editHint')}</p>`:''}<label>${t('title')}<input name="title" id="task-title" required value="${esc(task.title)}" placeholder="${t('titlePlaceholder')}" autofocus></label>
    <label>${t('brief')}<textarea name="brief" id="task-brief" rows="8" placeholder="${t('briefPlaceholder')}">${esc(task.brief||'')}</textarea><small>${t('briefHint')} · <span id="brief-bytes">${new TextEncoder().encode(task.brief||'').length}</span> B</small></label>
    <details><summary class="pointer">${t('preview')}</summary><div id="brief-preview" class="preview brief">${markdown(task.brief||'')}</div><small class="muted">${t('safeMarkdown')}</small></details>
    <label>${t('directory')}<input name="dir" class="mono" value="${esc(task.dir)}" placeholder="/work/project"><small>${t('dirHint')}</small></label>
    <div class="form-grid"><label>${t('defaultMachine')}<select name="machine">${machineOptions(task.machine,true)}</select></label><label>${t('defaultAgent')}<select name="agent">${agentOptions(task.agent,true)}</select></label></div></div>
    <footer class="modal-footer">${button('close-modal',t('cancel'))}<button type="submit" class="primary">${t(edit?'save':'create')}</button></footer></form>`,'',true);
}
const hints = {cli_missing:'hint.cli_missing', auth_missing:'hint.auth', auth:'hint.auth', quota:'hint.quota', rate_limit:'hint.later',
  overloaded:'hint.later', network:'hint.later', context_overflow:'hint.context_overflow', session_missing:'hint.session_missing', node_outdated:'hint.node_outdated'};
// runFacts: why the run ended, what it asked or last noted, what to do next, and a reply box when its session can go on.
function runFacts(run, terminal, writeDisabled) {
  const lines=[];
  if(attention(run))lines.push(`<div>${badge(attention(run))}</div>`);
  if(terminal&&run.reason&&words['reason.'+run.reason])lines.push(`<div>${t('reason.'+run.reason)}${run.detail?` · <code>${esc(run.detail)}</code>`:''}</div>`);
  else if(terminal&&run.detail)lines.push(`<div><code>${esc(run.detail)}</code></div>`);
  const asking=openStates.has(run.state)&&(run.requests||[]).some(q=>q.kind==='question');
  if(run.ask&&!asking)lines.push(`<div><strong>${t('question')}</strong><article class="brief">${markdown(run.ask)}</article></div>`);
  else if(run.note&&!terminal)lines.push(`<div><strong>${t('progress')}</strong> ${esc(run.note)}</div>`);
  else if(run.last&&!terminal)lines.push(`<div><strong>${t('lastSaid')}</strong> ${esc(run.last)}</div>`);
  if(usageText(run.usage))lines.push(`<div class="muted">${usageText(run.usage)}</div>`);
  if(terminal&&hints[run.reason])lines.push(`<div class="hint">${t('nextStep')}: ${t(hints[run.reason])}</div>`);
  const asks=openStates.has(run.state)?(run.requests||[]).map(q=>requestForm(run,q,writeDisabled)).join(''):'';
  const sends=(run.sends||[]).slice(-3).map(m=>`<div class="muted">${t('message')} · ${t('send.'+m.state)}: ${esc(m.text)}</div>`).join('');
  const send=run.state==='running'&&run.stream?`<form id="send-form" class="stack mt-10" data-run="${esc(run.id)}" data-command="${commandID()}"><div class="form-error" role="alert" hidden></div><label class="sr-only" for="send-text">${t('sendMessage')}</label><textarea name="text" id="send-text" rows="2" placeholder="${t('sendPlaceholder')}" ${writeDisabled}>${esc(ui.drafts.get('send:'+run.id)||'')}</textarea><div class="flex"><button type="submit" ${writeDisabled}>${t('sendMessage')}</button></div></form>`:'';
  const reply=terminal&&run.session&&!openRun(run.task)?`<form id="reply-form" class="stack mt-10" data-run="${esc(run.id)}" data-command="${commandID()}"><div class="form-error" role="alert" hidden></div><label class="sr-only" for="reply-text">${t('reply')}</label><textarea name="text" id="reply-text" rows="3" placeholder="${t('replyPlaceholder')}" ${writeDisabled}>${esc(ui.drafts.get(run.id)||'')}</textarea><div class="flex"><button class="${waiting(run)?'primary':''}" type="submit" ${writeDisabled}>${t('reply')}</button></div></form>`:'';
  return lines.length||reply||asks||send||sends?`<div class="run-alert stack">${lines.join('')}${asks}${sends}${send}${reply}</div>`:'';
}
// requestForm: what a running run waits on — a tool to allow or deny, or questions to answer by picking an option.
function requestForm(run, q, writeDisabled) {
  const head=`<form class="answer-form stack" data-run="${esc(run.id)}" data-request="${esc(q.id)}" data-command="${commandID()}"><div class="form-error" role="alert" hidden></div>`;
  if(q.kind==='question'){
    const qs=(q.questions||[]).map((x,i)=>`<fieldset class="stack"><legend>${esc(x.header?x.header+' · ':'')}${esc(x.question)}</legend>${(x.options||[]).map((o,j)=>{const key=`${q.id}/${i}`,checked=(ui.choices.get(key)??(x.options||[])[0])===o;return `<label class="choice"><input type="radio" name="q${i}" value="${esc(o)}" data-choice="${esc(key)}" ${checked?'checked':''} ${writeDisabled}>${esc(o)}</label>`;}).join('')}</fieldset>`).join('');
    return `${head}<strong>${t('itAsks')}</strong>${qs}<div class="flex"><button class="primary" type="submit" data-allow="1" ${writeDisabled}>${t('answer')}</button><button type="submit" data-allow="0" ${writeDisabled}>${t('noAnswer')}</button></div></form>`;
  }
  return `${head}<strong>${t('wantsTool').replace('{0}',esc(q.tool))}</strong><pre class="mono"><code>${esc(q.summary||'')}</code></pre><div class="flex"><button class="primary" type="submit" data-allow="1" ${writeDisabled}>${t('allow')}</button><button type="submit" data-allow="0" ${writeDisabled}>${t('deny')}</button></div></form>`;
}
async function submitAnswer(form, submitter) {
  const run=ui.state.runs[form.dataset.run],q=(run?.requests||[]).find(x=>x.id===form.dataset.request);if(!q)return;
  const allow=submitter?.dataset.allow!=='0',answer={run:run.id,request:q.id,allow};
  if(allow&&q.kind==='question'){const data=new FormData(form);answer.answers=Object.fromEntries((q.questions||[]).map((x,i)=>[x.question,data.get('q'+i)||'']));}
  const result=await api.runAnswer(answer,{command_id:form.dataset.command});
  ui.state.runs[result.id]=result;renderPage();toast(t('answered'));
}
async function submitSend(form) {
  const text=new FormData(form).get('text')||'';
  if(!text.trim()){const e=form.querySelector('.form-error');e.hidden=false;e.textContent=t('replyEmpty');return;}
  const result=await api.runSend({run:form.dataset.run,text},{command_id:form.dataset.command});
  ui.drafts.delete('send:'+form.dataset.run);ui.state.runs[result.id]=result;renderPage();toast(t('messageQueued'));
}
async function submitReply(form) {
  const text=new FormData(form).get('text')||'';
  if(!text.trim()){const e=form.querySelector('.form-error');e.hidden=false;e.textContent=t('replyEmpty');return;}
  const result=await api.runContinue({run:form.dataset.run,text},{command_id:form.dataset.command});
  ui.drafts.delete(form.dataset.run);ui.state.runs[result.id]=result;ui.run=result.id;ui.tab='output';ui.follow=true;ui.raw=false;ui.pending=0;
  renderPage();await fetchOutput();toast(t('replied'));
}
function dispatchProblem(task) {
  const open=openRun(task.id);
  if(open)return `${t('openRunReason')}${open.id} (${t(open.state)})。${t('resolveRun')}`;
  if(task.status!=='todo')return t('reopenReason');
  if(!task.dir)return t('missingDir');
  if(!ui.online)return t('lostHelp');return '';
}
function openDispatch() {
  const task=currentTask();if(!task)return;
  const problem=dispatchProblem(task),chosenMachine=task.machine||ui.machines.find(m=>m.state==='connected')?.name||'',chosenAgent=task.agent||ui.agents[0]?.name||'';
  showModal('dispatch',t('dispatchRun'),`<form id="dispatch-form" data-command="${commandID()}" data-task="${task.id}"><div class="modal-body stack"><div class="form-error" role="alert" hidden></div><div><strong>${esc(task.title)}</strong><div class="mono muted">${task.id}</div></div>
    ${problem?`<div class="notice" role="status" id="dispatch-block">${esc(problem)}</div>`:''}<div class="form-grid"><label>${t('machine')}<select name="machine" id="dispatch-machine" required>${machineOptions(chosenMachine)}</select></label><label>${t('agent')}<select name="agent" id="dispatch-agent" required>${agentOptions(chosenAgent)}</select></label></div>
    <div class="confirm-context"><span class="meta-label">${t('directory')}</span><code>${esc(task.dir||'—')}</code></div><div id="dispatch-advice" class="notice" hidden></div><div id="dispatch-preview" class="hint stack" aria-live="polite"></div><p class="hint">${t('frozenHelp')}</p></div>
    <footer class="modal-footer">${button('close-modal',t('cancel'),'autofocus')}<button class="primary" id="dispatch-submit" type="submit" ${problem?'disabled':''}>${t('enqueue')}</button></footer></form>`,'');
  refreshDispatchAdvice();
}
function refreshDispatchAdvice() {
  const form=document.querySelector('#dispatch-form');if(!form)return;
  const data=new FormData(form),task=ui.state.tasks[form.dataset.task],machine=ui.machines.find(m=>m.name===data.get('machine')),agent=ui.agents.find(a=>a.name===data.get('agent'));
  const wrong=agent?.machine&&agent.machine!==machine?.name,problem=dispatchProblem(task);
  const occupied=Object.values(ui.state.runs).some(r=>r.machine===machine?.name&&r.dir===task.dir&&openStates.has(r.state));
  const advice=wrong?t('agentMismatch')+' '+agent.machine:machine?.state!=='connected'?t('offlineQueue'):machine.active>=machine.slots||occupied?t('capacityQueue'):'';
  const el=document.querySelector('#dispatch-advice');el.hidden=!advice;el.textContent=advice;
  document.querySelector('#dispatch-submit').disabled=Boolean(problem||wrong||!machine||!agent);
  const block=document.querySelector('#dispatch-block');if(block)block.textContent=problem;
  if(problem&&!block){const warning=document.createElement('div');warning.className='notice';warning.id='dispatch-block';warning.textContent=problem;form.querySelector('.modal-body').prepend(warning);}
  if(!problem&&!wrong&&machine&&agent)previewDispatch(form,task.id,machine.name,agent.name);
}
// previewDispatch asks the coordinator how the run would go (the machine's agent CLI, the queue) and lists it.
async function previewDispatch(form, task, machine, agent) {
  const el=form.querySelector('#dispatch-preview'),key=`${task}/${machine}/${agent}`;if(!el||el.dataset.key===key)return;
  el.dataset.key=key;el.textContent=t('checking');
  try{
    const pv=await api.runPreview({task,machine,agent,runner:'background'});
    if(el.dataset.key!==key)return;
    el.replaceChildren(...[...(pv.blockers||[]).map(w=>['notice',why(w)]),...(pv.notes||[]).map(w=>['',why(w)])].map(([cls,text])=>{const d=document.createElement('div');if(cls)d.className=cls;d.textContent=text;return d;}));
    if(pv.check){const d=document.createElement('div');d.textContent=`${pv.provider} ${pv.check.version||''}`;el.prepend(d);}
  }catch(error){if(el.dataset.key===key)el.textContent=errorText(error);}
}
function confirmAction(action) {
  const task=currentTask(),run=selectedRun();
  const config={
    stop:{title:'stopTitle',body:run?.state==='queued'?'stopQueuedHelp':'stopHelp',label:'stop',danger:true},
    abandon:{title:'abandonTitle',body:'abandonHelp',label:'abandon',danger:true},
    'cancel-task':{title:'cancelTitle',body:'cancelHelp',label:'cancelTask',danger:true},
    done:{title:'markDone',body:'doneHelp',label:'markDone'},
    logout:{title:'logoutTitle',body:'logoutConfirm',label:'logout'}
  }[action];
  const id=['stop','abandon'].includes(action)?run?.id:task?.id;
  showModal('confirm',t(config.title),`<form id="confirm-form" data-command-action="${action}" data-id="${id||''}" data-command="${commandID()}"><div class="modal-body"><div class="form-error" role="alert" hidden></div><p>${t(config.body)}</p>${action!=='logout'?`<div class="confirm-context"><strong>${esc(task.title)}</strong><div class="mono muted">${esc(id)}${['stop','abandon'].includes(action)?` · ${esc(run.machine)} / ${esc(run.agent)}`:''}</div></div>`:''}</div><footer class="modal-footer">${button('close-modal',t('cancel'),'autofocus')}<button class="primary ${config.danger?'danger':''}" type="submit">${t(config.label)}</button></footer></form>`,'');
}
async function submitTask(form) {
  const data=Object.fromEntries(new FormData(form));data.title=data.title.trim();
  if(!data.title)return modalError(t('badTitle'));
  if(new TextEncoder().encode(data.brief).length>256*1024)return modalError(t('badBrief'));
  const editing=form.dataset.edit==='true';
  if(editing)data.id=form.dataset.id;
  const result=await api[editing?'taskEdit':'taskCreate'](data,{command_id:form.dataset.command});
  ui.state.tasks[result.id]=result;ui.briefs.set(result.id,result.brief||'');ui.task=result.id;ui.focusTask=result.id;ui.tab='brief';ui.mobileDetail=true;ui.search='';ui.status='';ui.machine='';ui.runState='';ui.run=latestRun(result.id)?.id||'';
  closeModal(true);renderShell();toast(t(editing?'saved':'created'));
}
async function submitDispatch(form) {
  const params=Object.fromEntries(new FormData(form));params.task=form.dataset.task;params.runner='background';
  const result=await api.runDispatch(params,{command_id:form.dataset.command});ui.state.runs[result.id]=result;ui.run=result.id;ui.tab='output';ui.follow=true;ui.raw=false;ui.pending=0;
  closeModal(true);renderPage();await fetchOutput();toast(t('dispatched'));
}
async function submitConfirm(form) {
  const action=form.dataset.commandAction,id=form.dataset.id,options={command_id:form.dataset.command};
  if(action==='logout') {
    await api.logout();ui.unsubscribe?.();ui.authenticated=false;ui.loggedOut=true;ui.online=true;ui.outputs.clear();ui.chats.clear();ui.briefs.clear();ui.state={seq:0,tasks:{},runs:{}};clearTimeout(ui.reconnectTimer);closeModal(true);renderShell();return;
  }
  const method=action==='stop'?'runStop':action==='abandon'?'runAbandon':'taskSetStatus';
  const params=method==='taskSetStatus'?{id,status:action==='done'?'done':'canceled'}:{id};
  const result=await api[method](params,options);
  if(method==='taskSetStatus')ui.state.tasks[id]=result;else ui.state.runs[id]=result;
  closeModal(true);renderPage();toast(t(action==='stop'?(result.state==='canceled'?'cancelledQueue':'pendingStop'):action==='abandon'?'abandonedToast':'statusSaved'));
}
async function setTaskStatus(status) {
  try{const result=await api.taskSetStatus({id:ui.task,status},{command_id:commandID()});ui.state.tasks[ui.task]=result;renderPage();toast(t('statusSaved'));}catch(error){toast(errorText(error));}
}
async function login(token) {
  const tokenInput=document.querySelector('#token-input');if(tokenInput)tokenInput.value='';
  try{await api.login(token);await enter();}
  catch(error){const el=document.querySelector('#login-error');if(el){el.hidden=false;el.textContent=t(error.code==='unauthorized'?'authInvalid':'offline');}}
}
// enter shows the workspace of the session the browser holds.
async function enter() {
  const who=await api.session();ui.who=who?.name||'';
  ui.authenticated=true;ui.online=true;ui.loggedOut=false;ui.loading=true;renderShell();await refreshData();
}
async function refreshData() {
  ui.error='';
  try {
    const [state,machines,agents]=await Promise.all([api.stateGet({no_briefs:true}),api.machineList({}),api.agentList({})]);
    if(!ui.authenticated)return;
    ui.state=state;ui.machines=machines.machines;ui.agents=agents.agents;ui.lastSync=new Date();ui.machineSync=new Date();ui.loading=false;
    if(!ui.state.tasks[ui.task])ui.task=Object.keys(ui.state.tasks)[0]||'';
    ui.focusTask=ui.task;ui.unsubscribe?.();ui.unsubscribe=api.subscribe({after_seq:state.seq},receiveJournal);
    renderShell();await loadDetail();
  } catch(error){ui.loading=false;ui.error=errorText(error);renderPage();}
}
let journalRenderTimer;
function receiveJournal(envelope) {
  if(envelope.seq<=ui.state.seq)return;
  for(const event of envelope.events)if(event.type==='task_edited'&&event.data.brief!==undefined)ui.briefs.delete(event.data.id);
  const edited=envelope.events.find(e=>e.type==='task_edited'&&e.data.id===ui.modalTask);
  if(edited&&modal.open&&ui.modalType==='task-form')modalError(t('pushed')+' · '+t('editHint'));
  clearTimeout(journalRenderTimer);journalRenderTimer=setTimeout(syncState,100);
}
// syncState reads the state the coordinator folded (the page never folds events itself).
async function syncState() {
  try{
    const state=await api.stateGet({no_briefs:true});if(!ui.authenticated)return;
    ui.state=state;ui.lastSync=new Date();refreshDispatchAdvice();preserveRender();
    if(!ui.briefs.has(ui.task))await loadDetail();else if(ui.tab==='output')fetchOutput();
  }catch(error){if(error.code!=='closed')toast(errorText(error));}
}
function preserveRender() {
  const active=document.activeElement,id=active?.id,selection=active instanceof HTMLInputElement?[active.selectionStart,active.selectionEnd]:null;
  const positions=['task-list','output-view','chat-list'].map(id=>[id,document.getElementById(id)?.scrollTop||0]);
  const opened=[...document.querySelectorAll('#task-detail details[open]')].map(el=>el.dataset.tool||'snapshot');
  renderCounts();renderPage();
  for(const [key,top]of positions){const el=document.getElementById(key);if(el)el.scrollTop=key==='output-view'&&ui.follow?el.scrollHeight:top;}
  for(const key of opened){const el=key==='snapshot'?document.querySelector('.history-note'):document.querySelector(`[data-tool="${key}"]`);el?.setAttribute('open','');}
  if(id&&!modal.open){const next=document.getElementById(id);next?.focus({preventScroll:true});if(selection&&next instanceof HTMLInputElement&&selection[0]!==null)next.setSelectionRange(...selection);}
}
async function selectTask(id) {
  ui.task=id;ui.focusTask=id;ui.run=latestRun(id)?.id||'';ui.mobileDetail=true;ui.detailError='';ui.follow=true;ui.pending=0;renderPage();await loadDetail();
}
async function loadDetail() {
  const id=ui.task;if(!id)return;const generation=++ui.requestGeneration;
  try {
    if(!ui.briefs.has(id)){const task=await api.taskGet({id});if(generation!==ui.requestGeneration)return;ui.briefs.set(id,task.brief||'');ui.state.tasks[id]=task;}
    if(generation!==ui.requestGeneration)return;ui.detailError='';renderDetail();
    if(ui.tab==='output')await fetchOutput();if(ui.tab==='conversation')await fetchChat();
  }catch(error){ui.detailError=errorText(error);if(ui.tab==='brief')renderDetail();else toast(ui.detailError);}
}
async function fetchOutput(older=false) {
  const run=selectedRun();if(!run||!ui.online||!ui.authenticated)return;
  const key=`tail-${run.id}`;if(ui.busy.has(key))return;
  ui.busy.add(key);if(older)ui.busy.add('older-output');
  const current=ui.outputs.get(run.id),encoder=new TextEncoder(),decoder=new TextDecoder();
  try{
    let page=await api.runTail({run:run.id,before:older?current?.from??-1:-1,max:65536,file:current?.file});
    if(!ui.authenticated)return;
    let result=page;
    if(current?.file===page.file&&!current.error) {
      const oldBytes=encoder.encode(current.text);let bytes=encoder.encode(page.text);
      if(older){result={...page,text:page.text+current.text};if(ui.frozenOutput?.id===run.id)ui.frozenOutput.data={...page,text:page.text+ui.frozenOutput.data.text};ui.prepending=true;}
      else {
        const end=current.from+oldBytes.length;let attempts=0;
        while(page.from>end&&!page.done&&attempts++<20){const previous=await api.runTail({run:run.id,before:page.from,max:65536,file:page.file});page={...previous,text:previous.text+page.text};}
        bytes=encoder.encode(page.text);
        if(page.from<=end&&page.from+bytes.length>=current.from){const delta=decoder.decode(bytes.slice(Math.max(0,end-page.from)));result={...current,text:current.text+delta};if(delta&&!ui.follow)ui.pending+=delta.split('\n').filter(Boolean).length;}
        else result=page;
      }
    }
    ui.outputs.set(run.id,result);
    if(selectedRun()?.id===run.id&&(ui.follow||older||!current)){renderOutput();}
    else updateFollowLabel();
  }catch(error){if(!current)ui.outputs.set(run.id,{error:errorText(error)});if(selectedRun()?.id===run.id)renderOutput();if(current)toast(errorText(error));}
  finally{ui.busy.delete(key);ui.busy.delete('older-output');}
}
async function fetchChat(older=false) {
  const run=selectedRun();if(!run?.session||!ui.online)return;
  const current=ui.chats.get(run.id),key=`chat-${run.id}`;if(ui.busy.has(key))return;
  ui.busy.add(key);if(older)ui.busy.add('older-chat');
  const el=document.querySelector('#chat-list'),oldHeight=el?.scrollHeight||0,oldTop=el?.scrollTop||0;
  try{
    const page=await api.nodeCall({machine:run.machine,method:'messages',params:{provider:run.provider,session_id:run.session,before:older?current?.before??-1:-1,n:40,file:current?.file}});
    if(!ui.authenticated)return;
    ui.chats.set(run.id,{...page,messages:older?[...page.messages,...(current?.messages||[])]:page.messages});
  }catch(error){ui.chats.set(run.id,{error:errorText(error)});}
  finally{ui.busy.delete(key);ui.busy.delete('older-chat');}
  if(selectedRun()?.id===run.id&&ui.tab==='conversation') {renderDetail();const list=document.querySelector('#chat-list');if(list)list.scrollTop=older?oldTop+list.scrollHeight-oldHeight:list.scrollHeight;}
}
async function refreshMachines() {
  if(!ui.online||!ui.authenticated)return;
  try{const result=await api.machineList({});if(!ui.authenticated)return;ui.machines=result.machines;ui.machineSync=new Date();renderCounts();if(ui.page==='machines')preserveRender();refreshDispatchAdvice();}
  catch(error){toast(errorText(error));}
}
function disconnect() {
  if(!ui.authenticated||!ui.online)return;
  ui.online=false;ui.lastSync=new Date();renderShell();refreshDispatchAdvice();
  ui.retryWait=1000;clearTimeout(ui.reconnectTimer);ui.reconnectTimer=setTimeout(reconnect,ui.retryWait);
}
async function reconnect() {
  if(ui.reconnecting||!ui.authenticated)return;ui.reconnecting=true;renderBanner();clearTimeout(ui.reconnectTimer);
  try{
    await api.session();await api.connect();
    ui.online=true;ui.reconnecting=false;ui.briefs.clear();await refreshData();toast(t('live'));
  }catch(error){
    ui.reconnecting=false;
    if(error.code==='unauthorized'){ui.authenticated=false;ui.loggedOut=true;renderShell();return;}
    renderBanner();ui.retryWait=Math.min((ui.retryWait||1000)*2,30000);
    ui.reconnectTimer=setTimeout(reconnect,ui.retryWait*(0.8+Math.random()*0.4));
  }
}
api.onClose(disconnect);
function showKeyboard() {
  showModal('keyboard',t('keyboard'),`<div class="modal-body stack"><p class="hint">${t('keyboardHelp')}</p>${[['/','search'],['n','newTask'],['↑ ↓','move'],['Enter','viewDetails'],['Esc','shortcutClose'],['?','keyboard']].map(([key,label])=>`<div class="flex between"><span>${t(label)}</span><kbd>${key}</kbd></div>`).join('')}</div>`,`<footer class="modal-footer">${button('close-modal',t('close'),'autofocus')}</footer>`);
}

document.addEventListener('submit',async event=>{
  event.preventDefault();const form=event.target,submitter=event.submitter||form.querySelector('[type="submit"]');if(submitter?.disabled)return;
  if(submitter)submitter.disabled=true;ui.modalSubmitting=form.id!=='login-form';
  try{
    if(form.id==='login-form')await login(new FormData(form).get('token'));
    else if(form.id==='task-form')await submitTask(form);
    else if(form.id==='dispatch-form')await submitDispatch(form);
    else if(form.id==='confirm-form')await submitConfirm(form);
    else if(form.id==='reply-form')await submitReply(form);
    else if(form.id==='send-form')await submitSend(form);
    else if(form.classList.contains('answer-form'))await submitAnswer(form,submitter);
  }catch(error){if(form.id==='reply-form'||form.id==='send-form'||form.classList.contains('answer-form')){const e=form.querySelector('.form-error');e.hidden=false;e.textContent=errorText(error);}else modalError(errorText(error));if(error.code==='conflict'){const state=await api.stateGet({no_briefs:true});ui.state=state;refreshDispatchAdvice();}}
  finally{ui.modalSubmitting=false;if(submitter?.isConnected)submitter.disabled=false;if(form.id==='dispatch-form')refreshDispatchAdvice();}
});
document.addEventListener('click',async event=>{
  const target=event.target.closest('button[data-action]');if(!target||target.disabled)return;
  const action=target.dataset.action;
  try{
    switch(action){
      case 'language':lang=lang==='zh'?'en':'zh';applyPreferences();renderShell();break;
      case 'logout':confirmAction('logout');break;
      case 'page':ui.page=target.dataset.page;renderShell();if(ui.page==='tasks')await loadDetail();break;
      case 'new':await openTaskForm();break;
      case 'edit':await openTaskForm(true);break;
      case 'select-task':await selectTask(target.dataset.id);break;
      case 'tab':ui.tab=target.dataset.tab;renderDetail();if(ui.tab==='conversation')await fetchChat();if(ui.tab==='output')await fetchOutput();break;
      case 'select-run':ui.run=target.dataset.id;ui.tab='output';ui.follow=true;ui.pending=0;renderDetail();await fetchOutput();break;
      case 'dispatch':openDispatch();break;
      case 'stop':case 'abandon':case 'cancel-task':confirmAction(action);break;
      case 'done':if(openRun(ui.task))confirmAction('done');else await setTaskStatus('done');break;
      case 'reopen':await setTaskStatus('todo');break;
      case 'close-modal':closeModal();break;
      case 'discard':closeModal(true);break;
      case 'keep-editing':restoreTaskForm();break;
      case 'toggle-raw':ui.raw=!ui.raw;renderDetail();break;
      case 'toggle-follow':if(ui.follow)pauseFollow();else{ui.follow=true;ui.pending=0;ui.frozenOutput=null;renderOutput();}updateFollowLabel();break;
      case 'older-output':pauseFollow();await fetchOutput(true);break;
      case 'older-chat':await fetchChat(true);break;
      case 'retry-output':ui.outputs.delete(selectedRun().id);await fetchOutput();break;
      case 'retry-chat':ui.chats.delete(selectedRun().id);await fetchChat();break;
      case 'retry-detail':ui.briefs.delete(ui.task);await loadDetail();break;
      case 'needs-you':ui.runState='needs';ui.status='';renderPage();break;
      case 'clear-filters':ui.search='';ui.status='';ui.machine='';ui.runState='';renderPage();break;
      case 'retry-data':ui.loading=true;renderPage();await refreshData();break;
      case 'back-tasks':ui.mobileDetail=false;renderPage();document.querySelector(`#row-${ui.task}`)?.focus();break;
      case 'refresh-machines':await refreshMachines();toast(t('loaded'));break;
      case 'machine-tasks':ui.page='tasks';ui.machine=target.dataset.machine;ui.status='';ui.runState='';ui.search='';ui.mobileDetail=false;renderShell();break;
      case 'reconnect':await reconnect();break;
      case 'keyboard':showKeyboard();break;
    }
  }catch(error){toast(errorText(error));}
});
document.addEventListener('input',event=>{
  const el=event.target;
  if(el.id==='search-input'){ui.search=el.value;renderTaskList();}
  if(el.id==='reply-text')ui.drafts.set(el.closest('form').dataset.run,el.value);
  if(el.id==='send-text')ui.drafts.set('send:'+el.closest('form').dataset.run,el.value);
  if(el.dataset.choice)ui.choices.set(el.dataset.choice,el.value);
  if(modal.open&&ui.modalType==='task-form'){
    ui.modalDirty=true;
    if(el.id==='task-brief'){document.querySelector('#brief-bytes').textContent=new TextEncoder().encode(el.value).length;document.querySelector('#brief-preview').innerHTML=markdown(el.value);}
  }
});
document.addEventListener('change',async event=>{
  const el=event.target;
  if(el.id==='theme-select'){theme=el.value;applyPreferences();}
  else if(el.id==='status-filter'){ui.status=el.value;renderTaskList();}
  else if(el.id==='machine-filter'){ui.machine=el.value;renderTaskList();}
  else if(el.id==='run-filter'){ui.runState=el.value;renderTaskList();}
  else if(el.id==='run-select'){ui.run=el.value;ui.follow=true;ui.pending=0;renderDetail();if(ui.tab==='conversation')await fetchChat();else await fetchOutput();}
  else if(el.id==='dispatch-machine'||el.id==='dispatch-agent')refreshDispatchAdvice();
  if(modal.open&&ui.modalType==='task-form')ui.modalDirty=true;
});
modal.addEventListener('cancel',event=>{event.preventDefault();closeModal();});
document.addEventListener('keydown',event=>{
  if(event.isComposing||event.metaKey||event.ctrlKey||event.altKey)return;
  if(modal.open)return;
  const textInput=event.target.matches('input,textarea,select,[contenteditable="true"]');
  if(textInput){if(event.key==='Escape'){event.target.blur();event.preventDefault();}return;}
  if(!ui.authenticated)return;
  if(event.key==='/'||event.key==='、'){event.preventDefault();ui.page='tasks';ui.mobileDetail=false;if(!document.querySelector('#search-input'))renderShell();else renderPage();document.querySelector('#search-input')?.focus();}
  else if(event.key==='n'){event.preventDefault();openTaskForm();}
  else if(event.key==='?'||event.key==='？'){event.preventDefault();showKeyboard();}
  else if(event.key==='Escape'){event.preventDefault();ui.mobileDetail=false;renderPage();document.querySelector(`#row-${ui.task}`)?.focus();}
  else if(event.target.matches('[role="tab"]')&&['ArrowLeft','ArrowRight'].includes(event.key)){
    event.preventDefault();const tabs=['output','conversation','brief','history'],index=tabs.indexOf(ui.tab);ui.tab=tabs[(index+(event.key==='ArrowRight'?1:3))%4];renderDetail();document.querySelector(`#tab-${ui.tab}`)?.focus();if(ui.tab==='conversation')fetchChat();if(ui.tab==='output')fetchOutput();
  } else if(ui.page==='tasks'&&['ArrowDown','ArrowUp'].includes(event.key)&&!event.target.closest('.output-view,.chat-list')){
    event.preventDefault();const rows=filteredTasks();if(!rows.length)return;const index=rows.findIndex(task=>task.id===ui.focusTask),next=Math.min(rows.length-1,Math.max(0,index+(event.key==='ArrowDown'?1:-1)));ui.focusTask=rows[next].id;renderTaskList();document.querySelector(`#row-${ui.focusTask}`)?.focus();
  } else if(event.key==='Enter'&&event.target===document.body&&ui.focusTask){event.preventDefault();selectTask(ui.focusTask);}
});
window.addEventListener('beforeunload',event=>{if(ui.modalDirty){event.preventDefault();event.returnValue='';}});
setInterval(()=>{
  if(!ui.authenticated||!ui.online||document.hidden)return;
  if(ui.page==='tasks'&&ui.tab==='output'&&openStates.has(selectedRun()?.state))fetchOutput();
},2000);
setInterval(()=>{if(ui.authenticated&&ui.online&&!document.hidden)refreshMachines();},5000);
document.addEventListener('visibilitychange',()=>{if(!document.hidden&&ui.authenticated&&ui.online){refreshMachines();if(ui.tab==='output')fetchOutput();}});
applyPreferences();renderShell();
api.session().then(enter,()=>{});
