// VibeMonitor Modern Dashboard Script

let nodes = [];
let currentGroup = 'all';
let searchQuery = '';
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
    adminBtn.textContent = '🔒 已管理员认证';
    quickActions.style.display = 'flex';
  } else {
    adminBtn.textContent = '⚙️ 管理';
    quickActions.style.display = 'none';
  }
  renderNodes();
}

// Global Stats Calculation
function updateGlobalStats() {
  const total = nodes.length;
  const online = nodes.filter(n => n.online).length;
  document.getElementById('statTotal').textContent = total;
  document.getElementById('statOnline').textContent = online;

  let totalCpu = 0;
  let activeCpuCount = 0;
  let totalMemUsed = 0;
  let totalMemMax = 0;
  let totalNetUp = 0;
  let totalNetDown = 0;

  nodes.forEach(n => {
    if (n.online && n.last_report) {
      if (n.last_report.cpu) {
        totalCpu += n.last_report.cpu.usage || 0;
        activeCpuCount++;
      }
      if (n.last_report.ram) {
        totalMemUsed += n.last_report.ram.used || 0;
        totalMemMax += n.last_report.ram.total || 0;
      }
      if (n.last_report.network) {
        totalNetUp += n.last_report.network.up || 0;
        totalNetDown += n.last_report.network.down || 0;
      }
    }
  });

  const avgCpu = activeCpuCount > 0 ? (totalCpu / activeCpuCount).toFixed(1) + '%' : '0%';
  document.getElementById('statAvgCPU').textContent = avgCpu;
  document.getElementById('statMemUsed').textContent = formatBytes(totalMemUsed);
  document.getElementById('statMemTotal').textContent = formatBytes(totalMemMax);
  document.getElementById('statNetUp').textContent = formatSpeed(totalNetUp);
  document.getElementById('statNetDown').textContent = formatSpeed(totalNetDown);

  const offline = total - online;
  const statusEl = document.getElementById('statStatusText');
  if (statusEl) {
    statusEl.textContent = offline > 0 ? `${offline} 台服务器离线` : '全部运行正常';
    statusEl.style.color = offline > 0 ? 'var(--warning)' : 'var(--muted-foreground)';
  }

  const trafficEl = document.getElementById('statTrafficTotal');
  if (trafficEl) {
    trafficEl.textContent = `实时上下行总和: ${formatSpeed(totalNetUp + totalNetDown)}`;
  }

  updateGroupButtons();
}

function updateGroupButtons() {
  const groups = new Set(['all']);
  nodes.forEach(n => {
    if (n.group) groups.add(n.group);
  });
  const container = document.getElementById('groupButtons');
  container.innerHTML = '';
  groups.forEach(g => {
    const btn = document.createElement('button');
    btn.className = `btn ${currentGroup === g ? 'btn-primary' : ''}`;
    btn.textContent = g === 'all' ? '全部' : g;
    btn.onclick = () => {
      currentGroup = g;
      updateGroupButtons();
      renderNodes();
    };
    container.appendChild(btn);
  });
}

