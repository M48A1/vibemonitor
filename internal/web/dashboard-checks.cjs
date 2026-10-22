const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const script = ['app.js', 'ping-history.js', 'resource-history.js'].map(name => fs.readFileSync(path.join(__dirname, 'dist', name), 'utf8')).join('\n');
const section = (start, end) => script.slice(script.indexOf(start), script.indexOf(end));

function dashboard() {
  const elements = new Map();
  const requests = [];
  const charts = [];
  const element = id => {
    if (!elements.has(id)) {
      const classes = new Set();
      elements.set(id, {
        id, textContent: '', innerHTML: '', style: {}, parentElement: {style: {}},
        classList: {
          add: name => classes.add(name), remove: name => classes.delete(name),
          contains: name => classes.has(name),
          toggle: (name, enabled) => enabled ? classes.add(name) : classes.delete(name)
        },
        setAttribute() {}, appendChild() {}
      });
    }
    return elements.get(id);
  };
  const context = vm.createContext({
    AbortController, URLSearchParams, Date, nodes: [],
    document: {
      getElementById: element,
      querySelectorAll: () => [], createElement: () => element('button')
    },
    fetch: (url, options) => new Promise((resolve, reject) => requests.push({url, options, resolve, reject})),
    openModal: id => element(id).classList.add('active'),
    resetPasswordFields() {}, resetLoginPasswordFields() {}, resetSettingsPasswordFields() {},
    formatChartTime: value => String(value), formatResourceValue: (_metric, value) => String(value),
    renderPingSvgChart: (...args) => charts.push({type: 'ping', args}),
    renderResourceSvgChart: (...args) => charts.push({type: 'resource', args})
  });
  context.window = context;
  vm.runInContext(section('window.closeModal =', '// Bind modal overlay') +
    section('// --- Ping Fluctuation Chart State', 'function formatChartTime') +
    section('// CPU, memory, disk, and total network rate', 'function formatResourceValue'), context);
  return {context, elements, requests, charts, element, run: code => vm.runInContext(code, context)};
}

const reply = (request, data) => request.resolve({ok: true, json: async () => data});
const pingData = (target, latency) => ({target, method: 'tcp', start_time: 100, end_time: 200,
  stats: {current: latency, avg: latency, min: latency, max: latency, total_count: 1},
  samples: [{t: 100, l: latency}]});

test('late ping response cannot overwrite a new node, target, or range', async () => {
  const d = dashboard();
  const old = d.run("currentPingNodeUUID='old'; currentPingTarget='old-target'; currentPingRange='1h'; loadPingHistory()");
  const latest = d.run("currentPingNodeUUID='new'; currentPingTarget='new & target'; currentPingRange='7d'; loadPingHistory()");
  assert.equal(d.requests[0].options.signal.aborted, true);
  const query = new URL(d.requests[1].url, 'http://localhost').searchParams;
  assert.equal(query.get('uuid'), 'new');
  assert.equal(query.get('target'), 'new & target');
  assert.equal(query.get('range'), '7d');
  reply(d.requests[1], pingData('new & target', 12));
  await latest;
  reply(d.requests[0], pingData('old-target', 99));
  await old;
  assert.equal(d.element('statCurrent').textContent, '12ms');
  assert.match(d.element('pingModalSubtitle').textContent, /new & target/);
  assert.equal(d.charts.length, 1);
  assert.equal(d.charts[0].args[1], '7d');
});

test('late ping failure cannot replace an already loaded chart', async () => {
  const d = dashboard();
  const old = d.run("currentPingNodeUUID='node'; loadPingHistory()");
  const latest = d.run("currentPingRange='24h'; loadPingHistory()");
  reply(d.requests[1], pingData('target', 20));
  await latest;
  d.requests[0].reject(new Error('old request failed'));
  await old;
  assert.equal(d.element('statCurrent').textContent, '20ms');
  assert.equal(d.charts.length, 1);
});

test('unknown ping history shows no fabricated zero latency or loss', async () => {
  const d = dashboard();
  const pending = d.run("currentPingNodeUUID='node'; loadPingHistory()");
  reply(d.requests[0], {stats: {current: -1, avg: 0, min: 0, max: 0, total_count: 0, packet_loss: 0}, samples: []});
  await pending;
  for (const name of ['Current', 'Avg', 'Min', 'Max', 'Loss']) {
    assert.equal(d.element('stat' + name).textContent, '--');
  }
});

test('an observed zero latency remains valid in ping statistics', async () => {
  const d = dashboard();
  const pending = d.run("currentPingNodeUUID='node'; loadPingHistory()");
  reply(d.requests[0], pingData('target', 0));
  await pending;
  for (const name of ['Current', 'Avg', 'Min', 'Max']) {
    assert.equal(d.element('stat' + name).textContent, '0ms');
  }
});

test('closing either chart cancels the request and ignores late responses', async () => {
  for (const [modal, setup, data] of [
    ['pingChartModal', "currentPingNodeUUID='node'; loadPingHistory()", pingData('target', 25)],
    ['resourceChartModal', "currentResourceNodeUUID='node'; loadResourceHistory()",
      {stats: {count: 1, current: 25, avg: 25, min: 25, max: 25}, samples: []}]
  ]) {
    for (const closeAll of [false, true]) {
      const d = dashboard();
      const pending = d.run(setup);
      d.context.closeModal(closeAll ? undefined : modal);
      assert.equal(d.requests[0].options.signal.aborted, true);
      reply(d.requests[0], data);
      await pending;
      assert.equal(d.charts.length, 0);
    }
  }
});

