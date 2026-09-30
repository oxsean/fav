// agents is the agent page: every agent the viewer may pick or manages (their definitions, their projects', those
// shared with them, other people's for an admin, the configured profiles), where each comes from, what it runs as, the
// machines it runs on and what uses it. The picked one shows its definition, its command and who else may use it.
// Whoever manages a definition edits, shares and removes it; anyone writes a new one, imports a Claude Code subagent
// or copies one they may read. On a phone the page only shows: the list, each opening its facts and its text.
import {useState, useEffect, useRef} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useSignalValue, useName, useNames} from '../ui/base.js';
import {Panel} from '../ui/panel.js';
import {Modal, Drawer} from '../ui/overlay.js';
import {Button, Chip, Chips, Segmented, Check, Tabs} from '../ui/controls.js';
import {TextInput, TextArea} from '../ui/input.js';
import {Picker} from '../ui/picker.js';
import {Table} from '../ui/table.js';
import {register} from '../core/i18n.js';
import {clock, day} from '../core/format.js';
import * as ag from '../core/agents.js';
import {apiText} from './words.js';
import './taskwords.js';

register('agents', {
  'ag.title': ['Agent', 'Agents'],
  'ag.lead': ['派活时选的是这里的名字：provider、模型、权限和说明书写在一起', 'Dispatching picks a name from here: provider, model, permissions and instructions in one place'],
  'ag.src.all': ['全部', 'All'], 'ag.src.mine': ['我的', 'Mine'], 'ag.src.project': ['项目', 'Projects'], 'ag.src.shared': ['分享给我', 'Shared with me'],
  'ag.src.others': ['其他人的', 'Other people\'s'], 'ag.src.profile': ['档案', 'Profiles'],
  'ag.from.mine': ['我的', 'mine'], 'ag.from.project': ['项目 %s', 'project %s'], 'ag.from.shared': ['%s 分享', 'shared by %s'],
  'ag.from.others': ['%s 的', 'owned by %s'], 'ag.from.profile': ['档案', 'profile'],
  'ag.subagent': ['导入 Claude Code subagent', 'Import a Claude Code subagent'], 'ag.new': ['新建 Agent', 'New agent'],
  'ag.col.name': ['名字 · 用途', 'Name · purpose'], 'ag.col.role': ['角色', 'Role'], 'ag.col.spec': ['provider · 模型', 'Provider · model'],
  'ag.col.source': ['来源', 'Source'], 'ag.col.machines': ['能跑的机器', 'Runs on'], 'ag.col.check': ['检查', 'Check'],
  'ag.nowhere': ['没有', 'none'], 'ag.checkOK': ['检查通过', 'Its check passes'], 'ag.checkWarn': ['有警告', 'It has warnings'],
  'ag.foot': ['「档案」来自 config.json，只读；同名时定义优先。✓ 检查通过，! 有警告。', 'Profiles come from config.json and are read-only; a definition of the same name comes first. ✓ passes its check, ! has warnings.'],
  'ag.none': ['还没有 agent', 'No agent yet'], 'ag.noneHere': ['这一类没有 agent', 'No agent of this kind'], 'ag.loading': ['正在读取…', 'Loading…'],
  'ag.rev': ['第 %d 版 · %s 改过', 'Version %d · changed %s'], 'ag.config': ['config.json 里的档案', 'A profile in config.json'],
  'ag.role': ['角色', 'Role'], 'ag.runsAs': ['provider · 模型', 'Provider · model'], 'ag.permission': ['权限', 'Permissions'],
  'ag.deny': ['不许用的工具', 'Tools denied'], 'ag.usedBy': ['用在', 'Used by'], 'ag.default': ['默认', 'default'],
  'ag.usedRoles': ['%s：%s', '%s: %s'], 'ag.usedTask': ['1 个没完成的任务', 'One unfinished task'], 'ag.usedTasks': ['%d 个没完成的任务', '%d unfinished tasks'], 'ag.unused': ['没有项目或任务在用', 'No project or task names it'],
  'ag.machines': ['机器', 'Machines'], 'ag.pinned': ['只在 %s 上跑', 'Runs on %s only'], 'ag.noMachines': ['你还不能用任何机器', 'You may use no machine yet'],
  'ag.on.ok': ['能跑', 'runs it'], 'ag.on.offline': ['离线：运行等它连上', 'offline: runs wait for it'], 'ag.on.auth': ['%s 没登录', '%s is not signed in'],
  'ag.on.missing': ['没装 %s', '%s is not installed'], 'ag.on.unknown': ['没报告 %s 能不能用', 'does not say whether %s works'],
  'ag.notYours': ['管理员看得见所有定义，但不能用没分享给自己的。', 'Admins see every definition but use only those shared with them.'],
  'ag.tabs': ['这个 agent', 'This agent'], 'ag.tab.def': ['定义', 'Definition'], 'ag.tab.launch': ['启动参数', 'Command'], 'ag.tab.share': ['分享', 'Sharing'],
  'ag.hidden': ['%s 没让你看说明书', '%s does not let you read its instructions'],
  'ag.launchNote': ['在任务的目录里启动；说明书放进任务书，在项目 context 之后。', 'It starts in the task\'s directory; its instructions go into the brief, after the project\'s context.'],
  'ag.launchHidden': ['看不到：分享时没让你看说明书', 'Not shown: it was shared without its instructions'],
  'ag.launchNone': ['协调器只给定义算启动参数', 'The coordinator works out the command for definitions only'],
  'ag.shareUser': ['%s 可以用', '%s may use it'], 'ag.shareProject': ['项目 %s 的参与者可以用', 'Participants of project %s may use it'],
  'ag.shareAll': ['所有人都能用', 'Everyone may use it'], 'ag.shareView': ['他们能看说明书', 'They may read its instructions'],
  'ag.shareNoView': ['他们只能用，不能看说明书', 'They may use it, not read it'], 'ag.shareNone': ['没有分享：只有主人和管理员能用', 'Not shared: only its owner and admins use it'],
  'ag.projectOwned': ['归项目 %s 所有：参与者能用，负责人管理', 'Owned by project %s: its participants use it, its owner manages it'],
  'ag.toYou': ['%s 分享给你', '%s shares it with you'], 'ag.youRead': ['你能用，也能看说明书', 'You may use it and read it'], 'ag.youUse': ['你只能用，不能看说明书', 'You may use it, not read it'],
  'ag.profileShare': ['只在这台协调器上：档案不分享', 'On this coordinator only: profiles are not shared'],
  'ag.checks': ['检查', 'Checks'],
  'ag.warn.allow': ['tools.allow 不生效：它会放宽节点允许的范围', 'tools.allow is not applied: it would widen what the node allows'],
  'ag.warn.claudeOnly': ['skills、mcp 和 hooks 只对 claude 生效：skills 要装在机器上，mcp 用节点 node.mcp 里的名字，hooks 要节点开 node.allow_hooks',
    'skills, mcp and hooks apply to claude only: skills must be installed on the machine, mcp names its node.mcp servers, hooks need node.allow_hooks'],
  'ag.warn.importOnce': ['import 只在导入时读一次', 'import is read when the definition is imported, not later'], 'ag.warn.unknown': ['不认识的字段 %s 原样保留', 'The unknown field %s is kept'],
  'ag.note.codex': ['codex 不接 mcp 和 hooks：派发时会提示 def_pending', 'codex takes no mcp or hooks: a dispatch warns def_pending'],
  'ag.note.kept': ['%s 只保存，还不生效', '%s is kept but has no effect yet'],
  'ag.note.auth': ['%s 的 %s 没登录', '%s: %s is not signed in'], 'ag.note.missing': ['%s 没装 %s', '%s: %s is not installed'],
  'ag.note.nowhere': ['你能用的机器都跑不了它', 'None of the machines you may use runs it'],
  'ag.edit': ['编辑', 'Edit'], 'ag.copy': ['复制成我的定义', 'Copy as my own'], 'ag.export': ['导出 .md', 'Export .md'], 'ag.remove': ['删除', 'Remove'],
  'ag.share': ['改分享', 'Change sharing'], 'ag.inConfig': ['在 config.json 里改', 'Changed in config.json'],
  'ag.editTitle': ['改 %s', 'Edit %s'], 'ag.copyTitle': ['复制 %s', 'Copy %s'],
  'ag.text': ['定义', 'Definition'],
  'ag.textNote': ['Markdown，开头是 YAML front matter：name、description、role、provider 或 profile、model、effort、permission、tools、mcp、skills、hooks、machines、output、budget；正文是说明书。',
    'Markdown with a YAML front matter: name, description, role, provider or profile, model, effort, permission, tools, mcp, skills, hooks, machines, output, budget; the body is its instructions.'],
  'ag.fromFile': ['从文件读…', 'Read a file…'],
  'ag.importNote': ['粘贴 .claude/agents/ 里的一个文件。没写 provider 的按 claude 跑，model: inherit 去掉（用 CLI 自己的默认）。',
    'Paste a file from .claude/agents/. Without a provider it runs with claude; model: inherit is left out (the CLI\'s own default).'],
  'ag.owner': ['归属', 'Owner'], 'ag.ownerMe': ['我', 'Me'],
  'ag.nameless': ['开头的 front matter 里要写 name', 'Give it a name in its front matter'],
  'ag.renamed': ['名字改成了 %s：保存为一个新定义，%s 还在', 'The name is now %s: it is saved as a new definition and %s stays'],
  'ag.overwrites': ['%s 已经有了：保存会覆盖它', '%s exists: saving replaces it'], 'ag.taken': ['%s 已经有了，你不能改它', '%s exists and is not yours to change'],
  'ag.shadows': ['档案里也有 %s：保存后定义优先', 'A profile is named %s too: once saved, the definition comes first'],
  'ag.refused': ['没有保存：%s', 'Not saved: %s'], 'ag.check': ['检查', 'Check'], 'ag.checkBad': ['保存会被拒绝', 'Saving it would be refused'],
  'ag.checkRefused': ['检查不了：%s', 'Cannot be checked: %s'], 'ag.saved': ['%s 已保存', '%s is saved'], 'ag.savedWarning': ['%s 已保存，有 1 条警告', '%s is saved with a warning'], 'ag.savedWarn': ['%s 已保存，有 %d 条警告', '%s is saved with %d warnings'],
  'ag.name': ['名字', 'Name'], 'ag.nameNote': ['小写字母、数字和 . _ -，最多 64 个；派活时选这个名字', 'Lowercase letters, digits and . _ -, up to 64; dispatching picks this name'],
  'ag.nameBad': ['名字不合规矩', 'That name does not fit the rule'], 'ag.nameTaken': ['%s 已经有了', '%s exists'],
  'ag.kind': ['类型', 'Kind'], 'ag.installedOn': ['装在 %s', 'Installed on %s'], 'ag.installedNone': ['你能用的机器还没有装它', 'None of the machines you may use has it'],
  'ag.model': ['模型', 'Model'],
  'ag.modelNote': ['手填：协调器不知道各台机器的 CLI 认哪些模型；留空用 CLI 自己的默认', 'Typed by hand: the coordinator does not know which models each machine\'s CLI takes; empty uses the CLI\'s own default'],
  'ag.aliases': ['别名', 'Aliases'], 'ag.next': ['下一步：写说明书', 'Next: its instructions'],
  'ag.shareTitle': ['谁还能用 %s', 'Who else may use %s'], 'ag.shareEveryone': ['所有人', 'Everyone'],
  'ag.shareEveryoneNote': ['团队里每个人都能用它', 'Everyone on the team may use it'], 'ag.users': ['人', 'People'],
  'ag.projects': ['项目（在这个项目的任务上用）', 'Projects (on that project\'s tasks)'], 'ag.viewField': ['能看说明书', 'They may read its instructions'],
  'ag.viewNote': ['不勾时他们只能派活用它，看不到正文和启动参数', 'Unticked, they dispatch with it but see neither its text nor its command'],
  'ag.sharedDone': ['%s 的分享已保存', 'Who may use %s is saved'],
  'ag.removeTitle': ['删除 %s？', 'Remove %s?'],
  'ag.removeNote': ['已经在跑或排队的运行不受影响：它们带着派发时的定义。', 'Runs going or queued are not affected: they carry the definition they were dispatched with.'],
  'ag.removeUsed': ['还在用它的：%s。之后的派发找不到它。', 'Still naming it: %s. Later dispatches will not find it.'],
  'ag.removed': ['%s 已删除', '%s is removed'],
  'ag.desktop': ['新建、编辑、导入和分享在电脑上管理', 'New agents, editing, importing and sharing are managed on a computer'],
});

