// charts are the home's small figures: Spark (a line under a number), Bars (days side by side, each split into
// parts) and Timeline (a lane per machine over the day). Each works out its own scale; sizes go through style
// objects, which reach the page as CSSOM, not as style attributes.
import {html, cx, useWords} from './base.js';
import {clock} from '../core/format.js';

const W = 130, H = 30;

// Spark: values oldest first; step draws them as steps (a count held over each bucket) rather than a line.
export function Spark({values, max, step = false, label, tone = 'accent'}) {
  if (!values?.length) return null;
  const top = max || Math.max(1, ...values);
  const y = v => (H - 1 - (v / top) * (H - 4)).toFixed(1);
  const pts = step
    ? values.flatMap((v, i) => [`${(i * W / values.length).toFixed(1)},${y(v)}`, `${((i + 1) * W / values.length).toFixed(1)},${y(v)}`])
    : values.map((v, i) => `${(values.length === 1 ? W : i * W / (values.length - 1)).toFixed(1)},${y(v)}`);
  const [lx, ly] = pts[pts.length - 1].split(',');
  return html`<svg class=${cx('spark', 'tone-' + tone)} viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" role="img" aria-label=${label}>
    <polyline points=${pts.join(' ')} fill="none" vector-effect="non-scaling-stroke"></polyline>
    <circle cx=${lx} cy=${ly} r="2.5"></circle>
  </svg>`;
}

// Bars: bars [{label, parts: {key: value}, title, now}] oldest first; keys [{key, label}] name the parts, which the
// CSS colours by their order (part-0, part-1, …); format spells a value for the axis.
export function Bars({bars, keys, format = String, label}) {
  const totals = bars.map(b => keys.reduce((n, k) => n + (b.parts[k.key] || 0), 0));
  const top = Math.max(1, ...totals);
  return html`<figure class="bars" aria-label=${label}>
    <div class="bars-plot">
      <div class="bars-axis mono" aria-hidden="true"><span>${format(top)}</span><span>${format(top / 2)}</span><span>0</span></div>
      <div class="bars-cols">
        ${bars.map((b, i) => html`<div class="bars-col" title=${b.title}>
          <div class="bars-stack">
            ${keys.map((k, j) => b.parts[k.key] ? html`<span class=${'part-' + j} style=${{height: (b.parts[k.key] / top * 100).toFixed(1) + '%'}}></span>` : null)}
          </div>
          <span class=${cx('bars-label', b.now && 'now')}>${b.label}</span>
          <span class="sr-only">${b.title || format(totals[i])}</span>
        </div>`)}
      </div>
    </div>
    ${keys.length > 1 && html`<figcaption class="bars-keys">${keys.map((k, j) => html`<span><i class=${'part-' + j} aria-hidden="true"></i>${k.label}</span>`)}</figcaption>`}
  </figure>`;
}

const hourMS = 3600e3;
const pct = (t, from, to) => ((t - from) / (to - from) * 100).toFixed(2) + '%';

// ⚠️ Segment kinds, coloured like their states: running, and how a run ended.
export const segKinds = ['running', 'exited', 'failed', 'stopped', 'canceled', 'abandoned', 'unknown'];

// Timeline: lanes [{name, state (a Status state), note, tone, rows: [[{from, to, kind, title, onClick}]]}] between
// from and to (ms, to is now); a tick on each even hour.
export function Timeline({from, to, lanes, label}) {
  const {t} = useWords();
  const ticks = [];
  // ⚠️ A tick closer to now than a tenth of the span would run into now's label.
  for (let h = Math.ceil(from / hourMS) * hourMS; h <= to - (to - from) / 10; h += hourMS) if (new Date(h).getHours() % 2 === 0) ticks.push(h);
  const seg = s => {
    const box = {left: pct(s.from, from, to), width: `max(2px, ${((s.to - s.from) / (to - from) * 100).toFixed(2)}%)`};
    return s.onClick
      ? html`<button type="button" class=${'tl-seg bar-' + s.kind} style=${box} title=${s.title} aria-label=${s.title} onClick=${s.onClick}></button>`
      : html`<span class=${'tl-seg bar-' + s.kind} style=${box} title=${s.title}></span>`;
  };
  return html`<figure class="timeline" aria-label=${label}>
    <div class="tl-head" aria-hidden="true"><span></span><div class="tl-ticks">
      ${ticks.map(h => html`<span class="tl-tick" style=${{left: pct(h, from, to)}}>${clock(h)}</span>`)}
      <span class="tl-tick tl-now-label" style=${{left: '100%'}}>${clock(to)}</span>
    </div></div>
    ${lanes.map(l => html`<div class="tl-lane">
      <div class="tl-name"><span class=${cx('mono', 'ell')}>${l.name}</span>${l.note && html`<span class=${cx('tl-note', l.tone && 't-' + l.tone)}>${l.note}</span>`}</div>
      <div class="tl-rows">
        ${l.rows.map(row => html`<div class="lane">${row.map(seg)}</div>`)}
        <span class="tl-now" aria-hidden="true"></span>
      </div>
    </div>`)}
    <figcaption class="tl-keys">${['running', 'exited', 'failed', 'stopped'].map(k => html`<span><i class=${'bar-' + k} aria-hidden="true"></i>${t('chart.' + k)}</span>`)}</figcaption>
  </figure>`;
}

// Meter is one bar split by parts [{key, value, label}], coloured as the states they are (bar-<key>), with a legend.
export function Meter({parts, label}) {
  const total = parts.reduce((n, p) => n + p.value, 0);
  const shown = parts.filter(p => p.value > 0);
  return html`<figure class="meter" aria-label=${label}>
    <div class="meter-bar">${total > 0 && shown.map(p => html`<span class=${'bar-' + p.key} style=${{width: (p.value / total * 100).toFixed(2) + '%'}}></span>`)}</div>
    <figcaption class="meter-keys">${shown.map(p => html`<span><i class=${'bar-' + p.key} aria-hidden="true"></i><b class="mono">${p.value}</b> ${p.label}</span>`)}</figcaption>
  </figure>`;
}
