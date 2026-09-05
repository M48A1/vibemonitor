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
    document, localStorage: { getItem: () => 'admin-session' }, console,
    alert(message) { throw new Error(message); },
  });
  context.window = context;
  context.location = { origin: 'https://monitor.example' };
  const source = fs.readFileSync(path.join(__dirname, 'dist/app.js'), 'utf8');
  // Test rendering and actions without starting network connections or timers.
  vm.runInContext(source.split('// Initialize')[0], context);
  return { context, document, run: code => vm.runInContext(code, context) };
}

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
  assert.ok(html.includes('25ms'));
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