test('resource chart ignores responses from a previous metric or range', async () => {
  const d = dashboard();
  const old = d.run("currentResourceNodeUUID='node'; loadResourceHistory()");
  const latest = d.run("currentResourceMetric='network'; currentResourceRange='31d'; loadResourceHistory()");
  reply(d.requests[1], {stats: {count: 1, current: 42, avg: 42, min: 42, max: 42}, samples: [], start_time: 100, end_time: 200});
  await latest;
  reply(d.requests[0], {stats: {count: 1, current: 99}, samples: []});
  await old;
  assert.equal(d.element('resourceStatCurrent').textContent, '42');
  assert.equal(d.charts.length, 1);
  assert.equal(d.charts[0].args[4], 'network');
});

test('reopening ping history clears the previous all-range selection', () => {
  const d = dashboard();
  d.element('btnRangeAll').classList.add('active');
  d.context.openPingChart('node', 'Node', 'target');
  assert.equal(d.element('btnRangeAll').classList.contains('active'), false);
  assert.equal(d.element('btnRange1h').classList.contains('active'), true);
  d.context.closeModal('pingChartModal');
  reply(d.requests[0], pingData('target', 1));
});

test('ServerStatus switches layout before its fixed table columns can be clipped', () => {
  const css = fs.readFileSync(path.join(__dirname, 'dist/serverstatus.css'), 'utf8');
  const columns = Array.from(css.matchAll(/--ss-cols:\s*([^;]+);/g), match => match[1]);
  const breakpoints = Array.from(css.matchAll(/@media \(max-width:\s*(\d+)px\)/g), match => Number(match[1]));
  const outerPadding = Number(css.match(/\.container\s*\{[^}]*padding:\s*\d+px\s+(\d+)px/)[1]) * 2;
  const gap = Number(css.match(/\.ss-row\s*\{[^}]*column-gap:\s*(\d+)px/)[1]);
  const rowPadding = Number(css.match(/\.ss-node \.ss-row\s*\{[^}]*padding:\s*\d+px\s+(\d+)px/)[1]) * 2;
  const tableWidth = definition => {
    const widths = Array.from(definition.matchAll(/(\d+)px/g), match => Number(match[1]));
    return widths.reduce((sum, width) => sum + width, 0) + (widths.length - 1) * gap + rowPadding + outerPadding + 2;
  };
  assert.ok(breakpoints[0] + 1 >= tableWidth(columns[0]), 'desktop columns would be clipped before switching');
  assert.ok(breakpoints[1] + 1 >= tableWidth(columns[1]), 'tablet columns would be clipped before switching');
  assert.ok(760 <= breakpoints[1], '760px must use the compact two-row layout');
  assert.ok(1260 <= breakpoints[0], '1260px must use the reduced-column table');
});

test('starting either history request clears the old graph, tooltip, and time axis', async () => {
  for (const [setup,svg,tooltip,start,end,data] of [
    ["currentPingNodeUUID='node'; loadPingHistory()",'pingChartSvg','chartTooltip','chartTimeStart','chartTimeEnd',pingData('target',12)],
    ["currentResourceNodeUUID='node'; loadResourceHistory()",'resourceChartSvg','resourceChartTooltip','resourceChartTimeStart','resourceChartTimeEnd',{stats:{count:0},samples:[]}]
  ]) {
    const d=dashboard();
    d.element(svg).innerHTML='<path old-data />';d.element(tooltip).style.display='block';
    d.element(start).textContent=d.element(end).textContent='old-time';
    const pending=d.run(setup);
    assert.match(d.element(svg).innerHTML,/正在加载/);
    assert.doesNotMatch(d.element(svg).innerHTML,/old-data/);
    assert.equal(d.element(tooltip).style.display,'none');
    assert.equal(d.element(start).textContent,'--');assert.equal(d.element(end).textContent,'--');
    reply(d.requests[0],data);await pending;
  }
});

function realPingRenderer() {
  const controls=new Map();
  const control=id=>{if(!controls.has(id))controls.set(id,{style:{},setAttribute(){},addEventListener(name,handler){this[name]=handler;}});return controls.get(id);};
  const svg={innerHTML:'',querySelector:control,getBoundingClientRect:()=>({left:0,width:700})};
  const tooltip={style:{display:'block'}};
  const context=vm.createContext({document:{getElementById:id=>id==='pingChartSvg'?svg:tooltip},formatTooltipTime:value=>String(value)});
  vm.runInContext(section('function renderPingSvgChart(', '// CPU, memory, disk, and total network rate'),context);
  return {svg,tooltip,controls,render:(samples,gaps=[])=>context.renderPingSvgChart(samples,'1h',null,100,1000,gaps)};
}

test('real ping SVG splits the line and area across an offline interval', () => {
  const d=realPingRenderer();
  d.render([{t:100,l:10},{t:200,l:20},{t:800,l:30},{t:900,l:40}],[{start:260,end:800}]);
  const paths=Array.from(d.svg.innerHTML.matchAll(/<path d="([^"]+)"/g),match=>match[1]);
  assert.equal(paths.length,2);
  assert.equal((paths[1].match(/ M /g)||[]).length,2,'line must start a fresh segment after the outage');
  assert.equal((paths[0].match(/ Z/g)||[]).length,2,'each fill segment must close before the outage');
  d.controls.get('#chartHitBox').mousemove({clientX:48+(500-100)/900*632});
  assert.match(d.tooltip.innerHTML,/探针掉线/);
  assert.equal(d.tooltip.style.display,'block');
});

test('real ping SVG displays a single valid sample and removes an old tooltip', () => {
  const d=realPingRenderer();d.render([{t:200,l:0}]);
  assert.match(d.svg.innerHTML,/class="ping-sample-point"/);
  assert.equal(d.tooltip.style.display,'none');
  d.controls.get('#chartHitBox').mousemove({clientX:48+100/900*632});
  assert.match(d.tooltip.innerHTML,/0 ms/);
});
