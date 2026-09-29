// VibeMonitor Modern Dashboard Script

let nodes = [];
let isAdmin = false;
let ws = null;
let wsHasData = false;
let lastWSDataAt = 0;
let pollTimer = null;
let lastNodeMarkup = '';
let lastNodeCards = [];
let lastPublicSettings = '';
let pendingAdminAction = null;

// Helpers: formatting
function formatBytes(bytes) {
  if (!bytes || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(1) + ' ' + units[i];
}

function formatSpeed(bytesPerSec) {
  return formatBytes(bytesPerSec) + '/s';
}

function formatUptime(seconds) {
  if (!seconds || seconds <= 0) return '0分';
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d}天 ${h}时`;
  if (h > 0) return `${h}时 ${m}分`;
  return `${m}分`;
}

function getRegionBadge(code) {
  if (!code) return '🌐 GLOBAL';
  code = code.toUpperCase();
  const flags = {
    JP: '🇯🇵 JP', US: '🇺🇸 US', CN: '🇨🇳 CN', HK: '🇭🇰 HK',
    SG: '🇸🇬 SG', TW: '🇹🇼 TW', KR: '🇰🇷 KR', DE: '🇩🇪 DE',
    GB: '🇬🇧 UK', FR: '🇫🇷 FR', CA: '🇨🇦 CA', RU: '🇷🇺 RU',
  };
  return flags[code] || `🌐 ${escapeHtml(code)}`;
}

function generateSparkline(history, field, width = 280, height = 28) {
  if (!history || history.length < 2) {
    return `<svg class="sparkline-box" viewBox="0 0 ${width} ${height}"><line x1="0" y1="${height/2}" x2="${width}" y2="${height/2}" stroke="rgba(255,255,255,0.1)" stroke-dasharray="3,3"/></svg>`;
  }
  const values = history.map(h => h[field] || 0);
  const max = Math.max(...values, 10);
  const min = 0;
  const pts = values.map((val, i) => {
    const x = (i / (values.length - 1)) * width;
    const y = height - ((val - min) / (max - min)) * (height - 4) - 2;
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  }).join(' ');

  return `<svg class="sparkline-box" viewBox="0 0 ${width} ${height}">
    <polyline fill="none" stroke="var(--primary)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" points="${pts}" />
  </svg>`;
}

// Modal handling
window.openModal = function(id) {
  const m = typeof id === 'string' ? document.getElementById(id) : id;
  if (!m || !m.classList) return;
  m.classList.add('active');
  if (typeof setTimeout === 'function') {
    setTimeout(() => {
      const focusable = typeof m.querySelector === 'function'
        ? m.querySelector('input:not([style*="display: none"]):not([style*="display:none"])')
        : null;
      if (focusable && typeof focusable.focus === 'function') focusable.focus();
    }, 50);
  }
};

function resetLoginPasswordFields() {
  const pwInput = document.getElementById('loginPassword');
  if (pwInput) { pwInput.type = 'text'; pwInput.value = ''; pwInput.autocomplete = 'off'; }
  const toggleBtn = document.querySelector('.btn-toggle-pwd[data-target="loginPassword"]');
  if (toggleBtn) { toggleBtn.textContent = '👁️'; toggleBtn.title = '显示密码'; }
  const unInput = document.getElementById('loginUsername');
  if (unInput) { unInput.autocomplete = 'off'; }
}

function resetSettingsPasswordFields() {
  const spInput = document.getElementById('settingNewPassword');
  if (spInput) { spInput.type = 'text'; spInput.value = ''; spInput.autocomplete = 'off'; }
  const toggleBtn = document.querySelector('.btn-toggle-pwd[data-target="settingNewPassword"]');
  if (toggleBtn) { toggleBtn.textContent = '👁️'; toggleBtn.title = '显示密码'; }
}

function resetPasswordFields() {
  resetLoginPasswordFields();
  resetSettingsPasswordFields();
}

window.closeModal = function(id) {
  if (!id) {
    const activeModals = typeof document.querySelectorAll === 'function'
      ? document.querySelectorAll('.modal-overlay.active')
      : [];
    for (let i = 0; i < activeModals.length; i++) {
      if (activeModals[i].classList && typeof activeModals[i].classList.remove === 'function') {
        activeModals[i].classList.remove('active');
      }
    }
    resetPasswordFields();
    return;
  }
  const m = typeof id === 'string' ? document.getElementById(id) : id;
  if (m && m.classList && typeof m.classList.remove === 'function') {
    m.classList.remove('active');
  }
  const modalId = (m && m.id) || id;
  if (modalId === 'loginModal') {
    resetLoginPasswordFields();
    pendingAdminAction = null;
  }
  if (modalId === 'settingsModal') {
    resetSettingsPasswordFields();
  }
  if (modalId === 'externalSettingsModal') {
    document.getElementById('telegramBotToken').value = '';
  }
};

// Bind modal overlay backdrop click to close
const modalOverlayIds = [
  'settingsModal',
  'externalSettingsModal',
  'loginModal',
  'selectNodeModal',
  'addNodeModal',
  'editNodeModal',
  'nodeGuideModal',
  'pingChartModal'
];
modalOverlayIds.forEach(id => {
  const overlay = document.getElementById(id);
  if (overlay && typeof overlay.addEventListener === 'function') {
    overlay.addEventListener('click', (e) => {
      if (e.target === overlay) {
        closeModal(id);
      }
    });
  }
});

// Bind all explicit close/cancel buttons
const modalCloseBtnBindings = [
  { id: 'closeSettingsModalBtn', modalId: 'settingsModal' },
  { id: 'cancelSettingsModalBtn', modalId: 'settingsModal' },
  { id: 'closeExternalSettingsModalBtn', modalId: 'externalSettingsModal' },
  { id: 'closeLoginModalBtn', modalId: 'loginModal' },
  { id: 'cancelLoginModalBtn', modalId: 'loginModal' },
  { id: 'closeSelectNodeModalBtn', modalId: 'selectNodeModal' },
  { id: 'closeSelectNodeBottomBtn', modalId: 'selectNodeModal' },
  { id: 'closeAddNodeModalBtn', modalId: 'addNodeModal' },
  { id: 'cancelAddNodeModalBtn', modalId: 'addNodeModal' },
  { id: 'closeEditNodeModalBtn', modalId: 'editNodeModal' },
  { id: 'cancelEditNodeModalBtn', modalId: 'editNodeModal' },
  { id: 'closeNodeGuideModalBtn', modalId: 'nodeGuideModal' },
  { id: 'closeNodeGuideBottomBtn', modalId: 'nodeGuideModal' },
  { id: 'closePingChartModalBtn', modalId: 'pingChartModal' },
  { id: 'closePingChartBottomBtn', modalId: 'pingChartModal' }
];
modalCloseBtnBindings.forEach(binding => {
  const btn = document.getElementById(binding.id);
  if (btn && typeof btn.addEventListener === 'function') {
    btn.addEventListener('click', () => closeModal(binding.modalId));
  }
});

// Guide command copy buttons
const guideCopyBtns = [
  { id: 'copyGuideInstallBtn', targetId: 'guideInstallCmd' },
  { id: 'copyGuideRunBtn', targetId: 'guideRunCmd' },
  { id: 'copyGuideUninstallBtn', targetId: 'guideUninstallCmd' }
];
guideCopyBtns.forEach(item => {
  const btn = document.getElementById(item.id);
  if (btn && typeof btn.addEventListener === 'function') {
    btn.addEventListener('click', () => copyGuideCommand(item.targetId));
  }
});

// Ping chart range switch buttons
['btnRange1h', 'btnRange24h', 'btnRange7d', 'btnRange31d'].forEach(id => {
  const btn = document.getElementById(id);
  if (btn && typeof btn.addEventListener === 'function') {
    const range = id === 'btnRange1h' ? '1h' : (id === 'btnRange24h' ? '24h' : (id === 'btnRange7d' ? '7d' : '31d'));
    btn.addEventListener('click', () => switchPingRange(range));
  }
});

// Global event delegation (backdrop click, Escape key, [data-close-modal])
if (typeof document.addEventListener === 'function') {
  document.addEventListener('click', (e) => {
    const trigger = e.target && typeof e.target.closest === 'function'
      ? e.target.closest('[data-close-modal], .modal-close')
      : null;
    if (trigger) {
      const modalId = (trigger.dataset && trigger.dataset.closeModal)
        ? trigger.dataset.closeModal
        : (trigger.closest && trigger.closest('.modal-overlay') ? trigger.closest('.modal-overlay').id : null);
      if (modalId) {
        closeModal(modalId);
      }
      return;
    }
    if (e.target && e.target.classList && typeof e.target.classList.contains === 'function' && e.target.classList.contains('modal-overlay')) {
      closeModal(e.target.id);
    }
  });

  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' || e.key === 'Esc') {
      const activeModals = typeof document.querySelectorAll === 'function'
        ? document.querySelectorAll('.modal-overlay.active')
        : [];
      for (let i = 0; i < activeModals.length; i++) {
        closeModal(activeModals[i].id);
      }
    }
  });
}

document.addEventListener('visibilitychange', () => {
  if (!document.hidden) fetchPublicSettings();
});

// Admin Authentication
function getAdminToken() {
  return '';
}

async function checkAdminAuth() {
  try {
    const res = await fetch('/api/admin/status', { credentials: 'same-origin' });
    const data = await res.json();
    setAdminState(data.is_admin === true);
  } catch (e) {
    setAdminState(false);
  }
}

function setAdminState(admin) {
  const changed = isAdmin !== admin;
  isAdmin = admin;
  const quickActions = document.getElementById('adminQuickActions');
  quickActions.style.display = admin ? 'block' : 'none';
  if (!admin) closeSiteMenu();
  if (changed && nodes.length) renderNodes();
}

// Global Stats Calculation
function updateGlobalStats() {
  VibeHex.renderOverview(nodes);
}

function readNodeOrder(prefix) {
  const text = document.getElementById(prefix + 'NodeOrder').value.trim();
  const order = text === '' ? 0 : Number(text);
  if (!Number.isSafeInteger(order) || order < 0) throw new Error('节点序号必须是非负整数');
  return order;
}

function readNodeProfile(prefix) {
  const thresholdText = document.getElementById(prefix+'NodeCPUThreshold').value.trim();
  const cpuThreshold = thresholdText === '' ? null : Number(thresholdText);
  if (cpuThreshold !== null && (!Number.isFinite(cpuThreshold) || cpuThreshold < 0 || cpuThreshold > 100)) {
    throw new Error('CPU 负载阈值必须在 0 到 100 之间');
  }
  const targets = document.getElementById(prefix + 'NodeTargets').value.split('\n').filter(l => l.trim()).map(line => {
    const comma = line.indexOf(',');
    const name = (comma < 0 ? line : line.slice(0, comma)).trim();
    let host = (comma < 0 ? line : line.slice(comma + 1)).trim();
    if (/^(tcp|https?):\/\//i.test(host)) {
      const url = new URL(host);
      if (url.username || url.password) throw new Error('测试链接不能包含账号密码');
      const port = url.port || (url.protocol === 'https:' ? '443' : url.protocol === 'http:' ? '80' : '');
      host = url.hostname + ':' + port;
    }
    if (!/^[a-zA-Z0-9.-]+:[0-9]+$/.test(host)) throw new Error('TCP 目标请填写地址:端口');
    return {name, host};
  });
  return {targets, due_date: document.getElementById(prefix+'NodeDue').value,
    payment_cycle: document.getElementById(prefix+'NodeCycle').value,
    price: Number(document.getElementById(prefix+'NodePrice').value || 0),
    currency: document.getElementById(prefix+'NodeCurrency').value,
    cpu_threshold: cpuThreshold,
    alerts_disabled: document.getElementById(prefix+'NodeAlertsDisabled').checked};
}
function fillNodeProfile(prefix, profile) {
  const p = profile || {};
  document.getElementById(prefix+'NodeTargets').value = (p.targets || []).map(t => `${t.name},${t.host}`).join('\n');
  document.getElementById(prefix+'NodeDue').value = p.due_date || '';
  document.getElementById(prefix+'NodeCycle').value = p.payment_cycle || '';
  document.getElementById(prefix+'NodePrice').value = p.price || '';
  document.getElementById(prefix+'NodeCurrency').value = p.currency || 'CNY';
  document.getElementById(prefix+'NodeCPUThreshold').value = p.cpu_threshold == null ? '' : p.cpu_threshold;
  document.getElementById(prefix+'NodeAlertsDisabled').checked = !!p.alerts_disabled;
}
function billingDisplay(profile) {
  const p = profile || {};
  const cycle = {month:'月',quarter:'季',year:'年'}[p.payment_cycle];
  const price = cycle ? `${escapeHtml(p.currency || 'CNY')} ${Number(p.price || 0).toFixed(2)} / ${cycle}` : '账单未设置';
  let remaining = '未设置到期日';
  if (/^\d{4}-\d{2}-\d{2}$/.test(p.due_date || '')) {
    const now = new Date();
    const today = Date.UTC(now.getFullYear(),now.getMonth(),now.getDate());
    const days = Math.round((Date.parse(p.due_date+'T00:00:00Z')-today)/86400000);
    remaining = days < 0 ? `已到期 ${-days} 天` : days === 0 ? '今天到期' : `剩余 ${days} 天`;
  }
  return {price, remaining, date:escapeHtml(p.due_date || '—')};
}
function renderPingPanels(node) {
  const reports = (node.last_report || {}).ping_results || [];
  const previews = node.ping_preview || reports.map(p => ({name:p.name,host:p.host,samples:[],loss:null}));
  if (!previews.length) return '<div class="ping-empty">尚未配置 TCP 测试目标</div>';
  return '<div class="ping-panels">'+['延迟','丢包'].map((heading,column) => `<div class="ping-panel"><div class="panel-label">${heading}</div>${previews.map((p,i) => {
    const report = reports.find(r => r.name===p.name);
    const value = column ? (p.loss == null ? '待采样' : Number(p.loss).toFixed(1)+'%') : (!node.online || !report ? '待上报' : report.latency<0 ? '超时' : report.latency+' ms');
    const samples = p.samples || [];
    const bars = Array.from({length:24},(_,j) => {
      const sample = samples[j-(24-samples.length)];
      const cls = !sample ? 'empty' : sample.l<0 ? 'lost' : column ? 'received' : sample.l>250 ? 'slow' : 'fast';
      return `<i class="sample-bar ${cls}" title="${!sample ? '暂无采样' : sample.l<0 ? '超时' : sample.l+' ms'}"></i>`;
    }).join('');
    return `<button class="ping-sample-row" data-action="ping" data-uuid="${escapeHtml(node.uuid)}" data-target="${escapeHtml(p.name)}" title="点击查看延迟与丢包波动曲线"><span class="ping-row-heading"><span><b class="target-dot target-${i%3}"></b>${escapeHtml(p.name)}</span><strong>${value}</strong></span><span class="sample-bars">${bars}</span></button>`;
  }).join('')}</div>`).join('')+'</div>';
}

// Preserve live card elements so each report does not repaint the entire grid.
function reconcileNode(current, next) {
  if (current.nodeType !== next.nodeType || current.nodeName !== next.nodeName) {
    current.replaceWith(next);
    return;
  }
  if (current.nodeType === 3) {
    if (current.nodeValue !== next.nodeValue) current.nodeValue = next.nodeValue;
    return;
  }
  if (current.nodeType !== 1) return;
  for (const attribute of Array.from(current.attributes)) {
    if (!next.hasAttribute(attribute.name)) current.removeAttribute(attribute.name);
  }
  for (const attribute of Array.from(next.attributes)) {
    if (current.getAttribute(attribute.name) !== attribute.value) current.setAttribute(attribute.name, attribute.value);
  }
  let oldChild = current.firstChild;
  let newChild = next.firstChild;
  while (oldChild || newChild) {
    if (!oldChild) {
      const following = newChild.nextSibling;
      current.appendChild(newChild);
      newChild = following;
    } else if (!newChild) {
      const following = oldChild.nextSibling;
      oldChild.remove();
      oldChild = following;
    } else {
      const oldFollowing = oldChild.nextSibling;
      const newFollowing = newChild.nextSibling;
      reconcileNode(oldChild, newChild);
      oldChild = oldFollowing;
      newChild = newFollowing;
    }
  }
}

// Render Nodes
function compareNodes(a, b) {
  const order = node => {
    const value = Number(node.weight);
    return Number.isSafeInteger(value) && value > 0 ? value : 0;
  };
  return order(a) - order(b)
    || (a.name || '').trim().localeCompare((b.name || '').trim(), 'en', { sensitivity: 'base', numeric: true })
    || (a.uuid || '').localeCompare(b.uuid || '', 'en');
}

function renderNodes() {
  const grid = document.getElementById('nodeGrid');
  const sorted = [...nodes].sort(compareNodes);

  if (sorted.length === 0) {
    const markup = `
      <div style="grid-column: 1 / -1; text-align: center; padding: 60px 20px; color: var(--muted-foreground);">
        暂无监控节点。点击左上角网站标题，登录后通过“节点管理 → 新建节点”开始监控。
      </div>
    `;
    if (markup !== lastNodeMarkup) {
      grid.innerHTML = markup;
      lastNodeMarkup = markup;
      lastNodeCards = [];
    }
    return;
  }

  const cards = sorted.map(VibeHex.renderNode);
  const markup = cards.join('');
  if (markup !== lastNodeMarkup) {
    const canReconcile = grid.children.length === sorted.length
      && sorted.every((node, index) => grid.children[index].dataset.nodeId === node.uuid)
      && lastNodeCards.length === cards.length;
    if (canReconcile) {
      const template = document.createElement('template');
      for (let index = 0; index < cards.length; index++) {
        if (cards[index] === lastNodeCards[index]) continue;
        template.innerHTML = cards[index];
        reconcileNode(grid.children[index], template.content.firstElementChild);
      }
    } else {
      grid.innerHTML = markup;
    }
    lastNodeMarkup = markup;
    lastNodeCards = cards;
  }
}

function escapeHtml(str) {
  if (!str) return '';
  return String(str).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

// Bind actions without interpreting node data as JavaScript.
document.getElementById('nodeGrid').addEventListener('click', (event) => {
  const action = event.target.closest('[data-action]');
  if (!action) return;
  const node = nodes.find(n => n.uuid === action.dataset.uuid);
  if (!node) return;
  switch (action.dataset.action) {
    case 'edit': if (isAdmin) openEditModal(node.uuid); break;
    case 'guide': if (isAdmin) showGuide(node.uuid); break;
    case 'delete': if (isAdmin) deleteNode(node.uuid); break;
    case 'ping': openPingChart(node.uuid, node.name, action.dataset.target); break;
  }
});

// Data Fetching & WebSocket
function hasFreshWSData() {
  return wsHasData && ws && ws.readyState === 1 && Date.now() - lastWSDataAt < 10000;
}

async function fetchNodes() {
  if (hasFreshWSData()) return;
  try {
    const res = await fetch('/api/nodes');
    const data = await res.json();
    if (Array.isArray(data) && !hasFreshWSData()) {
      nodes = data;
      updateGlobalStats();
      renderNodes();
    }
  } catch (e) {
    console.error('Failed to fetch nodes:', e);
  }
}

const DEFAULT_SITE_ICON_URL = document.getElementById('siteIcon').getAttribute('href');

function updateSiteIconDisplay(iconUrl) {
  const preview = document.getElementById('logoPreviewContent');
  const siteIcon = document.getElementById('siteIcon');
  const effectiveUrl = iconUrl || DEFAULT_SITE_ICON_URL;
  if (siteIcon) siteIcon.href = effectiveUrl;
  if (preview) {
    preview.innerHTML = `<img src="${escapeHtml(effectiveUrl)}" alt="网站图标预览" width="28" height="28">`;
    const img = preview.querySelector('img');
    if (img && iconUrl) {
      img.onerror = () => {
        img.onerror = null;
        img.src = DEFAULT_SITE_ICON_URL;
        if (siteIcon) siteIcon.href = DEFAULT_SITE_ICON_URL;
      };
    }
  }
}

async function fetchPublicSettings() {
  try {
    const res = await fetch('/api/public');
    if (!res.ok) throw new Error('无法读取网站设置');
    const data = await res.json();
    const settings = JSON.stringify([data.site_title || '', data.site_icon || '']);
    if (settings === lastPublicSettings) return;
    lastPublicSettings = settings;
    if (data.site_title) {
      document.getElementById('siteTitle').textContent = data.site_title;
      document.title = data.site_title;
    }
    updateSiteIconDisplay(data.site_icon || '');
  } catch (e) {
    console.error('Failed to fetch public settings:', e);
  }
}

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
    const tokenInput = document.getElementById('telegramBotToken');
    tokenInput.value = '';
    tokenInput.placeholder = telegram.token_configured ? '已配置；留空保留原 Token' : '填写 BotFather 提供的 Token';
    return true;
  } catch (e) { alert(e.message); return false; }
}

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
        reminder_timezone: document.getElementById('telegramReminderTimezone').value.trim()
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

function connectWebSocket() {
  if (ws) {
    try { ws.close(); } catch(e) {}
  }
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const wsUrl = `${protocol}//${window.location.host}/api/clients`;
  ws = new WebSocket(wsUrl);

  ws.onopen = () => {
    console.log('WebSocket connected to', wsUrl);
  };

  ws.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data);
      if (msg.nodes) {
        wsHasData = true;
        lastWSDataAt = Date.now();
        nodes = msg.nodes;
        updateGlobalStats();
        renderNodes();
      }
    } catch (e) {
      console.error('WS parse error:', e);
    }
  };

  ws.onclose = () => {
    wsHasData = false;
    lastWSDataAt = 0;
    console.warn('WebSocket disconnected, reconnecting in 3s...');
    setTimeout(connectWebSocket, 3000);
  };
}

