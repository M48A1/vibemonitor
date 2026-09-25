const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

function dashboard() {
  const elements = new Map();
  function element(id) {
    if (!elements.has(id)) {
      const classes = new Set();
      elements.set(id, { id, value: '', innerHTML: '', textContent: '', style: {}, dataset: {}, events: {},
        classList: { add: c => classes.add(c), remove: c => classes.delete(c), contains: c => classes.has(c) },
        addEventListener(name, callback) { this.events[name] = callback; },
        appendChild() {}, querySelector() { return null; }, focus() {},
      });
    }
    return elements.get(id);
  }
  const root = { dataset: { siteTheme: 'hex', theme: 'light' } };
  const ctx = vm.createContext({ console: { log() {}, warn() {}, error() {} },
    document: { documentElement: root, getElementById: element, createElement: () => element(Symbol()),
      querySelector: () => null, querySelectorAll: () => [], addEventListener() {} },
    fetch: () => new Promise(() => {}), setInterval() {}, setTimeout() {},
    WebSocket: class {}, location: { protocol: 'http:', host: 'localhost' },
    localStorage: { getItem() { throw Error('site theme must not read browser preferences'); } },
  });
  ctx.window = ctx;
  for (const file of ['themes.js', 'app.js']) vm.runInContext(fs.readFileSync(path.join(__dirname, 'dist', file), 'utf8'), ctx);
  return { ctx, element, root, run: code => vm.runInContext(code, ctx) };
}

const nodes = [
  { uuid: 'one', name: 'Tokyo <script>alert(1)</script>', region: 'JP', group: 'production', online: true,
    last_report: { cpu: { usage: 0 }, ram: { used: 0, total: 1024 }, disk: { used: 0, total: 2048 }, network: { up: 0, down: 0 }, uptime: 0 } },
  { uuid: 'two', name: 'London', region: 'GB', online: false,
    last_report: { cpu: { usage: 99 }, network: { up: 123456, down: 456789 } } },
];

test('dashboard renders Hex without consulting browser theme preferences', () => {
  const d = dashboard();
  d.ctx.fixture = nodes;
  d.run('nodes = fixture; renderNodes()');
  assert.match(d.element('nodeGrid').innerHTML, /hex-card/);
  assert.doesNotMatch(d.element('nodeGrid').innerHTML, /node-summary/);
});

test('Hex handles real zero, missing and offline values, escaping node names', () => {
  const d = dashboard();
  d.ctx.fixture = nodes;
  const online = d.run('VibeHex.renderNode(fixture[0])');
  assert.match(online, /0\.0%/);
  assert.match(online, /0 B\/s/);
  assert.match(online, /&lt;script&gt;/);
  assert.doesNotMatch(online, /<script>|production|未分组/);
  const offline = d.run('VibeHex.renderNode(fixture[1])');
  assert.doesNotMatch(offline, /99\.0%|120\.6 KB\/s/);
  assert.match(offline, /离线/);
  d.run('VibeHex.renderOverview(fixture)');
  assert.match(d.element('statsBanner').innerHTML, /0 B\/s/);
  assert.doesNotMatch(d.element('statsBanner').innerHTML, /120\.6/);
});

test('Hex shows all nodes regardless of status or legacy groups', () => {
  const d = dashboard();
  d.ctx.fixture = nodes;
  d.run('nodes = fixture; updateGlobalStats(); renderNodes()');
  const cards = d.element('nodeGrid').innerHTML;
  assert.equal((cards.match(/<article /g) || []).length, 2);
  assert.match(cards, /London/);
  assert.match(cards, /Tokyo/);
  assert.match(d.element('statsBanner').innerHTML, /1 个在线 · 1 个离线/);
});
