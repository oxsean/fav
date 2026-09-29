// Composer is the box under a conversation that talks to its agent. Before anything is sent it says where the message
// will go, as the coordinator routes it (task.message's route): into the running turn, after it, in place of it, on in
// a new run of the session, or to the workflow's next stage. It shows how the last messages got on, and sends with
// Mod+Enter.
import {useState} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords} from './base.js';
import {Button, Segmented} from './controls.js';
import {register} from '../core/i18n.js';

register('composer', {
  'cmp.label': ['给 agent 发消息', 'Message the agent'], 'cmp.hint': ['写给 agent 的话…', 'Write to the agent…'],
  'cmp.send': ['发送', 'Send'], 'cmp.mode': ['怎么发', 'How to send'],
  'cmp.steer': ['插话', 'Steer'], 'cmp.after': ['结束后再说', 'After this turn'], 'cmp.interrupt': ['打断并改说法', 'Interrupt and say'],
  'cmp.to.steer': ['插进正在跑的这一轮', 'Goes into the turn that runs now'],
  'cmp.to.after': ['等这一轮结束，在 %s 上接着这个会话再跑一次', 'Waits for this turn to end, then goes on with the session on %s'],
  'cmp.to.interrupt': ['先打断这一轮，再用这条消息在 %s 上接着跑', 'Interrupts this turn, then goes on with this message on %s'],
  'cmp.to.reply': ['在 %s 上接着这个会话再跑一次', 'Goes on with this session in a new run on %s'],
  'cmp.to.workpad': ['记下来，交给下一阶段 %s', 'Kept for the next stage, %s'],
  'cmp.to.unknown': ['按任务此刻的状态投递', 'Goes where the task stands when it arrives'],
  'cmp.to.none': ['现在发不了：%s', 'Cannot be sent now: %s'],
  'cmp.noSteer': ['这个 agent 不能中途插话，这条消息会在这一轮结束后接着发', 'This agent cannot be steered mid-turn: the message goes once this turn ends'],
  'cmp.queued': ['排队中', 'Queued'], 'cmp.sent': ['已送出', 'Sent'], 'cmp.seen': ['agent 已接收', 'Received by the agent'],
  'cmp.failed': ['没送到', 'Not delivered'], 'cmp.resend': ['重发', 'Send again'],
});

// modesOf are the ways a message can go on a route: steer, after and interrupt while a run runs (steer and interrupt
// only when the coordinator offers them for that run), none otherwise.
export function modesOf(route, acts = []) {
  if (route?.to !== 'run') return [];
  return [...(acts.includes('steer') ? ['steer'] : []), 'after', ...(acts.includes('interrupt') ? ['interrupt'] : [])];
}

// whereTo is the line under the box: where the message goes for route and mode.
export function whereTo(w, route, mode, machine) {
  if (!route) return w.t('cmp.to.unknown');
  if (route.to === 'run') return mode === 'steer' ? w.t('cmp.to.steer') : w.f('cmp.to.' + mode, machine || '');
  if (route.to === 'reply') return w.f('cmp.to.reply', machine || '');
  if (route.to === 'workpad') return w.f('cmp.to.workpad', route.stage || '');
  return w.f('cmp.to.none', route.why || route.to || '');
}

const sendWords = {queued: 'cmp.queued', sent: 'cmp.sent', seen: 'cmp.seen', failed: 'cmp.failed'};

// Composer: route is the task's (null when the coordinator gives none); acts what the run offers; machine where the
// run is; sends the run's last messages; onSend({text, mode}) resolves once sent, onResend(send) sends one again.
// draft and onDraft keep what is written across draws.
export function Composer({route, acts = [], machine = '', sends = [], busy = false, disabled = false, onSend, onResend, draft = '', onDraft}) {
  const w = useWords();
  const {t} = w;
  const phone = usePhone();
  const [own, setOwn] = useState(draft);
  const text = onDraft ? draft : own;
  const setText = v => (onDraft ? onDraft(v) : setOwn(v));
  const modes = modesOf(route, acts);
  const [picked, setMode] = useState('');
  const mode = modes.includes(picked) ? picked : modes[0] || '';
  const blocked = route && !['run', 'reply', 'workpad'].includes(route.to);
  const canSend = !!text.trim() && !busy && !disabled && !blocked;
  const send = () => {
    if (!canSend) return;
    Promise.resolve(onSend({text: text.trim(), mode})).then(ok => { if (ok !== false) setText(''); }, () => {});
  };
  const onKeyDown = e => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && !e.isComposing) { e.preventDefault(); send(); }
  };
  return html`<div class=${cx('composer', phone && 'composer-phone')}>
    ${sends.length > 0 && html`<ul class="cmp-sends" aria-label=${t('cmp.label')}>${sends.map(m => html`<li key=${m.id} class=${cx('cmp-send', m.state === 'failed' && 't-failed')}>
      <span class="ell">${m.text}</span><span class="cmp-state">${t(sendWords[m.state] || 'cmp.sent')}</span>
      ${m.state === 'failed' && onResend && html`<${Button} kind="quiet" disabled=${busy || disabled} onClick=${() => onResend(m)}>${t('cmp.resend')}<//>`}
    </li>`)}</ul>`}
    ${modes.length > 1 && html`<div class="cmp-modes"><${Segmented} label=${t('cmp.mode')} value=${mode} onChange=${setMode}
      options=${modes.map(m => ({value: m, label: t('cmp.' + m)}))} /></div>`}
    <div class="cmp-row">
      <textarea class="in cmp-in" rows=${phone ? 1 : 2} value=${text} placeholder=${t('cmp.hint')} aria-label=${t('cmp.label')} disabled=${disabled}
        onInput=${e => setText(e.currentTarget.value)} onKeyDown=${onKeyDown}></textarea>
      <${Button} kind=${mode === 'interrupt' ? 'danger' : 'primary'} keyName="Mod+Enter" disabled=${!canSend} onClick=${send}>${t('cmp.send')}<//>
    </div>
    <div class="cmp-where t-muted">${whereTo(w, route, mode, machine)}${route?.to === 'run' && !modes.includes('steer') ? ' · ' + t('cmp.noSteer') : ''}</div>
  </div>`;
}
