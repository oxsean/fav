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
};

export const iconNames = Object.keys(paths);

export function Icon({name, size = 16}) {
  return html`<svg class="icon" viewBox="0 0 24 24" width=${size} height=${size} aria-hidden="true" focusable="false"><path d=${paths[name]}></path></svg>`;
}
