// VibeMonitor Modern Dashboard Script

let nodes = [];
let currentGroup = null;
let isAdmin = false;
let ws = null;
let pollTimer = null;

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
  document.getElementById(id).classList.add('active');
};

window.closeModal = function(id) {
  document.getElementById(id).classList.remove('active');
};

// Theme Toggle
const themeToggle = document.getElementById('themeToggle');
const savedTheme = localStorage.getItem('theme') || 'dark';
document.documentElement.setAttribute('data-theme', savedTheme);

themeToggle.addEventListener('click', () => {
  const current = document.documentElement.getAttribute('data-theme');
  const next = current === 'dark' ? 'light' : 'dark';
  document.documentElement.setAttribute('data-theme', next);
  localStorage.setItem('theme', next);
});

// Admin Authentication
function getAdminToken() {
  return localStorage.getItem('admin_token');
}

async function checkAdminAuth() {
  const token = getAdminToken();
  if (!token) {
    setAdminState(false);
    return;
  }
  try {
    const res = await fetch('/api/admin/status', {
      headers: { 'Authorization': 'Bearer ' + token }
    });
    const data = await res.json();
    setAdminState(data.is_admin === true);
  } catch (e) {
    setAdminState(false);
  }
}

function setAdminState(admin) {
  isAdmin = admin;
  const adminBtn = document.getElementById('adminBtn');
  const quickActions = document.getElementById('adminQuickActions');
  if (admin) {
    adminBtn.textContent = '⚙️ 管理';
    quickActions.style.display = 'flex';
  } else {
    adminBtn.textContent = '⚙️ 管理';
    quickActions.style.display = 'none';
  }
  renderNodes();
}

// Global Stats Calculation
function updateGlobalStats() {
  let totalNetUp = 0;
  let totalNetDown = 0;

  nodes.forEach(n => {
    if (n.last_report) {
      if (n.last_report.network) {
        totalNetUp += n.last_report.network.up || 0;
        totalNetDown += n.last_report.network.down || 0;
      }
    }
  });

  const up = document.getElementById('statNetUp');
  const down = document.getElementById('statNetDown');
  if (up) up.textContent = `↑ ${formatSpeed(totalNetUp)}`;
  if (down) down.textContent = `↓ ${formatSpeed(totalNetDown)}`;

  updateGroupButtons();
}

function updateGroupButtons() {
  const groups = new Set();
  nodes.forEach(n => {
    if (n.group) groups.add(n.group);
  });
  if (currentGroup !== null && !groups.has(currentGroup)) currentGroup = null;
  const container = document.getElementById('groupButtons');
  container.innerHTML = '';
  groups.forEach(g => {
    const btn = document.createElement('button');
    btn.className = `btn ${currentGroup === g ? 'btn-primary' : ''}`;
    btn.textContent = g;
    btn.onclick = () => {
      currentGroup = currentGroup === g ? null : g;
      updateGroupButtons();
      renderNodes();
    };
    container.appendChild(btn);
  });
}

