// taskforms are the task page's forms: a new task (or a subtask, or one like another) and editing one, dispatching,
// moving a task in its tree, reviewing a planner's draft, and accepting work or sending it back. Each is a dialog on a
// desktop; on a phone the long ones take the screen and the short ones rise from the bottom. They collect what the
// write needs and hand it to the page, which sends it.
import {useState, useEffect} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords} from '../ui/base.js';
import {Modal} from '../ui/overlay.js';
import {Button, Tabs, Segmented} from '../ui/controls.js';
import {TextInput, TextArea} from '../ui/input.js';
import {Picker} from '../ui/picker.js';
import {Markdown} from '../ui/markdown.js';
import {Status} from '../ui/status.js';
import * as tk from '../core/tasks.js';
import {sitState} from './task.js';
import './taskwords.js';

// ⚠️ Where a new task's unsent draft is kept in this browser.
export const DRAFT_KEY = 'tend-task-draft';
// ⚠️ How long a typed directory rests before it is checked.
const checkWait = 400;

const lines = s => s.split('\n').map(x => x.trim()).filter(Boolean);

function machineOptions(w, machines, fallback) {
  const out = machines.filter(m => !m.retired).map(m => {
    const on = m.state === 'connected';
    const full = on && m.slots && m.active >= m.slots;
    return {value: m.name, label: m.name, sub: m.os || '', state: on ? 'online' : 'offline',
      note: !on ? w.t('m.offline') : w.f(full ? 'm.full' : 'm.inUse', m.active || 0, m.slots || 0), tone: !on ? 'failed' : full ? 'warning' : ''};
  });
  return [{value: '', label: fallback ? w.f('form.default', fallback) : w.t('form.unset')}, ...out];
}

function agentOptions(w, agents, fallback) {
  return [{value: '', label: fallback ? w.f('form.default', fallback) : w.t('form.unset')},
    ...agents.map(a => ({value: a.name, label: a.name, sub: [a.provider, a.model].filter(Boolean).join(' · '), note: a.machine ? w.f('a.only', a.machine) : ''}))];
}

// taskOptions: the tasks a task may be put under or after (none of its own subtree, none finished).
function taskOptions(state, self) {
  const mine = new Set(self ? [self.id] : []);
  for (let grew = !!self; grew;) {
    grew = false;
    for (const x of Object.values(state.tasks)) if (x.parent && mine.has(x.parent) && !mine.has(x.id)) { mine.add(x.id); grew = true; }
  }
  return tk.rows(state, x => !mine.has(x.id) && !tk.finished(x.status)).map(r => ({value: r.task.id, label: r.task.title, sub: r.task.id, state: sitState(r.sit)}));
}

const readDraft = storage => { try { return JSON.parse(storage?.getItem(DRAFT_KEY) || 'null'); } catch { return null; } };
const writeDraft = (storage, v) => { try { v ? storage?.setItem(DRAFT_KEY, JSON.stringify(v)) : storage?.removeItem?.(DRAFT_KEY); } catch {} };

function Brief({value, onInput, rows}) {
  const {t} = useWords();
  const [tab, setTab] = useState('write');
  return html`<div class="field">
    <div class="brief-head"><span class="brief-label">${t('form.brief')}</span>
      <${Tabs} label=${t('form.brief')} idPrefix="brief" value=${tab} onChange=${setTab} tabs=${[{id: 'write', label: t('form.write')}, {id: 'preview', label: t('form.preview')}]} /></div>
    ${tab === 'write' ? html`<${TextArea} value=${value} onInput=${onInput} rows=${rows} note=${t('form.briefNote')} />`
      : html`<div class="brief-preview"><${Markdown} text=${value} empty=${t('det.noBrief')} /></div>`}
  </div>`;
}

