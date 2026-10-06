// sessionview is what the sessions page shows of one session: its conversation under a head that says what it is and
// changes it (favorite, done, archive, edit, make a task, delete; one line instead where the viewer only reads it, a bar
// that restores it when it is in the trash), paged from the newest or, opened from a message search, from the hit with
// its words marked and a way between the hits; the dialog that edits its title, tags and summary; the one that makes a
// task go on with it on its machine; and the one that confirms moving it into its machine's trash.
import {useState, useEffect, useLayoutEffect, useRef} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useActions} from '../ui/base.js';
import {Button, Kbd as KeyCap} from '../ui/controls.js';
import {Icon} from '../ui/icons.js';
import {Status} from '../ui/status.js';
import {Modal} from '../ui/overlay.js';
import {Picker} from '../ui/picker.js';
import {Menu} from '../ui/menu.js';
import {Secret} from '../ui/secret.js';
import {Markdown} from '../ui/markdown.js';
import {TextInput, TextArea} from '../ui/input.js';
import {register} from '../core/i18n.js';
import {clock, day} from '../core/format.js';
import * as ss from '../core/sessions.js';
import {code} from '../core/proto.js';
import {apiText} from './words.js';

register('sessionview', {
  'sess.refresh': ['刷新', 'Refresh'], 'sess.untitled': ['（没有标题）', '(untitled)'], 'sess.running': ['在跑', 'running'], 'sess.blocked': ['在等人', 'waiting'],
  'sess.resume': ['在 %s 上恢复', 'Resume on %s'], 'sess.noResume': ['tend 不会恢复这种 agent 的会话', 'tend cannot resume this agent\'s sessions'],
  'sess.earlier': ['加载更早的', 'Load earlier'], 'sess.start': ['会话从这里开始', 'The session starts here'], 'sess.loading': ['正在读…', 'Reading…'],
  'sess.empty': ['这个会话还没有对话', 'This session has no conversation yet'], 'sess.reread': ['会话文件换了，已从最新重读', 'The session\'s file changed: read again from the newest'],
  'sess.you': ['你', 'You'], 'sess.full': ['全文', 'Full text'], 'sess.fold': ['收起', 'Fold'],
  'sess.steps': ['%d 步：%s', '%d {step|steps}: %s'], 'sess.turns': ['%d 轮', '%d {turn|turns}'], 'sess.branch': ['分支 %s', 'branch %s'],
  'sess.gone': ['没有这个会话，或者节点不让读它', 'No such session, or its node does not let it be read'], 'sess.openTask': ['打开任务 %s', 'Open task %s'],
  'sess.favorited': ['已收藏', 'Favorite'], 'sess.favoritedAt': ['收藏于 %s', 'Favorited %s'], 'sess.favBy': ['%s 收藏', 'Starred by %s'],
  'sess.archived': ['已归档', 'Archived'], 'sess.inProject': ['%s · 项目 %s', '%s · project %s'],
  'sess.a.fav': ['收藏', 'Favorite'], 'sess.a.faved': ['已收藏', 'Favorited'], 'sess.a.done': ['完成', 'Done'], 'sess.a.doneOn': ['已完成', 'Done'],
  'sess.a.archive': ['归档', 'Archive'], 'sess.a.archived': ['已归档', 'Archived'], 'sess.a.edit': ['编辑', 'Edit'], 'sess.more': ['更多', 'More'],
  'sess.readOnly': ['只读：%s 的 %s 共享给你。只有主人能改收藏、标签和摘要，也只有主人能把它建成任务或删除。',
    'Read only: %s shares %s with you. Only the owner can change favorites, tags and summaries, make it a task or delete it.'],
  'sess.oldRO': ['%s 的 tend 是旧版：能看，不能改，也不能建成任务、删除。更新后就能改：tend hosts install %s',
    '%s runs an older tend: read only, no tasks or deleting. Update it to change: tend hosts install %s'],
  'sess.hitNav': ['命中 %d / %d', 'Hit %d / %d'], 'sess.hitPrev': ['上一处', 'Previous hit'], 'sess.hitNext': ['下一处', 'Next hit'], 'sess.hitWords': ['关键词：%s', 'Words: %s'],
  'sess.later': ['后面还有对话', 'The conversation goes on'], 'sess.toLatest': ['回到最新', 'Back to the latest'],
  'sess.unseen': ['这一处在续接前的旧文件里，这里看不到：下面是最新的对话', 'This hit is in the file before the session went on and is not shown here: the newest part follows'],
  'sess.edit.title': ['编辑会话', 'Edit session'], 'sess.edit.name': ['标题', 'Title'], 'sess.edit.tags': ['标签', 'Tags'], 'sess.edit.summary': ['摘要', 'Summary'],
  'sess.edit.tagHint': ['空格或逗号成一个', 'Space or comma ends a tag'], 'sess.edit.common': ['这台机器上常用的：', 'Common on this machine:'],
  'sess.edit.note': ['改的是 %s 上 tend 的收藏记录，TUI 和 tend sessions 里看到的是同一份。', 'This changes tend’s record on %s; the TUI and tend sessions show the same one.'],
  'sess.edit.unfav': ['这条还没收藏：保存后有了记录，但不算收藏。', 'Not a favorite: saving keeps a record without making it one.'],
  'sess.edit.empty': ['标题不能空', 'The title cannot be empty'],
  'sess.edit.stale': ['这条刚在别处改过，已换成最新的内容，看一眼再保存', 'This was just changed elsewhere; the latest is shown, check it and save again'],
  'sess.edit.save': ['保存', 'Save'], 'sess.edit.dropTag': ['去掉 #%s', 'Remove #%s'],
  'sess.make': ['建成任务', 'Make a task'], 'sess.makeTitle': ['把会话建成任务', 'Make a task from this session'], 'sess.makeSub': ['%s · %s · %s', '%s · %s · %s'],
  'sess.makeText': ['要它做什么', 'What should it do'], 'sess.makeAgent': ['档案', 'Profile'], 'sess.makeProject': ['项目', 'Project'],
  'sess.makeAgentSub': ['能接着这个会话', 'Can go on with this session'], 'sess.private': ['私人', 'Private'], 'sess.privateSub': ['只有你看得到', 'Only you see it'],
  'sess.makeNote': ['新任务在 %s 后台接着这个会话做；不选项目就是私人的。', 'The task goes on with this session in the background on %s; without a project it is private.'],
  'sess.makeBusy': ['会话在跑：等它停了再建', 'The session is running: wait until it stops'], 'sess.makeNoAgent': ['没有能接着这个会话的档案', 'No profile can go on with this session'],
  'sess.makeOld': ['%s 的 tend 是旧版，不能建', '%s runs an older tend: cannot make a task'], 'sess.makeOffline': ['没连上 server，不能建', 'Not connected to the server'],
  'sess.del': ['删除…', 'Delete…'], 'sess.delTitle': ['删除会话', 'Delete session'], 'sess.delBody': ['把「%s」移到 %s 的回收站。', 'Move “%s” to the trash on %s.'],
  'sess.delNote': ['对话文件一起移走，回收站里能还原；超过 %d 天的会清掉。', 'Its files go too and can be restored; after %d {day|days} they are purged.'],
  'sess.delNoteKeep': ['对话文件一起移走，回收站里能还原。', 'Its files go too and can be restored.'], 'sess.delBtn': ['删除', 'Delete'],
  'sess.restore': ['还原', 'Restore'], 'sess.inTrash': ['在 %s 的回收站里 · %s 删除 · %d 天后清掉', 'In the trash on %s · deleted %s · purged in %d {day|days}'],
  'sess.inTrashKeep': ['在 %s 的回收站里 · %s 删除', 'In the trash on %s · deleted %s'],
});