// Admin Actions
window.showGuide = async function(uuid) {
  let token;
  try {
    const res = await fetch(`/api/admin/nodes/${encodeURIComponent(uuid)}/token`, {
      credentials: 'same-origin'
    });
    if (!res.ok) throw new Error('请重新登录后获取接入命令');
    const data = await res.json();
    token = data.token;
  } catch (error) {
    alert(error.message);
    return;
  }
  const host = window.location.origin;
  document.getElementById('guideInstallCmd').textContent = `curl -fsSL ${host}/install.sh?token=${token} | bash`;
  document.getElementById('guideRunCmd').textContent = `./vibemonitor agent --server ${host} --token ${token}`;
  document.getElementById('guideUninstallCmd').textContent = `curl -fsSL ${host}/install.sh?token=${encodeURIComponent(token)} | bash -s -- agent-uninstall`;
  document.getElementById('guideToken').textContent = token;
  openModal('nodeGuideModal');
};

window.copyGuideCommand = async function(id) {
  const value = document.getElementById(id).textContent;
  try {
    await navigator.clipboard.writeText(value);
    alert('命令已复制');
  } catch (e) {
    alert('复制失败，请手动复制命令');
  }
};

window.deleteNode = async function(uuid) {
  if (!confirm('确定要删除该监控节点吗？')) return;
  const token = getAdminToken();
  try {
    const res = await fetch(`/api/admin/nodes/${uuid}`, {
      method: 'DELETE',
      credentials: 'same-origin'
    });
    if (res.ok) {
      fetchNodes();
    } else {
      alert('删除失败');
    }
  } catch (e) {
    alert('请求失败: ' + e.message);
  }
};

