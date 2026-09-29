// Conversation is a task's talk with its agent: the runs of one conversation as one timeline (Output), the answers to
// what it asks where it asks, and the box that talks to it (Composer), sent where the coordinator routes it. The
// latest run's output is watched; earlier runs are paged in as the viewer scrolls up to them.
import {useState, useEffect, useMemo, useRef} from '../vendor/hooks.mjs';
import {html, useWords, useSignalValue} from '../ui/base.js';
import {Button} from '../ui/controls.js';
import {Modal} from '../ui/overlay.js';
import {Output} from '../ui/output.js';
import {Composer} from '../ui/composer.js';
import {AnswerForm, allowsRun} from '../ui/answer.js';
import {conversation} from '../core/fold.js';
import {readPlace, keepPlace} from '../core/follow.js';
import {link} from '../core/router.js';
import {unsure} from '../core/commands.js';
import {code} from '../core/proto.js';
import {register} from '../core/i18n.js';

register('conversation', {
  'conv.interrupt': ['打断这一轮', 'Interrupt this turn'], 'conv.confirmInterrupt': ['打断这一轮？', 'Interrupt this turn?'],
  'conv.confirmInterruptNote': ['%s 会停下这一轮，运行随之结束；之后可以接着这个会话再说。', '%s stops this turn and the run ends; you can go on with the session after.'],
  'conv.confirmSay': ['打断并改说法？', 'Interrupt and say this instead?'],
  'conv.confirmSayNote': ['先打断 %s 正在做的这一轮，再用这条消息接着跑。', 'Interrupts the turn %s is in, then goes on with this message.'],
  'conv.keep': ['先不打断', 'Keep going'], 'conv.sent': ['消息已发出', 'Message sent'], 'conv.answered': ['已作答', 'Answered'],
  'conv.routeChanged': ['去向变了，这条消息没有发出：请看新的去向再发', 'Where it goes changed, so it was not sent: check where it goes now and send again'],
  'conv.cannot': ['发不出去：%s', 'Cannot send: %s'], 'conv.interrupted': ['已打断这一轮', 'Interrupted the turn'],
  'conv.none': ['这个任务还没有运行', 'This task has not run yet'],
});


// idOf numbers the event lists a store hands out: a new list is a change, the same one is not.
const ids = new WeakMap();
let nextID = 1;
const idOf = list => { if (!ids.has(list)) ids.set(list, nextID++); return ids.get(list); };
const address = () => (globalThis.location ? globalThis.location.origin + globalThis.location.pathname : '');
const sessionStore = () => { try { return globalThis.sessionStorage; } catch { return null; } };

// useOutputs holds the outputs of runs (the last watched, the others paged) and gives their events and heads. A run
// kept across a change of ids keeps its output as it was taken: new ones are taken before the ones left are released.
function useOutputs(store, ids, watched) {
  const [, redraw] = useState(0);
  const held = useRef(new Map());
  const key = ids.join(',') + '|' + watched;
  useEffect(() => {
    const next = new Map();
    for (const id of ids) {
      const had = held.current.get(id);
      if (had) { next.set(id, had); continue; }
      const o = store.output(id, {watch: id === watched});
      const bump = () => redraw(n => n + 1);
      next.set(id, {o, stops: [o.events.subscribe(bump), o.head.subscribe(bump)]});
    }
    for (const [id, h] of held.current) if (next.get(id) !== h) { for (const s of h.stops) s(); h.o.release(); }
    held.current = next;
    redraw(n => n + 1);
  }, [key]);
  useEffect(() => () => { for (const h of held.current.values()) { for (const s of h.stops) s(); h.o.release(); } held.current = new Map(); }, []);
  return new Map([...held.current].map(([id, h]) => [id, h.o]));
}

