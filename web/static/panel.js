/* The panel: three views over one poller. */

const CAPS = S.user || {};
let status = null;          // last /api/status
let view = location.hash.replace('#', '') || (CAPS.Image ? 'image' : CAPS.Voice ? 'voice' : 'settings');
let runs = { image: [], voice: [] };
let selected = { image: null, voice: null };
let settings = null;
let pollTimer = null;

$('#mark').append(icon.mark(22));
$('#hostLabel').textContent = location.hostname;
$('#avatarBtn').textContent = (CAPS.Email || '?').slice(0, 2).toUpperCase();

$('#avatarBtn').addEventListener('click', () => {
  const m = $('#userMenu');
  if (m.hidden) {
    m.replaceChildren(
      el('div', { class: 'menu-head' }, CAPS.Email || '', el('span', {}, t('menu.role'))),
      el('button', { type: 'button', onclick: signOut }, t('common.signOut')));
  }
  m.hidden = !m.hidden;
});
document.addEventListener('click', (e) => {
  if (!$('#userWrap').contains(e.target)) $('#userMenu').hidden = true;
});

async function signOut() {
  await api('POST', '/api/logout');
  location.href = '/login';
}

/* ------------------------------------------------------------------ chrome */

function drawTabs() {
  const items = [];
  if (CAPS.Image) items.push(['image', t('nav.image')]);
  if (CAPS.Voice) items.push(['voice', t('nav.voice')]);
  items.push(['settings', t('nav.settings')]);
  $('#tabs').replaceChildren(...items.map(([id, label]) =>
    el('a', {
      class: view === id ? 'on' : '', href: '#' + id,
      onclick: (e) => { e.preventDefault(); go(id); },
    }, label)));
}

function go(next) {
  view = next;
  history.replaceState(null, '', '#' + next);
  drawTabs();
  draw();
}

function drawKernelBar() {
  const k = status && status.kernel;
  const bar = $('#kbar');
  if (!k) { bar.className = 'kbar'; $('#kbarInner').replaceChildren(); return; }

  const label = { stopped: 'kernel.stopped', starting: 'kernel.starting', ready: 'kernel.ready',
                  stopping: 'kernel.stopping', failed: 'kernel.failed' }[k.state] || 'kernel.stopped';
  const tone = k.state === 'starting' ? 'warn' : k.state === 'failed' ? 'err' : '';
  bar.className = 'kbar' + (tone ? ' ' + tone : '');

  const bits = [
    el('span', { class: 'row' },
      el('i', { class: 'dot ' + (k.state === 'ready' ? 'ok live' : k.state === 'starting' ? 'warn live' : k.state === 'failed' ? 'err' : '') }),
      el('b', { class: 'strong' }, t(label))),
  ];
  if (k.gpu) bits.push(el('span', { class: 'sep' }, '·'), el('span', { class: 'mono muted' }, k.gpu));
  if (k.state === 'ready') {
    bits.push(el('span', { class: 'sep' }, '·'));
    bits.push(status.panel.keep_alive
      ? el('span', { class: 'muted' }, t('kernel.keepAlive'))
      : el('span', { class: 'muted', html: t('kernel.idle', { t: '<span class="mono text-t">' + clock(k.stops_in) + '</span>' }) }));
  }
  if (k.state === 'failed' && k.failure) {
    bits.push(el('span', { class: 'sep' }, '·'), el('span', { style: 'color:var(--err)' }, k.failure));
  }

  const pct = k.quota_hours ? Math.min(100, (k.used_hours / k.quota_hours) * 100) : 0;
  const cls = k.blocked ? 'err' : k.warning ? 'warn' : '';
  const cells = [];
  for (let i = 0; i < 5; i++) {
    cells.push(el('i', { class: pct > i * 20 ? 'on ' + cls : '' }));
  }

  bits.push(el('span', { class: 'right row', style: 'gap:14px;flex-wrap:wrap' },
    el('span', { class: 'muted', title: t('quota.estimate') },
      t('quota.week') + ' ',
      el('b', { class: 'mono', style: 'color:var(--text);font-weight:400' }, k.used_hours.toFixed(1)),
      ' ', el('span', { class: 'dim' }, t('quota.of', { n: k.quota_hours.toFixed(0) }))),
    el('span', { class: 'meter' }, cells),
    k.state === 'ready'
      ? el('button', { class: 'btn sm', onclick: (e) => busy(e.currentTarget, stopKernel) }, t('kernel.stopNow'))
      : k.state === 'stopped' || k.state === 'failed'
        ? el('button', { class: 'btn sm', onclick: (e) => busy(e.currentTarget, startKernel) }, t('kernel.start'))
        : null));

  $('#kbarInner').replaceChildren(...bits);
}

