// DOM regressions run against the shipped HTML and JavaScript, for every theme.
const { JSDOM } = require('jsdom');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const root = path.join(__dirname, '../internal/web/dist');
const settle = () => new Promise(resolve => setImmediate(resolve));
(async () => {
 for (const theme of ['hex', 'rakugaki', 'win2000', 'design', 'serverstatus']) {
  const dom = new JSDOM(fs.readFileSync(path.join(root, 'index.html'), 'utf8'), {
   url: 'http://localhost', runScripts: 'outside-only', pretendToBeVisual: true
  });
  const w = dom.window, ctx = dom.getInternalVMContext();
  w.matchMedia = () => ({ matches: false, addEventListener() {} });
  w.confirm = () => true;
  const alerts = []; w.alert = text => alerts.push(text);
  vm.runInContext(fs.readFileSync(path.join(root, 'themes.js'), 'utf8'), ctx);
  vm.runInContext(fs.readFileSync(path.join(root, 'app.js'), 'utf8').split('// Initialize')[0], ctx);
  w.document.documentElement.dataset.siteTheme = theme;
  w.initial = [{ uuid: 'a', name: 'original', online: true }, { uuid: 'b', name: 'other', online: true }];
  vm.runInContext('acceptNodeSnapshot(initial);updateGlobalStats();renderNodes();ws={readyState:1};wsHasData=true;lastWSDataAt=Date.now()', ctx);
  const fresh = () => vm.runInContext('wsHasData=true;lastWSDataAt=Date.now()', ctx);
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
    if (options.method === method) { writes++; await wait; return { ok: true, json: async () => ({ node: { uuid: 'new' } }) }; }
    reads++; return { ok: true, json: async () => [{ uuid: 'a', name: 'edited', online: true }, { uuid: 'new', name: 'created' }] };
   };
   fresh();
   form.dispatchEvent(new w.Event('submit', { cancelable: true }));
   form.dispatchEvent(new w.Event('submit', { cancelable: true }));
   assert.equal(writes, 1, id + ' duplicate submission');
   assert(form.querySelector('[type="submit"]').disabled);
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
  // Successful deletion survives stale HTTP/WS data, including the last node.
  fresh(); w.fetch = async (url, options = {}) => ({ ok: true, json: async () => options.method ? {} : [{uuid:'a',name:'old'}] });
  await w.deleteNode('a'); await settle();
  vm.runInContext("acceptNodeSnapshot([{uuid:'a',name:'old'}]);updateGlobalStats();renderNodes()", ctx);
  assert(w.document.getElementById('nodeGrid').textContent.includes('暂无监控节点'));
  vm.runInContext("acceptNodeSnapshot([{uuid:'brand-new',name:'new'}]);renderNodes()", ctx);
  assert.equal(w.document.querySelectorAll('#nodeGrid article').length, 1);
  assert.equal(alerts.length, 2);
  console.log(theme + ': form locks, retries, immediate refresh, stale response protection, connection status, deletion PASS');
  dom.window.close();
 }
})().catch(error => { console.error(error); process.exit(1); });
