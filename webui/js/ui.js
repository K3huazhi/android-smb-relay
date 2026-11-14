// Small, dependency-free DOM and presentation helpers shared by all pages.
// All user-controlled text goes through textContent/createTextNode; this module
// never assigns innerHTML.

const SVG_NS = 'http://www.w3.org/2000/svg';

// Open modal dialogs are tracked so the shell can close them when the session
// ends or the route changes.
const openDialogs = new Set();

export function closeAllModals() {
  for (const dialog of [...openDialogs]) {
    try {
      if (dialog.open) dialog.close();
    } catch (error) {
      /* closing is best effort */
    }
  }
  openDialogs.clear();
}

const BOOLEAN_PROPS = new Set([
  'checked',
  'disabled',
  'selected',
  'readOnly',
  'required',
  'hidden',
  'multiple',
  'autofocus',
]);

const VALUE_PROPS = new Set(['value']);

/**
 * Build an element.
 *
 * @param {string} tag
 * @param {object} [props]  class, text, dataset, on<event>, boolean/normal attributes
 * @param {...(Node|string|number|Array|null|false)} children
 */
export function el(tag, props, ...children) {
  const node = document.createElement(tag);
  if (props && typeof props === 'object') {
    for (const [key, value] of Object.entries(props)) {
      if (value === undefined || value === null || value === false) continue;
      if (key === 'class' || key === 'className') {
        node.className = String(value);
      } else if (key === 'text') {
        node.textContent = String(value);
      } else if (key === 'dataset') {
        for (const [dataKey, dataValue] of Object.entries(value)) {
          if (dataValue === undefined || dataValue === null) continue;
          node.dataset[dataKey] = String(dataValue);
        }
      } else if (key.startsWith('on') && typeof value === 'function') {
        node.addEventListener(key.slice(2).toLowerCase(), value);
      } else if (BOOLEAN_PROPS.has(key)) {
        node[key] = Boolean(value);
      } else if (VALUE_PROPS.has(key)) {
        node.value = String(value);
      } else if (value === true) {
        node.setAttribute(key, '');
      } else {
        node.setAttribute(key, String(value));
      }
    }
  }
  appendChildren(node, children);
  return node;
}

export function appendChildren(node, children) {
  for (const child of children) {
    if (child === undefined || child === null || child === false) continue;
    if (Array.isArray(child)) {
      appendChildren(node, child);
    } else if (child instanceof Node) {
      node.appendChild(child);
    } else {
      node.appendChild(document.createTextNode(String(child)));
    }
  }
  return node;
}

/* ------------------------------------------------------------------------ */
/* Icons                                                                     */
/* ------------------------------------------------------------------------ */

const ICON_PATHS = {
  folder: ['M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z'],
  file: ['M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z', 'M14 2v6h6'],
  up: ['M12 19V5', 'M5 12l7-7 7 7'],
  refresh: ['M21 12a9 9 0 1 1-2.64-6.36', 'M21 3v6h-6'],
  plus: ['M12 5v14', 'M5 12h14'],
  lock: ['M5 11h14v10H5z', 'M8 11V7a4 4 0 0 1 8 0v4'],
  unlock: ['M5 11h14v10H5z', 'M8 11V7a4 4 0 0 1 7.5-2'],
  key: ['M21 2l-2 2', 'M15.5 8.5a4.5 4.5 0 1 1-6.36 6.36L6 18l-2 2-2-2 2-2 3.14-3.14A4.5 4.5 0 0 1 15.5 8.5z'],
  trash: ['M3 6h18', 'M8 6V4h8v2', 'M19 6l-1 14H6L5 6', 'M10 11v6', 'M14 11v6'],
  edit: ['M4 20h4L19 9l-4-4L4 16z', 'M14 6l4 4'],
  terminal: ['M4 4h16v16H4z', 'M8 9l3 3-3 3', 'M13 15h4'],
};

