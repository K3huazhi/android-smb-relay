// Application shell: boot, authentication, hash router, navigation and the
// session-lock flow (the CSRF token is memory-only, so a reload unlocks only
// after the user signs in again).

import {
  api,
  errorMessage,
  setCsrfToken,
  setRequiresAuth,
  hasCsrfToken,
  setUnauthorizedHandler,
  setUnlockHandler,
} from './api.js';
import { toast, setButtonBusy, renderPageError, closeAllModals } from './ui.js';
import * as dashboardPage from './pages/dashboard.js';
import * as sharesPage from './pages/shares.js';
import * as usersPage from './pages/users.js';
import * as networkPage from './pages/network.js';
import * as connectPage from './pages/connect.js';
import * as settingsPage from './pages/settings.js';
import * as logsPage from './pages/logs.js';

const PAGES = {
  dashboard: {
    title: '概览',
    subtitle: 'SMB 中继的实时状态。',
    mount: dashboardPage.mount,
  },
  shares: {
    title: '共享',
    subtitle: '中继对外提供的 SMB 共享。',
    mount: sharesPage.mount,
  },
  users: {
    title: '用户',
    subtitle: '允许通过认证的 SMB 账户。',
    mount: usersPage.mount,
  },
  network: {
    title: '网络',
    subtitle: '网络接口与接口选择。',
    mount: networkPage.mount,
  },
  connect: {
    title: '连接',
    subtitle: 'SMB 地址与电脑端快速挂载。',
    mount: connectPage.mount,
  },
  settings: {
    title: '设置',
    subtitle: 'SMB、WebUI、启动、存储与日志。',
    mount: settingsPage.mount,
  },
  logs: {
    title: '日志',
    subtitle: '查看 asrd 与 Samba 日志。',
    mount: logsPage.mount,
  },
};
const DEFAULT_PAGE = 'dashboard';

/* --- Element references -------------------------------------------------- */
const bootScreen = document.getElementById('boot');
const loginView = document.getElementById('login-view');
const loginForm = document.getElementById('login-form');
const loginTitle = document.getElementById('login-title');
const loginSub = document.getElementById('login-sub');
const loginUsername = document.getElementById('login-username');
const loginPassword = document.getElementById('login-password');
const loginSubmit = document.getElementById('login-submit');
const loginError = document.getElementById('login-error');
const loginNotice = document.getElementById('login-notice');
const forceForm = document.getElementById('force-form');
const forceNew = document.getElementById('force-new');
const forceConfirm = document.getElementById('force-confirm');
const forceSubmit = document.getElementById('force-submit');
const forceError = document.getElementById('force-error');

const appView = document.getElementById('app-view');
const pageRoot = document.getElementById('page-root');
const pageTitle = document.getElementById('page-title');
const pageSubtitle = document.getElementById('page-subtitle');
const adminUserChip = document.getElementById('admin-user');
const logoutBtn = document.getElementById('logout-btn');
const unlockBtn = document.getElementById('unlock-btn');
const appVersion = document.getElementById('app-version');

const app = {
  booted: false,
  authRequired: true,
  authenticated: false,
  currentPage: null,
  cleanup: null,
  adminUser: '',
  loginVisible: false,
  pendingPassword: '',
};

const context = {
  relogin(message) {
    setCsrfToken('');
    app.authenticated = false;
    showLogin(message || '请重新登录。', 'info');
  },
  refreshPage() {
    if (app.currentPage) {
      navigate(app.currentPage, { force: true });
    }
  },
};

/* --- Boot ---------------------------------------------------------------- */
async function boot() {
  setUnauthorizedHandler(handleUnauthorized);
  setUnlockHandler(handleUnlockRequired);
  wireEvents();

  try {
    const status = await api.authStatus();
    app.authRequired = Boolean(status && status.auth_required);
    setRequiresAuth(app.authRequired);
    app.adminUser = (status && status.admin_user) || 'admin';
    adminUserChip.textContent = app.adminUser;
    adminUserChip.hidden = !app.adminUser;
    loginUsername.value = app.adminUser;

    if (status && status.authenticated) {
      app.authenticated = true;
      showApplication();
      await navigate(routeFromHash(), { force: true });
      if (app.authRequired && !hasCsrfToken()) {
        toast(
          '会话已恢复为只读。请选择“解锁更改”重新登录。',
          'info',
          6500,
        );
      }
    } else if (status && status.must_change_password) {
      showLogin('初始密码为 admin，首次登录后必须修改。', 'info');
    } else {
      showLogin('', 'error');
    }
  } catch (error) {
    if (error && error.status === 403) {
      showLogin('仅允许私有局域网地址访问。', 'error');
    } else {
      showLogin('无法连接服务器，请检查连接后重试。', 'error');
    }
  } finally {
    bootScreen.hidden = true;
    app.booted = true;
  }

  loadVersion();
}

