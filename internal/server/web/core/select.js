// select derives what the pages show from the store: counts, what waits and in which order, today's and the last
// seven days' figures, the runs open now and each machine's lane of the day. Every function is pure: the state,
// machines and inbox as the store holds them, and now in milliseconds.
import {situation} from './fold.js';
import {usageTokens} from './format.js';

export const openStates = ['queued', 'starting', 'running', 'unknown'];
// ⚠️ The same states a machine counts as active (coord.Machine.Active).
export const activeStates = ['starting', 'running', 'unknown'];
export const endStates = ['exited', 'failed', 'stopped', 'canceled', 'abandoned'];

const hour = 3600e3;
const time = s => (s ? Date.parse(s) : NaN);

// dayStart is local midnight of the day now is in; daysBefore(now, n) the midnight n days earlier.
export function dayStart(now) {
  const d = new Date(now);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}
export function daysBefore(now, n) {
  const d = new Date(dayStart(now));
  d.setDate(d.getDate() - n);
  return d.getTime();
}

export const provider = r => r.provider || r.profile?.provider || r.agent || '';

// doing is what a run is at: its node's current tool (doing), else its own note, else the last thing its agent said.
export const doing = r => r.doing || r.note || r.last || '';

// usedAt is the moment a run's usage counts for: when it ended, else started, else was queued.
const usedAt = r => time(r.ended_at || r.started_at || r.queued_at);

// counts: the runs going and queued, the machines offline, and how much waits.
export function counts(state, machines = [], inbox = []) {
  const runs = Object.values(state.runs);
  return {
    running: runs.filter(r => activeStates.includes(r.state)).length,
    queued: runs.filter(r => r.state === 'queued').length,
    offline: machines.filter(m => m.state === 'offline' && !m.retired).map(m => m.name),
    waiting: inbox.length,
  };
}

// slots is the room on the machines connected now: how many runs they take, how many they run.
export function slots(machines = []) {
  const on = machines.filter(m => m.state === 'connected' && !m.retired);
  return {slots: on.reduce((n, m) => n + (m.slots || 0), 0), active: on.reduce((n, m) => n + (m.active || 0), 0),
    online: on.length, offline: machines.filter(m => m.state === 'offline' && !m.retired).length};
}

const tally = runs => {
  const out = Object.fromEntries(endStates.map(s => [s, 0]));
  for (const r of runs) if (r.state in out) out[r.state]++;
  return out;
};

// spans are the runs' times on their machines between from and to: started until ended, or until to while open.
function spans(state, from, to) {
  const out = [];
  for (const r of Object.values(state.runs)) {
    const a = time(r.started_at);
    if (Number.isNaN(a)) continue;
    const e = time(r.ended_at);
    const b = Number.isNaN(e) ? (activeStates.includes(r.state) ? to : NaN) : e;
    if (Number.isNaN(b) || b <= from || a >= to) continue;
    out.push({run: r, from: Math.max(a, from), to: Math.min(b, to)});
  }
  return out.sort((x, y) => x.from - y.from || x.to - y.to);
}

// peaks: how many spans overlap at most within each bucket of width from `from` to `to`, and over the whole stretch.
function peaks(list, from, to, width) {
  const edges = list.flatMap(s => [[s.from, 1], [s.to, -1]]).sort((a, b) => a[0] - b[0] || a[1] - b[1]);
  const n = Math.max(1, Math.ceil((to - from) / width));
  const buckets = new Array(n).fill(0);
  let level = 0, i = 0, peak = 0;
  for (let b = 0; b < n; b++) {
    const end = from + (b + 1) * width;
    let top = level;
    while (i < edges.length && edges[i][0] < end) {
      level += edges[i][1];
      top = Math.max(top, level);
      i++;
    }
    buckets[b] = top;
    peak = Math.max(peak, top);
  }
  return {buckets, peak};
}

// today: the runs that ended since midnight (by how, their mean length), what the day spent, and how many ran at
// once (at most, and hour by hour from the first hour anything ran).
export function today(state, now) {
  const from = dayStart(now);
  const runs = Object.values(state.runs);
  const ended = runs.filter(r => endStates.includes(r.state) && time(r.ended_at) >= from);
  const lengths = ended.map(r => time(r.ended_at) - time(r.started_at)).filter(ms => ms >= 0);
  let tok = 0, usd = 0;
  for (const r of runs) if (usedAt(r) >= from) { tok += usageTokens(r.usage); usd += r.usage?.cost_usd || 0; }
  const list = spans(state, from, now);
  const first = list.length ? from + Math.floor((list[0].from - from) / hour) * hour : now - hour;
  const {buckets, peak} = peaks(list, first, now, hour);
  return {ended: ended.length, ends: tally(ended), mean: lengths.length ? lengths.reduce((a, b) => a + b, 0) / lengths.length : 0,
    tokens: tok, usd, peak, hourly: buckets};
}