// Render Nodes
function renderNodes() {
  const grid = document.getElementById('nodeGrid');
  const filtered = nodes.filter(n => {
    const matchGroup = currentGroup === 'all' || n.group === currentGroup;
    const q = searchQuery.toLowerCase();
    const matchSearch = !q ||
      n.name.toLowerCase().includes(q) ||
      (n.region && n.region.toLowerCase().includes(q)) ||
      (n.group && n.group.toLowerCase().includes(q)) ||
      (n.client_ip && n.client_ip.toLowerCase().includes(q));
    return matchGroup && matchSearch;
  });

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
    const load = r.load || {};

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
    const cycleRemaining = node.cycle_remaining !== undefined ? node.cycle_remaining : (trafficLimit - cycleTotalUsed);
    const cyclePercent = node.cycle_percent || (trafficLimit > 0 ? (cycleTotalUsed / trafficLimit * 100) : 0);
    const resetDay = node.reset_day || 1;
    const daysUntilReset = node.days_until_reset !== undefined ? node.days_until_reset : 0;
    const trafficClass = cyclePercent >= 90 ? 'critical' : cyclePercent >= 60 ? 'high' : '';
    const trafficColor = cyclePercent >= 90 ? 'var(--destructive)' : cyclePercent >= 60 ? 'var(--warning)' : 'var(--success)';

    const adminActions = isAdmin ? `
      <div style="display: flex; gap: 6px; margin-top: 6px; flex-wrap: wrap;">
        <button class="btn" style="padding: 4px 8px; font-size: 11px;" data-action="edit" data-uuid="${escapeHtml(node.uuid)}">编辑</button>
        <button class="btn" style="padding: 4px 8px; font-size: 11px;" data-action="guide" data-uuid="${escapeHtml(node.uuid)}">接入命令</button>
        <button class="btn btn-danger" style="padding: 4px 8px; font-size: 11px;" data-action="delete" data-uuid="${escapeHtml(node.uuid)}">删除</button>
      </div>
    ` : '';

    return `
      <div class="node-card ${isOnline ? '' : 'offline'}">
        <div class="node-header">
          <div class="node-title-group">
            <span class="node-status-dot ${isOnline ? 'online' : ''}" title="${isOnline ? '在线' : '离线'}"></span>
            <div>
              <div class="node-name">${escapeHtml(node.name)}</div>
              <div style="font-size: 11px; color: var(--text-muted);">${escapeHtml(node.client_ip || '等待连接')}</div>
            </div>
          </div>
          <div class="node-badges">
            <span class="badge badge-region">${getRegionBadge(node.region)}</span>
            ${node.group ? `<span class="badge">${escapeHtml(node.group)}</span>` : ''}
          </div>
        </div>

        <div class="node-specs">
          <span>🖥️ ${escapeHtml(info.os || 'Linux')} (${escapeHtml(info.arch || 'x64')})</span>
          <span>⚡ ${info.cpu_cores || cpu.cores || 1} 核</span>
          <span>⏱️ 在线: ${formatUptime(r.uptime)}</span>
        </div>

        <!-- CPU Metric -->
        <div class="metric-row">
          <div class="metric-meta">
            <span class="metric-name">CPU (${escapeHtml(info.cpu_name || cpu.name || 'Processor')})</span>
            <span class="metric-value">${cpuUsage}%</span>
          </div>
          <div class="progress-track">
            <div class="progress-bar ${cpuClass}" style="width: ${cpuUsage}%;"></div>
          </div>
        </div>

        <!-- Memory Metric -->
        <div class="metric-row">
          <div class="metric-meta">
            <span class="metric-name">内存</span>
            <span class="metric-value">${formatBytes(ram.used)} / ${formatBytes(ram.total)} (${ramPct}%)</span>
          </div>
          <div class="progress-track">
            <div class="progress-bar ${ramClass}" style="width: ${ramPct}%;"></div>
          </div>
        </div>

        <!-- Disk Metric -->
        <div class="metric-row">
          <div class="metric-meta">
            <span class="metric-name">存储空间</span>
            <span class="metric-value">${formatBytes(disk.used)} / ${formatBytes(disk.total)} (${diskPct}%)</span>
          </div>
          <div class="progress-track">
            <div class="progress-bar ${diskClass}" style="width: ${diskPct}%;"></div>
          </div>
        </div>

        <!-- Traffic Quota Metric (sum billing mode) -->
        ${trafficLimit > 0 ? `
        <div class="metric-row">
          <div class="metric-meta">
            <span class="metric-name">周期流量 (每月 ${resetDay} 号重置 · 还有 ${daysUntilReset} 天)</span>
            <span class="metric-value" style="color: ${trafficColor};">
              ${cycleRemaining >= 0 ? `剩余 ${formatBytes(cycleRemaining)}` : `超额 ${formatBytes(-cycleRemaining)}`}
            </span>
          </div>
          <div class="progress-track">
            <div class="progress-bar ${trafficClass}" style="width: ${Math.min(cyclePercent, 100).toFixed(1)}%;"></div>
          </div>
          <div style="display: flex; justify-content: space-between; font-size: 11px; color: var(--muted-foreground);">
            <span>已用: ${formatBytes(cycleTotalUsed)}</span>
            <span>配额: ${formatBytes(trafficLimit)} (${cyclePercent.toFixed(1)}%)</span>
          </div>
        </div>
        ` : ''}

        <!-- Mini Sparkline for CPU -->
        <div class="sparkline-box">
          ${generateSparkline(node.history, 'cpu_usage')}
        </div>

        <!-- Ping Latency Badges -->
        ${(r.ping_results && r.ping_results.length > 0) ? `
        <div class="node-ping-section">
          <div class="ping-header">
            <span>📶 延迟监测</span>
            <span style="cursor: pointer; color: var(--primary); font-size: 11px;" data-action="ping" data-uuid="${escapeHtml(node.uuid)}" data-target="${escapeHtml(r.ping_results[0].name)}">📈 波动图 ›</span>
          </div>
          <div class="ping-badges">
            ${r.ping_results.map(p => {
              let pillClass = 'good';
              let text = `${p.latency}ms`;
              if (p.latency < 0) {
                pillClass = 'bad';
                text = '超时';
              } else if (p.latency > 250) {
                pillClass = 'bad';
              } else if (p.latency > 150) {
                pillClass = 'warn';
              }
              return `
                <div class="ping-pill ${pillClass}" title="${escapeHtml(p.host)} · ${escapeHtml(p.method || '自动探测')} (点击查看 1h/24h 波动曲线)" data-action="ping" data-uuid="${escapeHtml(node.uuid)}" data-target="${escapeHtml(p.name)}">
                  <span class="ping-dot"></span>
                  <span>${escapeHtml(p.name)}:</span>
                  <span>${text}</span>
                </div>
              `;
            }).join('')}
          </div>
        </div>
        ` : ''}

        <!-- Footer: Network & Load -->
        <div class="node-footer">
          <div class="footer-item">
            <span class="footer-label">网络传输</span>
            <span class="footer-val">↑ ${formatSpeed(net.up || 0)}</span>
            <span class="footer-val">↓ ${formatSpeed(net.down || 0)}</span>
          </div>
          <div class="footer-item">
            <span class="footer-label">负载 & 总流量</span>
            <span class="footer-val">Load: ${(load.load1 || 0).toFixed(2)}</span>
            <span class="footer-val" style="color: var(--text-muted); font-size: 11px;">总: ${formatBytes((net.totalUp || 0) + (net.totalDown || 0))}</span>
          </div>
        </div>

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
      document.getElementById('settingSiteTitle').value = data.site_title;
    }
    if (data.announcement !== undefined) {
      document.getElementById('siteAnnouncement').textContent = data.announcement || '实时服务器探针与性能监控 · 极简纯净版';
      document.getElementById('settingAnnouncement').value = data.announcement || '';
    }
    if (Array.isArray(data.ping_targets)) {
      const lines = data.ping_targets.map(t => `${t.name},${t.host}`).join('\n');
      document.getElementById('settingPingTargets').value = lines;
    }
  } catch (e) {
    console.error('Failed to fetch public settings:', e);
  }
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
  document.getElementById('guideToken').textContent = token;
  openModal('nodeGuideModal');
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
      body: JSON.stringify({ password })
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

document.getElementById('addNodeBtn').addEventListener('click', () => {
  document.getElementById('newNodeName').value = '';
  document.getElementById('newNodeGroup').value = '';
  document.getElementById('newNodeTrafficLimit').value = '';
  document.getElementById('newNodeResetDay').value = '';
  document.getElementById('newNodeInitialUsed').value = '';
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
        initial_used_gb: initialUsedGB
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
        initial_used_gb: initialUsedGB
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

document.getElementById('settingsBtn').addEventListener('click', () => {
  openModal('settingsModal');
});

document.getElementById('settingsForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const siteTitle = document.getElementById('settingSiteTitle').value;
  const announcement = document.getElementById('settingAnnouncement').value;
  const newPassword = document.getElementById('settingNewPassword').value;
  const rawPingText = document.getElementById('settingPingTargets').value;
  const token = getAdminToken();

  // Parse lines: Name,Host
  const pingTargets = [];
  const lines = rawPingText.split('\n');
  for (let line of lines) {
    line = line.trim();
    if (!line) continue;
    const parts = line.split(',');
    if (parts.length >= 2) {
      const name = parts[0].trim();
      const host = parts.slice(1).join(',').trim();
      if (name && host) {
        pingTargets.push({ name, host });
      }
    } else {
      pingTargets.push({ name: line, host: line });
    }
  }

  try {
    const res = await fetch('/api/admin/settings', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer ' + token
      },
      body: JSON.stringify({
        site_title: siteTitle,
        announcement,
        new_password: newPassword,
        ping_targets: pingTargets
      })
    });
    if (res.ok) {
      if (newPassword) { localStorage.removeItem('admin_token'); setAdminState(false); }
      alert(newPassword ? '密码已修改，请重新登录' : '设置已保存');
      closeModal('settingsModal');
      fetchPublicSettings();
    } else {
      alert('保存失败');
    }
  } catch (e) {
    alert('请求失败: ' + e.message);
  }
});

document.getElementById('searchInput').addEventListener('input', (e) => {
  searchQuery = e.target.value;
  renderNodes();
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
  if (node && node.last_report && node.last_report.ping_results && node.last_report.ping_results.length > 0) {
    if (!currentPingTarget) {
      currentPingTarget = node.last_report.ping_results[0].name;
    }
    node.last_report.ping_results.forEach(pr => {
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

    if (data.host) {
      document.getElementById('pingModalSubtitle').textContent = `目标: ${data.target} (${data.host}) · 历史方式: ${{tcp: 'TCP', icmp: 'ICMP', unknown: '未标明'}[data.method] || '暂无采样'}`;
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
