// sessions is one machine's own Claude / Codex sessions (?page=machines&sessions=<machine>), read through node.call by
// its owner and admins: every session its node shares with who of them is running, a text filter, and one of them open
// (&session=<provider>:<id>), its conversation paged from the newest with the earlier on demand and the line that
// resumes it on that machine. Read only; nothing is polled: the list and a conversation are read when they open and
// again on Refresh. On a desktop the list and the open conversation sit side by side; on a phone the list is cards and
// a conversation takes the screen.
import {useState, useEffect, useLayoutEffect, useRef} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useSignalValue} from '../ui/base.js';
import {Button} from '../ui/controls.js';
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
import {apiText} from './words.js';

register('sessions', {
  'sess.title': ['%s 的会话', 'Sessions on %s'], 'sess.back': ['机器', 'Machines'], 'sess.refresh': ['刷新', 'Refresh'],
  'sess.filter': ['筛选会话', 'Filter the sessions'], 'sess.filterHint': ['标题、目录、agent、分支', 'Title, directory, agent, branch'],
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
  'sess.cannot': ['只有机器的主人和管理员能看它的会话', 'Only a machine\'s owner and admins read its sessions'],
  'sess.resume': ['在 %s 上恢复', 'Resume on %s'], 'sess.noResume': ['tend 不会恢复这种 agent 的会话', 'tend cannot resume this agent\'s sessions'],
  'sess.earlier': ['加载更早的', 'Load earlier'], 'sess.start': ['会话从这里开始', 'The session starts here'],
  'sess.empty': ['这个会话还没有对话', 'This session has no conversation yet'], 'sess.reread': ['会话文件换了，已从最新重读', 'The session\'s file changed: read again from the newest'],
  'sess.you': ['你', 'You'], 'sess.full': ['全文', 'Full text'], 'sess.fold': ['收起', 'Fold'],
  'sess.steps': ['%d 步：%s', '%d {step|steps}: %s'], 'sess.turns': ['%d 轮', '%d {turn|turns}'], 'sess.branch': ['分支 %s', 'branch %s'],
  'sess.gone': ['没有这个会话，或者节点不让读它', 'No such session, or its node does not let it be read'],
});

