const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');

function dashboard() {
  const elements = new Map();
  const element = () => ({
    innerHTML: '', textContent: '', style: {}, dataset: {}, events: {}, children: [],
    classList: { add() {}, remove() {} },
    addEventListener(name, callback) { this.events[name] = callback; },
    setAttribute() {}, appendChild(child) { this.children.push(child); }
  });
  const document = {
    documentElement: element(),
    getElementById(id) { if (!elements.has(id)) elements.set(id, element()); return elements.get(id); },
    createElement: element,
  };
  const context = vm.createContext({
    document, localStorage: { getItem: () => 'admin-session', removeItem(key) { this.removed = key; } }, console,
    alert(message) { throw new Error(message); },
  });
  context.window = context;
  context.location = { origin: 'https://monitor.example' };
  const source = fs.readFileSync(path.join(__dirname, 'dist/app.js'), 'utf8');
  // Test rendering and actions without starting network connections or timers.
  vm.runInContext(source.split('// Initialize')[0], context);
  return { context, document, run: code => vm.runInContext(code, context) };
}

test('menu focus loss without a new focus target does not swallow submenu clicks', () => {
  const app = dashboard();
  const menu = app.document.getElementById('nodeManagementMenu');
  menu.open = true;
  menu.contains = () => false;
  menu.events.focusout({relatedTarget: null});
  assert.equal(menu.open, true);
  menu.events.focusout({relatedTarget: {}});
  assert.equal(menu.open, false);
});

test('existing node selection populates the edit form', () => {
  const app = dashboard();
  app.run('isAdmin = true; nodes = [{uuid:"node-1", name:"Tokyo", group:"Production", region:"JP", profile:{targets:[{name:"TCP",host:"example.com:443"}],currency:"USD",price:5}}];');
  app.document.getElementById('editExistingNodeBtn').events.click();
  const choice = app.document.getElementById('nodeSelectionList').children[0];
  assert.equal(choice.textContent, 'Tokyo · Production');
  choice.events.click();
  assert.equal(app.document.getElementById('editNodeUUID').value, 'node-1');
  assert.equal(app.document.getElementById('editNodeName').value, 'Tokyo');
  assert.equal(app.document.getElementById('editNodeTargets').value, 'TCP,example.com:443');
  assert.equal(app.document.getElementById('editNodePrice').value, 5);
});

test('node cards render empty and populated reports without executing node data', () => {
  const app = dashboard();
  const payload = `<img src=x onerror="alert(1)">'quoted`;
  app.context.fixture = [{
    uuid: 'node-1', name: payload, region: payload, client_ip: payload,
    online: true, basic_info: { arch: payload }, last_report: {
      ping_results: [{ name: payload, host: payload, latency: 25 }]
    }
  }];
  app.run('nodes = fixture; isAdmin = true; renderNodes();');
  const html = app.document.getElementById('nodeGrid').innerHTML;
  assert.ok(html.includes('25 ms'));
  assert.ok(html.includes('&lt;img'));
  assert.ok(!/<img|onclick=|onerror="/i.test(html));
  assert.ok(!html.includes('admin-session'));
  app.run('nodes = [{uuid:"node-2",name:"offline"}]; renderNodes();');
  assert.ok(app.document.getElementById('nodeGrid').innerHTML.includes('offline'));
});

test('ping actions preserve quoted names as data and open the target selector', () => {
  const app = dashboard();
  const target = `O'Reilly "network"`;
  app.context.fixture = [{uuid:'node-1',name:"Node's name",last_report:{ping_results:[{name:target,latency:20}]}}];
  app.run('nodes = fixture; fetchPingHistory = () => {};');
  app.document.getElementById('nodeGrid').events.click({target:{closest:()=>({dataset:{action:'ping',uuid:'node-1',target}})}});
  assert.equal(app.run('currentPingTarget'),target);
  assert.equal(app.document.getElementById('pingTargetSelector').children[0].textContent,target);
});

test('connection instructions fetch credentials through the admin endpoint', async () => {
  const app = dashboard();
  let requested;
  app.context.fetch = async (url, options) => {
    requested = {url, options};
    return {ok:true,json:async()=>({token:'node-secret'})};
  };
  await app.context.showGuide('node-1');
  assert.equal(requested.url,'/api/admin/nodes/node-1/token');
  assert.equal(requested.options.headers.Authorization,'Bearer admin-session');
  assert.equal(app.document.getElementById('guideToken').textContent,'node-secret');
});


test('logout revokes the server session before clearing local state', async () => {
  const app = dashboard();
  let request;
  app.context.fetch = async (url, options) => { request = {url, options}; return {ok:true}; };
  await app.document.getElementById('logoutBtn').events.click();
  assert.equal(request.url, '/api/admin/logout');
  assert.equal(request.options.method, 'POST');
  assert.equal(request.options.headers.Authorization, 'Bearer admin-session');
  assert.equal(app.context.localStorage.removed, 'admin_token');
});

test('failed logout does not pretend that the server session was revoked', async () => {
  const app = dashboard();
  let warning;
  app.context.alert = message => { warning = message; };
  app.context.fetch = async () => ({ok:false});
  await app.document.getElementById('logoutBtn').events.click();
  assert.equal(app.context.localStorage.removed, undefined);
  assert.ok(warning);
});

test('modal styles are top-level rules with balanced blocks', () => {
  const css = fs.readFileSync(path.join(__dirname, 'dist/style.css'), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'/g, '');
  let depth = 0;
  const modalOffset = css.indexOf('.modal-overlay {');
  assert.ok(modalOffset >= 0);
  for (let i = 0; i < css.length; i++) {
    if (i === modalOffset) assert.equal(depth, 0, 'modal rules must not be nested in another component');
    if (css[i] === '{') depth++;
    if (css[i] === '}') depth--;
    assert.ok(depth >= 0, 'unexpected closing brace');
  }
  assert.equal(depth, 0, 'unclosed CSS block');
});


test('billing cycles and missing ping samples are represented honestly', () => {
  const { context } = dashboard();
  assert.match(vm.runInContext("billingDisplay({price:45,currency:'EUR',payment_cycle:'year'}).price",context), /EUR 45.00 \/ 年/);
  assert.match(vm.runInContext("renderPingPanels({uuid:'n',online:true,ping_preview:[{name:'电信',host:'example.com:443',samples:[],loss:null}]})",context), /待采样/);
});