// Dir offers where the task works: its project's checkouts and the directories used lately, or one typed; a typed one
// is checked on the machine when the server can (project.dirs).
function Dir({state, wire, project, machine, value, onInput}) {
  const {t} = useWords();
  const [check, setCheck] = useState('');
  const candidates = tk.dirs(state, {project, machine});
  const can = !!wire?.has?.('project.dirs') && !!machine && !!value;
  useEffect(() => {
    if (!can || candidates.some(c => c.dir === value)) { setCheck(''); return; }
    setCheck('checking');
    let live = true;
    const timer = setTimeout(() => {
      wire.call('project.dirs', {project, machine, path: value}).then(r => live && setCheck(r?.exists === false ? 'missing' : 'ok'), () => live && setCheck('missing'));
    }, checkWait);
    return () => { live = false; clearTimeout(timer); };
  }, [can, value, machine, project]);
  const note = {checking: t('form.dirChecking'), ok: t('form.dirOK'), missing: t('form.dirMissing')}[check] || t('form.dirNote');
  return html`<div class="field">
    <${TextInput} label=${t('form.dir')} value=${value} onInput=${onInput} mono note=${check === 'missing' ? undefined : note} error=${check === 'missing' ? note : undefined} />
    ${candidates.length > 0 && html`<div class="dir-list" role="group" aria-label=${t('form.dir')}>${candidates.map(c => html`<button type="button"
      class=${cx('dir-pick', c.dir === value && 'on')} aria-pressed=${c.dir === value ? 'true' : 'false'} onClick=${() => onInput(c.dir)}>
      <span class="mono ell">${c.dir}</span><span class="dir-kind">${t(c.kind === 'project' ? 'form.dirProject' : 'form.dirRecent')}${c.machine ? ' · ' + c.machine : ''}</span></button>`)}</div>`}
  </div>`;
}

