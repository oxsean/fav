// changes is the changes tab of a task and of a run: what a run changed, file by file in directory order with its
// +a −d, filtered to the agent tools' own or the generated files. A file opens on its diff, in pages of hunks, with
// both sides' line numbers, more context on asking, whitespace ignored when the viewer asks, and side by side on a wide
// desktop. Big, generated and binary
// files start folded. Lines picked by their numbers, by the keyboard or by selecting their text make a quote for the
// agent's box or the send-back notes.
import {useState, useEffect, useRef, useMemo} from '../vendor/hooks.mjs';
import {signal} from '../vendor/signals-core.mjs';
import {html, cx, usePhone, useWords, useSignalValue, useActions} from '../ui/base.js';
import {Button, Check, Chip, Chips, Segmented} from '../ui/controls.js';
import * as ch from '../core/changes.js';
import {clock} from '../core/format.js';
import {openStates} from '../core/select.js';
import {register} from '../core/i18n.js';
import {code} from '../core/proto.js';

register('changes', {
  'chg.label': ['改动', 'Changes'], 'chg.files': ['%d 个文件', 'Files: %d'], 'chg.filter': ['筛选改动', 'Filter the changes'],
  'chg.f.all': ['全部', 'All'], 'chg.f.agent': ['只看 agent 用工具改的', 'Only the agent\'s tools'], 'chg.f.generated': ['生成的文件', 'Generated files'],
  'chg.binary': ['二进制 · %s', 'Binary · %s'], 'chg.big': ['改动很大：%s', 'Large change: %s'], 'chg.generated': ['生成的', 'generated'],
  'chg.shown': ['显示了 %d / %d 处', 'Showing %d of %d {hunk|hunks}'], 'chg.next': ['显示后面 %d 处', 'Show the next %d {hunk|hunks}'],
  'chg.goOn': ['这一处没显示完，接着显示', 'This hunk goes on: show the rest'],
  'chg.context': ['多看上下文', 'More context'], 'chg.contextNow': ['上下文 %d 行', 'Context: %d {line|lines}'],
  'chg.view': ['版式', 'Layout'], 'chg.v.unified': ['上下对照', 'Unified'], 'chg.v.split': ['左右对照', 'Side by side'],
  'chg.pickHint': ['点行号选中一行，再点另一行选到那里；拖选几行也行，或者用 j / k、Space 和 Shift+↑ / ↓', 'Tap a line number to pick it, then another to reach it; or select the lines, or use j / k, Space and Shift+↑ / ↓'],
  'chg.pickHintPhone': ['点行号选中一行，再点另一行选到那里；也可以长按拖选几行', 'Tap a line number to pick it, then another to reach it; or press and drag to select the lines'],
  'chg.pickLine': ['选行', 'Pick the line'], 'chg.picked': ['选了 %d 行', 'Lines picked: %d'], 'chg.pickMore': ['再点一行可以选到那里', 'Tap another line to reach it'],
  'chg.toAgent': ['发给 agent', 'Send to the agent'], 'chg.toNotes': ['写进退回意见', 'Add to the send-back notes'], 'chg.unpick': ['取消', 'Cancel'],
  'chg.q.before': ['%s（改之前）', '%s (before the change)'], 'chg.q.more': ['……另有 %d 行没有引用', '… %d more {line|lines} not quoted'],
  'chg.at': ['截至 %s', 'As of %s'], 'chg.refresh': ['刷新', 'Refresh'],
  'chg.outside': ['只包含 agent 用工具改的，命令改的看不到', 'Only what the agent changed with its tools: changes made by commands are not seen'],
  'chg.hidden': ['这段时间这个目录里还有 %d 个文件变了，只有机器主人能看', '%d more {file|files} changed in this directory meanwhile: only the machine\'s owner can see {it|them}'],
  'chg.gone': ['这次运行的改动已经清理', 'This run\'s changes were cleared'], 'chg.hunkGone': ['内容已清理，只剩统计', 'The content was cleared: only the counts are left'],
  'chg.unsupported': ['这台 server 还不能看改动', 'This server cannot show changes yet'], 'chg.none': ['没有改动', 'Nothing changed'],
  'chg.loading': ['正在读取改动…', 'Reading the changes…'], 'chg.failed': ['读不到改动：%s', 'Cannot read the changes: %s'],
  'chg.moved': ['工作区又变了，重读一次', 'The workspace moved on: read it again'], 'chg.run': ['看哪次运行的改动', 'Which run\'s changes'],
  'chg.notYet': ['运行开始后才有改动', 'Changes show once the run starts'],
  'chg.noHunk': ['这个文件没有可显示的改动行', 'No changed lines to show for this file'],
  'chg.space': ['忽略空白', 'Ignore whitespace'], 'chg.onlySpace': ['只有空白改动，忽略空白时不显示', 'Only whitespace changed: not shown while whitespace is ignored'],
  'chg.spaceOld': ['这台机器上的 tend 太旧，不能忽略空白', 'The tend on this machine is too old to ignore whitespace'],
});

