// AnswerForm answers what a run asks, where it is asked: in the output's ask row and in a waiting item on the home.
// A permission is allowed once, allowed for the rest of the run (when the coordinator offers it), or denied with a
// reason; questions get one answer each, picked from their options or written, and go together.
import {useState} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords} from './base.js';
import {Button} from './controls.js';
import {Markdown} from './markdown.js';
import {register} from '../core/i18n.js';

register('answer', {
  'ans.allow': ['允许', 'Allow'], 'ans.allowRun': ['这次运行里这条命令都允许', 'Allow this command for the rest of the run'],
  'ans.deny': ['拒绝', 'Deny'], 'ans.why': ['拒绝的理由（可不填）', 'Why not (optional)'],
  'ans.send': ['回答', 'Answer'], 'ans.skip': ['不回答', 'Don’t answer'], 'ans.own': ['或者自己写', 'Or write your own'],
  'ans.multi': ['可多选', 'Pick any'], 'ans.left': ['还有 %d 个问题没答', '%d {question|questions} left to answer'],
  'ans.goneBy': ['已经被 %s 答了', '%s answered it already'], 'ans.gone': ['这个请求已经不在了', 'It is no longer asked'],
  'ans.open': ['作答…', 'Answer…'], 'ans.asks': ['要你批准：%s', 'Asks to run: %s'],
  'ans.onceHint': ['只这一次，下次还会问你', 'This once; it asks again next time'],
  'ans.runHint': ['这次运行里同样的命令不再问', 'Not asked again for this command in this run'],
  'ans.denyHint': ['理由会转给 agent，它会换个做法或停下来问你', 'The reason goes to the agent: it tries another way or stops to ask'],
});

// isPermission tells a request that asks leave to run a tool from one that asks questions.
export const isPermission = req => !!req && (req.kind === 'permission' || !(req.questions || []).length);

// answersOf is what run.answer's answers carry for picks {question: [labels]} and own words {question: text}: the
// written words when there are any, else the picked labels joined as the node expects.
export function answersOf(questions, picks, own) {
  const out = {};
  for (const q of questions) {
    const text = (own[q.question] || '').trim();
    const chosen = picks[q.question] || [];
    if (text) out[q.question] = text;
    else if (chosen.length) out[q.question] = chosen.join(', ');
  }
  return out;
}

// allowsRun: req may be allowed for the rest of its run, both as the request says and as the run's affordances (acts)
// give the viewer.
export const allowsRun = (req, acts) => !!req?.allow_run && (acts || []).includes('allow_run');

// quickOf is what a request can be answered with in one press: allow and deny (scope: and allow for the run), or the
// options of its one single-choice question. The page puts them on 1–9.
export function quickOf(t, req, {scope = false} = {}) {
  if (!req) return [];
  if (isPermission(req)) {
    return [{label: t('ans.allow'), kind: 'primary', params: {allow: true}},
      ...(scope ? [{label: t('ans.allowRun'), params: {allow: true, decision: 'allow_run'}}] : []),
      {label: t('ans.deny'), params: {allow: false}}];
  }
  const qs = req.questions || [];
  if (qs.length !== 1 || qs[0].multi) return [];
  return (qs[0].options || []).slice(0, 9).map(o => ({label: o, params: {allow: true, answers: {[qs[0].question]: o}}}));
}