export function icon(name, size = 18) {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('width', String(size));
  svg.setAttribute('height', String(size));
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '1.8');
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('class', 'nav-icon');
  const paths = ICON_PATHS[name] || [];
  for (const definition of paths) {
    const path = document.createElementNS(SVG_NS, 'path');
    path.setAttribute('d', definition);
    svg.appendChild(path);
  }
  return svg;
}

/* ------------------------------------------------------------------------ */
/* Formatting                                                                */
/* ------------------------------------------------------------------------ */

const BYTE_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];

export function fmtBytes(bytes) {
  const value = Number(bytes);
  if (!Number.isFinite(value) || value < 0) return '—';
  let scaled = value;
  let unit = 0;
  while (scaled >= 1024 && unit < BYTE_UNITS.length - 1) {
    scaled /= 1024;
    unit += 1;
  }
  const digits = unit === 0 ? 0 : scaled >= 100 ? 0 : scaled >= 10 ? 1 : 2;
  return `${scaled.toFixed(digits)} ${BYTE_UNITS[unit]}`;
}

export function fmtRate(bytesPerSecond) {
  const value = Number(bytesPerSecond);
  if (!Number.isFinite(value) || value < 0) return '—';
  return `${fmtBytes(value)}/s`;
}

export function fmtPercent(value) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) return '—';
  return `${numeric.toFixed(1)}%`;
}