async function loadVersion() {
  try {
    const health = await api.health();
    appVersion.textContent = health && health.version ? `asrd ${health.version}` : 'asrd';
  } catch (error) {
    appVersion.textContent = 'asrd';
  }
}

/* --- View switching ------------------------------------------------------ */
function showApplication() {
  loginView.hidden = true;
  appView.hidden = false;
  app.loginVisible = false;
  bootScreen.hidden = true;
  updateUnlockButton();
}

function showForcePasswordChange(currentPassword) {
  loginView.hidden = false;
  appView.hidden = true;
  bootScreen.hidden = true;
  loginNotice.hidden = true;
  loginTitle.hidden = true;
  loginSub.hidden = true;
  loginForm.hidden = true;
  forceForm.hidden = false;
  forceError.hidden = true;
  forceError.textContent = '';
  forceNew.value = '';
  forceConfirm.value = '';
  app.pendingPassword = currentPassword;
  app.loginVisible = true;
  setButtonBusy(forceSubmit, false);
  window.setTimeout(() => forceNew.focus(), 0);
}

function showLogin(message = '', kind = 'error') {
  closeAllModals();
  teardownPage();
  app.currentPage = null;
  appView.hidden = true;
  loginView.hidden = false;
  bootScreen.hidden = true;
  loginTitle.hidden = false;
  loginSub.hidden = false;
  loginForm.hidden = false;
  forceForm.hidden = true;
  forceError.hidden = true;
  forceError.textContent = '';
  forceNew.value = '';
  forceConfirm.value = '';
  app.pendingPassword = '';
  loginNotice.textContent = message || '';
  loginNotice.className = `notice notice-${kind}`;
  loginNotice.hidden = !message;
  loginError.hidden = true;
  loginError.textContent = '';
  loginPassword.value = '';
  loginUsername.value = app.adminUser || loginUsername.value;
  setButtonBusy(loginSubmit, false);
  setButtonBusy(forceSubmit, false);
  loginSubmit.disabled = false;
  app.loginVisible = true;
  updateUnlockButton();
  window.setTimeout(() => {
    if (!loginView.hidden) loginUsername.focus();
  }, 0);
}

function updateUnlockButton() {
  const locked = app.authRequired && !hasCsrfToken();
  unlockBtn.hidden = !(appView.hidden === false && locked);
}

