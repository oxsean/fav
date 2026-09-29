// tasks_test checks what the task pages are built on, from the tasks frames: the board's columns and the four drops
// it allows, the tree's order and folding, filters, subtask progress, where a new task may work, what a dispatch meets,
// and a plan draft's edits with every way task.Plan.Check refuses one.
process.env.TZ = 'UTC';
import * as tk from '../web/core/tasks.js';
import {tasks} from './rig.js';
import {test, eq, run} from './check.js';

const draft = st => st.tasks.q6.draft.plan;

test('the board puts each task in the column its situation says, what waits first, the latest changed first', async () => {
  const {store, errors} = await tasks();
  const b = tk.board(store.state);
  eq(Object.fromEntries(Object.entries(b).map(([c, list]) => [c, list.map(r => r.task.id)])), {
    waiting: ['q6', 'q4', 'q1', 'q10', 'q9'], running: ['q3'], queued: ['q11'], backlog: ['q5', 'q7'], ended: ['q2', 'q8'],
  }, 'columns');
  eq(b.waiting.map(r => r.sit.reason), ['draft', 'accept', 'source_changed', 'merge_conflict', 'dispatch'], 'why each waits');
  eq(tk.rows(store.state).map(r => r.task.id), ['q6', 'q4', 'q1', 'q10', 'q9', 'q3', 'q11', 'q5', 'q7', 'q2', 'q8'], 'rows in the same order');
  eq(errors, [], 'errors');
});

test('a drop is one of four commands, with the write that takes it back; the rest are refused with a reason', async () => {
  const {store} = await tasks();
  const st = store.state, d = (id, to) => tk.drop(st, st.tasks[id], to);
  eq(d('q9', 'ended'), {method: 'task.set_status', params: {id: 'q9', status: 'done'}, undo: {method: 'task.set_status', params: {id: 'q9', status: 'todo'}}}, 'done');
  eq(d('q2', 'waiting'), {method: 'task.set_status', params: {id: 'q2', status: 'todo'}, undo: {method: 'task.set_status', params: {id: 'q2', status: 'done'}}}, 'reopen');
  eq(d('q2', 'backlog'), {method: 'task.set_status', params: {id: 'q2', status: 'backlog'}, undo: {method: 'task.set_status', params: {id: 'q2', status: 'done'}}}, 'not yet');
  eq(d('q9', 'backlog').params.status, 'backlog', 'a todo task not running: not yet');
  eq(d('q5', 'queued'), {method: 'task.start', params: {id: 'q5'}}, 'start, nothing to undo');
  eq(d('q5', 'running'), {method: 'task.start', params: {id: 'q5'}}, 'start from running too');
  eq([d('q4', 'ended'), d('q3', 'ended'), d('q3', 'backlog'), d('q7', 'ended'), d('q9', 'queued'), d('q6', 'running')],
    [{why: 'flow'}, {why: 'open'}, {why: 'open'}, {why: 'rule'}, {why: 'rule'}, {why: 'rule'}], 'refused');
  eq(d('q9', 'waiting'), null, 'its own column');
});

test('the tree: children under parents in after order, folded ones hidden, a filter keeps the ancestors', async () => {
  const {store} = await tasks();
  const st = store.state, line = r => `${'  '.repeat(r.depth)}${r.task.id}${r.kids ? '/' + r.kids : ''}`;
  eq(tk.tree(st).map(line), ['q1/4', '  q10', '  q2', '  q3/1', '    q5', '  q4', 'q11', 'q6', 'q7', 'q8', 'q9'], 'the whole tree');
  eq(tk.tree(st, undefined, new Set(['q3'])).map(line), ['q1/4', '  q10', '  q2', '  q3/1', '  q4', 'q11', 'q6', 'q7', 'q8', 'q9'], 'q3 folded');
  eq(tk.tree(st, t => t.id === 'q5').map(line), ['q1/1', '  q3/1', '    q5'], 'a match with its ancestors');
  const backlog = tk.matcher(st, {column: 'backlog'});
  eq(tk.tree(st, backlog).map(r => r.task.id), ['q1', 'q3', 'q5', 'q7'], 'by column');
});

