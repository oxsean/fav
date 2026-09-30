// runs is the runs page: every run, filtered by where it stands and by machine, the picked one previewed beside the
// list in the brief density (a desktop); and a run's own page (?page=runs&run=), its output, its changes and its facts.
// On a phone the list is cards under a line of how many machines are up, and a run takes the screen with the previous
// and next run of the list at its top. The task page opens the same run page for a run on a phone.
import {useState, useEffect, useRef} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useSignalValue, useActions} from '../ui/base.js';
import {Button, Chip, Chips, Segmented, Tabs} from '../ui/controls.js';
import {Status} from '../ui/status.js';
import {Table} from '../ui/table.js';
import {Drawer, Modal} from '../ui/overlay.js';
import {Output} from '../ui/output.js';
import * as sel from '../core/select.js';
import {tokens, money, duration, clock, day, usageTokens} from '../core/format.js';
import {register} from '../core/i18n.js';
import {unsure} from '../core/commands.js';
import {Conversation} from './conversation.js';
import {RunChanges} from './changes.js';
import './taskwords.js';

register('runs', {
  'runs.title': ['运行', 'Runs'], 'runs.help': ['每一次运行；选中一条看最近的输出。', 'Every run; pick one to see its latest output.'],
  'runs.filter': ['筛选运行', 'Filter the runs'], 'runs.f.all': ['全部', 'All'], 'runs.f.open': ['未结束', 'Not ended'], 'runs.f.failed': ['失败', 'Failed'],
  'runs.f.ended': ['已结束', 'Ended'], 'runs.machine': ['机器', 'Machine'], 'runs.anyMachine': ['全部机器', 'Every machine'],
  'runs.none': ['没有符合的运行', 'No runs match'], 'runs.machines': ['%d 台在线 · %d 台离线', '%d {machine|machines} up · %d down'],
  'runs.c.state': ['状态', 'State'], 'runs.c.run': ['运行', 'Run'], 'runs.c.task': ['任务', 'Task'], 'runs.c.stage': ['阶段', 'Stage'],
  'runs.c.where': ['Agent @ 机器', 'Agent @ machine'], 'runs.c.began': ['开始', 'Began'], 'runs.c.took': ['耗时', 'Took'], 'runs.c.spent': ['用量', 'Spent'],
  'runs.open': ['打开运行', 'Open the run'], 'runs.inTask': ['在任务里打开', 'Open in its task'], 'runs.of': ['%s · %s', '%s · %s'],
  'runs.tab.output': ['输出', 'Output'], 'runs.tab.changes': ['改动', 'Changes'], 'runs.tab.facts': ['概况', 'Facts'], 'runs.tabs': ['运行的内容', 'The run\'s panes'],
  'runs.noTask': ['这个运行的任务看不到了', 'This run\'s task is out of sight'], 'runs.gone': ['没有这个运行，或者看不到它', 'No such run, or it is out of sight'],
  'runs.fact.task': ['任务', 'Task'], 'runs.fact.state': ['状态', 'State'], 'runs.fact.where': ['在哪跑', 'Runs on'], 'runs.fact.stage': ['阶段', 'Stage'],
  'runs.fact.dir': ['目录', 'Directory'], 'runs.fact.branch': ['分支', 'Branch'], 'runs.fact.time': ['时间', 'Time'], 'runs.fact.spent': ['用量', 'Spent'],
  'runs.fact.doing': ['在做', 'Doing'], 'runs.fact.exit': ['退出码', 'Exit code'], 'runs.fact.session': ['会话', 'Session'], 'runs.fact.can': ['能做', 'Can'], 'runs.fact.verdict': ['结论', 'Verdict'],
  'runs.cap.steer': ['插话', 'steer'], 'runs.cap.interrupt': ['打断', 'interrupt'], 'runs.cap.answer_scope': ['整次运行都允许', 'allow for the run'],
  'runs.cap.questions': ['运行中作答', 'answer while it runs'], 'runs.cap.continue': ['接着会话再跑', 'go on with its session'], 'runs.cap.takeover': ['在终端接管', 'take over in a terminal'],
  'runs.span': ['%s 开始 · %s', 'began %s · %s'], 'runs.stopped': ['正在停止 %s', 'Stopping %s'],
});

// ⚠️ Where this browser keeps the runs page's filter.
export const RUNS_FILTER_KEY = 'tend-runs-filter';

export const runFilters = ['all', 'open', 'failed', 'ended'];
const tests = {all: () => true, open: r => sel.openStates.includes(r.state), failed: r => r.state === 'failed' || r.state === 'abandoned',
  ended: r => sel.endStates.includes(r.state)};
