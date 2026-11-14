// Dashboard: live SMB/WebUI/network/system state.
// Polls GET /api/status every 2s while the page is mounted and visible.

import { api, errorMessage } from '../api.js';
import {
  el,
  statusPill,
  fmtBytes,
  fmtRate,
  fmtDuration,
  fmtPercent,
  fmtNumber,
  fmtTime,
  clampPercent,
  setButtonBusy,
  toast,
  badge,
} from '../ui.js';

const POLL_MS = 2000;
const SMB_RUNNING_STATES = new Set(['running', 'starting', 'restarting']);

export async function mount(root) {
  let timer = null;
  let inFlight = false;
  let disposed = false;
  let busyAction = '';
  let lastStatus = null;
  let lastWarningsKey = '';
  let lastIfacesKey = '';

  function card(title, ...children) {
    return el(
      'section',
      { class: 'card' },
      el('div', { class: 'card-head' }, el('h2', { class: 'card-title', text: title })),
      ...children,
    );
  }

  function statRow(label) {
    const value = el('span', { class: 'stat-value', text: '—' });
    const row = el(
      'div',
      { class: 'stat' },
      el('span', { class: 'stat-label', text: label }),
      value,
    );
    return { row, value };
  }

  function setBadge(node, text, kind) {
    node.textContent = text;
    node.className = `badge badge-${kind}`;
  }

  /* --- Connection banner ------------------------------------------------ */
  const connectionBanner = el('div', {
    class: 'notice notice-warn',
    role: 'status',
    text: '实时更新已暂停，正在重试…',
    hidden: true,
  });

  /* --- SMB service card ------------------------------------------------- */
  const serviceState = statusPill('unknown');
  const svcUptime = statRow('运行时长');
  const svcPid = statRow('PID');
  const svcRestarts = statRow('看门狗重启次数');
  const svcInterface = statRow('网络接口');
  const svcSSID = statRow('Wi-Fi 名称');
  const svcAddress = statRow('IP 地址');
  const svcPort = statRow('端口');
  const svcMode = statRow('访问模式');
  const serviceError = el('div', {
    class: 'notice notice-error',
    role: 'alert',
    hidden: true,
  });
  const startBtn = el('button', {
    class: 'btn btn-primary btn-sm',
    type: 'button',
    text: '启动',
    onclick: () => runService('start', startBtn),
  });
  const stopBtn = el('button', {
    class: 'btn btn-sm',
    type: 'button',
    text: '停止',
    onclick: () => runService('stop', stopBtn),
  });
  const restartBtn = el('button', {
    class: 'btn btn-sm',
    type: 'button',
    text: '重启',
    onclick: () => runService('restart', restartBtn),
  });
  const serviceCard = card(
    'SMB 服务',
    el('div', { class: 'row' }, serviceState),
    el(
      'div',
      { class: 'stat-list' },
      svcUptime.row,
      svcPid.row,
      svcRestarts.row,
      svcInterface.row,
      svcSSID.row,
      svcAddress.row,
      svcPort.row,
      svcMode.row,
    ),
    serviceError,
    el('div', { class: 'card-actions' }, startBtn, stopBtn, restartBtn),
  );

  /* --- WebUI card ------------------------------------------------------- */
  const webDot = el('span', { class: 'dot dot-idle', 'aria-hidden': 'true' });
  const webLabel = el('span', { class: 'status-label', text: '未知' });
  const webState = el('span', { class: 'status' }, webDot, webLabel);
  const webEnabled = statRow('已配置');
  const webPort = statRow('端口');
  const webBind = statRow('绑定地址');
  const webUrls = el('div', { class: 'stack-sm' });
  const webError = el('div', {
    class: 'notice notice-error',
    role: 'alert',
    hidden: true,
  });
  const webuiCard = card(
    'WebUI',
    el('div', { class: 'row' }, webState),
    el('div', { class: 'stat-list' }, webEnabled.row, webPort.row, webBind.row),
    el('div', { class: 'meter-block' }, el('div', { class: 'meter-head' }, el('span', { class: 'meter-label', text: '地址' }))),
    webUrls,
    webError,
  );

  /* --- Connect card ------------------------------------------------------ */
  const connectList = el('div', { class: 'connect-list' });
  const connectHint = el('p', {
    class: 'hint',
    text: '默认已启用匿名访问：可直接使用这些路径，无需 SMB 账户和密码。请确保此局域网可信。',
  });
  function copyAddress(text) {
    navigator.clipboard
      .writeText(text)
      .then(() => toast('已复制 SMB 地址。', 'success'))
      .catch(() => toast('无法访问剪贴板，请手动选中地址复制。', 'error'));
  }
  const connectCard = card(
    '从电脑连接',
    connectHint,
    connectList,
    el(
      'div',
      { class: 'card-actions connect-actions' },
      el('a', {
        class: 'btn btn-sm btn-primary',
        href: '/downloads/windows-mount.cmd',
        download: 'windows-mount.cmd',
        text: 'Windows 快速挂载',
      }),
      el('a', {
        class: 'btn btn-sm',
        href: '/downloads/linux-mount.sh',
        download: 'linux-mount.sh',
        text: 'Linux 挂载脚本',
      }),
      el('a', {
        class: 'btn btn-sm',
        href: '/downloads/macos-mount.sh',
        download: 'macos-mount.sh',
        text: 'macOS 挂载脚本',
      }),
      el('a', {
        class: 'btn btn-sm btn-ghost',
        href: '/downloads/windows-allow-guest.ps1',
        download: 'windows-allow-guest.ps1',
        text: 'Windows 访客策略',
      }),
    ),
  );

  /* --- Network card ----------------------------------------------------- */
  const netReady = el('span', { class: 'badge badge-idle', text: '未知' });
  const netSelected = statRow('已选接口');
  const netIPv4 = statRow('IPv4');
  const netGateway = statRow('网关');
  const netDNS = statRow('DNS');
  const netIPv6 = statRow('IPv6');
  const netMessage = el('p', { class: 'hint', hidden: true });
  const ifaceList = el('div', { class: 'iface-list' });
  const networkCard = card(
    '网络',
    el('div', { class: 'row' }, netReady),
    el(
      'div',
      { class: 'stat-list' },
      netSelected.row,
      netIPv4.row,
      netIPv6.row,
      netGateway.row,
      netDNS.row,
    ),
    netMessage,
    el('div', { class: 'meter-block' }, el('div', { class: 'meter-head' }, el('span', { class: 'meter-label', text: '网络接口' }))),
    ifaceList,
  );

  /* --- Traffic card ----------------------------------------------------- */
  const trClients = statRow('活动连接');
  const trSambaProcs = statRow('smbd 进程');
  const trUpRate = statRow('上传速率');
  const trDownRate = statRow('下载速率');
  const trUpTotal = statRow('累计上传');
  const trDownTotal = statRow('累计下载');
  const clientIps = el('div', { class: 'row' });
  const trafficCard = card(
    '流量',
    el(
      'div',
      { class: 'stat-list' },
      trClients.row,
      trSambaProcs.row,
      trUpRate.row,
      trDownRate.row,
      trUpTotal.row,
      trDownTotal.row,
    ),
    el('div', { class: 'meter-block' }, el('div', { class: 'meter-head' }, el('span', { class: 'meter-label', text: '客户端地址' }))),
    clientIps,
  );

  /* --- System card ------------------------------------------------------ */
  const cpuText = el('span', { class: 'meter-value', text: '—' });
  const cpuBar = el('progress', { class: 'meter', max: 100, value: 0 });
  const memText = el('span', { class: 'meter-value', text: '—' });
  const memBar = el('progress', { class: 'meter', max: 100, value: 0 });
  const storePath = el('p', { class: 'hint mono', text: '—' });
  const storeText = el('span', { class: 'meter-value', text: '—' });
  const storeBar = el('progress', { class: 'meter', max: 100, value: 0 });
  const hostUptime = statRow('系统运行时长');
  const daemonUptime = statRow('守护进程运行时长');
  const systemCard = card(
    '系统',
    el(
      'div',
      { class: 'meter-block' },
      el(
        'div',
        { class: 'meter-head' },
        el('span', { class: 'meter-label', text: 'CPU' }),
        cpuText,
      ),
      cpuBar,
    ),
    el(
      'div',
      { class: 'meter-block' },
      el(
        'div',
        { class: 'meter-head' },
        el('span', { class: 'meter-label', text: '内存' }),
        memText,
      ),
      memBar,
    ),
    el(
      'div',
      { class: 'meter-block' },
      el(
        'div',
        { class: 'meter-head' },
        el('span', { class: 'meter-label', text: '存储' }),
        storeText,
      ),
      storeBar,
      storePath,
    ),
    el('div', { class: 'stat-list' }, hostUptime.row, daemonUptime.row),
  );

  /* --- Warnings + footer ------------------------------------------------ */
  const warningsBody = el('div');
  const warningsCard = card('警告', warningsBody);
  const footerLine = el('p', { class: 'small muted', text: '正在加载状态…' });

  root.append(
    connectionBanner,
    warningsCard,
    el('div', { class: 'grid' }, connectCard, serviceCard, webuiCard, networkCard, trafficCard, systemCard),
    footerLine,
  );

  /* --- Rendering -------------------------------------------------------- */
  function applyButtons(state) {
    const busy = busyAction !== '';
    startBtn.disabled = busy || SMB_RUNNING_STATES.has(state);
    stopBtn.disabled = busy || state === 'stopped';
    restartBtn.disabled = busy || state === 'starting' || state === 'restarting';
  }

  function renderWarnings(warnings) {
    const key = JSON.stringify(warnings || []);
    if (key === lastWarningsKey) return;
    lastWarningsKey = key;
    if (!warnings || warnings.length === 0) {
      warningsBody.replaceChildren(
        el(
          'div',
          { class: 'warning-empty' },
          el('span', { class: 'dot dot-ok', 'aria-hidden': 'true' }),
          el('span', { text: '没有警告。' }),
        ),
      );
      return;
    }
    warningsBody.replaceChildren(
      el(
        'ul',
        { class: 'warning-list' },
        warnings.map((warning) => el('li', { text: String(warning) })),
      ),
    );
  }

  function renderConnect(status) {
    const shares = Array.isArray(status.shares) ? status.shares.filter((s) => s.enabled) : [];
    const ip = (status.network && status.network.ipv4) || (status.smb && status.smb.ip) || '';
    if (shares.length === 0) {
      connectList.replaceChildren(
        el('p', { class: 'hint', text: '没有已启用的共享，请在“共享”页面创建。' }),
      );
      return;
    }
    connectList.replaceChildren(
      ...shares.map((share) => {
        const unc = share.unc || (ip ? `\\\\${ip}\\${share.name}` : '—');
        return el(
          'div',
          { class: 'connect-row' },
          el('code', { class: 'mono address-code', text: unc }),
          share.read_only ? badge('只读', 'warn') : badge('读写', 'ok'),
          share.guest_ok ? badge('匿名', 'danger') : badge('需要密码', 'info'),
          el('button', {
            class: 'btn btn-sm btn-ghost',
            type: 'button',
            text: '复制',
            onclick: () => copyAddress(unc),
          }),
        );
      }),
    );
  }

  function renderInterfaces(info) {
    const interfaces = (info && info.interfaces) || [];
    const key = JSON.stringify(interfaces) + '|' + (info ? info.selected : '');
    if (key === lastIfacesKey) return;
    lastIfacesKey = key;
    if (interfaces.length === 0) {
      ifaceList.replaceChildren(
        el('p', { class: 'hint', text: '未检测到网络接口。' }),
      );
      return;
    }
    ifaceList.replaceChildren(
      ...interfaces.map((iface) => {
        const selected = info && iface.name === info.selected;
        const up = Boolean(iface.up);
        const ipv4 = Array.isArray(iface.ipv4) && iface.ipv4.length > 0
          ? iface.ipv4.join(', ')
          : '无 IPv4';
        const tags = [];
        if (iface.default) tags.push('默认');
        if (selected) tags.push('已选');
        if (iface.type) tags.push(iface.type);
        return el(
          'div',
          { class: `iface-row${up ? ' up' : ''}${selected ? ' selected' : ''}` },
          el('span', {
            class: `dot iface-dot ${up ? 'dot-ok' : 'dot-idle'}`,
            'aria-hidden': 'true',
          }),
          el('span', { class: 'iface-name mono', text: iface.name || '—' }),
          el('span', { class: 'iface-tags', text: tags.join(' · ') }),
          el('span', { class: 'iface-ip', text: ipv4 }),
        );
      }),
    );
  }

  function renderClientIps(ips) {
    const list = Array.isArray(ips) ? ips : [];
    if (list.length === 0) {
      clientIps.replaceChildren(el('span', { class: 'muted small', text: '无活动客户端。' }));
      return;
    }
    clientIps.replaceChildren(
      ...list.map((ip) => el('span', { class: 'badge badge-info mono', text: String(ip) })),
    );
  }

  function applyStatus(status) {
    lastStatus = status;
    const smb = status.smb || {};
    const webui = status.webui || {};
    const network = status.network || {};
    const system = status.system || {};
    const cpu = system.cpu || {};
    const memory = system.memory || {};
    const storage = system.storage || {};
    const throughput = system.throughput || {};
    renderConnect(status);

    serviceState.update(smb.state || 'unknown');
    svcUptime.value.textContent = fmtDuration(smb.uptime_s);
    svcPid.value.textContent = smb.pid ? String(smb.pid) : '—';
    svcRestarts.value.textContent = fmtNumber(smb.restarts);
    svcInterface.value.textContent = smb.interface || '—';
    svcSSID.value.textContent = network.ssid || (network.wifi ? '未知' : '—');
    svcSSID.value.classList.toggle('mono', Boolean(network.ssid));
    const policy = network.policy || 'allow';
    svcMode.value.textContent = smb.read_only ? '只读（受保护）' : policy === 'stop' ? '已关闭' : '读写';
    svcMode.value.classList.toggle('warn', Boolean(smb.read_only));
    svcAddress.value.textContent = smb.ip || '—';
    svcAddress.value.classList.add('mono');
    svcPort.value.textContent = smb.port ? String(smb.port) : '—';
    const smbError = smb.last_error || '';
    serviceError.textContent = smbError;
    serviceError.hidden = !smbError;

    if (!webui.enabled) {
      webDot.className = 'dot dot-idle';
      webLabel.textContent = '已停用';
    } else if (webui.running) {
      webDot.className = 'dot dot-ok';
      webLabel.textContent = '运行中';
    } else {
      webDot.className = 'dot dot-danger';
      webLabel.textContent = '已停止';
    }
    webEnabled.value.textContent = webui.enabled ? '已启用' : '已停用';
    webPort.value.textContent = webui.port ? String(webui.port) : '—';
    webBind.value.textContent = webui.bind || '—';
    webUrls.replaceChildren(
      ...(Array.isArray(webui.urls) && webui.urls.length > 0
        ? webui.urls.map((url) => el('code', { class: 'mono', text: String(url) }))
        : [el('span', { class: 'muted small', text: '无监听地址。' })]),
    );
    const webErrorText = webui.error || '';
    webError.textContent = webErrorText;
    webError.hidden = !webErrorText;

    if (network.ready) setBadge(netReady, '就绪', 'ok');
    else setBadge(netReady, '未就绪', 'warn');
    netSelected.value.textContent = network.selected || '—';
    netIPv4.value.textContent = network.ipv4 || '—';
    netIPv4.value.classList.add('mono');
    netIPv6.value.textContent =
      Array.isArray(network.ipv6) && network.ipv6.length > 0
        ? network.ipv6.join(', ')
        : '—';
    netIPv6.value.classList.add('mono');
    netGateway.value.textContent = network.gateway || '—';
    netGateway.value.classList.add('mono');
    netDNS.value.textContent =
      Array.isArray(network.dns) && network.dns.length > 0
        ? network.dns.join(', ')
        : '—';
    netDNS.value.classList.add('mono');
    const networkMessage = network.message || '';
    netMessage.textContent = networkMessage;
    netMessage.hidden = !networkMessage;
    renderInterfaces(network);

    trClients.value.textContent = fmtNumber(system.clients);
    trSambaProcs.value.textContent = fmtNumber(system.samba_processes);
    trUpRate.value.textContent = fmtRate(throughput.upload_bps);
    trDownRate.value.textContent = fmtRate(throughput.download_bps);
    trUpTotal.value.textContent = fmtBytes(throughput.upload_total);
    trDownTotal.value.textContent = fmtBytes(throughput.download_total);
    renderClientIps(system.client_ips);

    const cpuPercent = clampPercent(cpu.percent);
    cpuBar.value = cpuPercent;
    cpuText.textContent = `${fmtPercent(cpu.percent)}${cpu.cores ? ` · ${cpu.cores} 核` : ''}`;

    const memPercent = clampPercent(memory.percent);
    memBar.value = memPercent;
    memText.textContent = memory.total_bytes
      ? `${fmtBytes(memory.used_bytes)} / ${fmtBytes(memory.total_bytes)} (${fmtPercent(memory.percent)})`
      : '不可用';

    const storePercent = clampPercent(storage.percent);
    storeBar.value = storePercent;
    storePath.textContent = storage.path || '—';
    storeText.textContent = storage.total_bytes
      ? `${fmtBytes(storage.used_bytes)} / ${fmtBytes(storage.total_bytes)} (${fmtPercent(storage.percent)})`
      : '不可用';

    hostUptime.value.textContent = fmtDuration(system.uptime_s);
    daemonUptime.value.textContent = fmtDuration(status.uptime_s);

    renderWarnings(status.warnings);
    applyButtons(smb.state || 'unknown');

    const version = status.version ? `asrd ${status.version}` : 'asrd';
    footerLine.textContent = `${version} · 更新于 ${fmtTime(status.time)}`;
  }

  /* --- Polling ---------------------------------------------------------- */
  async function pollOnce() {
    if (inFlight || disposed) return;
    inFlight = true;
    try {
      const status = await api.status();
      if (disposed || !status) return;
      applyStatus(status);
      connectionBanner.hidden = true;
    } catch (error) {
      if (disposed) return;
      if (error && error.status === 401) return;
      connectionBanner.textContent = `实时更新已暂停：${errorMessage(error)}`;
      connectionBanner.hidden = false;
    } finally {
      inFlight = false;
    }
  }

  function schedule(delay = POLL_MS) {
    if (disposed) return;
    if (timer !== null) clearTimeout(timer);
    timer = window.setTimeout(async () => {
      timer = null;
      if (disposed) return;
      if (document.visibilityState === 'visible') {
        await pollOnce();
        schedule();
      }
    }, delay);
  }

  function handleVisibility() {
    if (disposed) return;
    if (document.visibilityState === 'visible') {
      if (timer !== null) {
        clearTimeout(timer);
        timer = null;
      }
      pollOnce().finally(() => {
        if (!disposed) schedule();
      });
    } else if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
  }

  /* --- Service actions -------------------------------------------------- */
  async function runService(kind, button) {
    if (busyAction) return;
    const actions = {
      start: api.serviceStart,
      stop: api.serviceStop,
      restart: api.serviceRestart,
    };
    const action = actions[kind];
    if (!action) return;
    const labels = { start: '启动', stop: '停止', restart: '重启' };
    busyAction = kind;
    setButtonBusy(button, true);
    applyButtons(lastStatus && lastStatus.smb ? lastStatus.smb.state : 'unknown');
    try {
      await action();
      toast(`已请求${labels[kind] || kind} SMB 服务。`, 'success');
    } catch (error) {
      toast(errorMessage(error), 'error', 6000);
    } finally {
      busyAction = '';
      if (!disposed) {
        setButtonBusy(button, false);
        await pollOnce();
        applyButtons(lastStatus && lastStatus.smb ? lastStatus.smb.state : 'unknown');
      }
    }
  }

  document.addEventListener('visibilitychange', handleVisibility);
  await pollOnce();
  schedule();

  return () => {
    disposed = true;
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
    document.removeEventListener('visibilitychange', handleVisibility);
  };
}
