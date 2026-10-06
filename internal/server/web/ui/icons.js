// icons are the page's stroke icons, drawn in the text colour.
import {html} from './base.js';

const paths = {
  home: 'm3 11 9-8 9 8M5 9v12h14V9',
  tasks: 'M6 3h12a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2zM8 8h8M8 12h8M8 16h5',
  runs: 'm8 5 11 7-11 7z',
  machines: 'M5 4h14a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2zM8 21h8m-4-5v5',
  agents: 'M4 4h16v12H9l-5 4z',
  team: 'M12 3 4 6v6c0 5 3.5 8 8 9 4.5-1 8-4 8-9V6z',
  me: 'M15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0zM12 2v3m0 14v3M2 12h3m14 0h3',
  person: 'M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM4 21a8 8 0 0 1 16 0',
  inbox: 'M4 13h4l2 3h4l2-3h4M5 5h14l1 8v6H4v-6z',
  search: 'M10 16a6 6 0 1 0 0-12 6 6 0 0 0 0 12zm5-1 5 5',
  plus: 'M12 5v14M5 12h14',
  collapse: 'M4 4h16v16H4zM9 4v16M15 9l-3 3 3 3',
  expand: 'M4 4h16v16H4zM9 4v16M13 9l3 3-3 3',
  stop: 'M6 6h12v12H6z',
  close: 'M6 6l12 12M18 6 6 18',
  back: 'm15 5-7 7 7 7',
  down: 'm6 9 6 6 6-6',
  up: 'm6 15 6-6 6 6',
  lock: 'M6 11h12v10H6zM8 11V7a4 4 0 0 1 8 0v4',
  people: 'M9 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM2 20a7 7 0 0 1 14 0M16 4.5a3.5 3.5 0 0 1 0 6.5M18 13.5a7 7 0 0 1 4 6.5',
  eye: 'M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12zm10 3a3 3 0 1 0 0-6 3 3 0 0 0 0 6z',
  star: 'm12 3.5 2.6 5.3 5.9.9-4.3 4.1 1 5.8L12 16.8l-5.2 2.8 1-5.8-4.3-4.1 5.9-.9z',
  sessions: 'M4 5h16v10H8l-4 4zM8 9h8M8 12h5',
  help: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM9.5 9a2.5 2.5 0 1 1 3.5 2.3c-.6.3-1 .9-1 1.7M12 17h.01',
  more: 'M5 12a1.5 1.5 0 1 0 3 0 1.5 1.5 0 1 0-3 0M10.5 12a1.5 1.5 0 1 0 3 0 1.5 1.5 0 1 0-3 0M16 12a1.5 1.5 0 1 0 3 0 1.5 1.5 0 1 0-3 0',
  edit: 'M4 20h4L19 9l-4-4L4 16zM14 6l4 4',
  archive: 'M3 5h18v4H3zM5 9v10h14V9M10 13h4',
  trash: 'M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13M10 11v6M14 11v6',
  check: 'm5 12 5 5 9-10',
  filter: 'M4 5h16l-6 7v6l-4 2v-8z',
};

export const iconNames = Object.keys(paths);

// Icon: fill fills the shape where a rule asks (a favorite's star).
export function Icon({name, size = 16, fill = false}) {
  return html`<svg class=${fill ? 'icon icon-fill' : 'icon'} viewBox="0 0 24 24" width=${size} height=${size} aria-hidden="true" focusable="false"><path d=${paths[name]}></path></svg>`;
}