const caps = ['steer', 'interrupt', 'answer_scope', 'questions', 'continue', 'takeover'];

function readFilter(storage) {
  try {
    const f = JSON.parse(storage?.getItem(RUNS_FILTER_KEY) || '{}');
    return {state: runFilters.includes(f.state) ? f.state : 'all', machine: typeof f.machine === 'string' ? f.machine : ''};
  } catch { return {state: 'all', machine: ''}; }
}

// showMachine keeps the runs page's filter on machine, so the page opens on its runs.
export function showMachine(storage, machine) {
  try { storage?.setItem(RUNS_FILTER_KEY, JSON.stringify({...readFilter(storage), machine})); } catch {}
}

// listed is the runs of a filter, the latest queued first.
export function listed(state, filter) {
  return Object.values(state.runs).filter(r => tests[filter.state || 'all'](r) && (!filter.machine || r.machine === filter.machine))
    .sort((a, b) => (b.queued_at || '').localeCompare(a.queued_at || '') || b.id.localeCompare(a.id));
}

// machineCount is how many machines are up and how many are not, the retired left out.
export const machineCount = machines => {
  const live = machines.filter(m => !m.retired);
  const up = live.filter(m => m.state === 'connected').length;
  return {up, down: live.length - up};
};

const took = (r, now) => {
  const a = Date.parse(r.started_at || ''), b = r.ended_at ? Date.parse(r.ended_at) : now;
  return Number.isNaN(a) ? '' : duration(b - a);
};
const spent = r => (r.usage?.cost_usd >= 0.005 ? money(r.usage.cost_usd) : r.usage ? tokens(usageTokens(r.usage)) : '');
const where = r => [r.agent + (r.profile?.model ? ' · ' + r.profile.model : ''), r.machine].filter(Boolean).join(' @ ');

// useRunOutput holds a run's output while mounted and redraws as it grows.
function useRunOutput(store, id) {
  const [, redraw] = useState(0);
  const held = useRef(null);
  useEffect(() => {
    if (!id) return;
    const o = store.output(id);
    const bump = () => redraw(n => n + 1);
    const stops = [o.events.subscribe(bump), o.head.subscribe(bump)];
    held.current = o;
    redraw(n => n + 1);
    return () => { for (const s of stops) s(); o.release(); held.current = null; };
  }, [id]);
  return held.current;
}