// ⚠️ A desktop opens this many unfolded files on its own, fetching their first pages; a phone opens none.
export const AUTO_OPEN = 8;
// ⚠️ Side by side is offered where the tab is at least this wide (px).
export const SPLIT_WIDTH = 880;

const unified = signal('unified');
const keepSpace = signal(false);

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

// Line is one row of a diff; data-row names the rows it stands for, where a text selection starts and ends.
function Line({r, lo, hi, on, cur, side = ''}) {
  if (!r) return html`<div class="dv-row dv-none" data-row=${lo + ':' + hi}><span class="dv-g"></span><span class="dv-t"> </span></div>`;
  if (r.kind === 'hunk') return html`<div class="dv-row dv-hunk" data-row=${lo + ':' + hi}><span class="dv-g"></span><span class="dv-t">${r.text}</span></div>`;
  return html`<div class=${cx('dv-row', r.kind !== 'ctx' && 'dv-' + r.kind, on && 'on', cur && 'cur')} data-row=${lo + ':' + hi}>
    <span class="dv-g mono" data-lo=${lo} data-hi=${hi}>${side !== 'r' && html`<span class="dv-n">${r.old || ''}</span>`}${side !== 'l' && html`<span class="dv-n">${r.new || ''}</span>`}</span><span class="dv-t">${r.text}</span></div>`;
}

// DiffLines draws a file's rows, unified or side by side (split), each hunk a block the browser skips while it is out
// of sight; sel is the picked rows ({a, b}), cur the keyboard's ({lo, hi}); onPick(lo, hi, extend) is a tap on a line's
// numbers.
export function DiffLines({rows, split = false, sel = null, cur = null, onPick}) {
  const on = (lo, hi) => !!sel && lo <= sel.b && hi >= sel.a;
  const isCur = (lo, hi) => !!cur && cur.lo === lo && cur.hi === hi;
  const tap = e => {
    for (let n = e.target; n && n !== e.currentTarget; n = n.parentNode) {
      const lo = n.getAttribute?.('data-lo');
      if (lo !== null && lo !== undefined) { onPick?.(Number(lo), Number(n.getAttribute('data-hi')), !!e.shiftKey); return; }
    }
  };
  if (!split) {
    const hunks = byHunk(rows.map((r, i) => ({r, i})), x => x.r.h);
    return html`<div class="dv mono" onClick=${tap}><div class="dv-col">${hunks.map(g => html`<div class="dv-h" key=${g.h}>
      ${g.items.map(({r, i}) => html`<${Line} key=${i} r=${r} lo=${i} hi=${i} on=${on(i, i)} cur=${isCur(i, i)} />`)}</div>`)}</div></div>`;
  }
  const hunks = byHunk(ch.sides(rows), p => rows[p.lo].h);
  const col = side => html`<div class="dv-col">${hunks.map(g => html`<div class="dv-h" key=${g.h}>
    ${g.items.map(p => { const i = p[side]; const r = i >= 0 ? rows[i] : null;
      return html`<${Line} key=${p.lo + ':' + p.hi} r=${r && r.kind === 'hunk' && side === 'r' ? {kind: 'hunk', text: ''} : r} lo=${p.lo} hi=${p.hi} on=${on(p.lo, p.hi)} cur=${isCur(p.lo, p.hi)} side=${side} />`; })}</div>`)}</div>`;
  return html`<div class="dv dv-split mono" onClick=${tap}>${col('l')}${col('r')}</div>`;
}

