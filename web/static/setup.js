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
      el('h2', {}, 'Create the administrator'),
      el('p', {}, 'This account signs in to the panel. It is stored on your server and nowhere else.')),
    field('Email', el('input', { class: 'input', type: 'email', id: 'email', autocomplete: 'username', placeholder: 'you@example.com' })),
    field('Password', el('input', { class: 'input', type: 'password', id: 'password', autocomplete: 'new-password' }), 'at least 10 characters'),
  ],

  kaggle: () => [
    el('div', {},
      el('h2', {}, 'Connect your Kaggle account'),
      el('p', { html: "gpuless runs the models on Kaggle's free GPU time — 30 hours a week. It uses <b>your</b> account and nobody else's, and nothing is sent anywhere but Kaggle." })),
    codeBox('Where to find the token',
      'kaggle.com <i>→</i> Settings <i>→</i> API <i>→</i> <b>Create New Token</b>\nDownloads <b>kaggle.json</b> — it holds the two values below.'),
    el('div', { class: 'grid2' },
      field('Username', el('input', { class: 'input mono', id: 'kuser', autocomplete: 'off', placeholder: 'jhehl' })),
      field('API token', el('input', { class: 'input mono', id: 'kkey', type: 'password', autocomplete: 'off' }))),
    field('Preferred accelerator', select('accel', [
      ['T4x2', 'T4 ×2 — best for image and voice'],
      ['P100', 'P100'],
    ]), 'a request, not a guarantee — the panel shows what Kaggle actually gave you'),
  ],

  tunnel: () => [
    el('div', {},
      el('h2', {}, 'Give the kernel a way back'),
      el('p', {}, 'A Kaggle kernel accepts no incoming connections, so it dials out to a Cloudflare tunnel and this panel talks to that hostname. Your server never needs an open port.')),
    codeBox('Where to get the token',
      'one.dash.cloudflare.com <i>→</i> Networks <i>→</i> Tunnels <i>→</i> <b>Create a tunnel</b>\nRoute a public hostname to <b>http://localhost:8189</b>, then copy the token.'),
    el('div', { class: 'grid2' },
      field('Public hostname', el('input', { class: 'input mono', id: 'thost', placeholder: 'gpu.example.com', autocomplete: 'off' })),
      field('Tunnel token', el('input', { class: 'input mono', id: 'ttoken', type: 'password', autocomplete: 'off' }))),
    el('div', { class: 'note warn note-row' }, icon.warn(),
      el('span', {}, 'Kaggle has no way to hand a secret to a notebook over the API, so the token is written into the notebook source. gpuless always pushes it private — do not make that notebook public.')),
  ],

  models: () => [
    el('div', {},
      el('h2', {}, 'Point at your datasets'),
      el('p', {}, 'Kaggle runs its own notebook image, so gpuless cannot ship a container to it. The ComfyUI runtime and the model weights live as datasets on your account and are mounted at start — seconds instead of a multi-gigabyte download every time.')),
    field('Runtime dataset', el('input', { class: 'input mono', id: 'dsrun', placeholder: 'you/gpuless-runtime', autocomplete: 'off' }),
      'required — carries ComfyUI, its nodes and cloudflared'),
    el('div', { class: 'grid2' },
      field('Image model', el('input', { class: 'input mono', id: 'dssdxl', placeholder: 'you/gpuless-sdxl', autocomplete: 'off' }), 'leave empty to hide the Image tab'),
      field('Voice model', el('input', { class: 'input mono', id: 'dsxtts', placeholder: 'you/gpuless-xtts', autocomplete: 'off' }), 'leave empty to hide the Voice tab')),
    el('p', { class: 'hint' }, 'See docs/runtime-dataset.md in the repository for what goes in the runtime dataset and how to build it.'),
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
      step === STEPS.length - 1 ? 'Finish' : t('common.continue')));

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
