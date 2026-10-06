// sessions is one machine's own Claude / Codex sessions (?page=machines&sessions=<machine>), read through node.call by
// its owner and whom its owner shares them with (read only for them, under a line saying whose they are, without the
// resume line; taken back while read, the page says so and drops what it showed): every session its node shares with
// who of them is running and the task a run of it worked for
// (its label opens the task), the project it belongs to (where the server tells it: sessions.list), its favorite star,
// tags and summary (an archived one dimmed), a text filter, a project filter (&project=<id>, none for those in no
// project) and a favorites-only switch (&fav=1), and one of them open
// (&session=<provider>:<id>), its conversation paged from the newest with the earlier on demand and the line that
// resumes it on that machine. Read only; nothing is polled: the list and a conversation are read when they open and
// again on Refresh. On a desktop the list and the open conversation sit side by side; on a phone the list is cards and
// a conversation takes the screen.
import {useState, useEffect, useLayoutEffect, useRef} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useSignalValue, useName} from '../ui/base.js';
import {Picker} from '../ui/picker.js';
import {Button, Chip} from '../ui/controls.js';
import {Icon} from '../ui/icons.js';
import {TextInput} from '../ui/input.js';
import {Table} from '../ui/table.js';
import {Status} from '../ui/status.js';
import {Drawer} from '../ui/overlay.js';
import {Secret} from '../ui/secret.js';
import {Markdown} from '../ui/markdown.js';
import {register} from '../core/i18n.js';
import {clock, day} from '../core/format.js';
import * as ss from '../core/sessions.js';
import {code} from '../core/proto.js';
import {personal, sharedToMe} from '../core/team.js';
import {apiText} from './words.js';

