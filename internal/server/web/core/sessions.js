// sessions reads one machine's own Claude / Codex sessions through node.call (its owner and admins): the list with who
// is running, the task whose run used each, a conversation paged from the newest, a message's full text, and the line
// that resumes a session there. No DOM.

// ⚠️ The node's session reads (internal/remote) and the size of a page of messages.
export const M = {list: 'list', live: 'live', messages: 'messages', text: 'text'};
export const PAGE = 40;

const call = (wire, machine, method, params) => wire.call('node.call', {machine, method, ...(params ? {params} : {})});

// keyOf names a session in the page's address: provider:session_id.
export const keyOf = s => `${s.provider}:${s.session_id}`;
export const refOf = key => {
  const i = key.indexOf(':');
  return i < 0 ? null : {provider: key.slice(0, i), session_id: key.slice(i + 1)};
};

// rows are the sessions of list newest first, each with live (its entry in live, or null); a running session the
// index has not seen yet is a row of what live tells of it.
export function rowsOf(list, live) {
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
  return rows.sort((a, b) => at(b) - at(a) || a.key.localeCompare(b.key));
}

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

// matches keeps the rows whose title, directory, agent, branch, id or linked task hold every word of q.
export function matches(rows, q) {
  const words = String(q || '').toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return rows;
  return rows.filter(r => {
    const s = [r.title, r.label, r.summary, r.cwd, r.provider, r.git_branch, r.session_id, r.task?.id, r.task?.title].filter(Boolean).join(' ').toLowerCase();
    return words.every(w => s.includes(w));
  });
}

// readList asks machine for its sessions and who of them is running.
export async function readList(wire, machine) {
  const [list, live] = await Promise.all([call(wire, machine, M.list), call(wire, machine, M.live).catch(() => ({live: {}}))]);
  return rowsOf(list, live?.live || {});
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
