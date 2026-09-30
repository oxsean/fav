// project is a project's drawer on the team page: its members, and for its owner or an admin on a computer its
// settings (what every run is told, repositories and where they are checked out, default machine, the agent for each
// role, workflow, hooks, its own workflows) and its issue sync (a repository bound, how it syncs, the issues it
// follows and the progress comment each would get).
import {useState, useEffect} from '../vendor/hooks.mjs';
import {html, cx, useWords, useName} from '../ui/base.js';
import {Modal, Drawer} from '../ui/overlay.js';
import {Button, Check, Segmented, Tabs} from '../ui/controls.js';
import {TextInput, TextArea} from '../ui/input.js';
import {Picker} from '../ui/picker.js';
import {Markdown} from '../ui/markdown.js';
import {Secret} from '../ui/secret.js';
import {register} from '../core/i18n.js';
import {clock, day} from '../core/format.js';
import {workflows as builtinFlows} from '../core/proto.js';
import * as tm from '../core/team.js';
import {apiText} from './words.js';
import './taskwords.js';

register('project', {
  'proj.members': ['成员', 'Members'], 'proj.settings': ['设置', 'Settings'], 'proj.sync': ['工单同步', 'Issue sync'],
  'proj.name': ['名称', 'Name'], 'proj.owner': ['负责人', 'Owner'],
  'proj.context': ['项目说明', 'Project context'], 'proj.contextNote': ['每次运行的任务书开头都会带上。', 'Put at the head of every run\'s brief.'],
  'proj.repos': ['仓库', 'Repositories'], 'proj.repoName': ['名称', 'Name'], 'proj.remote': ['远端', 'Remote'], 'proj.base': ['基准分支', 'Base branch'],
  'proj.worktrees': ['每个任务用自己的分支和 worktree', 'Each task on its own branch and worktree'],
  'proj.worktreesNote': ['分支是 tend/<任务>，子任务合进父任务的分支，评审在只读副本上做。', 'The branch is tend/<task>; subtasks merge into their parent\'s branch, reviews run on a read-only copy.'],
  'proj.dirs': ['检出在', 'Checked out at'], 'proj.machine': ['机器', 'Machine'], 'proj.path': ['目录', 'Directory'],
  'proj.addDir': ['加一台机器', 'Add a machine'], 'proj.removeDir': ['去掉 %s 上的目录', 'Remove the directory on %s'],
  'proj.addRepo': ['加仓库', 'Add a repository'], 'proj.removeRepo': ['去掉仓库', 'Remove the repository'], 'proj.noRepos': ['还没有仓库', 'No repository yet'],
  'proj.check': ['检查目录', 'Check the directories'], 'proj.noDirs': ['仓库还没写目录', 'No repository lists a directory'],
  'proj.dir.checking': ['检查中', 'checking'], 'proj.dir.git': ['git 仓库', 'a git checkout'], 'proj.dir.plain': ['在，但不是 git 仓库', 'there, but not a git checkout'],
  'proj.dir.missing': ['不在', 'not there'], 'proj.dir.outside': ['这台机器不让运行去这个目录', 'this machine lets no run go there'],
  'proj.defaults': ['默认值', 'Defaults'], 'proj.defaultMachine': ['默认机器', 'Default machine'], 'proj.anyMachine': ['不指定', 'None'],
  'proj.role.implement': ['实现 agent', 'Implementing agent'], 'proj.role.review': ['评审 agent', 'Review agent'],
  'proj.role.test': ['测试 agent', 'Test agent'], 'proj.role.planner': ['拆解 agent', 'Planner agent'], 'proj.noAgent': ['不指定', 'None'],
  'proj.workflow': ['默认工作流', 'Default workflow'], 'proj.noWorkflow': ['不用工作流（一次一个运行）', 'None (one run at a time)'],
  'proj.hooks': ['钩子', 'Hooks'], 'proj.hooksNote': ['一行一条命令，按空格分开参数。', 'One command each, its arguments split on spaces.'],
  'proj.hook.setup': ['setup：worktree 建好后运行一次', 'setup: runs once the worktree is made'],
  'proj.hook.before_run': ['before_run：每次运行开工前在 worktree 里运行，失败则运行失败', 'before_run: runs in the worktree before each run; failing fails the run'],
  'proj.hook.check': ['check：阶段结束后运行', 'check: runs after a stage'], 'proj.hook.cleanup': ['cleanup：合并后删 worktree 前运行', 'cleanup: runs before a merged worktree is removed'],
  'proj.save': ['保存设置', 'Save the settings'], 'proj.saved': ['%s 的设置已保存', 'The settings of %s are saved'], 'proj.unchanged': ['没有改动', 'Nothing changed'],
  'proj.flows': ['自定义工作流', 'Custom workflows'], 'proj.noFlows': ['没有；内置 feature、fix、docs。', 'None; feature, fix and docs are built in.'],
  'proj.newFlow': ['新建工作流', 'New workflow'], 'proj.editFlow': ['编辑', 'Edit'], 'proj.removeFlow': ['删除', 'Remove'],
  'proj.flowText': ['定义', 'Definition'], 'proj.flowNote': ['开头 --- 之间的 name 是它的名字。', 'The name between the opening --- lines is its name.'],
  'proj.flowNameless': ['定义开头要写 name', 'Give it a name in its front matter'], 'proj.flowSaved': ['工作流 %s 已保存', 'Workflow %s is saved'],
  'proj.flowRemoveTitle': ['删除工作流 %s？', 'Remove workflow %s?'],
  'proj.flowRemoveNote': ['用着它的任务在下一个阶段开始时会失败。', 'Tasks using it fail when their next stage starts.'], 'proj.flowRemoved': ['工作流 %s 已删除', 'Workflow %s is removed'],
  'proj.syncHelp': ['打了标签的 issue 自动成为这个项目的需求；tend 在 issue 上维护一条进度评论，需求完成后关单。', 'Issues with the label become requirements of this project; tend keeps one progress comment on each and closes it once the requirement is done.'],
  'proj.syncHelp.label': ['打了标签的 issue 自动成为这个项目的需求；tend 在 issue 上维护一条进度评论，需求完成后给 issue 打上 %s 标签。', 'Issues with the label become requirements of this project; tend keeps one progress comment on each and labels it %s once the requirement is done.'],
  'proj.bind': ['绑定仓库', 'Bind a repository'], 'proj.kind': ['工单系统', 'Tracker'], 'proj.trackerBase': ['地址', 'Address'],
  'proj.trackerRepo': ['仓库（owner/name，GitLab 可带子组）', 'Repository (owner/name; GitLab subgroups too)'], 'proj.token': ['机器人账号的 token', 'The bot account\'s token'],
  'proj.tokenNote': ['只存在 server 上，加密保存；需要读写 issue 的权限。', 'Kept on the server only, encrypted; it needs read and write access to issues.'],
  'proj.label': ['导入标签', 'Import label'], 'proj.labelNote': ['留空则不按标签导入', 'Empty imports none by label'],
  'proj.assigned': ['也导入指派给项目成员的 issue', 'Also import issues assigned to members of the project'],
  'proj.comment': ['维护进度评论', 'Keep a progress comment'], 'proj.detail': ['评论里列出子任务（仓库的读者都能看到）', 'List subtasks in it (every reader of the repository sees them)'],
  'proj.subIssues': ['每个子任务开一个子 issue（GitHub 挂成 sub-issue，其余写明属于哪条）', 'Open a sub-issue for each subtask (a sub-issue on GitHub, a reference elsewhere)'],
  'proj.pr': ['需求的分支推送后，等验收或完成时开 PR / MR', 'Open a PR / MR from a requirement\'s pushed branch once it awaits acceptance or is done'],
  'proj.onAccept': ['需求完成后', 'Once a requirement is done'], 'proj.accept.close': ['关闭 issue', 'Close the issue'], 'proj.accept.label': ['只打标签', 'Only add a label'],
  'proj.acceptLabel': ['完成标签', 'The label to add'], 'proj.poll': ['轮询间隔（秒，30–3600）', 'Poll every (seconds, 30–3600)'],
  'proj.bound': ['已绑定 %s', '%s is bound'],
  'proj.hook.github': ['要更快收到变化，在仓库 Settings → Webhooks 加这个 webhook：Content type 选 application/json，事件选 Issues 和 Issue comments，Secret 填下面的密钥。', 'For faster updates, add this webhook under the repository Settings → Webhooks: content type application/json, events Issues and Issue comments, the secret below as Secret.'],
  'proj.hook.gitea': ['要更快收到变化，在仓库设置 → Webhooks 加这个 Gitea webhook：事件选 Issues 和 Issue Comment，密钥填下面这个。', 'For faster updates, add this Gitea webhook under the repository settings → Webhooks: events Issues and Issue Comment, the secret below as its secret.'],
  'proj.hook.gitlab': ['要更快收到变化，在项目 Settings → Webhooks 加这个 webhook：触发器选 Issues events 和 Comments，Secret token 填下面的密钥。', 'For faster updates, add this webhook under the project Settings → Webhooks: triggers Issues events and Comments, the secret below as Secret token.'],
  'proj.hookURL': ['Webhook 地址', 'Webhook address'], 'proj.hookSecret': ['Webhook 密钥（只显示这一次）', 'Webhook secret (shown only this once)'],
  'proj.hookNone': ['server 没有配 public_url，没有 webhook 地址，只靠轮询。', 'The server has no public_url, so there is no webhook address: it polls.'],
  'proj.state.ok': ['同步中', 'syncing'], 'proj.state.stopped': ['已停：token 被拒，换 token 后继续', 'stopped: the token was refused; replace it to go on'],
  'proj.state.paused': ['限流中，%s 继续', 'rate limited until %s'],
  'proj.issues': ['%d 条需求', '%d requirements'], 'proj.failing': ['%d 条出错', '%d failing'],
  'proj.lastOK': ['上次成功 %s', 'last success %s'], 'proj.polled': ['上次扫描 %s', 'last scan %s'], 'proj.never': ['还没有', 'never'],
  'proj.syncSettings': ['同步设置', 'Sync settings'], 'proj.syncSaved': ['同步设置已保存', 'The sync settings are saved'],
  'proj.replaceToken': ['换 token', 'Replace the token'], 'proj.tokenSaved': ['token 已换', 'The token is replaced'],
  'proj.rescan': ['重新同步', 'Sync again'], 'proj.rescanned': ['马上重新同步', 'It syncs again now'],
  'proj.unbind': ['解绑', 'Unbind'], 'proj.unbindTitle': ['解绑 %s？', 'Unbind %s?'],
  'proj.unbindNote': ['之后不再导入 issue、不再写评论；已经建的任务留着。', 'No more issues come in and no comment is written; the tasks made so far stay.'], 'proj.unbound': ['%s 已解绑', '%s is unbound'],
  'proj.log': ['同步记录', 'Sync log'], 'proj.noLog': ['还没有跟着的 issue', 'No issue followed yet'], 'proj.noTask': ['没有对应任务', 'No task'],
  'proj.written': ['评论写于 %s', 'comment written %s'], 'proj.noComment': ['还没写评论', 'no comment yet'], 'proj.closed': ['已关闭', 'closed'], 'proj.labelled': ['已打标签 %s', 'labelled %s'],
  'proj.dirty': ['待重读', 'to read again'], 'proj.subOf': ['#%d 的子工单', 'sub-issue of #%d'],
  'proj.preview': ['预览评论', 'Preview the comment'], 'proj.previewTitle': ['#%d 的进度评论（现在写会是这样）', 'The progress comment on #%d, as it would be written now'],
});