/* --- Router -------------------------------------------------------------- */
function routeFromHash() {
  const raw = location.hash.replace(/^#\/?/, '').trim().toLowerCase();
  if (PAGES[raw]) return raw;
  history.replaceState(null, '', `#/${DEFAULT_PAGE}`);
  return DEFAULT_PAGE;
}

function setActiveNav(name) {
  for (const node of document.querySelectorAll('[data-nav]')) {
    const active = node.dataset.nav === name;
    node.classList.toggle('active', active);
    if (active) node.setAttribute('aria-current', 'page');
    else node.removeAttribute('aria-current');
  }
}

function teardownPage() {
  const cleanup = app.cleanup;
  app.cleanup = null;
  if (typeof cleanup === 'function') {
    try {
      cleanup();
    } catch (error) {
      /* cleanup must never block navigation */
    }
  }
}

async function navigate(name, options = {}) {
  const pageName = PAGES[name] ? name : DEFAULT_PAGE;
  if (!options.force && pageName === app.currentPage) return;

  closeAllModals();
  teardownPage();
  app.currentPage = pageName;
  const meta = PAGES[pageName];
  pageTitle.textContent = meta.title;
  pageSubtitle.textContent = meta.subtitle || '';
  pageSubtitle.hidden = !meta.subtitle;
  document.title = `${meta.title} · Android SMB Relay`;
  setActiveNav(pageName);
  pageRoot.replaceChildren();
  const main = document.querySelector('.main');
  if (main) main.scrollTop = 0;

  try {
    const cleanup = await meta.mount(pageRoot, context);
    if (app.currentPage === pageName) {
      app.cleanup = typeof cleanup === 'function' ? cleanup : null;
    } else if (typeof cleanup === 'function') {
      cleanup();
    }
  } catch (error) {
    if (error && error.status === 401) return;
    renderPageError(pageRoot, errorMessage(error), () =>
      navigate(pageName, { force: true }),
    );
  }
}

/* --- Authentication actions --------------------------------------------- */
async function handleLogin(event) {
  event.preventDefault();
  loginError.hidden = true;
  loginError.textContent = '';
  const username = loginUsername.value.trim();
  const password = loginPassword.value;
  if (!username || !password) {
    showLoginError('请输入用户名和密码。');
    return;
  }

  setButtonBusy(loginSubmit, true);
  try {
    const result = await api.login(username, password);
    app.authenticated = true;
    setCsrfToken(result && result.csrf_token ? result.csrf_token : '');
    loginPassword.value = '';
    loginNotice.hidden = true;
    if (result && result.must_change_password) {
      showForcePasswordChange(password);
      toast('首次登录必须修改管理员密码。', 'info', 4000);
      return;
    }
    showApplication();
    await navigate(routeFromHash(), { force: true });
    toast('已登录。', 'success', 2500);
  } catch (error) {
    loginPassword.value = '';
    if (error && error.status === 401) {
      showLoginError('用户名或密码错误。');
    } else if (error && error.status === 429) {
      const retry = error.retryAfter > 0 ? ` 请 ${error.retryAfter} 秒后重试。` : ' 请稍后重试。';
      showLoginError(`失败次数过多。${retry}`);
    } else {
      showLoginError(errorMessage(error));
    }
    loginPassword.focus();
  } finally {
    setButtonBusy(loginSubmit, false);
    updateUnlockButton();
  }
}

function showLoginError(message) {
  loginError.textContent = message;
  loginError.hidden = !message;
}

async function handleForcePasswordChange(event) {
  event.preventDefault();
  forceError.hidden = true;
  forceError.textContent = '';
  const next = forceNew.value;
  const confirm = forceConfirm.value;
  if (next.length < 10) {
    forceError.textContent = '新密码至少需要 10 个字符。';
    forceError.hidden = false;
    forceNew.focus();
    return;
  }
  if (next !== confirm) {
    forceError.textContent = '两次输入的新密码不一致。';
    forceError.hidden = false;
    forceConfirm.focus();
    return;
  }
  setButtonBusy(forceSubmit, true);
  try {
    await api.changePassword(app.pendingPassword, next);
    setCsrfToken('');
    app.authenticated = false;
    app.pendingPassword = '';
    showLogin('密码已修改，请用新密码重新登录。', 'success');
  } catch (error) {
    forceError.textContent = errorMessage(error);
    forceError.hidden = false;
    setButtonBusy(forceSubmit, false);
  }
}

async function logout() {
  logoutBtn.disabled = true;
  try {
    await api.logout();
  } catch (error) {
    /* Even if the request fails (offline, expired), clear the local UI. */
  } finally {
    logoutBtn.disabled = false;
    setCsrfToken('');
    app.authenticated = false;
    showLogin('已退出登录。', 'info');
  }
}

function unlock() {
  if (!app.authRequired) return;
  showLogin(
    '请重新登录以启用更改。CSRF 令牌仅保存在内存中，因此页面刷新后执行写操作需要重新登录。',
    'info',
  );
}

/* --- Global API events --------------------------------------------------- */
function handleUnauthorized() {
  if (!loginView.hidden) return;
  if (!app.authenticated) return;
  app.authenticated = false;
  setCsrfToken('');
  showLogin('会话已过期，请重新登录。', 'error');
}

function handleUnlockRequired() {
  if (!loginView.hidden) return;
  if (appView.hidden) return;
  unlock();
}

/* --- Events -------------------------------------------------------------- */
function wireEvents() {
  loginForm.addEventListener('submit', handleLogin);
  forceForm.addEventListener('submit', handleForcePasswordChange);
  logoutBtn.addEventListener('click', logout);
  unlockBtn.addEventListener('click', unlock);

  window.addEventListener('hashchange', () => {
    if (loginView.hidden === false) return;
    navigate(routeFromHash());
  });
}

boot();
