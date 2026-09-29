// router maps the address to a route and back. A route is {page, task?, run?, view?, event?, auth?}: run is the run
// whose conversation the task page shows, or the run the runs page has open; event and auth are one-time: event (from #task-<id>/r-<run>/e-<event>) is
// the step to scroll to, auth a fragment (#device-, #invite-, #signin-) the page acts on; neither is written back.
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
  const frag = hash.replace(/^#/, '');
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
  const s = q.toString();
  return s ? `?${s}` : '/';
}

// createRouter keeps the route in a signal; location and history are the page's (the tests pass their own).
export function createRouter({location, history}) {
  const route = signal(parse(location.search, location.hash));
  return {
    route,
    go(to, {replace = false} = {}) {
      const next = {...to};
      delete next.auth;
      delete next.event;
      const url = format(next);
      (replace ? history.replaceState : history.pushState).call(history, null, '', url);
      route.value = parse(url === '/' ? '' : url, '');
    },
    // popped: the browser moved through its history.
    popped() { route.value = parse(location.search, location.hash); },
  };
}