function readNodeProfile(prefix) {
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
    currency: document.getElementById(prefix+'NodeCurrency').value};
}
function fillNodeProfile(prefix, profile) {
  const p = profile || {};
  document.getElementById(prefix+'NodeTargets').value = (p.targets || []).map(t => `${t.name},${t.host}`).join('\n');
  document.getElementById(prefix+'NodeDue').value = p.due_date || '';
  document.getElementById(prefix+'NodeCycle').value = p.payment_cycle || '';
  document.getElementById(prefix+'NodePrice').value = p.price || '';
  document.getElementById(prefix+'NodeCurrency').value = p.currency || 'CNY';
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
    return `<button class="ping-sample-row" data-action="ping" data-uuid="${escapeHtml(node.uuid)}" data-target="${escapeHtml(p.name)}" title="24 小时采样统计"><span class="ping-row-heading"><span><b class="target-dot target-${i%3}"></b>${escapeHtml(p.name)}</span><strong>${value}</strong></span><span class="sample-bars">${bars}</span></button>`;
  }).join('')}</div>`).join('')+'</div>';
}

// Render Nodes
function renderNodes() {
  const grid = document.getElementById('nodeGrid');
  const filtered = nodes.filter(n => currentGroup === null || n.group === currentGroup);

  if (filtered.length === 0) {
    grid.innerHTML = `
      <div style="grid-column: 1 / -1; text-align: center; padding: 60px 20px; color: var(--text-muted);">
        ${nodes.length === 0 ? '暂无监控节点。点击右上角“管理”添加节点开始监控。' : '没有匹配的节点'}
      </div>
    `;
    return;
  }

  grid.innerHTML = filtered.map(node => {
    const isOnline = node.online;
    const r = node.last_report || {};
    const info = node.basic_info || {};
    const cpu = r.cpu || {};
    const ram = r.ram || {};
    const disk = r.disk || {};
    const net = r.network || {};

    const cpuUsage = isOnline ? (cpu.usage || 0).toFixed(1) : 0;
    let ramPct = 0;
    if (isOnline && ram.total > 0) {
      ramPct = ((ram.used / ram.total) * 100).toFixed(1);
    }
    let diskPct = 0;
    if (isOnline && disk.total > 0) {
      diskPct = ((disk.used / disk.total) * 100).toFixed(1);
    }

    const cpuClass = cpuUsage > 90 ? 'critical' : cpuUsage > 75 ? 'high' : '';
    const ramClass = ramPct > 90 ? 'critical' : ramPct > 80 ? 'high' : '';
    const diskClass = diskPct > 90 ? 'critical' : diskPct > 80 ? 'high' : '';

    const trafficLimit = node.traffic_limit || 0;
    const cycleTotalUsed = node.cycle_total_used || 0;
    const cyclePercent = node.cycle_percent || (trafficLimit > 0 ? (cycleTotalUsed / trafficLimit * 100) : 0);
    const trafficClass = cyclePercent >= 90 ? 'critical' : cyclePercent >= 60 ? 'high' : '';

    const bill = billingDisplay(node.profile);
    const adminActions = '';

    return `
      <div class="node-card ${isOnline ? '' : 'offline'}">
        <div class="node-header">
          <div class="node-title-group">
            <span class="node-status-dot ${isOnline ? 'online' : ''}" title="${isOnline ? '在线' : '离线'}"></span>
            <div>
              <div class="node-name">${escapeHtml(node.name)}</div>
            </div>
          </div>
          <div class="node-badges">
            <span class="badge badge-region">${getRegionBadge(node.region)}</span>
            ${node.group ? `<span class="badge">${escapeHtml(node.group)}</span>` : ''}
          </div>
        </div>

        <div class="node-specs">
          <span>🖥️ ${escapeHtml(info.os || 'Linux')} (${escapeHtml(info.arch || 'x64')})</span>
          <span>⚡ ${info.cpu_cores || cpu.cores || 1}C</span>
          <span>⏱️ ${formatUptime(r.uptime)}</span>
          <span class="price-badge">${bill.price}</span>
          ${node.reset_day > 0 ? `<span class="billing-reset">♻️ ${node.reset_day}日重置</span>` : ''}
        </div>

        <div class="node-metrics">
        <!-- CPU Metric -->
        <div class="metric-row">
          <div class="metric-meta">
            <span class="metric-name">CPU</span>
            <span class="metric-value">${cpuUsage}%</span>
          </div>
          <div class="progress-track">
            <div class="progress-bar ${cpuClass}" style="width: ${cpuUsage}%;"></div>
          </div>
        </div>

        <!-- Memory Metric -->
        <div class="metric-row">
          <div class="metric-meta">
            <span class="metric-name">RAM</span>
            <span class="metric-value">${formatBytes(ram.used)} / ${formatBytes(ram.total)} (${ramPct}%)</span>
          </div>
          <div class="progress-track">
            <div class="progress-bar ${ramClass}" style="width: ${ramPct}%;"></div>
          </div>
        </div>

        <!-- Disk Metric -->
        <div class="metric-row">
          <div class="metric-meta">
            <span class="metric-name">Disk</span>
            <span class="metric-value">${formatBytes(disk.used)} / ${formatBytes(disk.total)} (${diskPct}%)</span>
          </div>
          <div class="progress-track">
            <div class="progress-bar ${diskClass}" style="width: ${diskPct}%;"></div>
          </div>
        </div>

        ${trafficLimit > 0 ? `
        <div class="metric-row">
          <div class="metric-meta">
            <span class="metric-name">Traffic</span>
            <span class="metric-value">${formatBytes(cycleTotalUsed)} / ${formatBytes(trafficLimit)} (${cyclePercent.toFixed(1)}%)</span>
          </div>
          <div class="progress-track">
            <div class="progress-bar ${trafficClass}" style="width: ${Math.min(cyclePercent, 100).toFixed(1)}%;"></div>
          </div>
        </div>
        ` : ''}

        </div>

        <div class="node-summary">
          <div class="summary-box"><span class="summary-value up">↑ ${formatSpeed(net.up || 0)}</span><span class="summary-value down">↓ ${formatSpeed(net.down || 0)}</span></div>
          <div class="summary-box"><span class="summary-value">↑ ${formatBytes(net.totalUp || 0)}</span><span class="summary-value">↓ ${formatBytes(net.totalDown || 0)}</span></div>
          <div class="summary-box"><span class="summary-value">▦ ${bill.remaining}</span><span class="summary-value">${bill.price}</span></div>
        </div>


        ${renderPingPanels(node)}

        ${adminActions}
      </div>
    `;
  }).join('');
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
async function fetchNodes() {
  try {
    const res = await fetch('/api/nodes');
    const data = await res.json();
    if (Array.isArray(data)) {
      nodes = data;
      updateGlobalStats();
      renderNodes();
    }
  } catch (e) {
    console.error('Failed to fetch nodes:', e);
  }
}

async function fetchPublicSettings() {
  try {
    const res = await fetch('/api/public');
    const data = await res.json();
    if (data.site_title) {
      document.getElementById('siteTitle').textContent = data.site_title;
      document.title = data.site_title;
    }
    if (data.announcement !== undefined) {
      document.getElementById('siteAnnouncement').textContent = data.announcement || '实时服务器探针与性能监控 · 极简纯净版';
    }
    if (data.site_icon) document.getElementById('siteIcon').href = data.site_icon;
  } catch (e) {
    console.error('Failed to fetch public settings:', e);
  }
}

async function fetchSettingsForAdmin() {
  try {
    const res = await fetch('/api/public');
    const data = await res.json();
    document.getElementById('settingSiteTitle').value = data.site_title || '';
    document.getElementById('settingAnnouncement').value = data.announcement || '';
    document.getElementById('settingSiteIcon').value = data.site_icon || '';
    document.getElementById('settingNewPassword').value = '';
  } catch (e) { console.error('Failed to fetch admin settings:', e); }
}

function connectWebSocket() {
  if (ws) {
    try { ws.close(); } catch(e) {}
  }
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const wsUrl = `${protocol}//${window.location.host}/api/clients`;
  ws = new WebSocket(wsUrl);

  ws.onopen = () => {
    console.log('WebSocket connected to', wsUrl);
    // Request initial data
    ws.send('get');
  };

  ws.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data);
      if (msg.nodes) {
        nodes = msg.nodes;
        updateGlobalStats();
        renderNodes();
      }
    } catch (e) {
      console.error('WS parse error:', e);
    }
  };

  ws.onclose = () => {
    console.warn('WebSocket disconnected, reconnecting in 3s...');
    setTimeout(connectWebSocket, 3000);
  };
}

