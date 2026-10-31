// Admin Authentication
async function checkAdminAuth() {
  try {
    const res = await fetch('/api/admin/status', { credentials: 'same-origin' });
    const data = await res.json();
    setAdminState(data.is_admin === true);
  } catch (e) {
    setAdminState(false);
  }
}

function setAdminState(admin) {
  const changed = isAdmin !== admin;
  isAdmin = admin;
  const quickActions = document.getElementById('adminQuickActions');
  if (quickActions) quickActions.hidden = !admin;
  if (!admin) closeSiteMenu();
  if (changed && nodes.length) renderNodes();
}

// Login errors live in a role="alert" node toggled through the hidden attribute,
// so no inline style is needed and screen readers announce the text.
function showLoginError(message) {
  const error = document.getElementById('loginError');
  if (!error) return;
  if (typeof message === 'string') error.textContent = message;
  error.hidden = false;
}

function hideLoginError() {
  const error = document.getElementById('loginError');
  if (error) error.hidden = true;
}

// Event Listeners
function requireAdmin(action) {
  if (isAdmin) {
    action();
    return;
  }
  pendingAdminAction = action;
  hideLoginError();
  const pwInput = document.getElementById('loginPassword');
  pwInput.value = '';
  pwInput.type = 'password';
  pwInput.autocomplete = 'current-password';
  const pwToggleBtn = document.querySelector('.btn-toggle-pwd[data-target="loginPassword"]');
  setPasswordVisibility(pwToggleBtn, pwInput, false);
  const unInput = document.getElementById('loginUsername');
  if (unInput) unInput.autocomplete = 'username';
  openModal('loginModal');
}

// Management pages are served to a signed-in admin only, so a menu click asks
// for the password first and then follows the link. The dashboard dialog is the
// single place a session can be started.
document.addEventListener('click', (event) => {
  // Leave middle-clicks and "open in new tab" to the browser: that tab gets the
  // same bounce, and stealing the gesture would look broken.
  if (event.defaultPrevented || event.button > 0 || event.metaKey || event.ctrlKey ||
      event.shiftKey || event.altKey) return;
  const target = event.target;
  const link = target && target.closest ? target.closest('.site-menu-panel a[href^="/admin"]') : null;
  if (!link || isAdmin) return;
  event.preventDefault();
  const href = link.getAttribute('href');
  closeSiteMenu();
  requireAdmin(() => { location.href = href; });
});

document.getElementById('loginForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const password = document.getElementById('loginPassword').value;
  try {
    const res = await fetch('/api/admin/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: document.getElementById('loginUsername').value, password })
    });
    const data = await res.json();
    if (data.token) {
      setAdminState(true);
      const action = pendingAdminAction;
      closeModal('loginModal');
      if (action) action();
    } else {
      showLoginError('账号或密码错误');
    }
  } catch (e) {
    showLoginError('连接失败: ' + e.message);
  }
});

async function handleLogout() {
  try {
    const res = await fetch('/api/admin/logout', {
      method: 'POST', credentials: 'same-origin'
    });
    if (!res.ok) throw new Error('退出失败，请重试');
    const guideToken = document.getElementById('guideToken');
    if (guideToken) guideToken.textContent = '';
    const guideInstallCmd = document.getElementById('guideInstallCmd');
    if (guideInstallCmd) guideInstallCmd.textContent = '';
    const guideRunCmd = document.getElementById('guideRunCmd');
    if (guideRunCmd) guideRunCmd.textContent = '';
    setAdminState(false);
  } catch (error) { alert(error.message); }
}

const logoutBtn = document.getElementById('logoutBtn');
if (logoutBtn) logoutBtn.addEventListener('click', handleLogout);

// Password show/hide toggle handling
function setPasswordVisibility(btn, input, visible) {
  if (!btn || !input) return;
  input.type = visible ? 'text' : 'password';
  btn.textContent = visible ? '🙈' : '👁️';
  btn.title = visible ? '隐藏密码' : '显示密码';
  btn.setAttribute('aria-label', btn.title);
  btn.setAttribute('aria-pressed', visible ? 'true' : 'false');
}

document.addEventListener('click', (e) => {
  const btn = e.target.closest('.btn-toggle-pwd');
  if (!btn) return;
  const targetId = btn.dataset.target;
  if (!targetId) return;
  const input = document.getElementById(targetId);
  if (!input) return;
  setPasswordVisibility(btn, input, input.type === 'password');
});

