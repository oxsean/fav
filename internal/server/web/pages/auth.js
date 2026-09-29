// auth is what a browser sees before it is signed in, or for a sign-in it is asked about: the sign-in page (with an
// invitation or the result of a sign-in the server sent back) and a terminal's sign-in to allow.
import {useState, useEffect} from '../vendor/hooks.mjs';
import {html, useWords} from '../ui/base.js';
import {Button} from '../ui/controls.js';
import {TextInput} from '../ui/input.js';
import {startURL} from '../core/http.js';
import {code as codes} from '../core/proto.js';

const when = (w, at) => new Date(at).toLocaleString(w.lang.value === 'zh' ? 'zh-CN' : 'en-GB', {dateStyle: 'medium', timeStyle: 'short'});
import './words.js';

// AuthFrame is the plain page around them: the brand and a language switch.
export function AuthFrame({onLang, children}) {
  const {t} = useWords();
  return html`<div class="auth">
    <header class="auth-top"><span class="brand mono"><span class="mark" aria-hidden="true">>_</span>tend</span>
      ${onLang && html`<${Button} kind="quiet" onClick=${onLang}>${t('app.lang')}<//>`}</header>
    <main class="auth-main" id="main"><section class="auth-card">${children}</section></main>
  </div>`;
}

const deniedCodes = ['not_admitted', 'disabled'];

// Denied is a sign-in the server refused: who the account is, why it cannot come in, what to do.
function Denied({auth, onSwitch, copy}) {
  const {t, f} = useWords();
  const [copied, setCopied] = useState(false);
  const p = auth.params || {};
  const who = p.username || p.email || '';
  const text = auth.value === 'disabled' ? t('denied.disabled') : f('denied.notAdmitted', p.provider || '', who);
  return html`<div class="auth-denied" role="alert">
    <h1>${t('denied.title')}</h1>
    <p>${text}</p>
    <p class="t-muted">${t('denied.help')}</p>
    <div class="auth-actions">
      <${Button} kind="primary" onClick=${onSwitch}>${t('denied.switch')}<//>
      ${who && html`<${Button} onClick=${async () => { await copy([p.provider, who, p.email].filter(Boolean).join(' · ')); setCopied(true); }}>${t(copied ? 'denied.copied' : 'denied.copy')}<//>`}
    </div>
  </div>`;
}

// Login: auth is the route's one-time fragment (an invitation, or a sign-in's result); onSignedIn(session) goes on.
export function Login({http, auth, onSignedIn, onSwitch, copy = text => globalThis.navigator?.clipboard?.writeText(text)}) {
  const w = useWords();
  const {t, f, has} = w;
  const [logins, setLogins] = useState([]);
  const [invite, setInvite] = useState(null);
  const [token, setToken] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const code = auth?.kind === 'invite' ? auth.value : '';
  useEffect(() => {
    http.logins().then(setLogins, () => setLogins([]));
    if (code) http.invite(code).then(setInvite, () => setInvite(false));
  }, [code]);
  if (auth?.kind === 'signin' && deniedCodes.includes(auth.value)) return html`<${Denied} auth=${auth} onSwitch=${onSwitch} copy=${copy} />`;
  const problem = auth?.kind === 'signin' && auth.value !== 'ok' ? t(has('signin.' + auth.value) ? 'signin.' + auth.value : 'signin.internal') : '';
  const submit = async e => {
    e.preventDefault();
    if (!token.trim() || busy) return;
    setBusy(true);
    setError('');
    try {
      await http.login(token.trim());
      onSignedIn(await http.session());
    } catch (err) {
      setError(err.status === 401 ? t('auth.badToken') : f('auth.failed', err.code || String(err)));
    } finally {
      setBusy(false);
    }
  };
  return html`<div class="auth-body">
    <h1>${t('auth.title')}</h1>
    <p class="t-muted">${t('auth.tagline')}</p>
    ${problem && html`<div class="auth-problem" role="alert">${problem}</div>`}
    ${code && (invite ? html`<div class="auth-invite">
        <b>${f('auth.invitedBy', invite.inviter, t('role.' + invite.role))}</b>
        ${invite.project && html`<span>${f('auth.invitedTo', invite.project, t('role.' + invite.access))}</span>`}
        ${invite.expires && html`<span class="t-muted">${f('auth.inviteExpires', when(w, invite.expires))}</span>`}
      </div>` : invite === false && html`<div class="auth-problem" role="alert">${t('auth.inviteGone')}</div>`)}
    ${!code && html`<p class="auth-note t-muted">${t('auth.inviteOnly')}</p>`}
    ${logins.length > 0 && html`<div class="auth-providers">
      ${logins.map(l => html`<a class="btn primary wide" href=${startURL(l.name, code)}>${f(code ? 'auth.acceptWith' : 'auth.with', l.display || l.name)}</a>`)}
    </div><div class="auth-or"><span>${t('auth.orToken')}</span></div>`}
    <form class="auth-form" onSubmit=${submit}>
      <${TextInput} label=${t('auth.token')} type="password" name="token" value=${token} onInput=${setToken} mono
        placeholder=${t('auth.tokenPlaceholder')} note=${t('auth.tokenNote')} error=${error} autoFocus=${!logins.length} />
      <${Button} kind="primary" type="submit" wide disabled=${busy || !token.trim()}>${t('auth.signIn')}<//>
    </form>
  </div>`;
}

const gone404 = e => e.status === 404 || e.code === codes.notFound;

// Device is a terminal's sign-in (#device-<code>): check the code it shows, then allow or deny it.
export function Device({http, code, onBack}) {
  const w = useWords();
  const {t, f} = w;
  const [info, setInfo] = useState(null);
  const [gone, setGone] = useState(false);
  const [result, setResult] = useState('');
  const [error, setError] = useState('');
  useEffect(() => {
    http.device(code).then(setInfo, e => (gone404(e) ? setGone(true) : setError(f('device.failed', e.code))));
  }, [code]);
  const decide = async allow => {
    setError('');
    try {
      await http.decideDevice(code, allow);
      setResult(allow ? 'allowed' : 'denied');
    } catch (e) {
      if (gone404(e)) setGone(true); else setError(f('device.failed', e.code));
    }
  };
  const back = html`<${Button} onClick=${onBack}>${t('device.back')}<//>`;
  if (result) return html`<div class="auth-body"><h1>${t('device.' + result)}</h1><div class="auth-actions">${back}</div></div>`;
  if (gone) return html`<div class="auth-body"><h1>${t('device.title')}</h1><div class="auth-problem" role="alert">${t('device.gone')}</div><div class="auth-actions">${back}</div></div>`;
  return html`<div class="auth-body">
    <h1>${t('device.title')}</h1>
    <p class="t-muted">${t('device.help')}</p>
    ${error && html`<div class="auth-problem" role="alert">${error}</div>`}
    ${info ? html`<code class="device-code mono">${info.code}</code>
      <dl class="facts">
        <dt>${t('device.name')}</dt><dd>${info.name || '—'}</dd>
        <dt>${t('device.ip')}</dt><dd class="mono">${info.ip}</dd>
        <dt>${t('device.at')}</dt><dd>${info.created ? when(w, info.created) : '—'}</dd>
      </dl>
      <div class="auth-actions">
        <${Button} kind="primary" onClick=${() => decide(true)}>${t('device.allow')}<//>
        <${Button} onClick=${() => decide(false)}>${t('device.deny')}<//>
      </div>` : !error && html`<p class="t-muted">${t('device.loading')}</p>`}
  </div>`;
}