// Preview is the picked run beside the list: what it is and its latest output in the brief density.
function Preview({store, run, now, onOpen, onTask}) {
  const w = useWords();
  const {t} = w;
  const o = useRunOutput(store, run.id);
  const task = store.state.tasks[run.task];
  const parts = o ? [{run, events: o.events.value, head: null}] : [];
  return html`<aside class="runs-preview" aria-label=${run.id}>
    <header class="runs-pv-head"><${Status} state=${run.state} word /><span class="mono">${run.id}</span><span class="ell runs-pv-task">${task?.title || run.task}</span></header>
    <dl class="facts runs-pv-facts">
      <dt>${t('runs.fact.where')}</dt><dd class="mono">${where(run)}</dd>
      ${sel.doing(run) && html`<dt>${t('runs.fact.doing')}</dt><dd class="ell">${sel.doing(run)}</dd>`}
      <dt>${t('runs.fact.time')}</dt><dd class="mono">${took(run, now) || '—'}${spent(run) ? ' · ' + spent(run) : ''}</dd>
    </dl>
    <div class="runs-pv-out"><${Output} bare parts=${parts} density="brief" key=${run.id} /></div>
    <div class="runs-pv-acts">
      <${Button} kind="primary" keyName="Enter" onClick=${() => onOpen(run.id)}>${t('runs.open')}<//>
      ${task && html`<${Button} onClick=${() => onTask(run)}>${t('runs.inTask')}<//>`}
    </div>
  </aside>`;
}

function Facts({store, run, now, onTask}) {
  const w = useWords();
  const {t, f} = w;
  const task = store.state.tasks[run.task];
  const can = caps.filter(c => run.caps?.[c]);
  return html`<dl class="facts run-facts">
    <dt>${t('runs.fact.task')}</dt><dd>${task ? html`<button type="button" class="det-link" onClick=${() => onTask(run)}><span class="ell">${task.title}</span><span class="mono t-muted">${task.id}</span></button>`
      : html`<span class="mono t-muted">${run.task}</span>`}</dd>
    <dt>${t('runs.fact.state')}</dt><dd><${Status} state=${run.state} word />${run.detail || run.reason ? html` <span class="t-muted">${run.detail || run.reason}</span>` : ''}</dd>
    <dt>${t('runs.fact.where')}</dt><dd class="mono">${where(run)}</dd>
    ${run.stage && html`<dt>${t('runs.fact.stage')}</dt><dd class="mono">${run.stage}</dd>`}
    ${run.dir && html`<dt>${t('runs.fact.dir')}</dt><dd class="mono">${run.dir}</dd>`}
    ${run.branch && html`<dt>${t('runs.fact.branch')}</dt><dd class="mono">${run.branch}</dd>`}
    ${(run.started_at || run.queued_at) && html`<dt>${t('runs.fact.time')}</dt><dd class="mono">${f('runs.span', day(run.started_at || run.queued_at) + ' ' + clock(run.started_at || run.queued_at), took(run, now) || '—')}</dd>`}
    ${spent(run) && html`<dt>${t('runs.fact.spent')}</dt><dd class="mono">${spent(run)}</dd>`}
    ${sel.doing(run) && html`<dt>${t('runs.fact.doing')}</dt><dd>${sel.doing(run)}</dd>`}
    ${run.verdict && html`<dt>${t('runs.fact.verdict')}</dt><dd>${run.verdict.verdict}${run.verdict.summary ? ' · ' + run.verdict.summary : ''}</dd>`}
    ${run.exit_code !== undefined && run.exit_code !== null && html`<dt>${t('runs.fact.exit')}</dt><dd class="mono">${run.exit_code}</dd>`}
    ${run.session && html`<dt>${t('runs.fact.session')}</dt><dd class="mono">${run.session}</dd>`}
    ${can.length > 0 && html`<dt>${t('runs.fact.can')}</dt><dd>${can.map(c => t('runs.cap.' + c)).join(' · ')}</dd>`}
  </dl>`;
}

// RunPage is one run: its output (the conversation it is in, at this run), its changes and its facts. tab and onTab
// pick the pane; onStop asks to stop it, onAbandon to abandon it once its machine lost it; onClose leaves it (a desktop;
// a phone's drawer has its own). Lines picked in its changes go into its task's box in drafts, shown on the output
// pane, unless onQuote(text, to) takes them (with notes, into the send-back notes too).
export function RunPage({store, commands, toasts, prefs, copy, changes, drafts, run, now, tab = 'output', onTab, onTask, onStop, onAbandon, onClose, target = '',
  notes = false, onQuote}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  const aff = useSignalValue(store.affordances);
  const task = store.state.tasks[run.task];
  const tabs = ['output', ...(changes ? ['changes'] : []), 'facts'].map(id => ({id, label: t('runs.tab.' + id)}));
  const open = sel.openStates.includes(run.state);
  const quote = !task || !prefs ? null : onQuote || (text => { drafts.quote(task.id, 'message', text); onTab('output'); });
  const pane = tab === 'changes' ? html`<div class="run-scroll"><${RunChanges} changes=${changes} runs=${[run]} prefs=${prefs} notes=${notes} onQuote=${quote} /></div>`
    : tab === 'facts' ? html`<div class="run-scroll"><${Facts} store=${store} run=${run} now=${now} onTask=${onTask} /></div>`
    : task && prefs ? html`<${Conversation} store=${store} commands=${commands} toasts=${toasts} prefs=${prefs} drafts=${drafts} task=${task} run=${run.id} target=${target} copy=${copy} />`
    : html`<p class="empty">${t('runs.noTask')}</p>`;
  const busy = commands.state('run:' + run.id) === 'pending';
  const abandon = run.state === 'unknown' && onAbandon && (aff.runs?.[run.id] || []).includes('abandon')
    && html`<${Button} kind="danger" disabled=${busy} onClick=${() => onAbandon(run)}>${t('do.abandon')}<//>`;
  const stop = open && onStop && html`<${Button} kind="danger" keyName=${phone ? '' : 'x'} disabled=${busy} onClick=${() => onStop(run)}>${t('home.stop')}<//>`;
  if (phone) {
    return html`<article class="run run-phone" aria-label=${run.id}>
      <div class="run-tabs"><${Segmented} label=${t('runs.tabs')} value=${tab} onChange=${onTab} options=${tabs.map(x => ({value: x.id, label: x.label}))} /></div>
      <div class="run-pane">${pane}</div>
      ${(stop || task) && html`<div class="run-acts">${stop}${abandon}${task && html`<${Button} onClick=${() => onTask(run)}>${t('runs.inTask')}<//>`}</div>`}
    </article>`;
  }
  return html`<article class="run det-out" aria-label=${run.id}>
    <header class="det-bar">
      <${Status} state=${run.state} word />
      <h2 class="det-bar-title ell" title=${f('runs.of', run.id, task?.title || run.task)}><span class="mono">${run.id}</span> · ${task?.title || run.task}</h2>
      <${Tabs} label=${t('runs.tabs')} value=${tab} onChange=${onTab} idPrefix=${'run-' + run.id} tabs=${tabs} />
      <span class="det-bar-acts">${stop}${abandon}${task && html`<${Button} onClick=${() => onTask(run)}>${t('runs.inTask')}<//>`}
        ${onClose && html`<${Button} kind="quiet" icon="close" label=${t('ui.close')} onClick=${onClose} />`}</span>
    </header>
    <div class="det-pane" role="tabpanel" id=${'run-' + run.id + '-' + tab + '-pane'}>${pane}</div>
  </article>`;
}

// Runs: storage keeps the filter; changes reads run changes (core/changes.js).
export function Runs({store, commands, toasts, router, prefs, copy, changes, drafts, storage, clock: now = () => Date.now()}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  useSignalValue(store.rev.runs);
  useSignalValue(store.rev.tasks);
  useSignalValue(commands.pending);
  const machines = useSignalValue(store.machines);
  const route = useSignalValue(router.route);
  const [filter, setFilter] = useState(() => readFilter(storage));
  const [picked, setPicked] = useState('');
  const [tab, setTab] = useState('output');
  const [confirm, setConfirm] = useState(null);
  const st = store.state, at = now();
  const rows = listed(st, filter);
  const all = Object.values(st.runs);
  const ids = rows.map(r => r.id);
  const sel0 = ids.includes(picked) ? picked : ids[0] || '';
  const opened = route.run ? st.runs[route.run] : null;
  const filterBy = patch => setFilter(o => { const n = {...o, ...patch}; try { storage?.setItem(RUNS_FILTER_KEY, JSON.stringify(n)); } catch {} return n; });
  const openRun = id => { setPicked(id); router.go({page: 'runs', run: id}, {replace: false}); };
  const closeRun = () => router.go({page: 'runs'});
  const toTask = r => router.go({page: 'tasks', task: r.task, run: r.id});
  const failed = e => toasts.show({text: unsure.includes(e.code) ? f('app.unsure', e.code) : f('app.failed', e.code || String(e.message || e)), tone: 'danger'});
  const askStop = r => setConfirm({title: t('home.confirmStop'), note: f('home.confirmStopNote', st.tasks[r.task]?.title || r.task, r.machine), label: t('home.stop'),
    go: () => commands.send('run.stop', {id: r.id}, {key: 'run:' + r.id}).then(() => toasts.show({text: f('runs.stopped', r.id)}), failed)});
  const askAbandon = r => setConfirm({title: t('confirm.abandon'), note: f('confirm.abandonNote', r.machine), label: t('do.abandon'),
    go: () => commands.send('run.abandon', {id: r.id}, {key: 'run:' + r.id}).then(() => toasts.show({text: f('toast.abandoned', st.tasks[r.task]?.title || r.task)}), failed)});
  const target = opened || st.runs[sel0];
  useActions('page', {
    stop: {run: () => target && askStop(target), when: () => !!target && sel.openStates.includes(target.state) && commands.state('run:' + target.id) !== 'pending'},
  }, {active: !confirm && !(phone && !opened)});

  const machineNames = [...new Set([...machines.filter(m => !m.retired).map(m => m.name), ...all.map(r => r.machine)])].filter(Boolean);
  const head = html`<div class=${cx('runs-head', phone && 'runs-head-phone')}>
    ${!phone && html`<h1 class="tasks-title">${t('runs.title')}</h1>`}
    ${phone && html`<button type="button" class="runs-machines" onClick=${() => router.go({page: 'machines'})}>${f('runs.machines', machineCount(machines).up, machineCount(machines).down)} ›</button>`}
    <${Chips} label=${t('runs.filter')}>${runFilters.map(x => html`<${Chip} label=${t('runs.f.' + x)} count=${all.filter(tests[x]).length}
      on=${filter.state === x} onClick=${() => filterBy({state: x})} />`)}<//>
    ${machineNames.length > 1 && html`<${Chips} label=${t('runs.machine')}>
      <${Chip} label=${t('runs.anyMachine')} on=${!filter.machine} onClick=${() => filterBy({machine: ''})} />
      ${machineNames.map(m => html`<${Chip} label=${m} on=${filter.machine === m} onClick=${() => filterBy({machine: filter.machine === m ? '' : m})} />`)}
    <//>`}
  </div>`;
  const columns = [
    {id: 'state', label: t('runs.c.state'), width: '28px', render: r => html`<${Status} state=${r.state} />`, mobile: 'lead'},
    {id: 'run', label: t('runs.c.run'), width: '64px', render: r => html`<span class="mono">${r.id}</span>`, sort: (a, b) => a.id.localeCompare(b.id), mobile: 'secondary'},
    {id: 'task', label: t('runs.c.task'), width: 'minmax(0, 2fr)', render: r => html`<span class="ell">${st.tasks[r.task]?.title || r.task}</span>`, mobile: 'primary'},
    {id: 'stage', label: t('runs.c.stage'), width: 'minmax(0, .6fr)', render: r => (r.stage ? html`<span class="mono ell">${r.stage}</span>` : '')},
    {id: 'where', label: t('runs.c.where'), width: 'minmax(0, 1.2fr)', render: r => html`<span class="mono ell">${where(r)}</span>`, mobile: 'secondary'},
    {id: 'began', label: t('runs.c.began'), width: '84px', align: 'right', render: r => html`<span class="mono t-muted">${r.started_at || r.queued_at ? day(r.started_at || r.queued_at) + ' ' + clock(r.started_at || r.queued_at) : ''}</span>`,
      sort: (a, b) => (a.started_at || a.queued_at || '').localeCompare(b.started_at || b.queued_at || '')},
    {id: 'took', label: t('runs.c.took'), width: '56px', align: 'right', render: r => html`<span class="mono">${took(r, at)}</span>`, mobile: 'trailing'},
    {id: 'spent', label: t('runs.c.spent'), width: '64px', align: 'right', render: r => html`<span class="mono">${spent(r)}</span>`},
  ];
  const table = html`<${Table} label=${t('runs.title')} columns=${columns} rows=${rows} selected=${sel0} onSelect=${setPicked} onOpen=${openRun}
    active=${!confirm && !opened} empty=${t('runs.none')} />`;
  const dialog = confirm && html`<${Modal} title=${confirm.title} onClose=${() => setConfirm(null)} actions=${[{label: t('confirm.keep'), onClick: () => setConfirm(null)},
    {label: confirm.label, kind: 'primary', keyName: 'Mod+Enter', onClick: () => { const c = confirm; setConfirm(null); c.go(); }}]}><p>${confirm.note}</p><//>`;
  const page = r => html`<${RunPage} store=${store} commands=${commands} toasts=${toasts} prefs=${prefs} copy=${copy} changes=${changes} drafts=${drafts} run=${r} now=${at}
    tab=${tab} onTab=${setTab} onTask=${toTask} onStop=${askStop} onAbandon=${askAbandon} onClose=${phone ? null : closeRun} />`;

  if (phone) {
    const i = opened ? ids.indexOf(opened.id) : -1;
    const step = n => { const id = ids[i + n]; if (id) router.go({page: 'runs', run: id}, {replace: true}); };
    const nav = opened && html`<span class="det-nav">
      <${Button} kind="quiet" icon="up" label=${t('ui.prev')} disabled=${i <= 0} onClick=${() => step(-1)} />
      <${Button} kind="quiet" icon="down" label=${t('ui.next')} disabled=${i < 0 || i >= ids.length - 1} onClick=${() => step(1)} />
    </span>`;
    return html`<div class="runs runs-phone">${head}${table}
      ${route.run && html`<${Drawer} title=${opened ? f('runs.of', opened.id, st.tasks[opened.task]?.title || opened.task) : route.run} onClose=${closeRun} extra=${nav}>
        ${opened ? page(opened) : html`<p class="empty">${t('runs.gone')}</p>`}<//>`}
      ${dialog}
    </div>`;
  }
  if (route.run) {
    return html`<div class="runs runs-one">${opened ? page(opened) : html`<p class="empty">${t('runs.gone')}</p>`}${dialog}</div>`;
  }
  return html`<div class="runs">
    ${head}
    <div class=${cx('runs-body', target && 'runs-split')}>
      <div class="runs-list">${table}</div>
      ${target && html`<${Preview} store=${store} run=${target} now=${at} onOpen=${openRun} onTask=${toTask} />`}
    </div>
    ${dialog}
  </div>`;
}