// ⚠️ The warnings defs.Check gives, by how they start, and what the page says for each.
const warnings = [[/^tools\.allow /, 'ag.warn.allow'], [/^skills, mcp and hooks /, 'ag.warn.claudeOnly'], [/^import /, 'ag.warn.importOnce'],
  [/^unknown field (\S+) is kept$/, 'ag.warn.unknown']];

function warningText(w, s) {
  for (const [re, key] of warnings) {
    const m = re.exec(s);
    if (m) return m[1] ? w.f(key, m[1]) : w.t(key);
  }
  return s;
}

// ⚠️ The fields a definition keeps that no run applies yet (internal/defs: output and budget).
const keptOnly = ['output', 'budget'];
const headOf = text => /^---[ \t]*\n([\s\S]*?)\n---/.exec(String(text || '').replace(/\r\n/g, '\n'))?.[1] || '';

// notes are what the checks section says of row r beside the coordinator's warnings, as [key, …args].
function notesOf(r, on) {
  const s = ag.spec(r), p = r.profile || {};
  const out = [];
  if (s.provider === 'codex' && (p.mcp?.length || Object.keys(p.hooks || {}).length)) out.push(['ag.note.codex']);
  const kept = keptOnly.filter(k => new RegExp(`^${k}:`, 'm').test(headOf(r.view?.text)));
  if (kept.length) out.push(['ag.note.kept', kept.join(', ')]);
  for (const x of on) if (x.state === 'auth' || x.state === 'missing') out.push(['ag.note.' + x.state, x.name, s.provider]);
  if (r.profile && on.length && !ag.runnable(on).length) out.push(['ag.note.nowhere']);
  return out;
}