// Admin Actions
window.showGuide = async function(uuid) {
  let token;
  try {
    const res = await fetch(`/api/admin/nodes/${encodeURIComponent(uuid)}/token`, {
      headers: { 'Authorization': 'Bearer ' + getAdminToken() }
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
      headers: { 'Authorization': 'Bearer ' + token }
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
document.getElementById('adminBtn').addEventListener('click', () => {
  if (isAdmin) {
    openModal('settingsModal');
    fetchSettingsForAdmin();
  } else {
    document.getElementById('loginError').style.display = 'none';
    document.getElementById('loginPassword').value = '';
    openModal('loginModal');
  }
});

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
      localStorage.setItem('admin_token', data.token);
      setAdminState(true);
      closeModal('loginModal');
    } else {
      document.getElementById('loginError').style.display = 'block';
    }
  } catch (e) {
    document.getElementById('loginError').textContent = '连接失败: ' + e.message;
    document.getElementById('loginError').style.display = 'block';
  }
});

document.getElementById('logoutBtn').addEventListener('click', async () => {
  try {
    const res = await fetch('/api/admin/logout', {
      method: 'POST', headers: { 'Authorization': 'Bearer ' + getAdminToken() }
    });
    if (!res.ok) throw new Error('退出失败，请重试');
    localStorage.removeItem('admin_token');
    document.getElementById('guideToken').textContent = '';
    document.getElementById('guideInstallCmd').textContent = '';
    document.getElementById('guideRunCmd').textContent = '';
    setAdminState(false);
  } catch (error) { alert(error.message); }
});

document.getElementById('settingsForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const token = getAdminToken();
  const newPassword = document.getElementById('settingNewPassword').value;
  if (newPassword && newPassword.length < 8) {
    alert('管理员密码至少需要 8 位');
    return;
  }
  try {
    const res = await fetch('/api/admin/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token },
      body: JSON.stringify({
        site_title: document.getElementById('settingSiteTitle').value.trim(),
        announcement: document.getElementById('settingAnnouncement').value.trim(),
        site_icon: document.getElementById('settingSiteIcon').value.trim(),
        new_password: newPassword
      })
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || '保存失败');
    if (newPassword) {
      localStorage.removeItem('admin_token');
      setAdminState(false);
    }
    closeModal('settingsModal');
    await fetchPublicSettings();
    alert(newPassword ? '设置已保存，请使用新密码重新登录' : '设置已保存');
  } catch (e) { alert(e.message); }
});

