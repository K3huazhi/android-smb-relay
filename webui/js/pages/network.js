// Network: detected interfaces and the auto/specific interface selection.
// The selection is persisted through the `network` field of PUT /api/settings.

import { api, errorMessage } from '../api.js';
import {
  el,
  badge,
  mutedDash,
  toast,
  setButtonBusy,
  emptyState,
  spinner,
} from '../ui.js';

export async function mount(root) {
  let network = {};
  let settings = {};
  let loading = false;

  const refreshBtn = el('button', {
    class: 'btn btn-sm',
    type: 'button',
    text: '刷新',
    onclick: () => load(),
  });
  const body = el('div');

  root.append(
    el(
      'div',
      { class: 'toolbar' },
      el('p', { class: 'muted small', text: '检测到的网络接口与 SMB 接口选择。' }),
      el('div', { class: 'toolbar-actions' }, refreshBtn),
    ),
    body,
  );

  function card(title, ...children) {
    return el(
      'section',
      { class: 'card' },
      el('div', { class: 'card-head' }, el('h2', { class: 'card-title', text: title })),
      ...children,
    );
  }

  function statRow(label, value, mono = false) {
    const valueNode = el('span', { class: `stat-value${mono ? ' mono' : ''}`, text: value || '—' });
    return el(
      'div',
      { class: 'stat' },
      el('span', { class: 'stat-label', text: label }),
      valueNode,
    );
  }

  async function load() {
    if (loading) return;
    loading = true;
    refreshBtn.disabled = true;
    body.replaceChildren(
      el('div', { class: 'loading-block' }, spinner(), el('span', { text: '正在检测接口…' })),
    );
    try {
      const [networkInfo, settingsInfo] = await Promise.all([api.network(), api.settings()]);
      network = networkInfo || {};
      settings = settingsInfo || {};
      render();
    } catch (error) {
      if (error && error.status === 401) return;
      body.replaceChildren(
        emptyState({
          title: '无法加载网络信息',
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

  function render() {
    const ifaces = Array.isArray(network.interfaces) ? network.interfaces : [];
    const netCfg = settings.network || {};
    const selected = network.selected || '';

    /* Current selection card -------------------------------------------- */
    const currentCard = card(
      '当前选择',
      el(
        'div',
        { class: 'row' },
        network.ready ? badge('就绪', 'ok') : badge('未就绪', 'warn'),
        selected ? badge(`已选：${selected}`, 'info') : mutedDash(),
      ),
      el(
        'div',
        { class: 'stat-list' },
        statRow('IPv4', network.ipv4, true),
        statRow('IPv6', Array.isArray(network.ipv6) ? network.ipv6.join(', ') : '', true),
        statRow('网关', network.gateway, true),
        statRow('DNS', Array.isArray(network.dns) ? network.dns.join(', ') : '', true),
        statRow('SMB 绑定接口', settings.smb && settings.smb.interface ? settings.smb.interface : '自动'),
        statRow('网络策略', netCfg.interface === 'auto' ? '自动' : netCfg.interface || '自动'),
      ),
      network.message
        ? el('p', { class: 'hint', text: network.message })
        : null,
    );

    /* Interface table ---------------------------------------------------- */
    const rows = ifaces.map((iface) => {
      const isSelected = iface.name === selected;
      const ipv4 = Array.isArray(iface.ipv4) && iface.ipv4.length > 0 ? iface.ipv4.join(', ') : '';
      const ipv6 = Array.isArray(iface.ipv6) && iface.ipv6.length > 0 ? iface.ipv6.join(', ') : '';
      return el(
        'tr',
        { class: isSelected ? 'is-selected' : '' },
        el(
          'td',
          { dataset: { label: '接口' } },
          el('span', { class: 'cell-title mono', text: iface.name || '—' }),
        ),
        el('td', { dataset: { label: '类型' }, text: iface.type || '—' }),
        el(
          'td',
          { dataset: { label: '连接' } },
          iface.up ? badge('已连接', 'ok') : badge('未连接', 'idle'),
        ),
        el('td', { dataset: { label: 'IPv4' } }, el('span', { class: 'mono', text: ipv4 || '—' })),
        el('td', { dataset: { label: 'IPv6' } }, el('span', { class: 'mono', text: ipv6 || '—' })),
        el('td', { dataset: { label: 'MAC' } }, el('span', { class: 'mono', text: iface.mac || '—' })),
        el('td', { dataset: { label: '网关' } }, el('span', { class: 'mono', text: iface.gateway || '—' })),
        el(
          'td',
          { dataset: { label: '默认' } },
          iface.default ? badge('默认', 'info') : mutedDash(),
        ),
      );
    });

    const ifaceTable =
      ifaces.length === 0
        ? emptyState({
            title: '未检测到接口',
            text: network.message || '守护进程无法枚举网络接口。',
          })
        : el(
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
                  el('th', { text: '接口' }),
                  el('th', { text: '类型' }),
                  el('th', { text: '连接' }),
                  el('th', { text: 'IPv4' }),
                  el('th', { text: 'IPv6' }),
                  el('th', { text: 'MAC' }),
                  el('th', { text: '网关' }),
                  el('th', { text: '默认' }),
                ),
              ),
              el('tbody', {}, rows),
            ),
          );

    /* Selection form ----------------------------------------------------- */
    const configured = netCfg.interface && netCfg.interface !== 'auto' ? netCfg.interface : '';
    const autoRadio = el('input', {
      type: 'radio',
      name: 'iface-mode',
      id: 'iface-mode-auto',
      value: 'auto',
      checked: configured === '',
    });
    const manualRadio = el('input', {
      type: 'radio',
      name: 'iface-mode',
      id: 'iface-mode-manual',
      value: 'manual',
      checked: configured !== '',
    });

    const select = el('select', { id: 'iface-select', 'aria-label': '接口' });
    for (const iface of ifaces) {
      const ipv4 = Array.isArray(iface.ipv4) && iface.ipv4.length > 0 ? iface.ipv4[0] : '无 IPv4';
      select.appendChild(
        el('option', {
          value: iface.name,
          text: `${iface.name}（${iface.type || '其他'}）— ${ipv4}${iface.up ? '' : ' — 未连接'}`,
        }),
      );
    }
    if (configured && !ifaces.some((iface) => iface.name === configured)) {
      select.appendChild(
        el('option', { value: configured, text: `${configured}（未检测到）` }),
      );
    }
    if (configured) select.value = configured;
    select.disabled = configured === '';

    autoRadio.addEventListener('change', () => {
      select.disabled = autoRadio.checked;
    });
    manualRadio.addEventListener('change', () => {
      select.disabled = !manualRadio.checked;
    });

    const allowCellular = el('input', {
      type: 'checkbox',
      id: 'network-allow-cellular',
      checked: Boolean(netCfg.allow_cellular),
    });

    const errorBox = el('div', { class: 'form-error', role: 'alert', hidden: true });
    const saveBtn = el('button', {
      class: 'btn btn-primary',
      type: 'button',
      text: '保存接口设置',
    });

    saveBtn.addEventListener('click', async () => {
      errorBox.hidden = true;
      const useManual = manualRadio.checked;
      if (useManual && !select.value) {
        errorBox.textContent = '请选择一个接口，或切回“自动”。';
        errorBox.hidden = false;
        return;
      }
      const payload = {
        interface: useManual ? select.value : 'auto',
        allow_cellular: allowCellular.checked,
        settle_seconds:
          Number.isFinite(Number(netCfg.settle_seconds)) && Number(netCfg.settle_seconds) >= 0
            ? Number(netCfg.settle_seconds)
            : 5,
      };
      setButtonBusy(saveBtn, true);
      try {
        await api.saveSettings({ network: payload });
        toast(
          payload.interface === 'auto'
            ? '网络选择已设为自动。'
            : `网络接口已设为“${payload.interface}”。`,
          'success',
        );
        await load();
      } catch (error) {
        if (error && error.status === 401) return;
        errorBox.textContent = errorMessage(error);
        errorBox.hidden = false;
      } finally {
        setButtonBusy(saveBtn, false);
      }
    });

    const formCard = card(
      '接口选择',
      el('p', {
        class: 'hint',
        text:
          '自动模式会选择具有私有 IPv4 地址的最佳 Wi-Fi/以太网/USB 接口。' +
          'VPN 接口不会被自动选中。更改此项时，若 SMB 正在运行会重启 SMB。',
      }),
      el(
        'div',
        { class: 'check-field' },
        autoRadio,
        el(
          'label',
          { for: 'iface-mode-auto' },
          el('span', { class: 'check-title', text: '自动' }),
          el('span', { class: 'hint', text: '由守护进程自动选择接口。' }),
        ),
      ),
      el(
        'div',
        { class: 'check-field' },
        manualRadio,
        el(
          'label',
          { for: 'iface-mode-manual' },
          el('span', { class: 'check-title', text: '指定接口' }),
          el('span', { class: 'hint', text: '将 SMB 中继绑定到某一个指定接口。' }),
        ),
      ),
      el('div', { class: 'field' }, el('label', { for: 'iface-select', text: '接口' }), select),
      el(
        'div',
        { class: 'check-field' },
        allowCellular,
        el(
          'label',
          { for: 'network-allow-cellular' },
          el('span', { class: 'check-title', text: '自动模式下允许蜂窝网络' }),
          el('span', {
            class: 'hint',
            text: '默认关闭：移动数据通常按流量计费，且不属于局域网。',
          }),
        ),
      ),
      el('p', {
        class: 'hint',
        text:
          '通过“设置 → 网络”（network.interface）保存。SMB 绑定接口是' +
          '“设置 → SMB”下的独立设置。',
      }),
      errorBox,
      el('div', { class: 'form-actions' }, saveBtn),
    );

    body.replaceChildren(
      el('div', { class: 'grid' }, currentCard),
      el(
        'div',
        { class: 'toolbar' },
        el('h2', { class: 'section-title', text: '接口' }),
        el('span', { class: 'muted small', text: ifaces.length === 1 ? '1 个接口' : `${ifaces.length} 个接口` }),
      ),
      ifaceTable,
      el('div', { class: 'section-gap' }),
      formCard,
    );
  }

  await load();
}