test('filters, subtask progress and what a task spent', async () => {
  const {store} = await tasks();
  const st = store.state, ids = f => tk.rows(st, tk.matcher(st, f)).map(r => r.task.id).sort();
  eq(ids({q: 'coupon'}), ['q9'], 'by title');
  eq(ids({q: 'Q1'}), ['q1', 'q10', 'q11'], 'by id, any case');
  eq(ids({me: 'u_a', mine: true}), [], 'mine: none for another');
  eq(ids({me: 'u_b', mine: true, column: 'ended'}), ['q2', 'q8'], 'mine and ended');
  eq(ids({project: 'p2'}), [], 'another project');
  eq([tk.progress(st, 'q1'), tk.progress(st, 'q3'), tk.progress(st, 'q9')], [{done: 1, of: 4}, {done: 0, of: 1}, null], 'progress');
  eq(tk.spent(st, 'q2'), {usd: 1.1, tokens: 58000}, 'spent');
  eq(tk.runsOf(st, 'q4').map(r => r.id), ['r25'], 'runs, the latest first');
});

test('where a new task may work: its project\'s checkouts, then the directories its tasks used', async () => {
  const {store} = await tasks();
  const st = store.state;
  eq(tk.dirs(st, {project: 'p1', machine: 'mba'}), [{dir: '/w/shop', kind: 'project', machine: 'mba'}, {dir: '/w/search', kind: 'recent', machine: ''},
    {dir: '/w/docs', kind: 'recent', machine: ''}], 'on mba');
  eq(tk.dirs(st, {project: 'p1'}).map(x => `${x.kind}:${x.dir}`), ['project:/w/shop', 'project:/srv/shop', 'recent:/w/search', 'recent:/w/docs'], 'on any machine');
  eq(tk.dirs(st, {project: 'nope'}).length, 0, 'an unknown project with no tasks');
  eq(tk.defaults(st, st.tasks.q9), {machine: 'linux', agent: 'codex', fromProject: {machine: false, agent: false}}, 'its own');
  eq(tk.defaults(st, st.tasks.q11), {machine: 'mba', agent: 'claude', fromProject: {machine: true, agent: true}}, 'its project\'s');
});

test('what a dispatch meets: blocks the preview would, notes on how it goes', async () => {
  const {store} = await tasks();
  const st = store.state, m = Object.fromEntries(store.machines.value.map(x => [x.name, x]));
  const claude = {name: 'claude', provider: 'claude'}, codex = {name: 'codex', provider: 'codex'};
  const a = (id, machine, profile) => tk.advice(st, typeof id === 'string' ? st.tasks[id] : id, machine, profile);
  eq(a('q9', m.linux, codex), {}, 'clear');
  eq(a('q9', m.mba, codex), {block: 'cli', detail: 'codex'}, 'its CLI is not on mba');
  eq(a('q9', m.mba, {...claude, machine: 'linux'}), {block: 'pinned', detail: 'linux'}, 'an agent pinned elsewhere');
  eq(a('q9', m.win, claude), {note: 'offline', detail: 'win'}, 'it queues until win answers');
  eq(a({id: 'z', status: 'todo', dir: '/w/shop'}, m.mba, claude), {note: 'busy', detail: '1/2'}, 'a run works in its directory');
  eq(a('q11', m.mba, claude), {block: 'open', detail: 'r23'}, 'a run is open');
  eq(a('q7', m.mba, claude), {block: 'status', detail: 'backlog'}, 'not started');
  eq(a({id: 'y', status: 'todo'}, m.mba, claude), {block: 'dir'}, 'no directory');
  eq(a('q9', null, claude), {block: 'pick'}, 'nothing picked');
});

