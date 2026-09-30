// changes is the changes tab of a task and of a run: what a run changed, file by file in directory order with its
// +a −d, filtered to the agent tools' own or the generated files. A file opens on its diff, in pages of hunks, with
// both sides' line numbers, more context on asking, and side by side on a wide desktop. Big, generated and binary
// files start folded.
import {useState, useEffect, useRef, useMemo} from '../vendor/hooks.mjs';
import {signal} from '../vendor/signals-core.mjs';
import {html, cx, usePhone, useWords, useSignalValue} from '../ui/base.js';
import {Button, Chip, Chips, Segmented} from '../ui/controls.js';
import * as ch from '../core/changes.js';
import {clock} from '../core/format.js';
import {openStates} from '../core/select.js';
import {register} from '../core/i18n.js';
import {code} from '../core/proto.js';

register('changes', {
  'chg.label': ['改动', 'Changes'], 'chg.files': ['%d 个文件', 'Files: %d'], 'chg.filter': ['筛选改动', 'Filter the changes'],
  'chg.f.all': ['全部', 'All'], 'chg.f.agent': ['只看 agent 用工具改的', 'Only the agent\'s tools'], 'chg.f.generated': ['生成的文件', 'Generated files'],
  'chg.binary': ['二进制 · %s', 'Binary · %s'], 'chg.big': ['改动很大：%s', 'Large change: %s'], 'chg.generated': ['生成的', 'generated'],
  'chg.shown': ['显示了 %d / %d 处', 'Showing %d of %d hunks'], 'chg.next': ['显示后面 %d 处', 'Show the next %d hunks'],
  'chg.goOn': ['这一处没显示完，接着显示', 'This hunk goes on: show the rest'],
  'chg.context': ['多看上下文', 'More context'], 'chg.contextNow': ['上下文 %d 行', 'Context: %d lines'],
  'chg.view': ['版式', 'Layout'], 'chg.v.unified': ['上下对照', 'Unified'], 'chg.v.split': ['左右对照', 'Side by side'],
  'chg.at': ['截至 %s', 'As of %s'], 'chg.refresh': ['刷新', 'Refresh'],
  'chg.outside': ['只包含 agent 用工具改的，命令改的看不到', 'Only what the agent changed with its tools: changes made by commands are not seen'],
  'chg.hidden': ['这段时间这个目录里还有 %d 个文件变了，只有机器主人能看', '%d more files changed in this directory meanwhile: only the machine\'s owner can see them'],
  'chg.gone': ['这次运行的改动已经清理', 'This run\'s changes were cleared'], 'chg.hunkGone': ['内容已清理，只剩统计', 'The content was cleared: only the counts are left'],
  'chg.unsupported': ['这台 server 还不能看改动', 'This server cannot show changes yet'], 'chg.none': ['没有改动', 'Nothing changed'],
  'chg.loading': ['正在读取改动…', 'Reading the changes…'], 'chg.failed': ['读不到改动：%s', 'Cannot read the changes: %s'],
  'chg.moved': ['工作区又变了，重读一次', 'The workspace moved on: read it again'], 'chg.run': ['看哪次运行的改动', 'Which run\'s changes'],
  'chg.notYet': ['运行开始后才有改动', 'Changes show once the run starts'],
  'chg.noHunk': ['这个文件没有可显示的改动行', 'No changed lines to show for this file'],
});

// ⚠️ A desktop opens this many unfolded files on its own, fetching their first pages; a phone opens none.
export const AUTO_OPEN = 8;
// ⚠️ Side by side is offered where the tab is at least this wide (px).
export const SPLIT_WIDTH = 880;

const unified = signal('unified');

const num = n => Number(n || 0).toLocaleString('en-US');
const size = b => (b >= 1024 * 1024 ? (b / 1024 / 1024).toFixed(1) + ' MB' : b >= 1024 ? Math.round(b / 1024) + ' KB' : (b || 0) + ' B');

function Counts({add, del}) {
  return html`<span class="chg-n mono"><span class="t-success">+${num(add)}</span> <span class="t-failed">−${num(del)}</span></span>`;
}

