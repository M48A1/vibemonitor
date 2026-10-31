// Applies the theme the panel selected in /admin/theme. The management page owns
// the upload/list/enable flows; the dashboard only needs to swap the stylesheet.
(() => {
  // ?theme=default is the recovery escape hatch when a broken theme hides the UI.
  const recovery = new URLSearchParams(location.search).get('theme') === 'default';
  // Keep the mobile browser chrome in step with the active theme background.
  function syncThemeColor() {
    const meta = document.querySelector('meta[name="theme-color"]');
    if (!meta) return;
    const color = getComputedStyle(document.body).backgroundColor;
    if (color && color !== 'rgba(0, 0, 0, 0)' && color !== 'transparent') meta.setAttribute('content', color);
  }
  // Keep the data-theme hook truthful so third-party theme CSS can branch on it.
  function syncThemeMode() {
    const match = /rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/.exec(getComputedStyle(document.body).backgroundColor || '');
    if (!match) return;
    const linear = (value) => { const c = Number(value) / 255; return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
    const luminance = 0.2126 * linear(match[1]) + 0.7152 * linear(match[2]) + 0.0722 * linear(match[3]);
    document.documentElement.dataset.theme = luminance > 0.4 ? 'light' : 'dark';
  }
  function syncChrome() { syncThemeColor(); syncThemeMode(); }
  // A theme may follow prefers-color-scheme, so re-read the colors when it flips.
  const colorSchemeQuery = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)');
  if (colorSchemeQuery && colorSchemeQuery.addEventListener) colorSchemeQuery.addEventListener('change', syncChrome);
  function setEffectiveTheme(id, layout = 'cards') {
    const root = document.documentElement;
    if (root.dataset.siteTheme === id && root.dataset.siteLayout === layout) return;
    root.dataset.siteTheme = id;
    root.dataset.siteLayout = layout;
    if (typeof window.scheduleDashboard === 'function') window.scheduleDashboard();
  }
  function apply(id) {
    const custom = !recovery && /^custom-[a-f0-9]{64}$/.test(id);
    const effective = custom ? id : 'design';
    const href = custom ? '/api/themes/' + id + '/style.css' : '/design.css';
    const current = document.getElementById('customThemeStylesheet');
    const activateStyle = () => {
      const layout = custom && getComputedStyle(document.documentElement).getPropertyValue('--vibemonitor-layout').trim() === 'serverstatus' ? 'serverstatus' : 'cards';
      setEffectiveTheme(effective, layout);
      syncChrome();
    };
    if (current && (current.getAttribute('href') || '').split('?')[0] === href) {
      if (current.sheet) activateStyle();
      return;
    }
    const link = document.createElement('link');
    link.id = 'customThemeStylesheet'; link.rel = 'stylesheet'; link.href = href;
    link.onload = () => {
      if (document.getElementById('customThemeStylesheet') === link) activateStyle();
    };
    link.onerror = () => {
      if (document.getElementById('customThemeStylesheet') !== link) return;
      link.remove();
      setEffectiveTheme('design');
      if (custom) apply('design');
      console.warn('主题加载失败，已显示默认外观；可在 /admin/theme 重新启用主题');
    };
    if (current) current.remove();
    document.head.appendChild(link);
    if (!custom) setEffectiveTheme('design');
  }
  window.VibeThemes = { apply };
})();