// Event Listeners
function requireAdmin(action) {
  if (isAdmin) {
    action();
    return;
  }
  pendingAdminAction = action;
  const error = document.getElementById('loginError');
  error.style.display = 'none';
  error.textContent = '账号或密码错误';
  const pwInput = document.getElementById('loginPassword');
  pwInput.value = '';
  pwInput.type = 'password';
  pwInput.autocomplete = 'current-password';
  const pwToggleBtn = document.querySelector('.btn-toggle-pwd[data-target="loginPassword"]');
  if (pwToggleBtn) { pwToggleBtn.textContent = '👁️'; pwToggleBtn.title = '显示密码'; }
  const unInput = document.getElementById('loginUsername');
  if (unInput) unInput.autocomplete = 'username';
  openModal('loginModal');
}

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

document.getElementById('loginForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const password = document.getElementById('loginPassword').value;
  try {
    const res = await fetch('/api/admin/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: document.getElementById('loginUsername').value, password })
    });
    const data = await res.json();
    if (data.token) {
      setAdminState(true);
      const action = pendingAdminAction;
      closeModal('loginModal');
      if (action) action();
    } else {
      document.getElementById('loginError').style.display = 'block';
    }
  } catch (e) {
    document.getElementById('loginError').textContent = '连接失败: ' + e.message;
    document.getElementById('loginError').style.display = 'block';
  }
});

