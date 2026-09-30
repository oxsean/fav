// format spells numbers, times and spans the way every page shows them: 312k tokens, $3.18, 1h 04m, 14:32.

// tokens: 940, 12.4k, 312k, 1.2M.
export function tokens(n) {
  n = Math.max(0, Math.round(n || 0));
  if (n < 1000) return String(n);
  if (n < 1e6) return (n < 1e4 ? (n / 1e3).toFixed(1).replace(/\.0$/, '') : Math.round(n / 1e3)) + 'k';
  return (n / 1e6).toFixed(1).replace(/\.0$/, '') + 'M';
}

// money: dollars to two places; under a cent shows as <$0.01.
export function money(usd) {
  if (!usd) return '$0';
  if (usd < 0.01) return '<$0.01';
  return '$' + usd.toFixed(2);
}

// duration: 45s, 12m, 1h 04m, 2d 3h.
export function duration(ms) {
  const s = Math.max(0, Math.floor((ms || 0) / 1000));
  if (s < 60) return s + 's';
  const m = Math.floor(s / 60);
  if (m < 60) return m + 'm';
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${String(m % 60).padStart(2, '0')}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

const two = n => String(n).padStart(2, '0');

// clock is a time of day in the page's time zone: 09:05.
export const clock = at => {
  const d = new Date(at);
  return `${two(d.getHours())}:${two(d.getMinutes())}`;
};

// day is a date without the year: 9-30 in either language, as the canvas writes it.
export const day = at => {
  const d = new Date(at);
  return `${d.getMonth() + 1}-${two(d.getDate())}`;
};

// usageTokens is what a run's usage counts as tokens: sent (not from the cache), written to the cache, and output.
export const usageTokens = u => (u ? (u.input || 0) + (u.cache_write || 0) + (u.output || 0) : 0);

// browserOf names a browser session by what its User-Agent says: the browser and the system ('Chrome · macOS'), as
// much of it as it tells, '' for none. The order matters: Edge, Opera and Samsung's browser say Chrome too, and every
// browser on an iPhone says Safari.
export function browserOf(ua) {
  ua = ua || '';
  const os = /iPad/.test(ua) ? 'iPad' : /iPhone|iPod/.test(ua) ? 'iPhone' : /Android/.test(ua) ? 'Android' : /CrOS/.test(ua) ? 'ChromeOS'
    : /Mac OS X|Macintosh/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux|X11/.test(ua) ? 'Linux' : '';
  const browser = /Edg(e|A|iOS)?\//.test(ua) ? 'Edge' : /OPR\/|Opera/.test(ua) ? 'Opera' : /SamsungBrowser\//.test(ua) ? 'Samsung Internet'
    : /Firefox\/|FxiOS\//.test(ua) ? 'Firefox' : /Chrome\/|CriOS\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) && /Version\//.test(ua) ? 'Safari' : '';
  return [browser, os].filter(Boolean).join(' · ');
}
