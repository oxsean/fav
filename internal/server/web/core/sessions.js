// sessions reads the Claude / Codex sessions of every machine the viewer reads, through the coordinator: a page of rows
// for a query (sessions.query), message search (sessions.grep), a record changed through node.call put, a session
// moved into its machine's trash and back (trash, restore), a conversation, a message's full text and a session's hits through node.call, and the names of the machines' owners
// (people.names). From the answers it works out what the page shows: the query's tokens as chips, a toggle's patch,
// its undo and the row as it will be, the spans of a snippet, whether a changed row still fits, what may be deleted or
// restored and how long the trash keeps it, and the line that resumes a session. It never reads a query by its rules: Go's tokens say what each word is. No DOM.
import {signal} from '../vendor/signals-core.mjs';

// ⚠️ The node's session methods (internal/remote), the size of a page of messages, of a page of rows, the most rows
// one sessions.query gives (the coordinator's pageMax) and the hits of one session read at once.
export const M = {messages: 'messages', text: 'text', put: 'put', hits: 'hits', trash: 'trash', restore: 'restore'};
export const PAGE = 40;
export const LIMIT = 100;
export const MAX = 500;
export const HITS = 500;

// ⚠️ tend.Token's kinds (internal/tend/query.go) and the project: value of the sessions in no project.
export const kind = {tag: 'tag', project: 'project', provider: 'provider', status: 'status', after: 'after', before: 'before',
  last: 'last', turns: 'turns', file: 'file', host: 'host', word: 'word', unknown: 'unknown'};
export const NONE = 'none';

// ⚠️ The states the state chip offers (tend's status: values, open being the default no token writes; trash lists what
// each machine's trash holds), the sorts
// (tend.ParseSort), and how the coordinator says a machine answered (coord.MachineAnswer.State) and why a session
// cannot become a task (coord.MakeTask.Why).
export const states = ['open', 'active', 'done', 'archived', 'all', 'trash'];
export const TRASH = 'trash';
export const sorts = ['active', 'started', 'favorited', 'turns'];
export const answer = {ok: 'ok', offline: 'offline', old: 'old', timeout: 'timeout', unauthorized: 'unauthorized', error: 'error'};
export const cannot = {busy: 'busy', noAgent: 'no_agent', old: 'old'};

// keyOf names a row in the page's address: <machine>/<provider>:<session_id>; refOf reads it back.
export const keyOf = r => `${r.machine}/${r.provider}:${r.session_id}`;
export function refOf(key) {
  const at = String(key || '').indexOf('/');
  const colon = at < 0 ? -1 : key.indexOf(':', at + 1);
  if (at <= 0 || colon < 0) return null;
  return {machine: key.slice(0, at), provider: key.slice(at + 1, colon), session_id: key.slice(colon + 1)};
}
const refIn = r => ({provider: r.provider, session_id: r.session_id});

// ⚠️ What starts message search in the query box (fulltext.Prefixed): > or the 》 a CJK input method types.
export const searching = q => /^\s*[>》]/.test(q || '');
export const searchText = q => String(q || '').replace(/^\s*[>》]\s*/, '');

// chosen is what the tokens pick, as tend.Parse reads them: the tags, each other kind's last token, and the words Go
// did not understand.
export function chosen(tokens) {
  const out = {tags: [], unknown: []};
  for (const t of tokens || []) {
    if (t.kind === kind.tag) out.tags.push(t.value);
    else if (t.kind === kind.unknown) out.unknown.push(t.text);
    else if (t.kind !== kind.word) out[t.kind] = t;
  }
  return out;
}

// replaced is q without the tokens of kinds (their text as Go read it) and with add after it; a message search keeps
// its >.
export function replaced(q, tokens, kinds, add = []) {
  const drop = new Set((tokens || []).filter(t => kinds.includes(t.kind)).map(t => t.text));
  const kept = String(q || '').trim().split(/\s+/).filter(w => w && !drop.has(w));
  return [...kept, ...add].join(' ');
}

// dropped is q without the one token t.
export function dropped(q, t) {
  let gone = false;
  return String(q || '').trim().split(/\s+/).filter(w => w && (gone || w !== t.text || !(gone = true))).join(' ');
}

// stateOf is the state the query picks: its status: value, else the default (a message search looks at every state).
export const stateOf = (tokens, search = false) => chosen(tokens).status?.value || (search ? 'all' : 'open');
export const stateToken = s => (s === 'open' ? [] : ['status:' + s]);

// ⚠️ The time chip's choices as last: tokens (tend.ParseWhen): today and this year as the page's local dates.
export function times(now) {
  const d = new Date(now);
  const pad = n => String(n).padStart(2, '0');
  const today = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  return [{id: 'today', token: 'last:' + today}, {id: '7d', token: 'last:7d'}, {id: '30d', token: 'last:30d'}, {id: 'year', token: `last:${d.getFullYear()}-01-01`}];
}
export const timeKinds = [kind.last, kind.after, kind.before];

// favorited: the session has a record that is a favorite.
export const favorited = r => !!r?.favorited_at;