// TaskForm: mode is new, child (of base), copy (of base) or edit (task). onSubmit(how, params) gets how (start,
// backlog, plan or save) and the task.create or task.edit params; storage keeps a new task's draft.
export function TaskForm({store, wire, agents = [], machines = [], mode = 'new', task, base, storage, busy = false, onSubmit, onClose}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  const st = store.state;
  const from = mode === 'edit' ? task : mode === 'copy' ? base : null;
  const [v, setV] = useState(() => {
    if (mode === 'new') {
      const d = readDraft(storage);
      if (d) return d;
    }
    const x = from ? {...from, brief: store.briefOf(from.id) ?? ''} : {};
    return {title: mode === 'edit' ? x.title || '' : '', brief: x.brief || '', project: x.project || (mode === 'child' ? base?.project : '') || '',
      dir: x.dir || (mode === 'child' ? base?.dir : '') || '', machine: x.machine || '', agent: x.agent || '', workflow: mode === 'edit' ? x.workflow || '' : '',
      parent: mode === 'child' ? base.id : mode === 'copy' ? x.parent || '' : '', after: mode === 'copy' ? x.after || [] : [], acceptance: (x.acceptance || []).join('\n')};
  });
  const [tried, setTried] = useState(false);
  const set = (k, val) => setV(o => {
    const next = {...o, [k]: val};
    if (mode === 'new') writeDraft(storage, next);
    return next;
  });
  useEffect(() => {
    if (!from || store.briefOf(from.id) !== undefined) return;
    store.brief(from.id).then(b => setV(o => (o.brief ? o : {...o, brief: b})), () => {});
  }, []);
  const project = st.projects[v.project];
  const d = project?.defaults || {};
  const projects = Object.values(st.projects);
  const params = () => {
    const out = {};
    const put = (k, val) => { if (val !== '' && val !== undefined && !(Array.isArray(val) && !val.length)) out[k] = val; };
    put('title', v.title.trim()); put('brief', v.brief); put('project', v.project); put('dir', v.dir.trim()); put('machine', v.machine); put('agent', v.agent);
    put('workflow', v.workflow); put('acceptance', lines(v.acceptance));
    if (mode !== 'edit') { put('parent', v.parent); put('after', v.after); }
    return out;
  };
  const submit = how => {
    setTried(true);
    if (!v.title.trim()) return;
    const p = params();
    if (mode === 'edit') {
      const was = {title: task.title, brief: store.briefOf(task.id) ?? '', project: task.project || '', dir: task.dir || '', machine: task.machine || '', agent: task.agent || '',
        workflow: task.workflow || '', acceptance: task.acceptance || []};
      const edit = {id: task.id};
      for (const k of Object.keys(was)) {
        const now = p[k] ?? (k === 'acceptance' ? [] : '');
        if (JSON.stringify(now) !== JSON.stringify(was[k])) edit[k] = now;
      }
      onSubmit('save', edit);
      return;
    }
    onSubmit(how, how === 'backlog' || how === 'plan' ? {...p, status: 'backlog'} : p);
  };
  const title = mode === 'edit' ? f('form.edit', task.title) : mode === 'child' ? f('form.child', base.title) : mode === 'copy' ? f('form.copy', base.title) : t('form.new');
  const actions = mode === 'edit'
    ? [{label: t('home.cancel'), onClick: onClose}, {label: t('form.save'), kind: 'primary', keyName: 'Mod+Enter', disabled: busy, onClick: () => submit('save')}]
    : [{label: t('form.createBacklog'), disabled: busy, onClick: () => submit('backlog')}, {label: t('form.createPlan'), disabled: busy, onClick: () => submit('plan')},
      {label: t('form.createStart'), kind: 'primary', keyName: 'Mod+Enter', disabled: busy, onClick: () => submit('start')}];
  const workflows = [{value: '', label: d.workflow ? f('form.default', d.workflow) : t('form.unset')}, {value: 'none', label: t('form.workflowNone')},
    ...Object.keys(project?.workflows || {}).map(n => ({value: n, label: n}))];
  if (v.workflow && !workflows.some(o => o.value === v.workflow)) workflows.push({value: v.workflow, label: v.workflow});
  const others = taskOptions(st, mode === 'edit' ? task : null);
  return html`<${Modal} title=${title} onClose=${onClose} full actions=${actions}>
    <div class=${cx('form', !phone && 'form-2')}>
      <div class="form-main">
        <${TextInput} label=${t('form.title')} value=${v.title} onInput=${x => set('title', x)} autoFocus error=${tried && !v.title.trim() ? t('form.titleNeeded') : undefined} />
        <${Brief} value=${v.brief} onInput=${x => set('brief', x)} rows=${phone ? 12 : 8} />
        <${TextArea} label=${t('form.accept')} value=${v.acceptance} onInput=${x => set('acceptance', x)} rows=${3} note=${t('form.acceptNote')} />
        ${mode === 'new' && html`<p class="field-note">${t('form.draftNote')}</p>`}
      </div>
      <div class="form-side">
        ${projects.length > 0 && html`<${Picker} label=${t('form.project')} value=${v.project} onChange=${x => set('project', x)}
          options=${[{value: '', label: t('form.noProject')}, ...projects.map(p => ({value: p.id, label: p.name, sub: p.id}))]} />`}
        <${Picker} label=${t('form.machine')} value=${v.machine} onChange=${x => set('machine', x)} options=${machineOptions(w, machines, d.machine)} />
        <${Picker} label=${t('form.agent')} value=${v.agent} onChange=${x => set('agent', x)} options=${agentOptions(w, agents, d.agent || d.roles?.implement)} />
        <${Dir} state=${st} wire=${wire} project=${v.project} machine=${v.machine || d.machine || ''} value=${v.dir} onInput=${x => set('dir', x)} />
        <${Picker} label=${t('form.workflow')} value=${v.workflow} onChange=${x => set('workflow', x)} options=${workflows} />
        ${mode !== 'edit' && html`
          <${Picker} label=${t('form.parent')} value=${v.parent} onChange=${x => set('parent', x)} options=${[{value: '', label: t('form.top')}, ...others]} />
          <${Picker} label=${t('form.after')} multi value=${v.after} onChange=${x => set('after', x)} options=${others.filter(o => o.value !== v.parent)} />`}
      </div>
    </div>
  <//>`;
}

// ChoiceRows is a phone's list of machines or agents to tap, the picked one marked.
function ChoiceRows({label, options, value, onChange}) {
  return html`<div class="field"><span class="brief-label">${label}</span><div class="choice-rows" role="radiogroup" aria-label=${label}>
    ${options.map(o => html`<button type="button" role="radio" aria-checked=${o.value === value ? 'true' : 'false'} class=${cx('crow', o.value === value && 'on')} onClick=${() => onChange(o.value)}>
      ${o.state && html`<${Status} state=${o.state} />`}<span class="crow-text"><span>${o.label}</span>${o.sub && html`<small>${o.sub}</small>`}</span>
      ${o.note && html`<span class=${cx('crow-note', o.tone && 't-' + o.tone)}>${o.note}</span>`}
    </button>`)}
  </div></div>`;
}