export function fmtDuration(totalSeconds) {
  const seconds = Number(totalSeconds);
  if (!Number.isFinite(seconds) || seconds < 0) return '—';
  const whole = Math.floor(seconds);
  const days = Math.floor(whole / 86400);
  const hours = Math.floor((whole % 86400) / 3600);
  const minutes = Math.floor((whole % 3600) / 60);
  const secs = whole % 60;
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m ${secs}s`;
  return `${secs}s`;
}

export function fmtTime(value) {
  if (value === undefined || value === null || value === '') return '—';
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return date.toLocaleString();
}

export function fmtUnixSeconds(seconds) {
  const numeric = Number(seconds);
  if (!Number.isFinite(numeric) || numeric <= 0) return '—';
  return fmtTime(new Date(numeric * 1000));
}

export function fmtNumber(value) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) return '—';
  return numeric.toLocaleString();
}

export function clampPercent(value) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) return 0;
  return Math.max(0, Math.min(100, numeric));
}

/* ------------------------------------------------------------------------ */
/* Status, badges                                                            */
/* ------------------------------------------------------------------------ */

const SMB_STATES = {
  running: { label: '运行中', kind: 'ok' },
  starting: { label: '启动中', kind: 'info' },
  restarting: { label: '重启中', kind: 'warn' },
  waiting_network: { label: '等待网络', kind: 'warn' },
  waiting_storage: { label: '等待存储', kind: 'warn' },
  untrusted_network: { label: '不可信网络', kind: 'danger' },
  stopped: { label: '已停止', kind: 'idle' },
  failed: { label: '失败', kind: 'danger' },
};

export function smbStateInfo(state) {
  const key = String(state || '').toLowerCase();
  return SMB_STATES[key] || { label: state ? String(state) : '未知', kind: 'idle' };
}

/**
 * A status dot plus label. Call `.update(state)` to change it in place.
 */
export function statusPill(initialState = 'unknown') {
  const dot = el('span', { class: 'dot', 'aria-hidden': 'true' });
  const label = el('span', { class: 'status-label', text: '未知' });
  const node = el('span', { class: 'status', role: 'status' }, dot, label);
  node.update = (state) => {
    const info = smbStateInfo(state);
    dot.className = `dot dot-${info.kind}`;
    label.textContent = info.label;
  };
  node.update(initialState);
  return node;
}

export function badge(text, kind = 'idle') {
  return el('span', { class: `badge badge-${kind}`, text: String(text) });
}

export function mutedDash() {
  return el('span', { class: 'muted', text: '—' });
}

export function spinner() {
  return el('span', { class: 'spinner', 'aria-hidden': 'true' });
}

/* ------------------------------------------------------------------------ */
/* Toasts                                                                    */
/* ------------------------------------------------------------------------ */

const TOAST_TYPES = new Set(['info', 'success', 'warn', 'error']);

export function toast(message, type = 'info', timeout = 4200) {
  const region = document.getElementById('toast-region');
  if (!region) return;
  const kind = TOAST_TYPES.has(type) ? type : 'info';
  while (region.children.length >= 4) {
    region.firstElementChild.remove();
  }
  const node = el(
    'div',
    { class: `toast toast-${kind}`, role: 'status' },
    el('span', { class: 'toast-text', text: String(message) }),
  );
  let removed = false;
  const dismiss = () => {
    if (removed) return;
    removed = true;
    node.classList.add('leaving');
    window.setTimeout(() => node.remove(), 180);
  };
  node.addEventListener('click', dismiss);
  region.appendChild(node);
  if (timeout > 0) window.setTimeout(dismiss, timeout);
}

/* ------------------------------------------------------------------------ */
/* Modals                                                                    */
/* ------------------------------------------------------------------------ */

/**
 * Open a modal dialog built from DOM nodes (never HTML strings).
 *
 * @param {object} options
 * @param {string} options.title
 * @param {Node} options.content
 * @param {Array<{label: string, kind?: string, onClick: (controls: object) => any, disabled?: boolean}>} [options.actions]
 * @param {'md'|'wide'} [options.size]
 * @param {() => void} [options.onClose]
 * @returns {{dialog: HTMLDialogElement, body: HTMLElement, footer: HTMLElement|null, buttons: HTMLButtonElement[], close: () => void, setBusy: (busy: boolean) => void, setError: (message: string) => void, clearError: () => void, busy: boolean}}
 */
export function openModal(options) {
  const dialog = el('dialog', {
    class: `modal${options.size === 'wide' ? ' modal-wide' : ''}`,
  });
  const title = options.title || '对话框';
  const closeButton = el('button', {
    class: 'btn btn-icon',
    type: 'button',
    'aria-label': '关闭对话框',
    text: '✕',
  });
  const head = el(
    'div',
    { class: 'modal-head' },
    el('h2', { class: 'modal-title', text: title }),
    closeButton,
  );

  const body = el('div', { class: 'modal-body' });
  appendChildren(body, [options.content]);
  const error = el('div', {
    class: 'form-error modal-error',
    role: 'alert',
    hidden: true,
  });
  body.appendChild(error);

  const controls = {
    dialog,
    body,
    footer: null,
    buttons: [],
    busy: false,
    close() {
      if (dialog.open) dialog.close();
    },
    setBusy(busy) {
      controls.busy = Boolean(busy);
      for (const button of controls.buttons) button.disabled = controls.busy;
      closeButton.disabled = controls.busy;
    },
    setError(message) {
      error.textContent = message || '';
      error.hidden = !message;
      if (message) {
        try {
          error.scrollIntoView({ block: 'nearest' });
        } catch (ignored) {
          /* scrollIntoView is best effort */
        }
      }
    },
    clearError() {
      error.textContent = '';
      error.hidden = true;
    },
  };

  if (Array.isArray(options.actions) && options.actions.length > 0) {
    const footer = el('div', { class: 'modal-foot' });
    for (const action of options.actions) {
      const button = el('button', {
        class: `btn btn-${action.kind || 'ghost'}`,
        type: 'button',
        text: action.label,
        disabled: Boolean(action.disabled),
      });
      button.addEventListener('click', () => {
        if (controls.busy) return;
        try {
          const result = action.onClick(controls);
          if (result && typeof result.then === 'function') {
            result.catch((actionError) => {
              controls.setError(
                actionError && actionError.message
                  ? actionError.message
                  : '操作失败。',
              );
            });
          }
        } catch (actionError) {
          controls.setError(
            actionError && actionError.message
              ? actionError.message
              : '操作失败。',
          );
        }
      });
      controls.buttons.push(button);
      footer.appendChild(button);
    }
    controls.footer = footer;
    dialog.append(head, body, footer);
  } else {
    dialog.append(head, body);
  }

  closeButton.addEventListener('click', () => {
    if (!controls.busy) dialog.close();
  });
  dialog.addEventListener('click', (event) => {
    if (event.target !== dialog) return;
    if (controls.busy) return;
    dialog.close();
  });
  dialog.addEventListener('cancel', (event) => {
    if (controls.busy) {
      event.preventDefault();
    }
  });

  let closed = false;
  dialog.addEventListener('close', () => {
    if (closed) return;
    closed = true;
    openDialogs.delete(dialog);
    dialog.remove();
    if (typeof options.onClose === 'function') options.onClose();
  });

  openDialogs.add(dialog);
  document.body.appendChild(dialog);
  dialog.showModal();

  const firstField = dialog.querySelector('input, select, textarea');
  if (firstField) {
    firstField.focus();
  } else {
    closeButton.focus();
  }
  return controls;
}

/**
 * Ask for confirmation. Resolves true when confirmed, false otherwise.
 */
export function confirmAction({
  title,
  message,
  details = null,
  confirmLabel = '确认',
  cancelLabel = '取消',
  danger = false,
}) {
  return new Promise((resolve) => {
    let settled = false;
    const content = el(
      'div',
      { class: 'stack' },
      el('p', { class: 'modal-message', text: message }),
      details ? el('div', { class: 'modal-details', text: details }) : null,
    );
    const finish = (value) => {
      if (settled) return;
      settled = true;
      resolve(value);
    };
    openModal({
      title,
      content,
      onClose: () => finish(false),
      actions: [
        {
          label: cancelLabel,
          kind: 'ghost',
          onClick: (controls) => {
            finish(false);
            controls.close();
          },
        },
        {
          label: confirmLabel,
          kind: danger ? 'danger' : 'primary',
          onClick: (controls) => {
            finish(true);
            controls.close();
          },
        },
      ],
    });
  });
}

/* ------------------------------------------------------------------------ */
/* Loading / empty / error states                                            */
/* ------------------------------------------------------------------------ */

export function renderLoading(root, label = '加载中…') {
  root.replaceChildren(
    el('div', { class: 'loading-block' }, spinner(), el('span', { text: label })),
  );
}

export function emptyState({ title, text, actions = [] }) {
  return el(
    'div',
    { class: 'empty' },
    el('p', { class: 'empty-title', text: title }),
    text ? el('p', { class: 'empty-text', text }) : null,
    actions.length > 0 ? el('div', { class: 'empty-actions' }, actions) : null,
  );
}

export function renderPageError(root, message, onRetry) {
  root.replaceChildren(
    emptyState({
      title: '无法加载此页面',
      text: message,
      actions: onRetry
        ? [
            el('button', {
              class: 'btn btn-primary',
              type: 'button',
              text: '重试',
              onclick: onRetry,
            }),
          ]
        : [],
    }),
  );
}

/* ------------------------------------------------------------------------ */
/* Form helpers                                                              */
/* ------------------------------------------------------------------------ */

export function field({ label, control, hint, required = false, id }) {
  const labelNode = el('label', { for: id || undefined, text: label });
  if (required) labelNode.appendChild(el('span', { class: 'req', text: '*', 'aria-hidden': 'true' }));
  return el(
    'div',
    { class: 'field' },
    labelNode,
    control,
    hint ? el('span', { class: 'hint', text: hint }) : null,
  );
}

export function setFormError(node, message) {
  if (!node) return;
  node.textContent = message || '';
  node.hidden = !message;
}

export function setButtonBusy(button, busy) {
  if (!button) return;
  if (busy) {
    if (button.dataset.busy === '1') return;
    button.dataset.busy = '1';
    button.dataset.idleLabel = button.textContent;
    button.disabled = true;
    button.replaceChildren(spinner(), document.createTextNode('处理中…'));
  } else {
    if (button.dataset.busy !== '1') return;
    button.dataset.busy = '0';
    button.disabled = false;
    button.textContent = button.dataset.idleLabel || '';
  }
}
