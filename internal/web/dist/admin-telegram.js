// External communication page: Telegram bot credentials, thresholds and the
// per-event message templates.
(() => {
  const { ready, request, status } = window.VibeAdmin;
  const $ = id => document.getElementById(id);

  const TEMPLATE_FIELDS = {
    offline: 'telegramTemplateOffline',
    recovery: 'telegramTemplateRecovery',
    traffic_warning: 'telegramTemplateTrafficWarning',
    cpu: 'telegramTemplateCPU',
    memory: 'telegramTemplateMemory',
    traffic: 'telegramTemplateTraffic',
    due: 'telegramTemplateDue'
  };
  let defaultTemplates = {};

  async function load() {
    const telegram = await request('/api/admin/telegram');
    $('telegramEnabled').checked = !!telegram.enabled;
    $('telegramChatID').value = telegram.chat_id || '';
    $('telegramOfflineDelay').value = telegram.offline_delay_seconds ?? 60;
    $('telegramReminderDays').value = telegram.reminder_days ?? 7;
    $('telegramReminderHour').value = telegram.reminder_hour ?? 9;
    $('telegramReminderTimezone').value = telegram.reminder_timezone || 'Asia/Shanghai';
    defaultTemplates = telegram.default_templates || {};
    for (const [kind, id] of Object.entries(TEMPLATE_FIELDS)) {
      $(id).value = (telegram.templates && telegram.templates[kind]) || defaultTemplates[kind] || '';
    }
    const token = $('telegramBotToken');
    token.value = '';
    token.placeholder = telegram.token_configured ? '已配置；留空保留原 Token' : '填写 BotFather 提供的 Token';
  }

  async function save(event) {
    event.preventDefault();
    const numbers = ['telegramOfflineDelay', 'telegramReminderDays', 'telegramReminderHour'];
    if (numbers.some(id => $(id).value.trim() === '')) {
      status('请填写离线等待时间和到期提醒设置', 'error');
      return;
    }
    const offlineDelay = Number($('telegramOfflineDelay').value);
    const reminderDays = Number($('telegramReminderDays').value);
    const reminderHour = Number($('telegramReminderHour').value);
    if (!Number.isInteger(offlineDelay) || offlineDelay < 0 || offlineDelay > 3600 ||
        !Number.isInteger(reminderDays) || reminderDays < 0 || reminderDays > 7 ||
        !Number.isInteger(reminderHour) || reminderHour < 0 || reminderHour > 23) {
      status('请检查离线等待时间和到期提醒设置', 'error');
      return;
    }
    const submit = $('telegramForm').querySelector('button[type="submit"]');
    submit.disabled = true;
    $('telegramStatus').textContent = '正在保存…';
    try {
      await request('/api/admin/telegram', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          enabled: $('telegramEnabled').checked,
          bot_token: $('telegramBotToken').value.trim(),
          chat_id: $('telegramChatID').value.trim(),
          offline_delay_seconds: offlineDelay,
          reminder_days: reminderDays,
          reminder_hour: reminderHour,
          reminder_timezone: $('telegramReminderTimezone').value.trim(),
          // An untouched template falls back to the server-side default.
          templates: Object.fromEntries(Object.entries(TEMPLATE_FIELDS).map(([kind, id]) => {
            const value = $(id).value;
            return [kind, value === defaultTemplates[kind] ? '' : value];
          }))
        })
      });
      $('telegramStatus').textContent = '';
      await load();
      status('Telegram 配置已保存', 'success');
    } catch (error) {
      $('telegramStatus').textContent = '';
      status(error.message, 'error');
    } finally {
      submit.disabled = false;
    }
  }

  function wire() {
    $('telegramForm').addEventListener('submit', save);

    $('resetTelegramTemplatesBtn').addEventListener('click', () => {
      for (const [kind, id] of Object.entries(TEMPLATE_FIELDS)) {
        $(id).value = defaultTemplates[kind] || '';
      }
      $('telegramStatus').textContent = '已填入默认文案，点击保存后生效。';
    });

    $('testTelegramBtn').addEventListener('click', async () => {
      const btn = $('testTelegramBtn');
      btn.disabled = true;
      try {
        await request('/api/admin/telegram/test', { method: 'POST' });
        status('测试消息已发送', 'success');
      } catch (error) {
        status(error.message, 'error');
      } finally {
        btn.disabled = false;
      }
    });

    $('clearTelegramBtn').addEventListener('click', async () => {
      if (!confirm('清除 Telegram Bot Token、Chat ID 并关闭告警？')) return;
      try {
        await request('/api/admin/telegram', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ clear: true })
        });
        await load();
        status('Telegram 配置已清除', 'success');
      } catch (error) {
        status(error.message, 'error');
      }
    });
  }

  ready(async () => {
    wire();
    await load();
  });
})();
