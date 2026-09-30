// changes is what a run changed, for the changes tab: its files (run.changes, read in pages of the node's size), a
// file's diff in pages of hunks (run.diff, ignoring whitespace as git diff -w does when asked), and how the tab lays
// them out: line numbers, side by side, picked lines and the quote they make for the agent. A running run's list is
// compared against its workspace as it is (a snapshot); when that moves between two pages the list is taken again from
// its start. An ended run's changes do not change, so they are kept; a running run's pages are kept by their snapshot.
import {code} from './proto.js';

// ⚠️ How many times a list is taken again from its start before its snapshot_changed is given up to.
export const RESTARTS = 3;
// ⚠️ Hunks asked for in one run.diff page (the node also stops a page at 256 KiB).
export const HUNKS = 10;
// ⚠️ The lines of context the node gives by default, and how many more each ask for context adds.
export const CONTEXT = 3;
export const MORE_CONTEXT = 20;
// ⚠️ A quote holds at most this many lines and bytes of what was picked (the coordinator takes 64 KiB a message).
export const QUOTE_LINES = 200;
export const QUOTE_BYTES = 16 << 10;
// ⚠️ run.diff pages kept at most, the oldest let go first.
export const KEPT_PAGES = 200;

export const filters = ['all', 'agent', 'generated'];

const glyphs = {add: '+', modify: '~', delete: '−', rename: '→'};
export const glyph = op => glyphs[op] || '~';

const dirOf = path => (path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '');

// ordered groups files by directory: the root's first, then each directory by name, the files in it by name.
export function ordered(files) {
  const by = new Map();
  for (const f of files) {
    const d = dirOf(f.path);
    if (!by.has(d)) by.set(d, []);
    by.get(d).push(f);
  }
  return [...by.keys()].sort((a, b) => (a === '' ? -1 : b === '' ? 1 : a < b ? -1 : a > b ? 1 : 0))
    .map(dir => ({dir, files: by.get(dir).sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0))}));
}

export function filtered(files, filter) {
  if (filter === 'agent') return files.filter(f => f.agent);
  if (filter === 'generated') return files.filter(f => f.generated);
  return files;
}

export const counts = files => ({all: files.length, agent: files.filter(f => f.agent).length, generated: files.filter(f => f.generated).length});

// folded: a file that starts folded (a big change, a generated file, a binary one).
export const folded = f => !!(f.big || f.generated || f.binary);

const head = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/;

// rowsOf is a file's hunks as rows: each hunk's head, then its lines, each {kind, text, old?, new?, h}: kind is ctx,
// add, del or note (git's "\\ No newline…"), old and new its line numbers on each side, h its hunk.
export function rowsOf(hunks) {
  const rows = [];
  hunks.forEach((x, h) => {
    rows.push({kind: 'hunk', text: x.at || '', h});
    const m = head.exec(x.at || '');
    let o = m ? Number(m[1]) : 0, n = m ? Number(m[2]) : 0;
    for (const text of x.lines || []) {
      const c = text[0];
      if (c === '+') rows.push({kind: 'add', text, new: n++, h});
      else if (c === '-') rows.push({kind: 'del', text, old: o++, h});
      else if (c === '\\') rows.push({kind: 'note', text, h});
      else rows.push({kind: 'ctx', text, old: o++, new: n++, h});
    }
  });
  return rows;
}

// onlySpace: a file whose lines changed has no hunks left once its diff ignores whitespace (the list counts every
// change).
export const onlySpace = (f, diff) => !!diff?.ignoreSpace && !(diff.hunks || []).length && !diff.of && (f.add || 0) + (f.del || 0) > 0;

// joined is a file's pages ({from: {hunk, line?}, page}, in order) as one: its hunks, a page that starts inside a
// hunk adding to it; of, the file's hunks in all; next, where a page after them starts (null past the last).
export function joined(pages) {
  const hunks = [];
  let of = 0, next = null;
  for (const {from, page} of pages) {
    const got = (page.hunks || []).map(x => ({at: x.at, lines: [...(x.lines || [])]}));
    if (from.line > 0 && hunks.length && got.length) hunks.at(-1).lines.push(...got.shift().lines);
    hunks.push(...got);
    of = page.of || 0;
    next = page.next || null;
  }
  return {hunks, of, next};
}

// sides pairs rows for side by side: {l, r} the row shown on the left and on the right (-1 for none), lo and hi the
// rows the pair covers. A run of removed lines goes beside the added ones after it; a hunk's head and context lines
// are on both sides; a note stays with the side of the line before it.
export function sides(rows) {
  const out = [];
  let i = 0;
  while (i < rows.length) {
    const k = rows[i].kind;
    if (k === 'del' || k === 'add') {
      const dels = [], adds = [];
      while (i < rows.length && (rows[i].kind === 'del' || (rows[i].kind === 'note' && !adds.length && dels.length))) dels.push(i++);
      while (i < rows.length && (rows[i].kind === 'add' || (rows[i].kind === 'note' && adds.length))) adds.push(i++);
      for (let j = 0; j < Math.max(dels.length, adds.length); j++) {
        const l = dels[j] ?? -1, r = adds[j] ?? -1;
        out.push({l, r, lo: Math.min(...[l, r].filter(x => x >= 0)), hi: Math.max(l, r)});
      }
    } else {
      out.push({l: i, r: i, lo: i, hi: i});
      i++;
    }
  }
  return out;
}