register('sessions', {
  'sess.title': ['%s 的会话', 'Sessions on %s'], 'sess.refresh': ['刷新', 'Refresh'],
  'sess.filter': ['筛选会话', 'Filter the sessions'], 'sess.filterHint': ['标题、目录、agent、分支、任务、#标签', 'Title, directory, agent, branch, task, #tag'],
  'sess.summary': ['%d 个会话 · %d 个在跑', '%d {session|sessions} · %d running'], 'sess.shown': ['显示 %d 个', '%d shown'],
  'sess.loading': ['正在读…', 'Reading…'], 'sess.none': ['这台机器上没有可看的会话', 'No sessions to read on this machine'],
  'sess.noMatch': ['没有符合的会话', 'No sessions match'], 'sess.pick': ['选一个会话看对话', 'Pick a session to read it'],
  'sess.c.state': ['状态', 'State'], 'sess.c.title': ['标题', 'Title'], 'sess.c.dir': ['目录', 'Directory'], 'sess.c.agent': ['Agent', 'Agent'],
  'sess.c.last': ['最近活动', 'Last active'],
  'sess.running': ['在跑', 'running'], 'sess.blocked': ['在等人', 'waiting'], 'sess.untitled': ['（没有标题）', '(untitled)'],
  'sess.shareRuns': ['%s 只共享 tend 运行的会话，你在那台机器上手开的不在这里。要看全部：在它的 tend config.json 的 "node" 里写 "share_sessions": "all"，再重启它的 tend node。',
    '%s shares only the sessions of tend runs, so those opened there by hand are not here. To see them all, set "share_sessions": "all" under "node" in its tend config.json and restart its tend node.'],
  'sess.shareNone': ['%s 不共享任何会话。要看全部：在它的 tend config.json 的 "node" 里写 "share_sessions": "all"，再重启它的 tend node。',
    '%s shares no sessions. To see them all, set "share_sessions": "all" under "node" in its tend config.json and restart its tend node.'],
  'sess.why.offline': ['%s 不在线：连上以后刷新', '%s is offline: refresh once it connects'],
  'sess.why.unauthorized': ['%s 的节点不让读这些会话', 'The node on %s does not let these sessions be read'],
  'sess.why.unknown_method': ['%s 的 tend 太旧，读不了会话', 'The tend on %s is too old to read sessions'],
  'sess.why.timeout': ['%s 没有及时回答：再刷新一次', '%s did not answer in time: refresh again'],
  'sess.cannot': ['%s 的会话不再共享给你', 'The sessions of %s are no longer shared with you'],
  'sess.cannotWhy': ['主人改了「会话谁能看」。要再看，请 %s 在机器页把你加回去。', 'Its owner changed who sees them. Ask %s to add you back on the machines page.'],
  'sess.backToMachines': ['回到机器', 'Back to machines'],
  'sess.sharedBy': ['%s 的 %s，共享给你，只读', '%s\'s %s, shared with you, read only'],
  'sess.sharedNote': ['不能在这里恢复，也不能从它建任务', 'You cannot resume it here or make a task from it'],
  'sess.readonly': ['只读：别人共享给你的会话，恢复命令只给机器的主人', 'Read only: a session shared with you; only the machine\'s owner gets the resume command'],
  'sess.readonlyShort': ['%s 的 %s，只读', '%s\'s %s, read only'],
  'sess.shareRunsShared': ['%s 只放出 tend 运行的会话，%s 在那台机器上手开的不在这里。这是那台机器自己的设置，只有主人能改。',
    '%s releases only the sessions of tend runs, so those %s opened there by hand are not here. That is the machine\'s own setting; only its owner changes it.'],
  'sess.shareNoneShared': ['%s 不放出任何会话。这是那台机器自己的设置，只有主人能改。', '%s releases no sessions. That is the machine\'s own setting; only its owner changes it.'],
  'sess.resume': ['在 %s 上恢复', 'Resume on %s'], 'sess.noResume': ['tend 不会恢复这种 agent 的会话', 'tend cannot resume this agent\'s sessions'],
  'sess.earlier': ['加载更早的', 'Load earlier'], 'sess.start': ['会话从这里开始', 'The session starts here'],
  'sess.empty': ['这个会话还没有对话', 'This session has no conversation yet'], 'sess.reread': ['会话文件换了，已从最新重读', 'The session\'s file changed: read again from the newest'],
  'sess.you': ['你', 'You'], 'sess.full': ['全文', 'Full text'], 'sess.fold': ['收起', 'Fold'],
  'sess.steps': ['%d 步：%s', '%d {step|steps}: %s'], 'sess.turns': ['%d 轮', '%d {turn|turns}'], 'sess.branch': ['分支 %s', 'branch %s'],
  'sess.gone': ['没有这个会话，或者节点不让读它', 'No such session, or its node does not let it be read'],
  'sess.openTask': ['打开任务 %s', 'Open task %s'],
  'sess.favOnly': ['只看收藏', 'Favorites only'], 'sess.favSummary': ['收藏 %d / %d 个会话', '%d favorited of %d {session|sessions}'],
  'sess.favNone': ['%s 上没有收藏的会话', 'No favorite sessions on %s'], 'sess.showAll': ['看全部会话', 'Show all sessions'],
  'sess.project': ['项目', 'Project'], 'sess.projectAll': ['全部项目', 'All projects'], 'sess.projectNone': ['未归项目', 'No project'],
  'sess.projectNoneSub': ['目录不在任何项目下的会话', 'Sessions whose directory is in no project'], 'sess.projectTeam': ['团队 · 负责人 %s', 'Team · owned by %s'],
  'sess.projectPersonal': ['个人', 'Personal'], 'sess.projectSummary': ['%s · %d / %d 个会话', '%s · %d of %d {session|sessions}'],
  'sess.projectVia': ['项目 %s · 按目录 %s 归入', 'Project %s · by its directory %s'], 'sess.projectEmpty': ['%s 在 %s 上没有会话', '%s has no sessions on %s'],
  'sess.projectEmptyWhy': ['项目只按目录归会话：%s 的目录都不在 %s 上', 'Projects group sessions by directory: none of %s\'s directories is on %s'],
  'sess.favorited': ['已收藏', 'Favorite'], 'sess.favoritedAt': ['收藏于 %s', 'Favorited %s'], 'sess.archived': ['已归档', 'Archived'],
});