const liveState = l => (!l ? '' : l.Status === 'blocked' ? 'waiting' : 'running');
const liveWord = (t, l) => (l?.Status === 'blocked' ? t('sess.blocked') : t('sess.running'));
const taskText = x => [x.id, x.title].filter(Boolean).join(' ') + (x.stage ? ' · ' + x.stage : '');
const agentOf = p => (p === 'codex' ? 'Codex' : p === 'claude' ? 'Claude' : p);

// when is a time as a list shows it: the clock today, else the date too.
export const when = (at, now) => {
  const t = Date.parse(at || '');
  if (!t || t < 0) return '';
  return day(t) === day(now) ? clock(t) : day(t) + ' ' + clock(t);
};

// Marked is text with the parts spans name marked.
export const Marked = ({text, spans}) => ss.marks(text, spans).map(([s, hit]) => (hit ? html`<mark class="hl">${s}</mark>` : s));

// TaskLabel is the task a run of the session worked for; with onOpen it opens that task.
export function TaskLabel({task, onOpen}) {
  const {f} = useWords();
  if (!onOpen) return html`<span class="sess-task ell">${taskText(task)}</span>`;
  return html`<button type="button" class="sess-task ell" title=${f('sess.openTask', task.id)} aria-label=${f('sess.openTask', task.id)}
    onClick=${e => { e.stopPropagation(); onOpen(task.id); }}>${taskText(task)}</button>`;
}

