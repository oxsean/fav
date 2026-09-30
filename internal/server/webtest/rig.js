// rig connects a wire and a store to the fake server, for the tests that play the home's frames.
import {createWire} from '../web/core/wire.js';
import {createStore} from '../web/core/store.js';
import {clock, server} from './fake.js';

// ⚠️ The home frames are written for this moment (TZ=UTC).
export const NOW = Date.parse('2026-09-30T14:32:00Z');

export function rig() {
  const clk = clock(), srv = server(clk);
  const wire = createWire({url: 'ws://tend.test/client', open: srv.open, timers: clk, random: () => 1});
  const frames = [], errors = [];
  const store = createStore({wire, frame: fn => frames.push(fn), onError: e => errors.push(String(e.message || e))});
  const flush = () => { for (const fn of frames.splice(0)) fn(); };
  wire.start();
  return {clk, srv, wire, store, flush, errors};
}

// home is a rig that has played home-state up to live; steps run more of the file.
export const home = steps => played((r, s) => r.srv.play('home-state', s), steps);

// tasks is a rig that has played tasks-state, the task pages' data.
export const tasks = steps => played((r, s) => r.srv.play('tasks-state', s), steps);

// team is a rig that has played team-state, the machines and team pages' data as an admin sees it.
export const team = steps => played((r, s) => r.srv.play('team-state', s), steps);

// agents is a rig that has played agents-state, the agent page's data as Bo (a member) sees it: steps.mount draws the
// page, which reads its lists then, and steps.read runs once they are in.
export const agents = (steps = {}) => played((r, s) => r.srv.play('agents-state', {...s, mount: () => steps.mount?.(r), read: () => steps.read?.(r)}), steps);

async function played(play, steps = {}) {
  const r = rig();
  await play(r, {start() { r.store.start(); }, live() { r.flush(); steps.live?.(r); }, journal() { r.flush(); steps.journal?.(r); }});
  r.flush();
  return r;
}

// outputs is a rig that has played output-state: a task whose conversation is two runs, one of them asking.
export const outputs = steps => played((r, s) => r.srv.play('output-state', s), steps);