// pick is the lines picked after a tap on the rows lo..hi (one row, or a side-by-side pair): the first tap picks it,
// a second one reaches from it to the one tapped, and past a range a tap starts again; extend (Shift) always reaches.
// The same single row tapped again lets go (null). sel is {a, b, at, one}: the rows a..b, at the rows it started from.
export function pick(sel, lo, hi, extend) {
  if (sel && (extend || sel.one)) {
    if (!extend && sel.at[0] === lo && sel.at[1] === hi) return null;
    return {a: Math.min(sel.at[0], lo), b: Math.max(sel.at[1], hi), at: sel.at, one: false};
  }
  return {a: lo, b: hi, at: [lo, hi], one: true};
}

const span = ns => (ns.length ? (Math.min(...ns) === Math.max(...ns) ? String(ns[0]) : Math.min(...ns) + '-' + Math.max(...ns)) : '');

// quote is what picked rows a..b of path say to the agent: path:lines (the new side's numbers, or the old side's
// through before when only removed lines are picked), then the lines as the diff has them in a fenced block, at most
// QUOTE_LINES and QUOTE_BYTES of them, more(n) saying how many are left out.
export function quote({path, rows, a, b, before, more}) {
  const picked = rows.slice(a, b + 1);
  const news = picked.filter(r => r.new).map(r => r.new), olds = picked.filter(r => r.old).map(r => r.old);
  const ref = news.length ? path + ':' + span(news) : olds.length ? before(path + ':' + span(olds)) : path;
  const lines = [];
  let bytes = 0;
  for (const r of picked) {
    if (lines.length >= QUOTE_LINES || (lines.length && bytes + r.text.length + 1 > QUOTE_BYTES)) break;
    lines.push(r.text);
    bytes += r.text.length + 1;
  }
  const longest = Math.max(2, ...lines.map(l => Math.max(0, ...(l.match(/`+/g) || []).map(x => x.length))));
  const fence = '`'.repeat(longest + 1);
  const cut = picked.length - lines.length;
  return `${ref}\n${fence}diff\n${lines.join('\n')}\n${fence}` + (cut > 0 ? '\n' + more(cut) : '');
}

// withQuote is a draft with a quote put after what is written.
export const withQuote = (draft, q) => (draft.trim() ? draft.trimEnd() + '\n\n' : '') + q + '\n';

// createChanges reads changes over wire; now gives the time a list was taken (ms).
export function createChanges({wire, now = () => Date.now()}) {
  const kept = new Map(), pages = new Map();

  async function read(run) {
    let files = [], head = null, after = '';
    for (;;) {
      const p = await wire.call('run.changes', after ? {run, after, ...(head.snapshot ? {snapshot: head.snapshot} : {})} : {run});
      if (head && (p.snapshot || '') !== head.snapshot) throw Object.assign(new Error(code.snapshotChanged), {code: code.snapshotChanged});
      head ||= {total: p.total || {files: 0, add: 0, del: 0}, snapshot: p.snapshot || '', git: !!p.git, hidden: p.hidden || 0};
      files = [...files, ...(p.files || [])];
      if (!p.next) return {...head, files, at: now()};
      after = p.next;
    }
  }

  return {
    can: () => !!wire.has?.('run.changes'),
    // list is a run's changes; ended keeps them.
    async list(run, {ended = false} = {}) {
      if (ended && kept.has(run)) return kept.get(run);
      for (let n = 0; ; n++) {
        try {
          const got = await read(run);
          if (ended) kept.set(run, got);
          return got;
        } catch (e) {
          if (e.code !== code.snapshotChanged || n >= RESTARTS) throw e;
        }
      }
    },
    // diff is a page of a file's hunks in the snapshot its list was taken at ('' outside git): from hunk (its line
    // on), with context lines around each change (the node's own when not given), ignoring whitespace when asked (a
    // node that cannot answers unsupported).
    async diff(run, path, snapshot, {hunk = 0, line = 0, context = CONTEXT, ignoreSpace = false} = {}) {
      const k = [run, snapshot, path, hunk, line, context, ignoreSpace].join('\n');
      if (pages.has(k)) return pages.get(k);
      const got = await wire.call('run.diff', {run, path, ...(snapshot ? {snapshot} : {}), hunk, ...(line ? {line} : {}), n: HUNKS,
        ...(context !== CONTEXT ? {context} : {}), ...(ignoreSpace ? {ignore_space: true} : {})});
      pages.set(k, got);
      if (pages.size > KEPT_PAGES) pages.delete(pages.keys().next().value);
      return got;
    },
  };
}
