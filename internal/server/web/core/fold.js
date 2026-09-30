// fold applies journal envelopes to the state the page holds, in place, as task.State.Apply does in Go (fold_test.go
// runs both on the same envelopes). An event about something the page does not hold throws: the page takes the state
// again.
const openStates = new Set(['queued', 'starting', 'running', 'unknown']);
const rank = {queued: 0, starting: 1, running: 2, unknown: 2, exited: 3, stopped: 3, failed: 3, canceled: 3, abandoned: 3};
const need = (map, id, what) => {
  const x = map[id];
  if (!x) throw new Error(`no ${what} ${id}`);
  return x;
};

function observe(r, o) {
  if (!openStates.has(r.state) && r.state !== 'abandoned') return;
  if ((o.node_rev || 0) < (r.node_rev || 0)) return;
  if (r.state === 'abandoned') {
    if (rank[o.state] < 3) return;
  } else if (rank[o.state] < rank[r.state]) return;
  r.state = o.state;
  r.node_rev = o.node_rev || 0;
  if (o.exit_code !== undefined && o.exit_code !== null) r.exit_code = o.exit_code;
  if (o.reason) r.reason = o.reason;
  if (o.detail) r.detail = o.detail;
  if ((o.node_rev || 0) > 0) {
    r.attention = o.attention; r.ask = o.ask; r.note = o.note; r.last = o.last; r.usage = o.usage;
    r.stream = o.stream; r.requests = o.requests; r.caps = o.caps; r.doing = o.doing; r.turn = o.turn; r.verdict = o.verdict; r.checked = o.check; r.worked = o.work; r.plan = o.plan;
    const node = o.sends || [];
    r.sends = [...node, ...(r.sends || []).filter(m => !node.some(x => x.id === m.id))];
    r.answers = (r.answers || []).filter(a => (r.requests || []).some(q => q.id === a.request));
  }
  if (o.session) { r.provider = o.provider; r.session = o.session; }
  if (o.pane) r.pane = o.pane;
  if (o.started_at) r.started_at = o.started_at;
  if (o.ended_at) r.ended_at = o.ended_at;
  if (!openStates.has(r.state)) {
    r.requests = undefined; r.answers = undefined;
    const carried = m => (m.mode === 'after' || m.mode === 'interrupt') && !!r.session; // a continuation takes it
    r.sends = (r.sends || []).map(m => m.state === 'queued' && !carried(m) ? {...m, state: 'failed'} : m);
  }
}

