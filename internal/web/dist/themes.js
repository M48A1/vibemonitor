// Hex is a native VibeMonitor adaptation inspired by 8bitNull/monitor-theme-hex.
window.VibeHex = (() => {
  const number = value => Number.isFinite(Number(value)) && value != null ? Number(value) : null;
  const percent = (used, total) => number(total) > 0 && number(used) != null ? Math.max(0, Math.min(100, Number(used) / Number(total) * 100)) : null;

  function metric(label, value, detail) {
    const pct = value == null ? null : Math.max(0, Math.min(100, value));
    const level = pct >= 90 ? 'critical' : pct >= 80 ? 'high' : '';
    return `<div class="hex-metric"><div class="metric-meta"><span class="metric-name">${label}</span><strong>${pct == null ? '—' : pct.toFixed(1) + '%'}</strong></div><div class="progress-track"><div class="progress-bar ${level}" style="width:${pct || 0}%"></div></div><small>${detail}</small></div>`;
  }

  function renderNode(node) {
    const r = node.last_report || {};
    const info = node.basic_info || {};
    const live = node.online && !!node.last_report;
    const cpu = r.cpu || {}, ram = r.ram || {}, disk = r.disk || {}, net = r.network || {};
    const bill = billingDisplay(node.profile);
    const capacity = data => number(data.total) > 0 ? (live && number(data.used) != null ? `${formatBytes(data.used)} / ` : '容量 ') + formatBytes(data.total) : '等待上报';
    const speed = value => live && number(value) != null ? formatSpeed(value) : '—';
    const used = number(node.cycle_total_used);
    const limit = number(node.traffic_limit);
    const traffic = limit > 0 ? percent(used, limit) : null;
    return `<article class="node-card hex-card ${node.online ? '' : 'offline'}">
      <div class="hex-node-header"><div class="hex-node-identity"><span class="hex-region">${getRegionBadge(node.region)}</span><h2>${escapeHtml(node.name)}</h2><span class="hex-system">${escapeHtml(info.os || '等待上报')} ${info.arch ? '· ' + escapeHtml(info.arch) : ''}</span></div><span class="hex-status ${node.online ? 'is-online' : ''}"><i></i>${node.online ? '在线' : '离线'}</span></div>
      <div class="hex-resources">
        ${metric('CPU', live ? number(cpu.usage) : null, `${info.cpu_cores || cpu.cores || '—'} 核`)}
        ${metric('内存', live ? percent(ram.used, ram.total) : null, capacity(ram))}
        ${metric('硬盘', live ? percent(disk.used, disk.total) : null, capacity(disk))}
        ${metric('周期流量', traffic, limit > 0 ? `${formatBytes(used)} / ${formatBytes(limit)}` : '未设置流量配额')}
      </div>
      <div class="hex-network"><div><span>↑ 上行</span><strong>${speed(net.up)}</strong></div><div><span>↓ 下行</span><strong>${speed(net.down)}</strong></div></div>
      <div class="hex-facts"><div><span>累计上传</span><strong>${number(net.totalUp) == null ? '—' : formatBytes(net.totalUp)}</strong></div><div><span>累计下载</span><strong>${number(net.totalDown) == null ? '—' : formatBytes(net.totalDown)}</strong></div><div><span>在线时长</span><strong>${live && number(r.uptime) != null ? formatUptime(r.uptime) : '—'}</strong></div></div>
      <div class="hex-probes">${renderPingPanels(node)}</div>
      <div class="hex-billing"><div><span>到期时间</span><strong>${bill.date}</strong><small>${bill.remaining}</small></div><div><span>流量重置</span><strong>${node.reset_day > 0 ? '每月 ' + node.reset_day + ' 日' : '未设置'}</strong><small>${node.reset_day > 0 && node.days_until_reset != null ? (node.days_until_reset === 0 ? '今日重置' : '剩余 ' + node.days_until_reset + ' 天') : '按节点账期统计'}</small></div></div>
      <footer class="hex-card-footer"><strong>${bill.price}</strong></footer>
    </article>`;
  }

  function renderOverview(nodes) {
    const live = nodes.filter(n => n.online && n.last_report);
    const online = nodes.filter(n => n.online).length;
    const total = field => live.reduce((sum, n) => sum + (number((n.last_report.network || {})[field]) || 0), 0);
    const busy = live.filter(n => number((n.last_report.cpu || {}).usage) >= 85).length;
    const traffic = nodes.reduce((sum, n) => sum + (number(n.cycle_total_used) || 0), 0);
    document.getElementById('statsBanner').innerHTML = `
      <div class="hex-stat"><span>节点状态</span><strong>${online}<em> / ${nodes.length}</em></strong><small><i class="hex-live-dot"></i>${online} 个在线 · ${nodes.length - online} 个离线</small></div>
      <div class="hex-stat"><span>周期已用流量</span><strong>${formatBytes(traffic)}</strong><small>各节点当前计费周期合计</small></div>
      <div class="hex-stat"><span>实时网速</span><strong>${formatSpeed(total('up') + total('down'))}</strong><small class="hex-stat-network"><span>↑ ${formatSpeed(total('up'))}</span><span>↓ ${formatSpeed(total('down'))}</span></small></div>
      <div class="hex-stat"><span>高负载节点</span><strong>${busy}<em> 台</em></strong><small>当前在线节点 CPU ≥ 85%</small></div>`;
  }
  return Object.freeze({ renderNode, renderOverview });
})();
