'use strict';

// home.js holds the home page: what waits for you, answered in place, then what runs and on which machines, how each
// project stands with 7 days of usage, and the latest sessions. Every block follows the pushed state. It also holds the
// runs page: every run, filtered, with a preview of the chosen one's latest output.
const homeWords = {
  home: ['首页', 'Home'], homeHelp: ['等你的事在最上面，就地处理。', 'What waits for you comes first; handle it here.'],
  nothingWaits: ['没有等你的事。', 'Nothing waits for you.'], allWaiting: ['全部 {0} 条', 'All {0}'],
  runningNow: ['运行中', 'Running'], noneRunning: ['没有在跑的。', 'Nothing is running.'], progress: ['项目进度', 'Projects'],
  noProjects: ['还没有项目。', 'No projects yet.'], usage7: ['7 天用量', 'Last 7 days'], recent: ['最近会话', 'Recent sessions'],
  noRecent: ['还没有结束的会话。', 'No finished sessions yet.'], quickReply: ['回复…', 'Reply…'], send: ['发送', 'Send'],
  noProject: ['不属于项目', 'No project'], finishedOf: ['{0}/{1} 完成', '{0} of {1} done'], tokensDay: ['{0} token', '{0} tokens'],
  runs: ['运行', 'Runs'], runsHelp: ['每一次运行，选中一条看最近的输出。', 'Every run; pick one to see its latest output.'],
  runsOpen: ['未结束', 'Open'], runsFailed: ['失败', 'Failed'], runsEnded: ['已结束', 'Ended'], anyMachine: ['任何机器', 'Any machine'],
  noRuns: ['没有符合的运行', 'No runs match'], pickRun: ['选中一条运行看预览', 'Pick a run to preview it'], openRun: ['在任务里打开', 'Open in its task'],
  queuedN: ['排队 {0}', '{0} queued'], openBoard: ['在看板里看', 'Open the board'],
};

