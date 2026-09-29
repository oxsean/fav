// Task is one task's page: where it stands and what it asks for, its brief and acceptance, where it sits in its tree,
// its workflow's stages and workpad, and its runs. On a desktop it is the pane beside the list (or a drawer beside the
// board); on a phone it takes the screen, with the previous and next task of the list at its top. What each button
// does is the task page's (onAct): this only says which ones there are.
import {useEffect} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords} from '../ui/base.js';
import {Button} from '../ui/controls.js';
import {Status} from '../ui/status.js';
import {Markdown} from '../ui/markdown.js';
import {Menu} from '../ui/menu.js';
import * as tk from '../core/tasks.js';
import * as sel from '../core/select.js';
import {situation} from '../core/fold.js';
import {tokens, money, duration, clock, usageTokens} from '../core/format.js';
import {why} from './words.js';
import './taskwords.js';

// ⚠️ The keys of the task page's actions, from the action table.
export const actionKeys = {dispatch: 'd', edit: 'e', stop: 'x', done: 'Shift+D'};

// sitState is how a situation is drawn by Status; sitWord what it says.
export function sitState(sit) {
  if (sit.kind === 'waiting') return ['asked', 'permission', 'unknown'].includes(sit.reason) ? sit.reason
    : ['failed', 'stopped', 'canceled', 'abandoned', 'merge_conflict', 'setup_failed', 'work', 'blocked', 'max_loops', 'budget'].includes(sit.reason) ? 'failed' : 'waiting';
  return {running: 'running', queued: 'queued', backlog: 'backlog', done: 'done', canceled: 'canceled'}[sit.kind] || 'unknown';
}

export function sitWord(w, sit) {
  if (sit.kind === 'waiting' || sit.kind === 'queued') return why(w, sit.reason);
  return w.t('status.' + ({backlog: 'backlog', done: 'done', canceled: 'canceled', running: 'running'}[sit.kind] || 'unknown'));
}

export const who = x => [x?.agent, x?.machine].filter(Boolean).join(' @ ');

// actLabel is what an action's button says for this task.
export const actLabel = (w, id, sit) => w.t(id === 'keep' && sit.reason === 'source_closed' ? 'do.keepGoing' : 'do.' + id);

function Section({title, count, children}) {
  return html`<section class="det-sec"><h3 class="det-h">${title}${count !== undefined && html` <span class="mono count">${count}</span>`}</h3>${children}</section>`;
}

function TaskLink({state, id, onGo}) {
  const x = state.tasks[id];
  if (!x) return html`<span class="mono t-muted">${id}</span>`;
  return html`<button type="button" class="det-link" onClick=${() => onGo(id)}><${Status} state=${sitState(situation(state, x))} /><span class="ell">${x.title}</span><span class="mono t-muted">${id}</span></button>`;
}

function RunRow({run, now}) {
  const w = useWords();
  const began = Date.parse(run.started_at || run.queued_at), ended = run.ended_at ? Date.parse(run.ended_at) : now;
  const said = sel.doing(run) || run.detail || run.reason || '';
  return html`<div class="det-run">
    <${Status} state=${run.state} />
    <span class="det-run-main"><span class="mono">${run.id}</span> <span class="t-muted">${who(run)}${run.stage ? ' · ' + run.stage : ''}</span>
      ${said && html`<span class=${cx('det-run-said', 'ell', /^\$ |^go |^npm /.test(said) && 'mono')}>${said}</span>`}
      ${run.verdict && html`<span class="det-run-said">${w.f('det.verdict', run.verdict.verdict)}${run.verdict.summary ? ' · ' + run.verdict.summary : ''}</span>`}</span>
    <span class="det-run-time mono">${Number.isNaN(began) ? '' : duration(ended - began)}</span>
    <span class="det-run-cost mono">${run.usage?.cost_usd >= 0.005 ? money(run.usage.cost_usd) : run.usage ? tokens(usageTokens(run.usage)) : ''}</span>
  </div>`;
}