// useWidth is the element's width as it is laid out (0 where nothing lays it out).
function useWidth(ref) {
  const [w, setW] = useState(0);
  useEffect(() => {
    const el = ref.current, RO = globalThis.ResizeObserver;
    if (!el || !RO) return;
    const o = new RO(es => setW(es[0].contentRect.width));
    o.observe(el);
    return () => o.disconnect();
  }, []);
  return w;
}

const byHunk = (items, h) => {
  const out = [];
  for (const x of items) {
    const k = h(x);
    if (!out.length || out.at(-1).h !== k) out.push({h: k, items: []});
    out.at(-1).items.push(x);
  }
  return out;
};

function Line({r, side = ''}) {
  if (!r) return html`<div class="dv-row dv-none"><span class="dv-g"></span><span class="dv-t"> </span></div>`;
  if (r.kind === 'hunk') return html`<div class="dv-row dv-hunk"><span class="dv-g"></span><span class="dv-t">${r.text}</span></div>`;
  return html`<div class=${cx('dv-row', r.kind !== 'ctx' && 'dv-' + r.kind)}>
    <span class="dv-g mono">${side !== 'r' && html`<span class="dv-n">${r.old || ''}</span>`}${side !== 'l' && html`<span class="dv-n">${r.new || ''}</span>`}</span><span class="dv-t">${r.text}</span></div>`;
}

// DiffLines draws a file's rows, unified or side by side (split), each hunk a block the browser skips while it is out
// of sight.
export function DiffLines({rows, split = false}) {
  if (!split) {
    const hunks = byHunk(rows.map((r, i) => ({r, i})), x => x.r.h);
    return html`<div class="dv mono"><div class="dv-col">${hunks.map(g => html`<div class="dv-h" key=${g.h}>
      ${g.items.map(({r, i}) => html`<${Line} key=${i} r=${r} />`)}</div>`)}</div></div>`;
  }
  const hunks = byHunk(ch.sides(rows), p => rows[p.l >= 0 ? p.l : p.r].h);
  const col = side => html`<div class="dv-col">${hunks.map(g => html`<div class="dv-h" key=${g.h}>
    ${g.items.map((p, j) => { const i = p[side]; const r = i >= 0 ? rows[i] : null;
      return html`<${Line} key=${j} r=${r && r.kind === 'hunk' && side === 'r' ? {kind: 'hunk', text: ''} : r} side=${side} />`; })}</div>`)}</div>`;
  return html`<div class="dv dv-split mono">${col('l')}${col('r')}</div>`;
}

