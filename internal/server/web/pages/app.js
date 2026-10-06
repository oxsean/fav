// App is the signed-in page: the shell around the page the route names, the global actions (going to a page,
// the palette, the shortcuts, theme, language, density) and the user's menu.
import {useState, useMemo, useEffect} from '../vendor/hooks.mjs';
import {signal} from '../vendor/signals-core.mjs';
import {html, useWords, useSignalValue, useActions, usePhone, NamesContext} from '../ui/base.js';
import {Shell} from '../ui/shell.js';
import {Palette, Help} from '../ui/palette.js';
import {runnable} from '../core/actions.js';
import * as sel from '../core/select.js';
import {Home} from './home.js';
import {Tasks} from './tasks.js';
import {Runs} from './runs.js';
import {Machines} from './machines.js';
import {Sessions} from './sessions.js';
import {mayRead} from '../core/team.js';
import {Team} from './team.js';
import {Me} from './me.js';
import {Agents} from './agents.js';
import {useNotices, useTreesDone} from './notify.js';
import {createChanges} from '../core/changes.js';
import {createDrafts} from '../core/drafts.js';
import {createAgentDefs} from '../core/agents.js';
import {nowhere} from '../core/platform.js';
import {noPush} from '../core/push.js';
import './words.js';

const goes = {home: {page: 'home'}, tasks: {page: 'tasks', view: 'list'}, board: {page: 'tasks', view: 'board'}, runs: {page: 'runs'},
  machines: {page: 'machines'}, agents: {page: 'agents'}, team: {page: 'team'}, me: {page: 'me'}};

// find is the palette's own hits for q: tasks by title or id, runs by id, machines by name.
function finder(store, machines, go) {
  return q => {
    const s = q.toLowerCase();
    const st = store.state;
    const tasks = Object.values(st.tasks).filter(x => `${x.title} ${x.id}`.toLowerCase().includes(s))
      .map(x => ({kind: 'task', id: x.id, title: x.title, sub: x.id, run: () => go({page: 'tasks', task: x.id})}));
    const runs = Object.values(st.runs).filter(r => r.id.toLowerCase().includes(s))
      .map(r => ({kind: 'run', id: r.id, title: st.tasks[r.task]?.title || r.task, sub: r.id, run: () => go({page: 'runs', run: r.id})}));
    const ms = machines.filter(m => m.name.toLowerCase().includes(s))
      .map(m => ({kind: 'machine', id: m.name, title: m.name, sub: m.state, run: () => go({page: 'machines'})}));
    return [...tasks, ...runs, ...ms];
  };
}