test('a plan draft: its rows, adding, removing, renaming, and what the coordinator would refuse', async () => {
  const {store} = await tasks();
  const plan = draft(store.state);
  eq(tk.planRows(plan).map(r => `${r.depth}${r.item.key}`), ['0index', '0api', '1api-docs', '0ui'], 'rows');
  eq(tk.planCheck(plan), [], 'the planner\'s plan passes');
  const cut = tk.planRemove(plan, 'api');
  eq([cut.tasks.map(x => x.key), cut.tasks.find(x => x.key === 'ui').after], [['index', 'ui'], undefined], 'api goes with what it holds; ui no longer waits for it');
  eq(plan.tasks.length, 4, 'the draft itself is untouched');
  const more = tk.planAdd(plan, {parent: 'api', title: 'Rate limits'});
  eq(more.tasks.at(-1), {key: 'task-5', title: 'Rate limits', parent: 'api'}, 'added under api');
  const renamed = tk.planEdit(plan, 'index', {key: 'idx', brief: ''});
  eq([renamed.tasks[0].key, renamed.tasks[0].brief, renamed.tasks[1].after], ['idx', undefined, ['idx']], 'renamed, its brief cleared, api follows');
  const bad = (edit, what) => tk.planCheck({tasks: plan.tasks.map(x => (x.key === edit.at ? {...x, ...edit.set} : x))});
  eq(tk.planCheck({tasks: []}), [{key: '', code: 'empty'}], 'empty');
  eq(bad({at: 'ui', set: {key: 'Bad Key'}}), [{key: 'Bad Key', code: 'key'}], 'a key');
  eq(bad({at: 'ui', set: {key: 'api'}}), [{key: 'api', code: 'twice'}], 'a key twice');
  eq(bad({at: 'ui', set: {title: '  '}}), [{key: 'ui', code: 'title'}], 'no title');
  eq(bad({at: 'ui', set: {size: 'XL'}}), [{key: 'ui', code: 'size'}], 'a size');
  eq(bad({at: 'ui', set: {parent: 'web'}}), [{key: 'ui', code: 'parent', detail: 'web'}], 'part of nothing');
  eq(bad({at: 'ui', set: {parent: 'api-docs', after: []}}), [{key: 'ui', code: 'deep'}], 'three levels');
  eq(bad({at: 'ui', set: {after: ['web']}}), [{key: 'ui', code: 'after', detail: 'web'}], 'after nothing');
  eq(bad({at: 'api-docs', set: {after: ['api']}}), [{key: 'api-docs', code: 'own', detail: 'api'}], 'after what holds it');
  eq(bad({at: 'index', set: {after: ['ui']}}), [{key: '', code: 'cycle'}], 'a cycle');
  eq(tk.planCheck({tasks: Array.from({length: tk.PLAN_MAX + 1}, (_, i) => ({key: 'k' + i, title: 'T'}))}), [{key: '', code: 'many', detail: '50'}], 'too many');
});

test('what can be done with each task: only what the affordances give, the primary one by its situation', async () => {
  const {store} = await tasks();
  const st = store.state, aff = store.affordances.value, of = id => tk.actionsOf(st, aff, st.tasks[id]);
  eq(Object.fromEntries(['q1', 'q2', 'q3', 'q4', 'q5', 'q6', 'q7', 'q8', 'q9', 'q10', 'q11'].map(id => [id, of(id).primary])), {
    q1: 'ack', q2: 'reopen', q3: 'stop', q4: 'pass', q5: 'start', q6: 'review', q7: 'start', q8: 'reopen', q9: 'dispatch', q10: 'merge', q11: 'stop',
  }, 'primaries');
  eq(of('q4').more, ['rework', 'dispatch', 'plan', 'backlog', 'edit', 'move', 'child', 'copy', 'cancel'], 'at its human gate: pass, then rework');
  eq(of('q9').more, ['done', 'start', 'plan', 'backlog', 'edit', 'move', 'child', 'copy', 'cancel'], 'a task to dispatch');
  eq(of('q2').more, ['edit', 'move', 'copy'], 'done');
  eq(of('q6').more.includes('sendBack'), true, 'its last run may be continued: send it back');
  eq(tk.actionsOf(st, {runs: {}, tasks: {}}, st.tasks.q9), {primary: '', more: ['copy']}, 'no affordances: only what the page does itself');
  eq(tk.actionsOf(st, {runs: {}, tasks: {q9: {actions: ['edit', 'done']}}}, st.tasks.q9), {primary: '', more: ['done', 'edit', 'copy']},
    'the one its situation asks for is not given: no primary, nothing in its place');
});

await run();