// Dispatch picks the machine and agent a task runs with, says what they meet (the page's rules, then the
// coordinator's preview) and queues it, or holds the task back.
export function Dispatch({store, wire, task, agents = [], machines = [], busy = false, onDispatch, onLater, onClose}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  const def = tk.defaults(store.state, task);
  const [machine, setMachine] = useState(def.machine);
  const [agent, setAgent] = useState(def.agent);
  const [pv, setPv] = useState(null);
  const m = machines.find(x => x.name === machine), a = agents.find(x => x.name === agent);
  const adv = tk.advice(store.state, task, m, a);
  useEffect(() => {
    setPv(null);
    if (adv.block || !wire?.has?.('run.preview')) return;
    let live = true;
    setPv({loading: true});
    wire.call('run.preview', {task: task.id, machine, agent}).then(p => live && setPv(p), e => live && setPv({error: e.code || String(e.message || e)}));
    return () => { live = false; };
  }, [machine, agent, adv.block]);
  const blocked = !!adv.block || !!pv?.blockers?.length || !machine || !agent;
  const whyText = x => t('pv.' + x.code) + (x.detail ? ' · ' + x.detail : '');
  const machineOpts = machineOptions(w, machines, '').filter(o => o.value);
  const agentOpts = agentOptions(w, agents, '').filter(o => o.value);
  const pick = phone
    ? html`<${ChoiceRows} label=${t('form.machine')} options=${machineOpts} value=${machine} onChange=${setMachine} />
      <${ChoiceRows} label=${t('form.agent')} options=${agentOpts} value=${agent} onChange=${setAgent} />`
    : html`<div class="form-row"><${Picker} label=${t('form.machine')} value=${machine} onChange=${setMachine} options=${machineOpts} />
      <${Picker} label=${t('form.agent')} value=${agent} onChange=${setAgent} options=${agentOpts} /></div>`;
  const actions = [
    ...(task.status === 'todo' && onLater ? [{label: t('disp.later'), disabled: busy, onClick: onLater}] : []),
    {label: t('disp.go'), kind: 'primary', keyName: 'Mod+Enter', disabled: busy || blocked, onClick: () => onDispatch(machine, agent)},
  ];
  return html`<${Modal} title=${f('disp.title', task.title)} onClose=${onClose} actions=${actions}>
    ${pick}
    <div class="advice" aria-live="polite">
      ${adv.block && html`<p class="notice notice-bad">${f('adv.' + adv.block, adv.detail || '')}</p>`}
      ${adv.note && html`<p class="notice">${f('adv.' + adv.note, adv.detail || '')}</p>`}
      ${pv?.loading && html`<p class="t-muted">${t('disp.checking')}</p>`}
      ${pv?.error && html`<p class="notice notice-bad">${f('form.failed', pv.error)}</p>`}
      ${pv?.check && html`<p class="t-muted mono">${f('disp.cli', pv.provider || '', pv.check.version || '')}</p>`}
      ${(pv?.blockers || []).map(x => html`<p class="notice notice-bad">${whyText(x)}</p>`)}
      ${(pv?.notes || []).map(x => html`<p class="notice">${whyText(x)}</p>`)}
    </div>
  <//>`;
}

// Move puts a task under another parent, or after other tasks.
export function Move({store, task, busy = false, onMove, onClose}) {
  const {t, f} = useWords();
  const [parent, setParent] = useState(task.parent || '');
  const [after, setAfter] = useState(task.after || []);
  const others = taskOptions(store.state, task);
  return html`<${Modal} title=${f('move.title', task.title)} onClose=${onClose}
    actions=${[{label: t('home.cancel'), onClick: onClose}, {label: t('form.save'), kind: 'primary', keyName: 'Mod+Enter', disabled: busy, onClick: () => onMove(parent, after)}]}>
    <${Picker} label=${t('form.parent')} value=${parent} onChange=${setParent} options=${[{value: '', label: t('form.top')}, ...others]} />
    <${Picker} label=${t('form.after')} multi value=${after} onChange=${setAfter} options=${others.filter(o => o.value !== parent)} />
  <//>`;
}

