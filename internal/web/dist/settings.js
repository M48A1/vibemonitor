async function fetchBasicSettingsForAdmin() {
  const submit = document.querySelector('#settingsForm button[type="submit"]');
  submit.disabled = true;
  try {
    const res = await fetch('/api/public');
    if (!res.ok) throw new Error('无法读取网站设置，请关闭后重试');
    const data = await res.json();
    const titleInput = document.getElementById('settingSiteTitle');
    if (titleInput) titleInput.value = data.site_title || '';
    const pwInput = document.getElementById('settingNewPassword');
    if (pwInput) pwInput.value = '';
    updateSiteIconDisplay(data.site_icon || '');
    const status = document.getElementById('logoUploadStatus');
    if (status) status.textContent = '';
  } catch (e) { alert(e.message); }
  finally { submit.disabled = false; }
}

const telegramTemplateFields = {
  offline: 'telegramTemplateOffline', recovery: 'telegramTemplateRecovery',
  traffic_warning: 'telegramTemplateTrafficWarning',
  cpu: 'telegramTemplateCPU', memory: 'telegramTemplateMemory',
  traffic: 'telegramTemplateTraffic', due: 'telegramTemplateDue'
};
let telegramDefaultTemplates = {};

async function fetchExternalSettingsForAdmin() {
  try {
    const res = await fetch('/api/admin/telegram', { credentials: 'same-origin' });
    if (!res.ok) throw new Error('无法读取 Telegram 配置，请关闭后重试');
    const telegram = await res.json();
    document.getElementById('telegramEnabled').checked = !!telegram.enabled;
    document.getElementById('telegramChatID').value = telegram.chat_id || '';
    document.getElementById('telegramOfflineDelay').value = telegram.offline_delay_seconds ?? 60;
    document.getElementById('telegramReminderDays').value = telegram.reminder_days ?? 7;
    document.getElementById('telegramReminderHour').value = telegram.reminder_hour ?? 9;
    document.getElementById('telegramReminderTimezone').value = telegram.reminder_timezone || 'Asia/Shanghai';
    telegramDefaultTemplates = telegram.default_templates || {};
    for (const [kind, id] of Object.entries(telegramTemplateFields)) {
      document.getElementById(id).value = telegram.templates?.[kind] || telegramDefaultTemplates[kind] || '';
    }
    const tokenInput = document.getElementById('telegramBotToken');
    tokenInput.value = '';
    tokenInput.placeholder = telegram.token_configured ? '已配置；留空保留原 Token' : '填写 BotFather 提供的 Token';
    return true;
  } catch (e) { alert(e.message); return false; }
}

document.getElementById('resetTelegramTemplatesBtn').addEventListener('click', () => {
  for (const [kind, id] of Object.entries(telegramTemplateFields)) {
    document.getElementById(id).value = telegramDefaultTemplates[kind] || '';
  }
});

document.getElementById('saveTelegramBtn').addEventListener('click', async () => {
  const button = document.getElementById('saveTelegramBtn');
  button.disabled = true;
  try {
    if (['telegramOfflineDelay', 'telegramReminderDays', 'telegramReminderHour'].some(id => document.getElementById(id).value.trim() === '')) {
      throw new Error('请填写离线等待时间和到期提醒设置');
    }
    const offlineDelay = Number(document.getElementById('telegramOfflineDelay').value);
    const reminderDays = Number(document.getElementById('telegramReminderDays').value);
    const reminderHour = Number(document.getElementById('telegramReminderHour').value);
    if (!Number.isInteger(offlineDelay) || offlineDelay < 0 || offlineDelay > 3600 ||
        !Number.isInteger(reminderDays) || reminderDays < 0 || reminderDays > 7 ||
        !Number.isInteger(reminderHour) || reminderHour < 0 || reminderHour > 23) {
      throw new Error('请检查离线等待时间和到期提醒设置');
    }
    const res = await fetch('/api/admin/telegram', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        enabled: document.getElementById('telegramEnabled').checked,
        bot_token: document.getElementById('telegramBotToken').value.trim(),
        chat_id: document.getElementById('telegramChatID').value.trim(),
        offline_delay_seconds: offlineDelay,
        reminder_days: reminderDays,
        reminder_hour: reminderHour,
        reminder_timezone: document.getElementById('telegramReminderTimezone').value.trim(),
        templates: Object.fromEntries(Object.entries(telegramTemplateFields).map(([kind, id]) => {
          const value = document.getElementById(id).value;
          return [kind, value === telegramDefaultTemplates[kind] ? '' : value];
        }))
      })
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || '保存失败');
    document.getElementById('telegramBotToken').value = '';
    document.getElementById('telegramBotToken').placeholder = '已配置；留空保留原 Token';
    alert('Telegram 配置已保存');
  } catch (e) { alert(e.message); }
  finally { button.disabled = false; }
});

