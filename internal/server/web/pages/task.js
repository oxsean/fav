// Task is one task's page: where it stands and what it asks for, its brief and acceptance, where it sits in its tree,
// its workflow's stages and workpad, and its runs. On a desktop it is the pane beside the list (or a drawer beside the
// board); on a phone it takes the screen, with the previous and next task of the list at its top. What each button
// does is the task page's (onAct): this only says which ones there are.
import {useEffect} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useSignalValue, useName} from '../ui/base.js';
import {Button, Tabs} from '../ui/controls.js';
import {Status, waitOf, runState} from '../ui/status.js';
import {Markdown} from '../ui/markdown.js';
import {Menu} from '../ui/menu.js';
import * as tk from '../core/tasks.js';
import * as sel from '../core/select.js';
import {situation, pausedBy} from '../core/fold.js';
import {tokens, money, duration, clock, usageTokens} from '../core/format.js';
import {why} from './words.js';
import './taskwords.js';

// ⚠️ The keys of the task page's actions, from the action table.
export const actionKeys = {dispatch: 'd', edit: 'e', stop: 'x', done: 'Shift+D', pause: 'p', resume: 'p'};

// sitState is how a situation is drawn by Status; sitWord what it says. With the state, the run it rests on tells a
// wait nothing names (attend) and a run with no output for long (stalled).
const waitIn = (sit, state) => (sit.run ? waitOf(state?.runs?.[sit.run]) : '');

export function sitState(sit, state) {
  const wait = waitIn(sit, state);
  if (sit.kind === 'waiting' && wait && wait !== 'stalled') return wait;
  if (sit.kind === 'running' && wait === 'stalled') return 'stalled';
  if (sit.kind === 'waiting') return ['asked', 'permission', 'unknown'].includes(sit.reason) ? sit.reason
    : ['failed', 'stopped', 'canceled', 'abandoned', 'merge_conflict', 'setup_failed', 'work', 'blocked', 'max_loops', 'budget'].includes(sit.reason) ? 'failed' : 'waiting';
  return {running: 'running', queued: 'queued', backlog: 'backlog', done: 'done', canceled: 'canceled'}[sit.kind] || 'unknown';
}

export function sitWord(w, sit, state) {
  const wait = waitIn(sit, state);
  if (sit.kind === 'waiting' && wait === 'attend') return w.t('why.attend');
  if (sit.kind === 'running' && wait === 'stalled') return w.t('why.stalled');
  if (sit.kind === 'waiting' || sit.kind === 'queued') return why(w, sit.reason);
  return w.t('status.' + ({backlog: 'backlog', done: 'done', canceled: 'canceled', running: 'running'}[sit.kind] || 'unknown'));
}

export const who = x => [x?.agent, x?.machine].filter(Boolean).join(' @ ');

// actLabel is what an action's button says for this task.
const goesOn = sit => sit.reason === 'source_closed' || sit.reason === 'source_reopened';
export const actLabel = (w, id, sit) => w.t(id === 'keep' && goesOn(sit) ? 'do.keepGoing' : 'do.' + id);
// keptWord is the toast's key once keep is done.
export const keptWord = sit => (goesOn(sit) ? 'toast.goesOn' : 'toast.kept');

function Section({title, count, children}) {
  return html`<section class="det-sec"><h3 class="det-h">${title}${count !== undefined && html` <span class="mono count">${count}</span>`}</h3>${children}</section>`;
}

function TaskLink({state, id, onGo}) {
  const x = state.tasks[id];
  if (!x) return html`<span class="mono t-muted">${id}</span>`;
  return html`<button type="button" class="det-link" onClick=${() => onGo(id)}><${Status} state=${sitState(situation(state, x), state)} /><span class="ell">${x.title}</span><span class="mono t-muted">${id}</span></button>`;
}