const Home = (() => {
  const pages = ['home', 'runs'];
  const runs = {state: '', machine: '', pick: '', tails: new Map()};

  function nav() {
    const item = (page, symbol) => `<button class="nav-item ${ui.page === page ? 'active' : ''}" data-action="page" data-page="${page}" ${ui.page === page ? 'aria-current="page"' : ''}>${icon(symbol)}${t(page)}</button>`;
    return item('home', 'home') + item('runs', 'play');
  }

  async function enter() {
    if (ui.page === 'runs') { render(); if (runs.pick) await tail(runs.pick); return; }
    try { await Tree.refreshInbox(); } catch (_) {}
    if (ui.page === 'home') render();
  }

  const failed = r => ['failed', 'abandoned'].includes(r.state) || r.state === 'exited' && r.exit_code !== 0;

  // runsPage lists every run, newest first, filtered by state and machine; the picked one shows its latest output.
  function runsPage() {
    const all = Object.values(ui.state.runs).sort((a, b) => b.queued_at.localeCompare(a.queued_at));
    const test = {open: r => openStates.has(r.state), failed, ended: r => !openStates.has(r.state) && !failed(r)};
    const shown = all.filter(r => (!runs.state || test[runs.state](r)) && (!runs.machine || r.machine === runs.machine)).slice(0, 200);
    const chip = (value, label) => `<button type="button" data-action="runs-state" data-value="${value}" aria-pressed="${runs.state === value}" class="${runs.state === value ? 'active' : ''}">${label} <span class="num">${value ? all.filter(test[value]).length : all.length}</span></button>`;
    const machines = [...new Set(all.map(r => r.machine))].sort();
    const rows = shown.map(r => `<button type="button" class="runs-row ${runs.pick === r.id ? 'selected' : ''}" data-action="runs-pick" data-run="${esc(r.id)}">${badge(r.state)}<span class="mono">${esc(r.id)}</span><span class="runs-title">${esc(ui.state.tasks[r.task]?.title || r.task)}</span><span class="chip">${esc(r.stage || '—')}</span><span class="mono">${esc(r.machine)}</span><span class="mono">${esc(r.agent)}${r.profile?.model ? ' · ' + esc(r.profile.model) : ''}</span><span class="mono num muted">${date(r.started_at || r.queued_at)}</span><span class="mono num">${r.started_at ? elapsed(r) : ''}</span><span class="mono num">${r.usage ? tokens((r.usage.input || 0) + (r.usage.cache_write || 0) + (r.usage.output || 0)) : ''}${money(r.usage?.cost_usd) ? ' · ' + money(r.usage.cost_usd) : ''}</span></button>`);
    return `<header class="page-heading"><div><h1>${t('runs')}</h1><p class="page-subtitle">${t('runsHelp')}</p></div></header>
      <div class="inbox-filters"><div class="view-toggle" role="group" aria-label="${t('runs')}">${chip('', t('allKinds'))}${chip('open', t('runsOpen'))}${chip('failed', t('runsFailed'))}${chip('ended', t('runsEnded'))}</div>
      <select id="runs-machine" aria-label="${t('machines')}"><option value="">${t('anyMachine')}</option>${machines.map(m => `<option ${runs.machine === m ? 'selected' : ''}>${esc(m)}</option>`).join('')}</select></div>
      <div class="runs-page"><div class="runs-list">${rows.join('') || statePanel('tasks', t('noRuns'), '')}</div><aside class="runs-preview">${preview()}</aside></div>`;
  }

  function preview() {
    const r = ui.state.runs[runs.pick];
    if (!r) return `<p class="muted">${t('pickRun')}</p>`;
    const data = runs.tails.get(r.id), events = data?.events?.length ? renderEvents(data.events.slice(-12)) : '';
    const why = [r.reason, r.exit_code !== undefined && r.exit_code !== null && !openStates.has(r.state) ? 'exit ' + r.exit_code : ''].filter(Boolean).map(esc).join(' · ');
    return `<div class="stack"><div class="flex">${badge(r.state)}<strong>${esc(ui.state.tasks[r.task]?.title || r.task)}</strong></div><p class="muted mono">${esc(r.id)} · ${esc(r.machine)} / ${esc(r.agent)}${why ? ' · ' + why : ''}</p>
      ${r.note || r.last ? `<p>${esc(r.note || r.last)}</p>` : ''}${r.ask ? `<p class="home-ask">${esc(r.ask)}</p>` : ''}
      <div class="output-view runs-tail">${data?.error ? `<p class="form-error">${esc(data.error)}</p>` : events || `<p class="muted">${t(data ? 'noOutput' : 'loading')}</p>`}</div>
      <div>${button('home-run', t('openRun'), `data-id="${esc(r.task)}" data-run="${esc(r.id)}"`, 'primary')}</div></div>`;
  }

  async function tail(id) {
    try { runs.tails.set(id, await api.runOutputPage({run: id, before: -1, n: 40})); } catch (error) { runs.tails.set(id, {error: errorText(error)}); }
    if (ui.page === 'runs' && runs.pick === id) render();
  }

  // control is how an item that waits is handled in place: a request of its open run, a reply to its run that asked,
  // else opening the task.
  function control(x) {
    const open = openRun(x.task), last = latestRun(x.task), off = ui.online ? '' : 'disabled';
    const q = open?.requests?.[0];
    if (q) return requestForm(open, q, off);
    if (last && waiting(last) && last.session) {
      return `<form class="home-reply flex" data-run="${esc(last.id)}" data-command="${commandID()}"><div class="form-error" role="alert" hidden></div><input id="home-reply-${esc(last.id)}" name="text" class="grow" data-draft="home:${esc(last.id)}" value="${esc(ui.drafts.get('home:' + last.id) || '')}" placeholder="${t('quickReply')}" aria-label="${t('reply')}" ${off}><button type="submit" class="primary" ${off}>${t('send')}</button></form><p class="hint">${esc(t('repliesTo').replace('{0}', last.id))} · ${t('sendSingle')}</p>`;
    }
    return '';
  }

  // quick is what the head of an item offers besides opening it: marking done what only waits for that, or passing
  // and sending back a workflow's human gate (a workflow task is never marked done by hand).
  const quick = x => {
    const on = `data-id="${esc(x.task)}" ${ui.online ? '' : 'disabled'}`, gate = x.reason === 'accept' && ui.state.tasks[x.task]?.flow;
    const act = gate ? button('tree-pass', t('pass'), on, 'quiet') + button('tree-rework', t('sendBack'), on, 'quiet')
      : x.reason === 'merge_conflict' ? button('tree-merge', t('retryMerge'), on, 'quiet')
      : ['ended', 'accept'].includes(x.reason) ? button('home-done', t('markDone'), on, 'quiet') : '';
    return act + button('home-open', t('openTask'), `data-id="${esc(x.task)}"`, 'quiet');
  };

  // waitRow is one item that waits: why, which task, where it runs, how long, and what handles it in place; extra goes
  // under the head.
  function waitRow(x, extra = '') {
    const last = latestRun(x.task), task = ui.state.tasks[x.task], ask = last?.ask && waiting(last) ? `<p class="home-ask">${esc(last.ask)}</p>` : '';
    const where = [task?.stage, last?.machine, last && (last.agent + (last.profile?.model ? ' · ' + last.profile.model : ''))].filter(Boolean).map(esc).join(' · ');
    return `<article class="home-wait"><div class="home-wait-head"><span class="status sit-waiting"><span class="status-icon" aria-hidden="true">!</span>${esc(Tree.why(x.reason))}</span><span class="muted mono">${esc(x.task)}</span><button type="button" class="link" data-action="home-open" data-id="${esc(x.task)}">${esc(x.title)}</button><span class="muted">${where}</span><span class="muted mono num" title="${date(x.since)}">${t('waited').replace('{0}', ago(x.since))}</span><span class="home-actions">${quick(x)}</span></div>${extra}${ask}${control(x)}</article>`;
  }

  function waitsBlock() {
    const items = Tree.inbox();
    if (!items.length) return `<section class="home-block home-waits empty"><h2>${icon('check')}${t('inbox')}</h2><span class="muted">${t('nothingWaits')}</span></section>`;
    const rank = x => ['permission', 'asked'].includes(x.reason) ? 0 : ['failed', 'unknown', 'merge_conflict'].includes(x.reason) ? 1 : 2;
    const rows = [...items].sort((a, b) => rank(a) - rank(b)).slice(0, 5).map(x => waitRow(x));
    const more = items.length > 5 ? button('page', t('allWaiting').replace('{0}', items.length), 'data-page="inbox"', 'quiet') : '';
    return `<section class="home-block home-waits"><h2>${icon('error')}${t('inbox')}<span class="count">${items.length}</span></h2>${rows.join('')}${more}</section>`;
  }

  function runningBlock() {
    const runs = Object.values(ui.state.runs).filter(r => openStates.has(r.state)).sort((a, b) => a.queued_at.localeCompare(b.queued_at));
    const queued = runs.filter(r => r.state === 'queued').length;
    const rows = runs.map(r => {
      const task = ui.state.tasks[r.task];
      const stop = r.want === 'stop' ? `<span class="muted">${t('stopping')}</span>` : button('home-stop', `${icon('stop')}<span class="sr-only">${t('stop')}</span>`, `data-id="${esc(r.task)}" data-run="${esc(r.id)}" title="${t('stop')}" ${ui.online ? '' : 'disabled'}`, 'quiet danger');
      return `<div class="home-run"><span class="row-glyph status ${esc(r.state)}" aria-hidden="true">${statusSymbols[r.state] || '·'}</span><span class="sr-only">${t(r.state)}</span>
        <button type="button" class="link home-run-title" data-action="home-run" data-id="${esc(r.task)}" data-run="${esc(r.id)}">${esc(task?.title || r.task)}</button><span class="chip">${esc(r.stage || '—')}</span><span class="mono">${esc(r.machine)}</span><span class="mono">${esc(r.agent)}${r.profile?.model ? ' · ' + esc(r.profile.model) : ''}</span>
        <span class="home-last muted">${esc(r.note || r.last || '')}</span><span class="mono num">${elapsed(r)}</span><span class="mono num">${r.usage ? tokens((r.usage.input || 0) + (r.usage.cache_write || 0) + (r.usage.output || 0)) : ''}</span>${stop}</div>`;
    });
    const machines = ui.machines.map(m => `<div class="home-machine">${badge(m.state)}<span class="mono">${esc(m.name)}</span>${m.os ? `<span class="muted">${esc(m.os)}</span>` : ''}<span class="mono num">${m.active ?? 0}/${m.slots ?? 0}${m.queued ? ' · ' + t('queuedN').replace('{0}', m.queued) : ''}</span>${m.state !== 'connected' && (m.error || m.detail) ? `<span class="muted home-machine-why">${esc(m.error || m.detail)}</span>` : ''}</div>`).join('');
    return `<section class="home-block home-running"><h2>${icon('play')}${t('runningNow')}<span class="count">${runs.filter(r => r.state === 'running' || r.state === 'starting').length}</span>${queued ? `<span class="muted home-note">${t('queuedN').replace('{0}', queued)}</span>` : ''}</h2>
      <div class="home-runs">${rows.join('') || `<p class="muted">${t('noneRunning')}</p>`}</div></section>
      <section class="home-block home-machines"><h2>${icon('machine')}${t('machines')}</h2>${machines || `<p class="muted">—</p>`}</section>`;
  }

  function projectsBlock() {
    const tasks = Object.values(ui.state.tasks), projects = Object.values(ui.state.projects || {});
    const rows = projects.map(p => {
      const mine = tasks.filter(x => x.project === p.id), done = mine.filter(x => x.status === 'done').length;
      const stages = {};
      for (const x of mine) if (x.stage && x.status === 'todo') stages[x.stage] = (stages[x.stage] || 0) + 1;
      const kinds = {};
      for (const x of mine) { const k = Fold.situation(ui.state, x).kind; kinds[k] = (kinds[k] || 0) + 1; }
      const link = (label, n, extra) => `<button type="button" class="chip link" data-action="home-board" data-project="${esc(p.id)}" ${extra}>${label} <strong class="num">${n}</strong></button>`;
      return `<div class="home-project"><div class="flex between"><strong>${esc(p.name || p.id)}</strong><span class="muted num">${t('finishedOf').replace('{0}', done).replace('{1}', mine.length)}</span></div>
        <div class="bar" role="img" aria-label="${t('finishedOf').replace('{0}', done).replace('{1}', mine.length)}"><span data-fill="${mine.length ? Math.round(done * 100 / mine.length) : 0}"></span></div>
        <div class="flex wrap">${Object.entries(stages).map(([s, n]) => link(esc(s), n, `data-stage="${esc(s)}"`)).join('')}${['waiting', 'running', 'queued', 'backlog'].filter(k => kinds[k]).map(k => link(t('sit.' + k), kinds[k], '')).join('')}</div></div>`;
    });
    return `<section class="home-block home-projects"><h2>${icon('tasks')}${t('progress')}</h2>${rows.join('') || `<p class="muted">${t('noProjects')}</p>`}</section>`;
  }

  function usageBlock() {
    const day = 864e5, today = new Date(); today.setHours(0, 0, 0, 0);
    const days = Array.from({length: 7}, (_, i) => ({at: new Date(today.getTime() - (6 - i) * day), tokens: 0, usd: 0}));
    for (const r of Object.values(ui.state.runs)) {
      if (!r.usage) continue;
      const at = new Date(r.ended_at || r.started_at || r.queued_at), i = Math.floor((at - days[0].at) / day);
      if (i < 0 || i > 6) continue;
      days[i].tokens += (r.usage.input || 0) + (r.usage.cache_write || 0) + (r.usage.output || 0);
      days[i].usd += r.usage.cost_usd || 0;
    }
    const top = Math.max(1, ...days.map(d => d.tokens)), total = days.reduce((s, d) => s + d.tokens, 0), usd = days.reduce((s, d) => s + d.usd, 0);
    const w = 28, gap = 10, h = 72, label = d => d.at.toLocaleDateString(lang === 'zh' ? 'zh-CN' : 'en-GB', {weekday: 'narrow'});
    const bars = days.map((d, i) => { const bh = Math.round(d.tokens / top * h); return `<g><title>${d.at.toLocaleDateString()} · ${tokens(d.tokens)}${d.usd ? ' · $' + d.usd.toFixed(2) : ''}</title><rect class="usage-bar" x="${i * (w + gap)}" y="${h - bh}" width="${w}" height="${Math.max(bh, 1)}" rx="3"/><text class="usage-label" x="${i * (w + gap) + w / 2}" y="${h + 14}" text-anchor="middle">${label(d)}</text></g>`; }).join('');
    return `<section class="home-block home-usage"><h2>${icon('refresh')}${t('usage7')}</h2><p class="num"><strong>${tokens(total)}</strong> token${usd ? ` · <strong>$${usd.toFixed(2)}</strong>` : ''}</p>
      <svg class="usage-chart" viewBox="0 0 ${7 * w + 6 * gap} ${h + 18}" role="img" aria-label="${t('usage7')}">${bars}</svg></section>`;
  }

  function recentBlock() {
    const runs = Object.values(ui.state.runs).filter(r => r.session && !openStates.has(r.state)).sort((a, b) => (b.ended_at || '').localeCompare(a.ended_at || '')).slice(0, 8);
    const rows = runs.map(r => `<button type="button" class="home-recent" data-action="home-run" data-id="${esc(r.task)}" data-run="${esc(r.id)}">${badge(r.state)}<span class="home-run-title">${esc(ui.state.tasks[r.task]?.title || r.task)}</span><span class="mono">${esc(r.machine)} / ${esc(r.agent)}</span><span class="mono muted num">${date(r.ended_at)}</span></button>`);
    return `<section class="home-block home-recents"><h2>${icon('chat')}${t('recent')}</h2>${rows.join('') || `<p class="muted">${t('noRecent')}</p>`}</section>`;
  }

  function render() {
    const el = document.querySelector('#page-content'); if (!el) return;
    if (ui.page === 'runs') { el.innerHTML = runsPage(); return; }
    el.innerHTML = `<header class="page-heading"><div><h1>${t('home')}</h1><p class="page-subtitle">${t('homeHelp')}</p></div><div class="flex"><span class="heading-right">${date(new Date())}</span>${button('new', `${icon('plus')}${t('newTask')}${Palette.hint('new')}`, ui.online ? '' : 'disabled', 'primary')}</div></header>
      <div class="home">${waitsBlock()}<div class="home-row home-more">${runningBlock()}</div><div class="home-row home-more">${projectsBlock()}${usageBlock()}</div><div class="home-more">${recentBlock()}</div></div>`;
    for (const bar of el.querySelectorAll('[data-fill]')) bar.style.width = `${bar.dataset.fill}%`; // CSP blocks style attributes
  }

  async function click(action, target) {
    const d = target.dataset;
    switch (action) {
      case 'home-open': ui.page = 'tasks'; ui.view = 'list'; renderShell(); await selectTask(d.id); return true;
      case 'home-run': ui.page = 'tasks'; ui.view = 'list'; renderShell(); await selectTask(d.id); ui.run = d.run; renderDetail(); await fetchOutput(); return true;
      case 'home-done': await setTaskStatus('done', d.id); await Tree.refreshInbox(); if (ui.page === 'home') render(); else Tree.render(); return true;
      case 'home-stop': ui.task = d.id; ui.run = d.run; confirmAction('stop'); return true;
      case 'runs-state': runs.state = d.value; render(); return true;
      case 'runs-pick': runs.pick = d.run; render(); await tail(d.run); return true;
      case 'home-board':
        Object.assign(ui, {page: 'tasks', view: 'board', project: d.project, stage: d.stage || '', status: '', machine: '', runState: '', search: ''});
        renderShell(); return true;
    }
    return false;
  }

  async function submit(form) {
    if (!form.classList.contains('home-reply')) return false;
    const text = new FormData(form).get('text') || '';
    if (!text.trim()) { const e = form.querySelector('.form-error'); e.hidden = false; e.textContent = t('replyEmpty'); return true; }
    const result = await api.runContinue({run: form.dataset.run, text}, {command_id: form.dataset.command});
    ui.drafts.delete('home:' + form.dataset.run); ui.state.runs[result.id] = result; render(); toast(t('replied'));
    return true;
  }

  const change = el => { if (el.id !== 'runs-machine') return false; runs.machine = el.value; render(); return true; };

  return {pages, nav, enter, render, click, submit, change, waitRow};
})();
