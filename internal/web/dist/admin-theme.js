// Theme management page: list, preview, upload, activate and delete themes.
(() => {
  const { ready, request, status, applyTheme, syncChrome } = window.VibeAdmin;
  const $ = id => document.getElementById(id);
  let active = 'design';
  let choice = 'design';
  let busy = false;

  async function load(preferred) {
    const data = await request('/api/admin/themes');
    active = data.selected || 'design';
    applyTheme(active);
    choice = preferred || active;
    const list = $('themeList');
    list.replaceChildren();
    for (const theme of data.themes || []) {
      const row = document.createElement('div');
      row.className = 'theme-row';
      const label = document.createElement('label');
      label.className = 'theme-option';
      const radio = document.createElement('input');
      radio.type = 'radio';
      radio.name = 'themeChoice';
      radio.value = theme.id;
      radio.checked = theme.id === choice;
      radio.addEventListener('change', () => {
        choice = theme.id;
        // Selecting a theme previews it here; enabling publishes it to visitors.
        applyTheme(theme.id);
        syncChrome();
        $('themeStatus').textContent = '正在预览「' + theme.name + '」，点击“启用所选主题”后对所有访客生效。';
      });
      const details = document.createElement('span');
      const title = document.createElement('strong');
      title.textContent = theme.name + (theme.id === active ? ' · 使用中' : '');
      const description = document.createElement('small');
      description.textContent = [theme.version, theme.description].filter(Boolean).join(' · ');
      details.append(title, description);
      label.append(radio, details);
      row.append(label);
      if (!theme.builtin) {
        const remove = document.createElement('button');
        remove.type = 'button';
        remove.className = 'btn';
        remove.textContent = '删除';
        remove.disabled = busy;
        remove.addEventListener('click', () => {
          if (!confirm('删除“' + theme.name + '”？正在使用的主题会恢复默认。')) return;
          run(async () => {
            await request('/api/admin/themes/' + encodeURIComponent(theme.id), { method: 'DELETE' });
            await load();
            status('主题已删除', 'success');
          });
        });
        row.append(remove);
      }
      list.append(row);
    }
  }

  async function run(action) {
    if (busy) return;
    busy = true;
    const activate = $('activateThemeBtn');
    const file = $('themePackageFile');
    activate.disabled = true;
    file.disabled = true;
    $('themeList').querySelectorAll('button,input').forEach(el => { el.disabled = true; });
    $('themeStatus').textContent = '正在处理…';
    try {
      await action();
    } catch (error) {
      $('themeStatus').textContent = error.message;
    } finally {
      busy = false;
      activate.disabled = false;
      file.disabled = false;
      $('themeList').querySelectorAll('button,input').forEach(el => { el.disabled = false; });
    }
  }

  function wire() {
    $('activateThemeBtn').addEventListener('click', () => run(async () => {
      await request('/api/admin/themes/select', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: choice })
      });
      await load();
      applyTheme(choice);
      $('themeStatus').textContent = '主题已启用';
      status('主题已启用', 'success');
    }));

    $('themePackageFile').addEventListener('change', () => {
      const file = $('themePackageFile');
      const selected = file.files[0];
      if (!selected) return;
      if (selected.size > 2 * 1024 * 1024) {
        $('themeStatus').textContent = '主题包最大 2MB';
        file.value = '';
        return;
      }
      run(async () => {
        const body = new FormData();
        body.append('file', selected);
        let theme;
        try {
          theme = await request('/api/admin/themes', { method: 'POST', body });
        } finally {
          file.value = '';
        }
        await load(theme.id);
        $('themeStatus').textContent = '主题已上传，点击“启用所选主题”即可使用';
      });
    });
  }

  ready(async () => {
    wire();
    await load();
  });
})();
