/* Shared helpers. No framework: the panel is a handful of screens and a
   poller, and a build step would be the largest thing in the repo. */

const S = JSON.parse(document.getElementById('i18n').textContent);

/** t looks up a string and fills {placeholders}. */
function t(key, vars) {
  let s = S.strings[key] || key;
  if (vars) for (const k in vars) s = s.replaceAll('{' + k + '}', vars[k]);
  return s;
}

function el(tag, attrs, ...kids) {
  const n = document.createElement(tag);
  for (const k in (attrs || {})) {
    const v = attrs[k];
    if (v === false || v === null || v === undefined) continue;
    if (k === 'class') n.className = v;
    else if (k === 'html') n.innerHTML = v;
    else if (k.startsWith('on')) n.addEventListener(k.slice(2), v);
    else if (v === true) n.setAttribute(k, '');
    else n.setAttribute(k, v);
  }
  for (const kid of kids.flat()) {
    if (kid === null || kid === undefined || kid === false) continue;
    n.append(kid.nodeType ? kid : document.createTextNode(kid));
  }
  return n;
}

const $ = (sel, root) => (root || document).querySelector(sel);
const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));

/** api wraps fetch so every caller gets the server's message, not "500". */
async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined,
    credentials: 'same-origin',
  });
  let data = null;
  const text = await res.text();
  if (text) { try { data = JSON.parse(text); } catch { data = { error: text }; } }
  if (!res.ok) {
    if (res.status === 401 && location.pathname !== '/login') location.href = '/login';
    throw new Error((data && data.error) || res.statusText || t('error.generic'));
  }
  return data;
}

let toastTimer;
function toast(msg, isError) {
  let n = $('#toast');
  if (!n) { n = el('div', { id: 'toast' }); document.body.append(n); }
  n.textContent = msg;
  n.classList.toggle('err', !!isError);
  n.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => n.classList.remove('show'), 4000);
}

/** busy disables a button and shows a spinner while `fn` runs. */
async function busy(button, fn) {
  if (button.disabled) return;
  button.disabled = true;
  button.classList.add('busy');
  try { return await fn(); }
  catch (e) { toast(e.message, true); throw e; }
  finally { button.disabled = false; button.classList.remove('busy'); }
}

function clock(seconds) {
  seconds = Math.max(0, Math.round(seconds));
  const m = Math.floor(seconds / 60), s = seconds % 60;
  return m + ':' + String(s).padStart(2, '0');
}

function bytes(n) {
  if (!n) return '0 B';
  const u = ['B', 'KB', 'MB', 'GB'];
  const i = Math.min(u.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  return (n / Math.pow(1024, i)).toFixed(i ? 1 : 0) + ' ' + u[i];
}

function when(iso) {
  const d = new Date(iso);
  if (isNaN(d)) return '';
  const today = new Date();
  const sameDay = d.toDateString() === today.toDateString();
  return sameDay
    ? d.toLocaleTimeString(S.lang, { hour: '2-digit', minute: '2-digit' })
    : d.toLocaleDateString(S.lang, { day: 'numeric', month: 'short' }) + ' ' +
      d.toLocaleTimeString(S.lang, { hour: '2-digit', minute: '2-digit' });
}

/* Icons are inline SVG so they inherit colour and never need a second
   request. Stroke weights match the design system. */
const icon = {
  mark: (size) => svg(size || 22, '<rect x="6" y="4" width="12" height="16" rx="3" stroke="currentColor" stroke-width="1.6"/>' +
    '<rect x="9.5" y="9.5" width="5" height="5" rx="1" stroke="currentColor" stroke-width="1.6"/>' +
    '<path d="M3.6 8H6M3.6 12H6M3.6 16H6M18 8h2.4M18 12h2.4M18 16h2.4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/>', 24),
  check: () => svg(14, '<path d="M3 8.5l3 3 7-7" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>'),
  cross: () => svg(14, '<path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/>'),
  spark: () => svg(15, '<path d="M8 1.8l1.6 4 4 1.6-4 1.6L8 13l-1.6-4-4-1.6 4-1.6L8 1.8z" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"/>'),
  down: () => svg(13, '<path d="M8 2.5v8M5 7.5L8 10.5l3-3" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/><path d="M2.5 13h11" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/>'),
  warn: () => svg(14, '<path d="M8 2.5l6 11H2l6-11z" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"/><path d="M8 6.5v3.2" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/><circle cx="8" cy="11.6" r="0.8" fill="currentColor"/>'),
  clock: () => svg(14, '<circle cx="8" cy="8" r="5.5" stroke="currentColor" stroke-width="1.4"/><path d="M8 5v3.2l2 1.2" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/>'),
  ring: () => svg(16, '<circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.5"/>'),
  dashed: () => svg(16, '<circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.5" stroke-dasharray="4 3"/>'),
  image: () => svg(28, '<rect x="2.5" y="3.5" width="11" height="9" rx="1.5" stroke="currentColor" stroke-width="1.2"/><circle cx="6" cy="6.5" r="1" fill="currentColor"/><path d="M3 11l3-2.5 2.5 2 2-1.5L13 12" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round"/>'),
  wave: () => svg(28, '<path d="M2 8h2M5.5 5v6M9 3v10M12.5 6v4M16 8h-2" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/>'),
};

function svg(size, inner, box) {
  const n = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  n.setAttribute('width', size);
  n.setAttribute('height', size);
  n.setAttribute('viewBox', '0 0 ' + (box || 16) + ' ' + (box || 16));
  n.setAttribute('fill', 'none');
  n.setAttribute('aria-hidden', 'true');
  n.innerHTML = inner;
  return n;
}

/** setLanguage stores the choice and reloads; the server renders the strings. */
function wireLanguagePicker(root) {
  $$('[data-lang]', root).forEach((a) => a.addEventListener('click', async (e) => {
    e.preventDefault();
    const lang = a.dataset.lang;
    try {
      await api('POST', '/api/settings', { language: lang, follow_browser: false });
    } catch { /* not signed in yet: fall through to the query parameter */ }
    const u = new URL(location.href);
    u.searchParams.set('lang', lang);
    location.href = u.toString();
  }));
}
