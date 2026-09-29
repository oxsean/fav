// Tasks is the task page: every task the viewer sees as a list, a board by situation or a tree, filtered to theirs, a
// project, a column or a search, with the selected task beside it. On a phone the list is in sections by situation
// (the board's columns), the tree is indented, and a task takes the screen. Every write goes through commands; what
// can be undone says so in its toast.
import {useState, useEffect, useRef} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useSignalValue, useActions} from '../ui/base.js';
import {Button, Chip, Chips, Segmented} from '../ui/controls.js';
import {Status} from '../ui/status.js';
import {Table} from '../ui/table.js';
import {TreeList} from '../ui/tree.js';
import {Board} from '../ui/board.js';
import {Drawer, Modal} from '../ui/overlay.js';
import {Icon} from '../ui/icons.js';
import * as tk from '../core/tasks.js';
import {views} from '../core/router.js';
import {clock as hhmm, day, money} from '../core/format.js';
import {dayStart} from '../core/select.js';
import {Task, sitState, sitWord, who} from './task.js';
import {TaskForm, Dispatch, Move, PlanReview, Gate, DRAFT_KEY} from './taskforms.js';
import {Conversation} from './conversation.js';
import {RunChanges} from './changes.js';
import {RunPage} from './runs.js';
import './taskwords.js';

// ⚠️ Where this browser keeps the viewer's task filter.
export const FILTER_KEY = 'tend-task-filter';
// ⚠️ Where this browser keeps which pane of a task the viewer last chose (overview or output).
export const PANE_KEY = 'tend-task-pane';
// ⚠️ The coordinator's codes for an answer that never came: the write may or may not have gone through.
const unsure = ['timeout', 'offline', 'closed'];

const readFilter = storage => { try { return {mine: false, project: '', column: '', ...JSON.parse(storage?.getItem(FILTER_KEY) || '{}'), q: ''}; } catch { return {mine: false, project: '', column: '', q: ''}; } };
const keepFilter = (storage, {mine, project, column}) => { try { storage?.setItem(FILTER_KEY, JSON.stringify({mine, project, column})); } catch {} };

const when = (at, now) => { const t = Date.parse(at); return Number.isNaN(t) ? '' : t >= dayStart(now) ? hhmm(t) : day(t); };