async function handleLogout() {
  try {
    const res = await fetch('/api/admin/logout', {
      method: 'POST', credentials: 'same-origin'
    });
    if (!res.ok) throw new Error('退出失败，请重试');
    const guideToken = document.getElementById('guideToken');
    if (guideToken) guideToken.textContent = '';
    const guideInstallCmd = document.getElementById('guideInstallCmd');
    if (guideInstallCmd) guideInstallCmd.textContent = '';
    const guideRunCmd = document.getElementById('guideRunCmd');
    if (guideRunCmd) guideRunCmd.textContent = '';
    setAdminState(false);
  } catch (error) { alert(error.message); }
}

const logoutBtn = document.getElementById('logoutBtn');
if (logoutBtn) logoutBtn.addEventListener('click', handleLogout);

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
    document.getElementById('newNodeOrder').value = '';
    document.getElementById('newNodeTrafficLimit').value = '';
    document.getElementById('newNodeResetDay').value = '';
    document.getElementById('newNodeInitialUsed').value = '';
    fillNodeProfile('new', {});
    openModal('addNodeModal');
  });
});

document.getElementById('addNodeForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const name = document.getElementById('newNodeName').value;
  const region = document.getElementById('newNodeRegion').value;
  const trafficLimitGB = parseFloat(document.getElementById('newNodeTrafficLimit').value) || 0;
  const resetDay = parseInt(document.getElementById('newNodeResetDay').value) || 0;
  const initialUsedGB = parseFloat(document.getElementById('newNodeInitialUsed').value) || 0;
  const token = getAdminToken();

  try {
    const res = await fetch('/api/admin/nodes', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      credentials: 'same-origin',
      body: JSON.stringify({
        name,
        region,
        weight: readNodeOrder('new'),
        traffic_limit_gb: trafficLimitGB,
        reset_day: resetDay,
        initial_used_gb: initialUsedGB,
        profile: readNodeProfile('new')
      })
    });
    const data = await res.json();
    if (data.node) {
      closeModal('addNodeModal');
      fetchNodes();
      showGuide(data.node.uuid);
    } else {
      alert('创建失败: ' + (data.error || '未知错误'));
    }
  } catch (e) {
    alert('请求失败: ' + e.message);
  }
});

