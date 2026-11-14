// Users: SMB account list, creation, password change, enable/disable, delete.
// Passwords are write-only: the API never returns them and this UI never
// pre-fills, prints or stores them.

import { api, errorMessage } from '../api.js';
import {
  el,
  field,
  spinner,
  badge,
  toast,
  openModal,
  confirmAction,
  fmtUnixSeconds,
  emptyState,
  setButtonBusy,
} from '../ui.js';

const USER_NAME_RE = /^[a-z][a-z0-9._-]{0,31}$/;
const CONTROL_CHARS_RE = /[\u0000\r\n]/;
const MIN_PASSWORD = 10;
const MAX_SMB_PASSWORD = 128;

export async function mount(root) {
  let users = [];
  let loading = false;

  const countText = el('p', { class: 'muted small', text: '正在加载用户…' });
  const tableHost = el('div', { class: 'table-host' });

  const refreshBtn = el('button', {
    class: 'btn btn-sm',
    type: 'button',
    text: '刷新',
    onclick: () => load(),
  });
  const newBtn = el('button', {
    class: 'btn btn-primary btn-sm',
    type: 'button',
    text: '新建用户',
    onclick: () => openCreateDialog(),
  });

  root.append(
    el(
      'div',
      { class: 'toolbar' },
      countText,
      el('div', { class: 'toolbar-actions' }, refreshBtn, newBtn),
    ),
    el('p', {
      class: 'muted small',
      text: 'SMB 用户使用 Samba 密码数据库认证。WebUI 不会显示密码。',
    }),
    tableHost,
  );

  /* --- Data loading ----------------------------------------------------- */
  async function load() {
    if (loading) return;
    loading = true;
    refreshBtn.disabled = true;
    if (users.length === 0) {
      tableHost.replaceChildren(
        el('div', { class: 'loading-block' }, spinner(), el('span', { text: '正在加载用户…' })),
      );
    }
    try {
      const list = await api.users();
      users = Array.isArray(list) ? list : [];
      renderTable();
    } catch (error) {
      if (error && error.status === 401) return;
      countText.textContent = '用户不可用';
      tableHost.replaceChildren(
        emptyState({
          title: '无法加载用户',
          text: errorMessage(error),
          actions: [
            el('button', {
              class: 'btn btn-primary',
              type: 'button',
              text: '重试',
              onclick: () => load(),
            }),
          ],
        }),
      );
    } finally {
      loading = false;
      refreshBtn.disabled = false;
    }
  }

  /* --- Table rendering -------------------------------------------------- */
  function renderTable() {
    countText.textContent =
      users.length === 1 ? '1 个 SMB 用户' : `${users.length} 个 SMB 用户`;
    if (users.length === 0) {
      tableHost.replaceChildren(
        emptyState({
          title: '没有 SMB 用户',
          text: '创建用户之前，客户端无法通过密码认证访问共享。',
          actions: [
            el('button', {
              class: 'btn btn-primary',
              type: 'button',
              text: '创建用户',
              onclick: () => openCreateDialog(),
            }),
          ],
        }),
      );
      return;
    }

    const rows = users.map((user) => {
      const toggleBtn = el('button', {
        class: 'btn btn-sm',
        type: 'button',
        text: user.enabled ? '停用' : '启用',
        onclick: () => toggleUser(user, toggleBtn),
      });
      const passwordBtn = el('button', {
        class: 'btn btn-sm',
        type: 'button',
        text: '修改密码',
        onclick: () => openPasswordDialog(user),
      });
      const deleteBtn = el('button', {
        class: 'btn btn-sm btn-danger',
        type: 'button',
        text: '删除',
        onclick: () => removeUser(user),
      });

      return el(
        'tr',
        { class: user.enabled ? '' : 'row-disabled' },
        el('td', { dataset: { label: '名称' } }, el('span', { class: 'cell-title mono', text: user.name || '—' })),
        el(
          'td',
          { dataset: { label: '状态' } },
          user.enabled ? badge('已启用', 'ok') : badge('已停用', 'idle'),
        ),
        el('td', { dataset: { label: '创建时间' }, text: fmtUnixSeconds(user.created_at) }),
        el('td', { dataset: { label: '更新时间' }, text: fmtUnixSeconds(user.updated_at) }),
        el(
          'td',
          { class: 'cell-actions', dataset: { label: '操作' } },
          passwordBtn,
          toggleBtn,
          deleteBtn,
        ),
      );
    });

    tableHost.replaceChildren(
      el(
        'div',
        { class: 'table-wrap' },
        el(
          'table',
          { class: 'data-table' },
          el(
            'thead',
            {},
            el(
              'tr',
              {},
              el('th', { text: '名称' }),
              el('th', { text: '状态' }),
              el('th', { text: '创建时间' }),
              el('th', { text: '更新时间' }),
              el('th', { text: '操作' }),
            ),
          ),
          el('tbody', {}, rows),
        ),
      ),
    );
  }

  /* --- Enable / disable ------------------------------------------------- */
  async function toggleUser(user, button) {
    setButtonBusy(button, true);
    try {
      await api.updateUser(user.name, { enabled: !user.enabled });
      toast(
        `用户“${user.name}”已${user.enabled ? '停用' : '启用'}。`,
        'success',
      );
      await load();
    } catch (error) {
      if (error && error.status === 401) return;
      toast(errorMessage(error), 'error', 6000);
      setButtonBusy(button, false);
    }
  }

  /* --- Delete ----------------------------------------------------------- */
  async function removeUser(user) {
    const confirmed = await confirmAction({
      title: `删除 SMB 用户“${user.name}”？`,
      message:
        '将删除该 Samba 账户及其配置项。磁盘上的文件归属保持不变，不会删除任何文件。',
      confirmLabel: '删除用户',
      danger: true,
    });
    if (!confirmed) return;
    try {
      await api.deleteUser(user.name);
      toast(`用户“${user.name}”已删除。`, 'success');
      await load();
    } catch (error) {
      if (error && error.status === 401) return;
      toast(errorMessage(error), 'error', 6000);
    }
  }

  /* --- Shared validation ------------------------------------------------ */
  function validatePassword(password, confirm) {
    if (!password) return '请输入密码。';
    if (password.length < MIN_PASSWORD) {
      return `密码至少需要 ${MIN_PASSWORD} 个字符。`;
    }
    if (password.length > MAX_SMB_PASSWORD) {
      return `密码最多 ${MAX_SMB_PASSWORD} 个字符。`;
    }
    if (CONTROL_CHARS_RE.test(password)) {
      return '密码包含无效字符。';
    }
    if (password !== confirm) return '两次输入的密码不一致。';
    return '';
  }

  /* --- Create dialog ---------------------------------------------------- */
  function openCreateDialog() {
    const nameInput = el('input', {
      id: 'user-name-input',
      type: 'text',
      autocomplete: 'off',
      spellcheck: 'false',
      autocapitalize: 'off',
      maxlength: 32,
      placeholder: '例如 media',
    });
    const passwordInput = el('input', {
      id: 'user-password-input',
      type: 'password',
      autocomplete: 'new-password',
      maxlength: MAX_SMB_PASSWORD,
    });
    const confirmInput = el('input', {
      id: 'user-password-confirm',
      type: 'password',
      autocomplete: 'new-password',
      maxlength: MAX_SMB_PASSWORD,
    });
    const enabledInput = el('input', { type: 'checkbox', id: 'user-enabled', checked: true });

    const form = el(
      'form',
      { class: 'stack', novalidate: true },
      field({
        id: 'user-name-input',
        label: '用户名',
        control: nameInput,
        required: true,
        hint: '以小写字母开头；最多 32 个字符：a–z、0–9、点、下划线或短横线。',
      }),
      field({
        id: 'user-password-input',
        label: '密码',
        control: passwordInput,
        required: true,
        hint: `至少 ${MIN_PASSWORD} 个字符，保存后不再显示。`,
      }),
      field({
        id: 'user-password-confirm',
        label: '确认密码',
        control: confirmInput,
        required: true,
      }),
      el(
        'div',
        { class: 'check-field' },
        enabledInput,
        el(
          'label',
          { for: 'user-enabled' },
          el('span', { class: 'check-title', text: '已启用' }),
          el('span', { class: 'hint', text: '已停用的账户无法通过认证。' }),
        ),
      ),
    );

    let modal = null;

    async function save(controls) {
      controls.clearError();
      const name = nameInput.value.trim();
      if (!name) {
        controls.setError('请输入用户名。');
        return;
      }
      if (!USER_NAME_RE.test(name)) {
        controls.setError(
          '用户名无效。请以小写字母开头，最多 32 个字符：a–z、0–9、点、下划线或短横线。',
        );
        return;
      }
      const passwordError = validatePassword(passwordInput.value, confirmInput.value);
      if (passwordError) {
        controls.setError(passwordError);
        return;
      }
      controls.setBusy(true);
      try {
        await api.createUser({
          name,
          password: passwordInput.value,
          enabled: enabledInput.checked,
        });
        controls.close();
        toast(`用户“${name}”已创建。`, 'success');
        await load();
      } catch (error) {
        if (error && error.status === 401) {
          controls.close();
          return;
        }
        controls.setError(errorMessage(error));
      } finally {
        controls.setBusy(false);
      }
    }

    form.addEventListener('submit', (event) => {
      event.preventDefault();
      if (modal) save(modal);
    });

    modal = openModal({
      title: '创建 SMB 用户',
      content: form,
      actions: [
        { label: '取消', kind: 'ghost', onClick: (controls) => controls.close() },
        { label: '创建用户', kind: 'primary', onClick: (controls) => save(controls) },
      ],
    });
  }

  /* --- Password dialog -------------------------------------------------- */
  function openPasswordDialog(user) {
    const passwordInput = el('input', {
      id: 'user-new-password',
      type: 'password',
      autocomplete: 'new-password',
      maxlength: MAX_SMB_PASSWORD,
    });
    const confirmInput = el('input', {
      id: 'user-new-password-confirm',
      type: 'password',
      autocomplete: 'new-password',
      maxlength: MAX_SMB_PASSWORD,
    });
    const form = el(
      'form',
      { class: 'stack', novalidate: true },
      el('p', {
        class: 'muted small',
        text: `为“${user.name}”设置新的 SMB 密码。现有密码无法查看。`,
      }),
      field({
        id: 'user-new-password',
        label: '新密码',
        control: passwordInput,
        required: true,
        hint: `至少 ${MIN_PASSWORD} 个字符。`,
      }),
      field({
        id: 'user-new-password-confirm',
        label: '确认新密码',
        control: confirmInput,
        required: true,
      }),
    );

    let modal = null;

    async function save(controls) {
      controls.clearError();
      const passwordError = validatePassword(passwordInput.value, confirmInput.value);
      if (passwordError) {
        controls.setError(passwordError);
        return;
      }
      controls.setBusy(true);
      try {
        await api.updateUser(user.name, { password: passwordInput.value });
        controls.close();
        toast(`用户“${user.name}”的密码已更新。`, 'success');
        await load();
      } catch (error) {
        if (error && error.status === 401) {
          controls.close();
          return;
        }
        controls.setError(errorMessage(error));
      } finally {
        controls.setBusy(false);
      }
    }

    form.addEventListener('submit', (event) => {
      event.preventDefault();
      if (modal) save(modal);
    });

    modal = openModal({
      title: '修改 SMB 密码',
      content: form,
      actions: [
        { label: '取消', kind: 'ghost', onClick: (controls) => controls.close() },
        { label: '更新密码', kind: 'primary', onClick: (controls) => save(controls) },
      ],
    });
  }

  await load();
}