document.getElementById('testTelegramBtn').addEventListener('click', async () => {
  const button = document.getElementById('testTelegramBtn');
  button.disabled = true;
  try {
    const res = await fetch('/api/admin/telegram/test', { method: 'POST', credentials: 'same-origin' });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || '发送失败');
    alert('测试消息已发送');
  } catch (e) { alert(e.message); }
  finally { button.disabled = false; }
});

document.getElementById('clearTelegramBtn').addEventListener('click', async () => {
  if (!confirm('清除 Telegram Bot Token、Chat ID 并关闭告警？')) return;
  try {
    const res = await fetch('/api/admin/telegram', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ clear: true })
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || '清除失败');
    document.getElementById('telegramEnabled').checked = false;
    document.getElementById('telegramChatID').value = '';
    document.getElementById('telegramBotToken').value = '';
    document.getElementById('telegramBotToken').placeholder = '填写 BotFather 提供的 Token';
    alert('Telegram 配置已清除');
  } catch (e) { alert(e.message); }
});

function openBasicSettings() {
  const spInput = document.getElementById('settingNewPassword');
  spInput.type = 'password';
  spInput.autocomplete = 'new-password';
  const spToggleBtn = document.querySelector('.btn-toggle-pwd[data-target="settingNewPassword"]');
  if (spToggleBtn) { spToggleBtn.textContent = '👁️'; spToggleBtn.title = '显示密码'; }
  openModal('settingsModal');
  fetchBasicSettingsForAdmin();
}

async function openExternalSettings() {
  if (await fetchExternalSettingsForAdmin()) openModal('externalSettingsModal');
}

document.getElementById('adminBtn').addEventListener('click', () => requireAdmin(openBasicSettings));
document.getElementById('externalSettingsBtn').addEventListener('click', () => requireAdmin(openExternalSettings));

// Favicon upload & reset button handling
const btnSelectLogo = document.getElementById('btnSelectLogo');
const fileInput = document.getElementById('settingSiteIconFile');
const btnResetLogo = document.getElementById('btnResetLogo');
const uploadStatus = document.getElementById('logoUploadStatus');