// Star marks a favorite.
export function Star() {
  const {t} = useWords();
  return html`<span class="sess-star" role="img" title=${t('sess.favorited')} aria-label=${t('sess.favorited')}><${Icon} name="star" size=${14} /></span>`;
}

export const tagChip = x => html`<span class="chip sess-tag">#${x}</span>`;
export const Archived = () => html`<span class="chip">${useWords().t('sess.archived')}</span>`;

// ⚠️ How far a message's folded text runs before it offers its full text (the node keeps up to 16 KiB of one).
const LONG = 600;

// Message is one turn's text, with the steps the agent took after it folded into one line that opens; at marks the
// one a message search opened on.
function Message({m, agent, onFull, at}) {
  const {t, f} = useWords();
  const [full, setFull] = useState(null);
  const [steps, setSteps] = useState(false);
  const user = m.Role === 'user';
  const tools = [...new Set((m.Steps || []).map(s => s.Tool).filter(Boolean))];
  const more = ss.cut(m) || (m.Chars || 0) > LONG;
  const open = () => (full !== null ? setFull(null) : onFull(m).then(setFull, () => {}));
  return html`<li class=${cx('sess-msg', user && 'sess-user', at && 'sv-at')} data-off=${m.Off}>
    <div class="sess-msg-head"><b>${user ? t('sess.you') : agent}</b><span class="mono t-muted">${m.At ? clock(m.At) : ''}</span>
      ${more && html`<${Button} kind="quiet" onClick=${open}>${full !== null ? t('sess.fold') : t('sess.full')}<//>`}</div>
    ${full !== null ? html`<div class="sess-text"><${Markdown} text=${full} /></div>`
      : html`<p class=${cx('sess-text', more && !at && 'sess-clamp')}><${Marked} text=${m.Text} spans=${m.spans} /></p>`}
    ${tools.length > 0 && html`<button type="button" class="sess-steps" aria-expanded=${steps ? 'true' : 'false'} onClick=${() => setSteps(!steps)}>
      ${f('sess.steps', m.Steps.length, tools.join(' · '))}</button>`}
    ${steps && html`<ul class="sess-step-list">${m.Steps.map((s, i) => html`<li key=${i}><span class="mono">${s.Tool}</span> <span class="t-muted">${s.Text}</span></li>`)}</ul>`}
  </li>`;
}

// makeWhy is why the row cannot become a task now, as words: the coordinator's reason, or the connection's.
export function makeWhy(w, row, online) {
  if (!online) return w.t('sess.makeOffline');
  const why = row.make?.why;
  return why === ss.cannot.busy ? w.t('sess.makeBusy') : why === ss.cannot.noAgent ? w.t('sess.makeNoAgent')
    : why === ss.cannot.old ? w.f('sess.makeOld', row.machine) : '';
}

// Acts are the head's switches on a row the viewer may change: favorite, done, archive, edit, and making a task; on a
// phone four big buttons without key caps, the task behind the page's ⋯.
function Acts({row, on, makeTask, why}) {
  const {t} = useWords();
  const phone = usePhone();
  const fav = ss.favorited(row), done = row.status === 'done', arch = !!row.archived_at;
  const star = html`<${Icon} name="star" fill=${fav} />`;
  if (phone) {
    return html`<div class="sv-phone-acts">
      <button type="button" class=${cx('btn', 'sv-act', fav && 'on')} aria-pressed=${fav ? 'true' : 'false'} onClick=${() => on('favorite')}>${star}${fav ? t('sess.a.faved') : t('sess.a.fav')}</button>
      <button type="button" class=${cx('btn', 'sv-act', done && 'on')} aria-pressed=${done ? 'true' : 'false'} onClick=${() => on('done')}><${Icon} name="check" />${done ? t('sess.a.doneOn') : t('sess.a.done')}</button>
      <button type="button" class=${cx('btn', 'sv-act', arch && 'on')} aria-pressed=${arch ? 'true' : 'false'} onClick=${() => on('archive')}><${Icon} name="archive" />${arch ? t('sess.a.archived') : t('sess.a.archive')}</button>
      <button type="button" class="btn sv-act" onClick=${() => on('edit')}><${Icon} name="edit" />${t('sess.a.edit')}</button>
    </div>`;
  }
  const act = (id, label, icon, key, pressed) => html`<button type="button" class=${cx('btn', 'quiet', 'sv-act', pressed && 'on')} aria-pressed=${pressed === undefined ? undefined : pressed ? 'true' : 'false'}
    onClick=${() => on(id)}>${icon}${label}<${KeyCap} k=${key} /></button>`;
  return html`<div class="sv-acts">
    ${act('favorite', fav ? t('sess.a.faved') : t('sess.a.fav'), star, 'f', fav)}
    ${act('done', done ? t('sess.a.doneOn') : t('sess.a.done'), html`<${Icon} name="check" />`, 'Shift+D', done)}
    ${act('archive', arch ? t('sess.a.archived') : t('sess.a.archive'), html`<${Icon} name="archive" />`, 'a', arch)}
    ${act('edit', t('sess.a.edit'), html`<${Icon} name="edit" />`, 'e')}
    ${makeTask && html`<${Button} kind="quiet" icon="runs" disabled=${!!why} onClick=${makeTask}>${t('sess.make')}<//>${why && html`<span class="sv-why t-muted">${why}</span>`}`}
  </div>`;
}

// Conversation is a session's messages, oldest at the top: the newest page when it opens, earlier pages on demand. at
// ({off, file}) opens it on a message search's best hit, find marking its words, with a way between the session's hits;
// readOnly is the line that stands in for the switches; on(id) runs one; more are what the head's ⋯ holds; owner
// names whose machine it is when it is not the viewer's; trashed ({at, days, restore}) says it is in its machine's trash,
// which neither changes nor resumes it.
export function Conversation({wire, row, os, copy, toasts, now, onTask, project, owner = '', readOnly = '', on, makeTask, makeWhy: why = '',
  more = [], trashed = null, at: best = null, find = ''}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  const [at, setAt] = useState(best);
  const [hits, setHits] = useState(null);
  const ref = {provider: row.provider, session_id: row.session_id};
  const empty = {msgs: [], from: -1, done: false, file: '', loading: true, error: null, latest: true, unseen: false, at: null};
  const [s, setS] = useState(empty);
  const [from, setFrom] = useState(best);
  const [rev, setRev] = useState(0);
  const box = useRef(null);
  const keep = useRef(null);
  const agent = agentOf(row.provider);
  // A hit already read is marked in place; another is read from a page that ends on it.
  useEffect(() => {
    if (at && s.msgs.some(m => m.Off === at.off) && (at.file || '') === (s.file || '')) {
      keep.current = 'at';
      setS(o => ({...o, at: at.off}));
    } else setFrom(at);
  }, [at?.off, at?.file]);
  useEffect(() => {
    let gone = false;
    setS(empty);
    const newest = unseen => ss.readPage(wire, row.machine, ref, {find}).then(p => {
      if (gone) return;
      keep.current = 'end';
      setS({...empty, msgs: ss.older([], p), from: p.From, done: !!p.Done, file: p.file || '', loading: false, unseen});
    });
    const fail = e => !gone && setS(o => ({...o, loading: false, error: e}));
    if (!from) newest(false).catch(fail);
    else {
      ss.readPage(wire, row.machine, ref, {before: from.off + 1, file: from.file || '', find}).then(p => {
        if (gone) return;
        keep.current = 'at';
        setS({...empty, msgs: ss.older([], p), from: p.From, done: !!p.Done, file: p.file || '', loading: false, latest: false, at: from.off});
      }, e => (e?.code === code.stale ? newest(true) : Promise.reject(e))).catch(fail);
    }
    return () => { gone = true; };
  }, [row.machine, row.session_id, rev, from?.off, from?.file, find]);
  useEffect(() => {
    if (!find || !best) return;
    let gone = false;
    ss.hits(wire, row, find).then(h => !gone && setHits({total: h?.total || 0, hits: [...(h?.hits || [])].sort((a, b) => a.off - b.off)}), () => {});
    return () => { gone = true; };
  }, [row.machine, row.session_id, find]);
  useLayoutEffect(() => {
    const el = box.current;
    if (!el || !keep.current) return;
    if (keep.current === 'at') el.querySelector?.('.sv-at')?.scrollIntoView?.({block: 'center'});
    else el.scrollTop = keep.current === 'end' ? el.scrollHeight : el.scrollHeight - keep.current;
    keep.current = null;
  }, [s.msgs, s.at]);
  const earlier = () => {
    setS(o => ({...o, loading: true}));
    ss.readPage(wire, row.machine, ref, {before: s.from, file: s.file, find}).then(p => {
      keep.current = box.current ? box.current.scrollHeight - box.current.scrollTop : null;
      setS(o => ({...o, msgs: ss.older(o.msgs, p), from: p.From, done: !!p.Done, loading: false}));
    }, e => {
      if (e?.code === code.stale) { toasts.show({text: t('sess.reread')}); setFrom(null); setRev(n => n + 1); return; }
      setS(o => ({...o, loading: false, error: e}));
    });
  };
  const full = m => ss.readText(wire, row.machine, ref, m.Off, s.file).catch(e => {
    toasts.show({text: apiText(w, e), tone: 'danger'});
    throw e;
  });
  const jump = h => h && setAt({off: h.off, file: h.file || ''});
  const list = hits?.hits || [];
  const i = list.findIndex(h => h.off === s.at);
  useActions('page', {hitPrev: {run: () => jump(list[i - 1]), when: () => i > 0}, hitNext: {run: () => jump(list[i + 1]), when: () => i >= 0 && i < list.length - 1}});
  const resume = !owner && ss.resumeLine(row, os);
  const facts = [agent, row.git_branch && f('sess.branch', row.git_branch), row.turns ? f('sess.turns', row.turns) : '', when(row.last_at, now)].filter(Boolean);
  const where = project ? f('sess.inProject', row.machine, project) : row.machine;
  const nav = find && list.length > 0 && html`<div class="sv-hitnav">
    <span>${f('sess.hitNav', i < 0 ? 0 : i + 1, hits.total || list.length)}</span>
    <${Button} kind="quiet" icon="up" label=${t('sess.hitPrev')} disabled=${i <= 0} onClick=${() => jump(list[i - 1])} />
    <${Button} kind="quiet" icon="down" label=${t('sess.hitNext')} disabled=${i < 0 || i >= list.length - 1} onClick=${() => jump(list[i + 1])} />
    <span class="t-muted ell">${f('sess.hitWords', ss.searchText(find))}</span></div>`;
  return html`<article class="sess-conv" aria-label=${row.title || row.session_id}>
    <header class="sess-conv-head">
      <div class="sess-conv-title">${row.live && html`<${Status} state=${liveState(row.live)} label=${liveWord(t, row.live)} />`}
        <h2 class="ell">${row.title || t('sess.untitled')}</h2>
        <${Button} kind="quiet" disabled=${s.loading} onClick=${() => { setFrom(at); setRev(n => n + 1); }}>${t('sess.refresh')}<//>
        ${!phone && !trashed && !readOnly && more.length > 0 && html`<${Menu} bare icon="more" label=${t('sess.more')} items=${more} />`}</div>
      ${nav}
      <div class="sess-conv-proj t-muted ell">${where}</div>
      <div class="sess-conv-facts t-muted"><span class="mono ell">${row.cwd}</span><span>${facts.join(' · ')}</span></div>
      ${trashed ? html`<p class="sv-trash"><${Icon} name="trash" size=${14} /><span class="msg">${trashed.days
        ? f('sess.inTrash', row.machine, when(trashed.at, now), ss.purgeIn(trashed.at, trashed.days, now)) : f('sess.inTrashKeep', row.machine, when(trashed.at, now))}</span>
        ${trashed.restore && html`<${Button} kind="quiet" keyName="Mod+Backspace" onClick=${trashed.restore}>${t('sess.restore')}<//>`}</p>`
        : readOnly ? html`<p class="sv-ro"><${Icon} name="lock" size=${14} />${readOnly}</p>`
        : html`<${Acts} row=${row} on=${on} makeTask=${phone ? null : makeTask} why=${why} />`}
      ${(row.favorited_at || row.tags?.length || row.archived_at) && html`<div class="sess-conv-meta">
        ${row.favorited_at && html`<${Star} /><span class="t-muted">${owner ? f('sess.favBy', owner) : f('sess.favoritedAt', day(Date.parse(row.favorited_at)))}</span>`}
        ${(row.tags || []).map(tagChip)}${row.archived_at && html`<${Archived} />`}</div>`}
      ${row.summary && html`<p class="sess-conv-sum">${row.summary}</p>`}
      ${row.task && html`<div class="sess-conv-task"><${TaskLabel} task=${row.task} onOpen=${onTask} /></div>`}
      ${!owner && !trashed && (resume ? html`<${Secret} label=${f('sess.resume', row.machine)} value=${resume} copy=${copy} />` : html`<p class="t-muted">${t('sess.noResume')}</p>`)}
    </header>
    <div class="sess-scroll" ref=${box}>
      ${s.unseen && html`<p class="sv-stale">${t('sess.unseen')}</p>`}
      ${s.error && html`<p class="sess-note t-failed">${s.error.code === code.notFound ? t('sess.gone') : apiText(w, s.error)}</p>`}
      ${!s.error && (s.done ? s.msgs.length > 0 && html`<p class="sess-edge t-muted">${t('sess.start')}</p>`
        : s.msgs.length > 0 && html`<div class="sess-edge"><${Button} disabled=${s.loading} onClick=${earlier}>${t('sess.earlier')}<//></div>`)}
      ${s.loading && !s.msgs.length && html`<p class="empty">${t('sess.loading')}</p>`}
      ${!s.loading && !s.error && !s.msgs.length && html`<p class="empty">${t('sess.empty')}</p>`}
      <ol class="sess-msgs">${s.msgs.map(m => html`<${Message} key=${m.Off} m=${m} agent=${agent} onFull=${full} at=${m.Off === s.at} />`)}</ol>
      ${!s.latest && !s.loading && html`<p class="sv-later"><span class="t-muted">${t('sess.later')}</span>
        <${Button} kind="quiet" onClick=${() => { setFrom(null); setRev(n => n + 1); }}>${t('sess.toLatest')}<//></p>`}
    </div>
  </article>`;
}

// TagInput takes tags as chips: a space, a comma or Enter ends one, Backspace on nothing takes the last back; common
// are the machine's tags to add with a click.
function TagInput({tags, onChange, common}) {
  const {t, f} = useWords();
  const [text, setText] = useState('');
  const add = s => {
    const got = ss.normalize([...tags, ...s.split(/[\s,，]+/)]);
    if (got.length !== tags.length) onChange(got);
    setText('');
  };
  const onInput = e => {
    const v = e.currentTarget.value;
    if (/[\s,，]$/.test(v)) add(v);
    else setText(v);
  };
  const onKeyDown = e => {
    if (e.isComposing) return;
    if (e.key === 'Enter' && text.trim()) { e.preventDefault(); add(text); } else if (e.key === 'Backspace' && !text && tags.length) onChange(tags.slice(0, -1));
  };
  const more = common.filter(x => !tags.includes(x));
  return html`<div class="field">
    <label>${t('sess.edit.tags')}</label>
    <div class="in sv-tagin">${tags.map(x => html`<button type="button" class="chip" key=${x} title=${f('sess.edit.dropTag', x)} onClick=${() => onChange(tags.filter(y => y !== x))}>#${x} ×</button>`)}
      <input value=${text} placeholder=${t('sess.edit.tagHint')} aria-label=${t('sess.edit.tags')} onInput=${onInput} onKeyDown=${onKeyDown}
        onBlur=${() => text.trim() && add(text)} /></div>
    ${more.length > 0 && html`<div class="sv-tagsugg"><span class="field-note">${t('sess.edit.common')}</span>
      ${more.map(x => html`<button type="button" class="chip" key=${x} onClick=${() => onChange([...tags, x])}>#${x}</button>`)}</div>`}
  </div>`;
}

const fieldsOf = r => ({title: r.title || '', tags: [...(r.tags || [])], summary: r.summary || ''});

// EditSession edits a row's title, tags and summary; onSave(patch, expect) sends what changed and fails with stale when
// the record changed since it was read, in which case base is the record now and the dialog shows it.
export function EditSession({row, common, stale, onSave, onClose}) {
  const {t, f} = useWords();
  const [v, setV] = useState(() => fieldsOf(row));
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (stale) setV(fieldsOf(row)); }, [stale, row.updated_at]);
  const was = fieldsOf(row);
  const patch = {};
  if (v.title.trim() !== was.title) patch.title = v.title.trim();
  if (v.tags.join(' ') !== was.tags.join(' ')) patch.tags = v.tags;
  if (v.summary.trim() !== was.summary) patch.summary = v.summary.trim();
  const empty = !v.title.trim();
  const save = () => {
    if (empty || busy) return;
    if (!Object.keys(patch).length) { onClose(); return; }
    setBusy(true);
    onSave(patch, row.updated_at || '').finally(() => setBusy(false));
  };
  return html`<${Modal} title=${t('sess.edit.title')} onClose=${onClose} full actions=${[
    {label: t('home.cancel'), onClick: onClose},
    {label: t('sess.edit.save'), kind: 'primary', keyName: 'Mod+Enter', onClick: save, disabled: empty || busy},
  ]}>
    <div class="form">
      ${stale && html`<p class="sv-stale" role="alert">${t('sess.edit.stale')}</p>`}
      <${TextInput} label=${t('sess.edit.name')} value=${v.title} onInput=${x => setV(o => ({...o, title: x}))} error=${empty ? t('sess.edit.empty') : ''} />
      <${TagInput} tags=${v.tags} common=${common} onChange=${x => setV(o => ({...o, tags: x}))} />
      <${TextArea} label=${t('sess.edit.summary')} value=${v.summary} rows=${4} onInput=${x => setV(o => ({...o, summary: x}))} />
      <p class="field-note">${f('sess.edit.note', row.machine)}${!ss.favorited(row) && ' ' + t('sess.edit.unfav')}</p>
    </div>
  <//>`;
}

// MakeTask makes a task go on with row in the background on its machine: what to do, the profile (the agents the
// coordinator found able to) and the project (projects, those the viewer may make tasks in; none: private). why says why
// it cannot now; onMake({text, agent, project}) sends it.
export function MakeTask({row, projects, why, onMake, onClose}) {
  const {t, f} = useWords();
  const agents = row.make?.agents || [];
  const [v, setV] = useState({text: '', agent: agents[0] || '', project: ''});
  const [busy, setBusy] = useState(false);
  const ready = !why && !!v.text.trim() && !!v.agent && !busy;
  const make = () => {
    if (!ready) return;
    setBusy(true);
    onMake({text: v.text.trim(), agent: v.agent, project: v.project}).finally(() => setBusy(false));
  };
  return html`<${Modal} title=${t('sess.makeTitle')} onClose=${onClose} actions=${[
    {label: t('home.cancel'), onClick: onClose},
    {label: t('sess.make'), kind: 'primary', keyName: 'Mod+Enter', onClick: make, disabled: !ready},
  ]}>
    <div class="form">
      <p class="t-muted">${f('sess.makeSub', row.title || t('sess.untitled'), row.machine, agentOf(row.provider))}</p>
      <${TextArea} label=${t('sess.makeText')} value=${v.text} rows=${4} onInput=${x => setV(o => ({...o, text: x}))} />
      <${Picker} label=${t('sess.makeAgent')} value=${v.agent} onChange=${x => setV(o => ({...o, agent: x}))}
        options=${agents.map(a => ({value: a, label: a, sub: t('sess.makeAgentSub')}))} />
      <${Picker} label=${t('sess.makeProject')} value=${v.project} onChange=${x => setV(o => ({...o, project: x}))}
        options=${[{value: '', label: t('sess.private'), sub: t('sess.privateSub')}, ...projects.map(p => ({value: p.id, label: p.name, sub: p.id}))]} />
      ${why ? html`<p class="sv-why t-failed" role="alert">${why}</p>` : html`<p class="field-note">${f('sess.makeNote', row.machine)}</p>`}
    </div>
  <//>`;
}

// DeleteSession confirms moving row's session into its machine's trash, which purges it after days (0: never); Enter
// deletes, Esc keeps it.
export function DeleteSession({row, days, onDelete, onClose}) {
  const {t, f} = useWords();
  return html`<${Modal} title=${t('sess.delTitle')} onClose=${onClose} actions=${[
    {label: t('home.cancel'), keyName: 'Esc', onClick: onClose},
    {label: t('sess.delBtn'), kind: 'primary danger-fill', keyName: 'Enter', first: true, onClick: onDelete},
  ]}>
    <div class="sv-del"><p>${f('sess.delBody', row.title || t('sess.untitled'), row.machine)}</p>
      <p class="t-muted">${days ? f('sess.delNote', days) : t('sess.delNoteKeep')}</p></div>
  <//>`;
}
