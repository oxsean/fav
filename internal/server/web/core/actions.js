// actions is the page's one table of actions: every key, the command palette, the shortcuts page and the key bar come
// from it, so an action has one key everywhere and is found by its Chinese or English name. The table says what an
// action is and where its key acts; whoever can do it (the app, a page, a list) binds it with bindingsFor while mounted.
import {register} from './i18n.js';

// ⚠️ Letters act at once and can be taken back; Shift+letter is a confirmed or whole-page change; "g x" moves to a
// page; starting or stopping an agent only opens its confirmation. level is the key scope the action is bound in;
// bar puts it in the key bar; aliases are further keys that do the same and are not shown.
export const actions = [
  {id: 'home', keys: ['g h'], level: 'global', group: 'go'},
  {id: 'tasks', keys: ['g t'], level: 'global', group: 'go'},
  {id: 'board', keys: ['g b'], level: 'global', group: 'go'},
  {id: 'runs', keys: ['g r'], level: 'global', group: 'go'},
  {id: 'machines', keys: ['g m'], level: 'global', group: 'go'},
  {id: 'agents', keys: ['g a'], level: 'global', group: 'go'},
  {id: 'team', keys: ['g p'], level: 'global', group: 'go'},
  {id: 'me', keys: ['g s'], level: 'global', group: 'go'},
  {id: 'palette', keys: ['Mod+K'], level: 'global', group: 'general', palette: false},
  {id: 'help', keys: ['?'], level: 'global', group: 'general'},
  {id: 'search', keys: ['/'], level: 'global', group: 'general'},
  {id: 'new', keys: ['n'], level: 'global', group: 'general', bar: true},
  {id: 'nav', keys: ['['], level: 'global', group: 'general', bar: true},
  {id: 'undo', keys: ['Mod+Z'], level: 'global', group: 'general', bar: true},
  {id: 'theme', keys: ['Shift+T'], level: 'global', group: 'general'},
  {id: 'lang', keys: ['Shift+L'], level: 'global', group: 'general'},
  {id: 'density', keys: ['Shift+M'], level: 'global', group: 'general'},
  {id: 'view', keys: ['v'], level: 'page', group: 'work'},
  {id: 'dispatch', keys: ['d'], level: 'page', group: 'work', bar: true},
  {id: 'edit', keys: ['e'], level: 'page', group: 'work'},
  {id: 'stop', keys: ['x'], level: 'page', group: 'work', bar: true},
  {id: 'done', keys: ['Shift+D'], level: 'page', group: 'work', bar: true},
  {id: 'next', keys: ['j'], aliases: ['ArrowDown'], level: 'list', group: 'list', bar: true, hint: 'act.move', palette: false},
  {id: 'prev', keys: ['k'], aliases: ['ArrowUp'], level: 'list', group: 'list', bar: true, hint: 'act.move', palette: false},
  {id: 'pick', keys: ['1', '2', '3', '4', '5', '6', '7', '8', '9'], level: 'list', group: 'list', bar: true, palette: false},
  {id: 'toggle', keys: ['Space'], level: 'list', group: 'list', bar: true, palette: false},
  {id: 'open', keys: ['Enter'], level: 'list', group: 'list', bar: true, palette: false},
  {id: 'end', keys: ['End'], level: 'list', group: 'output', palette: false},
  {id: 'start', keys: ['Home'], level: 'list', group: 'output', palette: false},
  {id: 'unfold', keys: ['Shift+O'], level: 'list', group: 'output'},
  {id: 'find', keys: ['Mod+F'], level: 'list', group: 'output'},
];

export const groups = ['go', 'general', 'work', 'list', 'output'];

export const byID = Object.fromEntries(actions.map(a => [a.id, a]));

register('actions', {
  'act.home': ['去首页', 'Go home'], 'act.tasks': ['去任务列表', 'Go to the task list'], 'act.board': ['去看板', 'Go to the board'],
  'act.runs': ['去运行', 'Go to the runs'], 'act.machines': ['去机器', 'Go to machines'], 'act.agents': ['去 Agent', 'Go to agents'],
  'act.team': ['去团队', 'Go to the team'], 'act.me': ['去「我」', 'Go to your page'],
  'act.palette': ['全部命令', 'All commands'], 'act.help': ['快捷键', 'Keyboard shortcuts'], 'act.search': ['搜索任务', 'Search tasks'],
  'act.new': ['新建', 'New task'], 'act.nav': ['收起或展开菜单', 'Collapse or expand the menu'], 'act.undo': ['撤销', 'Undo'],
  'act.theme': ['切换主题', 'Switch theme'], 'act.lang': ['切换语言', 'Switch language'], 'act.density': ['切换密度', 'Switch density'],
  'act.view': ['切换视图', 'Switch view'], 'act.dispatch': ['派发', 'Dispatch'], 'act.edit': ['编辑', 'Edit'], 'act.stop': ['停止', 'Stop'],
  'act.done': ['完成', 'Mark done'], 'act.next': ['下一条', 'Next'], 'act.prev': ['上一条', 'Previous'], 'act.move': ['上下一条', 'Next / previous'],
  'act.pick': ['选择', 'Pick'], 'act.toggle': ['展开', 'Expand'], 'act.open': ['打开', 'Open'],
  'act.end': ['跳到最新', 'Jump to the latest'], 'act.start': ['跳到开头', 'Jump to the start'], 'act.unfold': ['全部展开', 'Unfold all'],
  'act.find': ['在输出里查找', 'Find in the output'],
  'group.go': ['去往', 'Go to'], 'group.general': ['常用', 'General'], 'group.work': ['任务', 'Tasks'], 'group.list': ['列表', 'Lists'],
  'group.output': ['输出', 'Output'],
});

// bindingsFor turns what a component can do, {id: {run(key, e), when?, label?}}, into key bindings for keys.push. label
// replaces the action's word where the page names it more closely (on the home, pick is 作答, dispatch is 重试).
export function bindingsFor(impls) {
  return Object.entries(impls).flatMap(([id, impl]) => {
    const a = byID[id];
    if (!a) throw new Error(`actions: no action ${id}`);
    if (!impl) return [];
    const b = key => ({key, id, label: impl.label || a.hint || 'act.' + id, run: e => impl.run(key, e), when: impl.when});
    return [...a.keys.map(k => ({...b(k), bar: !!a.bar, palette: a.palette !== false})), ...(a.aliases || []).map(k => ({...b(k), alias: true}))];
  });
}

// runnable is the palette's list: each bound action once, from the bindings keys.active() gives, with its first key.
export function runnable(bindings) {
  const seen = new Set();
  return bindings.filter(b => b.id && b.palette && !seen.has(b.id) && seen.add(b.id))
    .map(b => ({id: b.id, key: byID[b.id].keys[0], label: 'act.' + b.id, run: b.run}));
}

// rank orders items [{words: [...], …}] for query q: a word that starts with it first, then one that holds it; no
// query keeps them all in order.
export function rank(items, q) {
  const query = q.trim().toLowerCase();
  if (!query) return items;
  const score = x => {
    const hay = x.words.map(s => String(s).toLowerCase());
    return hay.some(s => s.startsWith(query)) ? 3 : hay.some(s => s.includes(query)) ? 2 : 0;
  };
  return items.map((x, i) => [x, score(x), i]).filter(([, s]) => s > 0).sort((a, b) => b[1] - a[1] || a[2] - b[2]).map(([x]) => x);
}
