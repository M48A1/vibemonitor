// Basic settings page: site title, favicon and the admin password.
(() => {
  const { ready, request, status, updateIcon } = window.VibeAdmin;
  const $ = id => document.getElementById(id);
  const DEFAULT_ICON = ($('siteIcon') || { getAttribute: () => '' }).getAttribute('href') || '';

  function previewIcon(url) {
    updateIcon(url || '');
    const box = $('logoPreviewContent');
    if (!box) return;
    box.replaceChildren();
    const img = document.createElement('img');
    img.src = url || DEFAULT_ICON;
    img.alt = '网站图标预览';
    img.width = 28;
    img.height = 28;
    box.appendChild(img);
  }

  async function loadSettings() {
    const data = await request('/api/public');
    $('settingSiteTitle').value = data.site_title || '';
    $('settingNewPassword').value = '';
    $('logoUploadStatus').textContent = '';
    previewIcon(data.site_icon || '');
  }

  async function uploadIcon(file) {
    if (file.size > 2 * 1024 * 1024) {
      $('logoUploadStatus').textContent = '图片大小不能超过 2MB';
      return;
    }
    $('logoUploadStatus').textContent = '正在上传…';
    const body = new FormData();
    body.append('icon', file);
    const data = await request('/api/admin/upload-icon', { method: 'POST', body });
    previewIcon(data.url || '');
    $('logoUploadStatus').textContent = '网站图标已保存并更新';
  }

  async function saveSettings(event) {
    event.preventDefault();
    const password = $('settingNewPassword').value;
    if (password && password.length < 8) {
      status('管理员密码至少需要 8 位', 'error');
      return;
    }
    const submit = event.currentTarget.querySelector('button[type="submit"]');
    submit.disabled = true;
    $('settingsStatus').textContent = '正在保存…';
    try {
      await request('/api/admin/settings', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          site_title: $('settingSiteTitle').value.trim(),
          new_password: password
        })
      });
      $('settingsStatus').textContent = '';
      if (password) {
        // Saving a password clears every admin session, including this one.
        status('设置已保存，请使用新密码重新登录', 'success');
        setTimeout(() => location.reload(), 1500);
        return;
      }
      status('设置已保存', 'success');
      await loadSettings();
      const brand = $('adminBrandTitle');
      const title = $('settingSiteTitle').value.trim();
      if (brand && title) brand.textContent = title;
      if (title) document.title = title + ' · 基本设置 · 管理后台';
    } catch (error) {
      $('settingsStatus').textContent = '';
      status(error.message, 'error');
    } finally {
      submit.disabled = false;
    }
  }

  function wire() {
    $('btnSelectLogo').addEventListener('click', () => $('settingSiteIconFile').click());
    $('settingSiteIconFile').addEventListener('change', async (event) => {
      const file = event.target.files[0];
      event.target.value = '';
      if (!file) return;
      try {
        await uploadIcon(file);
        status('网站图标已更新', 'success');
      } catch (error) {
        $('logoUploadStatus').textContent = '上传失败：' + error.message;
        status(error.message, 'error');
      }
    });
    $('btnResetLogo').addEventListener('click', async () => {
      if (!confirm('确定要恢复默认网站图标吗？')) return;
      try {
        await request('/api/admin/delete-icon', { method: 'POST' });
        previewIcon('');
        $('logoUploadStatus').textContent = '已恢复默认图标';
      } catch (error) {
        status(error.message, 'error');
      }
    });
    $('settingsForm').addEventListener('submit', saveSettings);
  }

  ready(async () => {
    wire();
    await loadSettings();
  });
})();