const saveFile = (name, text) => {
  const url = URL.createObjectURL(new Blob([text], {type: 'text/markdown'}));
  const a = globalThis.document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url));
};

function useSource() {
  const w = useWords();
  const name = useName();
  return (r, st) => {
    if (r.source === 'project') return w.f('ag.from.project', st.projects?.[ag.projectOf(r.view.owner)]?.name || ag.projectOf(r.view.owner));
    if (r.source === 'shared' || r.source === 'others') return w.f('ag.from.' + r.source, name(r.view.owner));
    return w.t('ag.from.' + r.source);
  };
}

const specLine = r => { const s = ag.spec(r); return [s.provider, s.model, s.effort].filter(Boolean).join(' · '); };
const checkOf = r => (!r.view?.text ? '' : r.view.warnings?.length ? '!' : '✓');

function CheckMark({r}) {
  const {t} = useWords();
  const c = checkOf(r);
  if (!c) return null;
  return html`<span class=${cx('st', c === '✓' ? 's-success' : 's-unknown')} title=${t(c === '✓' ? 'ag.checkOK' : 'ag.checkWarn')}
    aria-label=${t(c === '✓' ? 'ag.checkOK' : 'ag.checkWarn')}>${c}</span>`;
}

// Facts is one agent: what it runs as, what uses it, the machines it runs on and its checks.
function Facts({r, st, on, pinned}) {
  const w = useWords();
  const {t, f} = w;
  const s = ag.spec(r);
  const used = ag.usedBy(st, r.name);
  const shown = on.filter(x => x.state !== 'pinned');
  const glyph = {ok: '✓', offline: '○', auth: '!', missing: '·', unknown: '?'};
  const tone = {ok: 's-success', offline: 's-muted', auth: 's-unknown', missing: 's-muted', unknown: 's-muted'};
  const notes = [...(r.view?.warnings || []).map(x => warningText(w, x)), ...notesOf(r, shown).map(([k, ...a]) => (a.length ? f(k, ...a) : t(k)))];
  return html`<div class="ag-facts">
    <dl class="facts">
      <dt>${t('ag.role')}</dt><dd>${r.view?.role || '—'}</dd>
      <dt>${t('ag.runsAs')}</dt><dd class="mono">${specLine(r) || '—'}</dd>
      ${s.permission && html`<dt>${t('ag.permission')}</dt><dd class="mono">${s.permission}</dd>`}
      ${s.deny.length > 0 && html`<dt>${t('ag.deny')}</dt><dd class="mono">${s.deny.join(' ')}</dd>`}
      <dt>${t('ag.usedBy')}</dt><dd>${!used.projects.length && !used.tasks.length ? t('ag.unused') : html`<ul class="ag-list">
        ${used.projects.map(x => html`<li key=${x.project.id}>${f('ag.usedRoles', x.project.name, x.roles.map(role => role || t('ag.default')).join(', '))}</li>`)}
        ${used.tasks.length > 0 && html`<li title=${used.tasks.map(x => x.title).join('\n')}>${used.tasks.length === 1 ? t('ag.usedTask') : f('ag.usedTasks', used.tasks.length)}</li>`}</ul>`}</dd>
    </dl>
    <section class="det-sec"><h3 class="det-h">${t('ag.machines')}</h3>
      ${!r.profile ? html`<p class="t-muted ag-p">${t('ag.notYours')}</p>` : html`
        ${pinned && html`<p class="ag-p">${f('ag.pinned', s.machine)}</p>`}
        ${shown.length ? html`<ul class="ag-on">${shown.map(x => html`<li key=${x.name}><span class=${cx('st', tone[x.state])} aria-hidden="true">${glyph[x.state]}</span>
          <span class="mono">${x.name}</span><span class="t-muted">${x.state === 'ok' || x.state === 'offline' ? t('ag.on.' + x.state) : f('ag.on.' + x.state, s.provider)}</span></li>`)}</ul>`
          : !pinned && html`<p class="t-muted ag-p">${t('ag.noMachines')}</p>`}`}
    </section>
    ${notes.length > 0 && html`<section class="det-sec"><h3 class="det-h">${t('ag.checks')}</h3>
      <ul class="ag-checks">${notes.map(n => html`<li><span class="st s-unknown" aria-hidden="true">!</span><span>${n}</span></li>`)}</ul></section>`}
  </div>`;
}

