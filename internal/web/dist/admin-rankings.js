// Rankings page: today, this week and this month, three nodes per metric.
// The rankings used to be a dialog in the public shell; they now have their own
// management page so the dashboard ships no management UI.
(() => {
  const { ready, request, escapeHtml, formatBytes } = window.VibeAdmin;
  const $ = id => document.getElementById(id);

  const METRICS = {
    connections: ['连接数量', 'TCP + UDP · 平均数量'],
    load: ['系统负载', 'Load 1 · 平均负载'],
    memory: ['内存占用', '平均使用率'],
    connection_rate: ['建连速度', 'TCP 建连尝试 · 平均次数/秒'],
    traffic: ['流量', '上传 + 下载 · 累计用量']
  };

  let period = 'day';
  let pending = null;
  let sequence = 0;

  function metricValue(metric, value) {
    if (metric === 'traffic') return formatBytes(value);
    if (metric === 'memory') return value.toFixed(1) + '%';
    if (metric === 'connection_rate') return value.toFixed(1) + ' 次/s';
    if (metric === 'load') return value.toFixed(2);
    return value.toLocaleString('zh-CN', {maximumFractionDigits: 1}) + ' 个';
  }

  // Cards follow the same shape for every metric, so a missing record only
  // replaces the rows, never the heading that explains the unit.
  function render(data) {
    const groups = data.groups || [];
    $('rankingsGrid').innerHTML = Object.entries(METRICS).map(([metric, [label, hint]]) => {
      const entries = (groups.find(group => group.metric === metric)?.entries || [])
        .filter(entry => Number.isFinite(entry.value) && entry.value >= 0).slice(0, 3);
      const top = entries[0]?.value || 0;
      const rows = entries.map((entry, index) => {
        const name = escapeHtml(entry.name || entry.uuid || '未命名节点');
        const width = top > 0 ? Math.max(0, Math.min(100, entry.value / top * 100)) : 0;
        return `<li class="ranking-row"><span class="ranking-place">${index + 1}</span><div class="ranking-detail"><div class="ranking-line"><span class="ranking-node" title="${name}">${name}</span><strong>${metricValue(metric, entry.value)}</strong></div><div class="ranking-track" aria-hidden="true"><i style="width:${width}%"></i></div></div></li>`;
      }).join('');
      return `<section class="ranking-card"><div class="ranking-card-heading"><h4>${label}</h4><span>${hint}</span></div>${entries.length ? `<ol class="ranking-rows">${rows}</ol>` : '<p class="ranking-empty">暂无有效记录</p>'}</section>`;
    }).join('');
    const stamp = seconds => new Date(seconds * 1000).toLocaleString('zh-CN', {timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false});
    $('rankingsPeriod').textContent = `${stamp(data.start_time)} — ${stamp(data.end_time)} · 北京时间`;
  }

  async function load(next = period) {
    if (!['day', 'week', 'month'].includes(next)) return;
    period = next;
    if (pending) pending.abort();
    const controller = new AbortController();
    pending = controller;
    const ticket = ++sequence;
    const grid = $('rankingsGrid');
    const status = $('rankingsStatus');
    const refresh = $('refreshRankingsBtn');
    document.querySelectorAll('[data-ranking-period]').forEach(button => {
      const active = button.dataset.rankingPeriod === next;
      button.classList.toggle('active', active);
      button.setAttribute('aria-pressed', String(active));
    });
    grid.innerHTML = '';
    grid.setAttribute('aria-busy', 'true');
    $('rankingsPeriod').textContent = '';
    status.textContent = '正在加载排行榜…';
    refresh.disabled = true;
    try {
      const data = await request(`/api/rankings?period=${next}`, { signal: controller.signal });
      if (ticket !== sequence) return;
      render(data);
      status.textContent = (data.groups || []).some(group => (group.entries || []).length) ? '' : '本时段暂无历史数据，收到采样后即可生成排名。';
    } catch (error) {
      if (ticket !== sequence || error.name === 'AbortError') return;
      status.textContent = '排行榜加载失败，请点击刷新重试。';
    } finally {
      if (ticket === sequence) {
        pending = null;
        grid.removeAttribute('aria-busy');
        refresh.disabled = false;
      }
    }
  }

  function wire() {
    document.querySelectorAll('[data-ranking-period]').forEach(button => {
      button.addEventListener('click', () => load(button.dataset.rankingPeriod));
    });
    $('refreshRankingsBtn').addEventListener('click', () => load());
  }

  ready(async () => {
    wire();
    await load();
  });
})();