const when = at => (at ? day(at) + ' ' + clock(at) : '');
const byLabel = (a, b) => a.label.localeCompare(b.label);
const kindLabel = {github: 'GitHub', gitea: 'Gitea', gitlab: 'GitLab'};

// Members is a project's owner and members; manages draws the controls that change them.
function Members({project: p, manages, busy, onRole, onAdd, onRemove}) {
  const {t} = useWords();
  const name = useName();
  const members = Object.entries(p.members || {}).map(([id, role]) => ({id, role, label: name(id)})).sort(byLabel);
  return html`<section class="det-sec">
    <h3 class="det-h mach-h">${t('team.members')}${manages && html`<${Button} kind="quiet" icon="plus" onClick=${onAdd}>${t('team.add')}<//>`}</h3>
    <ul class="team-rows">
      ${p.owner && html`<li class="team-row"><span class="team-main">${name(p.owner)} <span class="mono t-muted">${p.owner}</span></span><span class="t-muted">${t('role.owner')}</span></li>`}
      ${members.map(m => html`<li class="team-row" key=${m.id}>
        <span class="team-main">${m.label} <span class="mono t-muted">${m.id}</span></span>
        ${manages ? html`<${Segmented} label=${t('team.role')} value=${m.role} onChange=${r => onRole(m, r)} options=${tm.accessRoles.map(r => ({value: r, label: t('role.' + r)}))} />
          <${Button} kind="quiet danger" disabled=${busy} onClick=${() => onRemove(m)}>${t('team.remove')}<//>` : html`<span class="t-muted">${t('role.' + m.role)}</span>`}
      </li>`)}
    </ul>
  </section>`;
}

// AddMember picks someone not in the project yet and their role there.
export function AddMember({project, people, busy, onAdd, onClose}) {
  const {t} = useWords();
  const [user, setUser] = useState('');
  const [role, setRole] = useState('participant');
  return html`<${Modal} title=${t('team.add') + ' · ' + project.name} onClose=${onClose}
    actions=${[{label: t('home.cancel'), onClick: onClose}, {label: t('team.add'), kind: 'primary', keyName: 'Mod+Enter', disabled: !user || busy, onClick: () => onAdd(user, role)}]}>
    <${Picker} label=${t('team.person')} value=${user} onChange=${setUser} options=${people} />
    <div class="field"><span class="brief-label">${t('team.role')}</span>
      <${Segmented} label=${t('team.role')} value=${role} onChange=${setRole} options=${tm.accessRoles.map(r => ({value: r, label: t('role.' + r)}))} /></div>
  <//>`;
}

const dirWord = {checking: 'proj.dir.checking', git: 'proj.dir.git', plain: 'proj.dir.plain', missing: 'proj.dir.missing', outside: 'proj.dir.outside'};
const dirTone = {git: 't-success', plain: 't-warning', missing: 't-failed', outside: 't-failed', checking: 't-muted'};

// Repos edits the draft's repositories; checks[machine + '\n' + path] is what checking said of a checkout.
function Repos({repos, machines, checks, onChange}) {
  const {t, f} = useWords();
  const set = (i, patch) => onChange(repos.map((r, j) => (j === i ? {...r, ...patch} : r)));
  const setDir = (i, k, patch) => set(i, {dirs: repos[i].dirs.map((d, j) => (j === k ? {...d, ...patch} : d))});
  return html`<div class="proj-repos">
    ${!repos.length && html`<p class="t-muted mach-p">${t('proj.noRepos')}</p>`}
    ${repos.map((r, i) => html`<div class="proj-repo" key=${i}>
      <div class="proj-grid3">
        <${TextInput} label=${t('proj.repoName')} value=${r.name} onInput=${v => set(i, {name: v})} mono />
        <${TextInput} label=${t('proj.remote')} value=${r.remote} onInput=${v => set(i, {remote: v})} mono />
        <${TextInput} label=${t('proj.base')} value=${r.base} onInput=${v => set(i, {base: v})} mono />
      </div>
      <${Check} label=${t('proj.worktrees')} note=${t('proj.worktreesNote')} on=${r.worktrees} onChange=${v => set(i, {worktrees: v})} />
      <span class="brief-label">${t('proj.dirs')}</span>
      ${r.dirs.map((d, k) => {
        const said = checks[d.machine + '\n' + d.path.trim()];
        return html`<div class="proj-dir" key=${k}>
          <${Picker} label=${t('proj.machine')} value=${d.machine} onChange=${v => setDir(i, k, {machine: v})} options=${machines} />
          <${TextInput} label=${t('proj.path')} value=${d.path} onInput=${v => setDir(i, k, {path: v})} mono />
          <${Button} kind="quiet danger" icon="close" label=${f('proj.removeDir', d.machine || '—')} onClick=${() => set(i, {dirs: r.dirs.filter((_, j) => j !== k)})} />
          ${said && html`<span class=${cx('proj-dir-said', dirTone[said])}>${t(dirWord[said])}</span>`}
        </div>`;
      })}
      <div class="det-acts">
        <${Button} kind="quiet" icon="plus" onClick=${() => set(i, {dirs: [...r.dirs, {machine: '', path: ''}]})}>${t('proj.addDir')}<//>
        <${Button} kind="quiet danger" onClick=${() => onChange(repos.filter((_, j) => j !== i))}>${t('proj.removeRepo')}<//>
      </div>
    </div>`)}
  </div>`;
}

function FlowEditor({name, text: first, busy, onSave, onClose}) {
  const {t} = useWords();
  const [text, setText] = useState(first);
  const [error, setError] = useState('');
  const save = () => { const n = tm.flowName(text); if (!n) { setError(t('proj.flowNameless')); return; } onSave(n, text); };
  return html`<${Modal} full title=${name ? name : t('proj.newFlow')} onClose=${onClose}
    actions=${[{label: t('home.cancel'), onClick: onClose}, {label: t('form.save'), kind: 'primary', keyName: 'Mod+Enter', disabled: busy, onClick: save}]}>
    <${TextArea} label=${t('proj.flowText')} value=${text} onInput=${setText} rows=${18} mono note=${t('proj.flowNote')} error=${error} />
  <//>`;
}

// Settings is the project's settings form; save sends what changed (project.edit), its workflows are saved one by one.
function Settings({project: p, machines, wire, commands, toasts, people}) {
  const w = useWords();
  const {t, f} = w;
  const [d, setD] = useState(() => tm.draftOf(p));
  const [agents, setAgents] = useState([]);
  const [checks, setChecks] = useState({});
  const [modal, setModal] = useState(null);
  useEffect(() => { setD(tm.draftOf(p)); }, [p.id]);
  useEffect(() => {
    if (wire?.has?.('agent.list') === false) return;
    wire?.call('agent.list', {}).then(r => setAgents(r?.agents || []), () => {});
  }, []);
  const key = 'project:' + p.id;
  const busy = commands.state(key) === 'pending';
  const failed = e => toasts.show({text: apiText(w, e), tone: 'danger'});
  const edit = (params, text) => commands.send('project.edit', params, {key}).then(() => { if (text) toasts.show({text}); }, e => { failed(e); throw e; });
  const change = e => edit(e, f('proj.saved', p.name)).catch(() => {});
  const save = () => { const e = tm.projectEditOf(p, d); if (!e) { toasts.show({text: t('proj.unchanged')}); return; } change(e); };
  const check = () => {
    const pairs = tm.dirsToCheck(d);
    if (!pairs.length) { toasts.show({text: t('proj.noDirs')}); return; }
    setChecks(Object.fromEntries(pairs.map(x => [x.machine + '\n' + x.path, 'checking'])));
    for (const x of pairs) {
      wire.call('project.dirs', {project: p.id, machine: x.machine, path: tm.parentOf(x.path)})
        .then(r => tm.dirVerdict(r, x.path), () => 'missing')
        .then(v => setChecks(c => ({...c, [x.machine + '\n' + x.path]: v})));
    }
  };
  const flows = p.workflows || {};
  const setFlows = (next, text) => edit({id: p.id, workflows: next}, text);
  const agentOptions = [{value: '', label: t('proj.noAgent')}, ...agents.map(a => ({value: a.name, label: a.name, sub: [a.provider, a.model].filter(Boolean).join(' · ')}))];
  const machineOptions = machines.filter(m => !m.retired).map(m => ({value: m.name, label: m.name, sub: m.state}));
  const flowOptions = [{value: '', label: t('proj.noWorkflow')}, ...[...new Set([...builtinFlows, ...Object.keys(flows)])].sort().map(n => ({value: n, label: n}))];
  const dialog = modal && ({
    flow: () => html`<${FlowEditor} name=${modal.name} text=${modal.name ? flows[modal.name] : tm.flowTemplate} busy=${busy} onClose=${() => setModal(null)}
      onSave=${(n, text) => { const next = {...flows}; if (modal.name && modal.name !== n) delete next[modal.name]; next[n] = text;
        setFlows(next, f('proj.flowSaved', n)).then(() => setModal(null), () => {}); }} />`,
    removeFlow: () => html`<${Modal} title=${f('proj.flowRemoveTitle', modal.name)} onClose=${() => setModal(null)} actions=${[{label: t('confirm.keep'), onClick: () => setModal(null)},
      {label: t('proj.removeFlow'), kind: 'primary', keyName: 'Mod+Enter', onClick: () => { const next = {...flows}; delete next[modal.name]; setModal(null);
        setFlows(next, f('proj.flowRemoved', modal.name)).catch(() => {}); }}]}><p>${t('proj.flowRemoveNote')}</p><//>`,
  })[modal.kind]?.();
  return html`<div class="proj-settings">
    <div class="proj-grid2">
      <${TextInput} label=${t('proj.name')} value=${d.name} onInput=${v => setD({...d, name: v})} />
      <${Picker} label=${t('proj.owner')} value=${d.owner} onChange=${v => setD({...d, owner: v})} options=${people} />
    </div>
    <${TextArea} label=${t('proj.context')} value=${d.context} onInput=${v => setD({...d, context: v})} rows=${4} note=${t('proj.contextNote')} />
    <section class="det-sec"><h3 class="det-h mach-h">${t('proj.repos')}
      <span class="det-acts">${wire?.has?.('project.dirs') && html`<${Button} kind="quiet" onClick=${check}>${t('proj.check')}<//>`}
        <${Button} kind="quiet" icon="plus" onClick=${() => setD({...d, repos: [...d.repos, {name: '', remote: '', base: '', worktrees: false, dirs: []}]})}>${t('proj.addRepo')}<//></span></h3>
      <${Repos} repos=${d.repos} machines=${machineOptions} checks=${checks} onChange=${repos => setD({...d, repos})} />
    </section>
    <section class="det-sec"><h3 class="det-h">${t('proj.defaults')}</h3>
      <div class="proj-grid2">
        <${Picker} label=${t('proj.defaultMachine')} value=${d.machine} onChange=${v => setD({...d, machine: v})} options=${[{value: '', label: t('proj.anyMachine')}, ...machineOptions]} />
        <${Picker} label=${t('proj.workflow')} value=${d.workflow} onChange=${v => setD({...d, workflow: v})} options=${flowOptions} />
        ${tm.roleNames.map(r => html`<${Picker} label=${t('proj.role.' + r)} value=${d.roles[r]} onChange=${v => setD({...d, roles: {...d.roles, [r]: v}})} options=${agentOptions} />`)}
      </div>
    </section>
    <section class="det-sec"><h3 class="det-h">${t('proj.hooks')}</h3>
      <p class="field-note">${t('proj.hooksNote')}</p>
      ${tm.hookNames.map(h => html`<${TextInput} label=${t('proj.hook.' + h)} value=${d.hooks[h]} onInput=${v => setD({...d, hooks: {...d.hooks, [h]: v}})} mono />`)}
    </section>
    <div class="det-acts"><${Button} kind="primary" disabled=${busy} onClick=${save}>${t('proj.save')}<//></div>
    <section class="det-sec"><h3 class="det-h mach-h">${t('proj.flows')}<${Button} kind="quiet" icon="plus" onClick=${() => setModal({kind: 'flow', name: ''})}>${t('proj.newFlow')}<//></h3>
      ${Object.keys(flows).length ? html`<ul class="team-rows">${Object.keys(flows).sort().map(n => html`<li class="team-row" key=${n}>
        <span class="team-main mono">${n}</span>
        <${Button} kind="quiet" onClick=${() => setModal({kind: 'flow', name: n})}>${t('proj.editFlow')}<//>
        <${Button} kind="quiet danger" onClick=${() => setModal({kind: 'removeFlow', name: n})}>${t('proj.removeFlow')}<//>
      </li>`)}</ul>` : html`<p class="t-muted mach-p">${t('proj.noFlows')}</p>`}
    </section>
    ${dialog}
  </div>`;
}

function SyncFields({value: s, onChange}) {
  const {t} = useWords();
  const set = patch => onChange({...s, ...patch});
  return html`<div class="proj-sync-fields">
    <div class="proj-grid2">
      <${TextInput} label=${t('proj.label')} value=${s.label} onInput=${v => set({label: v})} mono note=${t('proj.labelNote')} />
      <${TextInput} label=${t('proj.poll')} value=${String(s.poll)} onInput=${v => set({poll: Number(v) || 0})} mono />
    </div>
    <${Check} label=${t('proj.assigned')} on=${!!s.assigned} onChange=${v => set({assigned: v})} />
    <${Check} label=${t('proj.comment')} on=${!!s.comment} onChange=${v => set({comment: v})} />
    <${Check} label=${t('proj.detail')} on=${!!s.detail} onChange=${v => set({detail: v})} disabled=${!s.comment} />
    <${Check} label=${t('proj.subIssues')} on=${!!s.sub_issues} onChange=${v => set({sub_issues: v})} />
    <${Check} label=${t('proj.pr')} on=${!!s.pr} onChange=${v => set({pr: v})} />
    <div class="field"><span class="brief-label">${t('proj.onAccept')}</span>
      <${Segmented} label=${t('proj.onAccept')} value=${s.on_accept} onChange=${v => set({on_accept: v})}
        options=${['close', 'label'].map(v => ({value: v, label: t('proj.accept.' + v)}))} /></div>
    ${s.on_accept === 'label' && html`<${TextInput} label=${t('proj.acceptLabel')} value=${s.accept_label || ''} onInput=${v => set({accept_label: v})} mono />`}
  </div>`;
}

// syncHelp says what a binding with settings s does, closing or labelling a done requirement's issue.
const syncHelp = ({t, f}, s) => (s?.on_accept === 'label' ? f('proj.syncHelp.label', s.accept_label) : t('proj.syncHelp'));

// ⚠️ What a new binding syncs with unless changed (server.DefaultSettings).
const defaultSync = {label: 'tend', comment: true, on_accept: 'close', accept_label: 'tend:accepted', poll: 60};
const syncOK = s => s.poll >= tm.pollRange[0] && s.poll <= tm.pollRange[1] && (s.on_accept !== 'label' || !!s.accept_label?.trim());

function Bind({project, busy, error, onBind, onClose}) {
  const w = useWords();
  const {t} = w;
  const [kind, setKind] = useState('github');
  const [base, setBase] = useState(tm.trackerKinds.github);
  const [repo, setRepo] = useState('');
  const [token, setToken] = useState('');
  const [s, setS] = useState(defaultSync);
  const pick = k => { if (!base || base === tm.trackerKinds[kind]) setBase(tm.trackerKinds[k]); setKind(k); };
  const ok = base.trim() && repo.trim() && token && syncOK(s) && !busy;
  return html`<${Modal} full title=${t('proj.bind') + ' · ' + project.name} onClose=${onClose}
    actions=${[{label: t('home.cancel'), onClick: onClose}, {label: t('proj.bind'), kind: 'primary', keyName: 'Mod+Enter', disabled: !ok,
      onClick: () => onBind({project: project.id, kind, base: base.trim(), repo: repo.trim(), token, settings: {...s, label: s.label.trim()}})}]}>
    <p class="field-note">${syncHelp(w, s)}</p>
    <div class="field"><span class="brief-label">${t('proj.kind')}</span>
      <${Segmented} label=${t('proj.kind')} value=${kind} onChange=${pick} options=${Object.keys(tm.trackerKinds).map(k => ({value: k, label: kindLabel[k]}))} /></div>
    <div class="proj-grid2">
      <${TextInput} label=${t('proj.trackerBase')} value=${base} onInput=${setBase} mono placeholder="https://git.example.com" />
      <${TextInput} label=${t('proj.trackerRepo')} value=${repo} onInput=${setRepo} mono placeholder="owner/name" />
    </div>
    <${TextInput} label=${t('proj.token')} type="password" value=${token} onInput=${setToken} note=${t('proj.tokenNote')} error=${error} />
    <${SyncFields} value=${s} onChange=${setS} />
  <//>`;
}

function Bound({x, copy, onClose}) {
  const {t, f} = useWords();
  return html`<${Modal} title=${f('proj.bound', x.repo)} onClose=${onClose} actions=${[{label: t('team.done'), kind: 'primary', keyName: 'Mod+Enter', onClick: onClose}]}>
    ${x.hook ? html`<p class="mach-p">${t('proj.hook.' + x.kind)}</p>
      <${Secret} label=${t('proj.hookURL')} value=${x.hook} copy=${copy} />
      <${Secret} label=${t('proj.hookSecret')} value=${x.hook_secret} copy=${copy} />` : html`<p class="mach-p">${t('proj.hookNone')}</p>`}
  <//>`;
}

function Token({busy, error, onSave, onClose}) {
  const {t} = useWords();
  const [token, setToken] = useState('');
  return html`<${Modal} title=${t('proj.replaceToken')} onClose=${onClose}
    actions=${[{label: t('home.cancel'), onClick: onClose}, {label: t('form.save'), kind: 'primary', keyName: 'Mod+Enter', disabled: !token || busy, onClick: () => onSave(token)}]}>
    <${TextInput} label=${t('proj.token')} type="password" value=${token} onInput=${setToken} autoFocus note=${t('proj.tokenNote')} error=${error} />
  <//>`;
}

function SyncSettings({x, busy, onSave, onClose}) {
  const {t} = useWords();
  const [s, setS] = useState({...defaultSync, ...x.settings});
  return html`<${Modal} title=${t('proj.syncSettings') + ' · ' + x.repo} onClose=${onClose}
    actions=${[{label: t('home.cancel'), onClick: onClose}, {label: t('form.save'), kind: 'primary', keyName: 'Mod+Enter', disabled: !syncOK(s) || busy,
      onClick: () => onSave({...s, label: s.label.trim()})}]}>
    <${SyncFields} value=${s} onChange=${setS} />
  <//>`;
}

// Sync is the project's issue sync: its bindings (/api/trackers), each with how it syncs and its log.
function Sync({project: p, st, http, toasts, copy}) {
  const w = useWords();
  const {t, f} = w;
  const [xs, setXs] = useState(null);
  const [logs, setLogs] = useState({});
  const [modal, setModal] = useState(null);
  const [busy, setBusy] = useState(false);
  const read = () => http.trackers().then(all => {
    const mine = all.filter(x => x.project === p.id);
    setXs(mine);
    for (const x of mine) http.trackerIssues(x.id).then(rows => setLogs(l => ({...l, [x.id]: rows})), () => {});
  }, e => { setXs([]); toasts.show({text: apiText(w, e), tone: 'danger'}); });
  useEffect(() => { read(); }, [p.id]);
  const close = () => setModal(null);
  const run = (promise, text, next = close) => {
    setBusy(true);
    return promise.then(v => { next(v); if (text) toasts.show({text}); read(); }, e => {
      if (modal?.kind === 'bind' || modal?.kind === 'token') { setModal({...modal, error: apiText(w, e)}); return; }
      close();
      toasts.show({text: apiText(w, e), tone: 'danger'});
    }).finally(() => setBusy(false));
  };
  const dialog = modal && ({
    bind: () => html`<${Bind} project=${p} busy=${busy} error=${modal.error} onClose=${close}
      onBind=${b => run(http.bindTracker(b), '', x => setModal({kind: 'bound', x}))} />`,
    bound: () => html`<${Bound} x=${modal.x} copy=${copy} onClose=${close} />`,
    settings: () => html`<${SyncSettings} x=${modal.x} busy=${busy} onClose=${close} onSave=${s => run(http.trackerSettings(modal.x.id, s), t('proj.syncSaved'))} />`,
    token: () => html`<${Token} busy=${busy} error=${modal.error} onClose=${close} onSave=${tok => run(http.trackerToken(modal.x.id, tok), t('proj.tokenSaved'))} />`,
    unbind: () => html`<${Modal} title=${f('proj.unbindTitle', modal.x.repo)} onClose=${close} actions=${[{label: t('confirm.keep'), onClick: close},
      {label: t('proj.unbind'), kind: 'primary', keyName: 'Mod+Enter', onClick: () => run(http.unbindTracker(modal.x.id), f('proj.unbound', modal.x.repo))}]}>
      <p>${t('proj.unbindNote')}</p><//>`,
    preview: () => html`<${Modal} title=${f('proj.previewTitle', modal.number)} onClose=${close} actions=${[{label: t('team.done'), kind: 'primary', keyName: 'Mod+Enter', onClick: close}]}>
      ${modal.body === undefined ? html`<p class="t-muted">${t('proj.dir.checking')}</p>` : html`<div class="proj-preview"><${Markdown} text=${modal.body} /></div>`}<//>`,
  })[modal.kind]?.();
  const preview = (x, n) => {
    setModal({kind: 'preview', number: n});
    http.trackerPreview(x.id, n).then(v => setModal(m => (m?.kind === 'preview' ? {...m, body: tm.shownComment(v.body)} : m)), e => { close(); toasts.show({text: apiText(w, e), tone: 'danger'}); });
  };
  const state = x => {
    const s = tm.trackerState(x);
    return html`<span class=${cx('proj-state', s === 'ok' ? 't-success' : s === 'paused' ? 't-warning' : 't-failed')}>${s === 'paused' ? f('proj.state.paused', when(x.paused_until)) : t('proj.state.' + s)}</span>`;
  };
  if (xs === null) return html`<p class="t-muted mach-p">${t('proj.dir.checking')}</p>`;
  return html`<div class="proj-sync">
    <p class="field-note">${syncHelp(w, xs[0]?.settings)}</p>
    ${xs.map(x => html`<section class="det-sec proj-binding" key=${x.id}>
      <h3 class="det-h mach-h"><span><b class="mono">${x.repo}</b> <span class="t-muted">${kindLabel[x.kind] || x.kind} · @${x.bot}</span></span>${state(x)}</h3>
      <p class="mach-p t-muted">${[f('proj.issues', x.issues), x.failing && f('proj.failing', x.failing), f('proj.lastOK', when(x.last_ok) || t('proj.never')),
        f('proj.polled', when(x.polled) || t('proj.never'))].filter(Boolean).join(' · ')}</p>
      ${x.last_error && html`<p class="mach-p t-failed mono">${x.last_error}</p>`}
      ${x.hook && html`<p class="mach-p"><span class="t-muted">${t('proj.hookURL')}</span> <span class="mono">${x.hook}</span></p>`}
      <div class="det-acts">
        <${Button} kind="quiet" onClick=${() => setModal({kind: 'settings', x})}>${t('proj.syncSettings')}<//>
        <${Button} kind="quiet" onClick=${() => setModal({kind: 'token', x})}>${t('proj.replaceToken')}<//>
        <${Button} kind="quiet" onClick=${() => run(http.rescanTracker(x.id), t('proj.rescanned'), () => {})}>${t('proj.rescan')}<//>
        <${Button} kind="quiet danger" onClick=${() => setModal({kind: 'unbind', x})}>${t('proj.unbind')}<//>
      </div>
      <h3 class="det-h">${t('proj.log')}</h3>
      ${(logs[x.id] || []).length ? html`<ul class="team-rows">${logs[x.id].map(i => html`<li class="team-row proj-issue" key=${i.number}>
        <a class="mono" href=${tm.issueURL(x, i.number)} target="_blank" rel="noreferrer">#${i.number}</a>
        <span class="team-main"><span class="ell">${i.task ? st.tasks[i.task]?.title || i.task : html`<span class="t-muted">${t('proj.noTask')}</span>`}</span>
          <span class="team-sub">${[i.written ? f('proj.written', when(i.written)) : t('proj.noComment'), i.closed && (x.settings?.on_accept === 'label' ? f('proj.labelled', x.settings.accept_label) : t('proj.closed')), i.dirty && t('proj.dirty'),
            i.parent && f('proj.subOf', i.parent)].filter(Boolean).join(' · ')}${i.pr && html` · <a href=${i.pr} target="_blank" rel="noreferrer">PR</a>`}</span>
          ${i.last_error && html`<span class="team-sub t-failed mono">${i.last_error}</span>`}</span>
        ${i.task && html`<${Button} kind="quiet" onClick=${() => preview(x, i.number)}>${t('proj.preview')}<//>`}
      </li>`)}</ul>` : html`<p class="t-muted mach-p">${t('proj.noLog')}</p>`}
    </section>`)}
    ${!xs.length && html`<div class="det-acts"><${Button} kind="primary" icon="plus" onClick=${() => setModal({kind: 'bind'})}>${t('proj.bind')}<//></div>`}
    ${dialog}
  </div>`;
}

// ProjectDrawer is one project; settings and issue sync are there for who manages it on a computer (manages).
export function ProjectDrawer({project: p, st, machines, people, wire, http, commands, toasts, copy, manages, busy, onRole, onAdd, onRemove, onClose}) {
  const {t, f} = useWords();
  const name = useName();
  const [tab, setTab] = useState('members');
  const n = tm.projectFacts(st, p);
  const tabs = [{id: 'members', label: t('proj.members')}, ...(manages ? [{id: 'settings', label: t('proj.settings')}, {id: 'sync', label: t('proj.sync')}] : [])];
  const shown = tabs.some(x => x.id === tab) ? tab : 'members';
  return html`<${Drawer} title=${p.name} onClose=${onClose}>
    <div class="team-drawer">
      <p class="t-muted team-facts">${[f('team.owner', name(p.owner)), f('team.tasks', n.tasks, n.open), f('team.people', n.participants, n.readers)].join(' · ')}</p>
      ${tabs.length > 1 && html`<${Tabs} label=${p.name} idPrefix="proj" value=${shown} onChange=${setTab} tabs=${tabs} />`}
      <div id=${'proj-' + shown + '-pane'} role=${tabs.length > 1 ? 'tabpanel' : undefined} aria-labelledby=${tabs.length > 1 ? 'proj-' + shown : undefined}>
        ${shown === 'members' && html`<${Members} project=${p} manages=${manages} busy=${busy} onRole=${onRole} onAdd=${onAdd} onRemove=${onRemove} />`}
        ${shown === 'settings' && html`<${Settings} key=${p.id} project=${p} machines=${machines} wire=${wire} commands=${commands} toasts=${toasts} people=${people} />`}
        ${shown === 'sync' && html`<${Sync} key=${p.id} project=${p} st=${st} http=${http} toasts=${toasts} copy=${copy} />`}
      </div>
    </div>
  <//>`;
}
