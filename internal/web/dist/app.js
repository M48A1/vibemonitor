// VibeMonitor Modern Dashboard Script

let nodes = [];
// UUIDs are never reused. Ignore pre-deletion HTTP/WS snapshots for this page.
const deletedNodeIDs = new Set();
const deletingNodeIDs = new Set();
let isAdmin = false;
let ws = null;
let wsHasData = false;
let lastWSDataAt = 0;
let lastDashboardUpdate = 0;
let dashboardRevision = 0;
let dashboardConnectionFailed = false;
const pendingNodeForms = new WeakSet();

function updateConnectionStatus() {
  const status = document.getElementById('connectionStatus');
  if (!status) return;
  const stale = !lastDashboardUpdate || Date.now() - lastDashboardUpdate > 20000;
  const last = lastDashboardUpdate ? ' · 最后更新 ' + new Date(lastDashboardUpdate).toLocaleTimeString() : '';
  status.dataset.state = stale ? 'stale' : 'live';
  const message = stale
    ? (dashboardConnectionFailed ? '连接中断，数据已过期，正在重试' : lastDashboardUpdate ? '数据已过期，正在重新同步' : '正在连接并加载数据') + last
    : (ws && ws.readyState === 1 && !dashboardConnectionFailed ? '实时数据已同步' : '实时连接重连中，使用备用同步') + last;
  if (status.textContent !== message) status.textContent = message;
  document.getElementById('nodeGrid').dataset.stale = String(stale);
}

function acceptNodeSnapshot(data) {
  nodes = data.filter(node => !deletedNodeIDs.has(node.uuid));
  dashboardRevision++;
  lastDashboardUpdate = Date.now();
  dashboardConnectionFailed = false;
  updateConnectionStatus();
  scheduleDashboard();
}
let pollTimer = null;
let lastNodeMarkup = '';
const nodeCardCache = new Map();
let dashboardFrame = 0;
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
    cancelPingHistory();
    cancelResourceHistory();
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
  if (modalId === 'pingChartModal') cancelPingHistory();
  if (modalId === 'resourceChartModal') cancelResourceHistory();
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
  'themeManagementModal',
  'settingsModal',
  'externalSettingsModal',
  'loginModal',
  'selectNodeModal',
  'addNodeModal',
  'editNodeModal',
  'nodeGuideModal',
  'pingChartModal',
  'resourceChartModal'
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
  { id: 'closeThemeManagementBtn', modalId: 'themeManagementModal' },
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
  { id: 'closePingChartBottomBtn', modalId: 'pingChartModal' },
  { id: 'closeResourceChartModalBtn', modalId: 'resourceChartModal' },
  { id: 'closeResourceChartBottomBtn', modalId: 'resourceChartModal' }
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

