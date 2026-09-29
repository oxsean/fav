// Output draws a conversation's timeline (core/output.js) at the viewer's density: what people and the agent said,
// open; each step one line; results folded; failures and questions open by themselves; finished turns folded into one
// line. It scrolls natively and follows the bottom until the viewer moves up (core/follow.js), counts what came since,
// holds back changes above the view while scrolling, finds text in what is loaded and earlier, and answers questions
// where they are asked (the page's renderAsk). On a phone it fills the screen with its tools on top.
import {useState, useRef, useEffect, useLayoutEffect, useMemo} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useActions} from './base.js';
import {Button, Chip, Chips, Segmented} from './controls.js';
import {Markdown} from './markdown.js';
import {register} from '../core/i18n.js';
import * as out from '../core/output.js';
import {density as dt} from '../core/proto.js';
import * as fl from '../core/follow.js';
import {duration, tokens, money, clock, usageTokens} from '../core/format.js';

register('output', {
  'out.label': ['输出', 'Output'], 'out.filter': ['筛选', 'Filter'],
  'out.f.all': ['全部', 'All'], 'out.f.talk': ['只看对话', 'Talk'], 'out.f.shell': ['只看命令', 'Commands'], 'out.f.edit': ['只看改动', 'Changes'],
  'out.f.error': ['出错的', 'Errors'], 'out.nextError': ['下一处错误', 'Next error'], 'out.noError': ['没有出错的步骤', 'No step failed'],
  'out.density': ['密度', 'Density'], 'out.d.brief': ['简洁', 'Brief'], 'out.d.standard': ['标准', 'Standard'], 'out.d.detailed': ['详细', 'Detailed'],
  'out.raw': ['原始', 'Raw'], 'out.rawNote': ['这一页的原始行', 'This page’s lines as written'],
  'out.follow': ['跟随', 'Follow'], 'out.pause': ['暂停', 'Pause'], 'out.find': ['查找', 'Find'],
  'out.more': ['加载更早的…', 'Load earlier…'], 'out.loading': ['正在加载更早的…', 'Loading earlier…'], 'out.start': ['这次运行的开头', 'The start of this run'],
  'out.gone': ['这一轮的输出已清理', 'This part’s output was cleared'], 'out.failedLoad': ['没取到更早的内容（%s）', 'Could not load earlier output (%s)'],
  'out.retry': ['重试', 'Retry'], 'out.run': ['第 %d 次运行 · 续接 · %s', 'Run %d · continued · %s'],
  'out.turn': ['第 %d 轮', 'Turn %d'], 'out.worked': ['工作了 %s', 'worked %s'], 'out.steps': ['%d 步', '%d steps'], 'out.files': ['改了 %d 个文件', 'changed %d files'],
  'out.sum.shell': ['跑了 %d 条命令', 'ran %d commands'], 'out.sum.reads': ['读了 %d 个文件', 'read %d files'], 'out.sum.searches': ['搜了 %d 次', 'searched %d times'],
  'out.sum.other': ['%d 次别的调用', '%d other calls'], 'out.sum.failed': ['%d 处失败', '%d failed'],
  'out.brief': ['任务书', 'Brief'], 'out.you': ['你', 'You'], 'out.by': ['%s 说', '%s said'],
  'out.whole': ['展开全文', 'Show all'], 'out.fold': ['收起', 'Fold'], 'out.think': ['思考', 'Thought'],
  'out.running': ['运行中', 'running'], 'out.exit': ['exit %d', 'exit %d'], 'out.lines': ['%d 行', '%d lines'],
  'out.cut': ['… 中间省略 %d 行 …', '… %d lines left out …'], 'out.truncated': ['全文 %s，这里只有头尾', 'In full %s: only its head and tail are here'],
  'out.copyCmd': ['复制命令', 'Copy the command'], 'out.copyOut': ['复制输出', 'Copy the output'], 'out.copied': ['已复制', 'Copied'],
  'out.plan': ['更新了计划 %s', 'Updated the plan %s'], 'out.planHead': ['计划 %s', 'Plan %s'],
  'out.agent': ['子 agent：%s · %d 步', 'Sub-agent: %s · %d steps'],
  'out.ended': ['本轮结束', 'Turn ended'], 'out.failedTurn': ['这一轮出错了', 'This turn failed'],
  'out.hook': ['%s %s', '%s %s'], 'out.hook.begin': ['开始', 'begins'], 'out.hook.end': ['结束', 'ends'],
  'out.gap': ['省略了 %s，往前翻', '%s left out: scroll up'],
  'out.interrupt': ['打断了这一轮', 'Interrupted this turn'], 'out.interruptBy': ['%s 打断了这一轮', '%s interrupted this turn'],
  'out.decided': ['%s · %s', '%s · %s'], 'out.d.allow': ['允许了', 'allowed'], 'out.d.allow_run': ['这次运行里都允许', 'allowed for the run'],
  'out.d.deny': ['拒绝了', 'denied'], 'out.d.answered': ['回答了', 'answered'], 'out.handled': ['已处理', 'Handled'], 'out.whoYou': ['你', 'You'],
  'out.new': ['↓ %d 条新的', '↓ %d new'], 'out.newWaiting': ['↓ %d 条新的 · %d 条等你', '↓ %d new · %d waiting on you'],
  'out.toWaiting': ['跳到等你的', 'Go to what waits'], 'out.endedSee': ['运行结束了 · 看结果', 'The run ended · see how'],
  'out.latest': ['回到最新', 'Back to the latest'], 'out.newLine': ['新的 ↓', 'New ↓'],
  'out.queued': ['排队中', 'Queued'], 'out.notSent': ['没送到', 'Not delivered'], 'out.resend': ['重发', 'Send again'],
  'out.findHint': ['在输出里查找', 'Find in the output'], 'out.findScope': ['查的是摘要，不含截掉的部分', 'Searches the summaries, not what was cut'],
  'out.hits': ['%d / %d', '%d / %d'], 'out.noHit': ['没有找到', 'Not found'], 'out.earlier': ['在更早的内容里找', 'Find in earlier output'],
  'out.earlierMore': ['找过更早的 %d 页，没有找到。接着找？', 'Looked through %d earlier pages without a hit. Go on?'],
  'out.prevHit': ['上一处', 'Previous'], 'out.nextHit': ['下一处', 'Next'], 'out.close': ['关闭查找', 'Close find'],
  'out.empty': ['还没有输出', 'No output yet'], 'out.copyLink': ['复制这一步的链接', 'Copy a link to this step'],
});