if (btnSelectLogo && fileInput) {
  btnSelectLogo.addEventListener('click', () => fileInput.click());
  fileInput.addEventListener('change', async () => {
    const file = fileInput.files[0];
    if (!file) return;
    if (file.size > 2 * 1024 * 1024) {
      alert('图片大小不能超过 2MB');
      fileInput.value = '';
      return;
    }
    if (uploadStatus) {
      uploadStatus.textContent = '正在上传...';
      uploadStatus.style.color = 'var(--muted-foreground)';
    }
    const formData = new FormData();
    formData.append('icon', file);
    try {
      const res = await fetch('/api/admin/upload-icon', {
        method: 'POST',
        credentials: 'same-origin',
        body: formData
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || '上传失败');
      updateSiteIconDisplay(data.url);
      if (uploadStatus) {
        uploadStatus.textContent = '网站图标已保存并更新';
        uploadStatus.style.color = 'var(--success)';
      }
    } catch (err) {
      if (uploadStatus) {
        uploadStatus.textContent = '上传失败: ' + err.message;
        uploadStatus.style.color = 'var(--destructive)';
      }
      alert('网站图标上传失败: ' + err.message);
    } finally {
      fileInput.value = '';
    }
  });
}

if (btnResetLogo) {
  btnResetLogo.addEventListener('click', async () => {
    if (!confirm('确定要恢复默认网站图标吗？')) return;
    try {
      const res = await fetch('/api/admin/delete-icon', {
        method: 'POST',
        credentials: 'same-origin'
      });
      if (!res.ok) throw new Error('操作失败');
      updateSiteIconDisplay('');
      if (uploadStatus) {
        uploadStatus.textContent = '已恢复默认图标';
        uploadStatus.style.color = 'var(--success)';
      }
    } catch (err) {
      alert('恢复默认图标失败: ' + err.message);
    }
  });
}

document.getElementById('settingsForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const newPassword = document.getElementById('settingNewPassword').value;
  if (newPassword && newPassword.length < 8) {
    alert('管理员密码至少需要 8 位');
    return;
  }
  try {
    const bodyPayload = {
      site_title: document.getElementById('settingSiteTitle').value.trim(),
      new_password: newPassword
    };
    const res = await fetch('/api/admin/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' }, credentials: 'same-origin',
      body: JSON.stringify(bodyPayload)
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || '保存失败');
    if (newPassword) {
      setAdminState(false);
    }
    closeModal('settingsModal');
    await fetchPublicSettings();
    alert(newPassword ? '设置已保存，请使用新密码重新登录' : '设置已保存');
  } catch (e) { alert(e.message); }
});

const siteMenu = document.getElementById('siteMenu');
const siteMenuTrigger = document.getElementById('siteMenuTrigger');
const nodeManagementMenu = document.getElementById('nodeManagementMenu');
function closeSiteMenu() {
  siteMenu.open = false;
  nodeManagementMenu.open = false;
}
siteMenu.addEventListener('toggle', () => {
  if (!siteMenu.open) nodeManagementMenu.open = false;
});
siteMenu.addEventListener('click', (event) => {
  if (event.target.closest('button')) {
    closeSiteMenu();
    siteMenuTrigger.focus();
  }
});
siteMenu.addEventListener('keydown', (event) => {
  if (event.key === 'Escape') {
    closeSiteMenu();
    siteMenuTrigger.focus();
  }
});
siteMenu.addEventListener('focusout', (event) => {
  // Safari can leave relatedTarget null until a menu button's click runs.
  if (event.relatedTarget && !siteMenu.contains(event.relatedTarget)) closeSiteMenu();
});
document.addEventListener('click', (event) => {
  if (!siteMenu.contains(event.target)) closeSiteMenu();
});
document.getElementById('editExistingNodeBtn').addEventListener('click', () => {
  nodeManagementMenu.open = false;
  requireAdmin(() => {
    const list = document.getElementById('nodeSelectionList');
    list.innerHTML = '';
    if (nodes.length === 0) {
      list.textContent = '暂无节点，请先通过“节点管理 → 新建节点”创建。';
    }
    [...nodes].sort(compareNodes).forEach(node => {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'btn';
      button.textContent = node.name || node.uuid;
      button.addEventListener('click', async () => {
        closeModal('selectNodeModal');
        await openEditModal(node.uuid);
      });
      list.appendChild(button);
    });
    openModal('selectNodeModal');
  });
});

const addNodeBtn = document.getElementById('addNodeBtn');
if (addNodeBtn) addNodeBtn.addEventListener('click', () => {
  nodeManagementMenu.open = false;
  requireAdmin(() => {
    document.getElementById('newNodeName').value = '';
    document.getElementById('newNodeTrafficLimit').value = '';
    document.getElementById('newNodeResetDay').value = '';
    document.getElementById('newNodeInitialUsed').value = '';
    fillNodeProfile('new', {});
    openModal('addNodeModal');
  });
});