async function stopKernel() { await api('POST', '/api/kernel/stop'); await refresh(); }
async function startKernel() { await api('POST', '/api/kernel/start'); await refresh(); }

/* ------------------------------------------------------------------- views */

function draw() {
  if (view === 'settings') return drawSettings();
  if (view === 'voice') return drawVoice();
  return drawImage();
}

function controlsPanel(...children) {
  return el('section', { class: 'panel sunk' }, el('div', { class: 'panel-body' }, ...children));
}

function fg(label, control, hint) {
  return el('label', { class: 'fg' },
    el('span', { class: 'lbl' }, label, hint ? el('span', {}, hint) : null), control);
}

function seg(id, options, value, onPick) {
  const wrap = el('div', { class: 'seg', id });
  options.forEach(([v, l]) => wrap.append(el('button', {
    type: 'button', class: String(v) === String(value) ? 'on' : '',
    onclick: () => { onPick(v); },
  }, l)));
  return wrap;
}

function coldHint() {
  const ready = status && status.kernel.state === 'ready';
  return el('p', { class: 'hint' }, t(ready ? 'image.coldNote' : 'image.coldNoteCold'));
}

/* ---- image ---------------------------------------------------------- */

const imageForm = { prompt: '', negative: '', size: 1024, steps: 28, guidance: 6.5, seed: 0 };

function drawImage() {
  const current = selected.image || runs.image.find((r) => r.status === 'done');
  const stage = el('div', { class: 'stage' });
  if (current && current.status === 'done') {
    stage.append(el('img', { src: '/media/' + current.id, alt: current.prompt }));
    const p = current.params ? JSON.parse(current.params) : {};
    stage.append(
      el('div', { class: 'chips-tl' },
        el('span', { class: 'tag' }, (p.width || '?') + ' × ' + (p.height || '?')),
        el('span', { class: 'tag' }, bytes(current.bytes))),
      el('div', { class: 'chips-br' },
        el('a', { class: 'btn sm', href: '/media/' + current.id, download: '' }, icon.down(), t('run.download'))));
  } else if (current && current.status === 'running') {
    stage.append(el('div', { class: 'empty' }, icon.dashed(), t('run.running') + '…'));
  } else if (current && current.status === 'failed') {
    stage.append(el('div', { class: 'empty', style: 'color:var(--err);max-width:70%;text-align:center' }, icon.cross(), current.error));
  } else {
    stage.append(el('div', { class: 'empty' }, icon.image(), t('image.empty')));
  }

  const thumbs = el('div', { class: 'thumbs' }, runs.image.slice(0, 11).map((r) =>
    el('button', {
      type: 'button', class: current && current.id === r.id ? 'on' : '', title: r.prompt,
      onclick: () => { selected.image = r; draw(); },
    }, r.status === 'done'
      ? el('img', { src: '/media/' + r.id, alt: '', loading: 'lazy' })
      : el('span', { class: 'pending' }, t('run.' + r.status)))));

  const meta = current && current.status === 'done' && current.params
    ? el('p', { class: 'hint mono' }, describeImage(JSON.parse(current.params)))
    : null;

  const controls = controlsPanel(
    fg(t('image.prompt'), el('textarea', {
      class: 'input', id: 'iPrompt', rows: 4, placeholder: 'A lighthouse on a basalt cliff at dusk…',
      oninput: (e) => { imageForm.prompt = e.target.value; },
    }, imageForm.prompt)),
    fg(t('image.exclude'), el('input', {
      class: 'input', id: 'iNeg', value: imageForm.negative, placeholder: 'blurry, watermark, text',
      oninput: (e) => { imageForm.negative = e.target.value; },
    }), t('image.optional')),
    fg(t('image.size'), seg('iSize', [[512, '512'], [768, '768'], [1024, '1024']], imageForm.size,
      (v) => { imageForm.size = v; draw(); })),
    el('div', { class: 'grid2' },
      fg(t('image.steps'), el('input', {
        class: 'input mono', type: 'number', min: 1, max: 80, value: imageForm.steps,
        oninput: (e) => { imageForm.steps = +e.target.value; },
      })),
      fg(t('image.guidance'), el('input', {
        class: 'input mono', type: 'number', min: 0.5, max: 20, step: 0.1, value: imageForm.guidance,
        oninput: (e) => { imageForm.guidance = +e.target.value; },
      }))),
    fg(t('image.seed'), el('div', { class: 'input', style: 'gap:8px' },
      el('input', {
        class: 'mono grow', id: 'iSeed', type: 'number', min: 0, value: imageForm.seed || '',
        placeholder: t('image.seedAuto'), style: 'background:none;border:0;outline:0',
        oninput: (e) => { imageForm.seed = +e.target.value; },
      }),
      el('button', {
        class: 'btn sm', type: 'button',
        onclick: () => { imageForm.seed = Math.floor(Math.random() * 4294967295); draw(); },
      }, t('image.random')))),
    el('button', {
      class: 'btn primary lg', id: 'iGo', disabled: blocked(),
      onclick: (e) => busy(e.currentTarget, generateImage),
    }, icon.spark(), t('image.generate')),
    blockedNote() || coldHint());

  $('#view').replaceChildren(el('div', { class: 'page' },
    el('div', { class: 'two-col' },
      el('section', { style: 'display:flex;flex-direction:column;gap:16px;min-width:0' }, stage, thumbs, meta),
      controls)));
}