// Body is the picked agent's definition, command or sharing.
function Body({r, st, tab, onTab}) {
  const w = useWords();
  const {t, f} = w;
  const name = useName();
  const v = r.view;
  const profileText = () => {
    const {name: _, ...rest} = r.profile;
    return `# config.json → agents.${r.name}\n` + JSON.stringify(rest, null, 2);
  };
  const def = () => (r.kind === 'profile' ? html`<pre class="box ag-text">${profileText()}</pre>`
    : v.text ? html`<pre class="box ag-text">${v.text}</pre>`
      : html`<p class="t-muted ag-p">${f('ag.hidden', name(v.owner))}</p>`);
  const launch = () => (r.kind === 'profile' ? html`<p class="t-muted ag-p">${t('ag.launchNone')}</p>`
    : v.launch?.length ? html`<pre class="box ag-text">${v.launch.join(' ')}</pre><p class="t-muted ag-p">${t('ag.launchNote')}</p>`
      : html`<p class="t-muted ag-p">${t('ag.launchHidden')}</p>`);
  const share = () => {
    if (r.kind === 'profile') return html`<p class="t-muted ag-p">${t('ag.profileShare')}</p>`;
    const p = ag.projectOf(v.owner);
    const lines = [];
    if (p) lines.push(f('ag.projectOwned', st.projects?.[p]?.name || p));
    if (v.share) {
      if (v.share.all) lines.push(t('ag.shareAll'));
      for (const u of v.share.users || []) lines.push(f('ag.shareUser', name(u)));
      for (const x of v.share.projects || []) lines.push(f('ag.shareProject', st.projects?.[x]?.name || x));
      if (v.share.all || v.share.users?.length || v.share.projects?.length) lines.push(t(v.share.view ? 'ag.shareView' : 'ag.shareNoView'));
      else if (!p) lines.push(t('ag.shareNone'));
    } else {
      if (!p) lines.push(f('ag.toYou', name(v.owner)));
      lines.push(t(v.text ? 'ag.youRead' : 'ag.youUse'));
    }
    if (!r.profile) lines.push(t('ag.notYours'));
    return html`<ul class="ag-list">${lines.map(x => html`<li>${x}</li>`)}</ul>`;
  };
  return html`<div class="ag-body-tabs">
    <${Tabs} label=${t('ag.tabs')} idPrefix="ag" value=${tab} onChange=${onTab}
      tabs=${['def', 'launch', 'share'].map(id => ({id, label: t('ag.tab.' + id)}))} />
    <div class="ag-pane" role="tabpanel" id=${'ag-' + tab + '-pane'} aria-labelledby=${'ag-' + tab}>${({def, launch, share})[tab]()}</div>
  </div>`;
}