// App: router, keys, nav and toasts are core's; prefs core/prefs.js's; session is who signed in; fetchOutput(run) the
// last events of a run; http is core/http.js's (the pages read /api there); storage is the browser's (the task page
// keeps its filter and draft there); onLogout signs out; changes reads what runs changed (core/changes.js; one over
// wire when not given); drafts (core/drafts.js) keep what is written to each task's agent and into its send-back
// notes; agentDefs reads the agents (core/agents.js; likewise); names is a signal of user id → name; notices are the
// browser's ({Notification, secure}), doc the document, tab the tab's storage (the me page's); platform is where the
// page runs and push its Web Push (core/push.js), whose notices of what no longer waits close as the inbox changes;
// copy and download are the clipboard's and a file save's (the tests pass their own).
export function App({store, commands, toasts, wire, http, router, keys, nav, prefs, session, clock, fetchOutput, storage, onLogout, copy, changes: given, names = null,
  notices = {}, doc = null, tab = null, agentDefs: givenDefs, download, platform = nowhere, push = noPush}) {
  const {t, f} = useWords();
  const route = useSignalValue(router.route);
  const machines = useSignalValue(store.machines);
  const inbox = useSignalValue(store.inbox);
  useSignalValue(store.rev.runs);
  useSignalValue(store.rev.tasks);
  const theme = useSignalValue(prefs.theme), density = useSignalValue(prefs.density);
  const [modal, setModal] = useState(null);
  // intent is what the task page is asked to do once it is up: open a new task or its search.
  const intent = useMemo(() => signal(null), []);
  const changes = useMemo(() => given || createChanges({wire}), [wire, given]);
  const drafts = useMemo(() => createDrafts(), []);
  const agentDefs = useMemo(() => givenDefs || createAgentDefs({wire}), [wire, givenDefs]);
  const go = (to, o) => router.go(to, o);
  const phone = usePhone();
  useNotices({store, prefs, notices, doc, active: !phone, onOpen: task => go({page: 'tasks', task})});
  useTreesDone({store, toasts});
  useEffect(() => push.clear(inbox), [inbox]);
  const toTasks = kind => { if (route.page !== 'tasks') go({page: 'tasks', view: 'list'}); intent.value = {kind}; };

  useActions('global', {
    ...Object.fromEntries(Object.entries(goes).map(([id, to]) => [id, {run: () => go(to)}])),
    palette: {run: () => setModal({kind: 'palette', entries: runnable(keys.active())})},
    help: {run: () => setModal({kind: 'help'})},
    search: {run: () => toTasks('search')},
    new: {run: () => toTasks('new')},
    theme: {run: prefs.cycleTheme},
    lang: {run: prefs.toggleLang},
    density: {run: prefs.cycleDensity},
  }, {active: !modal});

  const st = store.state, now = clock();
  const counts = sel.counts(st, machines, inbox);
  const day = sel.today(st, now);
  const onNavigate = page => go(goes[page] || {page});
  const onOpen = task => go({page: 'tasks', task});
  const userMenu = [
    {label: t('app.lang'), onClick: prefs.toggleLang},
    {label: f('app.theme', t('theme.' + theme)), onClick: prefs.cycleTheme},
    {label: f('app.density', t('density.' + density)), onClick: prefs.cycleDensity},
    {label: t('app.logout'), kind: 'danger', onClick: onLogout},
  ];
  // ⚠️ the phone's list and a waiting item's page are separate homes: one home switching between them in place could
  // leave the page's old section behind when a notice opened it early on the me page.
  const page = route.page === 'home'
    ? html`<${Home} key=${phone && route.wait ? 'wait' : 'list'} store=${store} commands=${commands} toasts=${toasts} clock=${clock} fetchOutput=${fetchOutput} changes=${changes} onOpen=${onOpen} onNavigate=${onNavigate}
      wire=${wire} session=${session} onSessions=${machine => go({page: 'machines', sessions: machine})}
      wait=${route.wait || ''} onWait=${(task, o) => go(task ? {page: 'home', wait: task} : {page: 'home'}, o)} onBack=${() => router.back({page: 'home'})} storage=${storage} />`
    : route.page === 'tasks'
      ? html`<${Tasks} store=${store} commands=${commands} toasts=${toasts} wire=${wire} router=${router} session=${session} clock=${clock} storage=${storage}
        intent=${intent} prefs=${prefs} copy=${copy} changes=${changes} drafts=${drafts} />`
      : route.page === 'runs'
        ? html`<${Runs} store=${store} commands=${commands} toasts=${toasts} router=${router} prefs=${prefs} copy=${copy} changes=${changes} drafts=${drafts} storage=${storage} clock=${clock} />`
        : route.page === 'team'
          ? html`<${Team} store=${store} commands=${commands} toasts=${toasts} platform=${platform} session=${session} http=${http} wire=${wire} clock=${clock} copy=${copy} />`
          : route.page === 'machines' && route.sessions
            ? html`<${Sessions} key=${route.sessions} store=${store} wire=${wire} router=${router} toasts=${toasts} machine=${route.sessions} open=${route.session || ''}
              may=${mayRead(session, machines.find(m => m.name === route.sessions) || {})} copy=${copy} clock=${clock} />`
          : route.page === 'machines'
            ? html`<${Machines} store=${store} commands=${commands} toasts=${toasts} platform=${platform} session=${session} http=${http} wire=${wire} router=${router} storage=${storage}
              clock=${clock} copy=${copy} />`
            : route.page === 'me'
              ? html`<${Me} session=${session} http=${http} prefs=${prefs} toasts=${toasts} router=${router} notices=${notices} tab=${tab} platform=${platform} push=${push} clock=${clock}
                copy=${copy} onLogout=${onLogout} />`
              : route.page === 'agents'
                ? html`<${Agents} store=${store} commands=${commands} toasts=${toasts} platform=${platform} session=${session} wire=${wire} agentDefs=${agentDefs} download=${download} />`
                : null;

  return html`<${NamesContext.Provider} value=${names}><${Shell} keys=${keys} wire=${wire} nav=${nav} toasts=${toasts} page=${route.page} onNavigate=${onNavigate}
    counts=${counts} spent=${{tokens: day.tokens, usd: day.usd}} user=${session} userMenu=${userMenu}
    navCounts=${{tasks: Object.values(st.tasks).filter(x => x.status === 'todo').length, runs: sel.openRuns(st).length, machines: machines.length}}
    onSearch=${() => setModal({kind: 'palette', entries: runnable(keys.active())})} onNew=${() => toTasks('new')}
    onReload=${() => platform.refresh()}>
    ${page}
    ${modal?.kind === 'palette' && html`<${Palette} entries=${modal.entries} find=${finder(store, machines, go)} onClose=${() => setModal(null)} />`}
    ${modal?.kind === 'help' && html`<${Help} onClose=${() => setModal(null)} />`}
  <//><//>`;
}
