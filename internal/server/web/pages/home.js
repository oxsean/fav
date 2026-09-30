// Home is where the day is seen at a glance: what waits on the viewer (to answer, to accept, what failed), what runs
// and waits for a machine, what ended, each machine's day and the last seven days. On a phone it keeps what waits, each
// item a card row with one quick action, and a short list of what runs; a row opens the item's own page, where it is
// answered and the next one follows. The figures stay on the desktop.
import {useState, useEffect, useRef} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useSignalValue, useActions} from '../ui/base.js';
import {Panel, Stat} from '../ui/panel.js';
import {Button, Chip, Chips} from '../ui/controls.js';
import {Status} from '../ui/status.js';
import {Icon} from '../ui/icons.js';
import {ExpandItem} from '../ui/expand.js';
import {Modal} from '../ui/overlay.js';
import {Spark, Bars, Timeline, Meter} from '../ui/charts.js';
import {TextInput, TextArea} from '../ui/input.js';
import {useListKeys} from '../ui/table.js';
import {AnswerForm, quickOf, isPermission, allowsRun} from '../ui/answer.js';
import * as sel from '../core/select.js';
import {tokens, money, duration, clock as hhmm, usageTokens} from '../core/format.js';
import {unsure} from '../core/commands.js';
import {code} from '../core/proto.js';
import {noInbox} from '../core/store.js';
import {why} from './words.js';

// ⚠️ An item's waiting bar is full after two hours.
const waitFull = 2 * 3600e3;
const stepsShown = 3;

const glyphOf = {shell: '$', read: '+', search: '+', edit: '~'};
const opGlyph = {add: '+', delete: '−', rename: '→'};

// stateOf is how a waiting item is drawn: its reason when it asks, done when it is to be accepted, failed (or unknown).
const stateOf = (group, reason) => (group === 'answer' ? reason : group === 'accept' ? 'done' : group === 'error'
  ? (reason === 'unknown' ? 'unknown' : 'failed') : 'waiting');

// endWords is how a run ended, for the lists.
function endWords(w, r) {
  if (r.state === 'exited') return r.checked && r.checked.exit !== 0 ? w.t('home.end.checkFailed') : w.t('home.end.exited');
  if (r.state === 'failed') return r.exit_code ? w.f('home.end.exit', r.exit_code) : r.detail || w.t('home.end.failed');
  return w.t('home.end.' + r.state);
}

const who = r => (r ? `${r.agent} @ ${r.machine}` : '');

// ⚠️ Where the phone keeps which of the viewer's roles the list shows.
const asKey = 'tend-home-as';
const asRoles = ['owner', 'approver', 'dispatcher'];
const readAs = storage => { try { const v = storage?.getItem(asKey) || ''; return asRoles.includes(v) ? v : ''; } catch { return ''; } };
const keepAs = (storage, v) => { try { v ? storage?.setItem(asKey, v) : storage?.removeItem(asKey); } catch {} };

