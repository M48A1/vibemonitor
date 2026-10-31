// Node management page: node list, create/edit, ordering, install commands and
// the temporary registration window. Every request uses the authenticated
// management APIs the dashboard dialogs use, so both entry points stay in sync.
(() => {
  const { ready, request, status, copyText, escapeHtml, formatBytes } = window.VibeAdmin;
  const $ = id => document.getElementById(id);
  const GIB = 1024 * 1024 * 1024;

  let nodes = [];
  const deletedNodeIDs = new Set();
  let editProfileReady = false;
  let currentGroup = '';

  // Traffic inputs use GiB; the upper bound keeps conversion inside int64.
  function validTrafficGB(value) {
    return Number.isFinite(value) && value >= 0 && value < 8589934592;
  }

  function validResetDay(value) {
    return Number.isInteger(value) && value >= 0 && value <= 31;
  }

  function compareNodes(a, b) {
    const order = node => {
      const value = Number(node.weight);
      return Number.isSafeInteger(value) && value > 0 ? value : 0;
    };
    return order(a) - order(b)
      || (a.name || '').trim().localeCompare((b.name || '').trim(), 'en', { sensitivity: 'base', numeric: true })
      || (a.uuid || '').localeCompare(b.uuid || '', 'en');
  }

  function percentOf(used, total) {
    if (!total || total <= 0) return 0;
    return Math.min(100, Math.max(0, (used / total) * 100));
  }

  function barLevel(percent) {
    if (percent >= 90) return 'critical';
    if (percent >= 75) return 'warn';
    return 'ok';
  }

  const REGION_FLAGS = {
    JP: '🇯🇵 JP', US: '🇺🇸 US', CN: '🇨🇳 CN', HK: '🇭🇰 HK', SG: '🇸🇬 SG',
    TW: '🇹🇼 TW', KR: '🇰🇷 KR', DE: '🇩🇪 DE', GB: '🇬🇧 UK', FR: '🇫🇷 FR',
    CA: '🇨🇦 CA', RU: '🇷🇺 RU'
  };

  function regionLabel(code) {
    if (!code) return '🌐 —';
    const key = String(code).toUpperCase();
    return REGION_FLAGS[key] || ('🌐 ' + escapeHtml(key));
  }

  function dueLabel(dueDate) {
    if (!dueDate) return '—';
    const due = new Date(dueDate + 'T00:00:00');
    if (Number.isNaN(due.getTime())) return escapeHtml(dueDate);
    const days = Math.ceil((due.getTime() - Date.now()) / 86400000);
    if (days < 0) return escapeHtml(dueDate) + ' · 已到期';
    if (days <= 7) return escapeHtml(dueDate) + ' · 剩 ' + days + ' 天';
    return escapeHtml(dueDate);
  }

  function trafficCell(node) {
    const used = Math.max(0, Number(node.cycle_total_used) || 0);
    const limit = Math.max(0, Number(node.traffic_limit) || 0);
    if (!limit) {
      return '<span class="admin-node-meta">' + formatBytes(used) + ' · 不限</span>';
    }
    const percent = percentOf(used, limit);
    return '<span class="admin-node-meta">' + formatBytes(used) + ' / ' + formatBytes(limit)
      + ' · ' + percent.toFixed(0) + '%</span>'
      + '<span class="admin-bar" data-level="' + barLevel(percent) + '"><span style="width:'
      + percent.toFixed(1) + '%"></span></span>';
  }

  function usageCell(node) {
    const report = node.last_report || {};
    const cpu = report.cpu && Number.isFinite(Number(report.cpu.usage)) ? Number(report.cpu.usage) : null;
    const total = (report.ram && Number(report.ram.total)) || (node.basic_info && Number(node.basic_info.mem_total)) || 0;
    const used = (report.ram && Number(report.ram.used)) || 0;
    if (cpu === null && !total) return '<span class="admin-node-meta">—</span>';
    const parts = [];
    if (cpu !== null) parts.push('CPU ' + cpu.toFixed(1) + '%');
    if (total) parts.push('内存 ' + percentOf(used, total).toFixed(0) + '%');
    return '<span class="admin-node-meta">' + parts.join(' · ') + '</span>';
  }

  function renderNodes() {
    const body = $('nodeTableBody');
    const sorted = [...nodes].sort(compareNodes);
    $('nodeCount').textContent = String(sorted.length);
    $('nodeEmpty').hidden = sorted.length > 0;
    body.replaceChildren();
    for (const node of sorted) {
      const info = node.basic_info || {};
      const system = [info.os, info.arch].filter(Boolean).join(' · ') || '—';
      const row = document.createElement('tr');
      row.dataset.uuid = node.uuid;
      row.innerHTML = '<td><span class="admin-pill" data-state="' + (node.online ? 'online' : 'offline') + '">'
        + (node.online ? '在线' : '离线') + '</span></td>'
        + '<td><span class="admin-node-name">' + escapeHtml(node.name || node.uuid) + '</span>'
        + '<span class="admin-node-meta">' + escapeHtml(node.uuid.slice(0, 8)) + '</span></td>'
        + '<td>' + regionLabel(node.region) + '</td>'
        + '<td><span class="admin-node-meta">' + escapeHtml(system) + '</span></td>'
        + '<td><span class="admin-node-meta">' + dueLabel((node.profile || {}).due_date) + '</span></td>'
        + '<td>' + trafficCell(node) + '</td>'
        + '<td>' + usageCell(node) + '</td>'
        + '<td><div class="admin-row-actions">'
        + '<button type="button" class="btn" data-action="edit">编辑</button>'
        + '<button type="button" class="btn" data-action="guide">接入命令</button>'
        + '<button type="button" class="btn btn-danger" data-action="delete">删除</button>'
        + '</div></td>';
      body.appendChild(row);
    }
  }

  async function loadNodes() {
    const data = await request('/api/nodes?view=dashboard');
    nodes = Array.isArray(data) ? data.filter(node => !deletedNodeIDs.has(node.uuid)) : [];
    renderNodes();
    renderSortList();
  }


  // --- node form ---------------------------------------------------------
  // The panel build never changes while the page is open, so it is fetched
  // once; the probe version comes from the node's last basic-info report.
  let panelVersionPromise = null;

  function loadPanelVersion() {
    if (!panelVersionPromise) {
      panelVersionPromise = request('/api/version')
        .then(data => (data && typeof data.version === 'string' ? data.version.trim() : ''))
        .catch(() => '');
    }
    return panelVersionPromise;
  }

  // "v1.2.3" and "1.2.3-rc1" are the same build as far as an operator cares.
  function versionParts(value) {
    const cleaned = String(value || '').trim().replace(/^v/i, '').split(/[+\-]/)[0];
    return /^\d+(\.\d+)*$/.test(cleaned) ? cleaned.split('.').map(Number) : null;
  }

  // Compares the agent build a node reports with the panel it reports to, so an
  // outdated probe is visible without reading the unit file on the host.
  function versionState(agent, panel) {
    if (!agent) return { state: 'unknown', label: '节点未上报版本' };
    const a = versionParts(agent), p = versionParts(panel);
    if (!a || !p) return { state: '', label: '' };
    for (let i = 0; i < Math.max(a.length, p.length); i++) {
      const diff = (a[i] || 0) - (p[i] || 0);
      if (diff) return diff < 0
        ? { state: 'outdated', label: '低于主控，可升级探针' }
        : { state: 'match', label: '高于主控' };
    }
    return { state: 'match', label: '与主控一致' };
  }

  function renderVersions(node) {
    const agent = String((node.basic_info && node.basic_info.version) || '').trim();
    const uuid = node.uuid;
    const state = $('nodeFormVersionState');
    $('nodeFormAgentVersion').textContent = agent || '未上报';
    state.hidden = true;
    state.textContent = '';
    delete state.dataset.state;
    const panelEl = $('nodeFormPanelVersion');
    panelEl.textContent = '读取中…';
    loadPanelVersion().then(panel => {
      // Another node may have been opened while the version was in flight.
      if ($('nodeFormUUID').value !== uuid) return;
      panelEl.textContent = panel || '未知';
      const verdict = versionState(agent, panel);
      state.textContent = verdict.label;
      state.hidden = !verdict.label;
      if (verdict.state) state.dataset.state = verdict.state;
    });
  }

  function setMode(mode) {
    const editing = mode === 'edit';
    $('nodeFormTitle').textContent = editing ? '编辑节点' : '新建节点';
    $('nodeFormSubmitBtn').textContent = editing ? '保存修改' : '创建节点';
    document.querySelectorAll('#nodeFormCard [data-mode]').forEach(el => {
      el.hidden = el.dataset.mode !== mode;
    });
  }

  function fillProfile(profile) {
    const p = profile || {};
    $('nodeFormTargets').value = (p.targets || []).map(t => `${t.name},${t.host}`).join('\n');
    $('nodeFormDue').value = p.due_date || '';
    $('nodeFormCycle').value = p.payment_cycle || '';
    $('nodeFormPrice').value = p.price || '';
    $('nodeFormCurrency').value = p.currency || 'CNY';
    $('nodeFormCPUThreshold').value = p.cpu_threshold == null ? '' : p.cpu_threshold;
    $('nodeFormTrafficWarning').value = p.traffic_warning_percent ?? 0;
    $('nodeFormAlertsDisabled').checked = !!p.alerts_disabled;
  }

  function readProfile() {
    const warningText = $('nodeFormTrafficWarning').value.trim();
    const trafficWarningPercent = warningText === '' ? 0 : Number(warningText);
    if (!Number.isFinite(trafficWarningPercent) || trafficWarningPercent < 0 || trafficWarningPercent > 100) {
      throw new Error('流量预警基准必须在 0 到 100 之间，0 表示不提前预警');
    }
    const thresholdText = $('nodeFormCPUThreshold').value.trim();
    const cpuThreshold = thresholdText === '' ? null : Number(thresholdText);
    if (cpuThreshold !== null && (!Number.isFinite(cpuThreshold) || cpuThreshold < 0 || cpuThreshold > 100)) {
      throw new Error('CPU 负载阈值必须在 0 到 100 之间');
    }
    const targets = $('nodeFormTargets').value.split('\n').filter(line => line.trim()).map(line => {
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
      return { name, host };
    });
    return {
      targets,
      due_date: $('nodeFormDue').value,
      payment_cycle: $('nodeFormCycle').value,
      price: Number($('nodeFormPrice').value || 0),
      currency: $('nodeFormCurrency').value,
      cpu_threshold: cpuThreshold,
      traffic_warning_percent: trafficWarningPercent,
      alerts_disabled: $('nodeFormAlertsDisabled').checked
    };
  }

  function openNewNode() {
    editProfileReady = false;
    currentGroup = '';
    $('nodeFormUUID').value = '';
    $('nodeFormName').value = '';
    $('nodeFormRegion').value = 'JP';
    $('nodeFormTrafficLimit').value = '';
    $('nodeFormTrafficLimit').dataset.originalValue = '';
    $('nodeFormResetDay').value = '';
    $('nodeFormInitialUsed').value = '';
    $('nodeFormCycleUsed').value = '';
    $('nodeFormStatus').textContent = '';
    fillProfile({});
    setMode('new');
    $('nodeFormCard').hidden = false;
    $('nodeFormCard').scrollIntoView({ behavior: 'smooth', block: 'start' });
    $('nodeFormName').focus();
  }

  async function openEditNode(uuid) {
    const node = nodes.find(n => n.uuid === uuid);
    if (!node) { status('节点不存在或已被删除', 'error'); return; }
    editProfileReady = false;
    currentGroup = node.group || '';
    $('nodeFormUUID').value = node.uuid;
    $('nodeFormName').value = node.name || '';
    $('nodeFormRegion').value = node.region || '';
    renderVersions(node);
    const quota = $('nodeFormTrafficLimit');
    quota.value = node.traffic_limit > 0 ? String(node.traffic_limit / GIB) : '';
    quota.dataset.originalValue = quota.value;
    $('nodeFormResetDay').value = node.reset_day > 0 ? node.reset_day : '';
    const used = Math.max(0, Number(node.cycle_total_used) || 0);
    $('nodeFormCycleUsed').value = '';
    $('nodeFormCycleUsed').placeholder = (used / GIB).toFixed(3);
    $('nodeFormCycleUsedHint').textContent = '当前已用 ' + formatBytes(used) + '；留空不修改，填 0 可清零。';
    fillProfile({});
    setMode('edit');
    $('nodeFormCard').hidden = false;
    $('nodeFormStatus').textContent = '正在加载节点配置…';
    $('nodeFormSubmitBtn').disabled = true;
    try {
      const data = await request('/api/admin/nodes/' + encodeURIComponent(uuid) + '/profile');
      if ($('nodeFormUUID').value !== uuid) return;
      fillProfile(data.profile);
      editProfileReady = true;
      $('nodeFormStatus').textContent = '';
    } catch (error) {
      $('nodeFormStatus').textContent = '加载节点配置失败：' + error.message;
    } finally {
      if ($('nodeFormUUID').value === uuid) $('nodeFormSubmitBtn').disabled = false;
    }
  }

  function closeForm() {
    $('nodeFormCard').hidden = true;
    $('nodeFormUUID').value = '';
    $('nodeFormStatus').textContent = '';
    editProfileReady = false;
  }

  async function submitForm(event) {
    event.preventDefault();
    const uuid = $('nodeFormUUID').value;
    if (uuid && !editProfileReady) {
      $('nodeFormStatus').textContent = '节点配置尚未加载完成，请稍后重试。';
      return;
    }
    const name = $('nodeFormName').value.trim();
    if (!name) { $('nodeFormStatus').textContent = '请填写节点名称。'; return; }
    const region = $('nodeFormRegion').value.trim();
    const quotaInput = $('nodeFormTrafficLimit');
    const trafficLimitGB = quotaInput.value.trim() === '' ? 0 : Number(quotaInput.value);
    const quotaChanged = quotaInput.value !== quotaInput.dataset.originalValue;
    const resetDay = Number($('nodeFormResetDay').value || 0);
    const initialUsedGB = Number($('nodeFormInitialUsed').value || 0);
    const cycleUsedText = $('nodeFormCycleUsed').value.trim();
    const cycleUsedGB = cycleUsedText === '' ? null : Number(cycleUsedText);
    if (!validTrafficGB(trafficLimitGB) || !validTrafficGB(initialUsedGB)
      || (cycleUsedGB !== null && !validTrafficGB(cycleUsedGB)) || !validResetDay(resetDay)) {
      $('nodeFormStatus').textContent = '流量必须是非负数字且小于 8589934592 GiB；重置日为 1–31，留空默认 1 日。';
      return;
    }
    let profile;
    try {
      profile = readProfile();
    } catch (error) {
      $('nodeFormStatus').textContent = error.message;
      return;
    }

    const submit = $('nodeFormSubmitBtn');
    const label = submit.textContent;
    submit.disabled = true;
    submit.textContent = '保存中…';
    $('nodeFormStatus').textContent = '正在保存…';
    try {
      if (uuid) {
        const payload = { name, region, group: currentGroup, reset_day: resetDay, profile };
        if (quotaChanged) payload.traffic_limit_gb = trafficLimitGB;
        if (cycleUsedGB !== null) payload.cycle_used_gb = cycleUsedGB;
        await request('/api/admin/nodes/' + encodeURIComponent(uuid), {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload)
        });
        closeForm();
        status('节点已保存', 'success');
        await loadNodes();
      } else {
        const data = await request('/api/admin/nodes', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            name,
            region,
            group: '',
            traffic_limit_gb: trafficLimitGB,
            reset_day: resetDay,
            initial_used_gb: initialUsedGB,
            profile
          })
        });
        closeForm();
        status('节点已创建，请用下面的命令安装探针', 'success');
        await loadNodes();
        if (data.node && data.node.uuid) await showGuide(data.node.uuid);
      }
    } catch (error) {
      $('nodeFormStatus').textContent = '保存失败：' + error.message;
    } finally {
      submit.disabled = false;
      submit.textContent = label;
    }
  }

  // --- install commands --------------------------------------------------
  async function showGuide(uuid) {
    const card = $('guideCard');
    card.hidden = false;
    $('guideTitle').textContent = '节点接入命令';
    $('guideInstallCmd').textContent = '正在获取 Token…';
    $('guideRunCmd').textContent = '';
    $('guideUninstallCmd').textContent = '';
    $('guideToken').textContent = '';
    try {
      const data = await request('/api/admin/nodes/' + encodeURIComponent(uuid) + '/token');
      const token = data.token || '';
      const host = window.location.origin;
      const node = nodes.find(n => n.uuid === uuid);
      $('guideTitle').textContent = '节点接入命令 · ' + (node ? (node.name || node.uuid.slice(0, 8)) : uuid.slice(0, 8));
      $('guideInstallCmd').textContent = `curl -fsSL ${host}/install.sh?token=${token} | bash`;
      $('guideRunCmd').textContent = `./vibemonitor agent --server ${host} --token ${token}`;
      $('guideUninstallCmd').textContent = `curl -fsSL ${host}/install.sh?token=${encodeURIComponent(token)} | bash -s -- agent-uninstall`;
      $('guideToken').textContent = token;
    } catch (error) {
      $('guideInstallCmd').textContent = '获取失败：' + error.message;
      status('请重新登录后再获取接入命令', 'error');
    }
    card.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }

  // A failed response does not prove the transaction failed (e.g. proxy timeout),
  // so an unconfirmed delete is verified against the authenticated profile API.
  async function removeNode(uuid) {
    const node = nodes.find(n => n.uuid === uuid);
    const label = node ? (node.name || uuid.slice(0, 8)) : uuid.slice(0, 8);
    if (!confirm('确定要删除节点「' + label + '」吗？历史数据一并删除，此操作不可恢复。')) return;
    try {
      const res = await fetch('/api/admin/nodes/' + encodeURIComponent(uuid), {
        method: 'DELETE',
        credentials: 'same-origin'
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        const check = await fetch('/api/admin/nodes/' + encodeURIComponent(uuid) + '/profile', {
          credentials: 'same-origin',
          cache: 'no-store'
        });
        const verify = await check.json().catch(() => ({}));
        // Only the authenticated API's explicit not-found response confirms deletion.
        if (!(check.status === 404 && verify.error === 'node not found')) {
          throw new Error(data.error || ('HTTP ' + res.status));
        }
      }
      deletedNodeIDs.add(uuid);
      if ($('nodeFormUUID').value === uuid) closeForm();


      status('节点已删除', 'success');
      await loadNodes();
    } catch (error) {
      status('删除结果尚未确认（' + error.message + '），请刷新页面核实', 'error');
    }
  }

  // --- ordering ----------------------------------------------------------
  function updateSortButtons() {
    const items = $('sortNodesList').querySelectorAll('.sort-node-item');
    items.forEach((item, index) => {
      const up = item.querySelector('.sort-up-btn');
      const down = item.querySelector('.sort-down-btn');
      if (up) up.disabled = index === 0;
      if (down) down.disabled = index === items.length - 1;
    });
  }

  function updateSortBadges() {
    $('sortNodesList').querySelectorAll('.sort-node-item').forEach((item, index) => {
      const badge = item.querySelector('.sort-node-order');
      if (badge) badge.textContent = String(index + 1);
    });
  }

  // The sort card is permanently expanded, so the list follows the table: it is
  // rebuilt when nodes are added or removed, while a pending drag order stays
  // intact until it is saved.
  let sortDragging = false;

  function renderSortList(force) {
    if (sortDragging) return;
    const list = $('sortNodesList');
    const sorted = [...nodes].sort(compareNodes);
    const rendered = [...list.querySelectorAll('.sort-node-item')].map(item => item.dataset.uuid);
    if (!force && rendered.length === sorted.length && sorted.every(node => rendered.includes(node.uuid))) return;
    list.replaceChildren();
    if (!sorted.length) {
      list.textContent = '暂无节点，请先新建节点。';
      return;
    }
    for (const node of sorted) {
      const item = document.createElement('div');
      item.className = 'sort-node-item';
      item.draggable = true;
      item.dataset.uuid = node.uuid;
      item.innerHTML = '<span class="sort-node-grip" title="拖动排序">⠿</span>'
        + '<span class="sort-node-name">' + escapeHtml(node.name || node.uuid) + '</span>'
        + '<span class="sort-node-order" title="当前序号">' + (Number(node.weight) || 0) + '</span>'
        + '<span class="sort-node-actions">'
        + '<button type="button" class="sort-up-btn" title="上移" aria-label="上移">▲</button>'
        + '<button type="button" class="sort-down-btn" title="下移" aria-label="下移">▼</button>'
        + '</span>';
      list.appendChild(item);
    }
    updateSortButtons();
    updateSortBadges();
  }

  function openSort() {
    $('sortNodesStatus').textContent = '';
    renderSortList();
    $('sortCard').scrollIntoView({ behavior: 'smooth', block: 'start' });
  }

  // Drag, the move buttons and touch dragging share one list element.
  function initSort() {
    const list = $('sortNodesList');
    let dragItem = null;

    list.addEventListener('click', (event) => {
      const upBtn = event.target.closest('.sort-up-btn');
      const downBtn = event.target.closest('.sort-down-btn');
      if (!upBtn && !downBtn) return;
      const item = event.target.closest('.sort-node-item');
      if (!item) return;
      if (upBtn && item.previousElementSibling) {
        list.insertBefore(item, item.previousElementSibling);
      } else if (downBtn && item.nextElementSibling) {
        list.insertBefore(item.nextElementSibling, item);
      }
      updateSortButtons();
      updateSortBadges();
    });

    list.addEventListener('dragstart', (event) => {
      dragItem = event.target.closest('.sort-node-item');
      if (!dragItem) return;
      sortDragging = true;
      dragItem.classList.add('dragging');
      event.dataTransfer.effectAllowed = 'move';
      event.dataTransfer.setData('text/plain', '');
    });

    list.addEventListener('dragend', () => {
      if (dragItem) dragItem.classList.remove('dragging');
      list.querySelectorAll('.drag-over').forEach(el => el.classList.remove('drag-over'));
      dragItem = null;
      sortDragging = false;
      updateSortButtons();
      updateSortBadges();
    });

    list.addEventListener('dragover', (event) => {
      if (!dragItem) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = 'move';
      const target = event.target.closest('.sort-node-item');
      if (!target || target === dragItem) return;
      list.querySelectorAll('.drag-over').forEach(el => el.classList.remove('drag-over'));
      target.classList.add('drag-over');
      const rect = target.getBoundingClientRect();
      if (event.clientY < rect.top + rect.height / 2) {
        list.insertBefore(dragItem, target);
      } else {
        list.insertBefore(dragItem, target.nextSibling);
      }
    });

    list.addEventListener('dragleave', (event) => {
      const target = event.target.closest('.sort-node-item');
      if (target) target.classList.remove('drag-over');
    });

    list.addEventListener('drop', (event) => {
      event.preventDefault();
      list.querySelectorAll('.drag-over').forEach(el => el.classList.remove('drag-over'));
    });

    let touchItem = null;
    list.addEventListener('touchstart', (event) => {
      const grip = event.target.closest('.sort-node-grip');
      if (!grip) return;
      touchItem = grip.closest('.sort-node-item');
      if (touchItem) {
        sortDragging = true;
        touchItem.classList.add('dragging');
      }
    }, { passive: true });

    list.addEventListener('touchmove', (event) => {
      if (!touchItem) return;
      event.preventDefault();
      const touch = event.touches[0];
      for (const item of list.querySelectorAll('.sort-node-item')) {
        if (item === touchItem) continue;
        const rect = item.getBoundingClientRect();
        const midY = rect.top + rect.height / 2;
        if (touch.clientY < midY && item === touchItem.previousElementSibling) {
          list.insertBefore(touchItem, item);
          break;
        } else if (touch.clientY > midY && item === touchItem.nextElementSibling) {
          list.insertBefore(item, touchItem);
          break;
        }
      }
    }, { passive: false });

    const finishTouch = () => {
      if (!touchItem) return;
      touchItem.classList.remove('dragging');
      touchItem = null;
      sortDragging = false;
      updateSortButtons();
      updateSortBadges();
    };
    list.addEventListener('touchend', finishTouch, { passive: true });
    list.addEventListener('touchcancel', finishTouch, { passive: true });
  }

  async function saveSort() {
    const btn = $('saveSortNodesBtn');
    const statusEl = $('sortNodesStatus');
    const items = $('sortNodesList').querySelectorAll('.sort-node-item');
    if (!items.length) {
      statusEl.textContent = '没有可保存的排序。';
      return;
    }
    const order = [...items].map((item, index) => ({ uuid: item.dataset.uuid, weight: index + 1 }));
    btn.disabled = true;
    statusEl.textContent = '正在保存…';
    statusEl.style.color = '';
    try {
      await request('/api/admin/nodes/reorder', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ order })
      });
      statusEl.textContent = '排序已保存';
      statusEl.style.color = 'var(--primary)';
      await loadNodes();
      renderSortList(true);
    } catch (error) {
      statusEl.textContent = '保存失败：' + error.message;
      statusEl.style.color = 'var(--destructive)';
    } finally {
      btn.disabled = false;
    }
  }

  // --- temporary registration window -------------------------------------
  // The server hands out a node token for anyone holding the window's key, so
  // the key is only shown while the window is open and the countdown comes from
  // the server's clock.
  let registerTimer = null;
  let registerDeadline = 0;
  let registerOpen = false;

  function stopRegisterTimer() {
    if (registerTimer) {
      clearInterval(registerTimer);
      registerTimer = null;
    }
  }

  function formatRegisterLeft(seconds) {
    const left = Math.max(0, Math.floor(seconds));
    const minutes = Math.floor(left / 60);
    return minutes > 0 ? `${minutes} 分 ${left % 60} 秒` : `${left} 秒`;
  }

  function tickRegister() {
    const statusEl = $('registerStatus');
    if (!registerOpen) {
      statusEl.textContent = '当前没有开放的注册窗口。';
      stopRegisterTimer();
      return;
    }
    const left = Math.round((registerDeadline - Date.now()) / 1000);
    if (left <= 0) {
      // Re-read rather than guess: the key is gone from the server's answer too.
      statusEl.textContent = '注册窗口已关闭。';
      stopRegisterTimer();
      void loadRegisterWindow();
      return;
    }
    statusEl.textContent = '注册窗口开放中，剩余 ' + formatRegisterLeft(left) + '。';
  }

  function applyRegisterWindow(data) {
    registerOpen = !!(data && data.open);
    const minutes = $('registerMinutes');
    if (data && data.max_minutes) minutes.max = String(data.max_minutes);
    if (data && data.default_minutes) minutes.placeholder = String(data.default_minutes);
    $('registerCommand').value = registerOpen
      ? `curl -fsSL ${window.location.origin}/install.sh?register=${data.register_key} | bash`
      : '';
    $('copyRegisterCommandBtn').disabled = !registerOpen;
    $('closeRegisterWindowBtn').disabled = !registerOpen;
    // Re-opening rotates the key, so the button says what it will do.
    $('openRegisterWindowBtn').textContent = registerOpen ? '重新生成' : '开放注册';
    registerDeadline = Date.now() + Math.max(0, Number(data && data.seconds_left) || 0) * 1000;
    stopRegisterTimer();
    tickRegister();
    if (registerOpen) registerTimer = setInterval(tickRegister, 1000);
  }

  async function loadRegisterWindow() {
    try {
      applyRegisterWindow(await request('/api/admin/register'));
    } catch (error) {
      $('registerStatus').textContent = '无法读取注册状态：' + error.message;
    }
  }

  async function updateRegisterWindow(method, body) {
    try {
      applyRegisterWindow(await request('/api/admin/register', {
        method,
        headers: body ? { 'Content-Type': 'application/json' } : undefined,
        body: body ? JSON.stringify(body) : undefined
      }));
    } catch (error) {
      $('registerStatus').textContent = error.message;
    }
  }
  // --- wiring ------------------------------------------------------------
  function wire() {
    $('newNodeBtn').addEventListener('click', openNewNode);
    $('refreshNodesBtn').addEventListener('click', async () => {
      try {
        await loadNodes();
        status('节点列表已刷新', 'success');
      } catch (error) {
        status(error.message, 'error');
      }
    });
    $('nodeForm').addEventListener('submit', submitForm);
    $('nodeFormCancelBtn').addEventListener('click', closeForm);
    $('closeGuideBtn').addEventListener('click', () => { $('guideCard').hidden = true; });
    $('saveSortNodesBtn').addEventListener('click', saveSort);

    $('openRegisterWindowBtn').addEventListener('click', () => {
      const input = $('registerMinutes');
      const raw = Number(input.value || 0);
      const max = Number(input.max) || 1440;
      const minutes = Number.isFinite(raw) ? Math.min(max, Math.max(1, Math.round(raw))) : 60;
      void updateRegisterWindow('POST', { minutes });
    });
    $('closeRegisterWindowBtn').addEventListener('click', () => void updateRegisterWindow('DELETE'));
    $('copyRegisterCommandBtn').addEventListener('click', () => {
      const command = $('registerCommand').value;
      if (command) void copyText(command, '注册命令');
    });

    $('nodeTableBody').addEventListener('click', (event) => {
      const btn = event.target.closest('[data-action]');
      if (!btn) return;
      const row = btn.closest('tr');
      if (!row) return;
      const uuid = row.dataset.uuid;
      if (btn.dataset.action === 'edit') void openEditNode(uuid);
      else if (btn.dataset.action === 'guide') void showGuide(uuid);
      else if (btn.dataset.action === 'delete') void removeNode(uuid);
    });

    // Copy buttons read the command straight out of the code block.
    document.addEventListener('click', (event) => {
      const btn = event.target.closest('[data-copy-target]');
      if (!btn) return;
      const target = $(btn.dataset.copyTarget);
      if (target) void copyText(target.textContent, '命令');
    });

    initSort();
    // Node cards keep changing while the page is open; the table follows along.
    setInterval(() => { if (!document.hidden) void loadNodes().catch(() => {}); }, 15000);
  }

  // Menu shortcuts land here as ?new=1, ?sort=1, ?register=1 or ?node=<uuid>.
  function applyDeepLink() {
    const params = new URLSearchParams(location.search);
    const clean = () => history.replaceState(null, '', '/admin/nodes');
    if (params.has('new')) {
      openNewNode();
      clean();
    } else if (params.has('sort')) {
      openSort();
      clean();
    } else if (params.has('register')) {
      $('registerCard').scrollIntoView({ behavior: 'smooth', block: 'start' });
      clean();
    } else if (params.get('node')) {
      void openEditNode(params.get('node'));
      clean();
    }
  }

  ready(async () => {
    wire();
    await loadNodes();
    void loadRegisterWindow();
    applyDeepLink();
  });


})();