function describeImage(p) {
  return ['steps ' + p.steps, 'guidance ' + p.guidance, 'seed ' + p.seed].join(' · ');
}

async function generateImage() {
  const run = await api('POST', '/api/generate/image', {
    prompt: imageForm.prompt,
    negative: imageForm.negative,
    width: imageForm.size, height: imageForm.size,
    steps: imageForm.steps, guidance: imageForm.guidance,
    seed: imageForm.seed || 0,
  });
  selected.image = null;
  await refresh();
  follow(run.run, 'image');
}

/* ---- voice ---------------------------------------------------------- */

const voiceForm = { text: '', language: S.lang, speaker: 'amelie.wav', speed: 1.0 };

function drawVoice() {
  const current = selected.voice || runs.voice.find((r) => r.status === 'done');

  const head = el('section', { class: 'panel' },
    el('div', { class: 'panel-head' },
      el('h3', {}, t('common.recent')),
      el('span', { class: 'right small dim' }, runs.voice.length + ' · ' +
        bytes(runs.voice.reduce((n, r) => n + (r.bytes || 0), 0)))));

  if (!runs.voice.length) {
    head.append(el('div', { class: 'clip' }, el('span', { class: 'muted' }, t('voice.empty'))));
  }
  runs.voice.slice(0, 12).forEach((r) => {
    const meta = el('div', { class: 'meta' },
      el('div', { class: 't ell' }, r.prompt),
      el('div', { class: 's' }, [t('run.' + r.status), when(r.created), r.bytes ? bytes(r.bytes) : null]
        .filter(Boolean).join(' · ')));
    head.append(el('div', { class: 'clip' },
      r.status === 'done'
        ? el('audio', { controls: true, preload: 'none', src: '/media/' + r.id, style: 'width:260px' })
        : el('span', { class: 'badge ' + (r.status === 'failed' ? 'err' : '') }, t('run.' + r.status)),
      meta,
      r.status === 'done'
        ? el('a', { class: 'btn sm', href: '/media/' + r.id, download: '' }, t('run.download'))
        : null));
  });

  const stage = el('div', { class: 'stage', style: 'height:auto;padding:40px' });
  if (current && current.status === 'done') {
    stage.replaceChildren(el('div', { style: 'width:100%;max-width:560px;display:flex;flex-direction:column;gap:16px' },
      el('div', { class: 'strong' }, current.prompt),
      el('audio', { controls: true, autoplay: false, src: '/media/' + current.id, style: 'width:100%' }),
      el('div', { class: 'hint mono' }, [when(current.created), bytes(current.bytes)].join(' · '))));
  } else if (current && current.status === 'failed') {
    stage.replaceChildren(el('div', { class: 'empty', style: 'color:var(--err);text-align:center' }, icon.cross(), current.error));
  } else if (current && current.status === 'running') {
    stage.replaceChildren(el('div', { class: 'empty' }, icon.dashed(), t('run.running') + '…'));
  } else {
    stage.replaceChildren(el('div', { class: 'empty' }, icon.wave(), t('voice.empty')));
  }

  const controls = controlsPanel(
    fg(t('voice.text'), el('textarea', {
      class: 'input', rows: 6, style: 'min-height:128px', maxlength: 5000,
      placeholder: 'Welcome to gpuless…',
      oninput: (e) => { voiceForm.text = e.target.value; },
    }, voiceForm.text), voiceForm.text.length + ' / 5000'),
    fg(t('voice.voice'), el('input', {
      class: 'input mono', value: voiceForm.speaker,
      oninput: (e) => { voiceForm.speaker = e.target.value; },
    }), t('voice.voiceHint')),
    fg(t('voice.language'), seg('vLang', S.strings ? langOptions() : [], voiceForm.language,
      (v) => { voiceForm.language = v; draw(); })),
    fg(t('voice.speed'), el('input', {
      class: 'input mono', type: 'number', min: 0.5, max: 2, step: 0.1, value: voiceForm.speed,
      oninput: (e) => { voiceForm.speed = +e.target.value; },
    })),
    el('button', {
      class: 'btn primary lg', disabled: blocked(),
      onclick: (e) => busy(e.currentTarget, generateVoice),
    }, icon.spark(), t('voice.speak')),
    blockedNote() || coldHint());

  $('#view').replaceChildren(el('div', { class: 'page' },
    el('div', { class: 'two-col' },
      el('section', { style: 'display:flex;flex-direction:column;gap:16px;min-width:0' }, stage, head),
      controls)));
}

