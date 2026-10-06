// sessions reads one machine's own Claude / Codex sessions through node.call (its owner and admins): the list with who
// is running, the project each belongs to (through sessions.list, where the server has it), the task whose run used
// each, its favorites and tags, a conversation paged from the newest, a message's full text, and the line
// that resumes a session there. No DOM.

// ⚠️ The node's session reads (internal/remote) and the size of a page of messages.
export const M = {list: 'list', live: 'live', messages: 'messages', text: 'text'};
export const PAGE = 40;

// ⚠️ The address's value for the sessions in no project: never a project id (the coordinator refuses it as one).
export const NONE = 'none';

// opened resolves once wire has connected or failed to: which methods its server has is known then.
const opened = wire => new Promise(resolve => {
  let off = null, done = false;
  off = wire.status?.subscribe?.(v => {
    if (done || v === 'connecting' || v === 'idle') return;
    done = true;
    queueMicrotask(() => off?.());
    resolve();
  });
  if (!off) resolve();
});

const call = (wire, machine, method, params) => wire.call('node.call', {machine, method, ...(params ? {params} : {})});

// keyOf names a session in the page's address: provider:session_id.
export const keyOf = s => `${s.provider}:${s.session_id}`;
export const refOf = key => {
  const i = key.indexOf(':');
  return i < 0 ? null : {provider: key.slice(0, i), session_id: key.slice(i + 1)};
};

// rows are the sessions of list newest first, each with live (its entry in live, or null) and project (its id in
// projects, by key, or ''); a running session the index has not seen yet is a row of what live tells of it.
export function rowsOf(list, live, projects = {}) {
  const seen = new Set();
  const rows = (list?.sessions || []).map(s => {
    seen.add(s.session_id);
    return {...s, key: keyOf(s), live: live?.[s.session_id] || null};
  });
  for (const [id, l] of Object.entries(live || {})) {
    if (seen.has(id) || !l.Agent) continue;
    rows.push({provider: l.Agent, session_id: id, title: l.Title || '', cwd: l.Cwd || '', last_at: l.Since || '', key: `${l.Agent}:${id}`, live: l});
  }
  const at = r => Date.parse(r.last_at || r.updated_at || '') || 0;
  for (const r of rows) r.project = projects?.[r.key] || '';
  return rows.sort((a, b) => at(b) - at(a) || a.key.localeCompare(b.key));
}

// inProject keeps the rows of project: every row for '', those in no project for NONE.
export const inProject = (rows, project) => (!project ? rows : rows.filter(r => (project === NONE ? !r.project : r.project === project)));

// links: by session id, the task of the newest run that used it, from the state as the store holds it.
export function links(state) {
  const newest = {};
  for (const r of Object.values(state?.runs || {})) {
    if (!r.session || !state.tasks?.[r.task]) continue;
    const o = newest[r.session];
    if (!o || (r.seq || 0) > (o.seq || 0) || (r.seq || 0) === (o.seq || 0) && String(r.queued_at || '') > String(o.queued_at || '')) newest[r.session] = r;
  }
  const out = {};
  for (const [s, r] of Object.entries(newest)) {
    const t = state.tasks[r.task];
    out[s] = {id: t.id, title: t.title || '', stage: t.stage || ''};
  }
  return out;
}

// linked gives each row task: its entry in links, or null.
export const linked = (rows, ls) => rows.map(r => ({...r, task: ls[r.session_id] || null}));

// favorites are the rows favorited, archived or not.
export const favorites = rows => rows.filter(r => r.favorited_at);

// ⚠️ How many tags a list row shows before it counts the rest.
const TAGS = 2;
export function tagsShown(tags) {
  const all = tags || [];
  return {shown: all.slice(0, TAGS), more: Math.max(0, all.length - TAGS)};
}

// matches keeps the rows whose title, summary, tags, directory, agent, branch, id or linked task hold every word of q; a
// word may start with # as a tag is written.
export function matches(rows, q) {
  const words = String(q || '').toLowerCase().split(/\s+/).map(w => w.replace(/^#+/, '')).filter(Boolean);
  if (!words.length) return rows;
  return rows.filter(r => {
    const s = [r.title, r.label, r.summary, ...(r.tags || []), r.cwd, r.provider, r.git_branch, r.session_id, r.task?.id, r.task?.title].filter(Boolean).join(' ').toLowerCase();
    return words.every(w => s.includes(w));
  });
}

// readList asks machine for its sessions and who of them is running, once the connection is open: through
// sessions.list with their projects (byProject), or of its node where the server lacks it.
export async function readList(wire, machine) {
  await opened(wire);
  if (wire.has('sessions.list')) {
    const r = await wire.call('sessions.list', {machine});
    return {rows: rowsOf(r, r?.live || {}, r?.projects || {}), byProject: true};
  }
  const [list, live] = await Promise.all([call(wire, machine, M.list), call(wire, machine, M.live).catch(() => ({live: {}}))]);
  return {rows: rowsOf(list, live?.live || {}), byProject: false};
}

// readPage is the page of messages before (the end when < 0) of the file a page before came from; a file that changed
// since answers stale.
export const readPage = (wire, machine, ref, before = -1, file = '') =>
  call(wire, machine, M.messages, {...ref, before, n: PAGE, ...(file ? {file} : {})});

// readText is a message's full text, at its offset.
export const readText = (wire, machine, ref, off, file = '') => call(wire, machine, M.text, {...ref, off, ...(file ? {file} : {})}).then(r => r?.text || '');

// older adds page (newest first, as the node gives it) before the messages held (oldest first).
export const older = (held, page) => [...[...(page?.Msgs || [])].reverse(), ...held];

// cut says a message's text was shortened by the node: it holds fewer characters than it had.
export const cut = m => (m.Chars || 0) > [...(m.Text || '')].length;

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
