// App is the signed-in page: the shell around the page the route names, the global actions (going to a page,
// the palette, the shortcuts, theme, language, density) and the user's menu.
import {useState, useMemo} from '../vendor/hooks.mjs';
import {signal} from '../vendor/signals-core.mjs';
import {html, useWords, useSignalValue, useActions, NamesContext} from '../ui/base.js';
import {Shell} from '../ui/shell.js';
import {Palette, Help} from '../ui/palette.js';
import {Panel} from '../ui/panel.js';
import {runnable} from '../core/actions.js';
import * as sel from '../core/select.js';
import {Home} from './home.js';
import {Tasks} from './tasks.js';
import {Runs} from './runs.js';
import {Machines} from './machines.js';
import {createChanges} from '../core/changes.js';
import './words.js';

const goes = {home: {page: 'home'}, tasks: {page: 'tasks', view: 'list'}, board: {page: 'tasks', view: 'board'}, runs: {page: 'runs'},
  machines: {page: 'machines'}, agents: {page: 'agents'}, team: {page: 'team'}, me: {page: 'me'}};

// Soon stands for a page the new interface does not have yet.
function Soon({page}) {
  const {t} = useWords();
  return html`<${Panel} title=${t('nav.' + page)}><p class="empty">${t('app.soon')}</p><p class="empty t-muted">${t('app.soonNote')}</p><//>`;
}

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
// wire when not given); names is a signal of user id → name.
export function App({store, commands, toasts, wire, http, router, keys, nav, prefs, session, clock, fetchOutput, storage, onLogout, copy, changes: given, names = null}) {
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
  const go = to => router.go(to);
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
  const page = route.page === 'home'
    ? html`<${Home} store=${store} commands=${commands} toasts=${toasts} clock=${clock} fetchOutput=${fetchOutput} onOpen=${onOpen} onNavigate=${onNavigate} />`
    : route.page === 'tasks'
      ? html`<${Tasks} store=${store} commands=${commands} toasts=${toasts} wire=${wire} router=${router} session=${session} clock=${clock} storage=${storage}
        intent=${intent} prefs=${prefs} copy=${copy} changes=${changes} />`
      : route.page === 'runs'
        ? html`<${Runs} store=${store} commands=${commands} toasts=${toasts} router=${router} prefs=${prefs} copy=${copy} changes=${changes} storage=${storage} clock=${clock} />`
        : route.page === 'machines'
          ? html`<${Machines} store=${store} commands=${commands} toasts=${toasts} session=${session} http=${http} router=${router} storage=${storage}
            clock=${clock} copy=${copy} />`
          : html`<${Soon} page=${route.page} />`;

  return html`<${NamesContext.Provider} value=${names}><${Shell} keys=${keys} wire=${wire} nav=${nav} toasts=${toasts} page=${route.page} onNavigate=${onNavigate}
    counts=${counts} spent=${{tokens: day.tokens, usd: day.usd}} user=${session} userMenu=${userMenu}
    navCounts=${{tasks: Object.values(st.tasks).filter(x => x.status === 'todo').length, runs: sel.openRuns(st).length, machines: machines.length}}
    onSearch=${() => setModal({kind: 'palette', entries: runnable(keys.active())})} onNew=${() => toTasks('new')}
    onReload=${() => globalThis.location?.reload()}>
    ${page}
    ${modal?.kind === 'palette' && html`<${Palette} entries=${modal.entries} find=${finder(store, machines, go)} onClose=${() => setModal(null)} />`}
    ${modal?.kind === 'help' && html`<${Help} onClose=${() => setModal(null)} />`}
  <//><//>`;
}
