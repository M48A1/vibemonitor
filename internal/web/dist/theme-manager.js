(() => {
  let active = 'hex', choice = 'hex', busy = false;
  const status = document.getElementById('themeStatus');
  const list = document.getElementById('themeList');
  const activate = document.getElementById('activateThemeBtn');
  const file = document.getElementById('themePackageFile');
  const recovery = new URLSearchParams(location.search).get('theme') === 'default';
  function apply(id) {
    active = id;
    const current = document.getElementById('customThemeStylesheet');
    const builtin = id === 'rakugaki' || id === 'win2000' || id === 'design';
    const valid = builtin || /^custom-[a-f0-9]{64}$/.test(id);
    if (recovery || !valid) { if (current) current.remove(); return; }
    const href = builtin ? '/' + id + '.css' : '/api/themes/' + id + '/style.css';
    if (current && current.getAttribute('href') === href) return;
    const link = document.createElement('link');
    link.id = 'customThemeStylesheet'; link.rel = 'stylesheet'; link.href = href;
    link.onerror = () => { link.remove(); status.textContent = '主题加载失败，已显示默认外观'; };
    if (current) current.remove();
    document.head.appendChild(link);
  }
  async function request(url, options = {}) {
    const response = await fetch(url, { credentials: 'same-origin', ...options });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || '操作失败，请重新登录后重试');
    return data;
  }
  async function load(preferred) {
    const data = await request('/api/admin/themes');
    apply(data.selected); choice = preferred || data.selected;
    list.replaceChildren();
    for (const theme of data.themes) {
      const row = document.createElement('div'); row.className = 'theme-row';
      const label = document.createElement('label'); label.className = 'theme-option';
      const radio = document.createElement('input'); radio.type = 'radio'; radio.name = 'themeChoice'; radio.value = theme.id; radio.checked = theme.id === choice;
      radio.addEventListener('change', () => { choice = theme.id; });
      const details = document.createElement('span');
      const title = document.createElement('strong'); title.textContent = theme.name + (theme.id === active ? ' · 使用中' : '');
      const description = document.createElement('small'); description.textContent = [theme.version, theme.description].filter(Boolean).join(' · ');
      details.append(title, description); label.append(radio, details); row.append(label);
      if (!theme.builtin) {
        const remove = document.createElement('button'); remove.type = 'button'; remove.className = 'btn'; remove.textContent = '删除'; remove.disabled = busy;
        remove.addEventListener('click', () => {
          if (!confirm('删除“' + theme.name + '”？正在使用的主题会恢复默认。')) return;
          run(async () => { await request('/api/admin/themes/' + theme.id, { method: 'DELETE' }); await load(); status.textContent = '主题已删除'; });
        }); row.append(remove);
      }
      list.append(row);
    }
  }
  async function run(action) {
    if (busy) return; busy = true; activate.disabled = true; file.disabled = true;
    list.querySelectorAll('button,input').forEach(el => { el.disabled = true; });
    status.textContent = '正在处理…';
    try { await action(); } catch (error) { status.textContent = error.message; }
    finally { busy = false; activate.disabled = false; file.disabled = false; list.querySelectorAll('button,input').forEach(el => { el.disabled = false; }); }
  }
  document.getElementById('themeManagementBtn').addEventListener('click', () => requireAdmin(() => {
    openModal('themeManagementModal');
    run(async () => { await load(); status.textContent = recovery ? '当前以默认外观打开，启用主题后移除网址中的 ?theme=default 即可查看。' : ''; });
  }));
  activate.addEventListener('click', () => run(async () => {
    await request('/api/admin/themes/select', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ id: choice }) });
    await load(); status.textContent = '主题已启用';
  }));
  file.addEventListener('change', () => {
    const selected = file.files[0]; if (!selected) return;
    if (selected.size > 2 * 1024 * 1024) { status.textContent = '主题包最大 2MB'; file.value = ''; return; }
    run(async () => { const body = new FormData(); body.append('file', selected); const theme = await request('/api/admin/themes', { method: 'POST', body }); await load(theme.id); status.textContent = '主题已上传，点击启用所选主题即可使用'; }).finally(() => { file.value = ''; });
  });
  window.VibeThemes = { apply };
})();