// Home: clock gives now in ms; fetchOutput(run) the last events of a run (run.output.page); changes is core/changes.js's
// (a phone shows what a run to accept changed); onOpen(task) goes to it. wait is the task whose item has the page to
// itself (the route's); onWait(task, {replace}) goes to one ('' to the list), onBack() leaves it; storage keeps the
// roles shown.
export function Home({store, commands, toasts, clock = () => Date.now(), fetchOutput, changes, onOpen, onNavigate, wait = '', onWait = () => {},
  onBack = () => onWait(''), storage = null}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  useSignalValue(store.rev.runs);
  useSignalValue(store.rev.tasks);
  const machines = useSignalValue(store.machines);
  const inbox = useSignalValue(store.inbox);
  const hidden = useSignalValue(commands.hidden);
  const aff = useSignalValue(store.affordances);
  useSignalValue(commands.pending);
  const [as, setAsNow] = useState(() => (phone ? readAs(storage) : ''));
  const setAs = v => { setAsNow(v); if (phone) keepAs(storage, v); };
  const [selected, setSelected] = useState('');
  const [open, setOpen] = useState('');
  const [gone, setGone] = useState({});
  const [confirm, setConfirm] = useState(null);
  const [confirmNote, setConfirmNote] = useState('');
  useEffect(() => { setConfirmNote(''); }, [confirm]);
  const [rework, setRework] = useState(null);
  const st = store.state, now = clock();

  const shown = inbox.filter(x => !hidden.has(x.task));
  const viewOf = x => {
    const run = x.run ? st.runs[x.run] : null;
    return {x, task: st.tasks[x.task], run, group: sel.waitGroup(x.reason), req: run?.requests?.[0]};
  };
  const list = sel.waits(shown, as).map(viewOf);
  const ids = list.map(v => v.x.task);
  const current = list.find(v => v.x.task === selected) || null;

  const failed = e => e.code !== code.requestGone && toasts.show({text: unsure.includes(e.code)
    ? f('app.unsure', e.code) : f('app.failed', e.code || String(e.message || e)), tone: 'danger'});
  const send = (method, params, opts) => commands.send(method, params, opts).catch(e => { failed(e); throw e; });

  const markDone = v => {
    const {task} = v, id = commands.newID();
    send('task.set_status', {id: task.id, status: 'done'}, {key: 'task:' + task.id, hide: task.id, until: store.inbox, id}).then(() => {
      toasts.show({text: f('home.doneToast', task.title), undo: () => {
        commands.show(task.id);
        send('task.undo', {id: task.id, command: id}, {key: 'task:' + task.id}).catch(() => {});
      }});
    }, () => {});
  };
  const answer = (v, params) => send('run.answer', {run: v.run.id, request: v.req.id, ...params}, {key: 'run:' + v.run.id, hide: v.task.id, until: store.inbox})
    .then(() => toasts.show({text: t('home.answered')}), e => {
      if (e.code !== code.requestGone) return;
      setGone(g => ({...g, [v.run.id + '\n' + v.req.id]: e.detail || ''}));
      setOpen(v.task.id);
    });
  const reply = (v, text) => send('run.continue', {run: v.run.id, text}, {key: 'run:' + v.run.id, hide: v.task.id, until: store.inbox})
    .then(() => toasts.show({text: t('home.answered')}), () => {});
  const askRetry = v => setConfirm({title: t('home.confirmRetry'), note: f('home.confirmRetryNote', v.task.title, v.run?.agent || v.task.agent || '', v.run?.machine || v.task.machine || ''),
    label: t('home.retry'), noteLabel: t('home.retryNote'),
    go: note => send('run.dispatch', {task: v.task.id, ...(v.run ? {machine: v.run.machine, agent: v.run.agent} : {}), ...(note.trim() ? {note: note.trim()} : {})},
      {key: 'task:' + v.task.id}).catch(() => {})});
  // askRework opens a failed task's brief to edit on a phone; goRework saves it (task.edit, when it changed) and only
  // then dispatches the task again as askRetry would.
  const askRework = v => {
    setRework({v, text: null});
    store.brief(v.task.id).then(b => setRework(x => (x?.v.task.id === v.task.id && x.text === null ? {...x, text: b} : x)), e => { setRework(null); failed(e); });
  };
  const goRework = async () => {
    const {v, text} = rework, key = 'task:' + v.task.id;
    try {
      if (text !== store.briefOf(v.task.id)) await send('task.edit', {id: v.task.id, brief: text}, {key});
    } catch { return; }
    setRework(null);
    send('run.dispatch', {task: v.task.id, ...(v.run ? {machine: v.run.machine, agent: v.run.agent} : {})}, {key})
      .then(() => toasts.show({text: f('home.reworked', v.task.title)}), () => {});
  };
  const askStop = (run, task) => setConfirm({title: t('home.confirmStop'), note: f('home.confirmStopNote', task?.title || run.task, run.machine),
    label: t('home.stop'), go: () => send('run.stop', {id: run.id}, {key: 'run:' + run.id}).catch(() => {})});

  const canDone = v => !!v && v.group === 'accept' && v.x.reason !== 'draft' && !!v.task && !v.task.flow;
  const canRetry = v => !!v && v.group === 'error' && !!v.task;
  const openRun = v => !!v?.run && sel.openStates.includes(v.run.state);
  const busy = v => !!v && (commands.state('task:' + v.task?.id) === 'pending' || commands.state('run:' + v.run?.id) === 'pending');

  // choices are what 1–9 does on an item: allow and deny (and allow for the run, when offered), or the options of its
  // one single-choice question.
  const choices = v => {
    if (!v || v.group !== 'answer' || !v.req || !v.run || gone[v.run.id + '\n' + v.req.id] !== undefined) return [];
    return quickOf(t, v.req, {scope: allowsRun(v.req, aff.runs?.[v.run.id])}).map(q => ({label: q.label, kind: q.kind, go: () => answer(v, q.params)}));
  };

  useEffect(() => {
    if (phone || !wait) return;
    setSelected(wait);
    setOpen(wait);
    onWait('', {replace: true});
  }, [phone, wait]);

  useListKeys({ids, selected, onSelect: setSelected, onOpen: id => onOpen(id), onToggle: id => setOpen(open === id ? '' : id),
    onPick: (n, id) => { const v = list.find(x => x.x.task === id); const c = choices(v)[n - 1]; if (c && !busy(v)) c.go(); },
    pickLabel: 'home.answer', canPick: id => choices(list.find(x => x.x.task === id)).length > 0, active: !confirm});
  useActions('page', {
    done: {when: () => canDone(current) && !busy(current), run: () => markDone(current)},
    dispatch: {when: () => canRetry(current) && !busy(current), run: () => askRetry(current), label: 'home.retry'},
    stop: {when: () => openRun(current), run: () => askStop(current.run, current.task)},
  }, {active: !confirm});

  const quick = v => {
    const c = choices(v);
    const opening = {label: t('home.open'), kind: 'quiet', onClick: () => onOpen(v.x.task)};
    if (c.length) return [...c.slice(0, 3).map((x, i) => ({label: x.label, kind: x.kind, keyName: String(i + 1), onClick: x.go})),
      ...(c.length > 3 || v.x.reason === 'asked' ? [{label: t('home.other'), kind: 'quiet', onClick: () => setOpen(v.x.task)}] : [])];
    if (v.group === 'answer') return [{label: t('home.answer'), kind: 'primary', onClick: () => setOpen(v.x.task)}];
    if (canDone(v)) return [{label: t('home.done'), kind: 'primary', keyName: 'Shift+D', onClick: () => markDone(v)}, opening];
    if (canRetry(v)) return [{label: t('home.retry'), keyName: 'd', onClick: () => askRetry(v)}, opening];
    return [opening];
  };

  const title = v => {
    const reason = why(w, v.x.reason);
    if (v.group === 'answer') {
      const q = v.req?.questions?.[0]?.question;
      if (q) return q;
      if (v.req?.tool) return f('home.perm', v.req.tool, v.req.summary || '');
      return v.run?.ask || reason;
    }
    const more = v.group === 'error' ? v.run?.detail : v.group === 'accept' ? v.run?.last : '';
    return more ? f('home.whyDetail', reason, more) : reason;
  };
  const sub = v => [v.task?.title || v.x.title, v.x.task, who(v.run), ...(v.x.as || []).map(a => t('home.as.' + a))].filter(Boolean).join(' · ');

  // quickPhone is the one action a closed item has on a phone: allow a permission, mark done, retry, or open the task;
  // a question is opened to be answered.
  const quickPhone = v => {
    if (v.group === 'answer') return isPermission(v.req) ? choices(v).slice(0, 1).map(c => ({label: c.label, onClick: c.go})) : [];
    if (canDone(v)) return [{label: t('home.done'), onClick: () => markDone(v)}];
    if (canRetry(v)) return [{label: t('home.retry'), onClick: () => askRetry(v)}];
    return [{label: t('home.openShort'), onClick: () => onOpen(v.x.task)}];
  };
  const body = v => html`<${WaitBody} v=${v} busy=${busy(v)} scope=${allowsRun(v.req, aff.runs?.[v.run?.id])} gone=${v.req && gone[v.run?.id + '\n' + v.req.id]}
    fetchOutput=${fetchOutput} changes=${changes} onOpen=${onOpen} onClose=${() => setOpen('')}
    onDone=${canDone(v) ? () => markDone(v) : null} onRetry=${canRetry(v) ? () => askRetry(v) : null} onRework=${phone && canRetry(v) ? () => askRework(v) : null}
    onAnswer=${p => answer(v, p)} onReply=${text => reply(v, text)} />`;
  const item = v => html`<${ExpandItem} key=${v.x.task} id=${v.x.task} open=${!phone && open === v.x.task} selected=${selected === v.x.task} page=${phone}
      onToggle=${phone ? () => onWait(v.x.task) : () => { setSelected(v.x.task); setOpen(open === v.x.task ? '' : v.x.task); }}
      state=${stateOf(v.group, v.x.reason)} title=${phone ? v.task?.title || v.x.title || v.x.task : title(v)} sub=${phone ? title(v) : sub(v)}
      age=${duration(now - Date.parse(v.x.since))} agePct=${(now - Date.parse(v.x.since)) / waitFull * 100}
      actions=${busy(v) ? [] : phone ? quickPhone(v) : quick(v)}>
      ${!phone && open === v.x.task && body(v)}
    <//>`;
  const roles = [['', 'home.all'], ['owner', 'home.asOwner'], ['approver', 'home.asApprover'], ...(phone ? [['dispatcher', 'home.asDispatcher']] : [])];
  const chips = html`<${Chips} label=${t('home.waiting')}>
      ${roles.map(([k, label]) => html`<${Chip} label=${t(label)} on=${as === k}
        count=${k ? shown.filter(x => (x.as || []).includes(k)).length : phone ? shown.length : undefined} onClick=${() => setAs(k)} />`)}
    <//>`;

  const waiting = html`<${Panel} title=${t('home.waiting')} count=${shown.length} actions=${chips}>
    ${list.length ? list.map(item) : html`<p class="empty">${t('home.none')}</p>`}
  <//>`;

  const going = sel.openRuns(st);
  const c = sel.counts(st, machines, shown);
  const room = sel.slots(machines);
  const runsPanel = html`<${Panel} title=${t('home.going')} count=${going.length}
    actions=${html`<${Button} kind="quiet" keyName="g r" onClick=${() => onNavigate('runs')}>${t('home.allRuns')}<//>`}>
    ${going.length ? html`<div class="rows">${going.map(({run, task, why: waitsFor}) => {
      const m = machines.find(x => x.name === run.machine);
      const doing = run.state === 'queued'
        ? (waitsFor === 'slot' && m ? f('home.slotBusy', m.active || 0, m.slots || 0) : waitsFor ? why(w, waitsFor) : '')
        : sel.doing(run);
      const since = Date.parse(run.started_at || run.queued_at);
      return html`<div class="run-row" key=${run.id}>
        <${Status} state=${run.state} />
        <button type="button" class="run-main" onClick=${() => onOpen(run.task)}>
          <span class="run-title ell">${task?.title || run.task}</span><span class="run-who mono">${who(run)}</span>
        </button>
        <span class=${cx('run-doing', 'ell', run.state === 'queued' && 't-muted', /^\$ /.test(doing) && 'mono')}>${doing}</span>
        <span class="run-time mono">${duration(now - since)}</span>
        <span class="run-cost mono">${run.usage?.cost_usd ? money(run.usage.cost_usd) : run.usage ? tokens(usageTokens(run.usage)) : '—'}</span>
        ${run.want === 'stop' ? html`<span class="t-muted run-stop">${t('home.stopping')}</span>`
          : html`<${Button} kind="quiet" icon="stop" label=${t('home.stop')} onClick=${() => askStop(run, task)} />`}
      </div>`;
    })}</div>` : html`<p class="empty">${t('home.nothingRuns')}</p>`}
  <//>`;

  const confirmDialog = confirm && html`<${Modal} title=${confirm.title} onClose=${() => setConfirm(null)}
    actions=${[{label: t('home.cancel'), onClick: () => setConfirm(null)}, {label: confirm.label, kind: 'primary',
      keyName: 'Mod+Enter', onClick: () => { setConfirm(null); confirm.go(confirmNote); }}]}><p>${confirm.note}</p>
    ${confirm.noteLabel && html`<${TextArea} label=${confirm.noteLabel} value=${confirmNote} onInput=${setConfirmNote} rows=${3} note=${t('home.retryNoteHint')} />`}<//>`;
  const reworkDialog = rework && html`<${Modal} full title=${f('home.reworkTitle', rework.v.task.title)} onClose=${() => setRework(null)}
    actions=${[{label: t('home.cancel'), onClick: () => setRework(null)}, {label: t('home.reworkGo'), kind: 'primary', keyName: 'Mod+Enter',
      disabled: rework.text === null || !rework.text.trim() || busy(rework.v), onClick: goRework}]}>
    <${TextArea} label=${t('home.reworkBrief')} value=${rework.text ?? ''} onInput=${text => setRework(x => ({...x, text}))} rows=${14}
      note=${rework.text === null ? t('home.reworkLoading') : t('home.reworkNote')} />
  <//>`;
  const dialog = confirmDialog || reworkDialog;

  if (phone && wait) {
    const pool = list.some(v => v.x.task === wait) ? list : sel.waits(shown, '').map(viewOf);
    return html`<${WaitPage} wait=${wait} list=${pool} loaded=${inbox !== noInbox} title=${title} body=${body} toasts=${toasts}
      onWait=${onWait} onBack=${onBack} onOpen=${onOpen} dialog=${dialog} />`;
  }

  if (phone) {
    const running = going.filter(g => g.run.state !== 'queued');
    return html`<div class="home">
      <div class="home-head"><h1>${t('home.waiting')}</h1><span class="mono t-muted">${shown.length}</span><span class="lbl home-hint">${t('home.hint')}</span></div>
      ${chips}
      <div class="home-waits">${list.length ? list.map(item) : html`<p class="empty">${t('home.none')}</p>`}</div>
      <section class="home-going">
        <button type="button" class="home-going-head" onClick=${() => onNavigate('runs')}>${f('home.running', c.running, c.queued)} ›</button>
        <div class="home-going-list">${running.length ? running.map(({run, task}) => html`<button type="button" class="going-row" key=${run.id} onClick=${() => onOpen(run.task)}>
          <${Status} state=${run.state} /><span class="going-title ell">${task?.title || run.task}</span>
          <span class="mono t-muted">${duration(now - Date.parse(run.started_at || run.queued_at))}</span>
        </button>`) : html`<p class="empty">${t('home.nothingRuns')}</p>`}</div>
      </section>
      ${dialog}
    </div>`;
  }

  const day = sel.today(st, now);
  const wk = sel.week(st, now);
  const sum = sel.waitSummary(shown, now);
  const day7 = sel.lanes(st, machines, now);
  const lang = w.lang.value === 'zh' ? 'zh-CN' : 'en-GB';
  const endParts = (ends, keys = ['exited', 'failed', 'stopped', 'canceled', 'abandoned']) => keys.filter(k => ends[k]).map(k => html`<${Status} state=${k} label=${String(ends[k])} />`);
  const recent = sel.recent(st, now);

  return html`<div class="home">
    <h1 class="sr-only">${t('home.title')}</h1>
    <div class="home-stats">
      <${Stat} label=${t('home.waiting')} value=${String(sum.count)} tone="unknown" note=${sum.count ? f('home.longest', duration(sum.longest)) : ''}>
        <span class="stat-parts">${sel.waitGroups.filter(g => sum.by[g]).map(g => html`<${Status} state=${stateOf(g, g === 'answer' ? 'asked' : g)} label=${f('home.g.n', t('home.g.' + g), sum.by[g])} />`)}</span>
      <//>
      <${Stat} label=${t('home.going')} value=${`${c.running} / ${c.queued}`} tone="running" note=${f('home.peak', day.peak)} onClick=${() => onNavigate('runs')}>
        <span class="stat-row"><span class="kn">${f('home.room', room.active, room.slots)}</span><${Spark} values=${day.hourly} step label=${f('home.peak', day.peak)} tone="running" /></span>
      <//>
      <${Stat} label=${t('home.ended')} value=${String(day.ended)} note=${day.ended ? f('home.mean', duration(day.mean)) : ''}>
        <span class="stat-parts">${endParts(day.ends)}</span>
      <//>
      <${Stat} label=${t('home.spent')} value=${day.usd ? money(day.usd) : tokens(day.tokens)} note=${f('home.spentNote', tokens(day.tokens), money(wk.usd))}>
        <${Spark} values=${wk.days.map(d => d.usd || d.tokens)} label=${t('home.week')} />
      <//>
    </div>
    <div class="home-grid">
      <div class="home-col">
        ${waiting}
        ${runsPanel}
        <${Panel} title=${t('home.recent')} count=${recent.length}>
          ${recent.length ? html`<div class="rows">${recent.map(r => html`<div class="end-row" key=${r.id}>
            <${Status} state=${r.state} />
            <button type="button" class="run-main" onClick=${() => onOpen(r.task)}><span class="run-title ell">${st.tasks[r.task]?.title || r.task}</span><span class="mono t-muted">${r.id}</span></button>
            <span class="run-who mono">${who(r)}</span><span class="t-muted ell">${endWords(w, r)}</span><span class="mono t-muted">${hhmm(r.ended_at)}</span>
          </div>`)}</div>` : html`<p class="empty">${t('home.noRecent')}</p>`}
        <//>
      </div>
      <div class="home-col">
        <${Panel} title=${t('home.lanes')} actions=${html`<span class="t-muted">${f('home.lanesNote', hhmm(day7.from))}</span>`}>
          <div class="panel-body"><${Timeline} from=${day7.from} to=${day7.to} label=${t('home.lanes')} lanes=${day7.lanes.map(l => ({
            name: l.name,
            note: l.state === 'offline' ? t('home.offline') : f(l.slots && l.active >= l.slots ? 'home.full' : 'home.inUse', l.active, l.slots),
            tone: l.state === 'offline' ? 'failed' : l.slots && l.active >= l.slots ? 'warning' : '',
            rows: l.rows.map(row => row.map(s => ({...s, title: `${st.tasks[s.task]?.title || s.task} · ${s.run}`, onClick: () => onOpen(s.task)}))),
          }))} /></div>
        <//>
        <${Panel} title=${t('home.week')} actions=${html`<span class="t-muted">${f('home.weekNote', tokens(wk.tokens), money(wk.usd))}</span>`}>
          <div class="panel-body"><${Bars} label=${t('home.week')} format=${tokens} keys=${wk.providers.map(p => ({key: p, label: p}))}
            bars=${wk.days.map((d, i) => ({label: new Date(d.at).toLocaleDateString(lang, {weekday: 'narrow'}), now: i === 6, parts: d.providers,
              title: `${new Date(d.at).toLocaleDateString(lang)} · ${tokens(d.tokens)} tok${d.usd ? ' · ' + money(d.usd) : ''}`}))} /></div>
        <//>
        <${Panel} title=${t('home.results')} actions=${html`<span class="t-muted">${f('home.resultsN', wk.ended)}</span>`}>
          <div class="panel-body"><${Meter} label=${t('home.results')} parts=${['exited', 'failed', 'stopped', 'canceled', 'abandoned']
            .map(k => ({key: k, value: wk.ends[k], label: t('home.end.' + k)}))} /></div>
        <//>
      </div>
    </div>
    ${dialog}
  </div>`;
}