// week: today and the six days before, oldest first: tokens by provider, dollars (only claude estimates them), and
// how the runs that ended that day ended.
export function week(state, now) {
  const starts = Array.from({length: 7}, (_, i) => daysBefore(now, 6 - i));
  const days = starts.map(at => ({at, tokens: 0, usd: 0, providers: {}, runs: []}));
  const dayOf = t => {
    for (let i = 6; i >= 0; i--) if (t >= starts[i]) return t <= now ? days[i] : null;
    return null;
  };
  for (const r of Object.values(state.runs)) {
    const d = dayOf(usedAt(r));
    if (d) {
      const n = usageTokens(r.usage), p = provider(r);
      d.tokens += n;
      d.usd += r.usage?.cost_usd || 0;
      if (n) d.providers[p] = (d.providers[p] || 0) + n;
    }
    if (endStates.includes(r.state)) dayOf(time(r.ended_at))?.runs.push(r);
  }
  const all = days.flatMap(d => d.runs);
  return {
    days: days.map(({runs, ...d}) => ({...d, ends: tally(runs)})),
    tokens: days.reduce((n, d) => n + d.tokens, 0), usd: days.reduce((n, d) => n + d.usd, 0),
    ended: all.length, ends: tally(all),
    providers: [...new Set(days.flatMap(d => Object.keys(d.providers)))].sort(),
  };
}

// ⚠️ The inbox's reasons as the home groups them; any reason not named here is an error of the run (a run's own
// reason or end state).
const answerReasons = ['asked', 'permission'];
const acceptReasons = ['accept', 'ended', 'draft'];
const otherReasons = ['dispatch', 'paused', 'source_changed', 'source_closed', 'source_reopened'];
export const waitGroups = ['answer', 'error', 'accept', 'other'];

export const waitGroup = reason => (answerReasons.includes(reason) ? 'answer' : acceptReasons.includes(reason) ? 'accept'
  : otherReasons.includes(reason) ? 'other' : 'error');

// waits is the inbox in the order the home lists it: to answer first, then errors, then work to accept, the rest
// last, each oldest first; as keeps only the items that wait on the viewer in that role.
export function waits(inbox = [], as = '') {
  const rank = x => waitGroups.indexOf(waitGroup(x.reason));
  return inbox.filter(x => !as || (x.as || []).includes(as)).sort((a, b) => rank(a) - rank(b) || time(a.since) - time(b.since));
}

export function waitSummary(inbox = [], now) {
  const by = Object.fromEntries(waitGroups.map(g => [g, 0]));
  for (const x of inbox) by[waitGroup(x.reason)]++;
  const oldest = inbox.reduce((t, x) => Math.min(t, time(x.since)), Infinity);
  return {count: inbox.length, by, longest: inbox.length ? now - oldest : 0};
}

// openRuns: the runs going, oldest first, then those queued in the order they were; a queued run says what it waits
// for (dir: another run in its directory; slot: its machine or a free slot).
export function openRuns(state) {
  const runs = Object.values(state.runs).filter(r => openStates.includes(r.state));
  const at = r => time(r.started_at || r.queued_at);
  const going = runs.filter(r => r.state !== 'queued').sort((a, b) => at(a) - at(b));
  const queued = runs.filter(r => r.state === 'queued').sort((a, b) => time(a.queued_at) - time(b.queued_at));
  return [...going, ...queued].map(r => {
    const t = state.tasks[r.task];
    const sit = t ? situation(state, t) : null;
    return {run: r, task: t, why: r.state === 'queued' && sit?.run === r.id ? sit.reason : ''};
  });
}

// recent: the runs that ended today, the latest first.
export function recent(state, now, n = 8) {
  const from = dayStart(now);
  return Object.values(state.runs).filter(r => endStates.includes(r.state) && time(r.ended_at) >= from)
    .sort((a, b) => time(b.ended_at) - time(a.ended_at)).slice(0, n);
}

// ⚠️ The day's lanes start at 08:00 unless something ran earlier.
const laneStartHour = 8;

// lanes: each machine's day from the first hour anything ran (08:00 at the latest) to now, its runs packed into as
// many rows as they overlap (at least its slots). Machines that ran nothing today still get their lane.
export function lanes(state, machines = [], now) {
  const midnight = dayStart(now);
  const list = spans(state, midnight, now);
  const earliest = list.length ? midnight + Math.floor((list[0].from - midnight) / hour) * hour : Infinity;
  const from = Math.min(midnight + laneStartHour * hour, earliest, now - hour);
  const names = [...machines.filter(m => !m.retired).map(m => m.name)];
  for (const s of list) if (!names.includes(s.run.machine)) names.push(s.run.machine);
  return {from, to: now, lanes: names.map(name => {
    const m = machines.find(x => x.name === name) || {name, state: 'unknown'};
    const rows = [];
    for (const s of list.filter(x => x.run.machine === name)) {
      let row = rows.find(r => r[r.length - 1].to <= s.from);
      if (!row) rows.push(row = []);
      row.push({run: s.run.id, task: s.run.task, from: s.from, to: s.to,
        kind: activeStates.includes(s.run.state) ? 'running' : s.run.state});
    }
    while (rows.length < Math.max(1, m.slots || 0)) rows.push([]);
    return {name, state: m.state, slots: m.slots || 0, active: m.active || 0, queued: m.queued || 0, rows};
  })};
}
