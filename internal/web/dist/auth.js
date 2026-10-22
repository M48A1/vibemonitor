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
  quickActions.style.display = admin ? 'block' : 'none';
  if (!admin) closeSiteMenu();
  if (changed && nodes.length) renderNodes();
}

// Event Listeners
function requireAdmin(action) {
  if (isAdmin) {
    action();
    return;
  }
  pendingAdminAction = action;
  const error = document.getElementById('loginError');
  error.style.display = 'none';
  error.textContent = '账号或密码错误';
  const pwInput = document.getElementById('loginPassword');
  pwInput.value = '';
  pwInput.type = 'password';
  pwInput.autocomplete = 'current-password';
  const pwToggleBtn = document.querySelector('.btn-toggle-pwd[data-target="loginPassword"]');
  if (pwToggleBtn) { pwToggleBtn.textContent = '👁️'; pwToggleBtn.title = '显示密码'; }
  const unInput = document.getElementById('loginUsername');
  if (unInput) unInput.autocomplete = 'username';
  openModal('loginModal');
}

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
      document.getElementById('loginError').style.display = 'block';
    }
  } catch (e) {
    document.getElementById('loginError').textContent = '连接失败: ' + e.message;
    document.getElementById('loginError').style.display = 'block';
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
document.addEventListener('click', (e) => {
  const btn = e.target.closest('.btn-toggle-pwd');
  if (!btn) return;
  const targetId = btn.dataset.target;
  if (!targetId) return;
  const input = document.getElementById(targetId);
  if (!input) return;
  if (input.type === 'password') {
    input.type = 'text';
    btn.textContent = '🙈';
    btn.title = '隐藏密码';
  } else {
    input.type = 'password';
    btn.textContent = '👁️';
    btn.title = '显示密码';
  }
});

