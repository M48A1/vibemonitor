let rankingsPeriod = 'day';
let rankingsRequest = null;
let rankingsSequence = 0;

const rankingLabels = {
  connections: ['连接数量', 'TCP + UDP · 平均数量'],
  load: ['系统负载', 'Load 1 · 平均负载'],
  memory: ['内存占用', '平均使用率'],
  connection_rate: ['建连速度', 'TCP 建连尝试 · 平均次数/秒'],
  traffic: ['流量', '上传 + 下载 · 累计用量']
};

function cancelRankings() {
  rankingsSequence++;
  if (rankingsRequest) rankingsRequest.abort();
  rankingsRequest = null;
  document.getElementById('rankingsGrid').removeAttribute('aria-busy');
  document.getElementById('refreshRankingsBtn').disabled = false;
  document.getElementById('siteMenuTrigger').focus();
}

function rankingValue(metric, value) {
  if (metric === 'traffic') return formatBytes(value);
  if (metric === 'memory') return value.toFixed(1) + '%';
  if (metric === 'connection_rate') return value.toFixed(1) + ' 次/s';
  if (metric === 'load') return value.toFixed(2);
  return value.toLocaleString('zh-CN', {maximumFractionDigits: 1}) + ' 个';
}

function renderRankings(data) {
  document.getElementById('rankingsGrid').innerHTML = Object.entries(rankingLabels).map(([metric, [label, hint]]) => {
    const entries = (data.groups.find(group => group.metric === metric)?.entries || [])
      .filter(entry => Number.isFinite(entry.value) && entry.value >= 0).slice(0, 3);
    const top = entries[0]?.value || 0;
    const rows = entries.map((entry, index) => {
      const name = escapeHtml(entry.name || entry.uuid || '未命名节点');
      const width = top > 0 ? Math.max(0, Math.min(100, entry.value / top * 100)) : 0;
      return `<li class="ranking-row"><span class="ranking-place">${index + 1}</span><div class="ranking-detail"><div class="ranking-line"><span class="ranking-node" title="${name}">${name}</span><strong>${rankingValue(metric, entry.value)}</strong></div><div class="ranking-track" aria-hidden="true"><i style="width:${width}%"></i></div></div></li>`;
    }).join('');
    return `<section class="ranking-card"><div class="ranking-card-heading"><h4>${label}</h4><span>${hint}</span></div>${entries.length ? `<ol class="ranking-rows">${rows}</ol>` : '<p class="ranking-empty">暂无有效记录</p>'}</section>`;
  }).join('');
  const date = seconds => new Date(seconds * 1000).toLocaleString('zh-CN', {timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false});
  document.getElementById('rankingsPeriod').textContent = `${date(data.start_time)} — ${date(data.end_time)} · 北京时间`;
}

async function loadRankings(period = rankingsPeriod) {
  if (!['day', 'week', 'month'].includes(period)) return;
  rankingsPeriod = period;
  if (rankingsRequest) rankingsRequest.abort();
  const controller = new AbortController();
  rankingsRequest = controller;
  const sequence = ++rankingsSequence;
  const status = document.getElementById('rankingsStatus');
  const grid = document.getElementById('rankingsGrid');
  const refresh = document.getElementById('refreshRankingsBtn');
  document.querySelectorAll('[data-ranking-period]').forEach(button => {
    const active = button.dataset.rankingPeriod === period;
    button.classList.toggle('active', active);
    button.setAttribute('aria-pressed', String(active));
  });
  grid.innerHTML = '';
  grid.setAttribute('aria-busy', 'true');
  document.getElementById('rankingsPeriod').textContent = '';
  status.textContent = '正在加载排行榜…';
  refresh.disabled = true;
  try {
    const response = await fetch(`/api/rankings?period=${period}`, {signal: controller.signal});
    if (!response.ok) throw new Error('request failed');
    const data = await response.json();
    if (sequence !== rankingsSequence) return;
    renderRankings(data);
    status.textContent = data.groups.some(group => group.entries.length) ? '' : '本时段暂无历史数据，收到采样后即可生成排名。';
  } catch (error) {
    if (sequence !== rankingsSequence || error.name === 'AbortError') return;
    status.textContent = '排行榜加载失败，请点击刷新重试。';
  } finally {
    if (sequence === rankingsSequence) {
      rankingsRequest = null;
      grid.removeAttribute('aria-busy');
      refresh.disabled = false;
    }
  }
}

document.getElementById('rankingsBtn').addEventListener('click', event => {
  event.stopPropagation();
  closeSiteMenu();
  openModal('rankingsModal');
  document.querySelector(`[data-ranking-period="${rankingsPeriod}"]`).focus();
  loadRankings();
});
document.querySelectorAll('[data-ranking-period]').forEach(button => button.addEventListener('click', () => loadRankings(button.dataset.rankingPeriod)));
document.getElementById('refreshRankingsBtn').addEventListener('click', () => loadRankings());
document.getElementById('rankingsModal').addEventListener('keydown', event => {
  if (event.key !== 'Tab') return;
  const controls = Array.from(event.currentTarget.querySelectorAll('button:not(:disabled)'));
  const first = controls[0], last = controls[controls.length - 1];
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
});
