// Hex is a native VibeMonitor adaptation inspired by 8bitNull/monitor-theme-hex.
window.VibeHex = (() => {
  const number = value => Number.isFinite(Number(value)) && value != null ? Number(value) : null;
  const percent = (used, total) => number(total) > 0 && number(used) != null ? Math.max(0, Math.min(100, Number(used) / Number(total) * 100)) : null;
  let overviewFields = null;

  function metric(label, value, detail, uuid, resource) {
    const pct = value == null ? null : Math.max(0, Math.min(100, value));
    const level = pct >= 90 ? 'critical' : pct >= 80 ? 'high' : '';
    const interactive = resource === 'cpu' || resource === 'memory';
    const tag = interactive ? 'button' : 'div';
    const attributes = interactive ? ` type="button" data-action="resource" data-uuid="${escapeHtml(uuid)}" data-metric="${resource}" title="查看${label}历史曲线" aria-label="查看${label}历史曲线"` : '';
    return `<${tag} class="hex-metric${interactive ? ' metric-button' : ''}"${attributes}><div class="metric-meta"><span class="metric-name">${label}</span><strong>${pct == null ? '—' : pct.toFixed(1) + '%'}</strong></div><div class="progress-track"><div class="progress-bar ${level}" style="width:${pct || 0}%"></div></div><small>${detail}</small></${tag}>`;
  }

  function renderNode(node) {
    const r = node.last_report || {};
    const info = node.basic_info || {};
    const live = node.online && !!node.last_report;
    const cpu = r.cpu || {}, ram = r.ram || {}, disk = r.disk || {}, net = r.network || {};
    const bill = billingDisplay(node.profile);
    const capacity = data => number(data.total) > 0 ? (live && number(data.used) != null ? `${formatBytes(data.used)} / ` : '容量 ') + formatBytes(data.total) : '等待上报';
    const speed = value => live && number(value) != null ? formatSpeed(value) : '—';
    const totalRate = number(net.up) != null && number(net.down) != null ? number(net.up) + number(net.down) : null;
    const used = number(node.cycle_total_used);
    const limit = number(node.traffic_limit);
    const traffic = limit > 0 ? percent(used, limit) : null;
    return `<article class="node-card hex-card ${node.online ? '' : 'offline'}" data-node-id="${escapeHtml(node.uuid)}">
      <div class="hex-node-header"><div class="hex-node-identity"><span class="hex-region">${getRegionBadge(node.region)}</span><h2>${escapeHtml(node.name)}</h2><span class="hex-system">${escapeHtml(info.os || '等待上报')} ${info.arch ? '· ' + escapeHtml(info.arch) : ''}</span></div><span class="hex-status ${node.online ? 'is-online' : ''}"><i></i>${node.online ? '在线' : '离线'}</span></div>
      <div class="hex-resources">
        ${metric('CPU', live ? number(cpu.usage) : null, `${info.cpu_cores || cpu.cores || '—'} 核`, node.uuid, 'cpu')}
        ${metric('内存', live ? percent(ram.used, ram.total) : null, capacity(ram), node.uuid, 'memory')}
        ${metric('硬盘', live ? percent(disk.used, disk.total) : null, capacity(disk))}
        ${metric('周期流量', traffic, limit > 0 ? `${formatBytes(used)} / ${formatBytes(limit)}` : '未设置流量配额')}
      </div>
      <div class="hex-network"><div><span>↑ 上行</span><strong>${speed(net.up)}</strong></div><button class="hex-network-total" type="button" data-action="resource" data-uuid="${escapeHtml(node.uuid)}" data-metric="network" title="查看实时速率历史曲线" aria-label="查看实时速率历史曲线"><span>实时速率</span><strong>${speed(totalRate)}</strong></button><div><span>↓ 下行</span><strong>${speed(net.down)}</strong></div></div>
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
    const memoryUsages = live.map(n => {
      const ram = n.last_report.ram || {};
      return percent(ram.used, ram.total);
    }).filter(usage => usage != null);
    const memoryBusy = memoryUsages.filter(usage => usage >= 85).length;
    const busy = live.filter(n => {
      const threshold = number((n.profile || {}).cpu_threshold);
      const usage = number((n.last_report.cpu || {}).usage);
      return threshold != null && usage != null && usage >= threshold;
    }).length;
    const up = total('up');
    const down = total('down');
    const values = {
      online: String(online), count: String(nodes.length),
      status: `${online} 个在线 · ${nodes.length - online} 个离线`,
      memoryBusy: String(memoryBusy),
      memoryStatus: memoryUsages.length ? `内存使用率 ≥ 85% · 已监测 ${memoryUsages.length} 台` : '暂无内存数据',
      speed: formatSpeed(up + down),
      up: `↑ ${formatSpeed(up)}`, down: `↓ ${formatSpeed(down)}`,
      busy: String(busy),
    };
    const banner = document.getElementById('statsBanner');
    if (!overviewFields) {
      banner.innerHTML = `
        <div class="hex-stat"><span>节点状态</span><strong><span data-stat="online">${values.online}</span><em> / <span data-stat="count">${values.count}</span></em></strong><small><i class="hex-live-dot"></i><span data-stat="status">${values.status}</span></small></div>
        <div class="hex-stat"><span>内存紧张节点</span><strong><span data-stat="memoryBusy">${values.memoryBusy}</span><em> 台</em></strong><small data-stat="memoryStatus">${values.memoryStatus}</small></div>
        <div class="hex-stat"><span>实时网速</span><strong data-stat="speed">${values.speed}</strong><small class="hex-stat-network"><span data-stat="up">${values.up}</span><span data-stat="down">${values.down}</span></small></div>
        <div class="hex-stat"><span>高负载节点</span><strong><span data-stat="busy">${values.busy}</span><em> 台</em></strong><small>负载阈值请在节点信息内修改</small></div>`;
      if (banner.querySelectorAll) {
        overviewFields = Object.fromEntries(Array.from(banner.querySelectorAll('[data-stat]')).map(element => [element.dataset.stat, element]));
      }
    } else {
      for (const [name, value] of Object.entries(values)) {
        if (overviewFields[name].textContent !== value) overviewFields[name].textContent = value;
      }
    }
    if (window.VibeGlobe) window.VibeGlobe.setNodes(nodes);
  }
  return Object.freeze({ renderNode, renderOverview });
})();
