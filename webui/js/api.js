// HTTP client for the asrd WebUI API.
//
// Security notes:
//   * The CSRF token is kept in a module-scoped variable only. It is never
//     written to localStorage, sessionStorage, cookies or the DOM, and it is
//     deliberately lost on page reload (the backend only hands it out once,
//     from POST /api/auth/login). After a reload the app runs read-only until
//     the user signs in again via the "Unlock changes" flow.
//   * No credentials or response bodies containing credentials are logged.
//   * The session cookie itself is HttpOnly and handled by the browser.

const state = {
  csrfToken: '',
  requiresAuth: true,
  unauthorizedHandler: null,
  unlockHandler: null,
};

export class ApiError extends Error {
  constructor(message, options = {}) {
    super(message || '请求失败。');
    this.name = 'ApiError';
    this.status = Number(options.status) || 0;
    this.code = options.code || '';
    this.retryAfter = Number(options.retryAfter) || 0;
    this.network = Boolean(options.network);
  }
}

export function setCsrfToken(token) {
  state.csrfToken = typeof token === 'string' ? token : '';
}

export function getCsrfToken() {
  return state.csrfToken;
}

export function hasCsrfToken() {
  return state.csrfToken !== '';
}

export function setRequiresAuth(value) {
  state.requiresAuth = Boolean(value);
}

export function isAuthRequired() {
  return state.requiresAuth;
}

export function setUnauthorizedHandler(handler) {
  state.unauthorizedHandler = typeof handler === 'function' ? handler : null;
}

export function setUnlockHandler(handler) {
  state.unlockHandler = typeof handler === 'function' ? handler : null;
}

function isWriteMethod(method) {
  return method !== 'GET' && method !== 'HEAD';
}

function buildQuery(query) {
  if (!query) return '';
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '') continue;
    params.set(key, String(value));
  }
  const encoded = params.toString();
  return encoded ? `?${encoded}` : '';
}

/**
 * Perform one API request.
 *
 * @param {string} path API path, e.g. "/api/status".
 * @param {object} [options]
 * @param {string} [options.method] HTTP method, default GET.
 * @param {object} [options.body] JSON request body.
 * @param {object} [options.query] Query string parameters (empty values skipped).
 * @param {boolean} [options.csrf] True when the endpoint is CSRF protected.
 * @returns {Promise<object|null>}
 */
async function request(path, options = {}) {
  const method = (options.method || 'GET').toUpperCase();
  const url = path + buildQuery(options.query);
  const headers = {};
  const init = {
    method,
    headers,
    credentials: 'same-origin',
    cache: 'no-store',
  };

  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(options.body);
  }

  const write = isWriteMethod(method);
  if (write) {
    if (state.csrfToken) {
      headers['X-CSRF-Token'] = state.csrfToken;
    }
    if (options.csrf && state.requiresAuth && !state.csrfToken) {
      // The session cookie may still be valid after a page reload, but the
      // in-memory CSRF token is gone. Ask the shell to show the unlock form
      // instead of sending a request that is guaranteed to be rejected.
      if (state.unlockHandler) state.unlockHandler();
      throw new ApiError('请重新登录以启用更改。', {
        status: 403,
        code: 'unlock_required',
      });
    }
  }

  let response;
  try {
    response = await fetch(url, init);
  } catch (error) {
    throw new ApiError('无法连接服务器，请检查网络连接。', {
      network: true,
    });
  }

  if (response.status === 204) return null;

  const text = await response.text();
  let payload = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch (error) {
      payload = null;
    }
  }

  if (response.status === 401) {
    if (path !== '/api/auth/login' && state.unauthorizedHandler) {
      state.unauthorizedHandler();
    }
    throw new ApiError(errorMessageOf(payload) || '需要认证。', {
      status: 401,
      code: errorCodeOf(payload) || 'unauthorized',
    });
  }

  if (!response.ok) {
    const retryAfterHeader = response.headers.get('Retry-After');
    const retryAfter = retryAfterHeader ? Number(retryAfterHeader) : 0;
    throw new ApiError(
      errorMessageOf(payload) || `请求失败（HTTP ${response.status}）。`,
      {
        status: response.status,
        code: errorCodeOf(payload),
        retryAfter: Number.isFinite(retryAfter) ? retryAfter : 0,
      },
    );
  }

  return payload;
}

function errorMessageOf(payload) {
  if (payload && payload.error && typeof payload.error.message === 'string') {
    return payload.error.message;
  }
  return '';
}

function errorCodeOf(payload) {
  if (payload && payload.error && typeof payload.error.code === 'string') {
    return payload.error.code;
  }
  return '';
}

/** Human-readable text for any thrown value. */
export function errorMessage(error) {
  if (!error) return '未知错误。';
  if (error instanceof ApiError) {
    if (error.status === 429) {
      const retry = error.retryAfter > 0 ? ` 请 ${error.retryAfter} 秒后重试。` : '';
      return `${error.message}${retry}`;
    }
    return error.message;
  }
  if (typeof error.message === 'string' && error.message) return error.message;
  return String(error);
}

/** Short helper for action endpoints that just need a boolean success. */
function postAction(path) {
  return request(path, { method: 'POST', csrf: true });
}

export const api = {
  // Public endpoints ------------------------------------------------------
  health: () => request('/api/health'),
  authStatus: () => request('/api/auth/status'),
  login: (username, password) =>
    request('/api/auth/login', {
      method: 'POST',
      body: { username, password },
    }),

  // Session ---------------------------------------------------------------
  logout: () => request('/api/auth/logout', { method: 'POST' }),
  changePassword: (currentPassword, newPassword) =>
    request('/api/auth/password', {
      method: 'POST',
      body: {
        current_password: currentPassword,
        new_password: newPassword,
      },
    }),

  // Status / system -------------------------------------------------------
  status: () => request('/api/status'),
  network: () => request('/api/network'),
  system: () => request('/api/system'),

  // Shares ----------------------------------------------------------------
  shares: () => request('/api/shares'),
  createShare: (body) =>
    request('/api/shares', { method: 'POST', body, csrf: true }),
  updateShare: (id, body) =>
    request(`/api/shares/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body,
      csrf: true,
    }),
  deleteShare: (id) =>
    request(`/api/shares/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      csrf: true,
    }),
  fsList: (path) =>
    request('/api/fs/list', { query: path ? { path } : undefined }),

  // Users -----------------------------------------------------------------
  users: () => request('/api/users'),
  createUser: (body) =>
    request('/api/users', { method: 'POST', body, csrf: true }),
  updateUser: (name, body) =>
    request(`/api/users/${encodeURIComponent(name)}`, {
      method: 'PUT',
      body,
      csrf: true,
    }),
  deleteUser: (name) =>
    request(`/api/users/${encodeURIComponent(name)}`, {
      method: 'DELETE',
      csrf: true,
    }),

  // Service control -------------------------------------------------------
  serviceStart: () => postAction('/api/service/start'),
  serviceStop: () => postAction('/api/service/stop'),
  serviceRestart: () => postAction('/api/service/restart'),
  serviceReload: () => postAction('/api/service/reload'),

  // Logs / settings -------------------------------------------------------
  logs: (source, level, limit) =>
    request('/api/logs', { query: { source, level, limit } }),
  settings: () => request('/api/settings'),
  saveSettings: (sections) =>
    request('/api/settings', { method: 'PUT', body: sections, csrf: true }),
};
