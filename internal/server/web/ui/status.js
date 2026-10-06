// Status is the site's one drawing of a state: a glyph in the state's colour, and its word.
import {html, cx, useWords} from './base.js';

// ⚠️ Each state differs in shape as well as colour; tone is the skin token that colours it.
export const statuses = {
  queued: {glyph: '◷', tone: 'queued'},
  starting: {glyph: '◌', tone: 'starting', pulse: true},
  running: {glyph: '●', tone: 'running', pulse: true},
  unknown: {glyph: '?', tone: 'unknown'},
  exited: {glyph: '✓', tone: 'exited'},
  stopped: {glyph: '■', tone: 'stopped'},
  failed: {glyph: '!', tone: 'failed'},
  canceled: {glyph: '×', tone: 'canceled'},
  abandoned: {glyph: '⊘', tone: 'abandoned'},
  asked: {glyph: '?', tone: 'accent'},
  attend: {glyph: '?', tone: 'accent'},
  permission: {glyph: '!', tone: 'warning'},
  stalled: {glyph: '…', tone: 'danger'},
  backlog: {glyph: '○', tone: 'muted'},
  todo: {glyph: '○', tone: 'muted'},
  done: {glyph: '✓', tone: 'exited'},
  waiting: {glyph: '?', tone: 'unknown'},
  online: {glyph: '●', tone: 'success'},
  offline: {glyph: '×', tone: 'failed'},
};

const open = new Set(['queued', 'starting', 'running', 'unknown']);

// waitOf is what a run that wants someone waits on, as Status draws it: permission; asked, a question it put into
// words; attend, a wait nothing names, never drawn or worded as a question; stalled, no output for long while it
// runs. '' when it wants nobody.
export function waitOf(run) {
  if (run?.attention === 'permission') return 'permission';
  if (run?.attention === 'asked') return run.ask || (run.requests || []).some(q => q.kind === 'question') ? 'asked' : 'attend';
  if (run?.attention === 'stalled' && open.has(run.state)) return 'stalled';
  return '';
}

// runState is how a run is drawn: what it waits on, else its state.
export const runState = run => waitOf(run) || run.state;

// Status draws state; word shows its name, or label says something of its own ("运行中 12m") in the state's colour.
// Without either the name is still there for screen readers.
export function Status({state, word = false, label = ''}) {
  const {t} = useWords();
  const known = statuses[state] ? state : 'unknown';
  const s = statuses[known];
  const name = t('status.' + known);
  const text = label || (word ? name : '');
  return html`<span class=${cx('status', 's-' + s.tone)}><span class=${cx('st', s.pulse && 'pulse')} aria-hidden="true">${s.glyph}</span>${text
    ? html`<span class="status-word">${text}</span>` : html`<span class="sr-only">${name}</span>`}</span>`;
}
