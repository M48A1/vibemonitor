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

// Only an explicit click on the site title can request dashboard login.
// Session checks and public data refreshes never open the login dialog.
document.getElementById('siteMenuTrigger').addEventListener('click', async (event) => {
  if (event.defaultPrevented || event.button > 0 || event.metaKey || event.ctrlKey ||
      event.shiftKey || event.altKey) return;
  event.preventDefault();
  await checkAdminAuth();
  requireAdmin(() => { location.href = '/admin'; });
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

