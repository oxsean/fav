// changes is what a run changed, for the changes tab: its files (run.changes, read in pages of the node's size) and a
// file's first hunk (run.diff), and how the tab lays them out. A running run's list is compared against its workspace
// as it is (a snapshot); when that moves between two pages the list is taken again from its start. An ended run's
// changes do not change, so they are kept.

// ⚠️ A file's preview is at most this many lines of its first hunk (the design draft's §4.6).
export const PREVIEW_LINES = 40;
// ⚠️ How many times a list is taken again from its start before its snapshot_changed is given up to.
export const RESTARTS = 3;

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

// preview is the first hunk of a run.diff page cut to PREVIEW_LINES: {at, lines, cut, more}, cut being the lines left
// out and more the hunks after it; null when there is none.
export function preview(page) {
  const h = page?.hunks?.[0];
  if (!h) return null;
  const lines = h.lines || [];
  return {at: h.at || '', lines: lines.slice(0, PREVIEW_LINES), cut: Math.max(0, lines.length - PREVIEW_LINES), more: Math.max(0, (page.of || page.hunks.length) - 1)};
}

// createChanges reads changes over wire; now gives the time a list was taken (ms).
export function createChanges({wire, now = () => Date.now()}) {
  const kept = new Map(), hunks = new Map();

  async function read(run) {
    let files = [], head = null, after = '';
    for (;;) {
      const p = await wire.call('run.changes', after ? {run, after, ...(head.snapshot ? {snapshot: head.snapshot} : {})} : {run});
      if (head && (p.snapshot || '') !== head.snapshot) throw Object.assign(new Error('snapshot_changed'), {code: 'snapshot_changed'});
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
          if (e.code !== 'snapshot_changed' || n >= RESTARTS) throw e;
        }
      }
    },
    // diff is the first hunk of a file in the snapshot its list was taken at ('' outside git).
    async diff(run, path, snapshot) {
      const k = run + '\n' + snapshot + '\n' + path;
      if (hunks.has(k)) return hunks.get(k);
      const got = await wire.call('run.diff', {run, path, ...(snapshot ? {snapshot} : {}), hunk: 0, n: 1});
      hunks.set(k, got);
      return got;
    },
  };
}