window.openEditModal = async function(uuid) {
  const node = nodes.find(n => n.uuid === uuid);
  if (!node || !isAdmin) return;
  let profile;
  try {
    const res = await fetch(`/api/admin/nodes/${encodeURIComponent(uuid)}/profile`, {
      credentials: 'same-origin',
      cache: 'no-store'
    });
    if (!res.ok) throw new Error('无法读取节点配置，请确认登录状态后重试');
    profile = (await res.json()).profile;
  } catch (e) {
    alert(e.message);
    return;
  }
  document.getElementById('editNodeUUID').value = node.uuid;
  document.getElementById('editNodeName').value = node.name || '';
  document.getElementById('editNodeOrder').value = node.weight > 0 ? node.weight : '';
  document.getElementById('editNodeRegion').value = node.region || '';
  document.getElementById('editNodeTrafficLimit').value = node.traffic_limit > 0 ? (node.traffic_limit / (1024*1024*1024)).toFixed(1) : '';
  document.getElementById('editNodeResetDay').value = node.reset_day > 0 ? node.reset_day : '';
  const currentUsed = Math.max(0, Number(node.cycle_total_used) || 0);
  const sampledAt = node.last_report?.updated_at ? new Date(node.last_report.updated_at) : null;
  const sampleHint = sampledAt && !Number.isNaN(sampledAt.getTime()) ? `最近采样：${sampledAt.toLocaleString()}` : '尚无探针采样';
  document.getElementById('editNodeCycleUsed').value = '';
  document.getElementById('editNodeCycleUsed').placeholder = (currentUsed / (1024*1024*1024)).toFixed(3);
  document.getElementById('editNodeCycleUsedHint').textContent = `当前已用 ${formatBytes(currentUsed)}；${sampleHint}。留空不修改，填 0 可清零。`;
  fillNodeProfile('edit', profile);
  openModal('editNodeModal');
};

document.getElementById('editNodeGuideBtn').addEventListener('click', () => {
  const uuid = document.getElementById('editNodeUUID').value;
  if (uuid) showGuide(uuid);
});

document.getElementById('editNodeForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const uuid = document.getElementById('editNodeUUID').value;
  const name = document.getElementById('editNodeName').value;
  const group = nodes.find(node => node.uuid === uuid)?.group || ''; // Preserve legacy metadata when editing.
  const region = document.getElementById('editNodeRegion').value;
  const trafficLimitGB = parseFloat(document.getElementById('editNodeTrafficLimit').value) || 0;
  const resetDay = parseInt(document.getElementById('editNodeResetDay').value) || 0;
  const cycleUsedText = document.getElementById('editNodeCycleUsed').value.trim();
  const cycleUsedGB = cycleUsedText === '' ? null : Number(cycleUsedText);
  if (cycleUsedGB !== null && (!Number.isFinite(cycleUsedGB) || cycleUsedGB < 0)) {
    alert('当前周期已用流量必须是非负数字');
    return;
  }
  const token = getAdminToken();

  try {
    const res = await fetch(`/api/admin/nodes/${uuid}`, {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
      },
      credentials: 'same-origin',
      body: JSON.stringify({
        name,
        group,
        region,
        weight: readNodeOrder('edit'),
        traffic_limit_gb: trafficLimitGB,
        reset_day: resetDay,
        ...(cycleUsedGB === null ? {} : { cycle_used_gb: cycleUsedGB }),
        profile: readNodeProfile('edit')
      })
    });
    if (res.ok) {
      closeModal('editNodeModal');
      fetchNodes();
    } else {
      const data = await res.json().catch(() => ({}));
      alert('修改失败: ' + (data.error || '未知错误'));
    }
  } catch (e) {
    alert('请求失败: ' + e.message);
  }
});