// headOf is what the page of one waiting item is titled by.
const headOf = v => (v.group === 'answer' ? (isPermission(v.req) ? 'home.wait.permission' : 'home.wait.answer')
  : v.group === 'accept' ? 'home.wait.accept' : 'home.wait.error');

// WaitPage is one waiting item with the phone's screen to itself, as a notice or a row of the list opens it: what it
// asks, the ways to answer, where it stands among what waits. Once it no longer waits, the one now at its place in the
// list takes over (the address replaced, so going back still goes to the list); when none is left the list comes back.
// An item that no longer waited when the page opened says so. list is what waits, loaded whether the inbox came yet;
// title(v) and body(v) are the home's words and answer forms for an item.
function WaitPage({wait, list, loaded, title, body, toasts, onWait, onBack, onOpen, dialog}) {
  const {t, f} = useWords();
  const at = list.findIndex(v => v.x.task === wait);
  const seen = useRef({task: '', index: 0});
  useEffect(() => {
    if (!loaded) return;
    if (at >= 0) { seen.current = {task: wait, index: at}; return; }
    if (seen.current.task !== wait) return;
    if (list.length) return onWait(list[Math.min(seen.current.index, list.length - 1)].x.task, {replace: true});
    toasts.show({text: t('home.allDone')});
    onBack();
  });
  const v = at >= 0 ? list[at] : null;
  return html`<section class="page-over wait-page" aria-labelledby="wait-head">
    <header class="page-head">
      <button type="button" class="back" onClick=${onBack}><${Icon} name="back" />${t('home.waiting')}</button>
      <h1 id="wait-head" class="ell">${v ? t(headOf(v)) : t('home.waiting')}</h1>
      <span class="mono t-muted wait-pos">${v ? `${at + 1} / ${list.length}` : ''}</span>
    </header>
    <div class="page-body">
      ${v ? html`
        <div class="wait-title"><${Status} state=${stateOf(v.group, v.x.reason)} />
          <span class="wait-name"><b>${v.task?.title || v.x.title || v.x.task}</b><span class="t-muted">${title(v)}</span></span></div>
        ${body(v)}
        <p class="lbl wait-next">${list.length > 1 ? f('home.nextLeft', list.length - 1) : t('home.nextNone')}</p>`
      : loaded && html`<div class="wait-gone">
        <p><b>${t('home.goneTitle')}</b></p><p class="t-muted">${t('home.goneNote')}</p>
        <div class="wait-foot">
          ${list.length > 0 && html`<${Button} kind="primary" onClick=${() => onWait(list[0].x.task, {replace: true})}>${t('home.next')}<//>`}
          <${Button} onClick=${() => onOpen(wait)}>${t('home.open')}<//>
        </div>
      </div>`}
    </div>
    ${dialog}
  </section>`;
}

