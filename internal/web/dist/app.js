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
    if (typeof cancelEditNodeLoad === 'function') cancelEditNodeLoad();
    cancelPingHistory();
    cancelResourceHistory();
    if (typeof cancelRankings === 'function') cancelRankings();
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
  if (modalId === 'editNodeModal' && typeof cancelEditNodeLoad === 'function') cancelEditNodeLoad();
  if (modalId === 'pingChartModal') cancelPingHistory();
  if (modalId === 'resourceChartModal') cancelResourceHistory();
  if (modalId === 'rankingsModal' && typeof cancelRankings === 'function') cancelRankings();
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
  'rankingsModal',
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

['current', 'previous', 'three'].forEach(range => {
  document.getElementById(`trafficRange${range}`).addEventListener('click', () => switchResourceRange(range));
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

// Global Stats Calculation
function updateGlobalStats() {
  VibeHex.renderOverview(nodes);
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
  const settings = JSON.stringify([data.site_title || '', data.site_icon || '', data.site_theme || 'design']);
  if (settings === lastPublicSettings) return;
  lastPublicSettings = settings;
  if (data.site_title) {
    document.getElementById('siteTitle').textContent = data.site_title;
    document.title = data.site_title;
  }
  updateSiteIconDisplay(data.site_icon || '');
  window.VibeThemes.apply(data.site_theme || 'design');
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
