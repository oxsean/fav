'use strict';

// palette.js holds the page's one table of actions. The ⌘K palette, the keys, the shortcuts page and the key hints on
// buttons all come from it, so an action has one key everywhere and is found by its Chinese or English name.
const paletteWords = {
  palette: ['命令面板', 'Command palette'], paletteHint: ['输入操作或任务名…', 'Type an action or a task…'], noMatch: ['没有匹配的', 'Nothing matches'],
  paletteTasks: ['任务', 'Tasks'], paletteActions: ['操作', 'Actions'], thenKey: ['然后', 'then'],
  'act.palette': ['打开命令面板', 'Open the command palette'], 'act.help': ['快捷键', 'Keyboard shortcuts'],
  'act.search': ['搜索任务', 'Search tasks'], 'act.new': ['新建任务', 'New task'], 'act.home': ['去首页', 'Go home'],
  'act.tasks': ['去任务列表', 'Go to the task list'], 'act.board': ['去看板', 'Go to the board'], 'act.inbox': ['去等你', 'Go to what needs you'], 'act.runs': ['去运行', 'Go to the runs'],
  'act.machines': ['去机器', 'Go to machines'], 'act.agents': ['去 Agent 定义', 'Go to agent definitions'], 'act.projects': ['去项目', 'Go to projects'],
  'act.settings': ['去设置', 'Go to settings'], 'act.view': ['切换列表 / 看板', 'Switch list / board'], 'act.dispatch': ['派发选中任务', 'Dispatch the task'],
  'act.edit': ['编辑选中任务', 'Edit the task'], 'act.stop': ['停止运行', 'Stop the run'], 'act.done': ['标记完成', 'Mark done'],
  'act.undo': ['撤销刚才的操作', 'Undo the last action'], 'act.theme': ['切换主题', 'Switch theme'], 'act.lang': ['切换语言', 'Switch language'], 'act.density': ['切换密度', 'Switch density'],
};

