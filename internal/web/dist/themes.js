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

  function serverStatusBar(value) {
    const pct = value == null ? null : Math.max(0, Math.min(100, value));
    const level = pct >= 90 ? ' critical' : pct >= 80 ? ' warn' : '';
    return `<span class="ss-bar${level}"><i style="width:${pct || 0}%"></i><b>${pct == null ? '—' : pct.toFixed(1) + '%'}</b></span>`;
  }

  function renderServerStatusNode(node) {
    const r = node.last_report || {};
    const info = node.basic_info || {};
    const live = node.online && !!node.last_report;
    const cpu = r.cpu || {}, ram = r.ram || {}, disk = r.disk || {}, net = r.network || {};
    const load = r.load || {}, swap = r.swap || {}, connections = r.connections || {};
    const bill = billingDisplay(node.profile);
    const cpuUsage = live ? number(cpu.usage) : null;
    const ramUsage = live ? percent(ram.used, ram.total) : null;
    const diskUsage = live ? percent(disk.used, disk.total) : null;
    const used = number(node.cycle_total_used);
    const limit = number(node.traffic_limit);
    const trafficUsage = limit > 0 ? percent(used, limit) : null;
    const speed = value => live && number(value) != null ? formatSpeed(value) : '—';
    const capacity = data => number(data.total) > 0 ? `${formatBytes(data.used || 0)} / ${formatBytes(data.total)}` : '等待上报';
    const detail = (label, value) => `<div class="ss-detail"><span class="ss-detail-label">${label}</span><span class="ss-detail-value">${value}</span></div>`;
    const rate = number(connections.tcp_new_per_sec);
    return `<article class="node-card ss-node ${node.online ? '' : 'offline'}" data-node-id="${escapeHtml(node.uuid)}">
      <details class="ss-details">
        <summary class="ss-row">
          <span class="ss-cell ss-status"><i class="ss-dot ${node.online ? 'is-online' : ''}"></i><span>${node.online ? '在线' : '离线'}</span></span>
          <span class="ss-cell ss-name" title="${escapeHtml(node.name)}"><strong>${escapeHtml(node.name)}</strong></span>
          <span class="ss-cell ss-region">${getRegionBadge(node.region)}</span>
          <span class="ss-cell ss-system" title="${escapeHtml(info.os || '等待上报')}">${escapeHtml(info.os || '等待上报')}</span>
          <span class="ss-cell ss-uptime">${live ? formatUptime(r.uptime) : '—'}</span>
          <span class="ss-cell ss-expiry">${bill.remaining}</span>
          <span class="ss-cell ss-load">${live && number(load.load1) != null ? number(load.load1).toFixed(2) : '—'}</span>
          <span class="ss-cell ss-network"><span>↓ ${speed(net.down)}</span><span>↑ ${speed(net.up)}</span></span>
          <span class="ss-cell ss-traffic">${serverStatusBar(trafficUsage)}</span>
          <span class="ss-cell ss-cpu">${serverStatusBar(cpuUsage)}</span>
          <span class="ss-cell ss-memory">${serverStatusBar(ramUsage)}</span>
          <span class="ss-cell ss-disk">${serverStatusBar(diskUsage)}</span>
        </summary>
        <div class="ss-expanded">
          <div class="ss-detail-grid">
            ${detail('系统', `${escapeHtml(info.os || '等待上报')}${info.arch ? ' · ' + escapeHtml(info.arch) : ''}`)}
            ${detail('CPU', `${escapeHtml(info.cpu_name || cpu.name || '—')} · ${escapeHtml(info.cpu_cores || cpu.cores || '—')} 核`)}
            ${detail('负载', live ? [load.load1, load.load5, load.load15].map(v => (number(v) || 0).toFixed(2)).join(' / ') : '—')}
            ${detail('内存', live ? capacity(ram) : '—')}
            ${detail('交换', live && number(swap.total) > 0 ? capacity(swap) : '未启用')}
            ${detail('硬盘', live ? capacity(disk) : '—')}
            ${detail('进程 / 连接', live ? `${number(r.process) ?? '—'} · TCP ${number(connections.tcp) ?? '—'} · UDP ${number(connections.udp) ?? '—'}` : '—')}
            ${detail('TCP 建连速率', live && rate != null ? `${rate.toFixed(1)} 次/s` : '—')}
            ${detail('网速', `↓ ${speed(net.down)} · ↑ ${speed(net.up)}`)}
            ${detail('累计流量', live ? `↓ ${formatBytes(net.totalDown || 0)} · ↑ ${formatBytes(net.totalUp || 0)}` : '—')}
            ${detail('周期流量', limit > 0 ? `${formatBytes(used || 0)} / ${formatBytes(limit)}` : '未设置配额')}
            ${detail('在线时长', live ? formatUptime(r.uptime) : '—')}
            ${detail('到期时间', `${bill.date} · ${bill.remaining}`)}
            ${detail('流量重置', node.reset_day > 0 ? `每月 ${node.reset_day} 日${node.days_until_reset != null ? ' · 剩余 ' + node.days_until_reset + ' 天' : ''}` : '未设置')}
            ${detail('费用金额', bill.price)}
          </div>
          <div class="ss-actions">
            <button class="btn" type="button" data-action="resource" data-uuid="${escapeHtml(node.uuid)}" data-metric="cpu">CPU 历史</button>
            <button class="btn" type="button" data-action="resource" data-uuid="${escapeHtml(node.uuid)}" data-metric="memory">内存历史</button>
            <button class="btn" type="button" data-action="resource" data-uuid="${escapeHtml(node.uuid)}" data-metric="network">网速历史</button>
          </div>
          <div class="hex-probes">${renderPingPanels(node)}</div>
        </div>
      </details>
    </article>`;
  }

  // Only fields that influence the card. Report timestamps, history arrays and
  // per-interface accounting counters do not invalidate its rendered markup.
  function renderKey(node) {
    const r = node.last_report || {}, net = r.network || {};
    const siteTheme = document.documentElement.dataset.siteTheme || 'hex';
    return JSON.stringify([
      node.uuid, node.name, node.region, node.online, node.basic_info, node.profile,
      node.ping_preview, node.traffic_limit, node.cycle_total_used,
      node.reset_day, node.days_until_reset, !!node.last_report,
      r.cpu, r.ram, r.disk, r.ping_results, Math.floor((r.uptime || 0) / 60),
      net.up, net.down, net.totalUp, net.totalDown,
      siteTheme, siteTheme === 'serverstatus' ? [r.swap, r.load, r.connections, r.process] : null,
      new Date().toDateString()
    ]);
  }

  function renderNode(node) {
    if (document.documentElement.dataset.siteTheme === 'serverstatus') return renderServerStatusNode(node);
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
    const pingData = live.map(n => {
      const results = n.last_report.ping_results || [];
      const previews = n.ping_preview || [];
      const measured = results.some(p => number(p.latency) != null) || previews.some(p => number(p.loss) != null);
      const abnormal = results.some(p => number(p.latency) != null && number(p.latency) < 0) ||
        previews.some(p => number(p.loss) != null && number(p.loss) >= 20);
      return { measured, abnormal };
    });
    const measuredPing = pingData.filter(p => p.measured).length;
    const abnormalPing = pingData.filter(p => p.abnormal).length;
    const connectionRates = live.map(n => number((n.last_report.connections || {}).tcp_new_per_sec))
      .filter(rate => rate != null);
    const connectionRate = connectionRates.reduce((sum, rate) => sum + rate, 0);
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
      abnormalPing: String(abnormalPing),
      pingStatus: measuredPing ? `当前超时或近 24 小时丢包 ≥ 20% · 已监测 ${measuredPing} 台` : '暂无探测数据',
      connectionRate: connectionRates.length ? `${connectionRate.toLocaleString('zh-CN', { minimumFractionDigits: 1, maximumFractionDigits: 1 })} 次/s` : '—',
      connectionStatus: connectionRates.length ? `TCP 建连尝试/秒 · 已监测 ${connectionRates.length} 台` : '暂无连接速率数据',
    };
    const banner = document.getElementById('statsBanner');
    if (!overviewFields) {
      banner.innerHTML = `
        <div class="hex-stat"><span>节点状态</span><strong><span data-stat="online">${values.online}</span><em> / <span data-stat="count">${values.count}</span></em></strong><small><i class="hex-live-dot"></i><span data-stat="status">${values.status}</span></small></div>
        <div class="hex-stat"><span>内存紧张节点</span><strong><span data-stat="memoryBusy">${values.memoryBusy}</span><em> 台</em></strong><small data-stat="memoryStatus">${values.memoryStatus}</small></div>
        <div class="hex-stat"><span>实时网速</span><strong data-stat="speed">${values.speed}</strong><small class="hex-stat-network"><span data-stat="up">${values.up}</span><span data-stat="down">${values.down}</span></small></div>
        <div class="hex-stat"><span>高负载节点</span><strong><span data-stat="busy">${values.busy}</span><em> 台</em></strong><small>负载阈值请在节点信息内修改</small></div>
        <div class="hex-stat"><span>探测异常节点</span><strong><span data-stat="abnormalPing">${values.abnormalPing}</span><em> 台</em></strong><small data-stat="pingStatus">${values.pingStatus}</small></div>
        <div class="hex-stat"><span>连接速率</span><strong data-stat="connectionRate">${values.connectionRate}</strong><small data-stat="connectionStatus">${values.connectionStatus}</small></div>`;
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
  return Object.freeze({ renderNode, renderOverview, renderKey });
})();