// AnswerForm: onAnswer(params) sends run.answer's params (without run and request); scope offers allow_run (allowsRun);
// gone, once an answer came back request_gone, is who answered first ('' when nobody did): the form says so in place of
// the ways to answer (the row or item it is in already says what was asked).
export function AnswerForm({req, scope = false, busy = false, onAnswer, detail = '', gone}) {
  const {t, f} = useWords();
  const phone = usePhone();
  const [picks, setPicks] = useState({});
  const [own, setOwn] = useState({});
  const [why, setWhy] = useState('');
  if (!req) return null;
  if (gone !== undefined) {
    return html`<div class="answer answer-gone" role="status"><p class="answer-note">${gone ? f('ans.goneBy', gone) : t('ans.gone')}</p></div>`;
  }
  const submitKey = e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && !e.isComposing) { e.preventDefault(); e.currentTarget.form?.requestSubmit?.(); } };

  if (isPermission(req)) {
    const text = req.summary || detail;
    return html`<div class="answer" role="group" aria-label=${f('ans.asks', req.tool || '')}>
      ${req.tool && html`<div class="answer-q">${f('ans.asks', req.tool)}</div>`}
      ${text && html`<pre class="box">${text}</pre>`}
      <div class=${cx('answer-acts', phone && 'answer-acts-phone')}>
        ${quickOf(t, req, {scope}).filter(q => q.params.allow).map((q, i) => html`<button type="button" class=${cx('choice', q.kind, phone && 'choice-hinted')} disabled=${busy}
          onClick=${() => onAnswer(q.params)}>${phone ? html`<b>${q.label}</b><span class="choice-hint">${t(q.params.decision ? 'ans.runHint' : 'ans.onceHint')}</span>`
            : html`<span class="mono choice-n">${i + 1}</span>${q.label}`}</button>`)}
      </div>
      <form class="answer-own" onSubmit=${e => { e.preventDefault(); onAnswer({allow: false, ...(why.trim() ? {message: why.trim()} : {})}); }}>
        <input class="in" value=${why} placeholder=${t('ans.why')} aria-label=${t('ans.why')} onInput=${e => setWhy(e.currentTarget.value)} onKeyDown=${submitKey} />
        <button type="submit" class="btn danger" disabled=${busy}>${t('ans.deny')}</button>
      </form>
      ${phone && html`<p class="answer-hint">${t('ans.denyHint')}</p>`}
    </div>`;
  }

  const qs = req.questions || [];
  const answers = answersOf(qs, picks, own);
  const left = qs.filter(q => !answers[q.question]).length;
  const pick = (q, o) => setPicks(p => {
    const had = p[q.question] || [];
    const next = q.multi ? (had.includes(o) ? had.filter(x => x !== o) : [...had, o]) : (had[0] === o ? [] : [o]);
    return {...p, [q.question]: next};
  });
  const send = e => { e.preventDefault(); if (!left) onAnswer({allow: true, answers}); };
  return html`<form class="answer" onSubmit=${send}>
    ${qs.map((q, n) => html`<fieldset class="answer-block" key=${q.question}>
      <legend class="answer-q">${q.header && html`<span class="chip">${q.header}</span> `}${qs.length > 1 && html`<span class="mono t-muted">${n + 1}/${qs.length} </span>`}</legend>
      <${Markdown} text=${q.question} />
      ${q.multi && html`<span class="t-muted answer-note">${t('ans.multi')}</span>`}
      ${(q.options || []).length > 0 && html`<div class=${cx('answer-opts', phone && 'answer-acts-phone')} role=${q.multi ? 'group' : 'radiogroup'} aria-label=${q.question}>
        ${q.options.map((o, i) => {
          const on = (picks[q.question] || []).includes(o);
          const said = q.descriptions?.[i];
          return html`<button type="button" role=${q.multi ? 'checkbox' : 'radio'} aria-checked=${on ? 'true' : 'false'} class=${cx('choice', on && 'on', said && 'choice-said')}
            disabled=${busy} onClick=${() => pick(q, o)}>${!phone && qs.length === 1 && html`<span class="mono choice-n">${i + 1}</span>`}<span class="choice-body"><span class="choice-label">${o}</span>${said && html`<span class="choice-hint">${said}</span>`}</span></button>`;
        })}
      </div>`}
      <input class="in" value=${own[q.question] || ''} placeholder=${t('ans.own')} aria-label=${t('ans.own')} onKeyDown=${submitKey}
        onInput=${e => { const v = e.currentTarget.value; setOwn(o => ({...o, [q.question]: v})); }} />
    </fieldset>`)}
    <div class="answer-foot">
      ${left > 0 && qs.length > 1 && html`<span class="t-muted">${f('ans.left', left)}</span>`}
      <button type="button" class="btn quiet" disabled=${busy} onClick=${() => onAnswer({allow: false})}>${t('ans.skip')}</button>
      <${Button} kind="primary" type="submit" disabled=${busy || left > 0}>${t('ans.send')}<//>
    </div>
  </form>`;
}