// Editor writes a definition's Markdown: mode is edit, new, subagent (an import) or copy; owners are where a new one may go. The
// notes say what the save will do by the name the text gives.
function Editor({mode, name: was, text: first, list, owners, projects, busyOf, error, onSave, onCheck, onClose}) {
  const w = useWords();
  const {t, f} = w;
  const [text, setText] = useState(first);
  const [owner, setOwner] = useState(owners[0] || '');
  const [checked, setChecked] = useState(null);
  const edit = set => v => { set(v); setChecked(null); };
  const file = useRef(null);
  const readFile = e => {
    const x = e.currentTarget.files?.[0];
    if (x) x.text().then(setText, () => {});
    e.currentTarget.value = '';
  };
  const n = ag.nameOf(mode === 'subagent' ? ag.imported(text) : text);
  const other = list.find(r => r.name === n);
  const empty = !text.trim(), itself = mode === 'edit' && n === was;
  const bad = empty ? '' : !n ? t('ag.nameless') : other?.kind === 'def' && !other.view.manage && !itself ? f('ag.taken', n) : '';
  const notes = [];
  if (!bad && mode === 'edit' && n !== was) notes.push(f('ag.renamed', n, was));
  if (!bad && other?.kind === 'def' && !itself) notes.push(f('ag.overwrites', n));
  if (!bad && other?.kind === 'profile') notes.push(f('ag.shadows', n));
  const title = mode === 'edit' ? f('ag.editTitle', was) : mode === 'copy' ? f('ag.copyTitle', was) : t(mode === 'subagent' ? 'ag.subagent' : 'ag.new');
  const sent = () => ({text: mode === 'subagent' ? ag.imported(text) : text, owner: mode === 'edit' || other ? '' : owner, name: n});
  const save = () => onSave(sent());
  const check = () => {
    setChecked({busy: true});
    onCheck(sent()).then(v => setChecked({v}), e => setChecked({refused: apiText(w, e)}));
  };
  const acts = [{label: t('home.cancel'), onClick: onClose},
    ...(onCheck ? [{label: t('ag.check'), disabled: empty || !!checked?.busy, onClick: check}] : []),
    {label: t('form.save'), kind: 'primary', keyName: 'Mod+Enter', disabled: busyOf(n) || empty || !!bad, onClick: save}];
  return html`<${Modal} full title=${title} onClose=${onClose} actions=${acts}>
    ${mode !== 'edit' && owners.length > 1 && html`<div class="field"><span class="brief-label">${t('ag.owner')}</span>
      <${Segmented} label=${t('ag.owner')} value=${owner} onChange=${edit(setOwner)}
        options=${owners.map(o => ({value: o, label: o ? f('ag.from.project', projects[ag.projectOf(o)]?.name || o) : t('ag.ownerMe')}))} /></div>`}
    ${mode === 'subagent' && html`<div class="ag-file-row"><${Button} onClick=${() => file.current?.click()}>${t('ag.fromFile')}<//>
      <input ref=${file} type="file" class="ag-file" accept=".md,text/markdown,text/plain" tabindex="-1" aria-hidden="true" onChange=${readFile} /></div>`}
    <${TextArea} label=${t('ag.text')} value=${text} onInput=${edit(setText)} rows=${14} mono note=${t(mode === 'subagent' ? 'ag.importNote' : 'ag.textNote')}
      error=${error || bad} />
    ${notes.map(x => html`<p class="ag-p t-warning">${x}</p>`)}
    <${Checked} c=${checked} />
  <//>`;
}