// Tasks: intent is a signal the app sets to {kind: new | search} for the page to act on; storage keeps the filter and
// a new task's draft in this browser.
export function Tasks({store, commands, toasts, wire, router, session, clock = () => Date.now(), storage, intent, prefs, copy, changes}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  useSignalValue(store.rev.tasks);
  useSignalValue(store.rev.runs);
  useSignalValue(store.rev.projects);
  const machines = useSignalValue(store.machines);
  useSignalValue(commands.pending);
  const route = useSignalValue(router.route);
  const want = useSignalValue(intent);
  const [filter, setFilter] = useState(() => readFilter(storage));
  const [pane, setPaneState] = useState(() => { try { const p = storage?.getItem(PANE_KEY); return ['overview', 'changes'].includes(p) ? p : 'output'; } catch { return 'output'; } });
  const [runTab, setRunTab] = useState('output');
  const setPane = v => { setPaneState(v); try { storage?.setItem(PANE_KEY, v); } catch {} };
  const [collapsed, setCollapsed] = useState(() => new Set());
  const [modal, setModal] = useState(null);
  const [agents, setAgents] = useState(null);
  const search = useRef(null);
  const st = store.state, now = clock(), me = session?.id || '';
  const view = phone && route.view === 'board' ? 'list' : route.view || 'list';
  const picked = route.task || '';

  const filterBy = patch => setFilter(o => { const n = {...o, ...patch}; keepFilter(storage, n); return n; });
  const keep = tk.matcher(st, {...filter, me});
  const go = (task, {push = false} = {}) => router.go({page: 'tasks', view: route.view || 'list', ...(task ? {task} : {})}, {replace: !push});

  const failed = e => toasts.show({text: unsure.includes(e.code) ? f('app.unsure', e.code) : f('app.failed', e.code || String(e.message || e)), tone: 'danger'});
  const send = (method, params, key) => commands.send(method, params, {key}).catch(e => { failed(e); throw e; });
  const busy = task => !!task && (commands.state('task:' + task.id) === 'pending' || commands.state('new') === 'pending');
  const quiet = p => p.catch(() => {});
  const reversible = (task, method, params, back, text) => quiet(send(method, params, 'task:' + task.id).then(() => toasts.show({text,
    undo: () => quiet(send(back.method, back.params, 'task:' + task.id))})));
  const setStatus = (task, status, text) => reversible(task, 'task.set_status', {id: task.id, status}, {method: 'task.set_status', params: {id: task.id, status: task.status}}, text);

  const needAgents = () => {
    if (agents !== null) return;
    setAgents([]);
    if (wire?.has?.('agent.list') === false) return;
    wire.call('agent.list', {}).then(r => setAgents(r?.agents || []), () => {});
  };
  const open = m => { if (['form', 'dispatch'].includes(m.kind)) needAgents(); setModal(m); };
  const close = () => setModal(null);

  useEffect(() => {
    if (!want) return;
    if (want.kind === 'new') open({kind: 'form', mode: 'new'});
    if (want.kind === 'search') search.current?.focus?.();
    intent.value = null;
  }, [want]);

  function act(id, task) {
    const run = tk.openRun(st, task.id), last = tk.runsOf(st, task.id)[0];
    switch (id) {
      case 'start': return quiet(send('task.start', {id: task.id}, 'task:' + task.id).then(() => toasts.show({text: f('toast.started', task.title)})));
      case 'stop': return run && setModal({kind: 'confirm', title: t('home.confirmStop'), note: f('home.confirmStopNote', task.title, run.machine), label: t('home.stop'),
        go: () => quiet(send('run.stop', {id: run.id}, 'run:' + run.id).then(() => toasts.show({text: f('toast.stopping', task.title)})))});
      case 'review': return open({kind: 'plan', task});
      case 'gate': return open({kind: 'gate', task});
      case 'sendBack': return open({kind: 'gate', task, reply: last?.id});
      case 'ack': return quiet(send('task.source_ack', {id: task.id, accept: true}, 'task:' + task.id).then(() => toasts.show({text: t('toast.acked')})));
      case 'keep': return quiet(send('task.source_ack', {id: task.id}, 'task:' + task.id).then(() => toasts.show({text: t('toast.kept')})));
      case 'merge': return quiet(send('task.merge', {id: task.id}, 'task:' + task.id).then(() => toasts.show({text: f('toast.merging', task.title)})));
      case 'done': return setStatus(task, 'done', f('toast.done', task.title));
      case 'reopen': return setStatus(task, 'todo', f('toast.reopened', task.title));
      case 'backlog': return setStatus(task, 'backlog', f('toast.backlog', task.title));
      case 'cancel': return setModal({kind: 'confirm', title: t('confirm.cancel'), note: f('confirm.cancelNote', task.title), label: t('do.cancel'),
        go: () => quiet(send('task.set_status', {id: task.id, status: 'canceled'}, 'task:' + task.id).then(() => toasts.show({text: f('toast.canceled', task.title)})))});
      case 'dispatch': return open({kind: 'dispatch', task});
      case 'plan': return quiet(send('task.plan', {id: task.id}, 'task:' + task.id).then(() => toasts.show({text: f('toast.planning', task.title)})));
      case 'edit': return open({kind: 'form', mode: 'edit', task});
      case 'child': return open({kind: 'form', mode: 'child', base: task});
      case 'copy': return open({kind: 'form', mode: 'copy', base: task});
      case 'move': return open({kind: 'move', task});
    }
  }

  async function submitForm(m, how, params) {
    if (how === 'save') {
      if (Object.keys(params).length > 1) await send('task.edit', params, 'task:' + params.id);
      toasts.show({text: f('toast.saved', params.title || m.task.title)});
      close();
      return;
    }
    const made = await send('task.create', params, 'new');
    if (m.mode === 'new') { try { storage?.removeItem?.(DRAFT_KEY); } catch {} }
    close();
    toasts.show({text: f('toast.created', made.title)});
    go(made.id, {push: true});
    if (how === 'start') await send('task.start', {id: made.id}, 'task:' + made.id);
    if (how === 'plan') await send('task.plan', {id: made.id}, 'task:' + made.id);
  }

  // what the list shows, in the order next and previous go through it
  const listRows = tk.rows(st, keep);
  const treeRows = tk.tree(st, keep, collapsed);
  const boardCols = tk.board(st, keep);
  const order = view === 'tree' ? treeRows.map(r => r.task.id) : view === 'board' ? tk.columns.flatMap(c => boardCols[c].map(r => r.task.id)) : listRows.map(r => r.task.id);
  const task = picked ? st.tasks[picked] : null;
  const at = order.indexOf(picked);
  const step = n => { const id = order[at + n]; if (id) go(id); };
  const acts = task ? tk.actionsFor(st, task) : {primary: '', more: []};
  const can = id => !!task && !busy(task) && (acts.primary === id || acts.more.includes(id));

  useActions('page', {
    view: {run: () => router.go({page: 'tasks', view: (phone ? ['list', 'tree'] : views)[((phone ? ['list', 'tree'] : views).indexOf(view) + 1) % (phone ? 2 : 3)], ...(picked ? {task: picked} : {})}, {replace: true})},
    search: {run: () => search.current?.focus?.()},
    dispatch: {when: () => can('dispatch'), run: () => act('dispatch', task)},
    edit: {when: () => can('edit'), run: () => act('edit', task)},
    stop: {when: () => can('stop'), run: () => act('stop', task)},
    done: {when: () => can('done'), run: () => act('done', task)},
  }, {active: !modal});

  const toggle = id => setCollapsed(s => { const n = new Set(s); if (n.has(id)) n.delete(id); else n.add(id); return n; });
  const prog = x => { const p = tk.progress(st, x.id); return p ? `${p.done}/${p.of}` : ''; };
  const whoOf = x => { const d = tk.defaults(st, x); return who({agent: d.agent, machine: d.machine}); };
  const count = c => Object.values(st.tasks).filter(x => tk.matcher(st, {...filter, column: c, me})(x)).length;

  const columns = [
    {id: 'sit', label: '', width: '18px', mobile: 'lead', render: r => html`<${Status} state=${sitState(r.sit)} />`},
    {id: 'title', label: t('tasks.col.title'), mobile: 'primary', sort: (a, b) => a.task.title.localeCompare(b.task.title),
      render: r => html`<span class="task-cell"><span class="ell">${r.task.title}</span><span class="mono t-muted">${r.task.id}</span>${prog(r.task) && html`<span class="chip mono">${prog(r.task)}</span>`}</span>`},
    {id: 'why', label: t('tasks.col.why'), width: 'minmax(0, .8fr)', mobile: 'secondary', render: r => sitWord(w, r.sit)},
    {id: 'who', label: t('tasks.col.who'), width: '170px', render: r => html`<span class="mono t-muted">${whoOf(r.task)}</span>`},
    {id: 'updated', label: t('tasks.col.updated'), width: '56px', align: 'right', mobile: 'trailing',
      sort: (a, b) => String(a.task.updated_at).localeCompare(String(b.task.updated_at)), render: r => html`<span class="mono t-muted">${when(r.task.updated_at, now)}</span>`},
  ];
  const listKeys = !modal && !(phone && task);

  let body;
  if (view === 'tree') {
    body = html`<${TreeList} label=${t('tasks.view.tree')} selected=${picked} onSelect=${id => go(id)} onOpen=${id => go(id, {push: phone})} onFold=${toggle}
      active=${listKeys} empty=${t('tasks.none')} rows=${treeRows.map(r => ({id: r.task.id, depth: r.depth, kids: r.kids, open: r.open,
        lead: html`<${Status} state=${sitState(r.sit)} />`, title: r.task.title, sub: [sitWord(w, r.sit), whoOf(r.task)].filter(Boolean).join(' · '), trail: prog(r.task)}))} />`;
  } else if (view === 'board') {
    body = html`<${Board} label=${t('tasks.view.board')} selected=${picked} onSelect=${id => go(id)} onOpen=${id => go(id)} active=${listKeys}
      canDrop=${(id, c) => { const d = tk.drop(st, st.tasks[id], c); return !!d && !d.why; }}
      onDrop=${(id, c) => {
        const x = st.tasks[id], d = tk.drop(st, x, c);
        const text = f('toast.moved.board', x.title, t('col.' + c));
        if (d.undo) reversible(x, d.method, d.params, d.undo, text);
        else quiet(send(d.method, d.params, 'task:' + id).then(() => toasts.show({text})));
      }}
      onRefused=${(id, c) => { const d = tk.drop(st, st.tasks[id], c); if (d?.why) toasts.show({text: t('board.no.' + d.why), tone: 'warning'}); }}
      columns=${tk.columns.map(c => ({id: c, label: t('col.' + c), cards: boardCols[c].map(r => {
        const cost = tk.spent(st, r.task.id).usd;
        return {id: r.task.id, content: html`<span class="bc-top"><span class="mono t-muted">${r.task.id}</span>${r.task.loops > 0 && html`<span class="mono t-muted">↺${r.task.loops}</span>`}
          ${cost >= 0.005 && html`<span class="mono t-muted">${money(cost)}</span>`}${prog(r.task) && html`<span class="chip mono">${prog(r.task)}</span>`}</span>
          <span class="bc-title">${r.task.title}</span>
          ${r.task.stage && html`<span class="chip">${f('gate.stage', r.task.stage, r.task.loops || 0)}</span>`}
          <span class="bc-sit"><${Status} state=${sitState(r.sit)} label=${sitWord(w, r.sit)} /></span>
          <span class="bc-who mono">${whoOf(r.task)}</span>`};
      })}))} />`;
  } else if (phone) {
    const sections = tk.columns.filter(c => boardCols[c].length);
    body = sections.length ? sections.map(c => html`<section class="sect" key=${c}><h2 class="sect-h">${t('col.' + c)} <span class="mono count">${boardCols[c].length}</span></h2>
      <${Table} label=${t('col.' + c)} columns=${columns} rows=${boardCols[c]} rowKey=${r => r.task.id} selected=${picked} onOpen=${id => go(id, {push: true})} active=${false} />
    </section>`) : html`<p class="empty">${t('tasks.none')}</p>`;
  } else {
    body = html`<${Table} label=${t('tasks.view.list')} columns=${columns} rows=${listRows} rowKey=${r => r.task.id} selected=${picked} onSelect=${id => go(id)}
      onOpen=${id => go(id)} active=${listKeys} empty=${t('tasks.none')} />`;
  }

  const projects = Object.values(st.projects);
  const head = html`<div class=${cx('tasks-head', phone && 'tasks-head-phone')}>
    ${!phone && html`<h1 class="tasks-title">${t('tasks.title')}</h1>`}
    <${Segmented} label=${t('tasks.view')} value=${view} onChange=${v => router.go({page: 'tasks', view: v, ...(picked ? {task: picked} : {})}, {replace: true})}
      options=${(phone ? ['list', 'tree'] : views).map(v => ({value: v, label: t('tasks.view.' + v)}))} />
    <input class="in tasks-search" type="search" ref=${search} value=${filter.q} placeholder=${t('tasks.search')} aria-label=${t('tasks.search')}
      onInput=${e => setFilter(o => ({...o, q: e.currentTarget.value}))} onKeyDown=${e => { if (e.key === 'Escape') e.currentTarget.blur?.(); }} />
    ${!phone && html`<${Button} kind="primary" icon="plus" keyName="n" onClick=${() => open({kind: 'form', mode: 'new'})}>${t('tasks.new')}<//>`}
    <${Chips} label=${t('tasks.filter')}>
      <${Chip} label=${t('tasks.mine')} on=${filter.mine} onClick=${() => filterBy({mine: !filter.mine})} />
      ${projects.length > 1 && projects.map(p => html`<${Chip} label=${p.name} on=${filter.project === p.id} onClick=${() => filterBy({project: filter.project === p.id ? '' : p.id})} />`)}
      ${tk.columns.map(c => html`<${Chip} label=${t('col.' + c)} count=${count(c)} on=${filter.column === c} onClick=${() => filterBy({column: filter.column === c ? '' : c})} />`)}
    <//>
  </div>`;

  const runOf = route.run || '';
  const conv = () => task && prefs && html`<${Conversation} store=${store} commands=${commands} toasts=${toasts} prefs=${prefs} task=${task}
    run=${runOf} target=${route.event || ''} copy=${copy} />`;
  const showRun = id => router.go({page: 'tasks', view: route.view || 'list', task: picked, run: id}, {replace: !phone});
  const taskChanges = changes && task && (() => html`<${RunChanges} changes=${changes} runs=${tk.runsOf(st, task.id)} />`);
  const detail = picked && html`<${Task} store=${store} task=${task} now=${now} busy=${busy(task)} onAct=${act} onGo=${id => go(id, {push: phone})}
    output=${prefs ? conv : null} changes=${taskChanges} onRun=${prefs ? showRun : null} run=${runOf} pane=${runOf && pane === 'overview' ? 'output' : pane} onPane=${setPane} onClose=${phone || view === 'board' ? undefined : () => go('')} />`;
  const nav = phone && picked && html`<span class="det-nav">
    <${Button} kind="quiet" icon="up" label=${t('ui.prev')} disabled=${at <= 0} onClick=${() => step(-1)} />
    <${Button} kind="quiet" icon="down" label=${t('ui.next')} disabled=${at < 0 || at >= order.length - 1} onClick=${() => step(1)} />
  </span>`;

  const dialogs = modal && ({
    confirm: () => html`<${Modal} title=${modal.title} onClose=${close} actions=${[{label: t('confirm.keep'), onClick: close},
      {label: modal.label, kind: 'primary', keyName: 'Mod+Enter', onClick: () => { close(); modal.go(); }}]}><p>${modal.note}</p><//>`,
    form: () => html`<${TaskForm} store=${store} wire=${wire} agents=${agents || []} machines=${machines} mode=${modal.mode} task=${modal.task} base=${modal.base}
      storage=${storage} busy=${commands.state('new') === 'pending' || busy(modal.task)} onClose=${close} onSubmit=${(how, p) => submitForm(modal, how, p).catch(() => {})} />`,
    dispatch: () => html`<${Dispatch} store=${store} wire=${wire} task=${modal.task} agents=${agents || []} machines=${machines} busy=${busy(modal.task)} onClose=${close}
      onLater=${() => { close(); act('backlog', modal.task); }}
      onDispatch=${(machine, agent) => quiet(send('run.dispatch', {task: modal.task.id, machine, agent}, 'task:' + modal.task.id)
        .then(() => { close(); toasts.show({text: f('toast.dispatched', modal.task.title)}); }))} />`,
    move: () => html`<${Move} store=${store} task=${modal.task} busy=${busy(modal.task)} onClose=${close} onMove=${(parent, after) => {
      const x = modal.task;
      close();
      reversible(x, 'task.move', {id: x.id, parent, after}, {method: 'task.move', params: {id: x.id, parent: x.parent || '', after: x.after || []}}, f('toast.moved', x.title));
    }} />`,
    plan: () => {
      const x = st.tasks[modal.task.id] || modal.task;
      return html`<${PlanReview} key=${x.rev} store=${store} task=${x} busy=${busy(x)} onClose=${close}
        onSave=${plan => quiet(send('task.plan_save', {id: x.id, plan, expected_rev: x.rev}, 'task:' + x.id).then(() => toasts.show({text: t('toast.draftSaved')})))}
        onApply=${() => { const n = x.draft?.plan?.tasks?.length || 0; quiet(send('task.plan_apply', {id: x.id, expected_rev: x.rev}, 'task:' + x.id).then(() => { close(); toasts.show({text: f('toast.applied', n)}); })); }}
        onDiscard=${() => quiet(send('task.plan_save', {id: x.id, expected_rev: x.rev}, 'task:' + x.id).then(() => { close(); toasts.show({text: t('toast.discarded')}); }))}
        onReply=${text => quiet(send('run.continue', {run: x.draft.run, text}, 'run:' + x.draft.run).then(() => toasts.show({text: f('toast.replied', x.draft.run)})))} />`;
    },
    gate: () => {
      const x = st.tasks[modal.task.id] || modal.task;
      return html`<${Gate} task=${x} reply=${modal.reply || null} busy=${busy(x)} onClose=${close}
        onPass=${() => quiet(send('task.gate', {id: x.id, pass: true, expected_rev: x.rev}, 'task:' + x.id).then(() => { close(); toasts.show({text: f('toast.passed', x.title)}); }))}
        onBack=${notes => quiet((modal.reply ? send('run.continue', {run: modal.reply, text: notes}, 'run:' + modal.reply)
          : send('task.gate', {id: x.id, pass: false, notes, expected_rev: x.rev}, 'task:' + x.id)).then(() => { close(); toasts.show({text: f('toast.sentBack', x.title)}); }))} />`;
    },
  })[modal.kind]();

  if (phone) {
    return html`<div class="tasks tasks-phone">
      ${head}${body}
      <button type="button" class="fab" aria-label=${t('tasks.new')} onClick=${() => open({kind: 'form', mode: 'new'})}><${Icon} name="plus" size=${22} /></button>
      ${picked && html`<${Drawer} title=${task?.title || picked} onClose=${() => router.go({page: 'tasks', view: route.view || 'list'})} extra=${nav}>${detail}<//>`}
      ${picked && task && runOf && st.runs[runOf] && prefs && html`<${Drawer} title=${f('runs.of', runOf, task.title)} onClose=${() => router.go({page: 'tasks', view: route.view || 'list', task: picked})}>
        <${RunPage} store=${store} commands=${commands} toasts=${toasts} prefs=${prefs} copy=${copy} changes=${changes} run=${st.runs[runOf]} now=${now}
          tab=${runTab} onTab=${setRunTab} target=${route.event || ''} onTask=${() => router.go({page: 'tasks', view: route.view || 'list', task: picked})} /><//>`}
      ${dialogs}
    </div>`;
  }
  const split = picked && view !== 'board';
  return html`<div class="tasks">
    ${head}
    <div class=${cx('tasks-body', split && 'tasks-split')}>
      <div class=${cx('tasks-view', 'tasks-' + view)}>${body}</div>
      ${split && html`<aside class="tasks-pane" aria-label=${task?.title || picked}>${detail}</aside>`}
    </div>
    ${picked && view === 'board' && html`<${Drawer} title=${task?.title || picked} onClose=${() => go('')}>${detail}<//>`}
    ${dialogs}
  </div>`;
}

