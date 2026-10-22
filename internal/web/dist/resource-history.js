// CPU, memory, disk, and total network rate share the same history modal.
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
  if (!['cpu', 'memory', 'disk', 'network', 'traffic'].includes(metric)) return;
  currentResourceNodeUUID = uuid;
  currentResourceMetric = metric;
  currentResourceRange = metric === 'traffic' ? 'current' : '1h';
  const label = metric === 'cpu' ? 'CPU' : metric === 'memory' ? '内存' : metric === 'disk' ? '硬盘' : metric === 'traffic' ? '周期流量' : '实时速率';
  document.getElementById('resourceModalTitle').textContent = `${nodeName} ${label}${metric === 'network' ? '历史曲线' : '占用曲线'}`;
  document.getElementById('resourceModalSubtitle').textContent = `每 60 秒记录 · 保留 90 天 · ${metric === 'network' ? '上行＋下行（Bytes/s）' : `${label}占用率`}`;
  document.getElementById('resourceChartSvg').setAttribute('aria-label', `${label}历史曲线`);
  for (const [name, labelText] of Object.entries(metric === 'network'
    ? {Current: '最新速率', Avg: '平均速率', Min: '最低速率', Max: '最高速率'}
    : {Current: '最新记录', Avg: '平均占用', Min: '最低占用', Max: '最高占用'})) {
    document.getElementById(`resourceLabel${name}`).textContent = labelText;
  }
  const traffic = metric === 'traffic';
  document.getElementById('resourceTimeRanges').style.display = traffic ? 'none' : '';
  document.getElementById('trafficTimeRanges').style.display = traffic ? '' : 'none';
  document.getElementById('trafficLegend').style.display = traffic ? 'flex' : 'none';
  document.getElementById('resourceChartHint').textContent = traffic ? '未采集流量按 0 计算 · 今日统计中 · 可横向滑动查看每日流量' : '空白处表示未收到采样';
  if (traffic) {
    document.getElementById('resourceModalTitle').textContent = `${nodeName} 日流量汇总`;
    document.getElementById('resourceModalSubtitle').textContent = '按节点流量重置周期统计';
    document.getElementById('resourceChartSvg').setAttribute('aria-label', '每日流量柱形图');
    for (const [key, text] of Object.entries({Current:'周期累计流量',Avg:'已过日期日均',Min:'最低日流量',Max:'最高日流量'})) {
      document.getElementById(`resourceLabel${key}`).textContent = text;
    }
  }
  updateResourceRangeButtons();
  openModal('resourceChartModal');
  loadResourceHistory();
};

function updateResourceRangeButtons() {
  ['current', 'previous', 'three'].forEach(range => document.getElementById(`trafficRange${range}`).classList.toggle('active', range === currentResourceRange));
  ['1h', '24h', '7d', '31d'].forEach(range => {
    const button = document.getElementById(`resourceRange${range}`);
    button.classList.toggle('active', range === currentResourceRange);
  });
}

