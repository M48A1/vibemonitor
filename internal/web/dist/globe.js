// A small canvas globe inspired by the Emerald monitor theme. All map data is served locally.
window.VibeGlobe = (() => {
  const canvas = document.getElementById('overviewGlobe');
  const context = canvas && canvas.getContext ? canvas.getContext('2d') : null;
  if (!context) return { setNodes() {} };

  const radians = Math.PI / 180;
  const reducedMotion = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)');
  let landPoints = [];
  let regionCoordinates = {};
  let nodes = [];
  let markers = [];
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

  function project(latitude, longitude, centerX, centerY, radius) {
    const lat = latitude * radians;
    const delta = (longitude - centerLongitude) * radians;
    const tilt = centerLatitude * radians;
    const cosLat = Math.cos(lat);
    const depth = Math.sin(lat) * Math.sin(tilt) + cosLat * Math.cos(delta) * Math.cos(tilt);
    return {
      x: centerX + radius * cosLat * Math.sin(delta),
      y: centerY - radius * (Math.sin(lat) * Math.cos(tilt) - cosLat * Math.cos(delta) * Math.sin(tilt)),
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
      const marker = byRegion.get(code) || { latitude: position[0], longitude: position[1], online: 0, offline: 0 };
      if (node.online) marker.online += 1;
      else marker.offline += 1;
      byRegion.set(code, marker);
    }
    markers = Array.from(byRegion.values());
    draw();
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

  function drawCurve(points, centerX, centerY, radius) {
    context.beginPath();
    let drawing = false;
    for (const [latitude, longitude] of points) {
      const point = project(latitude, longitude, centerX, centerY, radius);
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
    const radius = Math.min(width * 0.29, height * 0.46, 162);
    const centerX = width * 0.53;
    const centerY = height * 0.52;

    const glow = context.createRadialGradient(centerX, centerY, radius * 0.6, centerX, centerY, radius * 1.42);
    glow.addColorStop(0, 'rgba(53, 109, 204, 0.10)');
    glow.addColorStop(1, 'rgba(53, 109, 204, 0)');
    context.fillStyle = glow;
    context.beginPath();
    context.arc(centerX, centerY, radius * 1.42, 0, Math.PI * 2);
    context.fill();

    const sea = context.createRadialGradient(centerX - radius * 0.35, centerY - radius * 0.4, radius * 0.12, centerX, centerY, radius);
    sea.addColorStop(0, '#ffffff');
    sea.addColorStop(0.65, '#edf5fc');
    sea.addColorStop(1, '#d4e5f3');
    context.fillStyle = sea;
    context.beginPath();
    context.arc(centerX, centerY, radius, 0, Math.PI * 2);
    context.fill();

    context.save();
    context.beginPath();
    context.arc(centerX, centerY, radius, 0, Math.PI * 2);
    context.clip();
    context.strokeStyle = 'rgba(78, 127, 170, 0.16)';
    context.lineWidth = 0.7;
    for (let latitude = -60; latitude <= 60; latitude += 30) {
      const points = [];
      for (let longitude = -180; longitude <= 180; longitude += 3) points.push([latitude, longitude]);
      drawCurve(points, centerX, centerY, radius);
    }
    for (let longitude = -180; longitude < 180; longitude += 30) {
      const points = [];
      for (let latitude = -90; latitude <= 90; latitude += 3) points.push([latitude, longitude]);
      drawCurve(points, centerX, centerY, radius);
    }

    context.fillStyle = '#5b83a5';
    for (const [latitude, longitude] of landPoints) {
      const point = project(latitude, longitude, centerX, centerY, radius);
      if (point.depth <= 0) continue;
      context.globalAlpha = 0.28 + point.depth * 0.55;
      context.beginPath();
      context.arc(point.x, point.y, Math.max(0.7, radius / 105), 0, Math.PI * 2);
      context.fill();
    }
    context.globalAlpha = 1;

    for (const marker of markers) {
      const point = project(marker.latitude, marker.longitude, centerX, centerY, radius);
      if (point.depth <= 0) continue;
      const color = marker.online > 0 ? '#238364' : '#c59634';
      const size = Math.min(7, 3.7 + Math.sqrt(marker.online + marker.offline));
      context.fillStyle = marker.online > 0 ? 'rgba(35, 131, 100, 0.20)' : 'rgba(197, 150, 52, 0.22)';
      context.beginPath();
      context.arc(point.x, point.y, size + 5, 0, Math.PI * 2);
      context.fill();
      context.fillStyle = color;
      context.strokeStyle = '#ffffff';
      context.lineWidth = 2;
      context.beginPath();
      context.arc(point.x, point.y, size, 0, Math.PI * 2);
      context.fill();
      context.stroke();
    }
    context.restore();

    context.strokeStyle = 'rgba(72, 119, 169, 0.28)';
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
    if (time - lastFrameTime >= 40) {
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
  }).then(points => { landPoints = points; draw(); }).catch(() => {});
  fetch('/globe-regions.json').then(response => {
    if (!response.ok) throw new Error('globe regions unavailable');
    return response.json();
  }).then(coordinates => { regionCoordinates = coordinates; updateMarkers(); }).catch(() => {});

  syncAnimation();
  return {
    setNodes(nextNodes) {
      nodes = Array.isArray(nextNodes) ? nextNodes : [];
      updateMarkers();
    },
  };
})();
