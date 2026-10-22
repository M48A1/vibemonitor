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

