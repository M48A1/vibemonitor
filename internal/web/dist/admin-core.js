// Shared plumbing for the management pages: branding, theme and admin session.
// Every page loads this file before its own script and calls VibeAdmin.ready().
(() => {
  const DEFAULT_ICON = (document.getElementById('siteIcon') || { getAttribute: () => '' }).getAttribute('href') || '';
  let statusTimer = null;

  function escapeHtml(str) {
    if (!str) return '';
    return String(str).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }

  function formatBytes(bytes) {
    if (!bytes || bytes <= 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return (bytes / Math.pow(1024, i)).toFixed(1) + ' ' + units[i];
  }

  function formatSpeed(bytesPerSec) { return formatBytes(bytesPerSec) + '/s'; }

  function formatUptime(seconds) {
    if (!seconds || seconds <= 0) return '0分';
    const d = Math.floor(seconds / 86400);
    const h = Math.floor((seconds % 86400) / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    if (d > 0) return `${d}天 ${h}时`;
    if (h > 0) return `${h}时 ${m}分`;
    return `${m}分`;
  }

  // A single toast keeps feedback identical across the four pages.
  function setStatus(text, kind) {
    const el = document.getElementById('adminStatus');
    if (!el) return;
    if (statusTimer) { clearTimeout(statusTimer); statusTimer = null; }
    if (!text) { el.hidden = true; el.textContent = ''; return; }
    el.textContent = text;
    el.dataset.kind = kind || 'info';
    el.hidden = false;
    if (kind !== 'error') statusTimer = setTimeout(() => setStatus(''), 4000);
  }

  async function request(url, options = {}) {
    const response = await fetch(url, { credentials: 'same-origin', cache: 'no-store', ...options });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.error || ('请求失败（HTTP ' + response.status + '）'));
    return data;
  }

  async function copyText(text, label) {
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setStatus((label || '内容') + '已复制', 'success');
    } catch (error) {
      setStatus('复制失败，请手动选择文本复制', 'error');
    }
  }

  // --- theme & branding -------------------------------------------------
  // Keep the mobile browser chrome and the data-theme hook in step with the
  // active theme, which third-party theme CSS may branch on.
  function syncChrome() {
    const color = getComputedStyle(document.body).backgroundColor || '';
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta && color && color !== 'rgba(0, 0, 0, 0)' && color !== 'transparent') meta.setAttribute('content', color);
    const match = /rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/.exec(color);
    if (!match) return;
    const linear = value => { const c = Number(value) / 255; return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
    const luminance = 0.2126 * linear(match[1]) + 0.7152 * linear(match[2]) + 0.0722 * linear(match[3]);
    document.documentElement.dataset.theme = luminance > 0.4 ? 'light' : 'dark';
  }

  function applyTheme(id) {
    const custom = /^custom-[a-f0-9]{64}$/.test(id || '');
    const href = custom ? '/api/themes/' + id + '/style.css' : '/design.css';
    const link = document.getElementById('customThemeStylesheet');
    if (!link) return;
    if (link.getAttribute('href') !== href) link.setAttribute('href', href);
    if (link.sheet) syncChrome(); else link.addEventListener('load', syncChrome, { once: true });
    const layout = getComputedStyle(document.documentElement).getPropertyValue('--vibemonitor-layout').trim() === 'serverstatus' ? 'serverstatus' : 'cards';
    document.documentElement.dataset.siteTheme = custom ? id : 'design';
    document.documentElement.dataset.siteLayout = layout;
  }

  function updateIcon(url) {
    const href = url || DEFAULT_ICON;
    const link = document.getElementById('siteIcon');
    const img = document.getElementById('adminBrandIcon');
    if (link) link.setAttribute('href', href);
    if (img) img.setAttribute('src', href);
  }

  async function loadBranding() {
    try {
      const data = await request('/api/public');
      const title = (data.site_title || '').trim() || 'VibeMonitor';
      const name = document.getElementById('adminBrandTitle');
      if (name) name.textContent = title;
      const page = document.body.dataset.pageTitle;
      document.title = page ? title + ' · ' + page + ' · 管理后台' : title + ' · 管理后台';
      updateIcon(data.site_icon || '');
      applyTheme(data.site_theme || 'design');
    } catch (error) {
      console.error('Unable to load site settings:', error);
    }
  }

  // --- session ----------------------------------------------------------
  function wirePasswordToggles() {
    document.addEventListener('click', (event) => {
      const btn = event.target.closest('.btn-toggle-pwd');
      if (!btn) return;
      const input = document.getElementById(btn.dataset.target || '');
      if (!input) return;
      const visible = input.type === 'password';
      input.type = visible ? 'text' : 'password';
      btn.textContent = visible ? '🙈' : '👁️';
      btn.title = visible ? '隐藏密码' : '显示密码';
      btn.setAttribute('aria-label', btn.title);
      btn.setAttribute('aria-pressed', visible ? 'true' : 'false');
    });
  }

  function wireLogout() {
    const btn = document.getElementById('adminLogoutBtn');
    if (!btn) return;
    btn.addEventListener('click', async () => {
      try { await request('/api/admin/logout', { method: 'POST' }); } catch (error) { /* already signed out */ }
      location.href = '/';
    });
  }

  function markNav() {
    const here = location.pathname.replace(/\/+$/, '');
    document.querySelectorAll('.admin-nav-link[data-admin-nav]').forEach(link => {
      const target = new URL(link.href, location.origin).pathname.replace(/\/+$/, '');
      if (target === here) link.setAttribute('aria-current', 'page');
    });
  }

  /**
   * Boots a management page: wires the shared chrome, verifies the admin
   * session and only then runs the page's own initialiser.
   *
   * The server already redirects visitors without a session, so a negative
   * answer here means the session expired while the page was open. The login
   * dialog lives on the dashboard, so the page sends the visitor back there
   * rather than growing a second copy of the form.
   */
  async function ready(init) {
    markNav();
    wireLogout();
    wirePasswordToggles();
    const scheme = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)');
    if (scheme && scheme.addEventListener) scheme.addEventListener('change', syncChrome);
    void loadBranding();

    const content = document.getElementById('adminContent');
    const loading = document.getElementById('adminLoading');
    const logout = document.getElementById('adminLogoutBtn');
    let admin = null;
    try {
      admin = (await request('/api/admin/status')).is_admin === true;
    } catch (error) {
      setStatus('无法验证登录状态：' + error.message, 'error');
    }
    if (loading) loading.hidden = true;
    if (admin === false) {
      location.replace('/');
      return;
    }
    // A failed status request leaves the page empty on purpose: the data calls
    // below would only produce the same error again.
    if (admin !== true) return;
    if (content) content.hidden = false;
    if (logout) logout.hidden = false;
    try {
      await init();
    } catch (error) {
      setStatus(error.message, 'error');
    }
  }

  window.VibeAdmin = {
    ready,
    request,
    status: setStatus,
    copyText,
    escapeHtml,
    formatBytes,
    formatSpeed,
    formatUptime,
    updateIcon,
    applyTheme,
    syncChrome
  };
})();
