// DOM regressions run against the shipped HTML and JavaScript, for every theme.
const { JSDOM } = require('jsdom');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const root = path.join(__dirname, '../internal/web/dist');
const settle = () => new Promise(resolve => setImmediate(resolve));
// Exercise the real HTML script order, including bootstrap and theme setup.
async function checkStartup() {
 const dom = new JSDOM(fs.readFileSync(path.join(root, 'index.html'), 'utf8'), {
  url: 'https://monitor.example.test', runScripts: 'outside-only', pretendToBeVisual: true
 });
 const w = dom.window, ctx = dom.getInternalVMContext(), requests = [], sockets = [], errors = [];
 const intervals = [];
 w.HTMLCanvasElement.prototype.getContext = () => null;
 w.matchMedia = () => ({matches: false, addEventListener() {}});
 w.setInterval = (fn, delay) => { intervals.push(delay); return intervals.length; };
 w.console.error = (...args) => errors.push(args.join(' '));
 w.console.log = () => {};
 w.WebSocket = class {
  constructor(url) { this.url = url; this.readyState = 1; sockets.push(this); }
  close() {}
 };
 w.fetch = async url => {
  requests.push(url);
  let data;
  if (url.startsWith('/api/nodes')) data = [{uuid:'startup', name:'Startup node', online:true}];
  else if (url === '/api/admin/status') data = {is_admin:true};
  else if (url === '/api/public') data = {site_title:'Startup title', site_theme:'hex', site_icon:''};
  else throw new Error('Unexpected startup request: ' + url);
  return {ok: true, json: async () => data};
 };
 try {
  for (const script of w.document.querySelectorAll('script[src]')) {
   const filename = script.getAttribute('src').slice(1);
   vm.runInContext(fs.readFileSync(path.join(root, filename), 'utf8'), ctx, {filename});
  }
  await settle(); await settle();
  assert.deepEqual(intervals, [1000, 5000, 15000]);
  assert.equal(sockets.length, 1);
  assert.equal(sockets[0].url, 'wss://monitor.example.test/api/clients?view=dashboard');
  assert(requests.includes('/api/admin/status'));
  assert.equal(w.document.title, 'Startup title');
  assert.equal(w.document.getElementById('adminQuickActions').style.display, 'block');
  sockets[0].onmessage({data: JSON.stringify({nodes:[{uuid:'live',name:'Live node',online:true}]})});
  vm.runInContext('renderNodes()',ctx);
  assert(w.document.getElementById('nodeGrid').textContent.includes('Live node'));
  assert.deepEqual(errors, []);
  console.log('HTML script order, bootstrap, authentication, settings, and live updates PASS');
 } finally { dom.window.close(); }
}
(async () => {
 await checkStartup();
 for (const theme of ['hex', 'rakugaki', 'win2000', 'design', 'serverstatus']) {
  const dom = new JSDOM(fs.readFileSync(path.join(root, 'index.html'), 'utf8'), {
   url: 'http://localhost', runScripts: 'outside-only', pretendToBeVisual: true
  });
  const w = dom.window, ctx = dom.getInternalVMContext();
  w.matchMedia = () => ({ matches: false, addEventListener() {} });
  w.confirm = () => true;
  const alerts = []; w.alert = text => alerts.push(text);
  vm.runInContext(fs.readFileSync(path.join(root, 'themes.js'), 'utf8'), ctx);
  for (const script of w.document.querySelectorAll('script[data-dashboard-module]')) {
   const filename = script.getAttribute('src').slice(1);
   vm.runInContext(fs.readFileSync(path.join(root, filename), 'utf8'), ctx, {filename});
  }
  w.document.documentElement.dataset.siteTheme = theme;
  w.initial = [{ uuid: 'a', name: 'original', online: true }, { uuid: 'b', name: 'other', online: true }];
  vm.runInContext('acceptNodeSnapshot(initial);updateGlobalStats();renderNodes();ws={readyState:1};wsHasData=true;lastWSDataAt=Date.now()', ctx);
  // Every theme exposes disk history through the shared resource modal.
  let diskQuery;
  w.fetch = async url => {
   diskQuery = new URL(url, 'http://localhost');
   const end = Math.floor(Date.now() / 1000);
   return {ok: true, json: async () => ({start_time: end-3600, end_time: end, step_seconds: 60,
    stats: {count: 2, current: 75, avg: 50, min: 25, max: 75}, samples: [{t: end-120,v:25},{t:end-60,v:75}]})};
  };
  const diskButton = w.document.querySelector('[data-action="resource"][data-metric="disk"]');
  assert(diskButton, theme + ' missing disk history button');
  diskButton.click(); await settle(); await settle();
  assert.equal(diskQuery.searchParams.get('metric'), 'disk');
  assert.equal(diskQuery.searchParams.get('uuid'), 'a');
  assert(w.document.getElementById('resourceModalTitle').textContent.includes('硬盘占用曲线'));
  assert.equal(w.document.getElementById('resourceStatCurrent').textContent, '75.0%');
  assert(w.document.querySelector('#resourceChartSvg path'));
  w.document.getElementById('resourceRange31d').click(); await settle(); await settle();
  assert.equal(diskQuery.searchParams.get('range'), '31d');
  w.document.getElementById('closeResourceChartModalBtn').click();
  // Billing traffic has its own cycle ranges and daily bars in every theme.
  w.fetch = async url => {
   diskQuery = new URL(url, 'http://localhost');
   return {ok:true,json:async()=>({start_date:'2026-10-15',end_date:'2026-11-14',reset_day:15,timezone:'UTC +00:00',total:3072,
    days:[{date:'2026-10-15',bytes:1024,status:'online',complete:true},{date:'2026-10-16',bytes:2048,status:'partial',complete:true},{date:'2026-10-17',bytes:0,status:'offline',complete:true}]})};
  };
  const trafficButton = w.document.querySelector('[data-action="resource"][data-metric="traffic"]');
  assert(trafficButton, theme + ' missing traffic history button');
  trafficButton.click(); await settle(); await settle();
  assert.equal(diskQuery.pathname, '/api/nodes/traffic-history');
  assert.equal(diskQuery.searchParams.get('range'), 'current');
  assert.equal(w.document.getElementById('resourceTimeRanges').style.display, 'none');
  assert.notEqual(w.document.getElementById('trafficTimeRanges').style.display, 'none');
  assert(w.document.getElementById('resourceModalTitle').textContent.includes('日流量汇总'));
  assert(w.document.getElementById('resourceModalSubtitle').textContent.includes('2026-10-15 至 2026-11-14'));
  assert.equal(w.document.querySelectorAll('#resourceChartSvg .traffic-day').length, 3);
  assert(w.document.getElementById('resourceChartSvg').textContent.includes('0 B'));
  assert.equal(w.document.querySelector('.traffic-day[data-status=online] rect').getAttribute('fill'), '#238364');
  assert.equal(w.document.querySelector('.traffic-day[data-status=offline] rect').getAttribute('fill'), '#dc4444');
  assert.equal(w.document.querySelector('.traffic-day[data-status=partial] rect').getAttribute('fill'), '#d49120');
  assert.equal(w.document.getElementById('resourceStatCurrent').textContent, w.formatBytes(3072));
  for (const range of ['previous','three']) {
   w.document.getElementById(`trafficRange${range}`).click(); await settle(); await settle();
   assert.equal(diskQuery.searchParams.get('range'),range);
  }
  diskButton.click(); await settle(); await settle();
  assert.equal(w.document.getElementById('trafficTimeRanges').style.display, 'none');
  assert.equal(w.document.getElementById('resourceChartSvg').style.minWidth, '');
  w.document.getElementById('closeResourceChartModalBtn').click();
  const fresh = () => vm.runInContext('wsHasData=true;lastWSDataAt=Date.now()', ctx);
  // Reopening the sort dialog must move exactly one row per click.
  vm.runInContext("acceptNodeSnapshot([{uuid:'a',name:'A'},{uuid:'b',name:'B'},{uuid:'c',name:'C'}]);openSortNodesModal();openSortNodesModal()", ctx);
  const sortList = w.document.getElementById('sortNodesList');
  sortList.querySelector('.sort-down-btn').click();
  assert.deepEqual([...sortList.children].map(item => item.dataset.uuid), ['b', 'a', 'c']);
  sortList.children[1].querySelector('.sort-up-btn').click();
  assert.deepEqual([...sortList.children].map(item => item.dataset.uuid), ['a', 'b', 'c']);
  let savedOrder;
  w.fetch = async (url, options = {}) => {
   if (options.method === 'PUT') savedOrder = JSON.parse(options.body).order;
   return { ok: true, json: async () => w.initial };
  };
  w.document.getElementById('saveSortNodesBtn').click();
  await settle(); await settle();
  assert.deepEqual(savedOrder, [{uuid:'a',weight:1},{uuid:'b',weight:2},{uuid:'c',weight:3}]);
  vm.runInContext('acceptNodeSnapshot(initial)', ctx);
  assert.equal(w.document.getElementById('newNodeOrder'), null);
  // Open immediately, lock incomplete forms, and reject stale profile responses.
  vm.runInContext('setAdminState(true)', ctx);
  const profiles = [];
  w.fetch = (url, options) => new Promise((resolve, reject) => profiles.push({url, options, resolve, reject}));
  const editForm = w.document.getElementById('editNodeForm');
  const editStatus = w.document.getElementById('editNodeStatus');
  const editSubmit = editForm.querySelector('[type="submit"]');
  const firstProfile = w.openEditModal('a');
  assert(w.document.getElementById('editNodeModal').classList.contains('active'));
  assert(editStatus.textContent.includes('加载'));
  assert(editSubmit.disabled);
  editForm.dispatchEvent(new w.Event('submit', {cancelable:true}));
  assert.equal(profiles.length, 1, 'incomplete profile was submitted');
  const secondProfile = w.openEditModal('b');
  assert(profiles[0].options.signal.aborted);
  profiles[1].resolve({ok:true,json:async()=>({profile:{targets:[],due_date:'2027-01-02'}})});
  await secondProfile;
  profiles[0].resolve({ok:true,json:async()=>({profile:{targets:[],due_date:'2026-01-01'}})});
  await firstProfile;
  assert.equal(w.document.getElementById('editNodeUUID').value, 'b');
  assert.equal(w.document.getElementById('editNodeDue').value, '2027-01-02');
  assert.equal(editSubmit.disabled, false);
  const closedProfile = w.openEditModal('a');
  w.closeModal('editNodeModal');
  assert(profiles[2].options.signal.aborted);
  profiles[2].resolve({ok:true,json:async()=>({profile:{targets:[],due_date:'2026-01-01'}})});
  await closedProfile;
  assert.equal(w.document.getElementById('editNodeModal').classList.contains('active'), false);
  const failedProfile = w.openEditModal('a');
  profiles[3].reject(new Error('offline'));
  await failedProfile;
  assert(editStatus.textContent.includes('加载失败'));
  assert(editSubmit.disabled);
  assert.equal(w.document.getElementById('retryEditNodeBtn').hidden, false);
  w.document.getElementById('retryEditNodeBtn').click();
  profiles[4].resolve({ok:true,json:async()=>({profile:{targets:[]}})});
  await settle();
  assert.equal(editSubmit.disabled, false);
  assert(editStatus.hidden);
  assert(w.document.getElementById('retryEditNodeBtn').hidden);
  // Both forms reject duplicate submissions while a request is pending.
  for (const [id, method] of [['addNodeForm', 'POST'], ['editNodeForm', 'PUT']]) {
   let writes = 0, reads = 0, release;
   const wait = new Promise(resolve => release = resolve);
   w.document.getElementById('newNodeName').value = 'created';
   w.document.getElementById('editNodeUUID').value = 'a';
   w.document.getElementById('editNodeName').value = 'edited';
   const form = w.document.getElementById(id);
   w.showGuide = async () => {};
   w.fetch = async (url, options = {}) => {
    if (options.method === method) { assert.equal(Object.hasOwn(JSON.parse(options.body), 'weight'), false); writes++; await wait; return { ok: true, json: async () => ({ node: { uuid: 'new' } }) }; }
    reads++; return { ok: true, json: async () => [{ uuid: 'a', name: 'edited', online: true }, { uuid: 'new', name: 'created' }] };
   };
   fresh();
   form.dispatchEvent(new w.Event('submit', { cancelable: true }));
   form.dispatchEvent(new w.Event('submit', { cancelable: true }));
   assert.equal(writes, 1, id + ' duplicate submission');
   assert(form.querySelector('[type="submit"]').disabled);
   if (method === 'PUT') {
    assert(editStatus.textContent.includes('正在保存'));
    assert(w.document.getElementById('deleteNodeBtn').disabled);
    const requestsBefore = writes;
    w.document.getElementById('deleteNodeBtn').click();
    assert.equal(writes, requestsBefore);
   }
   release(); await settle(); await settle();
   assert.equal(reads, 1, id + ' skipped fresh-WS refresh');
   assert.equal(form.querySelector('[type="submit"]').disabled, false);
   assert.equal(vm.runInContext('nodes[0].name', ctx), 'edited');
   // Failed requests release the lock and preserve form input for retry.
   w.fetch = async () => { throw new Error('simulated failure'); };
   form.dispatchEvent(new w.Event('submit', { cancelable: true })); await settle();
   assert.equal(form.querySelector('[type="submit"]').disabled, false);
  }
  // A late HTTP response cannot overwrite a newer WS snapshot.
  let releaseRead;
  const old = new Promise(resolve => releaseRead = resolve);
  w.fetch = async () => ({ ok: true, json: async () => old });
  const pending = w.fetchNodes(true);
  await settle();
  vm.runInContext("acceptNodeSnapshot([{uuid:'a',name:'latest',online:true}])", ctx);
  releaseRead([{uuid:'a',name:'stale'}]); await pending;
  assert.equal(vm.runInContext('nodes[0].name', ctx), 'latest');
  // Last-known online data must be explicitly labelled stale on disconnect.
  vm.runInContext('ws.readyState=3;wsHasData=false;lastDashboardUpdate=Date.now()-30000;dashboardConnectionFailed=true;updateConnectionStatus()', ctx);
  assert(w.document.getElementById('connectionStatus').textContent.includes('数据已过期'));
  assert.equal(w.document.getElementById('nodeGrid').dataset.stale, 'true');
  vm.runInContext("acceptNodeSnapshot([{uuid:'a',name:'latest',online:true}])", ctx);
  assert.equal(w.document.getElementById('nodeGrid').dataset.stale, 'false');
  // A slow/failed deletion shows progress and keeps the node available for retry.
  w.fetch = async () => ({ok:true,json:async()=>({profile:{targets:[]}})});
  await w.openEditModal('a');
  let finishDelete;
  w.fetch = () => new Promise(resolve => finishDelete = resolve);
  const deleteButton = w.document.getElementById('deleteNodeBtn');
  deleteButton.click();
  assert(editStatus.textContent.includes('正在删除'));
  assert(deleteButton.disabled);
  assert(editSubmit.disabled);
  assert.equal(vm.runInContext('nodes.length', ctx), 1);
  finishDelete({ok:false,json:async()=>({error:'simulated delete failure'})});
  await settle(); await settle();
  assert.equal(deleteButton.disabled, false);
  assert.equal(editSubmit.disabled, false);
  assert.equal(vm.runInContext('nodes.length', ctx), 1);
  assert(w.document.getElementById('editNodeModal').classList.contains('active'));
  assert(alerts.pop().includes('simulated delete failure'));
  // Successful deletion survives stale HTTP/WS data, including the last node.
  fresh(); w.fetch = async (url, options = {}) => ({ ok: true, json: async () => options.method ? {} : [{uuid:'a',name:'old'}] });
  await w.deleteNode('a'); await settle();
  vm.runInContext("acceptNodeSnapshot([{uuid:'a',name:'old'}]);updateGlobalStats();renderNodes()", ctx);
  assert(w.document.getElementById('nodeGrid').textContent.includes('暂无监控节点'));
  vm.runInContext("acceptNodeSnapshot([{uuid:'brand-new',name:'new'}]);renderNodes()", ctx);
  assert.equal(w.document.querySelectorAll('#nodeGrid article').length, 1);
  // Editing unrelated metadata must preserve the exact stored quota, including
  // values smaller than 0.1 GiB and byte counts beyond JS integer precision.
  for (const quota of [Math.floor(0.04 * 2**30), Math.floor(100.04 * 2**30), 2**53 + 2]) {
   let saved;
   w.quotaNode = {uuid: 'quota', name: 'before', traffic_limit: quota, reset_day: 15, online: true};
   vm.runInContext('setAdminState(true);acceptNodeSnapshot([quotaNode])', ctx);
   w.fetch = async (url, options = {}) => {
    if (url.endsWith('/profile')) return {ok: true, json: async () => ({profile: {targets: []}})};
    if (options.method === 'PUT') saved = JSON.parse(options.body);
    return {ok: true, json: async () => [w.quotaNode]};
   };
   await w.openEditModal('quota');
   const input = w.document.getElementById('editNodeTrafficLimit');
   assert(Number(input.value) > 0, 'small quota rounded to zero');
   w.document.getElementById('editNodeName').value = 'after';
   const form = w.document.getElementById('editNodeForm');
   form.dispatchEvent(new w.Event('submit', {cancelable: true}));
   await settle(); await settle();
   assert.equal(saved.name, 'after');
   assert.equal(Object.hasOwn(saved, 'traffic_limit_gb'), false, 'unchanged quota must be omitted');
   for (const value of ['100.04', '0']) {
    await w.openEditModal('quota');
    input.value = value;
    form.dispatchEvent(new w.Event('submit', {cancelable: true}));
    await settle(); await settle();
    assert.equal(saved.traffic_limit_gb, Number(value), 'explicit quota edit lost');
   }
  }
  assert.equal(alerts.length, 2);
  console.log(theme + ': form locks, retries, immediate refresh, stale response protection, connection status, deletion, disk history, daily traffic, quota precision PASS');
  dom.window.close();
 }
})().catch(error => { console.error(error); process.exit(1); });
