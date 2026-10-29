// Traffic inputs use GiB; the upper bound keeps conversion inside int64.
function validTrafficGB(value) {
  return Number.isFinite(value) && value >= 0 && value < 8589934592;
}

function validResetDay(value) {
  return Number.isInteger(value) && value >= 0 && value <= 31;
}

function readNodeProfile(prefix) {
  const warningText = document.getElementById(prefix+'NodeTrafficWarning').value.trim();
  const trafficWarningPercent = warningText === '' ? 0 : Number(warningText);
  if (!Number.isFinite(trafficWarningPercent) || trafficWarningPercent < 0 || trafficWarningPercent > 100) {
    throw new Error('流量预警基准必须在 0 到 100 之间，0 表示不提前预警');
  }
  const thresholdText = document.getElementById(prefix+'NodeCPUThreshold').value.trim();
  const cpuThreshold = thresholdText === '' ? null : Number(thresholdText);
  if (cpuThreshold !== null && (!Number.isFinite(cpuThreshold) || cpuThreshold < 0 || cpuThreshold > 100)) {
    throw new Error('CPU 负载阈值必须在 0 到 100 之间');
  }
  const targets = document.getElementById(prefix + 'NodeTargets').value.split('\n').filter(l => l.trim()).map(line => {
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
    return {name, host};
  });
  return {targets, due_date: document.getElementById(prefix+'NodeDue').value,
    payment_cycle: document.getElementById(prefix+'NodeCycle').value,
    price: Number(document.getElementById(prefix+'NodePrice').value || 0),
    currency: document.getElementById(prefix+'NodeCurrency').value,
    cpu_threshold: cpuThreshold,
    traffic_warning_percent: trafficWarningPercent,
    alerts_disabled: document.getElementById(prefix+'NodeAlertsDisabled').checked};
}
function fillNodeProfile(prefix, profile) {
  const p = profile || {};
  document.getElementById(prefix+'NodeTargets').value = (p.targets || []).map(t => `${t.name},${t.host}`).join('\n');
  document.getElementById(prefix+'NodeDue').value = p.due_date || '';
  document.getElementById(prefix+'NodeCycle').value = p.payment_cycle || '';
  document.getElementById(prefix+'NodePrice').value = p.price || '';
  document.getElementById(prefix+'NodeCurrency').value = p.currency || 'CNY';
  document.getElementById(prefix+'NodeCPUThreshold').value = p.cpu_threshold == null ? '' : p.cpu_threshold;
  document.getElementById(prefix+'NodeTrafficWarning').value = p.traffic_warning_percent ?? 0;
  document.getElementById(prefix+'NodeAlertsDisabled').checked = !!p.alerts_disabled;
}
// Admin Actions
window.showGuide = async function(uuid) {
  let token;
  try {
    const res = await fetch(`/api/admin/nodes/${encodeURIComponent(uuid)}/token`, {
      credentials: 'same-origin'
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
  document.getElementById('guideUninstallCmd').textContent = `curl -fsSL ${host}/install.sh?token=${encodeURIComponent(token)} | bash -s -- agent-uninstall`;
  document.getElementById('guideToken').textContent = token;
  openModal('nodeGuideModal');
};

window.copyGuideCommand = async function(id) {
  const value = document.getElementById(id).textContent;
  try {
    await navigator.clipboard.writeText(value);
    alert('命令已复制');
  } catch (e) {
    alert('复制失败，请手动复制命令');
  }
};

// A failed response does not prove the transaction failed (e.g. proxy timeout).
async function requestNodeDeletion(uuid) {
  let failure;
  try {
    const res = await fetch(`/api/admin/nodes/${encodeURIComponent(uuid)}`, {
      method: 'DELETE', credentials: 'same-origin'
    });
    if (res.ok) return;
    const data = await res.json().catch(() => ({}));
    failure = new Error(data.error || `HTTP ${res.status}`);
  } catch (error) {
    failure = error;
  }
  try {
    const check = await fetch(`/api/admin/nodes/${encodeURIComponent(uuid)}/profile`, {
      credentials: 'same-origin', cache: 'no-store'
    });
    const data = await check.json();
    // Only the authenticated API's explicit not-found response confirms deletion.
    if (check.status === 404 && data?.error === 'node not found') return;
  } catch (error) {
    console.warn('Unable to verify node deletion:', error);
  }
  console.warn('Node deletion was not confirmed:', failure);
  throw new Error(`删除结果尚未确认（${failure.message}），请刷新页面核实后再操作`);
}

function applyNodeDeletion(uuid) {
  deletedNodeIDs.add(uuid);
  dashboardRevision++;
  nodes = nodes.filter(node => node.uuid !== uuid);
  const editUUID = document.getElementById('editNodeUUID');
  if (editUUID?.value === uuid) closeModal('editNodeModal');
  if (typeof currentPingNodeUUID !== 'undefined' && currentPingNodeUUID === uuid) closeModal('pingChartModal');
  if (typeof currentResourceNodeUUID !== 'undefined' && currentResourceNodeUUID === uuid) closeModal('resourceChartModal');
}

async function deleteNodeAndRefresh(uuid) {
  if (deletingNodeIDs.has(uuid)) return;
  deletingNodeIDs.add(uuid);
  try {
    await requestNodeDeletion(uuid);
    // Once confirmed, UI errors must never be reported as a deletion failure.
    try {
      applyNodeDeletion(uuid);
      scheduleDashboard();
    } catch (error) {
      console.error('Node deleted, but UI update failed:', error);
      alert('节点已删除，但页面更新失败，请刷新页面');
    } finally {
      // Reconcile even when closing a modal or scheduling the render throws.
      try {
        await fetchNodes(true);
      } catch (error) {
        console.error('Node deleted, but dashboard refresh failed:', error);
      }
    }
  } finally {
    deletingNodeIDs.delete(uuid);
  }
}

window.deleteNode = async function(uuid) {
  if (!confirm('确定要删除该监控节点吗？')) return;
  try {
    await deleteNodeAndRefresh(uuid);
  } catch (e) {
    alert(e.message);
  }
};

// --- Node Sort Modal ---
const sortNodesBtn = document.getElementById('sortNodesBtn');
if (sortNodesBtn) sortNodesBtn.addEventListener('click', () => {
  nodeManagementMenu.open = false;
  requireAdmin(() => {
    openSortNodesModal();
  });
});

function openSortNodesModal() {
  const list = document.getElementById('sortNodesList');
  document.getElementById('sortNodesStatus').textContent = '';
  list.innerHTML = '';
  if (nodes.length === 0) {
    list.textContent = '暂无节点，请先通过"节点管理 → 新建节点"创建。';
    openModal('sortNodesModal');
    return;
  }
  const sorted = [...nodes].sort(compareNodes);
  sorted.forEach((node, idx) => {
    const item = document.createElement('div');
    item.className = 'sort-node-item';
    item.draggable = true;
    item.dataset.uuid = node.uuid;
    item.dataset.originalWeight = node.weight || 0;
    const weight = Number(node.weight) || 0;
    item.innerHTML = `<span class="sort-node-grip" title="拖动排序">⠿</span>`
      + `<span class="sort-node-name">${escapeHtml(node.name || node.uuid)}</span>`
      + `<span class="sort-node-order" title="当前序号">${weight}</span>`
      + `<span class="sort-node-actions">`
      + `<button type="button" class="sort-up-btn" title="上移" aria-label="上移">▲</button>`
      + `<button type="button" class="sort-down-btn" title="下移" aria-label="下移">▼</button>`
      + `</span>`;
    list.appendChild(item);
  });
  updateSortButtons();
  initSortDragDrop(list);
  document.getElementById('sortNodesStatus').textContent = '';
  openModal('sortNodesModal');
}

function updateSortButtons() {
  const list = document.getElementById('sortNodesList');
  const items = list.querySelectorAll('.sort-node-item');
  items.forEach((item, i) => {
    const upBtn = item.querySelector('.sort-up-btn');
    const downBtn = item.querySelector('.sort-down-btn');
    if (upBtn) upBtn.disabled = i === 0;
    if (downBtn) downBtn.disabled = i === items.length - 1;
  });
}

function updateSortOrderBadges() {
  const items = document.getElementById('sortNodesList').querySelectorAll('.sort-node-item');
  items.forEach((item, i) => {
    const badge = item.querySelector('.sort-node-order');
    if (badge) badge.textContent = String(i + 1);
  });
}

function initSortDragDrop(list) {
  if (list.dataset.sortInitialized) return;
  list.dataset.sortInitialized = 'true';
  let dragItem = null;

  // Move up/down button clicks
  list.addEventListener('click', (e) => {
    const upBtn = e.target.closest('.sort-up-btn');
    const downBtn = e.target.closest('.sort-down-btn');
    if (!upBtn && !downBtn) return;
    const item = e.target.closest('.sort-node-item');
    if (!item) return;
    if (upBtn && item.previousElementSibling) {
      item.parentNode.insertBefore(item, item.previousElementSibling);
    } else if (downBtn && item.nextElementSibling) {
      item.parentNode.insertBefore(item.nextElementSibling, item);
    }
    updateSortButtons();
    updateSortOrderBadges();
  });

  // Drag-and-drop
  list.addEventListener('dragstart', (e) => {
    dragItem = e.target.closest('.sort-node-item');
    if (!dragItem) return;
    dragItem.classList.add('dragging');
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', '');
  });

  list.addEventListener('dragend', () => {
    if (dragItem) dragItem.classList.remove('dragging');
    list.querySelectorAll('.drag-over').forEach(el => el.classList.remove('drag-over'));
    dragItem = null;
    updateSortButtons();
    updateSortOrderBadges();
  });

  list.addEventListener('dragover', (e) => {
    if (!dragItem) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    const target = e.target.closest('.sort-node-item');
    if (!target || target === dragItem) return;
    list.querySelectorAll('.drag-over').forEach(el => el.classList.remove('drag-over'));
    target.classList.add('drag-over');
    const rect = target.getBoundingClientRect();
    const midY = rect.top + rect.height / 2;
    if (e.clientY < midY) {
      list.insertBefore(dragItem, target);
    } else {
      list.insertBefore(dragItem, target.nextSibling);
    }
  });

  list.addEventListener('dragleave', (e) => {
    const target = e.target.closest('.sort-node-item');
    if (target) target.classList.remove('drag-over');
  });

  list.addEventListener('drop', (e) => {
    e.preventDefault();
    list.querySelectorAll('.drag-over').forEach(el => el.classList.remove('drag-over'));
  });

  // Touch drag support for mobile
  let touchItem = null;

  list.addEventListener('touchstart', (e) => {
    const grip = e.target.closest('.sort-node-grip');
    if (!grip) return;
    touchItem = grip.closest('.sort-node-item');
    if (!touchItem) return;
    touchItem.classList.add('dragging');
  }, { passive: true });

  list.addEventListener('touchmove', (e) => {
    if (!touchItem) return;
    e.preventDefault();
    const touch = e.touches[0];
    const items = [...list.querySelectorAll('.sort-node-item')];
    for (const item of items) {
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

  const finishTouchSort = () => {
    if (touchItem) {
      touchItem.classList.remove('dragging');
      touchItem = null;
      updateSortButtons();
      updateSortOrderBadges();
    }
  };
  list.addEventListener('touchend', finishTouchSort, { passive: true });
  list.addEventListener('touchcancel', finishTouchSort, { passive: true });
}

document.getElementById('saveSortNodesBtn').addEventListener('click', async () => {
  const items = document.getElementById('sortNodesList').querySelectorAll('.sort-node-item');
  if (!items.length) return;
  const btn = document.getElementById('saveSortNodesBtn');
  const status = document.getElementById('sortNodesStatus');
  btn.disabled = true;
  status.textContent = '正在保存…';
  status.style.color = '';
  const order = [];
  items.forEach((item, i) => {
    order.push({ uuid: item.dataset.uuid, weight: i + 1 });
  });
  try {
    const res = await fetch('/api/admin/nodes/reorder', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ order })
    });
    if (res.ok) {
      status.textContent = '排序已保存';
      status.style.color = 'var(--primary)';
      void fetchNodes(true);
      setTimeout(() => closeModal('sortNodesModal'), 600);
    } else {
      const data = await res.json().catch(() => ({}));
      status.textContent = '保存失败: ' + (data.error || '未知错误');
      status.style.color = 'var(--destructive)';
    }
  } catch (e) {
    status.textContent = '请求失败: ' + e.message;
    status.style.color = 'var(--destructive)';
  } finally {
    btn.disabled = false;
  }
});

document.getElementById('addNodeForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const form = e.currentTarget;
  if (pendingNodeForms.has(form)) return;
  const name = document.getElementById('newNodeName').value;
  const region = document.getElementById('newNodeRegion').value;
  const trafficLimitGB = Number(document.getElementById('newNodeTrafficLimit').value || 0);
  const resetDay = Number(document.getElementById('newNodeResetDay').value || 0);
  const initialUsedGB = Number(document.getElementById('newNodeInitialUsed').value || 0);
  if (!validTrafficGB(trafficLimitGB) || !validTrafficGB(initialUsedGB) || !validResetDay(resetDay)) {
    alert('流量必须是非负数字且小于 8589934592 GiB；重置日为 1–31，留空默认 1 日');
    return;
  }

  const submit = form.querySelector('button[type="submit"]');
  pendingNodeForms.add(form);
  if (submit) submit.disabled = true;
  try {
    const res = await fetch('/api/admin/nodes', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      credentials: 'same-origin',
      body: JSON.stringify({
        name,
        region,
        traffic_limit_gb: trafficLimitGB,
        reset_day: resetDay,
        initial_used_gb: initialUsedGB,
        profile: readNodeProfile('new')
      })
    });
    const data = await res.json();
    if (data.node) {
      closeModal('addNodeModal');
      void fetchNodes(true);
      showGuide(data.node.uuid);
    } else {
      alert('创建失败: ' + (data.error || '未知错误'));
    }
  } catch (e) {
    alert('请求失败: ' + e.message);
  } finally {
    pendingNodeForms.delete(form);
    if (submit) submit.disabled = false;
  }
});

let editNodeLoadController = null;
let editNodeProfileReady = false;

function cancelEditNodeLoad() {
  if (editNodeLoadController) editNodeLoadController.abort();
  editNodeLoadController = null;
}

function setEditNodeBusy(message) {
  const form = document.getElementById('editNodeForm');
  form.setAttribute('aria-busy', String(!!message));
  for (const control of form.querySelectorAll('input, textarea, select, button')) {
    if (control.id !== 'cancelEditNodeModalBtn') control.disabled = !!message;
  }
  const status = document.getElementById('editNodeStatus');
  status.textContent = message;
  status.hidden = !message;
}

document.getElementById('retryEditNodeBtn').addEventListener('click', () => {
  openEditModal(document.getElementById('editNodeUUID').value);
});

window.openEditModal = async function(uuid) {
  const node = nodes.find(n => n.uuid === uuid);
  if (!node || !isAdmin) return;
  const form = document.getElementById('editNodeForm');
  if (pendingNodeForms.has(form) || deletingNodeIDs.has(document.getElementById('editNodeUUID').value)) return;
  cancelEditNodeLoad();
  const controller = new AbortController();
  editNodeLoadController = controller;
  editNodeProfileReady = false;
  document.getElementById('editNodeUUID').value = node.uuid;
  document.getElementById('editNodeName').value = node.name || '';
  document.getElementById('editNodeRegion').value = node.region || '';
  const quotaInput = document.getElementById('editNodeTrafficLimit');
  quotaInput.value = node.traffic_limit > 0 ? String(node.traffic_limit / (1024*1024*1024)) : '';
  quotaInput.dataset.originalValue = quotaInput.value;
  document.getElementById('editNodeResetDay').value = node.reset_day > 0 ? node.reset_day : '';
  const currentUsed = Math.max(0, Number(node.cycle_total_used) || 0);
  const sampledAt = node.last_report?.updated_at ? new Date(node.last_report.updated_at) : null;
  const sampleHint = sampledAt && !Number.isNaN(sampledAt.getTime()) ? `最近采样：${sampledAt.toLocaleString()}` : '尚无探针采样';
  document.getElementById('editNodeCycleUsed').value = '';
  document.getElementById('editNodeCycleUsed').placeholder = (currentUsed / (1024*1024*1024)).toFixed(3);
  document.getElementById('editNodeCycleUsedHint').textContent = `当前已用 ${formatBytes(currentUsed)}；${sampleHint}。留空不修改，填 0 可清零。`;
  fillNodeProfile('edit', null);
  setEditNodeBusy('正在加载节点配置…');
  document.getElementById('retryEditNodeBtn').hidden = true;
  openModal('editNodeModal');
  try {
    const res = await fetch(`/api/admin/nodes/${encodeURIComponent(uuid)}/profile`, {
      credentials: 'same-origin', cache: 'no-store', signal: controller.signal
    });
    if (!res.ok) throw new Error('无法读取节点配置，请确认登录状态后重试');
    const data = await res.json();
    if (controller !== editNodeLoadController || controller.signal.aborted) return;
    fillNodeProfile('edit', data.profile);
    editNodeProfileReady = true;
    setEditNodeBusy('');
  } catch (error) {
    if (controller !== editNodeLoadController || controller.signal.aborted) return;
    setEditNodeBusy('加载失败：' + error.message);
    form.setAttribute('aria-busy', 'false');
    document.getElementById('retryEditNodeBtn').hidden = false;
  } finally {
    if (controller === editNodeLoadController) editNodeLoadController = null;
  }
};

document.getElementById('editNodeGuideBtn').addEventListener('click', () => {
  const uuid = document.getElementById('editNodeUUID').value;
  if (uuid) showGuide(uuid);
});

document.getElementById('editNodeForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const form = e.currentTarget;
  if (!editNodeProfileReady || pendingNodeForms.has(form) || deletingNodeIDs.has(document.getElementById('editNodeUUID').value)) return;
  const uuid = document.getElementById('editNodeUUID').value;
  const name = document.getElementById('editNodeName').value;
  const node = nodes.find(node => node.uuid === uuid);
  const group = node?.group || ''; // Preserve legacy metadata when editing.
  const region = document.getElementById('editNodeRegion').value;
  const quotaInput = document.getElementById('editNodeTrafficLimit');
  const trafficLimitGB = quotaInput.value.trim() === '' ? 0 : Number(quotaInput.value);
  const quotaChanged = quotaInput.value !== quotaInput.dataset.originalValue;
  const resetDay = Number(document.getElementById('editNodeResetDay').value || 0);
  const cycleUsedText = document.getElementById('editNodeCycleUsed').value.trim();
  const cycleUsedGB = cycleUsedText === '' ? null : Number(cycleUsedText);
  if (!validTrafficGB(trafficLimitGB) || (cycleUsedGB !== null && !validTrafficGB(cycleUsedGB)) || !validResetDay(resetDay)) {
    alert('流量必须是非负数字且小于 8589934592 GiB；重置日为 1–31，留空默认 1 日');
    return;
  }

  const submit = form.querySelector('button[type="submit"]');
  pendingNodeForms.add(form);
  setEditNodeBusy('正在保存修改…');
  if (submit) submit.textContent = '保存中…';
  try {
    const res = await fetch(`/api/admin/nodes/${uuid}`, {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
      },
      credentials: 'same-origin',
      body: JSON.stringify({
        name,
        group,
        region,
        ...(quotaChanged ? { traffic_limit_gb: trafficLimitGB } : {}),
        reset_day: resetDay,
        ...(cycleUsedGB === null ? {} : { cycle_used_gb: cycleUsedGB }),
        profile: readNodeProfile('edit')
      })
    });
    if (res.ok) {
      closeModal('editNodeModal');
      void fetchNodes(true);
    } else {
      const data = await res.json().catch(() => ({}));
      alert('修改失败: ' + (data.error || '未知错误'));
    }
  } catch (e) {
    alert('请求失败: ' + e.message);
  } finally {
    pendingNodeForms.delete(form);
    setEditNodeBusy('');
    if (submit) submit.textContent = '保存修改';
  }
});

document.getElementById('deleteNodeBtn').addEventListener('click', async () => {
  const uuid = document.getElementById('editNodeUUID').value;
  if (!uuid || !editNodeProfileReady || deletingNodeIDs.has(uuid) || pendingNodeForms.has(document.getElementById('editNodeForm'))) return;
  if (!confirm('确定要删除该节点吗？此操作不可恢复。')) return;
  const button = document.getElementById('deleteNodeBtn');
  setEditNodeBusy('正在删除节点及历史数据…');
  button.textContent = '删除中…';
  try {
    await deleteNodeAndRefresh(uuid);
  } catch (e) {
    alert(e.message);
  } finally {
    setEditNodeBusy('');
    button.textContent = '删除节点';
  }
});