// Conversation: run is the run whose conversation shows (the task's latest when empty); target a step to go to;
// prefs holds the viewer's output density; session is where the view's place is kept (the tab's sessionStorage).
export function Conversation({store, commands, toasts, prefs, task, run = '', target = '', session = sessionStore(), copy}) {
  const w = useWords();
  const {t, f} = w;
  useSignalValue(store.rev.runs);
  const aff = useSignalValue(store.affordances);
  const density = useSignalValue(prefs.output);
  useSignalValue(commands.pending);
  const st = store.state;
  const latest = Object.values(st.runs).filter(r => r.task === task.id).sort((a, b) => (b.seq || 0) - (a.seq || 0))[0];
  const conv = conversation(st, run && st.runs[run] ? run : latest?.id || '');
  const last = conv.at(-1);
  const root = conv[0]?.id || '';
  const [from, setFrom] = useState(Math.max(0, conv.length - 1));
  const [confirm, setConfirm] = useState(null);
  const [gone, setGone] = useState({});
  const [raw, setRaw] = useState(null);
  const [drafts, setDrafts] = useState({});
  useEffect(() => { setFrom(Math.max(0, conv.length - 1)); setRaw(null); }, [root, conv.length]);
  const shown = conv.slice(Math.min(from, Math.max(0, conv.length - 1)));
  const outputs = useOutputs(store, shown.map(r => r.id), last?.id || '');
  const place = useMemo(() => readPlace(session, root), [root]);

  const heads = shown.map(r => outputs.get(r.id)?.head.value || {more: true});
  const parts = shown.map((r, i) => {
    const o = outputs.get(r.id);
    let head = heads[i];
    if (i === 0 && head.start && from > 0) head = {more: true};
    const taken = (r.takes || []).map(id => (st.runs[r.parent]?.sends || []).find(m => m.id === id)).filter(Boolean);
    return {run: r, events: o?.events.value || [], head: i === 0 ? head : head.gone ? head : null, taken};
  });
  const partsKey = parts.map(p => p.run.id + ':' + idOf(p.events) + ':' + JSON.stringify(p.head) + ':' + (p.run.state || '') + ':' + (p.run.requests || []).map(q => q.id).join() + ':' + (p.run.sends || []).map(m => m.id + m.state).join() + ':' + p.taken.map(m => m.id).join()).join('|');
  const memoParts = useMemo(() => parts, [partsKey]);

  const more = () => {
    const first = shown[0];
    const o = outputs.get(first?.id);
    if (!o) return Promise.resolve();
    if (o.head.value.start && from > 0) { setFrom(from - 1); return Promise.resolve(); }
    return o.more();
  };
  // findBack says whether q is shown before what is loaded: in the run at the top, then in each earlier run
  const findBack = store.canFind() ? async q => {
    if (await outputs.get(shown[0]?.id)?.find(q)) return true;
    for (let i = conv.length - shown.length - 1; i >= 0; i--) if (await store.find(conv[i].id, q)) return true;
    return false;
  } : null;
  // an earlier run brought in by scrolling up starts from its last page
  useEffect(() => {
    const o = outputs.get(shown[0]?.id);
    if (o && shown[0].id !== last?.id && !o.events.value.length && o.head.value.more && !o.head.value.loading) o.more();
  }, [shown[0]?.id, outputs.get(shown[0]?.id)]);
  if (!last) return html`<p class="empty">${t('conv.none')}</p>`;

  const failed = e => e.code !== code.requestGone && toasts.show({text: unsure.includes(e.code) ? f('app.unsure', e.code) : e.code === code.routeChanged ? t('conv.routeChanged')
    : e.code === code.cannotSend ? f('conv.cannot', e.detail || e.code) : f('app.failed', e.code || String(e.message || e)), tone: 'danger'});
  const send = (method, params, key) => commands.send(method, params, {key}).catch(e => { failed(e); throw e; });

  const route = aff.tasks?.[task.id]?.route || null;
  const acts = aff.runs?.[last.id] || [];
  const busy = commands.state('run:' + last.id) === 'pending' || commands.state('task:' + task.id) === 'pending';

  const message = ({text, mode}) => {
    const params = {id: task.id, text, ...(mode && route?.to === 'run' ? {mode} : {}),
      ...(route ? {expect: {to: route.to, ...(route.run ? {run: route.run} : {}), ...(route.stage ? {stage: route.stage} : {}), ...(route.version ? {version: route.version} : {})}} : {})};
    return send('task.message', params, 'task:' + task.id).then(() => { toasts.show({text: t('conv.sent')}); return true; }, () => false);
  };
  const onSend = x => {
    if (x.mode !== 'interrupt') return message(x);
    return new Promise(resolve => setConfirm({title: t('conv.confirmSay'), note: f('conv.confirmSayNote', last.agent), label: t('cmp.interrupt'),
      go: () => message(x).then(resolve), cancel: () => resolve(false)}));
  };
  const turn = (() => { const evs = outputs.get(last.id)?.events.value || []; for (let i = evs.length - 1; i >= 0; i--) if (evs[i].turn) return evs[i].turn; return 0; })();
  const interrupt = () => setConfirm({title: t('conv.confirmInterrupt'), note: f('conv.confirmInterruptNote', last.agent), label: t('conv.interrupt'),
    go: () => send('run.interrupt', {run: last.id, ...(turn ? {turn} : {})}, 'run:' + last.id).then(() => toasts.show({text: t('conv.interrupted')}), () => {})});
  const answer = (s, params) => send('run.answer', {run: s.run, request: s.request, ...params}, 'run:' + s.run)
    .then(() => toasts.show({text: t('conv.answered')}), e => { if (e.code === code.requestGone) setGone(g => ({...g, [s.run + '\n' + s.request]: e.detail || ''})); });
  const renderAsk = s => {
    const r = st.runs[s.run];
    const req = (r?.requests || []).find(q => q.id === s.request);
    return html`<${AnswerForm} req=${req} scope=${allowsRun(req, aff.runs?.[s.run])} gone=${gone[s.run + '\n' + s.request]} busy=${commands.state('run:' + s.run) === 'pending'}
      onAnswer=${p => answer(s, p)} />`;
  };
  const toggleRaw = () => {
    if (raw !== null) { setRaw(null); return; }
    store.raw(last.id).then(x => setRaw(x ?? ''), e => failed(e));
  };

  return html`<div class="conv">
    <${Output} key=${root} parts=${memoParts} density=${density} onDensity=${prefs.setOutput} onMore=${more} onFind=${findBack} renderAsk=${renderAsk} target=${target}
      place=${place} onPlace=${p => keepPlace(session, root, p)} raw=${raw} onRaw=${toggleRaw} copy=${copy}
      onResend=${m => message({text: m.text, mode: ''})} linkOf=${s => address() + link(task.id, s.run, s.id)}>
      <${Composer} route=${route} acts=${acts} machine=${last.machine} busy=${busy} onSend=${onSend}
        tools=${acts.includes('interrupt') && html`<${Button} kind="danger" disabled=${busy} onClick=${interrupt}>${t('conv.interrupt')}<//>`}
        draft=${drafts[last.id] || ''} onDraft=${v => setDrafts(d => ({...d, [last.id]: v}))} />
    <//>
    ${confirm && html`<${Modal} title=${confirm.title} onClose=${() => { confirm.cancel?.(); setConfirm(null); }}
      actions=${[{label: t('conv.keep'), onClick: () => { confirm.cancel?.(); setConfirm(null); }},
        {label: confirm.label, kind: 'primary', keyName: 'Mod+Enter', onClick: () => { const c = confirm; setConfirm(null); c.go(); }}]}><p>${confirm.note}</p><//>`}
  </div>`;
}