function applyEvent(s, e, at, seq) {
  const d = e.data;
  switch (e.type) {
    case 'task_created':
      s.tasks[d.id] = {...d, rev: 1, created_at: at, updated_at: at};
      if (d.flow) s.tasks[d.id].stage_seq = seq;
      break;
    case 'task_edited': {
      const t = need(s.tasks, d.id, 'task');
      for (const k of ['title', 'brief', 'dir', 'machine', 'agent', 'project', 'owner', 'approver', 'kind', 'acceptance', 'tags']) if (d[k] != null) t[k] = d[k];
      if (d.workflow != null) {
        t.workflow = d.workflow || undefined; t.flow = d.flow; t.loops = undefined; t.stage_seq = seq;
        t.stage = d.flow && d.flow.stages && d.flow.stages.length ? d.flow.stages[0].name : undefined;
      }
      t.held = undefined; t.rev = (t.rev || 0) + 1; t.updated_at = at;
      break;
    }
    case 'task_staged': {
      const t = need(s.tasks, d.id, 'task');
      if (!stageOf(t.flow, d.stage)) throw new Error(`no stage ${d.stage} of task ${d.id}`);
      t.stage = d.stage; t.loops = d.loops || undefined; t.stage_seq = seq; t.rev = (t.rev || 0) + 1; t.updated_at = at;
      const mark = {at, stage: d.stage}; if (d.loops) mark.loops = d.loops; if (d.back) mark.back = true;
      t.stages = [...(t.stages || []), mark].slice(-50);
      break;
    }
    case 'task_noted': {
      const t = need(s.tasks, d.id, 'task');
      t.notes = [...(t.notes || []), {...d.note, at}].slice(-200);
      t.rev = (t.rev || 0) + 1; t.updated_at = at;
      break;
    }
    case 'task_sourced': {
      const t = need(s.tasks, d.id, 'task'), src = t.source;
      if (!src) throw new Error(`no sourced task ${d.id}`);
      if (d.digest && d.digest !== src.seen) {
        src.seen_rev = (src.seen_rev || 0) + 1; src.seen = d.digest;
        src.pending = d.digest === src.digest ? undefined : {rev: src.seen_rev, digest: d.digest, title: d.title || '', text: d.text || ''};
      }
      if (!!d.closed !== !!src.closed) { src.closed = !!d.closed; src.closed_acked = false; }
      if (d.repo) src.repo = d.repo;
      if (d.url) src.url = d.url;
      src.fetched_at = at; t.rev = (t.rev || 0) + 1; t.updated_at = at;
      break;
    }
    case 'task_linked': {
      const t = need(s.tasks, d.id, 'task');
      if (d.issue) t.issue = d.issue;
      if (d.pr) t.pr = d.pr;
      t.rev = (t.rev || 0) + 1; t.updated_at = at;
      break;
    }
    case 'task_source_acked': {
      const t = need(s.tasks, d.id, 'task'), src = t.source;
      if (!sourceWaits(t)) throw new Error(`nothing to acknowledge on ${d.id}`);
      if (src.pending) {
        if (d.accept) { t.title = src.pending.title; t.brief = src.pending.text; src.rev = src.pending.rev; src.digest = src.pending.digest; }
        src.pending = undefined;
      } else src.closed_acked = true;
      t.rev = (t.rev || 0) + 1; t.updated_at = at;
      break;
    }
    case 'task_status_set': {
      const t = need(s.tasks, d.id, 'task');
      if (finished(t.status) && !finished(d.status)) { t.auto = undefined; t.start_seq = seq; t.merged = undefined; } // reopened: what came before no longer stands
      t.status = d.status; t.updated_at = at; t.rev = (t.rev || 0) + 1;
      break;
    }
    case 'task_restored': { // an undo: the task as it stood before the undone command, field for field
      const t = need(s.tasks, d.id, 'task');
      for (const k of ['status', 'auto', 'start_seq', 'merged', 'stage', 'loops', 'stage_seq', 'stages', 'parent', 'after', 'held']) t[k] = d[k];
      t.updated_at = at; t.rev = (t.rev || 0) + 1;
      break;
    }
    case 'run_queued':
      s.runs[d.id] = {...d, state: 'queued', want: 'run', queued_at: at, seq};
      queuedWork(s, s.runs[d.id]);
      if (s.runs[d.parent] && (d.takes || []).length) { // what it carries went out with it
        const p = s.runs[d.parent];
        p.sends = (p.sends || []).map(m => d.takes.includes(m.id) ? {...m, state: 'sent'} : m);
      }
      break;
    case 'plan_drafted': {
      const t = need(s.tasks, d.id, 'task');
      t.draft = d.plan ? {plan: d.plan, by: d.by, source_rev: t.source ? t.source.rev : undefined} : undefined;
      t.rev = (t.rev || 0) + 1; t.updated_at = at;
      break;
    }
    case 'plan_applied': {
      const t = need(s.tasks, d.id, 'task');
      delete t.draft;
      t.rev = (t.rev || 0) + 1; t.updated_at = at;
      break;
    }
    case 'run_starting': {
      const r = need(s.runs, d.id, 'run');
      if (r.state === 'queued') { r.state = 'starting'; if (d.dir) r.dir = d.dir; }
      break;
    }
    case 'run_observed': {
      const r = need(s.runs, d.id, 'run'), open = openStates.has(r.state);
      observe(r, d);
      if (open && !openStates.has(r.state)) { worked(s, r); drafted(s, r); }
      break;
    }
    case 'run_stop_requested':
      need(s.runs, d.id, 'run').want = 'stop';
      break;
    case 'run_canceled': {
      const r = need(s.runs, d.id, 'run');
      if (r.state === 'queued') { r.state = 'canceled'; r.ended_at = at; if (d.reason) r.reason = d.reason; }
      break;
    }
    case 'run_abandoned': {
      const r = need(s.runs, d.id, 'run');
      if (openStates.has(r.state)) { r.state = 'abandoned'; r.want = 'stop'; r.ended_at = at; }
      break;
    }
    case 'run_answered': {
      const r = need(s.runs, d.id, 'run');
      r.answers = [...(r.answers || []).filter(a => a.request !== d.answer.request), d.answer]; // a later answer replaces
      break;
    }
    case 'run_sent': {
      const r = need(s.runs, d.id, 'run');
      if (!(r.sends || []).some(m => m.id === d.send.id)) r.sends = [...(r.sends || []), d.send];
      else r.sends = r.sends.map(m => m.id === d.send.id ? {...m, state: d.send.state} : m); // one the coordinator kept failed
      break;
    }
    case 'run_interrupt_requested': {
      const r = need(s.runs, d.id, 'run');
      if (openStates.has(r.state)) r.interrupt = d;
      break;
    }
    case 'task_moved': {
      const t = need(s.tasks, d.id, 'task');
      if (d.parent != null) t.parent = d.parent;
      if (d.after != null) t.after = d.after;
      t.held = undefined; t.rev = (t.rev || 0) + 1; t.updated_at = at;
      break;
    }
    case 'task_started':
      for (const id of d.ids || []) {
        const t = need(s.tasks, id, 'task');
        if (t.status === 'backlog') t.status = 'todo';
        t.auto = true; t.start_seq = seq; t.held = undefined; t.rev = (t.rev || 0) + 1; t.updated_at = at;
      }
      break;
    case 'task_held': {
      const t = need(s.tasks, d.id, 'task');
      t.held = d.reason + (d.detail ? ': ' + d.detail : ''); t.updated_at = at;
      break;
    }
    case 'project_created':
      s.projects[d.id] = {...d, rev: 1, created_at: at, updated_at: at};
      break;
    case 'project_edited': {
      const p = need(s.projects, d.id, 'project');
      for (const k of ['name', 'owner', 'repos', 'links', 'context', 'defaults', 'hooks', 'fetch', 'workflows']) if (d[k] != null) p[k] = d[k];
      p.rev = (p.rev || 0) + 1; p.updated_at = at;
      break;
    }
    case 'member_set': {
      const p = need(s.projects, d.project, 'project');
      p.members = {...(p.members || {})};
      if (d.role) p.members[d.user] = d.role; else delete p.members[d.user];
      p.rev = (p.rev || 0) + 1; p.updated_at = at;
      break;
    }
    case 'agentdef_saved': {
      const old = s.agent_defs[d.name];
      s.agent_defs[d.name] = {...d, rev: (old ? old.rev || 0 : 0) + 1, updated_at: at};
      break;
    }
    case 'agentdef_removed':
      need(s.agent_defs, d.name, 'agent');
      delete s.agent_defs[d.name];
      break;
    case 'agentdef_shared': {
      const x = need(s.agent_defs, d.name, 'agent');
      x.share = d.share; x.updated_at = at; x.rev = (x.rev || 0) + 1;
      break;
    }
    case 'machine_shared':
      if (!(d.users || []).length && !(d.projects || []).length) delete s.shares[d.machine];
      else s.shares[d.machine] = d;
      break;
    case 'machine_drained':
      if (d.on) s.drains[d.machine] = {machine: d.machine, ...(d.by ? {by: d.by} : {}), at};
      else delete s.drains[d.machine];
      break;
    default: // an event this page does not know yet: only the seq moves on
  }
}

