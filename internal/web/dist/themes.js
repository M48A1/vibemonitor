// Hex is a native VibeMonitor adaptation inspired by 8bitNull/monitor-theme-hex.
window.VibeHex = (() => {
  const number = value => Number.isFinite(Number(value)) && value != null ? Number(value) : null;
  const percent = (used, total) => number(total) > 0 && number(used) != null ? Math.max(0, Math.min(100, Number(used) / Number(total) * 100)) : null;
  let overviewFields = null;

  function metric(label, value, detail, uuid, resource) {
    const pct = value == null ? null : Math.max(0, Math.min(100, value));
    const level = pct >= 90 ? 'critical' : pct >= 80 ? 'high' : '';
    const interactive = resource === 'cpu' || resource === 'memory' || resource === 'disk' || resource === 'traffic';
    const tag = interactive ? 'button' : 'div';
    const attributes = interactive ? ` type="button" data-action="resource" data-uuid="${escapeHtml(uuid)}" data-metric="${resource}" title="查看${label}历史曲线" aria-label="查看${label}历史曲线"` : '';
    return `<${tag} class="hex-metric${interactive ? ' metric-button' : ''}"${attributes}><div class="metric-meta"><span class="metric-name">${label}${interactive ? historyIcon() : ''}</span><strong>${pct == null ? '—' : pct.toFixed(1) + '%'}</strong></div><div class="progress-track"><div class="progress-bar ${level}" style="width:${pct || 0}%"></div></div><small>${detail}</small></${tag}>`;
  }

  // A quiet chart glyph marks the captions that open a history chart, so the
  // affordance survives without hover on touch screens.
  function historyIcon() {
    return '<svg class="metric-history-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path d="M4 19V5M4 19h16"/><path d="M8 15.5l4-5 3 3 4-6"/></svg>';
  }

  function englishRegionName(code) {
    if (!/^[A-Z]{2}$/.test(code || '')) return 'Unknown';
    try {
      return new Intl.DisplayNames(['en'], { type: 'region', fallback: 'none' }).of(code) || 'Unknown';
    } catch (_) {
      return code;
    }
  }

  function systemIcon() {
    return '<svg class="node-info-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8M12 17v4"/></svg>';
  }

  function asnIcon() {
    return '<svg class="node-info-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><circle cx="12" cy="12" r="9" fill="#e0f2f1" stroke="#397a9e"/><ellipse cx="12" cy="12" rx="4" ry="9" stroke="#218b7e"/><path d="M3 12h18" stroke="#218b7e"/></svg>';
  }

  function networkIdentityLine(info) {
    const identity = info.network_identity || {};
    const asn = identity.asn ? `${identity.asn} ${identity.as_name || ''}`.trim() : 'Unknown';
    const regions = [
      ['IPV4', identity.ipv4_available, identity.youtube_region],
      ['IPV6', identity.ipv6_available, identity.youtube_region_ipv6],
    ].filter(([, available, code]) => available !== false || !!code);
    // Missing availability on legacy agents remains Unknown, not absent.
    const families = regions.map(([family]) => family).join('/');
    const countries = regions.map(([, , code]) => englishRegionName(code)).join('/');
    const google = regions.length ? `<span class="node-google-region" title="Region detected by YouTube Premium · Direct ${families}"><img src="/google-mark.svg" alt="Google" width="14" height="14"><span>${families}：${escapeHtml(countries)}</span></span>` : '';
    return `<span class="node-network-identity"><span class="node-asn" title="ASN · Direct IPv4">${asnIcon()}<span>ASN：${escapeHtml(asn)}</span></span>${google}</span>`;
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
    // The theme styles urgency on the expiry cell; wire it to the due date.
    const expiryState = bill.days == null ? '' : bill.days <= 0 ? ' is-critical' : bill.days <= 7 ? ' is-warning' : '';
    // Load only means something per core: 1.0x is saturated, 0.7x is busy.
    const cores = number(info.cpu_cores) || number(cpu.cores);
    const load1 = live ? number(load.load1) : null;
    const loadRatio = load1 != null && cores > 0 ? load1 / cores : null;
    const loadState = loadRatio == null ? '' : loadRatio >= 1 ? ' is-critical' : loadRatio >= 0.7 ? ' is-warning' : '';
    return `<article class="node-card ss-node ${node.online ? '' : 'offline'}" data-node-id="${escapeHtml(node.uuid)}">
      <details class="ss-details">
        <summary class="ss-row">
          <span class="ss-cell ss-status"><i class="ss-dot ${node.online ? 'is-online' : ''}"></i><span>${node.online ? '在线' : '离线'}</span></span>
          <span class="ss-cell ss-name" title="${escapeHtml(node.name)}"><strong>${escapeHtml(node.name)}</strong></span>
          <span class="ss-cell ss-region">${getRegionBadge(node.region)}</span>
          <span class="ss-cell ss-system" title="${escapeHtml(info.os || '等待上报')}">${escapeHtml(info.os || '等待上报')}</span>
          <span class="ss-cell ss-uptime">${live ? formatUptime(r.uptime) : '—'}</span>
          <span class="ss-cell ss-expiry${expiryState}">${bill.remaining}</span>
          <span class="ss-cell ss-load${loadState}">${load1 != null ? load1.toFixed(2) : '—'}</span>
          <span class="ss-cell ss-network"><span>↓ ${speed(net.down)}</span><span>↑ ${speed(net.up)}</span></span>
          <span class="ss-cell ss-traffic">${serverStatusBar(trafficUsage)}</span>
          <span class="ss-cell ss-cpu">${serverStatusBar(cpuUsage)}</span>
          <span class="ss-cell ss-memory">${serverStatusBar(ramUsage)}</span>
          <span class="ss-cell ss-disk">${serverStatusBar(diskUsage)}</span>
        </summary>
        <div class="ss-expanded">
          <div class="ss-detail-grid">
            ${detail('系统', `${escapeHtml(info.os || '等待上报')}${info.arch ? ' · ' + escapeHtml(info.arch) : ''}${networkIdentityLine(info)}`)}
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
            <button class="btn" type="button" data-action="resource" data-uuid="${escapeHtml(node.uuid)}" data-metric="traffic">周期流量</button>
            <button class="btn" type="button" data-action="resource" data-uuid="${escapeHtml(node.uuid)}" data-metric="disk">硬盘历史</button>
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
    const siteTheme = document.documentElement.dataset.siteTheme || 'design';
    const siteLayout = document.documentElement.dataset.siteLayout || 'cards';
    return JSON.stringify([
      node.uuid, node.name, node.region, node.online, node.basic_info, node.profile,
      node.ping_preview, node.traffic_limit, node.cycle_total_used,
      node.reset_day, node.days_until_reset, !!node.last_report,
      r.cpu, r.ram, r.disk, r.ping_results, Math.floor((r.uptime || 0) / 60),
      net.up, net.down, net.totalUp, net.totalDown,
      siteTheme, siteLayout, siteLayout === 'serverstatus' ? [r.swap, r.load, r.connections, r.process] : null,
      new Date().toDateString()
    ]);
  }

  function renderNode(node) {
    if (document.documentElement.dataset.siteLayout === 'serverstatus') return renderServerStatusNode(node);
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
      <div class="hex-node-header"><div class="hex-node-identity"><span class="hex-region">${getRegionBadge(node.region)}</span><h2>${escapeHtml(node.name)}</h2><span class="hex-system">${systemIcon(info.os)}<span>${escapeHtml(info.os || '等待上报')} ${info.arch ? '· ' + escapeHtml(info.arch) : ''}</span></span>${networkIdentityLine(info)}</div><span class="hex-status ${node.online ? 'is-online' : ''}"><i></i>${node.online ? '在线' : '离线'}</span></div>
      <div class="hex-resources">
        ${metric('CPU', live ? number(cpu.usage) : null, `${info.cpu_cores || cpu.cores || '—'} 核`, node.uuid, 'cpu')}
        ${metric('内存', live ? percent(ram.used, ram.total) : null, capacity(ram), node.uuid, 'memory')}
        ${metric('硬盘', live ? percent(disk.used, disk.total) : null, capacity(disk), node.uuid, 'disk')}
        ${metric('周期流量', traffic, limit > 0 ? `${formatBytes(used)} / ${formatBytes(limit)}` : '未设置流量配额', node.uuid, 'traffic')}
      </div>
      <div class="hex-network"><div><span>↑ 上行</span><strong>${speed(net.up)}</strong></div><button class="hex-network-total" type="button" data-action="resource" data-uuid="${escapeHtml(node.uuid)}" data-metric="network" title="查看实时速率历史曲线" aria-label="查看实时速率历史曲线"><span>实时速率${historyIcon()}</span><strong>${speed(totalRate)}</strong></button><div><span>↓ 下行</span><strong>${speed(net.down)}</strong></div></div>
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
    const cycleUsages = nodes.map(n => number(n.cycle_total_used))
      .filter(used => used != null && used >= 0);
    const cycleTraffic = cycleUsages.reduce((sum, used) => sum + used, 0);
    const connectionRates = live.map(n => number((n.last_report.connections || {}).tcp_new_per_sec))
      .filter(rate => rate != null);
    const connectionRate = connectionRates.reduce((sum, rate) => sum + rate, 0);
    const up = total('up');
    const down = total('down');
    // Split a formatted value ("294.9 KB/s") so the unit can be typeset and
    // updated independently of the number.
    const splitUnit = text => {
      const raw = String(text == null ? '—' : text);
      const match = /^([\d.,]+)\s*(.+)$/.exec(raw);
      return match ? { value: match[1], unit: match[2] } : { value: raw, unit: '' };
    };
    const offline = nodes.length - online;
    const speed = splitUnit(formatSpeed(up + down));
    const traffic = cycleUsages.length ? splitUnit(formatBytes(cycleTraffic)) : { value: '—', unit: '' };
    const rateText = connectionRates.length
      ? (connectionRate >= 10
        ? Math.round(connectionRate).toLocaleString('zh-CN')
        : connectionRate.toLocaleString('zh-CN', { minimumFractionDigits: 1, maximumFractionDigits: 1 }))
      : '—';
    const values = {
      online: String(online),
      count: `/ ${nodes.length}`,
      onlineNote: !nodes.length ? '暂无节点' : offline > 0 ? `${offline} 台离线` : '全部在线',
      busy: String(busy),
      memoryBusy: String(memoryBusy),
      speed: speed.value,
      speedUnit: speed.unit,
      speedNote: `↑ ${formatSpeed(up)} · ↓ ${formatSpeed(down)}`,
      cycleTraffic: traffic.value,
      cycleTrafficUnit: traffic.unit,
      connectionRate: rateText,
      connectionRateUnit: connectionRates.length ? '次/s' : '',
    };
    const banner = document.getElementById('statsBanner');
    if (!overviewFields) {
      banner.innerHTML = `
        <div class="hex-stat" title="在线节点数 / 节点总数"><dt>在线节点</dt><dd><strong><span data-stat="online">${values.online}</span><span class="unit" data-stat="count">${values.count}</span></strong><small data-stat="onlineNote">${values.onlineNote}</small></dd></div>
        <div class="hex-stat" title="CPU 使用率达到各节点设定阈值的在线节点数"><dt>高负载</dt><dd><strong><span data-stat="busy">${values.busy}</span><span class="unit">台</span></strong><small>按各节点 CPU 阈值</small></dd></div>
        <div class="hex-stat" title="内存使用率达到 85% 的在线节点数"><dt>内存紧张</dt><dd><strong><span data-stat="memoryBusy">${values.memoryBusy}</span><span class="unit">台</span></strong><small>内存使用率 ≥ 85%</small></dd></div>
        <div class="hex-stat" title="在线节点上传与下载速率之和"><dt>实时网速</dt><dd><strong><span data-stat="speed">${values.speed}</span><span class="unit" data-stat="speedUnit">${values.speedUnit}</span></strong><small data-stat="speedNote">${values.speedNote}</small></dd></div>
        <div class="hex-stat" title="各节点本计费周期已用流量之和（上传 + 下载）"><dt>周期流量</dt><dd><strong><span data-stat="cycleTraffic">${values.cycleTraffic}</span><span class="unit" data-stat="cycleTrafficUnit">${values.cycleTrafficUnit}</span></strong><small>各节点本周期合计</small></dd></div>
        <div class="hex-stat" title="在线节点 TCP 新建连接数每秒之和"><dt>连接速率</dt><dd><strong><span data-stat="connectionRate">${values.connectionRate}</span><span class="unit" data-stat="connectionRateUnit">${values.connectionRateUnit}</span></strong><small>TCP 新建连接/秒</small></dd></div>`;
      if (banner.querySelectorAll) {
        overviewFields = Object.fromEntries(Array.from(banner.querySelectorAll('[data-stat]')).map(element => [element.dataset.stat, element]));
      }
    } else {
      for (const [name, value] of Object.entries(values)) {
        const field = overviewFields[name];
        if (field && field.textContent !== value) field.textContent = value;
      }
    }
    // overviewFields caches the first render, so severity classes must be
    // re-applied on every update instead of only while building the markup.
    const markSeverity = (field, alert) => {
      const cell = field && field.closest('.hex-stat');
      if (!cell) return;
      cell.classList.toggle('is-alert', alert);
      cell.classList.toggle('is-idle', !alert);
    };
    if (overviewFields) {
      markSeverity(overviewFields.busy, busy > 0);
      markSeverity(overviewFields.memoryBusy, memoryBusy > 0);
    }
    if (window.VibeGlobe) window.VibeGlobe.setNodes(nodes);
  }
  return Object.freeze({ renderNode, renderOverview, renderKey });
})();
