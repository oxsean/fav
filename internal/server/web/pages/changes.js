// changes is the changes tab of a task and of a run: what a run changed, file by file in directory order with its
// +a −d, filtered to the agent tools' own or the generated files; a file opens on its first hunk. Big, generated and
// binary files start folded. Paging through hunks, side by side, and sending lines to the agent come later (F2).
import {useState, useEffect, useRef} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords} from '../ui/base.js';
import {Button, Chip, Chips, Segmented} from '../ui/controls.js';
import {Diff} from '../ui/output.js';
import * as ch from '../core/changes.js';
import {clock} from '../core/format.js';
import {openStates} from '../core/select.js';
import {register} from '../core/i18n.js';
import {code} from '../core/proto.js';

register('changes', {
  'chg.label': ['改动', 'Changes'], 'chg.files': ['%d 个文件', 'Files: %d'], 'chg.filter': ['筛选改动', 'Filter the changes'],
  'chg.f.all': ['全部', 'All'], 'chg.f.agent': ['只看 agent 用工具改的', 'Only the agent\'s tools'], 'chg.f.generated': ['生成的文件', 'Generated files'],
  'chg.binary': ['二进制 · %s', 'Binary · %s'], 'chg.big': ['改动很大：%s', 'Large change: %s'], 'chg.generated': ['生成的', 'generated'],
  'chg.more': ['还有 %d 处', '%d more hunks'], 'chg.cut': ['这一处另有 %d 行没显示', '%d more lines of this hunk not shown'],
  'chg.later': ['按处翻页以后再做', 'Paging through hunks comes later'],
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

// ⚠️ A desktop opens this many unfolded files on its own, fetching their first hunks; a phone opens none.
export const AUTO_OPEN = 8;

const num = n => Number(n || 0).toLocaleString('en-US');
const size = b => (b >= 1024 * 1024 ? (b / 1024 / 1024).toFixed(1) + ' MB' : b >= 1024 ? Math.round(b / 1024) + ' KB' : (b || 0) + ' B');

function Counts({add, del}) {
  return html`<span class="chg-n mono"><span class="t-success">+${num(add)}</span> <span class="t-failed">−${num(del)}</span></span>`;
}

function FileBody({f, prev}) {
  const {t, f: fmt} = useWords();
  if (!prev || prev === 'loading') return html`<p class="chg-note t-muted">${t('chg.loading')}</p>`;
  if (prev.error) return html`<p class="chg-note t-muted">${prev.error === code.gone ? t('chg.hunkGone') : prev.error === 'snapshot_changed' ? t('chg.moved') : fmt('chg.failed', prev.error)}</p>`;
  const lines = [prev.at, ...prev.lines].filter(Boolean);
  return html`<div class="chg-body">
    ${lines.length ? html`<${Diff} lines=${lines} />` : html`<p class="chg-note t-muted">${t('chg.noHunk')}</p>`}
    ${(prev.cut > 0 || prev.more > 0) ? html`<p class="chg-note t-muted">${[prev.cut > 0 && fmt('chg.cut', prev.cut), prev.more > 0 && fmt('chg.more', prev.more), t('chg.later')].filter(Boolean).join(' · ')}</p>` : ''}
  </div>`;
}

function FileRow({f, open, prev, onToggle}) {
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
    ${open && !f.binary && html`<${FileBody} f=${f} prev=${prev} />`}
  </li>`;
}

// Changes draws a run's changes: data is its list (core/changes.js list), error why there is none, unsupported an older
// server; open is the files shown open and previews their first hunks ({at, lines, cut, more}, 'loading' or {error});
// running says the list is the workspace as it was at data.at, refreshed with onRefresh; runs and run pick which run.
export function Changes({data = null, error = '', unsupported = false, running = false, open = new Set(), previews = {}, onToggle, onRefresh,
  runs = [], run = '', onRun}) {
  const {t, f} = useWords();
  const phone = usePhone();
  const [filter, setFilter] = useState('all');
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
        ${running && html`<span class="chg-at"><span class="t-muted">${f('chg.at', clock(data.at))}</span><${Button} kind="quiet" onClick=${onRefresh}>${t('chg.refresh')}<//></span>`}
      </div>
      ${!data.git && html`<p class="chg-warn">${t('chg.outside')}</p>`}
      ${data.hidden > 0 && html`<p class="chg-warn">${f('chg.hidden', data.hidden)}</p>`}
      ${!groups.length ? html`<p class="empty">${t('chg.none')}</p>` : html`<div class="chg-list">${groups.map(g => html`<section class="chg-group" key=${g.dir}>
        ${g.dir && html`<h4 class="chg-dir mono">${g.dir}/</h4>`}
        <ul class="chg-files">${g.files.map(x => html`<${FileRow} key=${x.path} f=${x} open=${open.has(x.path)} prev=${previews[x.path]} onToggle=${onToggle} />`)}</ul>
      </section>`)}</div>`}`;
  };
  return html`<section class="chg" aria-label=${t('chg.label')}>
    ${picker && html`<div class="chg-runs">${picker}</div>`}
    ${body()}
  </section>`;
}

// RunChanges loads the changes of one of runs (the first unless picked) through changes (core/changes.js) and draws
// them; a running run's are read again when its state moves and on asking.
export function RunChanges({changes, runs}) {
  const phone = usePhone();
  const [picked, setPicked] = useState('');
  const run = runs.find(r => r.id === picked) || runs[0];
  const [got, setGot] = useState({});
  const [open, setOpen] = useState(() => new Set());
  const [previews, setPreviews] = useState({});
  const [tick, setTick] = useState(0);
  const seq = useRef(0);
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
      setPreviews({});
      if (!phone) setOpen(new Set(data.files.filter(x => !ch.folded(x)).slice(0, AUTO_OPEN).map(x => x.path)));
    }, e => n === seq.current && setGot({error: e.code || String(e.message || e)}));
  }, [run?.id, run?.state, tick, can]);
  useEffect(() => {
    const data = got.data;
    if (!data) return;
    for (const path of open) {
      if (previews[path]) continue;
      const file = data.files.find(x => x.path === path);
      if (!file || file.binary) continue;
      setPreviews(p => ({...p, [path]: 'loading'}));
      changes.diff(run.id, path, data.snapshot).then(pg => setPreviews(p => ({...p, [path]: ch.preview(pg) || {at: '', lines: [], cut: 0, more: 0}})),
        e => { setPreviews(p => ({...p, [path]: {error: e.code || 'error'}})); if (e.code === 'snapshot_changed' && running) setTick(n => n + 1); });
    }
  }, [got.data, [...open].join('\n')]);
  const toggle = path => setOpen(o => { const s = new Set(o); if (s.has(path)) s.delete(path); else s.add(path); return s; });
  if (!run) return null;
  return html`<${Changes} data=${got.data || null} error=${got.error || ''} unsupported=${!can} running=${running} open=${open} previews=${previews}
    onToggle=${toggle} onRefresh=${() => setTick(n => n + 1)} runs=${runs} run=${run.id} onRun=${id => { setPicked(id); setGot({}); setOpen(new Set()); }} />`;
}
