// answer is the preview's fake server: the answers the frame files give by request, and a socket that sends them.
// answers maps each request method of the frame files to what the server sent for it: the result, then the pushes on
// its stream, remapped to the ids the page uses; a method asked of a run is answered per run, and
// a run the files do not ask of as the first one they do (with the same page or path, and a diff's same hunk, line and
// context).
export const sets = {home: ['home-state', 'output-page', 'home-commands', 'changes-gone'],
  tasks: ['tasks-state', 'tasks-create', 'tasks-dispatch', 'tasks-plan', 'tasks-acts', 'tasks-board', 'changes-list'],
  output: ['output-state', 'output-conv', 'output-item', 'output-send', 'output-answer', 'changes-list'],
  carry: ['output-state', 'output-conv', 'output-item', 'output-send', 'output-carry', 'changes-list'],
  gone: ['output-state', 'output-conv', 'output-item', 'output-answer-gone', 'changes-list'],
  team: ['team-state', 'team-share', 'team-project', 'team-settings'],
  agents: ['agents-state', 'agents-edit', 'agents-share']};
// joins are the files whose pushes on a stream an earlier file opened go on that stream, after what it pushed there.
const joins = new Set(['output-send', 'output-carry']);
const keyOf = (f, run = true) => [f.method, run && f.params?.run, f.params?.after, f.params?.path, f.params?.id, ...['hunk', 'line', 'context', 'ignore_space'].map(k => f.params?.[k] && k + f.params[k])]
  .filter(Boolean).join(' ');

export async function answers(names, text) {
  const out = {}, opened = {};
  for (const name of names) {
    const asked = {};
    for (const l of (await text(name)).split('\n').filter(Boolean).map(l => JSON.parse(l))) {
      if (l.c?.type === 'req') {
        const k = keyOf(l.c);
        asked[l.c.id] = k;
        out[k] ||= {res: null, pushes: []};
        out[keyOf(l.c, false)] ||= out[k];
        out[l.c.method] ||= out[k];
      }
      const f = l.s, m = f && asked[f.id];
      if (!m && f?.type === 'push' && joins.has(name) && opened[f.id]) out[opened[f.id]].pushes.push(f);
      if (!m || out[m].done) continue;
      if (f.type === 'res') out[m].res = f;
      else if (f.type === 'push') out[m].pushes.push(f);
    }
    for (const m of Object.values(out)) if (m.res || m.pushes.length) m.done = true;
    Object.assign(opened, asked);
  }
  return out;
}

// resumed is what a stream pushes to a watch asked from a cursor: as the coordinator does, the batches after the one that
// cursor ends, under an open that resumes from it; every push when no batch ends there.
function resumed(pushes, from) {
  const at = from ? pushes.findLastIndex(p => p.params?.cursor?.file === from.file && p.params.cursor.off === from.off && p.method !== 'open') : -1;
  if (at < 0) return pushes;
  return [{type: 'push', method: 'open', params: {cursor: from, mode: 'resume'}}, ...pushes.slice(at + 1).filter(p => p.method !== 'open')];
}

export function socket(table) {
  const s = {
    send(data) {
      for (const line of data.split('\n').filter(Boolean)) {
        const f = JSON.parse(line);
        if (f.type !== 'req') continue;
        const a = table[keyOf(f)] || table[keyOf(f, false)];
        setTimeout(() => {
          if (!a) { s.reply({type: 'res', id: f.id, result: {}}); return; }
          if (a.res) s.reply({...a.res, id: f.id});
          for (const p of resumed(a.pushes, f.params?.from)) s.reply({...p, id: f.id});
        }, 30);
      }
    },
    reply: f => s.onmessage?.({data: JSON.stringify(f) + '\n'}),
    close() { setTimeout(() => s.onclose?.({}), 0); },
  };
  setTimeout(() => s.onopen?.({}), 10);
  return s;
}