// Task: acts lists what can be done (core/tasks.js actionsFor); busy is a write about it that is out.
export function Task({store, task, now, busy = false, onAct, onGo}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  const st = store.state;
  const brief = task ? store.briefOf(task.id) : undefined;
  useEffect(() => { if (task && brief === undefined) store.brief(task.id).catch(() => {}); }, [task?.id, brief === undefined]);
  if (!task) return html`<p class="empty">${t('det.gone')}</p>`;
  const sit = situation(st, task);
  const acts = tk.actionsFor(st, task);
  const kids = tk.childrenOf(st, task.id);
  const prog = tk.progress(st, task.id);
  const runs = tk.runsOf(st, task.id);
  const cost = tk.spent(st, task.id);
  const def = tk.defaults(st, task);
  const project = st.projects[task.project];
  const stage = task.flow?.stages?.find(s => s.name === task.stage);
  const button = (id, kind) => html`<${Button} kind=${kind} keyName=${phone ? '' : actionKeys[id] || ''} disabled=${busy} onClick=${() => onAct(id, task)}>${actLabel(w, id, sit)}<//>`;
  const more = acts.more.filter(id => !['edit', 'dispatch'].includes(id) || phone);
  const direct = phone ? [] : acts.more.filter(id => ['edit', 'dispatch'].includes(id));
  return html`<article class="det" aria-label=${task.title}>
    <header class="det-head">
      <div class="det-sit"><${Status} state=${sitState(sit)} label=${sitWord(w, sit)} />${stage && html`<span class="chip">${f('gate.stage', stage.name, task.loops || 0)}</span>`}</div>
      <h2 class="det-title">${task.title}</h2>
      <div class="det-meta mono">${task.id}${project ? ' · ' + project.name : ''}${task.kind === 'requirement' ? ' · ' + t('det.requirement') : ''}</div>
    </header>
    <div class="det-acts">
      ${acts.primary && button(acts.primary, 'primary')}
      ${direct.map(id => button(id, ''))}
      ${more.length > 0 && html`<${Menu} label=${t('do.more')} items=${more.map(id => ({label: actLabel(w, id, sit), kind: id === 'cancel' ? 'danger' : '', onClick: () => onAct(id, task)}))} />`}
    </div>
    ${task.source?.pending && html`<div class="det-note"><b>${f('det.sourceNew', task.source.pending.rev)}</b><${Markdown} text=${task.source.pending.text} /></div>`}
    ${task.source?.closed && !task.source.closed_acked && html`<div class="det-note">${t('det.sourceClosed')}</div>`}
    ${task.draft && html`<div class="det-note">${f('det.draft', task.draft.plan?.tasks?.length || 0)}</div>`}
    <dl class="facts det-facts">
      ${task.owner && html`<dt>${t('det.owner')}</dt><dd class="mono">${task.owner}</dd>`}
      ${(task.approver || task.owner) && html`<dt>${t('det.approver')}</dt><dd class="mono">${task.approver || task.owner}</dd>`}
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
      ${stage?.gate === 'human' && !tk.finished(task.status) && html`<p class="t-muted">${f('gate.waits', task.approver || task.owner || '')}</p>`}
    <//>`}
    ${(task.parent || task.after?.length > 0) && html`<${Section} title=${t('det.parent')}>
      ${task.parent && html`<${TaskLink} state=${st} id=${task.parent} onGo=${onGo} />`}
      ${task.after?.length > 0 && html`<div class="det-after"><span class="lbl">${t('det.after')}</span>${task.after.map(id => html`<${TaskLink} state=${st} id=${id} onGo=${onGo} />`)}</div>`}
    <//>`}
    ${kids.length > 0 && html`<${Section} title=${t('det.kids')} count=${prog ? f('det.kidsN', prog.done, prog.of) : undefined}>
      ${tk.inOrder(kids).map(x => html`<${TaskLink} state=${st} id=${x.id} onGo=${onGo} />`)}
    <//>`}
    <${Section} title=${t('det.runs')} count=${runs.length || undefined}>
      ${runs.length ? html`<div class="det-runs">${runs.map(r => html`<${RunRow} key=${r.id} run=${r} now=${now} />`)}</div>` : html`<p class="t-muted">${t('det.noRuns')}</p>`}
    <//>
    ${task.notes?.length > 0 && html`<${Section} title=${t('det.notes')}>
      <ul class="det-notes">${task.notes.slice(-10).reverse().map(n => html`<li><span class="mono t-muted">${clock(n.at)} ${n.stage || ''} ${n.kind}</span> ${n.text}</li>`)}</ul>
    <//>`}
  </article>`;
}
