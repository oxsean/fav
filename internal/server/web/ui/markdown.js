// Markdown draws a brief or a question as elements, never as HTML: headings, paragraphs, lists, quotes, code blocks,
// and inline code, bold, italics and links. A link goes to http, https or this site only; any other is its text.
import {html} from './base.js';

// safeHref is where a link may go: an http(s) address or a path, query or fragment of this site; null otherwise.
export function safeHref(url) {
  const u = String(url || '').trim();
  if (/^https?:\/\/[^\s]+$/i.test(u)) return u;
  if (/^[/?#]/.test(u) && !u.startsWith('//')) return u;
  return null;
}

// blocks splits text into [{kind: h | p | ul | ol | quote | code, level, lines | items | text}].
export function blocks(text) {
  const lines = String(text || '').replace(/\r\n?/g, '\n').split('\n');
  const out = [];
  let para = null;
  const end = () => { para = null; };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const fence = line.match(/^\s*(```|~~~)/);
    if (fence) {
      end();
      const code = [];
      for (i++; i < lines.length && !lines[i].trim().startsWith(fence[1]); i++) code.push(lines[i]);
      out.push({kind: 'code', text: code.join('\n')});
      continue;
    }
    if (!line.trim()) { end(); continue; }
    const h = line.match(/^(#{1,6})\s+(.*)$/);
    if (h) { end(); out.push({kind: 'h', level: Math.min(h[1].length + 2, 6), text: h[2].replace(/\s#+\s*$/, '')}); continue; }
    const li = line.match(/^\s*(?:([-*+])|(\d+)[.)])\s+(.*)$/);
    if (li) {
      end();
      const kind = li[1] ? 'ul' : 'ol';
      const last = out[out.length - 1];
      if (last?.kind === kind && last.open) last.items.push(li[3]);
      else out.push({kind, items: [li[3]], open: true});
      continue;
    }
    const q = line.match(/^\s*>\s?(.*)$/);
    if (q) {
      end();
      const last = out[out.length - 1];
      if (last?.kind === 'quote' && last.open) last.lines.push(q[1]);
      else out.push({kind: 'quote', lines: [q[1]], open: true});
      continue;
    }
    const last = out[out.length - 1];
    if (!para && last?.open && /^\s{2,}/.test(line)) {
      if (last.items) last.items[last.items.length - 1] += ' ' + line.trim();
      else last.lines.push(line.trim());
      continue;
    }
    if (last) delete last.open;
    if (para) para.lines.push(line);
    else out.push(para = {kind: 'p', lines: [line]});
  }
  for (const b of out) delete b.open;
  return out;
}

// Underscores mark text only from outside a word, as in GitHub's Markdown: snake_case and ids stay as they are.
const inlineRe = /`([^`]+)`|\*\*([^*]+)\*\*|(?<![\p{L}\p{N}_])__([^_]+)__(?![\p{L}\p{N}_])|\[([^\]]+)\]\(([^)\s]+)\)|\*([^*\s][^*]*)\*|(?<![\p{L}\p{N}_])_([^_\s][^_]*)_(?![\p{L}\p{N}_])|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])/gu;

// inline draws the spans of one line of text.
export function inline(text) {
  const out = [];
  let at = 0;
  for (const m of String(text).matchAll(inlineRe)) {
    if (m.index > at) out.push(text.slice(at, m.index));
    const [whole, code, bold, bold2, label, url, em, em2, bare] = m;
    if (code !== undefined) out.push(html`<code>${code}</code>`);
    else if (bold !== undefined || bold2 !== undefined) out.push(html`<strong>${inline(bold ?? bold2)}</strong>`);
    else if (label !== undefined) {
      const href = safeHref(url);
      out.push(href ? html`<a href=${href} target=${href.startsWith('http') ? '_blank' : undefined} rel="noopener noreferrer">${inline(label)}</a>` : whole);
    } else if (em !== undefined || em2 !== undefined) out.push(html`<em>${inline(em ?? em2)}</em>`);
    else if (bare !== undefined) out.push(html`<a href=${bare} target="_blank" rel="noopener noreferrer">${bare}</a>`);
    at = m.index + whole.length;
  }
  if (at < text.length) out.push(text.slice(at));
  return out;
}

const withBreaks = lines => lines.flatMap((l, i) => (i ? [html`<br />`, ...inline(l)] : inline(l)));

export function Markdown({text, empty = ''}) {
  const bs = blocks(text);
  if (!bs.length) return empty ? html`<p class="md-empty">${empty}</p>` : null;
  return html`<div class="md">${bs.map(b => {
    switch (b.kind) {
      case 'h': {
        const tag = 'h' + b.level;
        return html`<${tag} class="md-h">${inline(b.text)}<//>`;
      }
      case 'code': return html`<pre class="md-code"><code>${b.text}</code></pre>`;
      case 'ul': return html`<ul>${b.items.map(x => html`<li>${inline(x)}</li>`)}</ul>`;
      case 'ol': return html`<ol>${b.items.map(x => html`<li>${inline(x)}</li>`)}</ol>`;
      case 'quote': return html`<blockquote>${withBreaks(b.lines)}</blockquote>`;
      default: return html`<p>${withBreaks(b.lines)}</p>`;
    }
  })}</div>`;
}