const nodeManagementMenu = document.getElementById('nodeManagementMenu');
nodeManagementMenu.addEventListener('keydown', (event) => {
  if (event.key === 'Escape') {
    nodeManagementMenu.open = false;
    nodeManagementMenu.querySelector('summary').focus();
  }
});
nodeManagementMenu.addEventListener('focusout', (event) => {
  if (!nodeManagementMenu.contains(event.relatedTarget)) nodeManagementMenu.open = false;
});
document.getElementById('editExistingNodeBtn').addEventListener('click', () => {
  nodeManagementMenu.open = false;
  if (!isAdmin) return;
  const list = document.getElementById('nodeSelectionList');
  list.innerHTML = '';
  if (nodes.length === 0) {
    list.textContent = '暂无节点，请先通过“节点管理 → 新建节点”创建。';
  }
  nodes.forEach(node => {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'btn';
    button.textContent = `${node.name || node.uuid}${node.group ? ' · ' + node.group : ''}`;
    button.addEventListener('click', () => {
      closeModal('selectNodeModal');
      openEditModal(node.uuid);
    });
    list.appendChild(button);
  });
  openModal('selectNodeModal');
});

const addNodeBtn = document.getElementById('addNodeBtn');
if (addNodeBtn) addNodeBtn.addEventListener('click', () => {
  nodeManagementMenu.open = false;
  if (!isAdmin) return;
  document.getElementById('newNodeName').value = '';
  document.getElementById('newNodeGroup').value = '';
  document.getElementById('newNodeTrafficLimit').value = '';
  document.getElementById('newNodeResetDay').value = '';
  document.getElementById('newNodeInitialUsed').value = '';
  fillNodeProfile('new', {});
  openModal('addNodeModal');
});