function RunRow({run, now, onRun, on}) {
  const w = useWords();
  const began = Date.parse(run.started_at || run.queued_at), ended = run.ended_at ? Date.parse(run.ended_at) : now;
  const said = sel.doing(run) || run.detail || run.reason || '';
  return html`<div class=${cx('det-run', onRun && 'det-run-go', on && 'on')} role=${onRun ? 'button' : undefined} tabindex=${onRun ? 0 : undefined}
    onClick=${onRun && (() => onRun(run.id))} onKeyDown=${onRun && (e => { if (e.key === 'Enter') { e.preventDefault(); onRun(run.id); } })}>
    <${Status} state=${runState(run)} />
    <span class="det-run-main"><span class="mono">${run.id}</span> <span class="t-muted">${who(run)}${run.stage ? ' · ' + run.stage : ''}</span>
      ${said && html`<span class=${cx('det-run-said', 'ell', /^\$ |^go |^npm /.test(said) && 'mono')}>${said}</span>`}
      ${run.output_at && sel.openStates.includes(run.state) && html`<span class="det-run-said t-muted">${w.f('det.outputAt', clock(run.output_at))}</span>`}
      ${run.verdict && html`<span class="det-run-said">${w.f('det.verdict', run.verdict.verdict)}${run.verdict.summary ? ' · ' + run.verdict.summary : ''}</span>`}</span>
    <span class="det-run-time mono">${Number.isNaN(began) ? '' : duration(ended - began)}</span>
    <span class="det-run-cost mono">${run.usage?.cost_usd >= 0.005 ? money(run.usage.cost_usd) : run.usage ? tokens(usageTokens(run.usage)) : ''}</span>
  </div>`;
}

// TreeDone is how a tree done went, as the coordinator sums it up (task.TreeSummary in its affordances).
function TreeDone({summary: s}) {
  const {t, f} = useWords();
  const span = Date.parse(s.done_at) - Date.parse(s.started);
  const facts = [f('det.treeLeaves', s.done || 0, s.leaves || 0), s.canceled > 0 && f('det.treeCanceled', s.canceled), f('det.treeRuns', s.runs || 0),
    span > 0 && f('det.treeTook', duration(span))].filter(Boolean);
  return html`<div class="det-done" role="status">
    <b><span aria-hidden="true">✓ </span>${t('det.treeDone')}</b>
    <span>${facts.join(' · ')}</span>
    ${s.branch && html`<span>${f('det.treeBranch', s.branch)} · <span class="t-muted">${t('det.treeNote')}</span></span>`}
  </div>`;
}

