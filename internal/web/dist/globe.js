// A small canvas globe inspired by the Emerald monitor theme. All map data is served locally.
window.VibeGlobe = (() => {
  const canvas = document.getElementById('overviewGlobe');
  const context = canvas && canvas.getContext ? canvas.getContext('2d') : null;
  if (!context) return { setNodes() {} };

  const radians = Math.PI / 180;
  const reducedMotion = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)');
  function cartesian(latitude, longitude) {
    const lat = latitude * radians;
    const lon = longitude * radians;
    const cosLat = Math.cos(lat);
    return [cosLat * Math.sin(lon), Math.sin(lat), cosLat * Math.cos(lon)];
  }
  const gridCurves = [];
  for (let latitude = -60; latitude <= 60; latitude += 30) {
    const curve = [];
    for (let longitude = -180; longitude <= 180; longitude += 3) curve.push(cartesian(latitude, longitude));
    gridCurves.push(curve);
  }
  for (let longitude = -180; longitude < 180; longitude += 30) {
    const curve = [];
    for (let latitude = -90; latitude <= 90; latitude += 3) curve.push(cartesian(latitude, longitude));
    gridCurves.push(curve);
  }
  let landPoints = [];
  let regionCoordinates = {};
  let nodes = [];
  let routes = [];
  let routeSignature = '';
  const shanghai = cartesian(31.2304, 121.4737);
  let centerLongitude = 105;
  let centerLatitude = 18;
  let width = 0;
  let height = 0;
  let visible = true;
  let dragging = false;
  let lastPointerX = 0;
  let lastPointerY = 0;
  let animationFrame = 0;
  let lastFrameTime = 0;

  function project(point, centerX, centerY, radius, rotation) {
    const facing = point[2] * rotation.cosLon + point[0] * rotation.sinLon;
    const horizontal = point[0] * rotation.cosLon - point[2] * rotation.sinLon;
    const depth = point[1] * rotation.sinTilt + facing * rotation.cosTilt;
    return {
      x: centerX + radius * horizontal,
      y: centerY - radius * (point[1] * rotation.cosTilt - facing * rotation.sinTilt),
      depth,
    };
  }

  function regionCode(value) {
    if (typeof value !== 'string') return null;
    const code = value.trim().toUpperCase();
    const match = /^([A-Z]{2})(?:$|[\s,/-])/.exec(code);
    if (match) return match[1];
    const flag = Array.from(value.trim());
    if (flag.length === 2) {
      const first = flag[0].codePointAt(0) - 0x1f1e6;
      const second = flag[1].codePointAt(0) - 0x1f1e6;
      if (first >= 0 && first < 26 && second >= 0 && second < 26) {
        return String.fromCharCode(65 + first, 65 + second);
      }
    }
    return null;
  }

  function updateMarkers() {
    const byRegion = new Map();
    for (const node of nodes) {
      const code = regionCode(node.region);
      const position = code && regionCoordinates[code];
      if (!position) continue;
      if (!byRegion.has(code)) byRegion.set(code, []);
      byRegion.get(code).push(node);
    }
    const signature = Array.from(byRegion, ([code, regionNodes]) =>
      `${code}:${regionNodes.map(node => `${node.uuid}:${node.online}`).sort().join(',')}`).sort().join('|');
    if (signature === routeSignature) return;
    routeSignature = signature;
    routes = [];
    for (const [code, regionNodes] of byRegion) {
      const position = regionCoordinates[code];
      regionNodes.sort((a, b) => String(a.uuid).localeCompare(String(b.uuid)));
      for (let index = 0; index < regionNodes.length; index++) {
        const spread = index === 0 ? 0 : 2.5 + Math.sqrt(index) * 1.4;
        const angle = index * 2.39996;
        const latitude = Math.max(-80, Math.min(80, position[0] + Math.sin(angle) * spread));
        const longitude = position[1] + Math.cos(angle) * spread;
        const point = cartesian(latitude, longitude);
        routes.push({ point, online: regionNodes[index].online, path: greatCircle(shanghai, point) });
      }
    }
    if (!shouldAnimate()) draw();
  }

  function greatCircle(from, to) {
    const angle = Math.acos(Math.max(-1, Math.min(1, from[0] * to[0] + from[1] * to[1] + from[2] * to[2])));
    const sine = Math.sin(angle);
    const points = [];
    for (let index = 0; index <= 28; index++) {
      const t = index / 28;
      const a = sine > 0.001 ? Math.sin((1 - t) * angle) / sine : 1 - t;
      const b = sine > 0.001 ? Math.sin(t * angle) / sine : t;
      points.push([from[0] * a + to[0] * b, from[1] * a + to[1] * b, from[2] * a + to[2] * b]);
    }
    return points;
  }

  function measure() {
    const bounds = canvas.getBoundingClientRect();
    const nextWidth = Math.max(0, Math.round(bounds.width));
    const nextHeight = Math.max(0, Math.round(bounds.height));
    if (!nextWidth || !nextHeight) return false;
    const ratio = Math.min(window.devicePixelRatio || 1, 2);
    if (width !== nextWidth || height !== nextHeight || canvas.width !== Math.round(nextWidth * ratio)) {
      width = nextWidth;
      height = nextHeight;
      canvas.width = Math.round(width * ratio);
      canvas.height = Math.round(height * ratio);
      context.setTransform(ratio, 0, 0, ratio, 0, 0);
    }
    return true;
  }

  function drawCurve(points, centerX, centerY, radius, rotation) {
    context.beginPath();
    let drawing = false;
    for (const coordinates of points) {
      const point = project(coordinates, centerX, centerY, radius, rotation);
      if (point.depth <= 0) {
        drawing = false;
        continue;
      }
      if (drawing) context.lineTo(point.x, point.y);
      else context.moveTo(point.x, point.y);
      drawing = true;
    }
    context.stroke();
  }

  function draw() {
    if (!measure()) return;
    context.clearRect(0, 0, width, height);
    const radius = Math.min(width * 0.38, height * 0.47, 190);
    const centerX = width * 0.52;
    const centerY = height * 0.5;
    const longitude = centerLongitude * radians;
    const tilt = centerLatitude * radians;
    const rotation = { sinLon: Math.sin(longitude), cosLon: Math.cos(longitude), sinTilt: Math.sin(tilt), cosTilt: Math.cos(tilt) };

    const glow = context.createRadialGradient(centerX, centerY, radius * 0.6, centerX, centerY, radius * 1.42);
    glow.addColorStop(0, 'rgba(33, 113, 210, 0.22)');
    glow.addColorStop(1, 'rgba(33, 113, 210, 0)');
    context.fillStyle = glow;
    context.beginPath();
    context.arc(centerX, centerY, radius * 1.42, 0, Math.PI * 2);
    context.fill();

    const sea = context.createRadialGradient(centerX - radius * 0.35, centerY - radius * 0.4, radius * 0.12, centerX, centerY, radius);
    sea.addColorStop(0, '#74cbf6');
    sea.addColorStop(0.58, '#328fe0');
    sea.addColorStop(1, '#1858ba');
    context.fillStyle = sea;
    context.beginPath();
    context.arc(centerX, centerY, radius, 0, Math.PI * 2);
    context.fill();

    context.save();
    context.beginPath();
    context.arc(centerX, centerY, radius, 0, Math.PI * 2);
    context.clip();
    context.strokeStyle = 'rgba(255, 255, 255, 0.15)';
    context.lineWidth = 0.7;
    for (const curve of gridCurves) drawCurve(curve, centerX, centerY, radius, rotation);

    const landBuckets = [[], [], [], []];
    for (const coordinates of landPoints) {
      const point = project(coordinates, centerX, centerY, radius, rotation);
      if (point.depth <= 0) continue;
      landBuckets[Math.min(3, Math.floor(point.depth * 4))].push(point);
    }
    const dotRadius = Math.max(1, radius / 70);
    for (let bucket = 0; bucket < landBuckets.length; bucket++) {
      context.fillStyle = `rgba(255, 207, 69, ${0.7 + bucket * 0.1})`;
      context.beginPath();
      for (const point of landBuckets[bucket]) {
        context.moveTo(point.x + dotRadius, point.y);
        context.arc(point.x, point.y, dotRadius, 0, Math.PI * 2);
      }
      context.fill();
    }

    context.beginPath();
    for (const route of routes) {
      if (!route.online) continue;
      let drawing = false;
      for (let index = 0; index < route.path.length; index++) {
        const point = project(route.path[index], centerX, centerY, radius, rotation);
        if (point.depth <= 0) {
          drawing = false;
          continue;
        }
        const lift = 1 + 0.08 * Math.sin(Math.PI * index / (route.path.length - 1));
        const x = centerX + (point.x - centerX) * lift;
        const y = centerY + (point.y - centerY) * lift;
        if (drawing) context.lineTo(x, y);
        else context.moveTo(x, y);
        drawing = true;
      }
    }
    context.strokeStyle = 'rgba(224, 48, 68, 0.78)';
    context.lineWidth = 1.6;
    context.stroke();

    for (const route of routes) {
      const point = project(route.point, centerX, centerY, radius, rotation);
      if (point.depth <= 0) continue;
      const color = route.online ? '#e03044' : '#8791a1';
      const size = 4.2;
      context.fillStyle = route.online ? 'rgba(224, 48, 68, 0.23)' : 'rgba(135, 145, 161, 0.18)';
      context.beginPath();
      context.arc(point.x, point.y, size + 4, 0, Math.PI * 2);
      context.fill();
      context.fillStyle = color;
      context.strokeStyle = '#ffffff';
      context.lineWidth = 1.8;
      context.beginPath();
      context.arc(point.x, point.y, size, 0, Math.PI * 2);
      context.fill();
      context.stroke();
    }
    if (routes.length) {
      const origin = project(shanghai, centerX, centerY, radius, rotation);
      if (origin.depth > 0) {
        context.fillStyle = 'rgba(224, 48, 68, 0.26)';
        context.beginPath();
        context.arc(origin.x, origin.y, 10, 0, Math.PI * 2);
        context.fill();
        context.fillStyle = '#e03044';
        context.strokeStyle = '#ffffff';
        context.lineWidth = 2;
        context.beginPath();
        context.arc(origin.x, origin.y, 5, 0, Math.PI * 2);
        context.fill();
        context.stroke();
      }
    }
    context.restore();

    context.strokeStyle = 'rgba(20, 83, 170, 0.38)';
    context.lineWidth = 1;
    context.beginPath();
    context.arc(centerX, centerY, radius, 0, Math.PI * 2);
    context.stroke();
  }

  function shouldAnimate() {
    return visible && document.visibilityState !== 'hidden' && !(reducedMotion && reducedMotion.matches);
  }

  function frame(time) {
    animationFrame = 0;
    if (!shouldAnimate()) return;
    if (time - lastFrameTime >= 66) {
      if (!dragging) centerLongitude = (centerLongitude + 0.14) % 360;
      draw();
      lastFrameTime = time;
    }
    animationFrame = window.requestAnimationFrame(frame);
  }

  function syncAnimation() {
    if (animationFrame) window.cancelAnimationFrame(animationFrame);
    animationFrame = 0;
    draw();
    if (shouldAnimate()) animationFrame = window.requestAnimationFrame(frame);
  }

  canvas.addEventListener('pointerdown', event => {
    dragging = true;
    lastPointerX = event.clientX;
    lastPointerY = event.clientY;
    canvas.setPointerCapture(event.pointerId);
  });
  canvas.addEventListener('pointermove', event => {
    if (!dragging) return;
    centerLongitude -= (event.clientX - lastPointerX) * 0.45;
    centerLatitude = Math.max(-65, Math.min(65, centerLatitude + (event.clientY - lastPointerY) * 0.35));
    lastPointerX = event.clientX;
    lastPointerY = event.clientY;
    draw();
  });
  function stopDragging() { dragging = false; }
  canvas.addEventListener('pointerup', stopDragging);
  canvas.addEventListener('pointercancel', stopDragging);
  canvas.addEventListener('lostpointercapture', stopDragging);

  if (window.ResizeObserver) new ResizeObserver(draw).observe(canvas);
  else window.addEventListener('resize', draw);
  if (window.IntersectionObserver) {
    new IntersectionObserver(entries => {
      visible = entries[0].isIntersecting;
      syncAnimation();
    }).observe(canvas);
  }
  document.addEventListener('visibilitychange', syncAnimation);
  if (reducedMotion) reducedMotion.addEventListener('change', syncAnimation);

  fetch('/globe-points.json').then(response => {
    if (!response.ok) throw new Error('globe points unavailable');
    return response.json();
  }).then(points => { landPoints = points.map(([latitude, longitude]) => cartesian(latitude, longitude)); draw(); }).catch(() => {});
  fetch('/globe-regions.json').then(response => {
    if (!response.ok) throw new Error('globe regions unavailable');
    return response.json();
  }).then(coordinates => { regionCoordinates = coordinates; routeSignature = ''; updateMarkers(); }).catch(() => {});

  syncAnimation();
  return {
    setNodes(nextNodes) {
      nodes = Array.isArray(nextNodes) ? nextNodes : [];
      updateMarkers();
    },
  };
})();