const when = (at, now) => {
  const t = Date.parse(at || '');
  if (!t || t < 0) return '';
  return day(t) === day(now) ? clock(t) : day(t) + ' ' + clock(t);
};
const why = (w, machine, e) => (['offline', 'unauthorized', 'unknown_method', 'timeout'].includes(e?.code) ? w.f('sess.why.' + e.code, machine) : apiText(w, e));
const liveState = l => (!l ? '' : l.Status === 'blocked' ? 'waiting' : 'running');
const liveWord = (t, l) => (l?.Status === 'blocked' ? t('sess.blocked') : t('sess.running'));
const taskText = x => [x.id, x.title].filter(Boolean).join(' ') + (x.stage ? ' · ' + x.stage : '');

// TaskLabel is the task a run of the session worked for; with onOpen it opens that task.
function TaskLabel({task, onOpen}) {
  const {f} = useWords();
  if (!onOpen) return html`<span class="sess-task ell">${taskText(task)}</span>`;
  return html`<button type="button" class="sess-task ell" title=${f('sess.openTask', task.id)} aria-label=${f('sess.openTask', task.id)}
    onClick=${e => { e.stopPropagation(); onOpen(task.id); }}>${taskText(task)}</button>`;
}

// ⚠️ A list row's height in px, as css/pages.css fixes it (.sess-list .tr; .sess-phone .card-row plus the 1px line
// between cards): the table draws a long list in windows of it.
export const ROW = {desktop: 50, phone: 100};

// Star marks a favorite; nothing changes it here.
function Star() {
  const {t} = useWords();
  return html`<span class="sess-star" role="img" title=${t('sess.favorited')} aria-label=${t('sess.favorited')}><${Icon} name="star" size=${14} /></span>`;
}

const tagChip = x => html`<span class="chip sess-tag">#${x}</span>`;
const Archived = () => html`<span class="chip">${useWords().t('sess.archived')}</span>`;

// Tags are a list row's first tags and the count of the rest.
function Tags({tags}) {
  const {shown, more} = ss.tagsShown(tags);
  if (!shown.length) return null;
  return html`<span class="sess-tags">${shown.map(tagChip)}${more > 0 && html`<span class="chip">+${more}</span>`}</span>`;
}

// ⚠️ How far a message's folded text runs before it offers its full text (the node keeps up to 16 KiB of one).
const LONG = 600;

