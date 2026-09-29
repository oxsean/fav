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
  asked: {glyph: '?', tone: 'unknown'},
  permission: {glyph: '!', tone: 'unknown'},
  stalled: {glyph: '…', tone: 'unknown'},
  backlog: {glyph: '○', tone: 'muted'},
  todo: {glyph: '○', tone: 'muted'},
  done: {glyph: '✓', tone: 'exited'},
  waiting: {glyph: '?', tone: 'unknown'},
  online: {glyph: '●', tone: 'success'},
  offline: {glyph: '×', tone: 'failed'},
};

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