window.switchResourceRange = function(range) {
  const allowed = currentResourceMetric === 'traffic' ? ['current', 'previous', 'three'] : ['1h', '24h', '7d', '31d'];
  if (!allowed.includes(range)) return;
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
  const svg = document.getElementById('resourceChartSvg');
  svg.style.minWidth = '';
  svg.setAttribute('viewBox', '0 0 700 240');
  svg.parentElement.style.overflowX = metric === 'traffic' ? 'auto' : '';
  showHistoryLoading('resourceChartSvg', 'resourceChartTooltip', 'resourceChartTimeStart', 'resourceChartTimeEnd');
  ['Current', 'Avg', 'Min', 'Max'].forEach(name => {
    document.getElementById(`resourceStat${name}`).textContent = '...';
  });
  try {
    const query = new URLSearchParams({uuid, metric, range});
    const endpoint = metric === 'traffic' ? 'traffic-history' : 'resource-history';
    const response = await fetch(`/api/nodes/${endpoint}?${query}`, {signal: controller.signal});
    if (!response.ok) throw new Error('加载失败');
    const data = await response.json();
    if (request !== resourceChartRequest || controller.signal.aborted) return;
    if (metric === 'traffic') { renderTrafficHistory(data); return; }
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

function renderTrafficHistory(data) {
  const days = data.days || [];
  const measured = days.filter(day => day.status !== 'future');
  const values = measured.map(day => Math.max(0, Number(day.bytes) || 0));
  const stats = {Current: data.total || 0, Avg: values.length ? (data.total || 0) / values.length : 0,
    Min: values.length ? Math.min(...values) : 0, Max: values.length ? Math.max(...values) : 0};
  for (const [key, value] of Object.entries(stats)) {
    document.getElementById(`resourceStat${key}`).textContent = values.length ? formatBytes(value) : '--';
  }
  document.getElementById('resourceModalSubtitle').textContent = `${data.start_date} 至 ${data.end_date} · 每月 ${data.reset_day || 1} 日重置 · ${data.timezone} · 上行＋下行；不含手动补录和校正；跨日流量按时间估算`;
  document.getElementById('resourceChartTimeStart').textContent = data.start_date;
  document.getElementById('resourceChartTimeEnd').textContent = data.end_date;
  const svg = document.getElementById('resourceChartSvg');
  const width = Math.max(700, days.length * 62 + 100), height = 240;
  const left = 80, top = 30, bottom = 195, plot = width-left-20;
  const peak = Math.max(1, ...values);
  const cell = plot / Math.max(1, days.length);
  svg.style.minWidth = `${width}px`;
  svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
  const grid = [0, .5, 1].map(f => {
    const y = bottom - (bottom-top)*f;
    return `<line x1="${left}" y1="${y}" x2="${width-20}" y2="${y}" stroke="currentColor" opacity=".15"/><text x="${left-8}" y="${y+4}" text-anchor="end" fill="currentColor" font-size="10">${formatBytes(peak*f)}</text>`;
  }).join('');
  svg.innerHTML = grid + days.map((day, index) => {
    const value = Math.max(0, Number(day.bytes) || 0);
    const barHeight = Math.max(3,value/peak*(bottom-top));
    const states = {online:['#238364','全天在线'],offline:['#dc4444','全天离线'],partial:['#d49120','部分时段离线'],unknown:['#8792a2','状态记录不完整'],future:['#b7bec8','尚未到达']};
    const [color, state] = states[day.status] || states.unknown;
    const x = left + index*cell, center = x+cell/2, y = bottom-barHeight;
    const label = formatBytes(value);
    const statusText = !day.complete && day.status !== 'future' ? `${state.replace('全天', '今日')} · 统计中` : state;
    const title = `${day.date} · ${label} (${value.toLocaleString()} B) · ${statusText}`;
    return `<g class="traffic-day" data-status="${escapeHtml(day.status || 'unknown')}" tabindex="0" role="img" aria-label="${escapeHtml(title)}"><title>${escapeHtml(title)}</title>
      <rect x="${x+5}" y="${y}" width="${Math.max(1,cell-10)}" height="${barHeight}" rx="3" fill="${color}" opacity=".85"/>
      <text x="${center}" y="${y-7}" text-anchor="middle" fill="currentColor" font-size="9">${label}</text>
      <text x="${center}" y="${bottom+20}" text-anchor="middle" fill="currentColor" font-size="10">${escapeHtml(day.date.slice(5))}</text></g>`;
  }).join('');
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
  const color = metric === 'cpu' ? '#356dcc' : metric === 'memory' ? '#238364' : metric === 'disk' ? '#8056b3' : '#986700';
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
    tooltip.textContent = `${formatTooltipTime(closest.t)} · ${metric === 'cpu' ? 'CPU' : metric === 'memory' ? '内存' : metric === 'disk' ? '硬盘' : '实时速率'} ${formatResourceValue(metric, closest.v)}`;
  });
  hitBox.addEventListener('mouseleave', () => {
    line.style.display = marker.style.display = tooltip.style.display = 'none';
  });
}

