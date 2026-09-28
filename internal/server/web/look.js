'use strict';

// look.js holds how the page looks to this viewer: skin, accent, contrast and density (theme and language live in
// app.js), kept in this browser, and the settings page that changes them with a live preview.
const lookWords = {
  settings: ['设置', 'Settings'], settingsHelp: ['外观只存在这个浏览器里，改了立即生效。', 'Appearance is kept in this browser and applies at once.'],
  appearance: ['外观', 'Appearance'], skin: ['皮肤', 'Skin'], accent: ['强调色', 'Accent'], accentOwn: ['用皮肤自带的', "Use the skin's own"],
  contrast: ['对比度', 'Contrast'], 'contrast.standard': ['标准', 'Standard'], 'contrast.high': ['高', 'High'],
  contrastHelp: ['高对比度：正文和边框更深，文字对比度至少 7:1。', 'High: darker text and borders, text at 7:1 or more.'],
  density: ['密度', 'Density'], 'density.compact': ['紧凑', 'Compact'], 'density.default': ['标准', 'Default'], 'density.comfortable': ['宽松', 'Comfortable'],
  densityHelp: ['每行 28 / 32 / 40 px，宽松多一行摘要；字号不变。', 'Rows of 28, 32 or 40 px, comfortable with a summary line; the text size stays.'],
  language: ['语言', 'Language'], preview: ['预览', 'Preview'], previewTask: ['导出 CSV', 'Export CSV'], previewTask2: ['评审导出格式', 'Review the export format'],
  'skin.tend': ['tend', 'tend'], 'skin.forest': ['森林', 'Forest'], 'skin.ember': ['余烬', 'Ember'], 'skin.graphite': ['石墨', 'Graphite'],
};