function PlanItem({plan, item, onEdit, onRemove}) {
  const {t} = useWords();
  const tops = plan.tasks.filter(x => !x.parent && x.key !== item.key);
  const others = plan.tasks.filter(x => x.key !== item.key);
  const edit = fields => onEdit(item.key, fields);
  return html`<div class="plan-item">
    <${TextInput} label=${t('form.title')} value=${item.title} onInput=${x => edit({title: x})} />
    <div class="form-row">
      <${TextInput} label=${t('plan.key')} value=${item.key} mono onInput=${x => edit({key: x})} />
      <div class="field"><span class="brief-label">${t('plan.size')}</span><${Segmented} label=${t('plan.size')} value=${item.size || ''}
        onChange=${x => edit({size: x})} options=${['', 'S', 'M', 'L'].map(s => ({value: s, label: s || '—'}))} /></div>
    </div>
    <${Picker} label=${t('plan.partOf')} value=${item.parent || ''} onChange=${x => edit({parent: x})}
      options=${[{value: '', label: t('form.top')}, ...tops.map(x => ({value: x.key, label: x.title, sub: x.key}))]} />
    <${Picker} label=${t('form.after')} multi value=${item.after || []} onChange=${x => edit({after: x})} options=${others.map(x => ({value: x.key, label: x.title, sub: x.key}))} />
    <${Brief} value=${item.brief || ''} onInput=${x => edit({brief: x})} rows=${5} />
    <${TextArea} label=${t('form.accept')} value=${(item.acceptance || []).join('\n')} onInput=${x => edit({acceptance: lines(x)})} rows=${3} note=${t('form.acceptNote')} />
    <div class="form-row">
      <${TextInput} label=${t('plan.role')} value=${item.role_hint || ''} onInput=${x => edit({role_hint: x})} />
      <${TextInput} label=${t('plan.machineHint')} value=${item.machine_hint || ''} onInput=${x => edit({machine_hint: x})} />
    </div>
    <div><${Button} kind="quiet danger" onClick=${() => onRemove(item.key)}>${t('plan.remove')}<//></div>
  </div>`;
}