function Picked({rows, sel, notes, onQuote, onClear}) {
  const {t, f} = useWords();
  const n = rows.slice(sel.a, sel.b + 1).filter(r => r.kind !== 'hunk').length;
  return html`<div class="chg-pick" role="group" aria-label=${f('chg.picked', n)}>
    <span class="chg-pick-n">${f('chg.picked', n)}${sel.one ? html` <span class="t-muted">${t('chg.pickMore')}</span>` : ''}</span>
    <span class="chg-pick-acts">
      <${Button} kind="primary" onClick=${() => onQuote('agent')}>${t('chg.toAgent')}<//>
      ${notes && html`<${Button} onClick=${() => onQuote('notes')}>${t('chg.toNotes')}<//>`}
      <${Button} kind="quiet" onClick=${onClear}>${t('chg.unpick')}<//>
    </span>
  </div>`;
}

function FileBody({f, diff, rows, split, sel, cur, notes, onMore, onContext, onPick, onQuote, onClear}) {
  const {t, f: fmt} = useWords();
  if (!diff || (diff.loading && !diff.hunks)) return html`<p class="chg-note t-muted">${t('chg.loading')}</p>`;
  if (diff.error && !diff.hunks) return html`<p class="chg-note t-muted">${diff.error === code.gone ? t('chg.hunkGone') : diff.error === code.snapshotChanged ? t('chg.moved') : fmt('chg.failed', diff.error)}</p>`;
  if (!rows.length) return html`<p class="chg-note t-muted">${t(ch.onlySpace(f, diff) ? 'chg.onlySpace' : 'chg.noHunk')}</p>`;
  const shown = diff.hunks.length - (diff.next?.line ? 1 : 0);
  const left = Math.min(ch.HUNKS, diff.of - shown);
  const context = f.op === 'modify' || f.op === 'rename';
  return html`<div class="chg-body">
    <${DiffLines} rows=${rows} split=${split} sel=${sel} cur=${cur} onPick=${onPick} />
    <div class="chg-foot">
      ${diff.of > 1 && html`<span class="t-muted">${fmt('chg.shown', shown, diff.of)}</span>`}
      ${diff.next && html`<${Button} kind="quiet" disabled=${diff.loading} onClick=${onMore}>${diff.next.line ? t('chg.goOn') : fmt('chg.next', left)}<//>`}
      ${context && html`<${Button} kind="quiet" disabled=${diff.loading} onClick=${onContext}>${t('chg.context')}<//><span class="t-muted">${fmt('chg.contextNow', diff.context)}</span>`}
      ${diff.error && html`<span class="t-muted">${diff.error === code.snapshotChanged ? t('chg.moved') : fmt('chg.failed', diff.error)}</span>`}
    </div>
    ${sel && html`<${Picked} rows=${rows} sel=${sel} notes=${notes} onQuote=${to => onQuote(to, rows)} onClear=${onClear} />`}
  </div>`;
}

function FileRow({f, open, diff, rows, split, sel, cur, notes, onToggle, onMore, onContext, onPick, onQuote, onClear}) {
  const {t, f: fmt} = useWords();
  const name = f.op === 'rename' && f.from ? `${f.from} → ${f.path}` : f.path;
  const binary = f.binary && fmt('chg.binary', f.old_bytes ? `${size(f.old_bytes)} → ${size(f.bytes)}` : size(f.bytes));
  return html`<li class=${cx('chg-file', open && 'open')} data-path=${f.path}>
    <button type="button" class=${cx('chg-row', cur?.lo === -1 && 'cur')} aria-expanded=${f.binary ? undefined : open ? 'true' : 'false'} disabled=${f.binary} onClick=${() => onToggle(f.path)}>
      <span class="chg-glyph mono" aria-hidden="true">${f.binary ? '·' : open ? '▾' : '▸'}</span>
      <span class=${cx('chg-op mono', f.op === 'add' && 'op-add', f.op === 'delete' && 'op-delete')}>${ch.glyph(f.op)}</span>
      <span class="chg-path mono ell" title=${name}>${name}</span>
      ${f.generated && html`<span class="chip">${t('chg.generated')}</span>`}
      ${binary ? html`<span class="chg-n t-muted">${binary}</span>`
        : f.big && !open ? html`<span class="chg-n t-muted">${fmt('chg.big', `+${num(f.add)} −${num(f.del)}`)}</span>`
        : html`<${Counts} add=${f.add} del=${f.del} />`}
    </button>
    ${open && !f.binary && html`<${FileBody} f=${f} diff=${diff} rows=${rows} split=${split} sel=${sel} cur=${cur?.lo >= 0 ? cur : null} notes=${notes}
      onMore=${() => onMore(f.path)} onContext=${() => onContext(f.path)} onPick=${(lo, hi, x) => onPick(f.path, lo, hi, x)}
      onQuote=${(to, rows) => onQuote(f.path, rows, to)} onClear=${onClear} />`}
  </li>`;
}

// Changes draws a run's changes: data is its list (core/changes.js list), error why there is none, unsupported an older
// server; open is the files shown open and diffs what they show ({hunks, of, next, context, ignoreSpace?, loading?,
// error?}); ignoreSpace the toggle (onIgnoreSpace changes it), spaceOff a node that cannot ignore whitespace; running
// says the list is the workspace as it was at data.at, refreshed with onRefresh; runs and run pick which run. view is
// the layout asked for (unified or split, onView changes it; side by side only where wide); sel the lines picked
// ({path, a, b, at, one}, onPick(path, lo, hi, extend), onSpan(path, sel) and onClear change it); onQuote(path, rows, to)
// sends them on, to the agent or, when notes, into the send-back notes. On a desktop, while the tab has the focus, the
// keys move a cursor over the files and the open files' lines: Space opens a file or picks a line as a tap does,
// Shift and an arrow reach, Enter sends what is picked to the agent, Esc lets go. Text selected across the lines of one
// file picks them.
export function Changes({data = null, error = '', unsupported = false, running = false, open = new Set(), diffs = {}, onToggle, onRefresh,
  ignoreSpace = false, spaceOff = false, onIgnoreSpace, runs = [], run = '', onRun,
  view = 'unified', onView, wide = false, sel = null, notes = false, onMore, onContext, onPick, onSpan, onQuote, onClear}) {
  const {t, f} = useWords();
  const phone = usePhone();
  const [filter, setFilter] = useState('all');
  const [cur, setCur] = useState(null);
  const [focused, setFocused] = useState(false);
  const box = useRef(null);
  const split = !phone && wide && view === 'split';
  const rowsBy = useMemo(() => Object.fromEntries(Object.entries(diffs).map(([p, d]) => [p, d?.hunks ? ch.rowsOf(d.hunks) : []])), [diffs]);
  const groups = data ? ch.ordered(ch.filtered(data.files, filter)) : [];
  const ss = ch.stops(groups, onPick ? open : new Set(), p => rowsBy[p] || [], split);
  const move = x => { if (x) setCur(x); };
  const tap = c => (c.lo < 0 ? onToggle(c.path) : onPick(c.path, c.lo, c.hi, false));
  const reach = n => {
    if (!cur || cur.lo < 0) return;
    if (sel?.path !== cur.path) onPick(cur.path, cur.lo, cur.hi, false);
    const next = ch.reach(ss, cur, n);
    onPick(next.path, next.lo, next.hi, true);
    setCur(next);
  };
  useActions('list', {
    next: {run: () => move(ch.stepTo(ss, cur, 1))},
    prev: {run: () => move(ch.stepTo(ss, cur, -1))},
    toggle: {run: () => tap(cur), when: () => !!cur, label: cur?.lo >= 0 ? 'chg.pickLine' : undefined},
    open: {run: () => (sel && cur?.lo >= 0 ? onQuote(sel.path, rowsBy[sel.path] || [], 'agent') : tap(cur)), when: () => !!cur},
    reachDown: {run: () => reach(1), when: () => cur?.lo >= 0},
    reachUp: {run: () => reach(-1), when: () => cur?.lo >= 0},
    unpick: {run: () => onClear?.(), when: () => !!sel},
  }, {active: !phone && focused && !!data});
  useEffect(() => { box.current?.querySelector?.('.cur')?.scrollIntoView?.({block: 'nearest'}); }, [cur]);
  // a text selection across the lines of one file picks them
  useEffect(() => {
    const doc = globalThis.document;
    if (!onSpan || !doc?.addEventListener) return;
    const rowAt = node => {
      let row = null;
      for (let n = node; n && n !== box.current; n = n.parentNode) {
        if (!row && n.getAttribute?.('data-row')) row = n.getAttribute('data-row');
        if (row && n.getAttribute?.('data-path')) { const [lo, hi] = row.split(':').map(Number); return {path: n.getAttribute('data-path'), lo, hi}; }
      }
      return null;
    };
    const selected = () => {
      const x = doc.getSelection?.();
      if (!x || x.isCollapsed || !box.current?.contains?.(x.anchorNode) || !box.current.contains(x.focusNode)) return;
      const a = rowAt(x.anchorNode), b = rowAt(x.focusNode);
      if (a && b && a.path === b.path) onSpan(a.path, ch.spanned(a, b));
    };
    doc.addEventListener('selectionchange', selected);
    return () => doc.removeEventListener('selectionchange', selected);
  }, [onSpan]);
  const picker = runs.length > 1 && html`<${Segmented} label=${t('chg.run')} value=${run} onChange=${onRun}
    options=${runs.map(r => ({value: r.id, label: r.id}))} />`;
  const body = () => {
    if (unsupported) return html`<p class="empty">${t('chg.unsupported')}</p>`;
    if (error) return html`<p class="empty">${error === code.gone ? t('chg.gone') : error === 'not_started' ? t('chg.notYet') : f('chg.failed', error)}</p>`;
    if (!data) return html`<p class="empty t-muted">${t('chg.loading')}</p>`;
    const c = ch.counts(data.files);
    return html`
      <div class=${cx('chg-head', phone && 'chg-head-phone')}>
        <span class="chg-sum">${f('chg.files', data.total.files)} <${Counts} add=${data.total.add} del=${data.total.del} /></span>
        <${Chips} label=${t('chg.filter')}>${ch.filters.map(x => html`<${Chip} label=${t('chg.f.' + x)} count=${c[x]} on=${filter === x} onClick=${() => setFilter(x)} />`)}<//>
        ${!phone && wide && html`<${Segmented} label=${t('chg.view')} value=${view} onChange=${onView}
          options=${['unified', 'split'].map(v => ({value: v, label: t('chg.v.' + v)}))} />`}
        <${Check} label=${t('chg.space')} on=${ignoreSpace && !spaceOff} disabled=${spaceOff} onChange=${on => onIgnoreSpace?.(on)} />
        ${running && html`<span class="chg-at"><span class="t-muted">${f('chg.at', clock(data.at))}</span><${Button} kind="quiet" onClick=${onRefresh}>${t('chg.refresh')}<//></span>`}
      </div>
      ${!data.git && html`<p class="chg-warn">${t('chg.outside')}</p>`}
      ${spaceOff && html`<p class="chg-warn">${t('chg.spaceOld')}</p>`}
      ${data.hidden > 0 && html`<p class="chg-warn">${f('chg.hidden', data.hidden)}</p>`}
      ${onPick && groups.length > 0 && open.size > 0 && html`<p class="chg-hint t-muted">${t(phone ? 'chg.pickHintPhone' : 'chg.pickHint')}</p>`}
      ${!groups.length ? html`<p class="empty">${t('chg.none')}</p>` : html`<div class="chg-list" tabindex=${phone ? undefined : 0}>${groups.map(g => html`<section class="chg-group" key=${g.dir}>
        ${g.dir && html`<h4 class="chg-dir mono">${g.dir}/</h4>`}
        <ul class="chg-files">${g.files.map(x => html`<${FileRow} key=${x.path} f=${x} open=${open.has(x.path)} diff=${diffs[x.path]} rows=${rowsBy[x.path] || []}
          split=${split} sel=${sel?.path === x.path ? sel : null} cur=${focused && cur?.path === x.path ? cur : null} notes=${notes} onToggle=${onToggle} onMore=${onMore} onContext=${onContext} onPick=${onPick}
          onQuote=${onQuote} onClear=${onClear} />`)}</ul>
      </section>`)}</div>`}`;
  };
  return html`<section class="chg" aria-label=${t('chg.label')} ref=${box}
    onFocusIn=${() => setFocused(true)} onFocusOut=${e => { if (!e.currentTarget.contains?.(e.relatedTarget)) setFocused(false); }}>
    ${picker && html`<div class="chg-runs">${picker}</div>`}
    ${body()}
  </section>`;
}

// RunChanges loads the changes of one of runs (the first unless picked) through changes (core/changes.js) and draws
// them; a running run's are read again when its state moves and on asking. prefs keeps the layout and whether to
// ignore whitespace (a run whose node cannot is shown plain, the toggle off); onQuote(text, to)
// takes a quote of picked lines to the agent's box ('agent') or, when notes, the send-back notes ('notes').
export function RunChanges({changes, runs, prefs = null, notes = false, onQuote}) {
  const w = useWords();
  const phone = usePhone();
  const [picked, setPicked] = useState('');
  const run = runs.find(r => r.id === picked) || runs[0];
  const [got, setGot] = useState({});
  const [open, setOpen] = useState(() => new Set());
  const [diffs, setDiffs] = useState({});
  const [sel, setSel] = useState(null);
  const [tick, setTick] = useState(0);
  const seq = useRef(0);
  const box = useRef(null);
  const wide = useWidth(box) >= SPLIT_WIDTH;
  const view = useSignalValue(prefs?.diff || unified);
  const [old, setOld] = useState('');
  const spaceOff = !!run && old === run.id;
  const space = useSignalValue(prefs?.ignoreSpace || keepSpace) && !spaceOff;
  const spaceNow = useRef(space);
  spaceNow.current = space;
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
      setSel(null);
      if (!phone) setOpen(new Set(data.files.filter(x => !ch.folded(x)).slice(0, AUTO_OPEN).map(x => x.path)));
    }, e => n === seq.current && setGot({error: e.code || String(e.message || e)}));
  }, [run?.id, run?.state, tick, can]);

  // fetch asks for a page of path's diff from `from` with context lines, adding it to what it shows (restart: in its
  // place); a list moved on under a running run is read again.
  const fetch = (path, from, context, restart) => {
    const data = got.data, n = seq.current, ignoreSpace = space, id = run.id;
    setDiffs(d => ({...d, [path]: {...(restart ? {} : d[path]), context, ignoreSpace, loading: true, error: ''}}));
    changes.diff(id, path, data.snapshot, {...from, context, ...(ignoreSpace ? {ignoreSpace} : {})}).then(page => {
      if (n !== seq.current || ignoreSpace !== spaceNow.current) return;
      setDiffs(d => {
        const pages = [...(restart ? [] : d[path]?.pages || []), {from, page}];
        return {...d, [path]: {...ch.joined(pages), pages, context, ignoreSpace}};
      });
    }, e => {
      if (n !== seq.current || ignoreSpace !== spaceNow.current) return;
      if (ignoreSpace && e.code === code.unsupported) { setOld(id); return; }
      setDiffs(d => ({...d, [path]: {...d[path], loading: false, error: e.code || 'error'}}));
      if (e.code === code.snapshotChanged && running) setTick(x => x + 1);
    });
  };
  useEffect(() => {
    const data = got.data;
    if (!data) return;
    for (const path of open) {
      const d = diffs[path];
      if (d && d.ignoreSpace === space) continue;
      const file = data.files.find(x => x.path === path);
      if (!file || file.binary) continue;
      if (sel?.path === path) setSel(null);
      fetch(path, {hunk: 0}, d?.context || ch.CONTEXT, true);
    }
  }, [got.data, [...open].join('\n'), space]);
  const shown = useMemo(() => Object.fromEntries(Object.entries(diffs).filter(([, d]) => d.ignoreSpace === space)), [diffs, space]);
  const toggle = path => setOpen(o => { const s = new Set(o); if (s.has(path)) { s.delete(path); if (sel?.path === path) setSel(null); } else s.add(path); return s; });
  const more = path => { const d = shown[path]; if (d?.next && !d.loading) fetch(path, d.next, d.context, false); };
  const context = path => { const d = shown[path]; if (d && !d.loading) { if (sel?.path === path) setSel(null); fetch(path, {hunk: 0}, d.context + ch.MORE_CONTEXT, true); } };
  const pick = (path, lo, hi, extend) => setSel(s => { const x = ch.pick(s?.path === path ? s : null, lo, hi, extend); return x && {...x, path}; });
  const span = (path, x) => setSel(s => (s?.path === path && s.a === x.a && s.b === x.b ? s : {...x, path}));
  const quote = (path, rows, to) => {
    if (!sel) return;
    onQuote?.(ch.quote({path, rows, a: sel.a, b: sel.b, before: s => w.f('chg.q.before', s), more: n => w.f('chg.q.more', n)}), to);
    setSel(null);
  };
  if (!run) return null;
  return html`<div class="chg-box" ref=${box}><${Changes} data=${got.data || null} error=${got.error || ''} unsupported=${!can} running=${running} open=${open} diffs=${shown}
    onToggle=${toggle} onRefresh=${() => setTick(n => n + 1)} runs=${runs} run=${run.id} onRun=${id => { setPicked(id); setGot({}); setOpen(new Set()); setSel(null); }}
    ignoreSpace=${space} spaceOff=${spaceOff} onIgnoreSpace=${on => (prefs ? prefs.setIgnoreSpace(on) : (keepSpace.value = on))}
    view=${view} onView=${v => prefs?.setDiff(v)} wide=${wide} sel=${onQuote ? sel : null} notes=${notes}
    onMore=${more} onContext=${context} onPick=${onQuote ? pick : null} onSpan=${onQuote ? span : null} onQuote=${quote} onClear=${() => setSel(null)} /></div>`;
}
