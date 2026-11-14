// Shares: list, create, edit, delete, and an fs/list-backed directory picker.

import { api, errorMessage } from '../api.js';
import {
  el,
  field,
  icon,
  spinner,
  badge,
  mutedDash,
  toast,
  openModal,
  confirmAction,
  emptyState,
} from '../ui.js';

const SHARE_NAME_RE = /^[A-Za-z0-9][A-Za-z0-9._ -]{0,31}$/;
const CONTROL_CHARS_RE = /[\u0000\r\n]/;

async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast('已复制 SMB 地址。', 'success');
  } catch (error) {
    toast('无法访问剪贴板，请手动选中地址复制。', 'error');
  }
}

export async function mount(root) {
  let shares = [];
  let loading = false;

  const countText = el('p', { class: 'muted small', text: '正在加载共享…' });
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
    text: '新建共享',
    onclick: () => openShareDialog(null),
  });

  root.append(
    el(
      'div',
      { class: 'toolbar' },
      countText,
      el('div', { class: 'toolbar-actions' }, refreshBtn, newBtn),
    ),
    tableHost,
  );

  /* --- Data loading ----------------------------------------------------- */
  async function load() {
    if (loading) return;
    loading = true;
    refreshBtn.disabled = true;
    if (shares.length === 0) {
      tableHost.replaceChildren(
        el('div', { class: 'loading-block' }, spinner(), el('span', { text: '正在加载共享…' })),
      );
    }
    try {
      const list = await api.shares();
      shares = Array.isArray(list) ? list : [];
      renderTable();
    } catch (error) {
      if (error && error.status === 401) return;
      countText.textContent = '共享不可用';
      tableHost.replaceChildren(
        emptyState({
          title: '无法加载共享',
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
      shares.length === 1 ? '已配置 1 个共享' : `已配置 ${shares.length} 个共享`;
    if (shares.length === 0) {
      tableHost.replaceChildren(
        emptyState({
          title: '尚未配置共享',
          text: '通过 SMB 将设备上的目录共享到局域网。',
          actions: [
            el('button', {
              class: 'btn btn-primary',
              type: 'button',
              text: '创建共享',
              onclick: () => openShareDialog(null),
            }),
          ],
        }),
      );
      return;
    }

    const rows = shares.map((share) => {
      const nameCell = el(
        'td',
        { dataset: { label: '名称' } },
        el('div', { class: 'cell-title', text: share.name || '—' }),
        share.comment ? el('div', { class: 'cell-sub', text: share.comment }) : null,
      );

      const addressText = share.unc || '—';
      const addressCell = el(
        'td',
        { dataset: { label: '地址' } },
        el(
          'div',
          { class: 'cell-stack' },
          el('code', { class: 'mono address-code', text: addressText }),
          share.unc
            ? el('button', {
                class: 'btn btn-sm btn-ghost copy-btn',
                type: 'button',
                text: '复制',
                onclick: () => copyText(share.unc),
              })
            : null,
        ),
      );

      const pathBits = [el('code', { class: 'mono', text: share.path || '—' })];
      if (!share.exists) {
        pathBits.push(
          badge('磁盘上不存在', 'danger'),
        );
      }
      const pathCell = el(
        'td',
        { dataset: { label: '路径' } },
        el('div', { class: 'cell-stack' }, pathBits),
      );

      const accessCell = el(
        'td',
        { dataset: { label: '访问权限' } },
        share.read_only ? badge('只读', 'warn') : badge('读写', 'ok'),
      );

      const guestCell = el(
        'td',
        { dataset: { label: '访客' } },
        share.guest_ok ? badge('匿名', 'danger') : badge('需要密码', 'info'),
      );

      const hiddenCell = el(
        'td',
        { dataset: { label: '隐藏' } },
        share.hidden ? badge('隐藏', 'info') : badge('可见', 'idle'),
      );

      const enabledCell = el(
        'td',
        { dataset: { label: '状态' } },
        share.enabled ? badge('已启用', 'ok') : badge('已停用', 'idle'),
      );

      const editBtn = el('button', {
        class: 'btn btn-sm',
        type: 'button',
        text: '编辑',
        onclick: () => openShareDialog(share),
      });
      const deleteBtn = el('button', {
        class: 'btn btn-sm btn-danger',
        type: 'button',
        text: '删除',
        onclick: () => removeShare(share),
      });

      return el(
        'tr',
        { class: share.enabled ? '' : 'row-disabled' },
        nameCell,
        addressCell,
        pathCell,
        accessCell,
        guestCell,
        hiddenCell,
        enabledCell,
        el('td', { class: 'cell-actions', dataset: { label: '操作' } }, editBtn, deleteBtn),
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
              el('th', { text: 'SMB 地址' }),
              el('th', { text: '路径' }),
              el('th', { text: '访问权限' }),
              el('th', { text: '访客' }),
              el('th', { text: '隐藏' }),
              el('th', { text: '状态' }),
              el('th', { text: '操作' }),
            ),
          ),
          el('tbody', {}, rows),
        ),
      ),
    );
  }

  /* --- Delete ----------------------------------------------------------- */
  async function removeShare(share) {
    const confirmed = await confirmAction({
      title: `删除共享“${share.name}”？`,
      message:
        '共享配置将立即删除，磁盘上的文件和文件夹不会删除。',
      details: `路径：${share.path || '—'}`,
      confirmLabel: '删除共享',
      danger: true,
    });
    if (!confirmed) return;
    try {
      await api.deleteShare(share.id);
      toast(`共享“${share.name}”已删除。`, 'success');
      await load();
    } catch (error) {
      if (error && error.status === 401) return;
      toast(errorMessage(error), 'error', 6000);
    }
  }

  /* --- Directory picker ------------------------------------------------- */
  function pickDirectory(initialPath) {
    return new Promise((resolve) => {
      let settled = false;
      let current = null;
      let parent = null;
      let navigation = 0;

      const finish = (value) => {
        if (settled) return;
        settled = true;
        resolve(value);
      };

      const pathLabel = el('code', { class: 'path-label', text: '…' });
      const upBtn = el(
        'button',
        { class: 'btn btn-sm', type: 'button' },
        icon('up', 15),
        el('span', { text: '上级' }),
      );
      const listHost = el('div', { class: 'dir-list' });
      const errorBox = el('div', { class: 'form-error', hidden: true });
      const content = el(
        'div',
        { class: 'stack' },
        errorBox,
        el('div', { class: 'path-bar' }, upBtn, pathLabel),
        listHost,
      );

      const modal = openModal({
        title: '选择目录',
        content,
        size: 'wide',
        onClose: () => finish(null),
        actions: [
          {
            label: '取消',
            kind: 'ghost',
            onClick: (controls) => {
              finish(null);
              controls.close();
            },
          },
          {
            label: '使用此目录',
            kind: 'primary',
            disabled: true,
            onClick: (controls) => {
              if (!current) return;
              finish(current);
              controls.close();
            },
          },
        ],
      });
      const useBtn = modal.buttons[1];

      async function go(path) {
        const sequence = ++navigation;
        upBtn.disabled = true;
        useBtn.disabled = true;
        errorBox.hidden = true;
        listHost.replaceChildren(
          el('div', { class: 'loading-inline' }, spinner(), el('span', { text: '加载中…' })),
        );
        try {
          const result = await api.fsList(path || undefined);
          if (settled || sequence !== navigation) return;
          current = result && result.path ? result.path : null;
          parent = result && result.parent ? result.parent : null;
          pathLabel.textContent = current || '—';
          upBtn.disabled = !parent;
          useBtn.disabled = !current;
          const entries = (result && Array.isArray(result.entries) ? result.entries : []).filter(
            (entry) => entry && entry.is_dir,
          );
          if (entries.length === 0) {
            listHost.replaceChildren(
              el('p', { class: 'dir-empty', text: '没有子目录。' }),
            );
            return;
          }
          listHost.replaceChildren(
            ...entries.map((entry) => {
              const item = el(
                'button',
                { class: 'dir-item', type: 'button' },
                icon('folder', 16),
                el('span', { class: 'dir-name', text: entry.name || entry.path }),
              );
              item.addEventListener('click', () => go(entry.path));
              return item;
            }),
          );
        } catch (error) {
          if (settled || sequence !== navigation) return;
          current = null;
          parent = null;
          useBtn.disabled = true;
          upBtn.disabled = true;
          pathLabel.textContent = path || '—';
          errorBox.textContent = errorMessage(error);
          errorBox.hidden = false;
          listHost.replaceChildren(
            el('p', { class: 'dir-empty', text: '无法列出此目录。' }),
          );
          const fallback = el('button', {
            class: 'btn btn-sm',
            type: 'button',
            text: '浏览存储根目录',
            onclick: () => go(undefined),
          });
          listHost.appendChild(fallback);
        }
      }

      upBtn.addEventListener('click', () => {
        if (parent) go(parent);
      });

      if (initialPath && initialPath.startsWith('/')) {
        go(initialPath);
      } else {
        go(undefined);
      }
    });
  }

  /* --- Create / edit dialog --------------------------------------------- */
  function openShareDialog(share) {
    const isEdit = Boolean(share);
    const dialogTitle = isEdit ? '编辑共享' : '创建共享';

    const nameInput = el('input', {
      id: 'share-name-input',
      type: 'text',
      value: share ? share.name || '' : '',
      maxlength: 32,
      autocomplete: 'off',
      spellcheck: 'false',
      placeholder: '例如 Photos',
    });
    const pathInput = el('input', {
      id: 'share-path-input',
      type: 'text',
      class: 'mono-input',
      value: share ? share.path || '' : '',
      autocomplete: 'off',
      spellcheck: 'false',
      placeholder: '/storage/emulated/0/SMB/Photos',
    });
    const commentInput = el('input', {
      id: 'share-comment-input',
      type: 'text',
      value: share ? share.comment || '' : '',
      maxlength: 255,
      autocomplete: 'off',
      placeholder: '可选说明',
    });

    const browseBtn = el(
      'button',
      { class: 'btn btn-sm', type: 'button' },
      icon('folder', 15),
      el('span', { text: '浏览…' }),
    );
    browseBtn.addEventListener('click', async () => {
      const selected = await pickDirectory(pathInput.value.trim());
      if (selected) pathInput.value = selected;
    });

    function checkbox(id, label, checked, hint) {
      const input = el('input', { type: 'checkbox', id, checked: Boolean(checked) });
      const wrap = el(
        'div',
        { class: 'check-field' },
        input,
        el(
          'label',
          { for: id },
          el('span', { class: 'check-title', text: label }),
          hint ? el('span', { class: 'hint', text: hint }) : null,
        ),
      );
      return { input, wrap };
    }

    const readOnly = checkbox(
      'share-readonly',
      '只读',
      share ? share.read_only : false,
      '客户端可浏览和读取，但不能写入。',
    );
    const guest = checkbox(
      'share-guest',
      '访客访问',
      share ? share.guest_ok : false,
      '允许客户端无需密码使用此共享。',
    );
    const hidden = checkbox(
      'share-hidden',
      '隐藏',
      share ? share.hidden : false,
      '浏览服务器时不会列出此共享。',
    );
    const enabled = checkbox(
      'share-enabled',
      '已启用',
      share ? share.enabled : true,
      '已停用的共享仍保留在配置中，但不会对外提供服务。',
    );

    const guestWarning = el('p', {
      class: 'field-warning',
      text: '局域网内任何人都可无需密码访问此共享。',
      hidden: !guest.input.checked,
    });
    guest.input.addEventListener('change', () => {
      guestWarning.hidden = !guest.input.checked;
    });

    const form = el(
      'form',
      { class: 'stack', novalidate: true },
      field({
        id: 'share-name-input',
        label: '名称',
        control: nameInput,
        required: true,
        hint: '最多 32 个字符：字母、数字、空格、点、下划线或短横线。',
      }),
      field({
        id: 'share-path-input',
        label: '路径',
        control: el('div', { class: 'input-row' }, pathInput, browseBtn),
        required: true,
        hint: '共享存储内的绝对路径。目录不存在时会自动创建。',
      }),
      field({
        id: 'share-comment-input',
        label: '备注',
        control: commentInput,
      }),
      el('div', { class: 'form-grid' }, readOnly.wrap, hidden.wrap, enabled.wrap),
      guest.wrap,
      guestWarning,
    );

    let modal = null;

    function collect() {
      const name = nameInput.value.trim();
      const path = pathInput.value.trim();
      const comment = commentInput.value.trim();
      if (!name) return { error: '请输入共享名称。' };
      if (!SHARE_NAME_RE.test(name)) {
        return {
          error:
            '名称无效。最多 32 个字符，可使用字母、数字、空格、点、下划线或短横线，且需以字母或数字开头。',
        };
      }
      if (!path) return { error: '请输入共享路径。' };
      if (!path.startsWith('/')) return { error: '路径必须是绝对路径（以 / 开头）。' };
      if (CONTROL_CHARS_RE.test(path)) return { error: '路径包含无效字符。' };
      if (CONTROL_CHARS_RE.test(comment)) {
        return { error: '备注包含无效字符。' };
      }
      return {
        payload: {
          name,
          path,
          comment,
          read_only: readOnly.input.checked,
          guest_ok: guest.input.checked,
          hidden: hidden.input.checked,
          enabled: enabled.input.checked,
        },
      };
    }

    async function save(controls) {
      controls.clearError();
      const result = collect();
      if (result.error) {
        controls.setError(result.error);
        return;
      }
      controls.setBusy(true);
      try {
        if (isEdit) {
          await api.updateShare(share.id, result.payload);
        } else {
          await api.createShare(result.payload);
        }
        controls.close();
        toast(
          isEdit
            ? `共享“${result.payload.name}”已更新。`
            : `共享“${result.payload.name}”已创建。`,
          'success',
        );
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
      title: dialogTitle,
      content: form,
      size: 'wide',
      actions: [
        {
          label: '取消',
          kind: 'ghost',
          onClick: (controls) => controls.close(),
        },
        {
          label: isEdit ? '保存更改' : '创建共享',
          kind: 'primary',
          onClick: (controls) => save(controls),
        },
      ],
    });
  }

  await load();
}