// Task: its buttons are what the viewer's affordances give (core/tasks.js actionsOf), grey while busy (a write about it
// is out) or offline. With output
// (a function of nothing that draws the conversation) a desktop shows it as a second pane, open by default once the
// task has run, under a head folded into one line so the timeline keeps the height; onRun(id) shows a run's
// conversation (on a phone, on a screen of its own); run is the one shown; onClose, when given, closes the pane.
// changes, like output, draws the changes tab beside it.
export function Task({store, task, now, busy = false, offline = false, onAct, onGo, output, changes, onRun, run = '', pane = 'output', onPane = () => {}, onClose}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  const name = useName();
  const st = store.state;
  const aff = useSignalValue(store.affordances);
  const brief = task ? store.briefOf(task.id) : undefined;
  useEffect(() => { if (task && brief === undefined) store.brief(task.id).catch(() => {}); }, [task?.id, brief === undefined]);
  if (!task) return html`<p class="empty">${t('det.gone')}</p>`;
  const sit = situation(st, task);
  const treeDone = aff.tasks?.[task.id]?.tree_done;
  const paused = !tk.finished(task.status) && pausedBy(st, task.id);
  const acts = tk.actionsOf(st, aff, task);
  const grey = busy || offline;
  const kids = tk.childrenOf(st, task.id);
  const prog = tk.progress(st, task.id);
  const runs = tk.runsOf(st, task.id);
  const cost = tk.spent(st, task.id);
  const def = tk.defaults(st, task);
  const project = st.projects[task.project];
  const stage = task.flow?.stages?.find(s => s.name === task.stage);
  const button = (id, kind) => html`<${Button} kind=${kind} keyName=${phone ? '' : actionKeys[id] || ''} disabled=${grey} onClick=${() => onAct(id, task)}>${actLabel(w, id, sit)}<//>`;
  const more = acts.more.filter(id => !['edit', 'dispatch'].includes(id) || phone);
  const direct = phone ? [] : acts.more.filter(id => ['edit', 'dispatch'].includes(id));
  const tabs = !phone && !!output && runs.length > 0;
  const panes = ['overview', 'output', ...(changes ? ['changes'] : [])];
  const shown = tabs && panes.includes(pane) ? pane : 'overview';
  const close = onClose && html`<${Button} kind="quiet" icon="close" label=${t('ui.close')} onClick=${onClose} />`;
  const menu = ids => ids.length > 0 && html`<${Menu} label=${t('do.more')} disabled=${grey} items=${ids.map(id => ({label: actLabel(w, id, sit), kind: id === 'cancel' || id === 'abandon' ? 'danger' : '', onClick: () => onAct(id, task)}))} />`;
  const tabBar = tabs && html`<${Tabs} label=${task.title} value=${shown} onChange=${onPane} idPrefix=${'det-' + task.id}
      tabs=${[{id: 'overview', label: t('det.overview')}, {id: 'output', label: t('det.output'), count: runs.length},
        ...(changes ? [{id: 'changes', label: t('det.changes')}] : [])]} />`;
  if (shown !== 'overview') {
    const meta = [task.id, project?.name, stage && f('gate.stage', stage.name, task.loops || 0)].filter(Boolean).join(' · ');
    return html`<article class="det det-out" aria-label=${task.title}>
      <header class="det-bar">
        <${Status} state=${sitState(sit, st)} label=${sitWord(w, sit, st)} />
        <h2 class="det-bar-title ell" title=${task.title + ' · ' + meta}>${task.title}</h2>
        ${tabBar}
        <span class="det-bar-acts">${acts.primary && button(acts.primary, 'primary')}${menu([...direct, ...more])}${close}</span>
      </header>
      <div class="det-pane" role="tabpanel" id=${'det-' + task.id + '-' + shown + '-pane'}>${shown === 'changes' ? html`<div class="run-scroll">${changes()}</div>` : output()}</div>
    </article>`;
  }
  const top = html`<header class="det-head">
      <div class="det-sit"><${Status} state=${sitState(sit, st)} label=${sitWord(w, sit, st)} />${stage && html`<span class="chip">${f('gate.stage', stage.name, task.loops || 0)}</span>`}${close && html`<span class="det-close">${close}</span>`}</div>
      <h2 class="det-title">${task.title}</h2>
      <div class="det-meta mono">${task.id}${project ? ' · ' + project.name : ''}${task.kind === 'requirement' ? ' · ' + t('det.requirement') : ''}</div>
    </header>
    <div class="det-acts">
      ${acts.primary && button(acts.primary, 'primary')}
      ${direct.map(id => button(id, ''))}
      ${menu(more)}
    </div>
    ${tabBar}`;
  return html`<article class="det" aria-label=${task.title}>
    ${top}
    ${task.source?.pending && html`<div class="det-note"><b>${f('det.sourceNew', task.source.pending.rev)}</b><${Markdown} text=${task.source.pending.text} /></div>`}
    ${task.source?.closed && !task.source.closed_acked && html`<div class="det-note">${t('det.sourceClosed')}</div>`}
    ${task.source?.reopened && html`<div class="det-note">${t('det.sourceReopened')}</div>`}
    ${treeDone && html`<${TreeDone} summary=${treeDone} />`}
    ${paused && html`<div class="det-note">${paused.id === task.id ? f('det.paused', name(paused.paused.by), clock(paused.paused.at)) : f('det.pausedUnder', paused.title)}</div>`}
    ${task.draft && html`<div class="det-note">${f('det.draft', task.draft.plan?.tasks?.length || 0)}</div>`}
    <dl class="facts det-facts">
      ${task.owner && html`<dt>${t('det.owner')}</dt><dd>${name(task.owner)}</dd>`}
      ${(task.approver || task.owner) && html`<dt>${t('det.approver')}</dt><dd>${name(task.approver || task.owner)}</dd>`}
      <dt>${t('det.runsOn')}</dt><dd class="mono">${[def.agent && (def.fromProject.agent ? f('det.projectDefault', def.agent) : def.agent),
        def.machine && (def.fromProject.machine ? f('det.projectDefault', def.machine) : def.machine)].filter(Boolean).join(' @ ') || '—'}</dd>
      ${task.dir && html`<dt>${t('det.dir')}</dt><dd class="mono">${task.dir}</dd>`}
      ${task.branch && html`<dt>${t('det.branch')}</dt><dd class="mono">${task.branch}</dd>`}
      ${runs.length > 0 && html`<dt>${t('det.spent')}</dt><dd>${f('det.spentN', runs.length, tokens(cost.tokens), cost.usd >= 0.005 ? ' · ' + money(cost.usd) : '')}</dd>`}
      ${task.source?.url && html`<dt>${t('det.issue')}</dt><dd><a href=${task.source.url} target="_blank" rel="noopener noreferrer">${task.source.repo}#${task.source.number}</a></dd>`}
    </dl>
    <${Section} title=${t('det.brief')}>
      ${brief === undefined ? html`<p class="t-muted">${t('det.loading')}</p>` : html`<${Markdown} text=${brief} empty=${t('det.noBrief')} />`}
    <//>
    ${task.acceptance?.length > 0 && html`<${Section} title=${t('det.accept')}><ul class="det-list">${task.acceptance.map(a => html`<li>${a}</li>`)}</ul><//>`}
    ${task.flow && html`<${Section} title=${f('det.flow', task.flow.name)}>
      <ol class="det-stages">${task.flow.stages.map(s => {
        const at = s.name === task.stage && !tk.finished(task.status);
        const past = task.status === 'done' || task.flow.stages.findIndex(x => x.name === task.stage) > task.flow.stages.indexOf(s);
        return html`<li class=${cx(at && 'at', past && 'past')}><span class="mono">${s.name}</span><small>${s.gate === 'human' ? t('det.gateHuman') : s.agent || s.role || ''}</small></li>`;
      })}</ol>
      ${stage?.gate === 'human' && !tk.finished(task.status) && html`<p class="t-muted">${f('gate.waits', name(task.approver || task.owner || ''))}</p>`}
    <//>`}
    ${(task.parent || task.after?.length > 0) && html`<${Section} title=${t('det.parent')}>
      ${task.parent && html`<${TaskLink} state=${st} id=${task.parent} onGo=${onGo} />`}
      ${task.after?.length > 0 && html`<div class="det-after"><span class="lbl">${t('det.after')}</span>${task.after.map(id => html`<${TaskLink} state=${st} id=${id} onGo=${onGo} />`)}</div>`}
    <//>`}
    ${kids.length > 0 && html`<${Section} title=${t('det.kids')} count=${prog ? f('det.kidsN', prog.done, prog.of) : undefined}>
      ${tk.inOrder(kids).map(x => html`<${TaskLink} state=${st} id=${x.id} onGo=${onGo} />`)}
    <//>`}
    <${Section} title=${t('det.runs')} count=${runs.length || undefined}>
      ${runs.length ? html`<div class="det-runs">${runs.map(r => html`<${RunRow} key=${r.id} run=${r} now=${now} on=${r.id === run}
        onRun=${onRun && (id => { onRun(id); onPane('output'); })} />`)}</div>` : html`<p class="t-muted">${t('det.noRuns')}</p>`}
    <//>
    ${task.notes?.length > 0 && html`<${Section} title=${t('det.notes')}>
      <ul class="det-notes">${task.notes.slice(-10).reverse().map(n => html`<li><span class="mono t-muted">${clock(n.at)} ${n.stage || ''} ${n.kind}</span> ${n.text}</li>`)}</ul>
    <//>`}
  </article>`;
}
