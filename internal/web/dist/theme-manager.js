(() => {
  let active = 'design', choice = 'design', busy = false;
  const status = document.getElementById('themeStatus');
  const list = document.getElementById('themeList');
  const activate = document.getElementById('activateThemeBtn');
  const file = document.getElementById('themePackageFile');
  const recovery = new URLSearchParams(location.search).get('theme') === 'default';
  // Keep the mobile browser chrome in step with the active theme background.
  function syncThemeColor() {
    const meta = document.querySelector('meta[name="theme-color"]');
    if (!meta) return;
    const color = getComputedStyle(document.body).backgroundColor;
    if (color && color !== 'rgba(0, 0, 0, 0)' && color !== 'transparent') meta.setAttribute('content', color);
  }
  // Keep the data-theme hook truthful so third-party theme CSS can branch on it.
  function syncThemeMode() {
    const match = /rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/.exec(getComputedStyle(document.body).backgroundColor || '');
    if (!match) return;
    const linear = (value) => { const c = Number(value) / 255; return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
    const luminance = 0.2126 * linear(match[1]) + 0.7152 * linear(match[2]) + 0.0722 * linear(match[3]);
    document.documentElement.dataset.theme = luminance > 0.4 ? 'light' : 'dark';
  }
  function syncChrome() { syncThemeColor(); syncThemeMode(); }
  // A theme may follow prefers-color-scheme, so re-read the colors when it flips.
  const colorSchemeQuery = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)');
  if (colorSchemeQuery && colorSchemeQuery.addEventListener) colorSchemeQuery.addEventListener('change', syncChrome);
  function setEffectiveTheme(id, layout = 'cards') {
    const root = document.documentElement;
    if (root.dataset.siteTheme === id && root.dataset.siteLayout === layout) return;
    root.dataset.siteTheme = id;
    root.dataset.siteLayout = layout;
    if (typeof window.scheduleDashboard === 'function') window.scheduleDashboard();
  }
  function apply(id) {
    active = id;
    const custom = !recovery && /^custom-[a-f0-9]{64}$/.test(id);
    const effective = custom ? id : 'design';
    const href = custom ? '/api/themes/' + id + '/style.css' : '/design.css';
    const current = document.getElementById('customThemeStylesheet');
    const activateStyle = () => {
      const layout = custom && getComputedStyle(document.documentElement).getPropertyValue('--vibemonitor-layout').trim() === 'serverstatus' ? 'serverstatus' : 'cards';
      setEffectiveTheme(effective, layout);
      syncChrome();
    };
    if (current && (current.getAttribute('href') || '').split('?')[0] === href) {
      if (current.sheet) activateStyle();
      return;
    }
    const link = document.createElement('link');
    link.id = 'customThemeStylesheet'; link.rel = 'stylesheet'; link.href = href;
    link.onload = () => {
      if (document.getElementById('customThemeStylesheet') === link) activateStyle();
    };
    link.onerror = () => {
      if (document.getElementById('customThemeStylesheet') !== link) return;
      link.remove();
      setEffectiveTheme('design');
      if (custom) apply('design');
      status.textContent = '主题加载失败，已显示默认外观';
    };
    if (current) current.remove();
    document.head.appendChild(link);
    if (!custom) setEffectiveTheme('design');
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