// parts names the tables of the state each event changes, for the store's versions; an event missing here changes
// nothing but the seq.
const parts = {
  task_created: ['tasks'], task_edited: ['tasks'], task_staged: ['tasks'], task_noted: ['tasks'], task_sourced: ['tasks'],
  task_linked: ['tasks'], task_source_acked: ['tasks'], task_status_set: ['tasks'], task_restored: ['tasks'], task_moved: ['tasks'], task_started: ['tasks'],
  task_held: ['tasks'], plan_drafted: ['tasks'], plan_applied: ['tasks'],
  run_queued: ['runs', 'tasks'], run_observed: ['runs', 'tasks'], run_starting: ['runs'], run_stop_requested: ['runs'],
  run_canceled: ['runs'], run_abandoned: ['runs'], run_answered: ['runs'], run_sent: ['runs'], run_interrupt_requested: ['runs'],
  project_created: ['projects'], project_edited: ['projects'], member_set: ['projects'],
  agentdef_saved: ['agent_defs'], agentdef_removed: ['agent_defs'], agentdef_shared: ['agent_defs'],
  machine_shared: ['shares'], machine_drained: ['drains', 'tasks'],
};

// apply folds env into s (which it changes) and returns s.
function apply(s, env) {
  s.tasks ||= {}; s.runs ||= {}; s.projects ||= {}; s.shares ||= {}; s.drains ||= {}; s.agent_defs ||= {};
  for (const e of env.events || []) applyEvent(s, e, env.at, env.seq);
  s.seq = env.seq;
  return s;
}