const when = (at, now) => {
  const t = Date.parse(at || '');
  if (!t || t < 0) return '';
  return day(t) === day(now) ? clock(t) : day(t) + ' ' + clock(t);
};
const why = (w, machine, e) => (['offline', 'unauthorized', 'unknown_method', 'timeout'].includes(e?.code) ? w.f('sess.why.' + e.code, machine) : apiText(w, e));
const liveState = l => (!l ? '' : l.Status === 'blocked' ? 'waiting' : 'running');
const liveWord = (t, l) => (l?.Status === 'blocked' ? t('sess.blocked') : t('sess.running'));
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
function Conversation({wire, machine, row, os, copy, toasts, now}) {
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
  return html`<article class="sess-conv" aria-label=${row.title || row.session_id}>
    <header class="sess-conv-head">
      <div class="sess-conv-title">${row.live && html`<${Status} state=${liveState(row.live)} label=${liveWord(t, row.live)} />`}
        <h2 class="ell">${row.title || t('sess.untitled')}</h2>
        <${Button} kind="quiet" disabled=${s.loading} onClick=${() => setRev(n => n + 1)}>${t('sess.refresh')}<//></div>
      <div class="sess-conv-facts t-muted"><span class="mono ell">${row.cwd}</span><span>${facts.join(' · ')}</span></div>
      ${resume ? html`<${Secret} label=${f('sess.resume', machine)} value=${resume} copy=${copy} />` : html`<p class="t-muted">${t('sess.noResume')}</p>`}
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
// (its owner or an admin); copy is the clipboard's (a test passes its own).
export function Sessions({store, wire, router, toasts, machine, open = '', may = true, copy, clock: now = () => Date.now()}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  const machines = useSignalValue(store.machines);
  const m = machines.find(x => x.name === machine);
  const [rows, setRows] = useState(null);
  const [error, setError] = useState(null);
  const [q, setQ] = useState('');
  const [rev, setRev] = useState(0);
  const at = now();
  useEffect(() => {
    if (!may) return;
    let gone = false;
    setRows(null);
    setError(null);
    ss.readList(wire, machine).then(r => !gone && setRows(r), e => !gone && setError(e));
    return () => { gone = true; };
  }, [machine, rev, may]);
  const shown = rows ? ss.matches(rows, q) : [];
  const row = rows?.find(r => r.key === open);
  const go = (key, replace) => router.go({page: 'machines', sessions: machine, ...(key ? {session: key} : {})}, {replace});
  const back = () => router.go({page: 'machines'});
  const share = m?.share_sessions === 'runs' || m?.share_sessions === 'none';
  const running = (rows || []).filter(r => r.live).length;

  const columns = [
    {id: 'state', label: t('sess.c.state'), width: '28px', render: r => (r.live ? html`<${Status} state=${liveState(r.live)} />` : ''), mobile: 'lead'},
    {id: 'title', label: t('sess.c.title'), width: 'minmax(0, 2fr)', render: r => html`<span class="ell">${r.title || t('sess.untitled')}</span>`, mobile: 'primary'},
    {id: 'dir', label: t('sess.c.dir'), width: 'minmax(0, 1.4fr)', render: r => html`<span class="mono ell">${r.cwd}</span>`, mobile: 'secondary'},
    {id: 'agent', label: t('sess.c.agent'), width: '64px', render: r => html`<span class="mono">${r.provider}</span>`, mobile: 'secondary'},
    {id: 'last', label: t('sess.c.last'), width: '88px', align: 'right', render: r => html`<span class="mono t-muted">${when(r.last_at, at)}</span>`, mobile: 'trailing',
      sort: (a, b) => (Date.parse(a.last_at || '') || 0) - (Date.parse(b.last_at || '') || 0)},
  ];
  const head = html`<div class=${cx('sess-head', phone && 'sess-head-phone')}>
    <span class="sess-head-row">
      <${Button} kind="quiet" icon="back" onClick=${back}>${t('sess.back')}<//>
      <h1 class="tasks-title ell">${f('sess.title', machine)}</h1>
      ${rows && html`<span class="t-muted">${f('sess.summary', rows.length, running)}${q && rows.length !== shown.length ? ' · ' + f('sess.shown', shown.length) : ''}</span>`}
      <span class="sess-head-acts"><${Button} disabled=${may && !rows && !error} onClick=${() => setRev(n => n + 1)}>${t('sess.refresh')}<//></span>
    </span>
    ${may && html`<${TextInput} label=${t('sess.filter')} value=${q} onInput=${setQ} placeholder=${t('sess.filterHint')} />`}
    ${share && html`<p class="box sess-share">${f(m.share_sessions === 'none' ? 'sess.shareNone' : 'sess.shareRuns', machine)}</p>`}
  </div>`;
  const body = !may ? html`<p class="empty">${t('sess.cannot')}</p>`
    : error ? html`<p class="sess-note t-failed">${why(w, machine, error)}</p>`
    : !rows ? html`<p class="empty">${t('sess.loading')}</p>`
    : html`<${Table} label=${f('sess.title', machine)} columns=${columns} rows=${shown} rowKey=${r => r.key} selected=${row?.key || ''}
        onSelect=${phone ? null : k => go(k, !!open)} onOpen=${k => go(k, !phone && !!open)} active=${!(phone && row)}
        empty=${rows.length ? t('sess.noMatch') : t('sess.none')} />`;
  const conv = row && html`<${Conversation} key=${row.key} wire=${wire} machine=${machine} row=${row} os=${m?.os || ''} copy=${copy} toasts=${toasts} now=${at} />`;

  if (phone) {
    return html`<div class="sess sess-phone">${head}${body}
      ${open && rows && html`<${Drawer} title=${row?.title || t('sess.untitled')} onClose=${() => router.back({page: 'machines', sessions: machine})}>
        ${conv || html`<p class="empty">${t('sess.gone')}</p>`}<//>`}
    </div>`;
  }
  return html`<div class="sess">
    ${head}
    <div class="sess-body">
      <div class="sess-list">${body}</div>
      <div class="sess-pane">${conv || html`<p class="empty">${open && rows ? t('sess.gone') : t('sess.pick')}</p>`}</div>
    </div>
  </div>`;
}