// Checked is what agentdef.check said of the editor's text: it would be refused, and why; or it passes, with its
// warnings.
function Checked({c}) {
  const w = useWords();
  const {t, f} = w;
  if (!c || c.busy) return null;
  if (c.refused) return html`<p class="ag-p t-failed" role="status">${f('ag.checkRefused', c.refused)}</p>`;
  const errs = c.v?.errors || [], warns = c.v?.warnings || [];
  const item = (g, tone, x) => html`<li><span class=${cx('st', tone)} aria-hidden="true">${g}</span><span>${x}</span></li>`;
  return html`<ul class="ag-checks" role="status">
    ${errs.length ? item('✗', 's-failed', t('ag.checkBad')) : item('✓', 's-success', t('ag.checkOK'))}
    ${errs.map(x => item('·', 's-failed', x))}
    ${warns.map(x => item('!', 's-unknown', warningText(w, x)))}
  </ul>`;
}

// New is a new definition's first step: its name, kind, model and role, which make the Markdown the editor opens with.
function New({list, machines, onNext, onClose}) {
  const {t, f} = useWords();
  const [name, setName] = useState('');
  const [provider, setProvider] = useState(ag.providers[0]);
  const [model, setModel] = useState('');
  const [role, setRole] = useState('implement');
  const n = name.trim();
  const error = !n ? '' : !ag.defName.test(n) ? t('ag.nameBad') : list.some(r => r.name === n) ? f('ag.nameTaken', n) : '';
  const has = machines.filter(m => m.agents?.[provider]?.installed).map(m => m.name);
  const next = () => onNext(ag.draft({name: n, provider, model, role}));
  return html`<${Modal} title=${t('ag.new')} onClose=${onClose}
    actions=${[{label: t('home.cancel'), onClick: onClose}, {label: t('ag.next'), kind: 'primary', keyName: 'Mod+Enter', disabled: !n || !!error, onClick: next}]}>
    <${TextInput} label=${t('ag.name')} value=${name} onInput=${setName} mono autoFocus note=${error ? null : t('ag.nameNote')} error=${error} />
    <div class="field"><span class="brief-label">${t('ag.kind')}</span>
      <${Segmented} label=${t('ag.kind')} value=${provider} onChange=${setProvider} options=${ag.providers.map(p => ({value: p, label: p}))} />
      <span class="field-note">${has.length ? f('ag.installedOn', has.join(' ')) : t('ag.installedNone')}</span></div>
    <${TextInput} label=${t('ag.model')} value=${model} onInput=${setModel} mono note=${t('ag.modelNote')} />
    ${ag.aliases[provider].length > 0 && html`<${Chips} label=${t('ag.aliases')}>${ag.aliases[provider].map(a => html`<${Chip} key=${a} label=${a} on=${model === a}
      onClick=${() => setModel(a)} />`)}<//>`}
    <div class="field"><span class="brief-label">${t('ag.role')}</span>
      <${Segmented} label=${t('ag.role')} value=${role} onChange=${setRole} options=${ag.roles.map(x => ({value: x, label: x}))} /></div>
  <//>`;
}

function Share({r, people, projects, busy, onSave, onClose}) {
  const {t, f} = useWords();
  const s = r.view.share || {};
  const [all, setAll] = useState(!!s.all);
  const [users, setUsers] = useState(s.users || []);
  const [ps, setPs] = useState(s.projects || []);
  const [view, setView] = useState(!!s.view);
  const save = () => onSave({name: r.name, share: {users: all ? [] : users, projects: all ? [] : ps, all, view}});
  return html`<${Modal} title=${f('ag.shareTitle', r.name)} onClose=${onClose}
    actions=${[{label: t('home.cancel'), onClick: onClose}, {label: t('form.save'), kind: 'primary', keyName: 'Mod+Enter', disabled: busy, onClick: save}]}>
    <${Check} label=${t('ag.shareEveryone')} note=${t('ag.shareEveryoneNote')} on=${all} onChange=${setAll} />
    ${!all && html`<${Picker} label=${t('ag.users')} multi value=${users} onChange=${setUsers} options=${people.filter(o => o.value !== r.view.owner)} />
      <${Picker} label=${t('ag.projects')} multi value=${ps} onChange=${setPs} options=${projects} />`}
    <${Check} label=${t('ag.viewField')} note=${t('ag.viewNote')} on=${view} onChange=${setView} />
  <//>`;
}

function Remove({r, st, onGo, onClose}) {
  const {t, f} = useWords();
  const used = ag.usedBy(st, r.name);
  const what = [...used.projects.map(x => x.project.name), ...used.tasks.map(x => x.title)];
  return html`<${Modal} title=${f('ag.removeTitle', r.name)} onClose=${onClose}
    actions=${[{label: t('confirm.keep'), onClick: onClose}, {label: t('ag.remove'), kind: 'primary', keyName: 'Mod+Enter', onClick: () => { onClose(); onGo(); }}]}>
    <p>${t('ag.removeNote')}</p>
    ${what.length > 0 && html`<p class="t-warning">${f('ag.removeUsed', what.join(', '))}</p>`}
  <//>`;
}

