// router maps the address to a route and back. A route is {page, task?, run?, view?, sessions?, session?, project?,
// fav?, wait?, event?, auth?}: run is the run whose conversation the task page shows, or the run the runs page has open;
// sessions the machine whose own sessions the machines page lists, session (provider:id) the one of them open, project
// the project it lists only (none: those in no project), fav that it lists only the favorites; wait the task whose
// waiting item the home shows on its own (a notice's #wait-<task>); event and auth are one-time: event (from
// #task-<id>/r-<run>/e-<event>) is the step to scroll to, auth a fragment (#device-, #invite-, #signin-) the page acts
// on; neither is written back.
import {signal} from '../vendor/signals-core.mjs';

export const pages = ['home', 'tasks', 'runs', 'machines', 'agents', 'team', 'me'];
export const views = ['list', 'board', 'tree'];

// ⚠️ Old addresses still in links and bookmarks; only these are mapped.
export const legacy = {inbox: 'home', settings: 'me', projects: 'team'};

const authKinds = ['device', 'invite', 'signin'];

export function parse(search = '', hash = '') {
  const q = new URLSearchParams(search);
  let page = q.get('page') || 'home';
  page = legacy[page] || page;
  if (!pages.includes(page)) page = 'home';
  const route = {page};
  if (page === 'tasks') {
    const view = q.get('view');
    route.view = views.includes(view) ? view : 'list';
    if (q.get('task')) route.task = q.get('task');
    if (q.get('task') && q.get('run')) route.run = q.get('run');
  }
  if (page === 'runs' && q.get('run')) route.run = q.get('run');
  if (page === 'machines' && q.get('sessions')) {
    route.sessions = q.get('sessions');
    if (q.get('session')) route.session = q.get('session');
    if (q.get('project')) route.project = q.get('project');
    if (q.get('fav') === '1') route.fav = true;
  }
  if (page === 'home' && q.get('wait')) route.wait = q.get('wait');
  const frag = hash.replace(/^#/, '');
  const wait = frag.match(/^wait-(.+)$/);
  if (wait) return {page: 'home', wait: decodeURIComponent(wait[1])};
  const task = frag.match(/^task-([^/]+)(?:\/r-([^/]+))?(?:\/e-(.+))?$/);
  if (task) {
    route.page = 'tasks';
    route.view ||= 'list';
    route.task = decodeURIComponent(task[1]);
    delete route.run;
    if (task[2]) route.run = decodeURIComponent(task[2]);
    if (task[2] && task[3]) route.event = decodeURIComponent(task[3]);
    return route;
  }
  const auth = frag.match(/^([a-z]+)-(.+)$/);
  if (auth && authKinds.includes(auth[1])) {
    const [value, rest] = auth[2].split(/\?(.*)/s);
    route.auth = {kind: auth[1], value: decodeURIComponent(value)};
    if (auth[1] === 'signin' && rest) route.auth.params = Object.fromEntries(new URLSearchParams(rest));
  }
  return route;
}

// link is the deep link to a step of a run: #task-<id>/r-<run>/e-<event>.
export const link = (task, run, event) => `#task-${encodeURIComponent(task)}/r-${encodeURIComponent(run)}/e-${encodeURIComponent(event)}`;

// format is the address of a route, without its one-time parts.
export function format(route) {
  const q = new URLSearchParams();
  if (route.page && route.page !== 'home') q.set('page', route.page);
  if (route.page === 'tasks') {
    if (route.task) q.set('task', route.task);
    if (route.task && route.run) q.set('run', route.run);
    if (route.view && route.view !== 'list') q.set('view', route.view);
  }
  if (route.page === 'runs' && route.run) q.set('run', route.run);
  if (route.page === 'machines' && route.sessions) {
    q.set('sessions', route.sessions);
    if (route.session) q.set('session', route.session);
    if (route.project) q.set('project', route.project);
    if (route.fav) q.set('fav', '1');
  }
  if (route.page === 'home' && route.wait) q.set('wait', route.wait);
  const s = q.toString();
  return s ? `?${s}` : '/';
}

const isWait = link => /^#wait-./.test(link);

// createRouter keeps the route in a signal; location and history are the page's (the tests pass their own). Every
// address it pushes is marked as having one of the page's before it. A page opened on a notice's #wait- stands on the
// list of what waits with the item pushed on top, so going back from it goes to the list.
export function createRouter({location, history}) {
  const route = signal(parse(location.search, location.hash));
  const write = (url, replace) => {
    if (replace) history.replaceState(history.state, '', url);
    else history.pushState({back: true}, '', url);
    route.value = parse(url === '/' ? '' : url.replace(/#.*/, ''), url.replace(/^[^#]*/, ''));
  };
  if (isWait(location.hash)) {
    const wait = route.value.wait;
    write('/', true);
    write(format({page: 'home', wait}), false);
  }
  const router = {
    route,
    go(to, {replace = false} = {}) {
      const next = {...to};
      delete next.auth;
      delete next.event;
      write(format(next), replace);
    },
    // open goes to a notice's link: one about what waits puts the list of what waits under it.
    open(link) {
      if (!isWait(link)) return write('/' + link, false);
      const r = route.value;
      if (r.page !== 'home' || r.wait) write('/', false);
      write(format(parse('', link)), false);
    },
    // back goes where the page came from, or, when it came from nowhere of the page's, to in its place.
    back(to) {
      if (history.state?.back) history.back();
      else router.go(to, {replace: true});
    },
    // popped: the browser moved through its history.
    popped() { route.value = parse(location.search, location.hash); },
  };
  return router;
}