function FileBody({f, diff, split, onMore, onContext}) {
  const {t, f: fmt} = useWords();
  const rows = useMemo(() => (diff?.hunks ? ch.rowsOf(diff.hunks) : []), [diff?.hunks]);
  if (!diff || (diff.loading && !diff.hunks)) return html`<p class="chg-note t-muted">${t('chg.loading')}</p>`;
  if (diff.error && !diff.hunks) return html`<p class="chg-note t-muted">${diff.error === code.gone ? t('chg.hunkGone') : diff.error === code.snapshotChanged ? t('chg.moved') : fmt('chg.failed', diff.error)}</p>`;
  if (!rows.length) return html`<p class="chg-note t-muted">${t('chg.noHunk')}</p>`;
  const shown = diff.hunks.length - (diff.next?.line ? 1 : 0);
  const left = Math.min(ch.HUNKS, diff.of - shown);
  const context = f.op === 'modify' || f.op === 'rename';
  return html`<div class="chg-body">
    <${DiffLines} rows=${rows} split=${split} />
    <div class="chg-foot">
      ${diff.of > 1 && html`<span class="t-muted">${fmt('chg.shown', shown, diff.of)}</span>`}
      ${diff.next && html`<${Button} kind="quiet" disabled=${diff.loading} onClick=${onMore}>${diff.next.line ? t('chg.goOn') : fmt('chg.next', left)}<//>`}
      ${context && html`<${Button} kind="quiet" disabled=${diff.loading} onClick=${onContext}>${t('chg.context')}<//><span class="t-muted">${fmt('chg.contextNow', diff.context)}</span>`}
      ${diff.error && html`<span class="t-muted">${diff.error === code.snapshotChanged ? t('chg.moved') : fmt('chg.failed', diff.error)}</span>`}
    </div>
  </div>`;
}

function FileRow({f, open, diff, split, onToggle, onMore, onContext}) {
  const {t, f: fmt} = useWords();
  const name = f.op === 'rename' && f.from ? `${f.from} → ${f.path}` : f.path;
  const binary = f.binary && fmt('chg.binary', f.old_bytes ? `${size(f.old_bytes)} → ${size(f.bytes)}` : size(f.bytes));
  return html`<li class=${cx('chg-file', open && 'open')}>
    <button type="button" class="chg-row" aria-expanded=${f.binary ? undefined : open ? 'true' : 'false'} disabled=${f.binary} onClick=${() => onToggle(f.path)}>
      <span class="chg-glyph mono" aria-hidden="true">${f.binary ? '·' : open ? '▾' : '▸'}</span>
      <span class=${cx('chg-op mono', f.op === 'add' && 'op-add', f.op === 'delete' && 'op-delete')}>${ch.glyph(f.op)}</span>
      <span class="chg-path mono ell" title=${name}>${name}</span>
      ${f.generated && html`<span class="chip">${t('chg.generated')}</span>`}
      ${binary ? html`<span class="chg-n t-muted">${binary}</span>`
        : f.big && !open ? html`<span class="chg-n t-muted">${fmt('chg.big', `+${num(f.add)} −${num(f.del)}`)}</span>`
        : html`<${Counts} add=${f.add} del=${f.del} />`}
    </button>
    ${open && !f.binary && html`<${FileBody} f=${f} diff=${diff} split=${split}
      onMore=${() => onMore(f.path)} onContext=${() => onContext(f.path)} />`}
  </li>`;
}

// Changes draws a run's changes: data is its list (core/changes.js list), error why there is none, unsupported an older
// server; open is the files shown open and diffs what they show ({hunks, of, next, context, loading?, error?}); running
// says the list is the workspace as it was at data.at, refreshed with onRefresh; runs and run pick which run. view is
// the layout asked for (unified or split, onView changes it; side by side only where wide); onMore(path) takes a
// file's next page, onContext(path) more context around its changes.
export function Changes({data = null, error = '', unsupported = false, running = false, open = new Set(), diffs = {}, onToggle, onRefresh,
  runs = [], run = '', onRun, view = 'unified', onView, wide = false, onMore, onContext}) {
  const {t, f} = useWords();
  const phone = usePhone();
  const [filter, setFilter] = useState('all');
  const split = !phone && wide && view === 'split';
  const picker = runs.length > 1 && html`<${Segmented} label=${t('chg.run')} value=${run} onChange=${onRun}
    options=${runs.map(r => ({value: r.id, label: r.id}))} />`;
  const body = () => {
    if (unsupported) return html`<p class="empty">${t('chg.unsupported')}</p>`;
    if (error) return html`<p class="empty">${error === code.gone ? t('chg.gone') : error === 'not_started' ? t('chg.notYet') : f('chg.failed', error)}</p>`;
    if (!data) return html`<p class="empty t-muted">${t('chg.loading')}</p>`;
    const c = ch.counts(data.files);
    const groups = ch.ordered(ch.filtered(data.files, filter));
    return html`
      <div class=${cx('chg-head', phone && 'chg-head-phone')}>
        <span class="chg-sum">${f('chg.files', data.total.files)} <${Counts} add=${data.total.add} del=${data.total.del} /></span>
        <${Chips} label=${t('chg.filter')}>${ch.filters.map(x => html`<${Chip} label=${t('chg.f.' + x)} count=${c[x]} on=${filter === x} onClick=${() => setFilter(x)} />`)}<//>
        ${!phone && wide && html`<${Segmented} label=${t('chg.view')} value=${view} onChange=${onView}
          options=${['unified', 'split'].map(v => ({value: v, label: t('chg.v.' + v)}))} />`}
        ${running && html`<span class="chg-at"><span class="t-muted">${f('chg.at', clock(data.at))}</span><${Button} kind="quiet" onClick=${onRefresh}>${t('chg.refresh')}<//></span>`}
      </div>
      ${!data.git && html`<p class="chg-warn">${t('chg.outside')}</p>`}
      ${data.hidden > 0 && html`<p class="chg-warn">${f('chg.hidden', data.hidden)}</p>`}
      ${!groups.length ? html`<p class="empty">${t('chg.none')}</p>` : html`<div class="chg-list">${groups.map(g => html`<section class="chg-group" key=${g.dir}>
        ${g.dir && html`<h4 class="chg-dir mono">${g.dir}/</h4>`}
        <ul class="chg-files">${g.files.map(x => html`<${FileRow} key=${x.path} f=${x} open=${open.has(x.path)} diff=${diffs[x.path]} split=${split}
          onToggle=${onToggle} onMore=${onMore} onContext=${onContext} />`)}</ul>
      </section>`)}</div>`}`;
  };
  return html`<section class="chg" aria-label=${t('chg.label')}>
    ${picker && html`<div class="chg-runs">${picker}</div>`}
    ${body()}
  </section>`;
}

// RunChanges loads the changes of one of runs (the first unless picked) through changes (core/changes.js) and draws
// them; a running run's are read again when its state moves and on asking. prefs keeps the layout.
export function RunChanges({changes, runs, prefs = null}) {
  const phone = usePhone();
  const [picked, setPicked] = useState('');
  const run = runs.find(r => r.id === picked) || runs[0];
  const [got, setGot] = useState({});
  const [open, setOpen] = useState(() => new Set());
  const [diffs, setDiffs] = useState({});
  const [tick, setTick] = useState(0);
  const seq = useRef(0);
  const box = useRef(null);
  const wide = useWidth(box) >= SPLIT_WIDTH;
  const view = useSignalValue(prefs?.diff || unified);
  const running = !!run && openStates.includes(run.state);
  const ended = !!run && !running;
  const can = changes.can();
  useEffect(() => {
    if (!run || !can) return;
    if (run.state === 'queued') { setGot({error: 'not_started'}); return; }
    const n = ++seq.current;
    changes.list(run.id, {ended}).then(data => {
      if (n !== seq.current) return;
      setGot({data});
      setDiffs({});
      if (!phone) setOpen(new Set(data.files.filter(x => !ch.folded(x)).slice(0, AUTO_OPEN).map(x => x.path)));
    }, e => n === seq.current && setGot({error: e.code || String(e.message || e)}));
  }, [run?.id, run?.state, tick, can]);

  // fetch asks for a page of path's diff from `from` with context lines, adding it to what it shows (restart: in its
  // place); a list moved on under a running run is read again.
  const fetch = (path, from, context, restart) => {
    const data = got.data, n = seq.current;
    setDiffs(d => ({...d, [path]: {...(restart ? {} : d[path]), context, loading: true, error: ''}}));
    changes.diff(run.id, path, data.snapshot, {...from, context}).then(page => {
      if (n !== seq.current) return;
      setDiffs(d => {
        const pages = [...(restart ? [] : d[path]?.pages || []), {from, page}];
        return {...d, [path]: {...ch.joined(pages), pages, context}};
      });
    }, e => {
      if (n !== seq.current) return;
      setDiffs(d => ({...d, [path]: {...d[path], loading: false, error: e.code || 'error'}}));
      if (e.code === code.snapshotChanged && running) setTick(x => x + 1);
    });
  };
  useEffect(() => {
    const data = got.data;
    if (!data) return;
    for (const path of open) {
      if (diffs[path]) continue;
      const file = data.files.find(x => x.path === path);
      if (!file || file.binary) continue;
      fetch(path, {hunk: 0}, ch.CONTEXT, true);
    }
  }, [got.data, [...open].join('\n')]);
  const toggle = path => setOpen(o => { const s = new Set(o); if (s.has(path)) s.delete(path); else s.add(path); return s; });
  const more = path => { const d = diffs[path]; if (d?.next && !d.loading) fetch(path, d.next, d.context, false); };
  const context = path => { const d = diffs[path]; if (d && !d.loading) fetch(path, {hunk: 0}, d.context + ch.MORE_CONTEXT, true); };
  if (!run) return null;
  return html`<div class="chg-box" ref=${box}><${Changes} data=${got.data || null} error=${got.error || ''} unsupported=${!can} running=${running} open=${open} diffs=${diffs}
    onToggle=${toggle} onRefresh=${() => setTick(n => n + 1)} runs=${runs} run=${run.id} onRun=${id => { setPicked(id); setGot({}); setOpen(new Set()); }}
    view=${view} onView=${v => prefs?.setDiff(v)} wide=${wide} onMore=${more} onContext=${context} /></div>`;
}