// Agents: agentDefs reads the two lists (core/agents.js); download gives the viewer a file (a test passes its own).
export function Agents({store, commands, toasts, session, wire, agentDefs, download = saveFile}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  const names = useNames();
  const sourceText = useSource();
  const status = useSignalValue(wire.status);
  const rev = useSignalValue(store.rev.agent_defs);
  const defs = useSignalValue(agentDefs.defs), agents = useSignalValue(agentDefs.agents);
  const machines = useSignalValue(store.machines);
  useSignalValue(store.rev.projects);
  useSignalValue(store.rev.tasks);
  useSignalValue(store.rev.shares);
  useSignalValue(commands.pending);
  const [source, setSource] = useState('all');
  const [picked, setPicked] = useState('');
  const [tab, setTab] = useState('def');
  const [modal, setModal] = useState(null);
  const [opened, setOpened] = useState('');
  const failed = e => toasts.show({text: apiText(w, e), tone: 'danger'});
  useEffect(() => { if (status === 'open') agentDefs.read().catch(failed); }, [status, rev]);

  const st = store.state, me = session?.id || '';
  const online = status === 'open';
  const list = ag.rows(defs, agents, me);
  const count = ag.counts(list);
  const shown = source === 'all' ? list : list.filter(r => r.source === source);
  const cur = shown.find(r => r.name === picked) || shown[0] || null;
  const usable = ag.usableMachines(machines, st, session);
  const onOf = r => (r?.profile ? ag.runsOn(ag.spec(r), usable) : []);
  const close = () => setModal(null);
  const key = name => 'agentdef:' + name;
  const busy = name => commands.state(key(name)) === 'pending';

  const save = ({text, owner, name}) => {
    commands.send('agentdef.save', owner ? {text, owner} : {text}, {key: key(name)}).then(v => {
      close();
      setPicked(v?.name || name);
      toasts.show({text: v?.warnings?.length === 1 ? f('ag.savedWarning', v.name) : v?.warnings?.length ? f('ag.savedWarn', v.name, v.warnings.length) : f('ag.saved', v?.name || name)});
    }, e => setModal(m => (m?.kind === 'editor' ? {...m, error: e.detail ? f('ag.refused', e.detail) : apiText(w, e)} : m)));
  };
  const share = p => commands.send('agentdef.share', p, {key: key(p.name)}).then(() => { close(); toasts.show({text: f('ag.sharedDone', p.name)}); }, failed);
  const remove = r => commands.send('agentdef.remove', {name: r.name}, {key: key(r.name)}).then(() => toasts.show({text: f('ag.removed', r.name)}), failed);
  const edit = r => setModal({kind: 'editor', mode: 'edit', name: r.name, text: r.view.text});
  const copy = r => { const n = ag.copyName(r.name, list.map(x => x.name)); setModal({kind: 'editor', mode: 'copy', name: r.name, text: ag.copied(r, n)}); };

  const people = Object.entries(names).filter(([id]) => id !== 'local').map(([id, n]) => ({value: id, label: n, sub: id})).sort((a, b) => a.label.localeCompare(b.label));
  const projectOptions = Object.values(st.projects).map(p => ({value: p.id, label: p.name, sub: p.id})).sort((a, b) => a.label.localeCompare(b.label));
  const owners = ag.ownersFor(st, session);
  const dialog = modal && ({
    editor: () => html`<${Editor} key=${modal.mode + modal.name} mode=${modal.mode} name=${modal.name} text=${modal.text} list=${list} owners=${owners}
      projects=${st.projects} busyOf=${n => !online || busy(n)} error=${modal.error} onSave=${save} onClose=${close}
      onCheck=${wire?.has?.('agentdef.check') ? ({text, owner}) => wire.call('agentdef.check', owner ? {text, owner} : {text}) : null} />`,
    new: () => html`<${New} list=${list} machines=${usable} onClose=${close} onNext=${text => setModal({kind: 'editor', mode: 'new', name: '', text})} />`,
    share: () => cur && html`<${Share} r=${list.find(r => r.name === modal.name) || cur} people=${people} projects=${projectOptions}
      busy=${!online || busy(modal.name)} onSave=${share} onClose=${close} />`,
    remove: () => html`<${Remove} r=${modal.r} st=${st} onGo=${() => remove(modal.r)} onClose=${close} />`,
  })[modal.kind]?.();

  const chips = html`<${Chips} label=${t('ag.title')}>${ag.sources.filter(s => s === 'all' || count[s] > 0).map(s => html`<${Chip} key=${s} label=${t('ag.src.' + s)}
    count=${count[s]} on=${source === s} onClick=${() => setSource(s)} />`)}<//>`;
  const columns = [
    {id: 'name', label: t('ag.col.name'), width: 'minmax(0, 1.8fr)', mobile: 'primary', sort: (a, b) => a.name.localeCompare(b.name),
      render: r => html`<span class="ag-name"><b class="mono ell">${r.name}</b>${!phone && r.view?.description && html`<span class="t-muted ell">${r.view.description}</span>`}</span>`},
    {id: 'role', label: t('ag.col.role'), width: '88px', render: r => r.view?.role || '—',
      sort: (a, b) => (a.view?.role || '').localeCompare(b.view?.role || '')},
    {id: 'spec', label: t('ag.col.spec'), width: 'minmax(0, 1.5fr)', mobile: 'secondary', render: r => html`${phone && r.view?.role && r.view.role + ' · '}<span class="mono ell" title=${specLine(r)}>${specLine(r) || '—'}</span>`},
    {id: 'source', label: t('ag.col.source'), width: 'minmax(0, 1fr)', mobile: 'secondary', render: r => sourceText(r, st),
      sort: (a, b) => ag.sources.indexOf(a.source) - ag.sources.indexOf(b.source) || a.name.localeCompare(b.name)},
    {id: 'machines', label: t('ag.col.machines'), width: 'minmax(0, 1.4fr)', render: r => {
      if (!r.profile) return html`<span class="t-muted">—</span>`;
      const on = ag.runnable(onOf(r));
      return on.length ? html`<span class="mono ell" title=${on.join(' ')}>${on.join(' ')}</span>` : html`<span class="t-warning">${t('ag.nowhere')}</span>`;
    }},
    {id: 'check', label: t('ag.col.check'), width: '48px', align: 'center', mobile: 'trailing', render: r => html`<${CheckMark} r=${r} />`},
  ];
  const table = html`<${Table} label=${t('ag.title')} columns=${columns} rows=${shown} rowKey=${r => r.name} selected=${cur?.name}
    onSelect=${n => { setPicked(n); setTab('def'); }} onOpen=${phone ? n => setOpened(n) : n => { const r = list.find(x => x.name === n); if (r && online && ag.may(r).edit) edit(r); }}
    active=${!modal && !(phone && opened)} empty=${defs === null ? t('ag.loading') : list.length ? t('ag.noneHere') : t('ag.none')} />`;

  if (phone) {
    const open = opened && list.find(r => r.name === opened);
    return html`<div class="ag ag-phone">
      <p class="t-muted ag-p">${t('ag.lead')}</p>
      ${chips}
      ${table}
      <p class="empty t-muted">${t('ag.desktop')}</p>
      ${open && html`<${Drawer} title=${open.name} onClose=${() => setOpened('')}>
        <div class="ag-phone-head"><span>${sourceText(open, st)}</span><${CheckMark} r=${open} /></div>
        ${open.view?.description && html`<p class="ag-p">${open.view.description}</p>`}
        <${Facts} r=${open} st=${st} on=${onOf(open)} pinned=${!!ag.spec(open).machine} />
        <${Body} r=${open} st=${st} tab=${tab} onTab=${setTab} />
      <//>`}
    </div>`;
  }

  const acts = cur && ag.may(cur);
  const meta = !cur ? '' : cur.kind === 'profile' ? t('ag.config') : cur.view.updated_at ? f('ag.rev', cur.view.rev || 1, day(cur.view.updated_at) + ' ' + clock(cur.view.updated_at)) : '';
  return html`<div class="ag">
    <div class="ag-head">
      <h1 class="tasks-title">${t('ag.title')}</h1><span class="t-muted">${t('ag.lead')}</span>
      <span class="ag-head-acts">
        <${Button} disabled=${!online} onClick=${() => setModal({kind: 'editor', mode: 'subagent', name: '', text: ''})}>${t('ag.subagent')}<//>
        <${Button} kind="primary" icon="plus" disabled=${!online} onClick=${() => setModal({kind: 'new'})}>${t('ag.new')}<//>
      </span>
      ${chips}
    </div>
    <div class="ag-body">
      <div class="ag-main">
        <${Panel} title=${t('ag.src.' + source)} count=${shown.length}>${table}<//>
        <p class="t-muted ag-foot">${t('ag.foot')}</p>
      </div>
      ${cur && html`<aside class="ag-aside panel" aria-label=${cur.name}>
        <header class="ag-aside-head"><b class="mono">${cur.name}</b><span class="chip">${sourceText(cur, st)}</span><${CheckMark} r=${cur} /></header>
        ${meta && html`<span class="t-muted ag-meta">${meta}</span>`}
        ${cur.view?.description && html`<p class="ag-p">${cur.view.description}</p>`}
        <${Facts} r=${cur} st=${st} on=${onOf(cur)} pinned=${!!ag.spec(cur).machine} />
        <${Body} r=${cur} st=${st} tab=${tab} onTab=${setTab} />
        <div class="det-acts">
          ${acts.edit && html`<${Button} kind="primary" keyName="Enter" disabled=${!online} onClick=${() => edit(cur)}>${t('ag.edit')}<//>`}
          ${acts.share && html`<${Button} disabled=${!online} onClick=${() => setModal({kind: 'share', name: cur.name})}>${t('ag.share')}<//>`}
          ${acts.copy && html`<${Button} disabled=${!online} onClick=${() => copy(cur)}>${t('ag.copy')}<//>`}
          ${acts.export && html`<${Button} kind="quiet" onClick=${() => download(cur.name + '.md', cur.view.text)}>${t('ag.export')}<//>`}
          ${acts.remove && html`<${Button} kind="quiet danger" disabled=${!online} onClick=${() => setModal({kind: 'remove', r: cur})}>${t('ag.remove')}<//>`}
          ${cur.kind === 'profile' && html`<span class="t-muted ag-p">${t('ag.inConfig')}</span>`}
        </div>
      </aside>`}
    </div>
    ${dialog}
  </div>`;
}