document.getElementById('addNodeForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const name = document.getElementById('newNodeName').value;
  const group = document.getElementById('newNodeGroup').value;
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
        'Authorization': 'Bearer ' + token
      },
      body: JSON.stringify({
        name,
        group,
        region,
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

window.openEditModal = function(uuid) {
  const node = nodes.find(n => n.uuid === uuid);
  if (!node) return;
  document.getElementById('editNodeUUID').value = node.uuid;
  document.getElementById('editNodeName').value = node.name || '';
  document.getElementById('editNodeGroup').value = node.group || '';
  document.getElementById('editNodeRegion').value = node.region || '';
  document.getElementById('editNodeTrafficLimit').value = node.traffic_limit > 0 ? (node.traffic_limit / (1024*1024*1024)).toFixed(1) : '';
  document.getElementById('editNodeResetDay').value = node.reset_day > 0 ? node.reset_day : '';
  document.getElementById('editNodeInitialUsed').value = node.initial_used > 0 ? (node.initial_used / (1024*1024*1024)).toFixed(1) : '';
  fillNodeProfile('edit', node.profile || {targets: (node.ping_preview || []).map(p => ({name:p.name,host:p.host}))});
  openModal('editNodeModal');
};

document.getElementById('editNodeForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const uuid = document.getElementById('editNodeUUID').value;
  const name = document.getElementById('editNodeName').value;
  const group = document.getElementById('editNodeGroup').value;
  const region = document.getElementById('editNodeRegion').value;
  const trafficLimitGB = parseFloat(document.getElementById('editNodeTrafficLimit').value) || 0;
  const resetDay = parseInt(document.getElementById('editNodeResetDay').value) || 0;
  const initialUsedGB = parseFloat(document.getElementById('editNodeInitialUsed').value) || 0;
  const token = getAdminToken();

  try {
    const res = await fetch(`/api/admin/nodes/${uuid}`, {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer ' + token
      },
      body: JSON.stringify({
        name,
        group,
        region,
        traffic_limit_gb: trafficLimitGB,
        reset_day: resetDay,
        initial_used_gb: initialUsedGB,
        profile: readNodeProfile('edit')
      })
    });
    if (res.ok) {
      closeModal('editNodeModal');
      fetchNodes();
    } else {
      alert('修改失败');
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

  document.getElementById('btnRange1h').classList.add('active');
  document.getElementById('btnRange24h').classList.remove('active');

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
  if (range === '1h') {
    document.getElementById('btnRange1h').classList.add('active');
    document.getElementById('btnRange24h').classList.remove('active');
  } else {
    document.getElementById('btnRange1h').classList.remove('active');
    document.getElementById('btnRange24h').classList.add('active');
  }
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

    // Time indicators
    if (cachedPingSamples.length > 0) {
      const firstT = new Date(cachedPingSamples[0].t * 1000);
      document.getElementById('chartTimeStart').textContent = firstT.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      const lastT = new Date(cachedPingSamples[cachedPingSamples.length - 1].t * 1000);
      document.getElementById('chartTimeEnd').textContent = lastT.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    } else {
      document.getElementById('chartTimeStart').textContent = currentPingRange === '1h' ? '1小时前' : '24小时前';
      document.getElementById('chartTimeEnd').textContent = '现在';
    }

    renderPingSvgChart(cachedPingSamples, currentPingRange);
  } catch (e) {
    renderPingSvgChart([], currentPingRange, e.message);
  }
}

function renderPingSvgChart(samples, range, errorMsg) {
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

  if (errorMsg || !samples || samples.length === 0) {
    svg.innerHTML = `
      <text x="${W / 2}" y="${H / 2}" text-anchor="middle" fill="#94a3b8" font-size="13" font-family="system-ui">
        ${errorMsg ? '获取数据失败: ' + errorMsg : '暂无历史时序数据，探针正在每 60 秒记录持久化中...'}
      </text>
    `;
    return;
  }

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

  // Calculate coordinates for points
  const points = samples.map((s, idx) => {
    const x = samples.length === 1 ? (padL + plotW / 2) : (padL + (idx / (samples.length - 1)) * plotW);
    const isLoss = s.l < 0;
    const y = isLoss ? (padT + plotH) : (padT + plotH - (s.l / maxLat) * plotH);
    return { x, y, l: s.l, t: s.t, isLoss };
  });

  // Build SVG path
  let pathD = '';
  let areaD = '';
  let lossDots = '';
  let hasValid = false;

  points.forEach((pt, i) => {
    if (i === 0) {
      pathD += `M ${pt.x.toFixed(1)} ${pt.y.toFixed(1)}`;
      areaD += `M ${pt.x.toFixed(1)} ${pt.y.toFixed(1)}`;
    } else {
      pathD += ` L ${pt.x.toFixed(1)} ${pt.y.toFixed(1)}`;
      areaD += ` L ${pt.x.toFixed(1)} ${pt.y.toFixed(1)}`;
    }
    if (pt.isLoss) {
      lossDots += `<circle cx="${pt.x.toFixed(1)}" cy="${pt.y.toFixed(1)}" r="4" fill="#f43f5e" />`;
    } else {
      hasValid = true;
    }
  });

  const lastPt = points[points.length - 1];
  const firstPt = points[0];
  areaD += ` L ${lastPt.x.toFixed(1)} ${padT + plotH} L ${firstPt.x.toFixed(1)} ${padT + plotH} Z`;

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
    <path d="${areaD}" fill="url(#${gradId})" />
    <path d="${pathD}" fill="none" stroke="${lineColor}" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" />
    ${lossDots}
    <line id="hoverLine" x1="0" y1="${padT}" x2="0" y2="${padT + plotH}" stroke="#94a3b8" stroke-width="1.5" stroke-dasharray="2,2" style="display: none;" />
    <circle id="hoverPoint" cx="0" cy="0" r="4.5" fill="#38bdf8" stroke="#ffffff" stroke-width="2" style="display: none;" />
    <rect x="${padL}" y="${padT}" width="${plotW}" height="${plotH}" fill="transparent" id="chartHitBox" style="cursor: crosshair;" />
  `;

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

    let closest = points[0];
    let minDiff = Infinity;
    points.forEach(pt => {
      const diff = Math.abs(pt.x - mouseX);
      if (diff < minDiff) {
        minDiff = diff;
        closest = pt;
      }
    });

    hoverLine.setAttribute('x1', closest.x);
    hoverLine.setAttribute('x2', closest.x);
    hoverLine.style.display = 'block';

    hoverPoint.setAttribute('cx', closest.x);
    hoverPoint.setAttribute('cy', closest.y);
    hoverPoint.setAttribute('fill', closest.isLoss ? '#f43f5e' : '#38bdf8');
    hoverPoint.style.display = 'block';

    if (tooltip) {
      const tDate = new Date(closest.t * 1000);
      const timeStr = tDate.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
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

// Initialize
checkAdminAuth();
fetchPublicSettings();
fetchNodes();
connectWebSocket();
// Periodic fallback polling every 5s
setInterval(fetchNodes, 5000);