const stageOf = (flow, name) => (flow?.stages || []).find(x => x.name === name);
const nextStage = (flow, name) => { const i = (flow?.stages || []).findIndex(x => x.name === name); return i >= 0 && i + 1 < flow.stages.length ? flow.stages[i + 1].name : ''; };

// spent is what t's runs used: cost estimates, and the minutes of those that ended (task.State.Spent).
function spent(s, t) {
  let usd = 0, minutes = 0;
  for (const r of Object.values(s.runs)) {
    if (r.task !== t.id) continue;
    usd += r.usage?.cost_usd || 0;
    if (r.started_at && r.ended_at) minutes += (Date.parse(r.ended_at) - Date.parse(r.started_at)) / 60000;
  }
  return {usd, minutes};
}
function overBudget(s, t) {
  const b = t.flow.budget;
  if (!b) return false;
  const x = spent(s, t);
  return b.usd > 0 && x.usd >= b.usd || b.minutes > 0 && x.minutes >= b.minutes;
}
function verdictOf(st, r) {
  if (r.checked && r.checked.exit !== 0) return 'rework';
  if (st.output !== 'verdict') return 'pass';
  return r.verdict ? r.verdict.verdict : 'blocked';
}
// stageSituation is where a started task in a workflow stands (task.State.stageSituation).
function stageSituation(s, t, last) {
  const st = stageOf(t.flow, t.stage);
  if (!st) return {kind: 'waiting', reason: 'blocked'};
  if (st.gate === 'human') return {kind: 'waiting', reason: 'accept'};
  const since = Math.max(t.stage_seq || 0, t.start_seq || 0);
  if (!last || (last.seq || 0) <= since) return overBudget(s, t) ? {kind: 'waiting', reason: 'budget'} : {kind: 'queued', reason: 'ready'};
  const waiting = !openStates.has(last.state) && (last.attention === 'asked' || last.attention === 'permission');
  if (waiting) return {kind: 'waiting', reason: last.attention, run: last.id};
  if (last.state === 'exited' && last.exit_code === 0 && !last.attention) {
    if (st.output === 'verdict' && last.verdict && stale(t, last)) return {kind: 'queued', reason: 'stale', run: last.id};
    const v = verdictOf(st, last);
    if (v === 'pass') return {kind: 'queued', reason: 'advance', run: last.id};
    if (v === 'rework') return (t.loops || 0) >= (t.flow.max_loops || 0) ? {kind: 'waiting', reason: 'max_loops', run: last.id} : {kind: 'queued', reason: 'rework', run: last.id};
    return {kind: 'waiting', reason: 'blocked', run: last.id};
  }
  if (last.reason && last.state !== 'exited') return {kind: 'waiting', reason: last.reason, run: last.id};
  return {kind: 'waiting', reason: last.state, run: last.id};
}
// queuedWork gives a queued run's task, and those whose branches it makes, their branches (task.State.queuedWork).
function queuedWork(s, r) {
  const w = r.work;
  if (!w || w.read_only || w.merge) return;
  for (const b of [...(w.chain || []), w.branch]) {
    const t = s.tasks[b.replace(/^tend\//, '')];
    if (!t) continue;
    if (!t.branch) t.branch = b;
    if (!t.work_on) t.work_on = r.machine;
  }
}
// worked records what an ended run did to its task's branch (task.State.worked).
function worked(s, r) {
  const t = s.tasks[r.task], w = r.worked;
  if (!t || !w || !r.work) return;
  if (r.work.merge) {
    if (w.merged) { t.merged = true; const p = s.tasks[t.parent]; if (p && w.head) p.head = w.head; }
  } else if (!r.work.read_only && w.head) t.head = w.head;
}
// drafted makes a planner's plan its task's draft once its run ended (task.State.drafted).
function drafted(s, r) {
  const t = s.tasks[r.task];
  if (!t || !r.planner || !r.plan) return;
  t.draft = {plan: r.plan, run: r.id, source_rev: t.source ? t.source.rev : undefined};
}
function planSituation(t, last) {
  if (t.draft) return {kind: 'waiting', reason: 'draft', run: last.id};
  if (!openStates.has(last.state) && (last.attention === 'asked' || last.attention === 'permission')) return {kind: 'waiting', reason: last.attention, run: last.id};
  if (last.reason && last.state !== 'exited') return {kind: 'waiting', reason: last.reason, run: last.id};
  return {kind: 'waiting', reason: 'no_plan', run: last.id};
}
const finished = status => status === 'done' || status === 'canceled';
function mergeSituation(last) {
  if (last.worked && last.worked.merged) return {kind: 'queued', reason: 'completing', run: last.id};
  if (last.reason === 'merge_conflict') return {kind: 'waiting', reason: 'merge_conflict', run: last.id};
  if (last.reason) return {kind: 'waiting', reason: last.reason, run: last.id};
  return {kind: 'waiting', reason: last.state, run: last.id};
}
const stale = (t, r) => !!(r.worked && r.worked.head && t.head && r.worked.head !== t.head);
// held is what keeps a started task from its own work (task.State.held).
function held(s, t) {
  if (t.held) return {kind: 'waiting', reason: 'held'};
  for (const a of t.after || []) {
    const d = s.tasks[a];
    if (!d) continue;
    if (d.status === 'canceled') return {kind: 'waiting', reason: 'after_canceled'};
    if (d.status !== 'done') return {kind: 'queued', reason: 'after'};
  }
  return null;
}

// sourceWaits is why t's issue keeps it waiting, as task.SourceWaits says.
function sourceWaits(t) {
  const src = t.source;
  if (!src) return '';
  if (src.pending) return 'source_changed';
  if (src.closed && !src.closed_acked) return 'source_closed';
  return '';
}

// dirHeld: queued run q waits for another run of its machine working in the same directory, as task.State.dirHeld.
const dirHeld = (s, q) => !!q.dir && !q.work && Object.values(s.runs).some(r => r.id !== q.id && r.machine === q.machine &&
  r.dir === q.dir && !r.work && openStates.has(r.state) && r.state !== 'queued');

// situation says how task t stands, as task.State.Situation does: {kind, reason, run}.
function situation(s, t) {
  if (t.status === 'backlog' || t.status === 'done' || t.status === 'canceled') return {kind: t.status};
  const runs = Object.values(s.runs).filter(r => r.task === t.id);
  const open = runs.find(r => openStates.has(r.state));
  if (open) {
    if (open.state === 'queued') return {kind: 'queued', reason: s.drains?.[open.machine] ? 'drain' : dirHeld(s, open) ? 'dir' : 'slot', run: open.id};
    if (open.attention === 'asked' || open.attention === 'permission') return {kind: 'waiting', reason: open.attention, run: open.id};
    if (open.state === 'unknown') return {kind: 'waiting', reason: 'unknown', run: open.id};
    return {kind: 'running', reason: open.state, run: open.id};
  }
  const why = sourceWaits(t);
  if (why) return {kind: 'waiting', reason: why};
  let last = null;
  for (const r of runs) if (!(r.state === 'canceled' && r.reason === 'undone') && (!last || (r.seq || 0) > (last.seq || 0) || (r.seq || 0) === (last.seq || 0) && r.queued_at > last.queued_at)) last = r; // a run an undo canceled never counts
  if (last && last.stage === 'merge' && (last.seq || 0) > (t.start_seq || 0)) return mergeSituation(last);
  const kids = Object.values(s.tasks).filter(k => k.parent === t.id);
  if (kids.some(k => k.status !== 'canceled')) {
    if (kids.some(k => k.status !== 'done' && k.status !== 'canceled')) return {kind: 'queued', reason: 'children'};
    if (!t.flow) return {kind: 'waiting', reason: 'accept'};
  } else if (last && last.stage === 'plan' && (last.seq || 0) > (t.start_seq || 0)) return planSituation(t, last);
  if (t.flow && t.auto) return held(s, t) || stageSituation(s, t, last);
  if (last && (last.seq || 0) > (t.start_seq || 0)) {
    const waiting = !openStates.has(last.state) && (last.attention === 'asked' || last.attention === 'permission');
    if (waiting) return {kind: 'waiting', reason: last.attention, run: last.id};
    if (last.state === 'exited' && last.exit_code === 0 && !last.attention) {
      return t.auto ? {kind: 'queued', reason: 'completing', run: last.id} : {kind: 'waiting', reason: 'ended', run: last.id};
    }
    if (last.reason && last.state !== 'exited') return {kind: 'waiting', reason: last.reason, run: last.id};
    return {kind: 'waiting', reason: last.state, run: last.id};
  }
  if (!t.auto) return {kind: 'waiting', reason: 'dispatch'};
  return held(s, t) || {kind: 'queued', reason: 'ready'};
}

// conversation is the runs of the conversation run id is in: its root (the run no parent of which the state holds) and
// every run that goes on from it, by seq, then queue time, then id (task.State.Conversation).
function conversation(s, id) {
  const rootOf = r => {
    const seen = new Set();
    while (r.parent && s.runs[r.parent] && !seen.has(r.id)) { seen.add(r.id); r = s.runs[r.parent]; }
    return r.id;
  };
  const r = s.runs[id];
  if (!r) return [];
  const root = rootOf(r);
  const cmp = (a, b) => (a < b ? -1 : a > b ? 1 : 0);
  // ⚠️ Go orders by the nanosecond: Date.parse keeps the millisecond, the fraction's digits past it come on top
  const sub = t => { const m = /\.(\d+)/.exec(t || ''); return m ? Number((m[1] + '000000000').slice(3, 9)) : 0; };
  const at = (a, b) => (Date.parse(a.queued_at || 0) - Date.parse(b.queued_at || 0)) || sub(a.queued_at) - sub(b.queued_at);
  return Object.values(s.runs).filter(x => x.task === r.task && rootOf(x) === root)
    .sort((a, b) => (a.seq || 0) - (b.seq || 0) || at(a, b) || cmp(a.id, b.id));
}

export {apply, situation, stageOf, nextStage, parts, conversation};