// toggles are the patches (tend.Patch, set not toggle) that turn a row the other way: done goes back to doing, as
// Rec.ToggleStatus.
export const toggles = {
  favorite: r => ({favorite: !favorited(r)}),
  archive: r => ({archived: !r.archived_at}),
  done: r => ({status: r.status === 'done' ? 'doing' : 'done'}),
};

// deletable: the viewer may move row into its machine's trash (the machine's answer a, coord.MachineAnswer): the node has
// trash and the row is one they may change. restorable: a row of the trash view, whose rows are never writable.
export const deletable = (row, a) => !!a?.trash && !!row?.writable;
export const restorable = (row, a) => !!row && !!a?.trash;

// ⚠️ How long a day is to the trash's trash_days.
const DAY = 864e5;

// purgeIn is how many days a session deleted at deletedAt has left in a trash that purges after days: at least one while
// it is there, at most days (its machine's clock may run ahead of this one), 0 for a trash that keeps everything.
export function purgeIn(deletedAt, days, now) {
  if (!days) return 0;
  const at = Date.parse(deletedAt || '');
  return at ? Math.min(days, Math.max(1, Math.ceil(days - (now - at) / DAY))) : days;
}

// normalize is tend.Normalize: tags lowercased, # dropped, blanks and repeats left out.
export function normalize(tags) {
  const out = [];
  for (const t of tags || []) {
    const v = String(t).replace(/^#/, '').trim().toLowerCase();
    if (v && !out.includes(v)) out.push(v);
  }
  return out;
}

// undoOf is tend.Patch.Undo: the patch that puts before's values back in the fields p sets.
export function undoOf(before, p) {
  const u = {};
  if ('favorite' in p) { u.favorite = favorited(before); if (before.favorited_at) u.favorited_at = before.favorited_at; }
  if ('archived' in p) { u.archived = !!before.archived_at; if (before.archived_at) u.archived_at = before.archived_at; }
  if ('status' in p) u.status = before.status || '';
  if ('title' in p) u.title = before.title || '';
  if ('tags' in p) u.tags = [...(before.tags || [])];
  if ('summary' in p) u.summary = before.summary || '';
  return u;
}

// applied is row as Patch.Apply leaves it at now, until the node's answer replaces it.
export function applied(row, p, now) {
  const out = {...row};
  const set = (k, v) => { if (v === undefined || v === '' || Array.isArray(v) && !v.length) delete out[k]; else out[k] = v; };
  const since = (on, cur, given) => (!on ? undefined : given || cur || new Date(now).toISOString());
  if ('favorite' in p) set('favorited_at', since(p.favorite, row.favorited_at, p.favorited_at));
  if ('archived' in p) set('archived_at', since(p.archived, row.archived_at, p.archived_at));
  if ('status' in p) set('status', p.status);
  if ('title' in p) set('title', String(p.title).trim());
  if ('tags' in p) set('tags', normalize(p.tags));
  if ('summary' in p) set('summary', String(p.summary).trim());
  return out;
}

// merged is the row a put answered (remote.Row) in the place of row: the coordinator's parts (machine, writable, task,
// make) and the project, which a put does not work out, stay.
export const merged = (row, got) => ({...got, machine: row.machine, writable: row.writable, task: row.task, make: row.make,
  ...(row.project_id ? {project_id: row.project_id} : {})});

// fits says whether a row changed in place is still one the query picks, by what a patch can change (state, favorite,
// tags) as Go's tokens say: one that no longer fits stays where it is, dimmed, until the list is read again.
export function fits(row, tokens, all) {
  if (!all && !favorited(row)) return false;
  const c = chosen(tokens);
  const s = c.status?.value || 'open';
  if (s === 'archived' && !row.archived_at) return false;
  if (!['archived', 'all', 'live', 'agent', 'trash'].includes(s)) {
    if (row.archived_at) return false;
    if (s === 'active' && row.status !== 'todo' && row.status !== 'doing') return false;
    if (['todo', 'doing', 'done'].includes(s) && row.status !== s) return false;
  }
  return c.tags.every(t => (row.tags || []).includes(t));
}

// marks cuts text into [text, hit] parts at spans, byte ranges of its UTF-8 as Go gives them.
export function marks(text, spans) {
  const s = String(text || '');
  if (!spans?.length) return s ? [[s, false]] : [];
  const bytes = new TextEncoder().encode(s), dec = new TextDecoder();
  const out = [];
  let at = 0;
  for (const [a, b] of [...spans].sort((x, y) => x[0] - y[0])) {
    const from = Math.max(at, Math.min(a, bytes.length)), to = Math.max(from, Math.min(b, bytes.length));
    if (from > at) out.push([dec.decode(bytes.subarray(at, from)), false]);
    if (to > from) out.push([dec.decode(bytes.subarray(from, to)), true]);
    at = to;
  }
  if (at < bytes.length) out.push([dec.decode(bytes.subarray(at)), false]);
  return out;
}

// ⚠️ How many tags a list row shows before it counts the rest.
const TAGS = 2;
export function tagsShown(tags) {
  const all = tags || [];
  return {shown: all.slice(0, TAGS), more: Math.max(0, all.length - TAGS)};
}

const call = (wire, machine, method, params) => wire.call('node.call', {machine, method, ...(params ? {params} : {})});

// query is a page of sessions.query: q as typed (host: picks machines), all (not only favorites), sort, limit rows after
// after; fresh has each node refresh its index first.
export function query(wire, {q = '', all = false, sort = '', limit = LIMIT, after = null, fresh = false} = {}) {
  return wire.call('sessions.query', {q, ...(all ? {all} : {}), ...(sort && sort !== sorts[0] ? {sort} : {}), limit, ...(after ? {after} : {}), ...(fresh ? {fresh} : {})});
}

// grep is sessions.grep: the text after > over every machine the viewer reads.
export const grep = (wire, {q, all = false}) => wire.call('sessions.grep', {q: searchText(q), ...(all ? {all} : {})});

// put changes row's record on its machine; the edit dialog says what it saw (expect, the record's updated_at): another
// answers stale.
export const put = (wire, row, patch, expect = '') => call(wire, row.machine, M.put, {...refIn(row), patch, ...(expect ? {expect} : {})});

// trash moves row's session into its machine's trash; restore puts it back. Both answer {title, files}.
export const trash = (wire, row) => call(wire, row.machine, M.trash, refIn(row));
export const restore = (wire, row) => call(wire, row.machine, M.restore, refIn(row));

// hits are the hits of q in row's session (remote.HitsResult).
export const hits = (wire, row, q) => call(wire, row.machine, M.hits, {...refIn(row), q: searchText(q), limit: HITS});

// readPage is the page of messages before (the end when < 0) of the file a page before came from (a file that changed
// since answers stale); find marks a message search's words in each (spans).
export const readPage = (wire, machine, ref, {before = -1, file = '', find = ''} = {}) =>
  call(wire, machine, M.messages, {...refIn(ref), before, n: PAGE, ...(file ? {file} : {}), ...(find ? {find: searchText(find)} : {})});

// readText is a message's full text, at its offset.
export const readText = (wire, machine, ref, off, file = '') => call(wire, machine, M.text, {...refIn(ref), off, ...(file ? {file} : {})}).then(r => r?.text || '');

// older adds page (newest first, as the node gives it) before the messages held (oldest first).
export const older = (held, page) => [...[...(page?.Msgs || [])].reverse(), ...held];

// cut says a message's text was shortened by the node: it holds fewer characters than it had.
export const cut = m => (m.Chars || 0) > [...(m.Text || '')].length;

// createPeople keeps the names people.names gave on this connection: ask(ids) asks once for those not asked yet; a new
// connection or a state reset forgets them, since the team may have changed. Without the method ids stay ids.
export function createPeople({wire, phase = null}) {
  const names = signal({});
  let asked = new Set();
  const forget = () => { asked = new Set(); names.value = {}; };
  wire.status?.subscribe?.(v => { if (v === 'connecting') forget(); });
  phase?.subscribe?.(v => { if (v === 'snapshot') forget(); });
  return {
    names,
    ask(ids) {
      const want = [...new Set(ids.filter(id => id && !asked.has(id)))].sort();
      if (!want.length || !wire.has?.('people.names')) return Promise.resolve();
      for (const id of want) asked.add(id);
      return wire.call('people.names', {ids: want}).then(r => { names.value = {...names.value, ...(r?.names || {})}; }, () => {
        for (const id of want) asked.delete(id);
      });
    },
  };
}

// ⚠️ internal/shell's quoting, which the TUI and the CLI type: POSIX sh and PowerShell (a Windows machine).
const posixPlain = /^[^ \t\n'"\\$`!*?[\]{}()<>|&;#~]+$/;
const psPlain = /^[^ \t\n'"`$&|;<>(){}@#,%]+$/;
export const quote = (s, ps) => (ps ? (s && psPlain.test(s) ? s : `'${s.replaceAll("'", "''")}'`)
  : (s && posixPlain.test(s) ? s : `'${s.replaceAll("'", "'\\''")}'`));

// line is argv typed in dir on a machine of os.
export function line(os, dir, argv) {
  const ps = os === 'windows';
  const parts = argv.map(a => quote(a, ps));
  if (ps && parts.length && parts[0] !== argv[0]) parts[0] = '& ' + parts[0];
  const cmd = parts.join(' ');
  if (!dir) return cmd;
  return ps ? `Set-Location -LiteralPath ${quote(dir, true)}; ${cmd}` : `cd ${quote(dir, false)} && ${cmd}`;
}

// resumeArgv is what resumes a session: a Claude background session attaches to its job; '' for an agent tend cannot
// resume.
export function resumeArgv(row) {
  if (row.provider === 'claude') return row.live?.BackgroundID ? ['claude', 'attach', row.live.BackgroundID] : ['claude', '--resume', row.session_id];
  if (row.provider === 'codex') return ['codex', 'resume', row.session_id];
  return null;
}

export function resumeLine(row, os) {
  const argv = resumeArgv(row);
  return argv ? line(os, row.cwd || '', argv) : '';
}