// WaitBody is an open waiting item: what it asks or how it failed, what it just did, and the ways to answer: the
// answer form for a request (every question, allow for the run when offered), a reply for a run that ended asking.
// On a phone it also says whose it is, where an answer goes, and, for a run to accept, what it changed.
function WaitBody({v, busy, scope, gone, fetchOutput, changes, onOpen, onClose, onDone, onRetry, onRework, onAnswer, onReply}) {
  const {t, f} = useWords();
  const phone = usePhone();
  const [steps, setSteps] = useState(null);
  const [changed, setChanged] = useState(null);
  const [text, setText] = useState('');
  const showChanges = phone && v.group === 'accept' && !!v.run && !!changes?.can?.();
  useEffect(() => {
    if (!v.run || !fetchOutput || showChanges) return;
    let live = true;
    fetchOutput(v.run.id).then(p => {
      if (!live) return;
      const tools = (p?.events || []).filter(e => e.title && ['tool', 'cmd', 'edit', 'mcp'].includes(e.kind));
      setSteps(tools.slice(-stepsShown).map(e => ({glyph: glyphOf[e.family] || '·', text: e.title})));
    }, () => live && setSteps([]));
    return () => { live = false; };
  }, [v.run?.id, showChanges]);
  useEffect(() => {
    if (!showChanges) return;
    let live = true;
    changes.list(v.run.id, {ended: !sel.openStates.includes(v.run.state)}).then(c => live && setChanged(c), () => live && setChanged(null));
    return () => { live = false; };
  }, [v.run?.id, showChanges]);
  const permission = isPermission(v.req);
  const q = v.req?.questions?.[0];
  const detail = v.group === 'answer' ? (v.req ? (permission ? '' : v.run?.ask !== q?.question ? v.run?.ask : '') : v.run?.ask) : v.group === 'error'
    ? [v.run?.detail, v.run?.checked?.tail].filter(Boolean).join('\n') : v.run?.last;
  const sendReply = () => { if (text.trim()) { onReply(text.trim()); setText(''); } };
  const agent = v.run?.agent || '';
  const goes = v.group === 'answer' && v.run ? (v.req ? (gone === undefined ? f('home.toRun', agent) : '') : f('home.toReply', agent))
    : v.group === 'accept' && v.run?.checked?.exit === 0 ? f('home.checkPassed', (v.run.checked.argv || []).join(' ')) : '';
  return html`<div class="wait-body">
    ${phone && html`<div class="mono wait-meta">${[v.x.task, who(v.run), ...(v.x.as || []).map(a => t('home.as.' + a))].filter(Boolean).join(' · ')}</div>`}
    ${detail && html`<pre class="box">${detail}</pre>`}
    ${changed?.files?.length > 0 && html`<div class="wait-steps"><span class="lbl">${changed.total.files === 1
      ? f('home.changedOne', changed.total.add, changed.total.del) : f('home.changed', changed.total.files, changed.total.add, changed.total.del)}</span>
      ${changed.files.slice(0, stepsShown).map(c => html`<div class="step"><span class="mono step-glyph">${opGlyph[c.op] || '~'}</span>
        <span class="mono ell">${c.path}</span><span class="mono t-muted step-n">${c.binary ? '' : `+${c.add} −${c.del}`}</span></div>`)}</div>`}
    ${steps?.length > 0 && html`<div class="wait-steps"><span class="lbl">${t('home.did')}</span>
      ${steps.map(s => html`<div class="step"><span class="mono step-glyph">${s.glyph}</span><span class="mono ell">${s.text}</span></div>`)}</div>`}
    ${v.group === 'answer' && v.req && html`<${AnswerForm} req=${v.req} scope=${scope} gone=${gone} busy=${busy} onAnswer=${onAnswer} />`}
    ${v.group === 'answer' && !v.req && v.run && html`<div class="wait-own">
      <${TextInput} label=${t('home.reply')} value=${text} onInput=${setText}
        onKeyDown=${e => { if (e.key === 'Enter' && !e.isComposing) { e.preventDefault(); sendReply(); } }} />
      <${Button} kind="primary" disabled=${busy || !text.trim()} onClick=${sendReply}>${t('home.send')}<//>
    </div>`}
    ${phone ? html`
      ${(onDone || onRetry) && html`<div class="wait-foot">
        ${onDone && html`<${Button} kind="primary" disabled=${busy} onClick=${onDone}>${t('home.done')}<//>`}
        ${onRework && html`<${Button} disabled=${busy} onClick=${onRework}>${t('home.rework')}<//>`}
        ${onRetry && html`<${Button} kind="primary" disabled=${busy} onClick=${onRetry}>${t('home.retry')}<//>`}
      </div>`}
      <div class="wait-goes"><span class="ell">${goes}</span><${Button} kind="quiet" onClick=${() => onOpen(v.x.task)}>${t('home.openTask')}<//></div>`
    : html`<div class="wait-foot">
      ${onDone && html`<${Button} kind="primary" keyName="Shift+D" disabled=${busy} onClick=${onDone}>${t('home.done')}<//>`}
      ${onRetry && html`<${Button} keyName="d" disabled=${busy} onClick=${onRetry}>${t('home.retry')}<//>`}
      <${Button} kind="quiet" keyName="Enter" onClick=${() => onOpen(v.x.task)}>${t('home.open')}<//>
      <${Button} kind="quiet" keyName="Space" onClick=${onClose}>${t('home.collapse')}<//>
    </div>`}
  </div>`;
}