const Palette = (() => {
  const onTask = () => ui.page === 'tasks' && ui.view !== 'board' && !!currentTask();
  const go = (page, extra = {}) => { Object.assign(ui, {page}, extra); renderShell(); return enterPage(); };
  // ⚠️ Keys: lowercase letters act at once and can be undone, Shift+letter is for a whole-page switch, "g x" moves to a
  // page; starting or stopping an agent only opens its dialog.
  const actions = [
    {id: 'palette', key: 'Mod+K', run: () => open()},
    {id: 'help', key: '?', run: () => showKeyboard()},
    {id: 'search', key: '/', run: () => go('tasks', {view: 'list', mobileDetail: false}).then(() => document.querySelector('#search-input')?.focus())},
    {id: 'new', key: 'n', when: () => ui.online, run: () => openTaskForm()},
    {id: 'home', key: 'g h', run: () => go('home')},
    {id: 'tasks', key: 'g t', run: () => go('tasks', {view: 'list'})},
    {id: 'board', key: 'g b', run: () => go('tasks', {view: 'board'})},
    {id: 'inbox', key: 'g i', run: () => go('inbox')},
    {id: 'runs', key: 'g r', run: () => go('runs')},
    {id: 'machines', key: 'g m', run: () => go('machines')},
    {id: 'agents', key: 'g a', run: () => go('agents')},
    {id: 'projects', key: 'g p', run: () => go('projects')},
    {id: 'settings', key: 'g s', run: () => go('settings')},
    {id: 'view', key: 'v', when: () => ui.page === 'tasks', run: () => { ui.view = ui.view === 'board' ? 'list' : 'board'; renderPage(); }},
    {id: 'dispatch', key: 'd', when: () => onTask() && ui.online, run: () => openDispatch()},
    {id: 'edit', key: 'e', when: () => onTask() && ui.online, run: () => openTaskForm(true)},
    {id: 'stop', key: 'x', when: () => onTask() && ui.online && !!openRun(ui.task), run: () => confirmAction('stop')},
    {id: 'done', key: 'Shift+D', when: () => onTask() && ui.online && currentTask().status === 'todo' && !currentTask().flow,
      run: () => openRun(ui.task) ? confirmAction('done') : setTaskStatus('done')},
    {id: 'undo', key: 'Mod+Z', when: () => !!ui.undo, run: () => undo()},
    {id: 'theme', key: 'Shift+T', run: () => { theme = {system: 'light', light: 'dark', dark: 'system'}[theme] || 'system'; applyPreferences(); renderShell(); toast(t(theme)); }},
    {id: 'lang', key: 'Shift+L', run: () => { lang = lang === 'zh' ? 'en' : 'zh'; applyPreferences(); renderShell(); }},
    {id: 'density', key: 'Shift+M', run: () => { const d = Look.cycleDensity(); toast(t('density.' + d)); }},
  ];
  const byID = Object.fromEntries(actions.map(a => [a.id, a]));
  const mac = /Mac|iPhone|iPad/.test(globalThis.navigator?.platform || '');
  const shown = key => key.replace('Mod+', mac ? '⌘' : 'Ctrl+');
  const names = a => (paletteWords['act.' + a.id] || [a.id, a.id]);

  // hint is an action's key as shown on its button.
  const hint = id => byID[id] ? ` <kbd>${esc(shown(byID[id].key))}</kbd>` : '';

  // search ranks the actions for query by either of their names, their id or their key: a name that starts with it
  // first, then one that holds it.
  function search(query) {
    const q = query.trim().toLowerCase();
    const score = a => {
      if (!q) return 1;
      const hay = [...names(a), a.id, a.key].map(s => s.toLowerCase());
      if (hay.some(s => s.startsWith(q))) return 3;
      if (hay.some(s => s.includes(q))) return 2;
      return 0;
    };
    return actions.map(a => [a, score(a)]).filter(([, s]) => s > 0).sort((x, y) => y[1] - x[1]).map(([a]) => a);
  }

  // keyName is a key press as the table spells keys: "n", "Shift+D", "Mod+K"; the full-width marks of a CJK input
  // method count as their ASCII keys.
  function keyName(e) {
    const k = ({'？': '?', '、': '/', '／': '/'})[e.key] || e.key;
    if ((e.metaKey || e.ctrlKey) && k.length === 1) return 'Mod+' + k.toUpperCase();
    if (e.shiftKey && /^[A-Z]$/.test(k)) return 'Shift+' + k;
    return k;
  }

  let pending = '', pendingTimer;
  // key runs the action a key press (or "g" and then a key) stands for; false when it stands for none.
  function key(e) {
    const name = keyName(e), full = pending ? pending + ' ' + name : name;
    clearTimeout(pendingTimer);
    if (!pending && actions.some(a => a.key.startsWith(name + ' '))) {
      pending = name; pendingTimer = setTimeout(() => { pending = ''; }, 1200); e.preventDefault(); return true;
    }
    pending = '';
    const a = actions.find(x => x.key === full);
    if (!a || a.when && !a.when()) return false;
    e.preventDefault(); a.run(); return true;
  }

  // rows is the shortcuts page: every action with its key.
  const rows = () => actions.map(a => `<div class="flex between"><span>${esc(t('act.' + a.id))}</span><kbd>${esc(shown(a.key)).replace(' ', ` ${t('thenKey')} `)}</kbd></div>`).join('');

  let picked = 0, items = [];
  function list(query) {
    const acts = search(query).filter(a => a.id !== 'palette' && (!a.when || a.when())).map(a => ({kind: 'action', a}));
    const q = query.trim().toLowerCase();
    const tasks = q ? Object.values(ui.state.tasks).filter(x => `${x.title} ${x.id}`.toLowerCase().includes(q)).slice(0, 8).map(x => ({kind: 'task', x})) : [];
    items = [...acts, ...tasks];
    picked = Math.min(picked, Math.max(0, items.length - 1));
    const row = (it, i) => it.kind === 'action'
      ? `<li role="option" id="pal-${i}" aria-selected="${i === picked}" data-i="${i}" class="${i === picked ? 'picked' : ''}"><span>${esc(t('act.' + it.a.id))}</span><kbd>${esc(shown(it.a.key))}</kbd></li>`
      : `<li role="option" id="pal-${i}" aria-selected="${i === picked}" data-i="${i}" class="${i === picked ? 'picked' : ''}"><span>${esc(it.x.title)}</span><span class="mono muted">${esc(it.x.id)}</span></li>`;
    const el = document.querySelector('#palette-list');
    el.innerHTML = items.length ? items.map(row).join('') : `<li class="muted">${t('noMatch')}</li>`;
    document.querySelector('#palette-input')?.setAttribute('aria-activedescendant', items.length ? 'pal-' + picked : '');
    el.querySelector('.picked')?.scrollIntoView({block: 'nearest'});
  }

  async function choose(i) {
    const it = items[i]; if (!it) return;
    closeModal(true);
    if (it.kind === 'task') { await go('tasks', {view: 'list'}); await selectTask(it.x.id); return; }
    await it.a.run();
  }

  function open() {
    picked = 0;
    showModal('palette', t('palette'), `<div class="modal-body stack palette"><input id="palette-input" role="combobox" aria-expanded="true" aria-controls="palette-list" aria-autocomplete="list" autocomplete="off" spellcheck="false" placeholder="${t('paletteHint')}" autofocus><ul id="palette-list" role="listbox" aria-label="${t('palette')}"></ul></div>`, '');
    const input = document.querySelector('#palette-input');
    list('');
    input.addEventListener('input', () => { picked = 0; list(input.value); });
    input.addEventListener('keydown', e => {
      if (e.isComposing) return;
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); picked = (picked + (e.key === 'ArrowDown' ? 1 : items.length - 1)) % Math.max(items.length, 1); list(input.value); }
      else if (e.key === 'Enter') { e.preventDefault(); choose(picked); }
    });
    document.querySelector('#palette-list').addEventListener('click', e => { const li = e.target.closest('[data-i]'); if (li) choose(Number(li.dataset.i)); });
  }

  return {actions, search, keyName, key, hint, rows, open};
})();