const Look = (() => {
  const pages = ['settings'];
  const densities = ['compact', 'default', 'comfortable'];
  const prefs = {skin: 'tend', accent: '', high: false, density: 'default'};
  let presets = [];
  try { Object.assign(prefs, JSON.parse(localStorage.getItem('tend-look') || '{}')); } catch (_) {}
  if (!densities.includes(prefs.density)) prefs.density = 'default';
  if (!/^[a-z]+$/.test(prefs.skin)) prefs.skin = 'tend';
  if (!/^[0-9a-f]{6}$/.test(prefs.accent)) prefs.accent = '';

  const skinName = () => prefs.skin + (prefs.accent ? '-' + prefs.accent : '') + (prefs.high ? '-high' : '');
  const preset = name => presets.find(p => p.name === name);

  function apply() {
    document.documentElement.dataset.density = prefs.density;
    const link = document.querySelector('#skin'), href = `theme/${skinName()}.css`;
    if (link && link.getAttribute('href') !== href) link.setAttribute('href', href);
    try { localStorage.setItem('tend-look', JSON.stringify(prefs)); } catch (_) {}
  }

  function nav() {
    return `<button class="nav-item ${ui.page === 'settings' ? 'active' : ''}" data-action="page" data-page="settings" ${ui.page === 'settings' ? 'aria-current="page"' : ''}>${icon('settings')}${t('settings')}</button>`;
  }

  async function enter() {
    if (!presets.length) {
      try { presets = await (await fetch('theme/presets.json')).json(); } catch (_) {}
    }
    if (ui.page === 'settings') render();
  }

  const swatch = hex => `<svg class="swatch" viewBox="0 0 16 16" aria-hidden="true"><rect width="16" height="16" rx="4" fill="${esc(hex)}"/></svg>`;
  const choice = (name, value, label, on) => `<label class="segment"><input type="radio" name="${name}" value="${esc(value)}" data-look="${name}" ${on ? 'checked' : ''}><span>${label}</span></label>`;
  const group = (legend, body, help = '') => `<fieldset class="setting"><legend>${legend}</legend><div class="segments">${body}</div>${help ? `<p class="hint">${help}</p>` : ''}</fieldset>`;

  function render() {
    const el = document.querySelector('#page-content'); if (!el) return;
    const own = preset(prefs.skin)?.input.accent || '#315fa5', accent = prefs.accent ? '#' + prefs.accent : own;
    const row = (state, title, why, time) => `<div class="task-row preview-row"><span class="row-line"><span class="row-glyph status ${state}" aria-hidden="true">${statusSymbols[state]}</span><span class="row-title">${title}</span>${why}<span class="row-time">${time}</span></span><span class="row-sum"><span class="row-id">t_3f2a</span><span>mba / claude</span>${badge(state)}</span></div>`;
    el.innerHTML = `<header class="page-heading"><div><h1>${t('settings')}</h1><p class="page-subtitle">${t('settingsHelp')}</p></div></header>
      <div class="settings-page"><section class="settings-form" aria-label="${t('appearance')}">
        ${group(t('theme'), ['system', 'light', 'dark'].map(v => choice('theme', v, t(v), theme === v)).join(''))}
        ${group(t('skin'), presets.map(p => choice('skin', p.name, swatch(p.input.accent) + t('skin.' + p.name), prefs.skin === p.name)).join(''))}
        <fieldset class="setting"><legend>${t('accent')}</legend><div class="segments"><label class="segment"><input type="color" data-look="accent" value="${esc(accent)}" aria-label="${t('accent')}"><span class="mono">${esc(accent)}</span></label>${prefs.accent ? button('look-own-accent', t('accentOwn'), '', 'quiet') : ''}</div></fieldset>
        ${group(t('contrast'), choice('contrast', 'standard', t('contrast.standard'), !prefs.high) + choice('contrast', 'high', t('contrast.high'), prefs.high), t('contrastHelp'))}
        ${group(t('density'), densities.map(d => choice('density', d, t('density.' + d), prefs.density === d)).join(''), t('densityHelp'))}
        ${group(t('language'), choice('lang', 'zh', '中文', lang === 'zh') + choice('lang', 'en', 'English', lang === 'en'))}
      </section><section class="settings-preview" aria-label="${t('preview')}"><h2>${t('preview')}</h2>
        <div class="task-list">${row('running', t('previewTask'), '', '4m 12s')}${row('queued', t('previewTask2'), `<span class="status sit-waiting"><span class="status-icon" aria-hidden="true">!</span>${t('needsYou')}</span>`, '0m 40s')}${row('failed', 'flaky test', '', '1m 03s')}</div>
        <div class="flex wrap">${['running', 'queued', 'starting', 'unknown', 'exited', 'stopped', 'failed', 'canceled'].map(badge).join('')}</div>
        <div class="flex">${button('look-noop', t('dispatch'), '', 'primary')}${button('look-noop', t('edit'))}<span class="muted">${t('taskSubtitle')}</span></div></section></div>`;
  }

  let accentTimer;
  function change(el) {
    switch (el.dataset.look) {
      case 'theme': theme = el.value; applyPreferences(); return;
      case 'lang': lang = el.value; applyPreferences(); renderShell(); return;
      case 'skin': prefs.skin = el.value; prefs.accent = ''; break;
      case 'contrast': prefs.high = el.value === 'high'; break;
      case 'density': prefs.density = el.value; break;
      case 'accent': {
        const v = el.value.slice(1).toLowerCase(), own = (preset(prefs.skin)?.input.accent || '').slice(1);
        prefs.accent = v === own ? '' : v;
        clearTimeout(accentTimer); accentTimer = setTimeout(apply, 120);
        const label = el.nextElementSibling; if (label) label.textContent = el.value;
        return;
      }
    }
    apply(); render();
  }

  async function click(action) {
    if (action === 'look-own-accent') { prefs.accent = ''; apply(); render(); return true; }
    return action === 'look-noop';
  }

  // cycleDensity moves to the next density and says which.
  function cycleDensity() {
    prefs.density = densities[(densities.indexOf(prefs.density) + 1) % densities.length]; apply();
    if (ui.page === 'settings') render();
    return prefs.density;
  }

  return {pages, apply, nav, enter, render, change, click, cycleDensity};
})();
