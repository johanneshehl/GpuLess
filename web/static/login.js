$('#mark').append(icon.mark(22));
wireLanguagePicker(document);

$('#loginForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const form = e.target;
  const button = $('button[type=submit]', form);
  await busy(button, async () => {
    await api('POST', '/api/login', {
      email: form.email.value.trim(),
      password: form.password.value,
      remember: form.remember.checked,
    });
    location.href = '/';
  }).catch(() => { form.password.select(); });
});