// Message is one turn's text, with the steps the agent took after it folded into one line that opens.
function Message({m, agent, onFull}) {
  const {t, f} = useWords();
  const [full, setFull] = useState(null);
  const [steps, setSteps] = useState(false);
  const user = m.Role === 'user';
  const tools = [...new Set((m.Steps || []).map(s => s.Tool).filter(Boolean))];
  const more = ss.cut(m) || (m.Chars || 0) > LONG;
  const open = () => (full !== null ? setFull(null) : onFull(m).then(setFull, () => {}));
  return html`<li class=${cx('sess-msg', user && 'sess-user')}>
    <div class="sess-msg-head"><b>${user ? t('sess.you') : agent}</b><span class="mono t-muted">${m.At ? clock(m.At) : ''}</span>
      ${more && html`<${Button} kind="quiet" onClick=${open}>${full !== null ? t('sess.fold') : t('sess.full')}<//>`}</div>
    ${full !== null ? html`<div class="sess-text"><${Markdown} text=${full} /></div>` : html`<p class=${cx('sess-text', more && 'sess-clamp')}>${m.Text}</p>`}
    ${tools.length > 0 && html`<button type="button" class="sess-steps" aria-expanded=${steps ? 'true' : 'false'} onClick=${() => setSteps(!steps)}>
      ${f('sess.steps', m.Steps.length, tools.join(' · '))}</button>`}
    ${steps && html`<ul class="sess-step-list">${m.Steps.map((s, i) => html`<li key=${i}><span class="mono">${s.Tool}</span> <span class="t-muted">${s.Text}</span></li>`)}</ul>`}
  </li>`;
}

// Conversation is a session's messages, oldest at the top: the newest page when it opens, earlier pages on demand.
// Conversation: owner is whose machine it is when its sessions are shared with the viewer: read only, no resume line.
function Conversation({wire, machine, row, os, copy, toasts, now, onTask, projectName, owner = ''}) {
  const w = useWords();
  const {t, f} = w;
  const ref = {provider: row.provider, session_id: row.session_id};
  const [s, setS] = useState({msgs: [], from: -1, done: false, file: '', loading: true, error: null});
  const [rev, setRev] = useState(0);
  const box = useRef(null);
  const keep = useRef(null);
  const agent = row.provider === 'codex' ? 'Codex' : row.provider === 'claude' ? 'Claude' : row.provider;
  useEffect(() => {
    let gone = false;
    setS({msgs: [], from: -1, done: false, file: '', loading: true, error: null});
    ss.readPage(wire, machine, ref).then(p => {
      if (gone) return;
      keep.current = 'end';
      setS({msgs: ss.older([], p), from: p.From, done: !!p.Done, file: p.file || '', loading: false, error: null});
    }, e => !gone && setS(o => ({...o, loading: false, error: e})));
    return () => { gone = true; };
  }, [machine, row.key, rev]);
  useLayoutEffect(() => {
    const el = box.current;
    if (!el || !keep.current) return;
    el.scrollTop = keep.current === 'end' ? el.scrollHeight : el.scrollHeight - keep.current;
    keep.current = null;
  }, [s.msgs]);
  const earlier = () => {
    setS(o => ({...o, loading: true}));
    ss.readPage(wire, machine, ref, s.from, s.file).then(p => {
      keep.current = box.current ? box.current.scrollHeight - box.current.scrollTop : null;
      setS(o => ({...o, msgs: ss.older(o.msgs, p), from: p.From, done: !!p.Done, loading: false}));
    }, e => {
      if (e?.code === code.stale) { toasts.show({text: t('sess.reread')}); setRev(n => n + 1); return; }
      setS(o => ({...o, loading: false, error: e}));
    });
  };
  const full = m => ss.readText(wire, machine, ref, m.Off, s.file).catch(e => {
    toasts.show({text: why(w, machine, e), tone: 'danger'});
    throw e;
  });
  const resume = ss.resumeLine(row, os);
  const facts = [agent, row.git_branch && f('sess.branch', row.git_branch), row.turns ? f('sess.turns', row.turns) : '', when(row.last_at, now)].filter(Boolean);
  const phone = usePhone();
  return html`<article class="sess-conv" aria-label=${row.title || row.session_id}>
    <header class="sess-conv-head">
      ${owner && phone && html`<p class="sess-owner"><${Icon} name="eye" size=${14} /><b>${f('sess.readonlyShort', owner, machine)}</b></p>`}
      <div class="sess-conv-title">${row.live && html`<${Status} state=${liveState(row.live)} label=${liveWord(t, row.live)} />`}
        <h2 class="ell">${row.title || t('sess.untitled')}</h2>
        <${Button} kind="quiet" disabled=${s.loading} onClick=${() => setRev(n => n + 1)}>${t('sess.refresh')}<//></div>
      ${row.project && html`<div class="sess-conv-proj t-muted">${f('sess.projectVia', projectName(row.project), row.repo || row.cwd)}</div>`}
      <div class="sess-conv-facts t-muted"><span class="mono ell">${row.cwd}</span><span>${facts.join(' · ')}</span></div>
      ${(row.favorited_at || row.tags?.length || row.archived_at) && html`<div class="sess-conv-meta">
        ${row.favorited_at && html`<${Star} /><span class="t-muted">${f('sess.favoritedAt', day(Date.parse(row.favorited_at)))}</span>`}
        ${(row.tags || []).map(tagChip)}${row.archived_at && html`<${Archived} />`}</div>`}
      ${row.summary && html`<p class="sess-conv-sum">${row.summary}</p>`}
      ${row.task && html`<div class="sess-conv-task"><${TaskLabel} task=${row.task} onOpen=${onTask} /></div>`}
      ${owner ? html`<p class="sess-readonly">${t('sess.readonly')}</p>`
        : resume ? html`<${Secret} label=${f('sess.resume', machine)} value=${resume} copy=${copy} />` : html`<p class="t-muted">${t('sess.noResume')}</p>`}
    </header>
    <div class="sess-scroll" ref=${box}>
      ${s.error && html`<p class="sess-note t-failed">${s.error.code === code.notFound ? t('sess.gone') : why(w, machine, s.error)}</p>`}
      ${!s.error && (s.done ? s.msgs.length > 0 && html`<p class="sess-edge t-muted">${t('sess.start')}</p>`
        : s.msgs.length > 0 && html`<div class="sess-edge"><${Button} disabled=${s.loading} onClick=${earlier}>${t('sess.earlier')}<//></div>`)}
      ${s.loading && !s.msgs.length && html`<p class="empty">${t('sess.loading')}</p>`}
      ${!s.loading && !s.error && !s.msgs.length && html`<p class="empty">${t('sess.empty')}</p>`}
      <ol class="sess-msgs">${s.msgs.map(m => html`<${Message} key=${m.Off} m=${m} agent=${agent} onFull=${full} />`)}</ol>
    </div>
  </article>`;
}

// Sessions: machine is the one whose sessions show, open the key of the one open; may says the viewer may read them
// (its owner, or whom it shares them with), session who the viewer is; copy is the clipboard's (a test passes its own).
export function Sessions({store, wire, router, toasts, session = null, machine, open = '', fav = false, project: asked = '', may = true, copy, clock: now = () => Date.now()}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  const name = useName();
  const machines = useSignalValue(store.machines);
  useSignalValue(store.rev.projects);
  useSignalValue(store.rev.tasks);
  useSignalValue(store.rev.runs);
  const m = machines.find(x => x.name === machine);
  const [rows, setRows] = useState(null);
  const [error, setError] = useState(null);
  const [q, setQ] = useState('');
  const [rev, setRev] = useState(0);
  const [byProject, setByProject] = useState(false);
  const at = now();
  useEffect(() => {
    if (!may) return;
    let gone = false;
    setRows(null);
    setError(null);
    ss.readList(wire, machine).then(r => { if (!gone) { setRows(r.rows); setByProject(r.byProject); } }, e => !gone && setError(e));
    return () => { gone = true; };
  }, [machine, rev, may]);
  const all = may && rows ? ss.linked(rows, ss.links(store.state)) : null;
  const shared = sharedToMe(session, m);
  const owner = shared ? name(m.owner) : '';
  const project = byProject ? asked : '';
  const projects = Object.values(store.state.projects || {}).sort((a, b) => a.name.localeCompare(b.name));
  const projectName = id => (id === ss.NONE ? t('sess.projectNone') : store.state.projects?.[id]?.name || id);
  const favs = all ? ss.favorites(all) : [];
  const ofProject = ss.inProject(all || [], project);
  const pool = ss.inProject(fav ? favs : all || [], project);
  const shown = ss.matches(pool, q);
  const row = all?.find(r => r.key === open);
  const toTask = id => router.go({page: 'tasks', task: id});
  const here = ({session = open, project: p = project, fav: on = fav}) =>
    ({page: 'machines', sessions: machine, ...(session ? {session} : {}), ...(p ? {project: p} : {}), ...(on ? {fav: true} : {})});
  const go = (key, replace) => router.go(here({session: key}), {replace});
  const setFav = on => router.go(here({fav: on}), {replace: true});
  const setProject = p => router.go(here({project: p}), {replace: true});
  const count = p => (all ? String(ss.inProject(all, p).length) : undefined);
  const projectOptions = [{value: '', label: t('sess.projectAll'), note: count('')},
    ...projects.map(p => ({value: p.id, label: p.name, sub: personal(p) ? t('sess.projectPersonal') : f('sess.projectTeam', name(p.owner)), note: count(p.id)})),
    {value: ss.NONE, label: t('sess.projectNone'), sub: t('sess.projectNoneSub'), note: count(ss.NONE)}];
  const back = () => router.back({page: 'machines'});
  const share = m?.share_sessions === 'runs' || m?.share_sessions === 'none';
  const running = (rows || []).filter(r => r.live).length;

  const columns = [
    {id: 'state', label: t('sess.c.state'), width: '28px', render: r => (r.live ? html`<${Status} state=${liveState(r.live)} />` : ''), mobile: 'lead'},
    {id: 'title', label: t('sess.c.title'), width: 'minmax(0, 3fr)', mobile: 'primary', render: r => {
      const titled = html`<span class="sess-titled">${r.favorited_at && html`<${Star} />`}<span class="ell">${r.title || t('sess.untitled')}</span>
        ${!phone && r.archived_at && html`<${Archived} />`}${r.task && html`<${TaskLabel} task=${r.task} onOpen=${phone ? null : toTask} />`}</span>`;
      if (phone) return titled;
      return html`<span class="sess-cell">${titled}${(r.tags?.length || r.summary) && html`<span class="sess-line2"><${Tags} tags=${r.tags} />
        ${r.summary && html`<span class="sess-sum ell">${r.summary}</span>`}</span>`}</span>`;
    }},
    ...(phone ? [
      {id: 'summary', mobile: 'line', render: r => r.summary && html`<span class="sess-sum ell">${r.summary}</span>`},
      {id: 'tags', mobile: 'line', render: r => (r.tags?.length || r.archived_at) && html`<span class="card-secondary"><${Tags} tags=${r.tags} />${r.archived_at && html`<${Archived} />`}</span>`},
    ] : []),
    ...(byProject ? [{id: 'project', label: t('sess.project'), width: '72px', mobile: 'secondary', render: r => (r.project
      ? html`<span class="sess-proj ell">${projectName(r.project)}</span>` : html`<span class="sess-proj ell none">${phone ? t('sess.projectNone') : '—'}</span>`)}] : []),
    {id: 'dir', label: t('sess.c.dir'), width: byProject ? 'minmax(0, 1fr)' : 'minmax(0, 1.1fr)', render: r => html`<span class="mono ell">${r.cwd}</span>`, mobile: 'secondary'},
    {id: 'agent', label: t('sess.c.agent'), width: '56px', render: r => html`<span class="mono">${r.provider}</span>`, mobile: 'secondary'},
    {id: 'last', label: t('sess.c.last'), width: '80px', align: 'right', render: r => html`<span class="mono t-muted">${when(r.last_at, at)}</span>`, mobile: 'trailing',
      sort: (a, b) => (Date.parse(a.last_at || '') || 0) - (Date.parse(b.last_at || '') || 0)},
  ];
  const head = html`<div class=${cx('sess-head', phone && 'sess-head-phone')}>
    <span class="sess-head-row">
      <${Button} kind="quiet" icon="back" onClick=${back}>${t('ui.back')}<//>
      <h1 class="tasks-title ell">${f('sess.title', machine)}</h1>
      ${may && rows && html`<span class="t-muted">${project ? f('sess.projectSummary', projectName(project), pool.length, rows.length)
        : fav ? f('sess.favSummary', favs.length, rows.length) : f('sess.summary', rows.length, running)}${q && pool.length !== shown.length ? ' · ' + f('sess.shown', shown.length) : ''}</span>`}
      <span class="sess-head-acts"><${Button} disabled=${may && !rows && !error} onClick=${() => setRev(n => n + 1)}>${t('sess.refresh')}<//></span>
    </span>
    ${may && html`<div class="sess-tools"><${TextInput} label=${t('sess.filter')} value=${q} onInput=${setQ} placeholder=${t('sess.filterHint')} />
      ${byProject && html`<div class="sess-pfield"><${Picker} label=${t('sess.project')} value=${project} onChange=${setProject} options=${projectOptions} /></div>`}
      <${Chip} label=${t('sess.favOnly')} on=${fav} count=${rows ? favs.length : undefined} onClick=${() => setFav(!fav)} /></div>`}
    ${shared && html`<p class="sess-owner"><${Icon} name="eye" size=${14} /><b>${f('sess.sharedBy', owner, machine)}</b><span class="t-muted">${t('sess.sharedNote')}</span></p>`}
    ${may && share && html`<p class="box sess-share">${shared ? (m.share_sessions === 'none' ? f('sess.shareNoneShared', machine) : f('sess.shareRunsShared', machine, owner))
      : f(m.share_sessions === 'none' ? 'sess.shareNone' : 'sess.shareRuns', machine)}</p>`}
  </div>`;
  const body = !may ? html`<div class="empty sess-gone"><p>${f('sess.cannot', machine)}</p>
      ${m?.owner && m.owner !== session?.id && html`<p>${f('sess.cannotWhy', name(m.owner))}</p>`}
      <${Button} onClick=${() => router.go({page: 'machines'})}>${t('sess.backToMachines')}<//></div>`
    : error ? html`<p class="sess-note t-failed">${why(w, machine, error)}</p>`
    : !rows ? html`<p class="empty">${t('sess.loading')}</p>`
    : project && !ofProject.length ? html`<div class="empty sess-none"><p>${f('sess.projectEmpty', projectName(project), machine)}</p>
      ${project !== ss.NONE && html`<p>${f('sess.projectEmptyWhy', projectName(project), machine)}</p>`}
      <${Button} onClick=${() => setProject('')}>${t('sess.showAll')}<//></div>`
    : fav && !favs.length ? html`<div class="empty sess-none"><p>${f('sess.favNone', machine)}</p><${Button} onClick=${() => setFav(false)}>${t('sess.showAll')}<//></div>`
    : html`<${Table} label=${f('sess.title', machine)} columns=${columns} rows=${shown} rowKey=${r => r.key} selected=${row?.key || ''}
        rowClass=${r => (r.archived_at ? 'sess-archived' : '')} rowHeight=${phone ? ROW.phone : ROW.desktop}
        onSelect=${phone ? null : k => go(k, true)} onOpen=${k => go(k, !phone)} active=${!(phone && row)}
        empty=${rows.length ? t('sess.noMatch') : t('sess.none')} />`;
  const conv = row && html`<${Conversation} key=${row.key} wire=${wire} machine=${machine} row=${row} os=${m?.os || ''} copy=${copy} toasts=${toasts} now=${at} onTask=${toTask} projectName=${projectName} owner=${owner} />`;

  if (phone) {
    return html`<div class="sess sess-phone">${head}${body}
      ${open && all && html`<${Drawer} title=${row?.title || t('sess.untitled')} onClose=${() => router.back(here({session: ''}))}>
        ${conv || html`<p class="empty">${t('sess.gone')}</p>`}<//>`}
    </div>`;
  }
  return html`<div class="sess">
    ${head}
    <div class="sess-body">
      <div class="sess-list">${body}</div>
      <div class="sess-pane">${conv || (may && html`<p class="empty">${open && rows ? t('sess.gone') : t('sess.pick')}</p>`)}</div>
    </div>
  </div>`;
}