document.getElementById('deleteNodeBtn').addEventListener('click', async () => {
  const uuid = document.getElementById('editNodeUUID').value;
  if (!uuid) return;
  if (!confirm('确定要删除该节点吗？此操作不可恢复。')) return;
  const token = getAdminToken();
  try {
    const res = await fetch(`/api/admin/nodes/${uuid}`, {
      method: 'DELETE',
      credentials: 'same-origin'
    });
    if (res.ok) {
      closeModal('editNodeModal');
      fetchNodes();
    } else {
      alert('删除失败');
    }
  } catch (e) {
    alert('请求失败: ' + e.message);
  }
});
// --- Ping Fluctuation Chart State & Logic ---
let currentPingNodeUUID = null;
let currentPingNodeName = '';
let currentPingTarget = '';
let currentPingRange = '1h';
let cachedPingSamples = [];

window.openPingChart = function(uuid, nodeName, targetName) {
  currentPingNodeUUID = uuid;
  currentPingNodeName = nodeName;
  currentPingTarget = targetName || '';
  currentPingRange = '1h';

  ['btnRange1h', 'btnRange24h', 'btnRange7d', 'btnRange31d'].forEach(id => {
    const el = document.getElementById(id);
    if (el && el.classList) {
      if (id === 'btnRange1h') {
        el.classList.add('active');
      } else {
        el.classList.remove('active');
      }
    }
  });

  // Render Target selector buttons
  const node = nodes.find(n => n.uuid === uuid);
  const selector = document.getElementById('pingTargetSelector');
  selector.innerHTML = '';
  const chartTargets = node ? (node.ping_preview || (node.last_report || {}).ping_results || []) : [];
  if (chartTargets.length > 0) {
    if (!currentPingTarget) {
      currentPingTarget = chartTargets[0].name;
    }
    chartTargets.forEach(pr => {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = `target-pill-btn ${pr.name === currentPingTarget ? 'active' : ''}`;
      btn.textContent = pr.name;
      btn.onclick = () => switchPingTarget(pr.name);
      selector.appendChild(btn);
    });
  }

  updatePingModalHeader();
  openModal('pingChartModal');
  loadPingHistory();
};

function updatePingModalHeader() {
  document.getElementById('pingModalTitle').textContent = `${currentPingNodeName} 延迟波动曲线`;
  document.getElementById('pingModalSubtitle').textContent = `目标: ${currentPingTarget || '全部'}`;
}

window.switchPingTarget = function(targetName) {
  currentPingTarget = targetName;
  const buttons = document.querySelectorAll('#pingTargetSelector .target-pill-btn');
  buttons.forEach(b => {
    if (b.textContent === targetName) b.classList.add('active');
    else b.classList.remove('active');
  });
  updatePingModalHeader();
  loadPingHistory();
};

window.switchPingRange = function(range) {
  currentPingRange = range;
  const rangeBtnMap = {
    '1h': 'btnRange1h',
    '24h': 'btnRange24h',
    '7d': 'btnRange7d',
    '31d': 'btnRange31d',
    'all': 'btnRangeAll'
  };
  Object.entries(rangeBtnMap).forEach(([r, btnId]) => {
    const el = document.getElementById(btnId);
    if (el && el.classList) {
      if (r === range) {
        el.classList.add('active');
      } else {
        el.classList.remove('active');
      }
    }
  });
  loadPingHistory();
};

async function loadPingHistory() {
  if (!currentPingNodeUUID) return;
  const statCur = document.getElementById('statCurrent');
  const statAvg = document.getElementById('statAvg');
  const statMin = document.getElementById('statMin');
  const statMax = document.getElementById('statMax');
  const statLoss = document.getElementById('statLoss');

  statCur.textContent = '...';
  statAvg.textContent = '...';
  statMin.textContent = '...';
  statMax.textContent = '...';
  statLoss.textContent = '...';

  try {
    const res = await fetch(`/api/nodes/ping-history?uuid=${currentPingNodeUUID}&target=${encodeURIComponent(currentPingTarget)}&range=${currentPingRange}`);
    if (!res.ok) throw new Error('加载失败');
    const data = await res.json();

    if (data.target) {
      document.getElementById('pingModalSubtitle').textContent = `目标: ${data.target} · 历史方式: ${{tcp: 'TCP', icmp: 'ICMP', unknown: '未标明'}[data.method] || '暂无采样'}`;
    }

    // Stats
    const s = data.stats || {};
    statCur.textContent = s.current >= 0 ? `${s.current}ms` : (s.total_count > 0 ? '超时' : '--');
    statCur.style.color = s.current < 0 ? 'var(--destructive)' : s.current > 150 ? 'var(--warning)' : 'var(--success)';
    statAvg.textContent = s.avg > 0 ? `${s.avg}ms` : '--';
    statMin.textContent = s.min >= 0 ? `${s.min}ms` : '--';
    statMax.textContent = s.max >= 0 ? `${s.max}ms` : '--';
    statLoss.textContent = `${s.packet_loss || 0}%`;
    statLoss.style.color = (s.packet_loss > 0) ? 'var(--destructive)' : 'var(--success)';

    cachedPingSamples = data.samples || [];

    const nowSec = Math.floor(Date.now() / 1000);
    let startSec = data.start_time;
    let endSec = data.end_time || nowSec;
    if (!startSec) {
      const duration = currentPingRange === '1h' ? 3600 :
                       currentPingRange === '7d' ? 7 * 86400 :
                       currentPingRange === '31d' ? 31 * 86400 :
                       currentPingRange === 'all' ? (cachedPingSamples[0]?.t || (nowSec - 86400)) : 86400;
      startSec = nowSec - duration;
    }

    // Time indicators
    const showDate = currentPingRange !== '1h';
    document.getElementById('chartTimeStart').textContent = formatChartTime(startSec, showDate);
    document.getElementById('chartTimeEnd').textContent = `现在 (${formatChartTime(endSec, showDate)})`;

    renderPingSvgChart(cachedPingSamples, currentPingRange, null, startSec, endSec, data.offline_intervals || []);
  } catch (e) {
    renderPingSvgChart([], currentPingRange, e.message);
  }
}