function langOptions() {
  return (status && status.panel.languages ? status.panel.languages : [{ code: 'en', name: 'English' }])
    .map((l) => [l.code, l.name]);
}

async function generateVoice() {
  const run = await api('POST', '/api/generate/voice', {
    prompt: voiceForm.text,
    language: voiceForm.language,
    speaker: voiceForm.speaker,
    speed: voiceForm.speed,
  });
  selected.voice = null;
  await refresh();
  follow(run.run, 'voice');
}

/* ---- shared run helpers --------------------------------------------- */

function blocked() { return !!(status && status.kernel.blocked); }

function blockedNote() {
  if (!status) return null;
  if (status.kernel.blocked) return el('div', { class: 'note err note-row' }, icon.warn(), el('span', {}, t('quota.blocked')));
  if (status.kernel.warning) return el('div', { class: 'note warn note-row' }, icon.warn(), el('span', {}, t('quota.warning')));
  return null;
}

/** follow polls one run until it settles, so the stage updates by itself. */
function follow(id, kind) {
  const started = Date.now();
  const tick = async () => {
    if (Date.now() - started > 30 * 60 * 1000) return;
    try {
      const run = await api('GET', '/api/runs/' + id);
      if (run.status === 'running') { setTimeout(tick, 2000); return; }
      await refresh();
      if (run.status === 'failed') toast(run.error, true);
      else { selected[kind] = run; draw(); }
    } catch (e) { toast(e.message, true); }
  };
  setTimeout(tick, 1500);
}

/* ---- settings -------------------------------------------------------- */