['1h', '24h', '7d', '31d'].forEach(range => {
  const btn = document.getElementById(`resourceRange${range}`);
  if (btn) btn.addEventListener('click', () => switchResourceRange(range));
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
  if (!document.hidden) { fetchPublicSettings(); scheduleDashboard(); fetchNodes(); }
  else if (dashboardFrame) { cancelAnimationFrame(dashboardFrame); dashboardFrame = 0; }
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
  const warningText = document.getElementById(prefix+'NodeTrafficWarning').value.trim();
  const trafficWarningPercent = warningText === '' ? 0 : Number(warningText);
  if (!Number.isFinite(trafficWarningPercent) || trafficWarningPercent < 0 || trafficWarningPercent > 100) {
    throw new Error('流量预警基准必须在 0 到 100 之间，0 表示不提前预警');
  }
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
    traffic_warning_percent: trafficWarningPercent,
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
  document.getElementById(prefix+'NodeTrafficWarning').value = p.traffic_warning_percent ?? 0;
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
    // Keep a ServerStatus row expanded while its live metrics are reconciled.
    if (attribute.name === 'open' && current.matches('details.ss-details')) continue;
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

function scheduleDashboard() {
  if (document.hidden || dashboardFrame) return;
  dashboardFrame = requestAnimationFrame(() => {
    dashboardFrame = 0;
    if (document.hidden) return;
    updateGlobalStats();
    renderNodes();
  });
}

function renderNodes() {
  if (document.hidden) return;
  const grid = document.getElementById('nodeGrid');
  const sorted = [...nodes].sort(compareNodes);
  if (!sorted.length) {
    const markup = '<div style="grid-column:1 / -1;text-align:center;padding:60px 20px;color:var(--muted-foreground)">暂无监控节点。点击左上角网站标题，登录后通过“节点管理 → 新建节点”开始监控。</div>';
    if (lastNodeMarkup !== markup) { grid.innerHTML = markup; lastNodeMarkup = markup; }
    nodeCardCache.clear();
    return;
  }
  if (!nodeCardCache.size) grid.replaceChildren();
  lastNodeMarkup = '';
  const present = new Set();
  let cursor = grid.firstChild;
  let template;
  for (const node of sorted) {
    present.add(node.uuid);
    const key = VibeHex.renderKey(node);
    let cached = nodeCardCache.get(node.uuid);
    if (!cached || cached.key !== key) {
      template ||= document.createElement('template');
      template.innerHTML = VibeHex.renderNode(node);
      const next = template.content.firstElementChild;
      if (cached) { reconcileNode(cached.element, next); cached.key = key; }
      else { cached = {key, element: next}; nodeCardCache.set(node.uuid, cached); }
    }
    if (cached.element !== cursor) grid.insertBefore(cached.element, cursor);
    cursor = cached.element.nextSibling;
  }
  for (const [id, cached] of nodeCardCache) {
    if (!present.has(id)) { cached.element.remove(); nodeCardCache.delete(id); }
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
    case 'resource': openResourceChart(node.uuid, node.name, action.dataset.metric); break;
  }
});

// Data Fetching & WebSocket
function hasFreshWSData() {
  return wsHasData && ws && ws.readyState === 1 && Date.now() - lastWSDataAt < 10000;
}

async function fetchNodes(force = false) {
  if (document.hidden || (!force && hasFreshWSData())) return;
  const revision = dashboardRevision;
  try {
    const res = await fetch('/api/nodes?view=dashboard', { cache: 'no-store' });
    if (!res.ok) throw new Error('无法读取节点列表');
    const data = await res.json();
    if (Array.isArray(data) && revision === dashboardRevision && (force || !hasFreshWSData())) {
      acceptNodeSnapshot(data);
    }
  } catch (e) {
    console.error('Failed to fetch nodes:', e);
    if (!hasFreshWSData()) { dashboardConnectionFailed = true; updateConnectionStatus(); }
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

function applyPublicSettings(data) {
  const settings = JSON.stringify([data.site_title || '', data.site_icon || '', data.site_theme || 'hex']);
  if (settings === lastPublicSettings) return;
  lastPublicSettings = settings;
  if (data.site_title) {
    document.getElementById('siteTitle').textContent = data.site_title;
    document.title = data.site_title;
  }
  updateSiteIconDisplay(data.site_icon || '');
  window.VibeThemes.apply(data.site_theme || 'hex');
}

async function fetchPublicSettings() {
  try {
    const res = await fetch('/api/public');
    if (!res.ok) throw new Error('无法读取网站设置');
    applyPublicSettings(await res.json());
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

function connectWebSocket() {
  if (ws) {
    try { ws.close(); } catch(e) {}
  }
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const wsUrl = `${protocol}//${window.location.host}/api/clients?view=dashboard`;
  ws = new WebSocket(wsUrl);

  ws.onopen = () => {
    console.log('WebSocket connected to', wsUrl);
  };

  ws.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data);
      if (Object.hasOwn(msg, 'site_title') && Object.hasOwn(msg, 'site_icon')) {
        applyPublicSettings(msg);
      }
      if (Array.isArray(msg.nodes)) {
        wsHasData = true;
        lastWSDataAt = Date.now();
        acceptNodeSnapshot(msg.nodes);
      }
    } catch (e) {
      console.error('WS parse error:', e);
    }
  };

  ws.onclose = () => {
    wsHasData = false;
    lastWSDataAt = 0;
    dashboardConnectionFailed = true;
    updateConnectionStatus();
    void fetchNodes(true);
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

async function deleteNodeAndRefresh(uuid) {
  if (deletingNodeIDs.has(uuid)) return;
  deletingNodeIDs.add(uuid);
  try {
    const res = await fetch(`/api/admin/nodes/${encodeURIComponent(uuid)}`, {
      method: 'DELETE', credentials: 'same-origin'
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error || '删除失败');
    }
    deletedNodeIDs.add(uuid);
    dashboardRevision++;
    nodes = nodes.filter(node => node.uuid !== uuid);
    if (document.getElementById('editNodeUUID').value === uuid) closeModal('editNodeModal');
    if (currentPingNodeUUID === uuid) closeModal('pingChartModal');
    if (currentResourceNodeUUID === uuid) closeModal('resourceChartModal');
    scheduleDashboard();
    // Update immediately, then reconcile even if the last WS report is fresh.
    void fetchNodes(true);
  } finally {
    deletingNodeIDs.delete(uuid);
  }
}

window.deleteNode = async function(uuid) {
  if (!confirm('确定要删除该监控节点吗？')) return;
  try {
    await deleteNodeAndRefresh(uuid);
  } catch (e) {
    alert('删除失败: ' + e.message);
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
  const form = e.currentTarget;
  if (pendingNodeForms.has(form)) return;
  const name = document.getElementById('newNodeName').value;
  const region = document.getElementById('newNodeRegion').value;
  const trafficLimitGB = parseFloat(document.getElementById('newNodeTrafficLimit').value) || 0;
  const resetDay = parseInt(document.getElementById('newNodeResetDay').value) || 0;
  const initialUsedGB = parseFloat(document.getElementById('newNodeInitialUsed').value) || 0;
  const token = getAdminToken();

  const submit = form.querySelector('button[type="submit"]');
  pendingNodeForms.add(form);
  if (submit) submit.disabled = true;
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
      void fetchNodes(true);
      showGuide(data.node.uuid);
    } else {
      alert('创建失败: ' + (data.error || '未知错误'));
    }
  } catch (e) {
    alert('请求失败: ' + e.message);
  } finally {
    pendingNodeForms.delete(form);
    if (submit) submit.disabled = false;
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
  const form = e.currentTarget;
  if (pendingNodeForms.has(form)) return;
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

  const submit = form.querySelector('button[type="submit"]');
  pendingNodeForms.add(form);
  if (submit) submit.disabled = true;
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
      void fetchNodes(true);
    } else {
      const data = await res.json().catch(() => ({}));
      alert('修改失败: ' + (data.error || '未知错误'));
    }
  } catch (e) {
    alert('请求失败: ' + e.message);
  } finally {
    pendingNodeForms.delete(form);
    if (submit) submit.disabled = false;
  }
});

document.getElementById('deleteNodeBtn').addEventListener('click', async () => {
  const uuid = document.getElementById('editNodeUUID').value;
  if (!uuid || deletingNodeIDs.has(uuid)) return;
  if (!confirm('确定要删除该节点吗？此操作不可恢复。')) return;
  const button = document.getElementById('deleteNodeBtn');
  button.disabled = true;
  try {
    await deleteNodeAndRefresh(uuid);
  } catch (e) {
    alert('删除失败: ' + e.message);
  } finally {
    button.disabled = false;
  }
});
// --- Ping Fluctuation Chart State & Logic ---
let currentPingNodeUUID = null;
let currentPingNodeName = '';
let currentPingTarget = '';
let currentPingRange = '1h';
let cachedPingSamples = [];
let pingChartRequest = 0;
let pingChartController = null;

function showHistoryLoading(svgId, tooltipId, startId, endId) {
  document.getElementById(svgId).innerHTML = '<text x="350" y="120" text-anchor="middle" fill="#64748b" font-size="13" font-family="system-ui">正在加载历史数据…</text>';
  document.getElementById(tooltipId).style.display = 'none';
  document.getElementById(startId).textContent = '--';
  document.getElementById(endId).textContent = '--';
}

function cancelPingHistory() {
  ++pingChartRequest;
  if (pingChartController) pingChartController.abort();
  pingChartController = null;
}

window.openPingChart = function(uuid, nodeName, targetName) {
  currentPingNodeUUID = uuid;
  currentPingNodeName = nodeName;
  currentPingTarget = targetName || '';
  currentPingRange = '1h';

  ['btnRange1h', 'btnRange24h', 'btnRange7d', 'btnRange31d', 'btnRangeAll'].forEach(id => {
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
  cancelPingHistory();
  const request = pingChartRequest;
  const uuid = currentPingNodeUUID;
  const target = currentPingTarget;
  const range = currentPingRange;
  if (!uuid) return;
  const controller = new AbortController();
  pingChartController = controller;
  cachedPingSamples = [];
  showHistoryLoading('pingChartSvg', 'chartTooltip', 'chartTimeStart', 'chartTimeEnd');
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
    const query = new URLSearchParams({uuid, target, range});
    const res = await fetch(`/api/nodes/ping-history?${query}`, {signal: controller.signal});
    if (!res.ok) throw new Error('加载失败');
    const data = await res.json();
    if (request !== pingChartRequest || controller.signal.aborted) return;

    if (data.target) {
      document.getElementById('pingModalSubtitle').textContent = `目标: ${data.target} · 历史方式: ${{tcp: 'TCP', icmp: 'ICMP', unknown: '未标明'}[data.method] || '暂无采样'}`;
    }

    // Stats
    const s = data.stats || {};
    const hasSamples = s.total_count > 0;
    statCur.textContent = hasSamples ? (s.current >= 0 ? `${s.current}ms` : '超时') : '--';
    statCur.style.color = !hasSamples ? 'var(--muted-foreground)' : s.current < 0 ? 'var(--destructive)' : s.current > 150 ? 'var(--warning)' : 'var(--success)';
    statAvg.textContent = hasSamples && s.min >= 0 && s.avg >= 0 ? `${s.avg}ms` : '--';
    statMin.textContent = hasSamples && s.min >= 0 ? `${s.min}ms` : '--';
    statMax.textContent = hasSamples && s.max >= 0 ? `${s.max}ms` : '--';
    statLoss.textContent = hasSamples ? `${s.packet_loss || 0}%` : '--';
    statLoss.style.color = !hasSamples ? 'var(--muted-foreground)' : s.packet_loss > 0 ? 'var(--destructive)' : 'var(--success)';

    cachedPingSamples = data.samples || [];

    const nowSec = Math.floor(Date.now() / 1000);
    let startSec = data.start_time;
    let endSec = data.end_time || nowSec;
    if (!startSec) {
      startSec = range === 'all' ? (cachedPingSamples[0]?.t || (nowSec - 86400)) :
        nowSec - (range === '1h' ? 3600 : range === '7d' ? 7 * 86400 : range === '31d' ? 31 * 86400 : 86400);
    }

    // Time indicators
    const showDate = range !== '1h';
    document.getElementById('chartTimeStart').textContent = formatChartTime(startSec, showDate);
    document.getElementById('chartTimeEnd').textContent = `现在 (${formatChartTime(endSec, showDate)})`;

    renderPingSvgChart(cachedPingSamples, range, null, startSec, endSec, data.offline_intervals || []);
  } catch (e) {
    if (request !== pingChartRequest || controller.signal.aborted) return;
    [statCur, statAvg, statMin, statMax, statLoss].forEach(stat => { stat.textContent = '--'; });
    renderPingSvgChart([], range, e.message);
  } finally {
    if (pingChartController === controller) pingChartController = null;
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
      if (inSegment && prevPt && offlineIntervals.some(iv => iv.start < pt.t && iv.end > prevPt.t)) {
        areaD += ` L ${prevPt.x.toFixed(1)} ${padT + plotH} L ${segStart.x.toFixed(1)} ${padT + plotH} Z`;
        inSegment = false;
        segStart = null;
      }
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
  const validPoints = points.filter(point => !point.isLoss);
  const singlePoint = validPoints.length === 1 ? validPoints[0] : null;
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
    ${singlePoint ? `<circle class="ping-sample-point" cx="${singlePoint.x.toFixed(1)}" cy="${singlePoint.y.toFixed(1)}" r="3.5" fill="${lineColor}"/>` : ''}
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

// CPU, memory, and total network rate share the same history modal.
let currentResourceNodeUUID = '';
let currentResourceMetric = 'cpu';
let currentResourceRange = '1h';
let resourceChartRequest = 0;
let resourceChartController = null;

function cancelResourceHistory() {
  ++resourceChartRequest;
  if (resourceChartController) resourceChartController.abort();
  resourceChartController = null;
}

window.openResourceChart = function(uuid, nodeName, metric) {
  if (!['cpu', 'memory', 'network'].includes(metric)) return;
  currentResourceNodeUUID = uuid;
  currentResourceMetric = metric;
  currentResourceRange = '1h';
  const label = metric === 'cpu' ? 'CPU' : metric === 'memory' ? '内存' : '实时速率';
  document.getElementById('resourceModalTitle').textContent = `${nodeName} ${label}${metric === 'network' ? '历史曲线' : '占用曲线'}`;
  document.getElementById('resourceModalSubtitle').textContent = `每 60 秒记录 · 保留 90 天 · ${metric === 'network' ? '上行＋下行（Bytes/s）' : `${label}占用率`}`;
  document.getElementById('resourceChartSvg').setAttribute('aria-label', `${label}历史曲线`);
  for (const [name, labelText] of Object.entries(metric === 'network'
    ? {Current: '最新速率', Avg: '平均速率', Min: '最低速率', Max: '最高速率'}
    : {Current: '最新记录', Avg: '平均占用', Min: '最低占用', Max: '最高占用'})) {
    document.getElementById(`resourceLabel${name}`).textContent = labelText;
  }
  updateResourceRangeButtons();
  openModal('resourceChartModal');
  loadResourceHistory();
};

function updateResourceRangeButtons() {
  ['1h', '24h', '7d', '31d'].forEach(range => {
    const button = document.getElementById(`resourceRange${range}`);
    button.classList.toggle('active', range === currentResourceRange);
  });
}

window.switchResourceRange = function(range) {
  if (!['1h', '24h', '7d', '31d'].includes(range)) return;
  currentResourceRange = range;
  updateResourceRangeButtons();
  loadResourceHistory();
};

async function loadResourceHistory() {
  cancelResourceHistory();
  const request = resourceChartRequest;
  const uuid = currentResourceNodeUUID;
  const metric = currentResourceMetric;
  const range = currentResourceRange;
  if (!uuid) return;
  const controller = new AbortController();
  resourceChartController = controller;
  showHistoryLoading('resourceChartSvg', 'resourceChartTooltip', 'resourceChartTimeStart', 'resourceChartTimeEnd');
  ['Current', 'Avg', 'Min', 'Max'].forEach(name => {
    document.getElementById(`resourceStat${name}`).textContent = '...';
  });
  try {
    const query = new URLSearchParams({uuid, metric, range});
    const response = await fetch(`/api/nodes/resource-history?${query}`, {signal: controller.signal});
    if (!response.ok) throw new Error('加载失败');
    const data = await response.json();
    if (request !== resourceChartRequest || controller.signal.aborted) return;
    const stats = data.stats || {};
    document.getElementById('resourceStatCurrent').textContent = stats.count > 0 ? formatResourceValue(metric, stats.current) : '--';
    document.getElementById('resourceStatAvg').textContent = stats.count > 0 ? formatResourceValue(metric, stats.avg) : '--';
    document.getElementById('resourceStatMin').textContent = stats.count > 0 ? formatResourceValue(metric, stats.min) : '--';
    document.getElementById('resourceStatMax').textContent = stats.count > 0 ? formatResourceValue(metric, stats.max) : '--';
    document.getElementById('resourceChartTimeStart').textContent = formatChartTime(data.start_time, range !== '1h');
    document.getElementById('resourceChartTimeEnd').textContent = `现在 (${formatChartTime(data.end_time, range !== '1h')})`;
    renderResourceSvgChart(data.samples || [], data.start_time, data.end_time, data.step_seconds, metric);
  } catch (error) {
    if (request !== resourceChartRequest || controller.signal.aborted) return;
    ['Current', 'Avg', 'Min', 'Max'].forEach(name => {
      document.getElementById(`resourceStat${name}`).textContent = '--';
    });
    renderResourceSvgChart([], 0, 0, 60, metric, '获取数据失败');
  } finally {
    if (resourceChartController === controller) resourceChartController = null;
  }
}

function formatResourceValue(metric, value) {
  if (!Number.isFinite(Number(value))) return '--';
  return metric === 'network' ? formatSpeed(Number(value)) : `${Number(value).toFixed(1)}%`;
}

function renderResourceSvgChart(samples, startSec, endSec, stepSeconds, metric, errorMsg) {
  const svg = document.getElementById('resourceChartSvg');
  const tooltip = document.getElementById('resourceChartTooltip');
  tooltip.style.display = 'none';
  const width = 700, height = 240, left = metric === 'network' ? 82 : 48, right = 20, top = 24, bottom = 32;
  const plotWidth = width - left - right, plotHeight = height - top - bottom;
  if (errorMsg || !samples.length || !(endSec > startSec)) {
    svg.innerHTML = `<text x="350" y="120" text-anchor="middle" fill="#64748b" font-size="13" font-family="system-ui">${errorMsg || '暂无历史采样，收到上报后将开始记录'}</text>`;
    return;
  }
  const duration = endSec - startSec;
  const gap = Math.max(180, Number(stepSeconds || 60) * 2.5);
  const color = metric === 'cpu' ? '#356dcc' : metric === 'memory' ? '#238364' : '#986700';
  const validSamples = samples.filter(sample => Number.isFinite(sample.t) && Number.isFinite(sample.v));
  let axisMax = 100;
  if (metric === 'network') {
    const peak = validSamples.reduce((max, sample) => Math.max(max, sample.v), 0);
    if (peak === 0) axisMax = 1024;
    else {
      const scale = 10 ** Math.floor(Math.log10(peak));
      const scaled = peak / scale;
      axisMax = (scaled <= 1 ? 1 : scaled <= 2 ? 2 : scaled <= 5 ? 5 : 10) * scale;
      axisMax = Math.max(4, axisMax);
    }
  }
  const points = validSamples
    .map(sample => ({
      t: sample.t, v: Math.max(0, Math.min(axisMax, sample.v)),
      x: left + Math.max(0, Math.min(1, (sample.t - startSec) / duration)) * plotWidth,
      y: top + (1 - Math.max(0, Math.min(axisMax, sample.v)) / axisMax) * plotHeight
    }));
  if (!points.length) {
    svg.innerHTML = '<text x="350" y="120" text-anchor="middle" fill="#64748b" font-size="13" font-family="system-ui">暂无历史采样</text>';
    return;
  }
  const grid = [0, 0.25, 0.5, 0.75, 1].map(fraction => {
    const value = axisMax * fraction;
    const y = top + (1 - fraction) * plotHeight;
    const label = metric === 'network' ? formatSpeed(value) : `${value}%`;
    return `<line x1="${left}" y1="${y}" x2="${width-right}" y2="${y}" stroke="#64748b" stroke-width="1" stroke-dasharray="3,3" opacity=".25"/><text x="${left-8}" y="${y+4}" text-anchor="end" fill="#64748b" font-size="11" font-family="system-ui">${label}</text>`;
  }).join('');
  const path = points.map((point, index) => `${index === 0 || point.t - points[index-1].t > gap ? 'M' : 'L'} ${point.x.toFixed(1)} ${point.y.toFixed(1)}`).join(' ');
  svg.innerHTML = `${grid}<path d="${path}" fill="none" stroke="${color}" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"/>
    ${points.length === 1 ? `<circle cx="${points[0].x.toFixed(1)}" cy="${points[0].y.toFixed(1)}" r="3.5" fill="${color}"/>` : ''}
    <line id="resourceHoverLine" x1="0" y1="${top}" x2="0" y2="${top+plotHeight}" stroke="#64748b" stroke-dasharray="2,2" style="display:none"/>
    <circle id="resourceHoverPoint" cx="0" cy="0" r="4.5" fill="${color}" stroke="#fff" stroke-width="2" style="display:none"/>
    <rect id="resourceChartHitBox" x="${left}" y="${top}" width="${plotWidth}" height="${plotHeight}" fill="transparent" style="cursor:crosshair"/>`;
  const hitBox = svg.querySelector('#resourceChartHitBox');
  const line = svg.querySelector('#resourceHoverLine');
  const marker = svg.querySelector('#resourceHoverPoint');
  hitBox.addEventListener('mousemove', event => {
    const rect = svg.getBoundingClientRect();
    const mouseX = (event.clientX - rect.left) / rect.width * width;
    const hoverTime = startSec + (mouseX - left) / plotWidth * duration;
    let low = 0, high = points.length - 1;
    while (low < high) {
      const middle = (low + high) >> 1;
      if (points[middle].t < hoverTime) low = middle + 1;
      else high = middle;
    }
    const closest = low > 0 && Math.abs(points[low-1].t-hoverTime) < Math.abs(points[low].t-hoverTime) ? points[low-1] : points[low];
    if (mouseX < left || mouseX > width-right || Math.abs(closest.t-hoverTime) > gap / 2) {
      line.style.display = marker.style.display = tooltip.style.display = 'none';
      return;
    }
    line.setAttribute('x1', closest.x);
    line.setAttribute('x2', closest.x);
    marker.setAttribute('cx', closest.x);
    marker.setAttribute('cy', closest.y);
    line.style.display = marker.style.display = tooltip.style.display = 'block';
    tooltip.textContent = `${formatTooltipTime(closest.t)} · ${metric === 'cpu' ? 'CPU' : metric === 'memory' ? '内存' : '实时速率'} ${formatResourceValue(metric, closest.v)}`;
  });
  hitBox.addEventListener('mouseleave', () => {
    line.style.display = marker.style.display = tooltip.style.display = 'none';
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
updateConnectionStatus();
setInterval(updateConnectionStatus, 1000);
resetPasswordFields();
fetchNodes();
connectWebSocket();
checkAdminAuth();
fetchPublicSettings();
// Periodic fallback polling every 5s
setInterval(fetchNodes, 5000);
// Refresh the site title and favicon when WebSocket is unavailable.
setInterval(fetchPublicSettings, 15000);
