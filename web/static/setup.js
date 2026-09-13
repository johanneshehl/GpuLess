/* The wizard. Each step posts to its own endpoint, so a half-finished setup
   keeps whatever it already got right and the operator can come back to it. */

$('#mark').append(icon.mark(34));
wireLanguagePicker(document);

const STEPS = ['admin', 'kaggle', 'tunnel', 'models'];
let step = 0;
let state = {};

const draw = {
  admin: () => [
    el('div', {},
      el('h2', {}, t('setup.adminTitle')),
      el('p', {}, t('setup.adminLead'))),
    field(t('login.email'), el('input', { class: 'input', type: 'email', id: 'email', autocomplete: 'username', placeholder: 'you@example.com' })),
    field(t('login.password'), el('input', { class: 'input', type: 'password', id: 'password', autocomplete: 'new-password' }), t('setup.pwHint')),
  ],

  kaggle: () => [
    el('div', {},
      el('h2', {}, t('setup.kaggleTitle')),
      el('p', { html: t('setup.kaggleLead') })),
    codeBox(t('setup.kaggleWhere'), t('setup.kaggleSteps')),
    el('div', { class: 'grid2' },
      field(t('setup.username'), el('input', { class: 'input mono', id: 'kuser', autocomplete: 'off', placeholder: 'jhehl' })),
      field(t('setup.apiToken'), el('input', { class: 'input mono', id: 'kkey', type: 'password', autocomplete: 'off' }))),
    field(t('setup.accel'), select('accel', [
      ['T4x2', t('setup.accelBest')],
      ['P100', 'P100'],
    ]), t('setup.accelHint')),
  ],

  tunnel: () => {
    // The instructions show the hostname as it is typed, so it is clear which
    // value goes into which Cloudflare field.
    const box = codeBox(t('setup.tunnelWhere'), tunnelSteps(''));
    return [
      el('div', {},
        el('h2', {}, t('setup.tunnelTitle')),
        el('p', {}, t('setup.tunnelLead'))),
      box,
      el('div', { class: 'grid2' },
        field(t('setup.host'), el('input', {
          class: 'input mono', id: 'thost', placeholder: 'gpu.example.com', autocomplete: 'off',
          oninput: (e) => { $('pre', box).innerHTML = tunnelSteps(e.target.value); },
        }), t('setup.hostHint')),
        field(t('setup.tunnelToken'), el('input', { class: 'input mono', id: 'ttoken', type: 'password', autocomplete: 'off' }))),
      el('div', { class: 'note warn note-row' }, icon.warn(),
        el('span', {}, t('setup.tunnelWarn'))),
    ];
  },

  models: () => [
    el('div', {},
      el('h2', {}, t('setup.modelsTitle')),
      el('p', {}, t('setup.modelsLead'))),
    field(t('setup.runtime'), el('input', { class: 'input mono', id: 'dsrun', placeholder: 'you/gpuless-runtime', autocomplete: 'off' }),
      t('setup.runtimeHint')),
    el('div', { class: 'grid2' },
      field(t('setup.imageModel'), el('input', { class: 'input mono', id: 'dssdxl', placeholder: 'you/gpuless-sdxl', autocomplete: 'off' }), t('setup.imageHint')),
      field(t('setup.voiceModel'), el('input', { class: 'input mono', id: 'dsxtts', placeholder: 'you/gpuless-xtts', autocomplete: 'off' }), t('setup.voiceHint'))),
    el('p', { class: 'hint' }, t('setup.docs')),
  ],
};

const submit = {
  admin: () => api('POST', '/api/setup/admin', {
    email: $('#email').value.trim(), password: $('#password').value,
  }),
  kaggle: () => api('POST', '/api/setup/kaggle', {
    username: $('#kuser').value.trim(), key: $('#kkey').value.trim(), accelerator: $('#accel').value,
  }),
  tunnel: () => api('POST', '/api/setup/tunnel', {
    host: $('#thost').value.trim(), token: $('#ttoken').value.trim(),
  }),
  models: () => api('POST', '/api/setup/models', {
    runtime: $('#dsrun').value.trim(), sdxl: $('#dssdxl').value.trim(), xtts: $('#dsxtts').value.trim(),
  }),
};

function field(label, control, hint) {
  return el('label', { class: 'fg' },
    el('span', { class: 'lbl' }, label, hint ? el('span', {}, hint) : null), control);
}

function select(id, options) {
  return el('select', { class: 'input', id }, options.map(([v, l]) => el('option', { value: v }, l)));
}

function tunnelSteps(host) {
  const bare = host.trim().replace(/^https?:\/\//, '').replace(/\/.*$/, '');
  const safe = bare.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  return t('setup.tunnelSteps', { host: safe || 'gpu.example.com' });
}

function codeBox(head, html) {
  return el('div', { class: 'code-box' },
    el('div', { class: 'code-head' }, head),
    el('pre', { html }));
}

function render() {
  const name = STEPS[step];

  $('#steps').replaceChildren(...STEPS.map((s, i) => {
    const label = t('setup.' + s);
    return el('div', { class: i === step ? 'on' : '' },
      i < step ? mint(icon.check()) : el('span', { class: 'n' }, String(i + 1)), label);
  }));

  $('#body').replaceChildren(...draw[name]());
  $('#foot').replaceChildren(
    el('button', { class: 'btn', disabled: step === 0, onclick: back }, t('common.back')),
    el('span', { class: 'right small dim' }, t('setup.step', { n: step + 1 })),
    el('button', { class: 'btn primary', onclick: next },
      step === STEPS.length - 1 ? t('setup.finish') : t('common.continue')));

  const first = $('#body input');
  if (first) first.focus();
}

function mint(node) { node.style.color = 'var(--ok)'; return node; }

function back() { if (step > 0) { step--; render(); } }

async function next(e) {
  await busy(e.currentTarget, async () => {
    await submit[STEPS[step]]();
    if (step === STEPS.length - 1) { location.href = '/'; return; }
    step++;
    render();
  });
}

/* Resume where the operator left off rather than making them retype what the
   server already has. */
api('GET', '/api/setup').then((s) => {
  state = s;
  if (s.complete) { location.href = '/'; return; }
  if (s.has_admin) step = 1;
  if (s.has_kaggle) step = 2;
  if (s.has_tunnel) step = 3;
  render();
}).catch((e) => { toast(e.message, true); render(); });