function drawSettings() {
  if (!settings) {
    api('GET', '/api/settings').then((s) => { settings = s; drawSettings(); })
      .catch((e) => toast(e.message, true));
    $('#view').replaceChildren(el('div', { class: 'page' }, el('p', { class: 'muted' }, '…')));
    return;
  }
  const s = settings;
  const patch = {};
  const bind = (key) => (e) => { patch[key] = e.target.type === 'number' ? +e.target.value : e.target.value; };
  const toggle = (key, initial) => {
    const b = el('button', { class: 'switch' + (initial ? ' on' : ''), type: 'button' });
    b.addEventListener('click', () => {
      const on = !b.classList.contains('on');
      b.classList.toggle('on', on);
      patch[key] = on;
    });
    return b;
  };

  const save = async (e) => {
    await busy(e.currentTarget, async () => {
      await api('POST', '/api/settings', patch);
      settings = await api('GET', '/api/settings');
      toast(t('common.saved'));
      await refresh();
    });
  };

  const card = (id, title, badge, sub, body, foot) => el('section', { class: 'scard', id },
    el('div', { class: 'scard-body' },
      el('h2', {}, title, badge), el('p', { class: 'sub' }, sub), body),
    el('div', { class: 'scard-foot' }, ...foot));

  const k = status ? status.kernel : { used_hours: 0, quota_hours: s.weekly_quota_hours, resets_at: null };
  const pct = k.quota_hours ? Math.min(100, (k.used_hours / k.quota_hours) * 100) : 0;

  const cards = [
    card('kaggle', t('settings.kaggle'),
      el('span', { class: 'badge ' + (s.kaggle_key_set ? 'ok' : 'warn') }, el('i', { class: 'dot ' + (s.kaggle_key_set ? 'ok' : 'warn') }),
        s.kaggle_key_set ? t('settings.connected') : t('settings.notSet')),
      t('settings.kaggleSub'),
      el('div', { class: 'fields' },
        el('label', {}, t('setup.username'), el('input', { class: 'input mono', value: s.kaggle_username, oninput: bind('kaggle_username') })),
        el('label', {}, t('setup.apiToken'), el('input', { class: 'input mono', type: 'password', placeholder: s.kaggle_key_set ? '••••••••••••' : '', oninput: bind('kaggle_key') })),
        el('label', {}, t('setup.accel'),
          el('select', { class: 'input', oninput: bind('accelerator') },
            el('option', { value: 'T4x2', selected: s.accelerator === 'T4x2' }, 'T4 ×2'),
            el('option', { value: 'P100', selected: s.accelerator === 'P100' }, 'P100')))),
      [el('button', { class: 'btn primary', onclick: save }, t('common.save')),
       el('span', { class: 'right small dim' }, t('settings.keepSecret'))]),

    card('tunnel', t('settings.tunnel'), null,
      t('settings.tunnelSub'),
      el('div', { class: 'fields' },
        el('label', {}, t('settings.hostname'), el('input', { class: 'input mono', value: s.tunnel_host, oninput: bind('tunnel_host') })),
        el('label', {}, t('setup.tunnelToken'), el('input', { class: 'input mono', type: 'password', placeholder: s.tunnel_token_set ? '••••••••••••' : '', oninput: bind('tunnel_token') }))),
      [el('button', { class: 'btn primary', onclick: save }, t('common.save'))]),

    card('budget', t('settings.budget'), null,
      t('settings.budgetSub'),
      el('div', {},
        el('div', { class: 'usage' },
          el('div', { class: 'top' },
            el('b', {}, k.used_hours.toFixed(1)),
            el('span', { class: 'muted' }, t('settings.usedWeek', { n: k.quota_hours.toFixed(0) })),
            el('span', { class: 'right small dim' }, k.resets_at ? t('quota.resets', { when: when(k.resets_at) }) : '')),
          el('div', { class: 'bar' },
            el('i', { style: 'flex:' + Math.max(pct, 0.5) + ';background:var(--ok)' }),
            el('i', { style: 'flex:' + Math.max(100 - pct, 0.5) + ';background:var(--line)' }))),
        el('div', { class: 'fields' },
          el('label', {}, t('settings.quotaHours'), el('input', { class: 'input mono', type: 'number', min: 1, max: 200, value: s.weekly_quota_hours, oninput: bind('weekly_quota_hours') })),
          el('label', {}, t('settings.warnAt'), el('input', { class: 'input mono', type: 'number', min: 1, max: 100, value: s.quota_warn_pct, oninput: bind('quota_warn_pct') })),
          el('label', {}, t('settings.refuseAt'), el('input', { class: 'input mono', type: 'number', min: 1, max: 100, value: s.quota_block_pct, oninput: bind('quota_block_pct') })))),
      [el('button', { class: 'btn primary', onclick: save }, t('common.save'))]),

    card('kernel', t('settings.kernel'), null,
      t('settings.kernelSub'),
      el('div', {},
        el('div', { class: 'toggle-line' }, toggle('warm_on_visit', s.warm_on_visit),
          el('div', { class: 'grow' }, el('div', { class: 't' }, t('settings.warmVisit')),
            el('div', { class: 's' }, t('settings.warmSub')))),
        el('div', { class: 'toggle-line' }, toggle('keep_alive', s.keep_alive),
          el('div', { class: 'grow' }, el('div', { class: 't' }, t('settings.keepAlive')),
            el('div', { class: 's' }, t('settings.keepSub')))),
        el('div', { class: 'fields' },
          el('label', {}, t('settings.idleStop') + ' (' + t('common.minutes') + ')',
            el('input', { class: 'input mono', type: 'number', min: 1, max: 120, value: s.idle_stop_minutes, oninput: bind('idle_stop_minutes') })),
          el('label', {}, t('settings.hardLimit') + ' (' + t('common.hours') + ')',
            el('input', { class: 'input mono', type: 'number', min: 1, max: 12, value: s.session_limit_hours, oninput: bind('session_limit_hours') })))),
      [el('button', { class: 'btn primary', onclick: save }, t('common.save'))]),

    card('models', t('settings.models'), null,
      t('settings.modelsSub'),
      el('div', { class: 'fields' },
        el('label', {}, t('setup.runtime'), el('input', { class: 'input mono', value: s.dataset_runtime, oninput: bind('dataset_runtime') })),
        el('label', {}, t('setup.imageModel'), el('input', { class: 'input mono', value: s.dataset_sdxl, oninput: bind('dataset_sdxl') })),
        el('label', {}, t('setup.voiceModel'), el('input', { class: 'input mono', value: s.dataset_xtts, oninput: bind('dataset_xtts') }))),
      [el('button', { class: 'btn primary', onclick: save }, t('common.save')),
       el('span', { class: 'right small dim' }, t('settings.workflows', { list: s.workflows.join(', ') }))]),

    card('language', t('settings.language'), null,
      t('settings.languageSub'),
      el('div', {},
        el('div', { class: 'fields' },
          el('label', {}, t('settings.panelLang'),
            el('select', { class: 'input', oninput: bind('language') },
              langOptions().map(([c, n]) => el('option', { value: c, selected: s.language === c }, n))))),
        el('div', { class: 'toggle-line' }, toggle('follow_browser', s.follow_browser),
          el('div', { class: 'grow' }, el('div', { class: 't' }, t('settings.follow')),
            el('div', { class: 's' }, t('settings.followSub'))))),
      [el('button', { class: 'btn primary', onclick: save }, t('common.save'))]),
  ];

  const nav = el('div', { class: 'side-nav' }, el('h1', {}, t('nav.settings')),
    [['kaggle', t('settings.kaggle')], ['tunnel', t('settings.tunnel')], ['budget', t('settings.budget')],
     ['kernel', t('settings.kernel')], ['models', t('settings.models')], ['language', t('settings.language')]]
      .map(([id, label]) => el('a', {
        onclick: () => document.getElementById(id).scrollIntoView({ behavior: 'smooth', block: 'start' }),
      }, label)));

  $('#view').replaceChildren(el('div', { class: 'page narrow' },
    el('div', { class: 'settings' }, nav, el('div', { class: 'cards' }, ...cards))));
}

/* ------------------------------------------------------------------ poller */

async function refresh() {
  try {
    status = await api('GET', '/api/status');
    const wanted = [];
    if (CAPS.Image) wanted.push('image');
    if (CAPS.Voice) wanted.push('voice');
    for (const kind of wanted) {
      runs[kind] = (await api('GET', '/api/runs?kind=' + kind + '&limit=24')).runs;
    }
  } catch (e) {
    if (!String(e.message).includes('sign in')) toast(e.message, true);
    return;
  }
  drawKernelBar();
  if (view !== 'settings') draw();
}

function schedule() {
  clearInterval(pollTimer);
  // The kernel bar counts down, so it wants a faster tick than the run list.
  pollTimer = setInterval(async () => {
    if (document.hidden) return;
    await refresh();
  }, 5000);
}

drawTabs();
refresh().then(draw);
schedule();
window.addEventListener('hashchange', () => go(location.hash.replace('#', '') || 'image'));