function formatChartTime(tSec, showDate) {
  const d = new Date(tSec * 1000);
  const h = String(d.getHours()).padStart(2, '0');
  const m = String(d.getMinutes()).padStart(2, '0');
  if (showDate) {
    const year = d.getFullYear();
    const currentYear = new Date().getFullYear();
    const month = String(d.getMonth() + 1).padStart(2, '0');
    const day = String(d.getDate()).padStart(2, '0');
    if (year !== currentYear) {
      return `${year}-${month}-${day} ${h}:${m}`;
    }
    return `${month}-${day} ${h}:${m}`;
  }
  return `${h}:${m}`;
}

function formatTooltipTime(tSec) {
  const d = new Date(tSec * 1000);
  const year = d.getFullYear();
  const currentYear = new Date().getFullYear();
  const month = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  const timeStr = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  if (year !== currentYear) {
    return `${year}-${month}-${day} ${timeStr}`;
  }
  return `${month}-${day} ${timeStr}`;
}

function renderPingSvgChart(samples, range, errorMsg, startSec, nowSec, offlineIntervals = []) {
  const svg = document.getElementById('pingChartSvg');
  const tooltip = document.getElementById('chartTooltip');
  if (tooltip) tooltip.style.display = 'none';

  const W = 700;
  const H = 240;
  const padL = 48;
  const padR = 20;
  const padT = 24;
  const padB = 32;
  const plotW = W - padL - padR;
  const plotH = H - padT - padB;

  if (errorMsg || ((!samples || samples.length === 0) && (!offlineIntervals || offlineIntervals.length === 0))) {
    svg.innerHTML = `
      <text x="${W / 2}" y="${H / 2}" text-anchor="middle" fill="#94a3b8" font-size="13" font-family="system-ui">
        ${errorMsg ? '获取数据失败: ' + errorMsg : '暂无历史时序数据，探针正在每 60 秒记录持久化中...'}
      </text>
    `;
    return;
  }

  const duration = (nowSec && startSec && nowSec > startSec)
    ? (nowSec - startSec)
    : (range === '1h' ? 3600 : (range === '7d' ? 7 * 86400 : (range === '31d' ? 31 * 86400 : 86400)));
  const baseStart = startSec || (Math.floor(Date.now() / 1000) - duration);

  const offlineSvg = offlineIntervals.map(iv => {
    const left = padL + (Math.max(0, iv.start - baseStart) / duration) * plotW;
    const right = padL + (Math.min(duration, iv.end - baseStart) / duration) * plotW;
    if (right <= left) return '';
    const width = (right - left).toFixed(1);
    return `<rect x="${left.toFixed(1)}" y="${padT}" width="${width}" height="${plotH}" fill="#ef4444" opacity="0.26"><title>探针掉线：${formatTooltipTime(iv.start)} - ${formatTooltipTime(iv.end)}</title></rect>
      <line x1="${left.toFixed(1)}" y1="${padT}" x2="${left.toFixed(1)}" y2="${padT + plotH}" stroke="#ef4444" stroke-width="1.5" stroke-dasharray="2,2" opacity="0.7" />
      <line x1="${right.toFixed(1)}" y1="${padT}" x2="${right.toFixed(1)}" y2="${padT + plotH}" stroke="#ef4444" stroke-width="1.5" stroke-dasharray="2,2" opacity="0.7" />`;
  }).join('');

  // Find max latency for Y scale
  let maxLat = 50;
  samples.forEach(s => {
    if (s.l > maxLat) maxLat = s.l;
  });
  maxLat = Math.ceil(maxLat * 1.25 / 10) * 10;
  if (maxLat < 50) maxLat = 50;

  // Grid steps (4 horizontal lines)
  const gridSteps = 4;
  let gridSvg = '';
  for (let i = 0; i <= gridSteps; i++) {
    const val = Math.round((maxLat / gridSteps) * i);
    const y = padT + plotH - (val / maxLat) * plotH;
    gridSvg += `
      <line x1="${padL}" y1="${y}" x2="${W - padR}" y2="${y}" stroke="#334155" stroke-width="1" stroke-dasharray="3,3" opacity="0.4" />
      <text x="${padL - 8}" y="${y + 4}" text-anchor="end" fill="#64748b" font-size="11" font-family="system-ui">${val}ms</text>
    `;
  }

  // Calculate coordinates based on true time offset from start
  const points = samples.map(s => {
    const timeOffset = Math.max(0, Math.min(duration, s.t - baseStart));
    const x = padL + (timeOffset / duration) * plotW;
    const isLoss = s.l < 0;
    const y = isLoss ? (padT + plotH) : (padT + plotH - (s.l / maxLat) * plotH);
    return { x, y, l: s.l, t: s.t, isLoss };
  }).sort((a, b) => a.x - b.x);

  // Build SVG path (遇丢包分段断开，避免直插底部尖刺)
  let pathD = '';
  let areaD = '';
  let lossDots = '';
  let hasValid = false;

  let inSegment = false;
  let segStart = null;
  let prevPt = null;
  let lastLossX = null;

  points.forEach(pt => {
    if (pt.isLoss) {
      if (lastLossX === null || Math.abs(pt.x - lastLossX) >= 2) {
        lossDots += `<circle cx="${pt.x.toFixed(1)}" cy="${padT + plotH - 3}" r="3.5" fill="#f43f5e" opacity="0.85" />`;
        lastLossX = pt.x;
      }
      if (inSegment && prevPt && segStart) {
        areaD += ` L ${prevPt.x.toFixed(1)} ${padT + plotH} L ${segStart.x.toFixed(1)} ${padT + plotH} Z`;
      }
      inSegment = false;
      segStart = null;
      prevPt = null;
    } else {
      hasValid = true;
      if (!inSegment) {
        pathD += ` M ${pt.x.toFixed(1)} ${pt.y.toFixed(1)}`;
        areaD += ` M ${pt.x.toFixed(1)} ${pt.y.toFixed(1)}`;
        segStart = pt;
        inSegment = true;
      } else {
        pathD += ` L ${pt.x.toFixed(1)} ${pt.y.toFixed(1)}`;
        areaD += ` L ${pt.x.toFixed(1)} ${pt.y.toFixed(1)}`;
      }
      prevPt = pt;
    }
  });
  if (inSegment && prevPt && segStart) {
    areaD += ` L ${prevPt.x.toFixed(1)} ${padT + plotH} L ${segStart.x.toFixed(1)} ${padT + plotH} Z`;
  }

  const lineColor = hasValid ? '#06b6d4' : '#f43f5e';
  const gradId = 'pingGrad_' + Math.random().toString(36).substr(2, 6);

  svg.innerHTML = `
    <defs>
      <linearGradient id="${gradId}" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0%" stop-color="${lineColor}" stop-opacity="0.35" />
        <stop offset="100%" stop-color="${lineColor}" stop-opacity="0.0" />
      </linearGradient>
    </defs>
    ${gridSvg}
    ${offlineSvg}
    ${areaD ? `<path d="${areaD}" fill="url(#${gradId})" />` : ''}
    ${pathD ? `<path d="${pathD}" fill="none" stroke="${lineColor}" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" />` : ''}
    ${lossDots}
    <line id="hoverLine" x1="0" y1="${padT}" x2="0" y2="${padT + plotH}" stroke="#94a3b8" stroke-width="1.5" stroke-dasharray="2,2" style="display: none;" />
    <circle id="hoverPoint" cx="0" cy="0" r="4.5" fill="#38bdf8" stroke="#ffffff" stroke-width="2" style="display: none;" />
    <rect x="${padL}" y="${padT}" width="${plotW}" height="${plotH}" fill="transparent" id="chartHitBox" style="cursor: crosshair;" />
  `;

  // 二分查找最近的采样点
  function findClosestPoint(pts, xVal) {
    if (pts.length === 1) return pts[0];
    let low = 0, high = pts.length - 1;
    while (low < high - 1) {
      const mid = (low + high) >> 1;
      if (pts[mid].x < xVal) low = mid;
      else high = mid;
    }
    return Math.abs(pts[low].x - xVal) <= Math.abs(pts[high].x - xVal) ? pts[low] : pts[high];
  }

  // Interactive mouse tracking
  const hitBox = svg.querySelector('#chartHitBox');
  const hoverLine = svg.querySelector('#hoverLine');
  const hoverPoint = svg.querySelector('#hoverPoint');

  hitBox.addEventListener('mousemove', (e) => {
    const rect = svg.getBoundingClientRect();
    const mouseX = (e.clientX - rect.left) / rect.width * W;
    if (mouseX < padL || mouseX > W - padR) {
      hoverLine.style.display = 'none';
      hoverPoint.style.display = 'none';
      if (tooltip) tooltip.style.display = 'none';
      return;
    }

    const hoverSec = baseStart + ((mouseX - padL) / plotW) * duration;
    const offlineIv = offlineIntervals.find(iv => hoverSec >= iv.start && hoverSec <= iv.end);
    if (offlineIv) {
      hoverLine.setAttribute('x1', mouseX);
      hoverLine.setAttribute('x2', mouseX);
      hoverLine.setAttribute('stroke', '#ef4444');
      hoverLine.style.display = 'block';
      hoverPoint.style.display = 'none';
      if (tooltip) {
        const timeStr = formatTooltipTime(Math.round(hoverSec));
        tooltip.innerHTML = `<div>${timeStr}</div><div style="color: #ef4444; font-weight: bold;">探针掉线 (离线)</div><div style="font-size: 11px; color: #94a3b8;">${formatTooltipTime(offlineIv.start)} ~ ${formatTooltipTime(offlineIv.end)}</div>`;
        tooltip.style.display = 'block';
      }
      return;
    }
    hoverLine.setAttribute('stroke', '#94a3b8');

    if (points.length === 0) {
      hoverLine.style.display = 'none';
      hoverPoint.style.display = 'none';
      if (tooltip) tooltip.style.display = 'none';
      return;
    }

    const closest = findClosestPoint(points, mouseX);

    hoverLine.setAttribute('x1', closest.x);
    hoverLine.setAttribute('x2', closest.x);
    hoverLine.style.display = 'block';

    hoverPoint.setAttribute('cx', closest.x);
    hoverPoint.setAttribute('cy', closest.isLoss ? (padT + plotH - 3) : closest.y);
    hoverPoint.setAttribute('fill', closest.isLoss ? '#f43f5e' : '#38bdf8');
    hoverPoint.style.display = 'block';

    if (tooltip) {
      const timeStr = formatTooltipTime(closest.t);
      const valStr = closest.isLoss ? `<strong style="color: #f43f5e;">丢包 (超时)</strong>` : `延迟: <strong>${closest.l} ms</strong>`;
      tooltip.innerHTML = `<div>${timeStr}</div><div>${valStr}</div>`;
      tooltip.style.display = 'block';
    }
  });

  hitBox.addEventListener('mouseleave', () => {
    hoverLine.style.display = 'none';
    hoverPoint.style.display = 'none';
    if (tooltip) tooltip.style.display = 'none';
  });
}

// Password show/hide toggle handling
document.addEventListener('click', (e) => {
  const btn = e.target.closest('.btn-toggle-pwd');
  if (!btn) return;
  const targetId = btn.dataset.target;
  if (!targetId) return;
  const input = document.getElementById(targetId);
  if (!input) return;
  if (input.type === 'password') {
    input.type = 'text';
    btn.textContent = '🙈';
    btn.title = '隐藏密码';
  } else {
    input.type = 'password';
    btn.textContent = '👁️';
    btn.title = '显示密码';
  }
});

// Initialize
resetPasswordFields();
fetchNodes();
connectWebSocket();
checkAdminAuth();
fetchPublicSettings();
// Periodic fallback polling every 5s
setInterval(fetchNodes, 5000);
// Refresh the site title and favicon when WebSocket is unavailable.
setInterval(fetchPublicSettings, 15000);