// PlanReview shows a planner's draft of subtasks to change, save, make into tasks or drop, and its questions with a
// way to answer them in the planner's session.
export function PlanReview({store, task, busy = false, onSave, onApply, onDiscard, onReply, onClose}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  const [plan, setPlan] = useState(() => structuredClone(task.draft?.plan || {tasks: []}));
  const [dirty, setDirty] = useState(false);
  const [at, setAt] = useState(() => (phone ? '' : task.draft?.plan?.tasks?.[0]?.key || ''));
  const [note, setNote] = useState('');
  const change = next => { setPlan(next); setDirty(true); };
  const errors = tk.planCheck(plan);
  const errorsOf = key => errors.filter(e => e.key === key).map(e => f('plan.err.' + e.code, e.detail || ''));
  const whole = errorsOf('');
  const item = plan.tasks.find(x => x.key === at);
  const edit = (key, fields) => { change(tk.planEdit(plan, key, fields)); if (fields.key !== undefined) setAt(fields.key); };
  const remove = key => { change(tk.planRemove(plan, key)); setAt(''); };
  const add = () => { const next = tk.planAdd(plan, {title: ''}); change(next); setAt(next.tasks.at(-1).key); };
  const run = task.draft?.run;
  const list = html`<ul class="plan-list" aria-label=${t('plan.items')}>
    ${tk.planRows(plan).map(r => html`<li key=${r.item.key}><button type="button" class=${cx('plan-row', 'depth-' + r.depth, r.item.key === at && 'sel', errorsOf(r.item.key).length && 'bad')}
      aria-current=${r.item.key === at ? 'true' : undefined} onClick=${() => setAt(r.item.key)}>
      <span class="plan-title">${r.item.title || '—'}</span>
      <span class="plan-sub mono">${r.item.key}${r.item.size ? ' · ' + r.item.size : ''}${r.item.after?.length ? ' · ← ' + r.item.after.join(', ') : ''}</span>
      ${errorsOf(r.item.key).map(e => html`<span class="plan-err t-failed">${e}</span>`)}
    </button></li>`)}
  </ul>`;
  const questions = html`${plan.questions?.length > 0 && html`<div class="det-note"><b>${t('plan.questions')}</b><ul class="det-list">${plan.questions.map(q => html`<li><${Markdown} text=${q} /></li>`)}</ul></div>`}
    ${run && onReply && html`<div class="plan-reply"><${TextArea} label=${t('plan.reply')} value=${note} onInput=${setNote} rows=${2} note=${f('plan.replyNote', run)} />
      <${Button} disabled=${busy || !note.trim()} onClick=${() => { onReply(note.trim()); setNote(''); }}>${t('plan.send')}<//></div>`}`;
  const actions = [
    {label: t('plan.discard'), kind: 'danger', disabled: busy, onClick: onDiscard},
    ...(dirty ? [{label: t('plan.save'), kind: 'primary', keyName: 'Mod+Enter', disabled: busy || errors.length > 0, onClick: () => { onSave(plan); setDirty(false); }}]
      : [{label: t('plan.apply'), kind: 'primary', keyName: 'Mod+Enter', disabled: busy || errors.length > 0, onClick: onApply}]),
  ];
  const body = html`
    ${questions}
    ${whole.map(e => html`<p class="notice notice-bad">${e}</p>`)}
    ${dirty && html`<p class="t-muted">${t('plan.saveFirst')}</p>`}
    <div class=${cx('plan', !phone && 'plan-2')}>
      <div class="plan-side">${list}<${Button} kind="quiet" icon="plus" onClick=${add}>${t('plan.add')}<//></div>
      ${!phone && html`<div class="plan-edit">${item ? html`<${PlanItem} plan=${plan} item=${item} onEdit=${edit} onRemove=${remove} />` : html`<p class="empty">${t('plan.editItem')}</p>`}</div>`}
    </div>`;
  return html`<${Modal} title=${f('plan.title', task.title)} onClose=${onClose} full actions=${actions}>
    ${body}
    ${phone && item && html`<${Modal} title=${item.title || item.key} onClose=${() => setAt('')} full actions=${[{label: t('plan.back'), kind: 'primary', onClick: () => setAt('')}]}>
      ${errorsOf(item.key).map(e => html`<p class="notice notice-bad">${e}</p>`)}
      <${PlanItem} plan=${plan} item=${item} onEdit=${edit} onRemove=${remove} />
    <//>`}
  <//>`;
}

// Gate sends a workflow task at its human gate back with what to change; for a task without one (reply), it sends what
// to change to its last run's session.
export function Gate({task, reply = null, busy = false, onBack, onClose}) {
  const {t, f} = useWords();
  const phone = usePhone();
  const [notes, setNotes] = useState('');
  const [tried, setTried] = useState(false);
  const back = () => { setTried(true); if (notes.trim()) onBack(notes.trim()); };
  const quick = [{label: t('gate.back'), kind: 'primary', keyName: 'Mod+Enter', disabled: busy, onClick: back}];
  const field = html`<${TextArea} label=${t('gate.notes')} value=${notes} onInput=${setNotes} rows=${phone ? 4 : 5}
    note=${reply ? f('gate.replyNote', reply) : t('gate.notesNote')} error=${tried && !notes.trim() ? t('gate.needNotes') : undefined} />`;
  if (phone) {
    return html`<${Modal} title=${f('gate.title', task.title)} onClose=${onClose}>
      <div class="gate-quick">${quick.map(a => html`<${Button} kind=${a.kind} wide disabled=${a.disabled} onClick=${a.onClick}>${a.label}<//>`)}</div>
      ${field}
    <//>`;
  }
  return html`<${Modal} title=${f('gate.title', task.title)} onClose=${onClose} actions=${[{label: t('home.cancel'), onClick: onClose}, ...quick]}>${field}<//>`;
}