// ⚠️ Find waits this long after a key; "find in earlier output" fetches this many pages (each at most 1 MiB, so about
// 8 MiB) before asking to go on.
const FIND_WAIT = 150;
const FIND_PAGES = 8;

const glyph = {ok: '✓', failed: '✗', running: '●', unknown: '?'};
const tone = {ok: 't-success', failed: 't-failed', running: 't-running', unknown: 't-muted'};

function kib(n) {
  if (n < 1024) return n + ' B';
  if (n < 1 << 20) return Math.round(n / 1024) + ' KiB';
  return (n / (1 << 20)).toFixed(1) + ' MiB';
}

// Pre is an output or a diff: lines kept as they are, scrolled sideways, with a copy button when copy is given.
function Pre({lines, cut = 0, tail = [], cls = '', copy, copyLabel}) {
  const {f} = useWords();
  return html`<div class="out-pre-wrap">
    <pre class=${cx('out-pre', cls)}>${lines.join('\n')}${cut > 0 && html`${'\n'}<span class="t-muted">${f('out.cut', cut)}</span>`}${tail.length > 0 && '\n' + tail.join('\n')}</pre>
    ${copy && html`<${Button} kind="quiet" onClick=${copy}>${copyLabel}<//>`}
  </div>`;
}

const diffCls = l => (l.startsWith('+') && !l.startsWith('+++') ? 'add' : l.startsWith('-') && !l.startsWith('---') ? 'del' : l.startsWith('@@') ? 'hunk' : '');

// Diff draws diff lines, coloured by their mark; past max (when given) the rest is left out.
export function Diff({lines, max = 0}) {
  const {f} = useWords();
  const shown = max && lines.length > max ? lines.slice(0, max) : lines;
  return html`<div class="out-pre-wrap"><pre class="out-pre out-diff">${shown.map(l => html`<span class=${diffCls(l)}>${l + '\n'}</span>`)}${max > 0 && lines.length > max && html`<span class="t-muted">${f('out.cut', lines.length - max)}</span>`}</pre></div>`;
}

// body is what an open step shows below its line.
function Body({row, density, copy}) {
  const w = useWords();
  const {t, f} = w;
  const s = row.step;
  const o = s.output || '';
  const whole = !!row.manual;
  switch (s.kind) {
    case 'shell': case 'output': case 'mcp': case 'web': case 'other': {
      if (s.state === 'running' && !o) return null;
      let view;
      if (s.state === 'running') view = {lines: out.tail(o, dt.runningTail)};
      else if (s.failed && !whole) view = {lines: out.tail(o, dt.failTail)};
      else if (density === 'detailed' && !whole) { const e = out.ends(o, dt.detailEnds); view = {lines: e.head, cut: e.cut, tail: e.tail}; }
      else view = {lines: o ? o.split('\n') : []};
      const input = s.kind !== 'shell' && s.input ? JSON.stringify(s.input, null, 2) : '';
      return html`<div class="out-body">
        ${input && html`<${Pre} lines=${input.split('\n')} cls="t-muted" />`}
        ${view.lines.length > 0 && html`<${Pre} ...${view} cls=${s.failed ? 'out-failed' : ''} copy=${o && (() => copy(o))} copyLabel=${t('out.copyOut')} />`}
        ${s.truncated?.output > 0 && html`<span class="t-muted out-note">${f('out.truncated', kib(o.length + s.truncated.output))}</span>`}
      </div>`;
    }
    case 'edit': {
      const ls = out.editLines(s);
      return html`<div class="out-body">${s.files.length > 1 && html`<ul class="out-files mono">${s.files.map(x => html`<li>~ ${x}</li>`)}</ul>`}
        ${ls.length > 0 && html`<${Diff} lines=${ls} max=${density === 'detailed' && !whole ? dt.diffCut : 0} />`}</div>`;
    }
    case 'group':
      return html`<ul class="out-members mono">${s.members.map(m => html`<li key=${m.id}><span class="t-muted">${m.family === 'search' ? '?' : '+'}</span> ${m.title}</li>`)}</ul>`;
    case 'think': return html`<div class="out-body out-think">${s.text}</div>`;
    case 'plan': return html`<ul class="out-plan">${s.plan.map(p => html`<li class=${cx(p.done && 'done', p.now && 'now')}>${p.done ? '☑' : '☐'} ${p.text}</li>`)}</ul>`;
    case 'sys': case 'raw': return html`<${Pre} lines=${String(s.text).split('\n')} cls="t-muted" />`;
  }
  return null;
}

function stepLine(w, s, density) {
  const {t, f} = w;
  switch (s.kind) {
    case 'shell': return {glyph: '$', text: s.title, mono: true};
    case 'group': {
      const parts = [s.reads && f('out.sum.reads', s.reads), s.searches && f('out.sum.searches', s.searches)].filter(Boolean);
      return {glyph: '+', text: s.members.length === 1 ? s.members[0].title : parts.join(' · '), mono: s.members.length === 1};
    }
    case 'edit': return {glyph: '~', text: s.title || s.files.join(', '), mono: true};
    case 'plan': return {glyph: '☐', text: f('out.plan', s.title), thin: true};
    case 'agent': return {glyph: '↳', text: f('out.agent', s.title, (s.kids || []).length)};
    case 'mcp': case 'web': case 'other': return {glyph: '·', text: s.title, mono: true};
    case 'output': return {glyph: '=', text: s.output.split('\n')[0], mono: true};
    case 'think': return {glyph: '…', text: t('out.think') + (s.text ? ' · ' + s.text.split('\n')[0] : ''), muted: true};
    case 'sys': return {glyph: s.warn ? '!' : '·', text: s.text || s.name, muted: !s.warn, warn: s.warn, mono: true};
    case 'raw': return {glyph: '·', text: s.text.split('\n')[0], muted: true, mono: true};
  }
  return null;
}

// StepMeta is the right end of a step's line: its state, how long it took and how many lines it wrote.
function StepMeta({s}) {
  const {t, f} = useWords();
  if (!['shell', 'mcp', 'web', 'other', 'agent'].includes(s.kind)) return null;
  return html`<span class="out-meta mono">
    <span class=${tone[s.state]}>${glyph[s.state]}${s.state === 'running' ? ' ' + t('out.running') : s.failed && s.exit ? ' ' + f('out.exit', s.exit) : ''}</span>
    ${s.dur > 0 && html`<span>${duration(s.dur)}</span>`}
    ${s.kind === 'shell' && s.lines > 0 && html`<span>${f('out.lines', s.lines)}</span>`}
  </span>`;
}

function decided(w, r) {
  if (!r) return w.t('out.handled');
  const who = r.by || w.t('out.whoYou');
  const what = w.t('out.d.' + (r.decision || 'answered')) || '';
  return w.f('out.decided', who + ' ' + what, r.at ? clock(r.at) : '').replace(/ · $/, '');
}

// Row draws one row of the timeline.
function Row({row, density, onToggle, onMore, onResend, renderAsk, copy, target, selected}) {
  const w = useWords();
  const {t, f} = w;
  const base = cx('out-row', 'out-' + row.type, row.depth > 0 && 'out-nested', row.key === target && 'out-flash', row.key === selected && 'sel');
  const depth = row.depth ? {paddingLeft: 12 + row.depth * 16 + 'px'} : undefined;
  switch (row.type) {
    case 'head':
      if (row.gone) return html`<div class=${base} data-key=${row.key}><span class="t-muted">${t('out.gone')}</span></div>`;
      if (row.start) return html`<div class=${base} data-key=${row.key}><span class="t-muted">${t('out.start')}</span></div>`;
      if (row.loading) return html`<div class=${base} data-key=${row.key}><span class="t-muted">${t('out.loading')}</span></div>`;
      return html`<div class=${base} data-key=${row.key}>${row.failed && html`<span class="t-failed">${f('out.failedLoad', row.failed)}</span>`}
        <${Button} kind="quiet" onClick=${onMore}>${row.failed ? t('out.retry') : t('out.more')}<//></div>`;
    case 'run':
      return html`<div class=${base} data-key=${row.key} role="separator"><span>${f('out.run', row.n, row.run?.machine || '')}</span></div>`;
    case 'turn': {
      const bits = [f('out.turn', row.n), row.dur > 0 && f('out.worked', duration(row.dur)), f('out.steps', row.steps), row.files > 0 && f('out.files', row.files)].filter(Boolean);
      return html`<div class=${base} data-key=${row.key}><button type="button" class="out-line" aria-expanded=${row.open ? 'true' : 'false'} onClick=${() => onToggle(row.key)}>
        <span class="out-chev mono" aria-hidden="true">${row.open ? '▾' : '▸'}</span><span class="out-turn-sum">${bits.join(' · ')}</span>
        ${!row.open && row.last && html`<span class="out-turn-last ell">${row.last}</span>`}${row.failed > 0 && html`<span class="t-failed">${f('out.sum.failed', row.failed)}</span>`}
      </button></div>`;
    }
    case 'summary': {
      const bits = [row.shell && f('out.sum.shell', row.shell), row.files && f('out.files', row.files), row.reads && f('out.sum.reads', row.reads),
        row.searches && f('out.sum.searches', row.searches), row.other && f('out.sum.other', row.other)].filter(Boolean);
      return html`<div class=${base} data-key=${row.key}><span class="out-glyph mono t-muted">·</span><span class="t-muted">${bits.join(' · ')}</span></div>`;
    }
    case 'temp':
      return html`<div class=${base} data-key=${row.key}><span class="out-temp">${row.text}</span></div>`;
    case 'send':
      return html`<div class=${base} data-key=${row.key}><div class="out-you pending"><div class="out-text">${row.text}</div>
        <span class=${cx('out-meta', row.state === 'failed' && 't-failed')}>${t(row.state === 'failed' ? 'out.notSent' : 'out.queued')}
        ${row.state === 'failed' && onResend && html` <${Button} kind="quiet" onClick=${() => onResend(row)}>${t('out.resend')}<//>`}</span></div></div>`;
  }
  const s = row.step;
  const toggle = () => onToggle(row.key);
  switch (s.kind) {
    case 'you': {
      const lines = s.text.split('\n');
      const clipped = row.brief && lines.length > dt.briefLines;
      return html`<div class=${base} data-key=${row.key} style=${depth}><div class="out-you">
        <span class="out-who">${s.brief ? t('out.brief') : s.by ? f('out.by', s.by) : t('out.you')}${s.at ? ' · ' + clock(s.at) : ''}</span>
        <div class="out-text">${clipped ? lines.slice(0, dt.briefLines).join('\n') : s.text}</div>
        ${clipped && html`<button type="button" class="out-link" onClick=${toggle}>${t('out.whole')}</button>`}
      </div></div>`;
    }
    case 'say': {
      const lines = s.text.split('\n');
      const text = row.clip ? lines.slice(0, dt.sayFold).join('\n') : s.text;
      return html`<div class=${base} data-key=${row.key} style=${depth}><div class="out-say"><${Markdown} text=${text} />
        ${(row.clip || row.open && lines.length > dt.sayFold && density === 'standard') && html`<button type="button" class="out-link" onClick=${toggle}>${row.clip ? t('out.whole') : t('out.fold')}</button>`}
      </div></div>`;
    }
    case 'result':
      return html`<div class=${base} data-key=${row.key}><div class=${cx('out-result', s.error && 't-failed')}>
        <span>${s.error ? t('out.failedTurn') : t('out.ended')}</span>
        ${s.dur > 0 && html`<span class="mono">${duration(s.dur)}</span>`}
        ${s.usage && html`<span class="mono">${tokens(usageTokens(s.usage))} tok</span>`}${s.cost >= 0.005 && html`<span class="mono">${money(s.cost)}</span>`}
        ${s.error && s.text && html`<div class="out-text">${s.text}</div>`}
      </div></div>`;
    case 'error':
      return html`<div class=${base} data-key=${row.key}><div class="out-error" role="alert">${s.text}</div></div>`;
    case 'mark':
      return html`<div class=${base} data-key=${row.key} role="separator"><span>── ${f('out.hook', s.name, t('out.hook.' + (s.phase === 'end' ? 'end' : 'begin')))} ──</span></div>`;
    case 'gap':
      return html`<div class=${base} data-key=${row.key}><button type="button" class="out-link" onClick=${onMore}>${f('out.gap', kib(s.bytes))}</button></div>`;
    case 'interrupt':
      return html`<div class=${base} data-key=${row.key} role="separator"><span>${s.by ? f('out.interruptBy', s.by) : t('out.interrupt')}</span></div>`;
    case 'ask':
      return html`<div class=${base} data-key=${row.key} style=${depth}><div class=${cx('out-ask', s.pending && 'pending')}>
        <div class="out-ask-head"><span class="out-glyph mono t-unknown">?</span><span class="out-ask-title">${s.title || s.tool}</span>
          ${!s.pending && html`<span class="out-meta">${decided(w, s.resolved)}</span>`}</div>
        ${s.pending && renderAsk?.(s)}
      </div></div>`;
  }
  const line = stepLine(w, s, density);
  if (!line) return null;
  const opens = s.kind !== 'plan' || (s.plan || []).length > 0;
  return html`<div class=${cx(base, s.failed && 'out-fail')} data-key=${row.key} style=${depth}>
    <div class="out-step-line">
      <button type="button" class=${cx('out-line', line.thin && 'thin')} aria-expanded=${opens ? (row.open ? 'true' : 'false') : undefined} onClick=${opens ? toggle : undefined}>
        <span class=${cx('out-glyph', 'mono', s.failed ? 't-failed' : 't-muted')} aria-hidden="true">${line.glyph}</span>
        <span class=${cx('out-title', line.mono && 'mono', line.muted && 't-muted', line.warn && 't-warning')}>${line.text}</span>
        ${s.more > 0 && html`<span class="t-muted mono">+${s.more}</span>`}
        <${StepMeta} s=${s} />
      </button>
      ${s.kind === 'shell' && s.input?.command && html`<${Button} kind="quiet" onClick=${() => copy(String(s.input.command))}>${t('out.copyCmd')}<//>`}
    </div>
    ${row.open && html`<${Body} row=${row} density=${density} copy=${copy} />`}
  </div>`;
}

// PlanPin is the latest plan, pinned above the timeline with how far it got.
function PlanPin({step}) {
  const {f} = useWords();
  const [open, setOpen] = useState(false);
  const done = step.plan.filter(p => p.done).length;
  return html`<div class="out-pin"><button type="button" class="out-line" aria-expanded=${open ? 'true' : 'false'} onClick=${() => setOpen(!open)}>
    <span class="out-chev mono" aria-hidden="true">${open ? '▾' : '▸'}</span>${f('out.planHead', `${done}/${step.plan.length}`)}
    <span class="out-bar" aria-hidden="true"><span style=${{width: (done / Math.max(1, step.plan.length)) * 100 + '%'}}></span></span></button>
    ${open && html`<ul class="out-plan">${step.plan.map(p => html`<li class=${cx(p.done && 'done', p.now && 'now')}>${p.done ? '☑' : '☐'} ${p.text}</li>`)}</ul>`}
  </div>`;
}

// FindBar is tend's own find: over what is loaded, and on request over earlier pages.
function FindBar({q, setQ, hits, at, onStep, onClose, onEarlier, earlier, inputRef}) {
  const {t, f} = useWords();
  const phone = usePhone();
  const i = hits.findIndex(h => h.key === at);
  return html`<div class="out-find" role="search">
    <input class="in" ref=${inputRef} value=${q} placeholder=${t('out.findHint')} aria-label=${t('out.findHint')} autofocus
      onInput=${e => setQ(e.currentTarget.value)} onKeyDown=${e => {
        if (e.isComposing) return;
        if (e.key === 'Enter') { e.preventDefault(); onStep(e.shiftKey ? -1 : 1); }
        if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation?.(); onClose(); }
      }} />
    <span class="mono out-hits">${q.trim() ? hits.length ? f('out.hits', i + 1, hits.length) : t('out.noHit') : ''}</span>
    ${phone && html`<${Button} kind="quiet" icon="up" label=${t('out.prevHit')} onClick=${() => onStep(-1)} /><${Button} kind="quiet" icon="down" label=${t('out.nextHit')} onClick=${() => onStep(1)} />`}
    <${Button} kind="quiet" icon="close" label=${t('out.close')} onClick=${onClose} />
    <span class="out-find-note t-muted">${t('out.findScope')}</span>
    ${onEarlier && q.trim() && html`<span class="out-find-more">${earlier?.asked ? html`<span class="t-muted">${f('out.earlierMore', earlier.pages)}</span>` : ''}
      <${Button} kind="quiet" disabled=${earlier?.busy} onClick=${onEarlier}>${t('out.earlier')}<//></span>`}
  </div>`;
}

// NewBadge is "N new" at the bottom: in the warning colour when something waits on the viewer, with a way straight to it.
function NewBadge({b, onGo, onWaiting}) {
  const {t, f} = useWords();
  if (!b) return null;
  const text = b.kind === 'ended' ? t('out.endedSee') : b.kind === 'latest' ? t('out.latest') : b.kind === 'waiting' ? f('out.newWaiting', b.n, b.waiting) : f('out.new', b.n);
  return html`<div class="out-new-wrap">
    <button type="button" class=${cx('out-new-btn', b.kind === 'waiting' && 'warn')} onClick=${onGo}>${text}</button>
    ${b.kind === 'waiting' && b.first && html`<button type="button" class="out-new-btn warn small" onClick=${() => onWaiting(b.first)}>${t('out.toWaiting')}</button>`}
  </div>`;
}

// useFollow ties the scroller to core/follow.js: what the viewer does moves it away from the bottom, programmatic
// scrolls do not; once the scrolling stops it follows again near the bottom; while following, growth sticks to it.
function useFollow({scroller, keys, waits, ended, away, timers = globalThis}) {
  const [s, setS] = useState(() => fl.initial(away));
  const state = useRef(s);
  state.current = s;
  const dispatch = ev => setS(prev => fl.step(prev, ev));
  const mine = useRef(0), touching = useRef(false), scrolling = useRef(false), settleTimer = useRef(null), onSettle = useRef(() => {});
  const fromBottom = el => el.scrollHeight - el.clientHeight - el.scrollTop;
  const toBottom = (how = 'jump') => {
    const el = scroller.current;
    if (!el) return;
    mine.current++;
    const top = Math.max(0, el.scrollHeight - el.clientHeight);
    if (how === 'smooth' && el.scrollTo) el.scrollTo({top, behavior: 'smooth'}); else el.scrollTop = top;
  };
  const settle = () => {
    timers.clearTimeout(settleTimer.current);
    settleTimer.current = timers.setTimeout(() => {
      scrolling.current = false;
      mine.current = 0;
      const el = scroller.current;
      if (el) dispatch({type: 'settled', fromBottom: fromBottom(el)});
      onSettle.current();
    }, touching.current ? fl.TOUCH_MS : fl.SETTLE_MS);
  };
  useEffect(() => { dispatch({type: 'rows', keys, waits, ended}); }, [keys.join('\n'), waits.join('\n'), ended]);
  const handlers = {
    onScroll: () => { scrolling.current = true; if (!touching.current) settle(); },
    onWheel: e => { if (e.deltaY < 0) dispatch({type: 'up'}); },
    onTouchStart: e => { touching.current = true; touchY.current = e.touches?.[0]?.clientY ?? 0; timers.clearTimeout(settleTimer.current); },
    onTouchMove: e => { const y = e.touches?.[0]?.clientY ?? 0; if (y > touchY.current + 4) dispatch({type: 'up'}); touchY.current = y; },
    onTouchEnd: () => { touching.current = false; settle(); },
    onPointerDown: e => { const el = scroller.current; if (el && e.offsetX > el.clientWidth) dispatch({type: 'up'}); },
    onKeyDown: e => { if (['ArrowUp', 'PageUp', 'Home'].includes(e.key)) dispatch({type: 'up'}); },
  };
  const touchY = useRef(0);
  return {s, state, dispatch, toBottom, handlers, scrolling, touching, onSettle, mine};
}

// Output: parts are the conversation's runs [{run, events, head}] (store outputs); density and onDensity the viewer's
// setting; onMore fetches the page before (a promise); renderAsk(step) draws the answer form of a pending question;
// target is a step to go to (a deep link); place and onPlace keep where the view was left; raw and onRaw show the
// lines as written; copy puts text on the clipboard; tools go at the top right (the page's own buttons); children
// (the composer) go under the timeline; linkOf(step) is the address of a step, copied for the selected one. bare is a
// preview: the timeline alone, without its tools and keys.
export function Output({bare = false, parts, density = 'standard', onDensity, onMore, onResend, renderAsk, target = '', place = null, onPlace, linkOf,
  raw = null, onRaw, copy = text => globalThis.navigator?.clipboard?.writeText?.(text), tools, children, timers = globalThis, active: activeProp}) {
  const w = useWords();
  const {t} = w;
  const phone = usePhone();
  const [open, setOpen] = useState(() => new Map());
  const [filter, setFilter] = useState('all');
  const [finding, setFinding] = useState(false);
  const [q, setQ] = useState(''), [query, setQuery] = useState('');
  const [hit, setHit] = useState(null);
  const [earlier, setEarlier] = useState(null);
  const [focused, setFocused] = useState(false);
  const [selected, setSelected] = useState('');
  const [flash, setFlash] = useState(target);
  const [holes, setHoles] = useState(() => new Map());
  const scroller = useRef(null), findInput = useRef(null), drawn = useRef([]), heights = useRef(new Map()), pageStarts = useRef(new Set());
  const anchor = useRef(place && !place.follow && place.anchor ? {key: place.anchor, offset: place.offset || 0, pinned: true} : null);

  const m = useMemo(() => out.model(parts), [parts]);
  const peek = useMemo(() => out.peekOf(hit), [hit]);
  const latest = useMemo(() => out.lay(m, {density, filter, open, peek}), [m, density, filter, open, peek]);
  const keys = out.counted(latest), waits = out.waits(latest);
  const last = parts.at(-1)?.run;
  const ended = !!last && !['queued', 'starting', 'running', 'unknown'].includes(last.state);
  const f = useFollow({scroller, keys, waits, ended, away: !!place && !place.follow || !!target, timers});
  const away = f.s.mode === 'away';

  // what is drawn: while away and scrolling, rows above the anchor stay as they were
  const rows = away && f.scrolling.current ? fl.hold(drawn.current, latest, anchor.current?.key) : latest;
  const hits = useMemo(() => (query ? out.find(m, query, {filter}) : []), [m, query, filter]);

  // the anchor, read from the DOM as it is before this draw changes it
  const el = scroller.current;
  if (el && away) {
    const list = el.querySelectorAll?.('[data-key]') || [];
    let lo = 0, hi = list.length - 1, found = null;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1, r = list[mid];
      if (r.offsetTop + r.offsetHeight > el.scrollTop) { found = r; hi = mid - 1; } else lo = mid + 1;
    }
    if (found && !anchor.current?.pinned) anchor.current = {key: found.getAttribute('data-key'), offset: found.offsetTop - el.scrollTop};
  }

  useLayoutEffect(() => {
    drawn.current = rows;
    const sc = scroller.current;
    if (!sc) return;
    for (const r of sc.querySelectorAll?.('.out-row:not(.cv)') || []) r.classList?.add('cv');
    if (f.s.mode === 'follow') { f.toBottom(); return; }
    const a = anchor.current;
    if (!a) return;
    const at = sc.querySelector?.(`[data-key="${cssKey(a.key)}"]`);
    if (!at) return;
    const d = fl.shift(a, at.offsetTop, sc.scrollTop);
    if (d) { f.mine.current++; sc.scrollTop += d; }
    if (a.pinned) anchor.current = null;
  });

  // growth while following sticks to the bottom; a phone's keyboard keeps a following view at its bottom
  useEffect(() => {
    const sc = scroller.current, RO = globalThis.ResizeObserver;
    if (!sc || !RO) return;
    let height = sc.scrollHeight;
    const ro = new RO(() => {
      const grew = sc.scrollHeight - height;
      height = sc.scrollHeight;
      if (f.state.current.mode === 'follow') f.toBottom(fl.stick({grew, screen: sc.clientHeight, touching: f.touching.current}));
      setHoles(h => (h.size ? new Map() : h));
    });
    ro.observe(sc);
    if (sc.firstElementChild) ro.observe(sc.firstElementChild);
    const vv = globalThis.visualViewport;
    const onVV = () => { if (f.state.current.mode === 'follow') f.toBottom(); };
    vv?.addEventListener?.('resize', onVV);
    return () => { ro.disconnect(); vv?.removeEventListener?.('resize', onVV); };
  }, [scroller.current]);

  // once the scrolling stops: redraw what was held, keep the place, fetch the page before near the top, and put far
  // pages out of the DOM
  f.onSettle.current = () => {
    const sc = scroller.current;
    if (!sc) return;
    setHoles(h => new Map(h));
    const a = anchor.current;
    onPlace?.({follow: f.state.current.mode === 'follow', anchor: a?.key || '', offset: a?.offset || 0});
    const head = latest[0];
    if (head?.type === 'head' && head.more && !head.loading && fl.nearTop(sc.scrollTop, sc.clientHeight)) onMore?.();
    const div = sc.querySelector?.('.out-newline');
    if (div && div.offsetTop < sc.scrollTop) f.dispatch({type: 'passed'});
    const pageEls = [...(sc.querySelectorAll?.('.out-page') || [])];
    if (latest.length > fl.KEEP_ROWS) {
      const doc = sc.ownerDocument, sel = doc?.getSelection?.();
      const pages = pageEls.map(p => {
        const key = p.getAttribute('data-page');
        return {key, top: p.offsetTop, height: p.offsetHeight || heights.current.get(key) || 0,
          keep: p.contains?.(doc?.activeElement) || !!(sel?.anchorNode && p.contains?.(sel.anchorNode))};
      });
      pages.forEach(p => heights.current.set(p.key, p.height));
      const far = fl.placeholders({rows: latest.length, pages, view: {top: sc.scrollTop, bottom: sc.scrollTop + sc.clientHeight}, screen: sc.clientHeight});
      setHoles(new Map([...far].map(i => [pages[i].key, pages[i].height])));
    }
  };

  // a target (a deep link, a find hit, the next error, what waits) is gone to: opened, scrolled to and flashed
  const goTo = (key, {how = 'jump'} = {}) => {
    f.dispatch({type: 'up'});
    setFlash(key);
    anchor.current = null;
    timers.setTimeout(() => {
      const sc = scroller.current, at = sc?.querySelector?.(`[data-key="${cssKey(key)}"]`);
      if (!sc || !at) return;
      f.mine.current++;
      sc.scrollTop = Math.max(0, at.offsetTop - sc.clientHeight / 3);
    }, 0);
    void how;
  };
  useEffect(() => {
    if (!target) return;
    const s = findStep(m, target);
    if (s) { setHit(s); goTo(s.key); }
  }, [target, !!findStep(m, target)]);

  useEffect(() => {
    const id = timers.setTimeout(() => setQuery(q.trim()), FIND_WAIT);
    return () => timers.clearTimeout(id);
  }, [q]);

  const stepHit = n => {
    const h = out.nextOf(hits, hit?.key, n);
    if (!h) return;
    setHit(h);
    goTo(h.key);
  };
  const closeFind = () => { setFinding(false); setHit(null); setQ(''); setQuery(''); setEarlier(null); };
  const findEarlier = async () => {
    const before = hits.length;
    setEarlier({busy: true, pages: 0});
    let pages = 0;
    for (; pages < FIND_PAGES; pages++) {
      const head = latestRef.current[0];
      if (head?.type !== 'head' || !head.more) break;
      await onMore?.();
      if (hitsRef.current.length > before) break;
    }
    setEarlier({busy: false, pages, asked: hitsRef.current.length === before && pages >= FIND_PAGES});
    if (hitsRef.current.length > before) { const h = hitsRef.current[0]; setHit(h); goTo(h.key); }
  };
  const latestRef = useRef(latest), hitsRef = useRef(hits);
  latestRef.current = latest;
  hitsRef.current = hits;

  const toggle = key => {
    const sc = scroller.current, at = sc?.querySelector?.(`[data-key="${cssKey(key)}"]`);
    if (sc && at && f.state.current.mode === 'away') anchor.current = {key, offset: at.offsetTop - sc.scrollTop, pinned: true};
    const row = latest.find(r => r.key === key);
    setSelected(key);
    setOpen(o => { const n = new Map(o); n.set(key, !(row ? row.open : false)); return n; });
    if (hit && peek.has(key)) setHit(null);
  };
  const turnOf = key => m.runs.flatMap(r => r.turns).find(tn => tn.steps.some(s => s.key === key)) || m.runs.at(-1)?.turns.at(-1);
  const unfold = () => {
    const tn = turnOf(selected);
    if (!tn) return;
    const stepRows = latest.filter(r => r.type === 'step' && tn.steps.some(s => s.key === r.key));
    const all = stepRows.length > 0 && stepRows.every(r => r.open);
    setOpen(o => { const n = new Map(o); n.set(tn.key, true); for (const s of tn.steps) n.set(s.key, !all); return n; });
  };
  const bottom = () => { f.dispatch({type: 'bottom'}); f.toBottom('smooth'); setHit(null); };
  const stepKeys = latest.filter(r => r.type === 'step' || r.type === 'turn').map(r => r.key);
  const moveSel = n => {
    const i = stepKeys.indexOf(selected);
    const k = stepKeys[i < 0 ? (n > 0 ? 0 : stepKeys.length - 1) : Math.min(stepKeys.length - 1, Math.max(0, i + n))];
    if (!k) return;
    setSelected(k);
    const sc = scroller.current, at = sc?.querySelector?.(`[data-key="${cssKey(k)}"]`);
    if (sc && at) { if (n < 0) f.dispatch({type: 'up'}); at.scrollIntoView?.({block: 'nearest'}); }
  };
  const active = !bare && (activeProp ?? (phone || focused));
  useActions('list', {
    end: {run: bottom},
    start: {run: () => { f.dispatch({type: 'up'}); const sc = scroller.current; if (sc) { f.mine.current++; sc.scrollTop = 0; } }},
    unfold: {run: unfold},
    find: {run: () => { setFinding(true); timers.setTimeout(() => findInput.current?.focus?.(), 0); }},
    next: {run: () => moveSel(1)},
    prev: {run: () => moveSel(-1)},
    toggle: {run: () => toggle(selected), when: () => !!selected},
    open: {run: () => toggle(selected), when: () => !!selected},
  }, {active});

  const errs = out.errors(m);
  const nextError = () => {
    const h = out.nextOf(errs, hit?.key, 1);
    if (h) { setHit(h); goTo(h.key); }
  };
  const b = fl.badge(f.s);
  const selStep = latest.find(r => r.key === selected && r.type === 'step')?.step;
  const top = rows[0]?.type === 'head' ? rows[0] : null;
  const pages = fl.pages(top ? rows.slice(1) : rows, pageStarts.current);
  pageStarts.current = new Set(pages.map(p => p[0].key));
  const divider = f.s.divider;
  const drawRow = r => [
    r.key === divider && html`<div class="out-newline" key="newline" role="separator"><span>${t('out.newLine')}</span></div>`,
    html`<${Row} key=${r.key} row=${r} density=${density} onToggle=${toggle} onMore=${onMore} onResend=${onResend} renderAsk=${renderAsk}
      copy=${copy} target=${flash} selected=${selected} />`];

  const bar = html`<div class=${cx('out-tools', phone && 'out-tools-phone')}>
    <${Chips} label=${t('out.filter')}>${out.filters.map(x => html`<${Chip} label=${t('out.f.' + x)} on=${filter === x} onClick=${() => setFilter(x)} />`)}<//>
    <span class="out-tools-end">
      <${Button} kind="quiet" disabled=${!errs.length} title=${errs.length ? undefined : t('out.noError')} onClick=${nextError}>${t('out.nextError')}<//>
      ${onDensity && html`<${Segmented} label=${t('out.density')} value=${density} onChange=${onDensity} options=${dt.names.map(d => ({value: d, label: t('out.d.' + d)}))} />`}
      ${onRaw && html`<${Button} kind="quiet" on=${raw !== null} onClick=${onRaw}>${t('out.raw')}<//>`}
      ${linkOf && selStep && html`<${Button} kind="quiet" onClick=${() => { copy(linkOf(selStep)); }}>${t('out.copyLink')}<//>`}
      ${phone && html`<${Button} kind="quiet" icon="search" label=${t('out.find')} onClick=${() => setFinding(true)} />
        <${Button} kind="quiet" on=${!away} onClick=${() => (away ? bottom() : f.dispatch({type: 'up'}))}>${away ? t('out.follow') : t('out.pause')}<//>`}
      ${tools}
    </span>
  </div>`;

  return html`<section class=${cx('out', phone && 'out-phone', bare && 'out-bare')} aria-label=${t('out.label')}
    onFocusIn=${() => setFocused(true)} onFocusOut=${e => { if (!e.currentTarget.contains?.(e.relatedTarget)) setFocused(false); }}>
    ${!bare && bar}
    ${m.plan && raw === null && html`<${PlanPin} step=${m.plan} />`}
    ${finding && html`<${FindBar} q=${q} setQ=${setQ} hits=${hits} at=${hit?.key} onStep=${stepHit} onClose=${closeFind} inputRef=${findInput}
      onEarlier=${latest[0]?.type === 'head' && latest[0].more ? findEarlier : null} earlier=${earlier} />`}
    <div class="out-view"><div class="out-scroll" ref=${scroller} tabindex="0" ...${f.handlers}>
      ${raw !== null ? html`<div class="out-raw"><span class="t-muted">${t('out.rawNote')}</span><pre class="out-pre">${raw}</pre></div>`
        : !rows.length ? html`<p class="empty">${t('out.empty')}</p>`
        : [top && drawRow(top), ...pages.map(p => holes.has(p[0].key) ? html`<div class="out-page out-hole" key=${'p' + p[0].key} data-page=${p[0].key} style=${{height: holes.get(p[0].key) + 'px'}}></div>`
          : html`<div class="out-page" key=${'p' + p[0].key} data-page=${p[0].key}>${p.flatMap(drawRow)}</div>`)]}
    </div>
    <${NewBadge} b=${hit || finding ? (away ? {kind: 'latest', n: 0} : null) : b} onGo=${bottom} onWaiting=${k => goTo(k)} /></div>
    ${children}
  </section>`;
}

const cssKey = k => String(k).replace(/["\\]/g, '\\$&');

// findStep is the step a target names: by its key, one of its events' ids (a group's member), or a result's.
function findStep(m, id) {
  if (!id) return null;
  const walk = (s, turn, agents, run) => {
    if (s.key === id || s.id === id || (s.members || []).some(x => x.id === id)) return {key: s.key, turn, agents, run};
    for (const k of s.kids || []) { const x = walk(k, turn, [...agents, s.key], run); if (x) return x; }
    return null;
  };
  for (const r of m.runs) for (const tn of r.turns) for (const s of tn.steps) { const x = walk(s, tn.key, [], r.id); if (x) return x; }
  return null;
}
