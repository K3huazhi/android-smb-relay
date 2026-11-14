// Settings: SMB, WebUI, Startup, Storage and Log sections plus the WebUI
// password change. Saving uses PUT /api/settings with complete section objects
// so fields that are not shown in the UI (force_user, network policy) are
// preserved.

import { api, errorMessage } from '../api.js';
import {
  el,
  field,
  toast,
  openModal,
  confirmAction,
  setFormError,
  setButtonBusy,
  renderLoading,
  renderPageError,
} from '../ui.js';

const PROTOCOLS = [
  'SMB2_02',
  'SMB2_10',
  'SMB2_11',
  'SMB3_00',
  'SMB3_02',
  'SMB3_10',
  'SMB3_11',
];
const SIGNING = [
  { value: 'mandatory', label: '强制' },
  { value: 'default', label: '默认' },
  { value: 'off', label: '关闭' },
];
const ENCRYPTION = [
  { value: 'off', label: '关闭' },
  { value: 'desired', label: '期望' },
  { value: 'required', label: '必需' },
];
const LOG_LEVELS = [
  { value: 'debug', label: '调试' },
  { value: 'info', label: '信息' },
  { value: 'warn', label: '警告' },
  { value: 'error', label: '错误' },
];
const CONTROL_CHARS_RE = /[\u0000\r\n]/;
const NETBIOS_RE = /^[A-Za-z0-9-]{1,15}$/;

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function readInt(input) {
  const raw = String(input.value || '').trim();
  if (!/^-?\d+$/.test(raw)) return NaN;
  return Number(raw);
}

function validBind(value) {
  const bind = String(value || '').trim();
  if (bind === '0.0.0.0' || bind === '::' || bind === '127.0.0.1' || bind === '::1') {
    return true;
  }
  if (bind.includes(':')) return true; // IPv6 literal; the daemon validates the rest.
  const parts = bind.split('.');
  if (parts.length !== 4) return false;
  return parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255);
}

export async function mount(root, ctx) {
  let cfg = null;
  let net = null;
  let f = {};
  let dirty = false;
  let saving = false;
  let loading = false;
  let formHost = null;
  let warningsHost = null;
  let pageError = null;
  let statusText = null;
  let saveTop = null;
  let saveBottom = null;
  let reloadBtn = null;

  function buildShell() {
    statusText = el('p', { class: 'muted small', text: '正在加载设置…' });
    saveTop = el('button', {
      class: 'btn btn-primary btn-sm',
      type: 'button',
      text: '保存更改',
      onclick: () => save(),
    });
    reloadBtn = el('button', {
      class: 'btn btn-sm',
      type: 'button',
      text: '重新加载',
      onclick: () => reload(),
    });
    saveBottom = el('button', {
      class: 'btn btn-primary',
      type: 'button',
      text: '保存更改',
      onclick: () => save(),
    });
    pageError = el('div', { class: 'form-error', role: 'alert', hidden: true });
    warningsHost = el('div', { class: 'danger-panel' });
    formHost = el('div');
    formHost.addEventListener('input', () => {
      dirty = true;
      updateWarnings();
    });
    formHost.addEventListener('change', () => {
      dirty = true;
      updateWarnings();
    });

    root.replaceChildren(
      el(
        'div',
        { class: 'toolbar' },
        statusText,
        el('div', { class: 'toolbar-actions' }, reloadBtn, saveTop),
      ),
      pageError,
      warningsHost,
      formHost,
      el('div', { class: 'form-actions' }, saveBottom),
    );
  }

  function section(title, description, ...children) {
    return el(
      'section',
      { class: 'card settings-section' },
      el('div', { class: 'card-head' }, el('h2', { class: 'card-title', text: title })),
      el('p', { class: 'hint', text: description }),
      ...children,
    );
  }

  function textField({ id, label, value, hint, placeholder = '' }) {
    const input = el('input', {
      id,
      type: 'text',
      value: value === undefined || value === null ? '' : String(value),
      autocomplete: 'off',
      spellcheck: 'false',
      placeholder,
    });
    return { input, wrap: field({ id, label, control: input, hint }) };
  }

  function numberField({ id, label, value, min, max, hint }) {
    const input = el('input', {
      id,
      type: 'number',
      inputmode: 'numeric',
      min: String(min),
      max: String(max),
      step: '1',
      value: value === undefined || value === null ? '' : String(value),
    });
    return { input, wrap: field({ id, label, control: input, hint }) };
  }

  function selectField({ id, label, options, value, hint }) {
    const select = el('select', { id });
    for (const option of options) {
      select.appendChild(
        el('option', { value: option.value, text: option.label }),
      );
    }
    select.value = String(value);
    return { input: select, wrap: field({ id, label, control: select, hint }) };
  }

  function checkField({ id, label, checked, hint }) {
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

  function renderForm() {
    const smb = cfg.smb || {};
    const webui = cfg.webui || {};
    const startup = cfg.startup || {};
    const storage = cfg.storage || {};
    const log = cfg.log || {};

    const ifaceOptions = [{ value: 'auto', label: '自动' }];
    const ifaces = (net && Array.isArray(net.interfaces) && net.interfaces) || [];
    for (const iface of ifaces) {
      ifaceOptions.push({
        value: iface.name,
        label: `${iface.name}（${iface.type || '其他'}）`,
      });
    }
    if (
      smb.interface &&
      smb.interface !== 'auto' &&
      !ifaces.some((iface) => iface.name === smb.interface)
    ) {
      ifaceOptions.push({ value: smb.interface, label: `${smb.interface}（已配置）` });
    }

    f = {};

    /* SMB ---------------------------------------------------------------- */
    f.smbEnabled = checkField({
      id: 'smb-enabled',
      label: '启用 SMB 服务',
      checked: smb.enabled,
      hint: '停用后将立即停止 smbd。',
    });
    f.smbPort = numberField({
      id: 'smb-port',
      label: '端口',
      value: smb.port,
      min: 1,
      max: 65535,
      hint: '需要重启，且必须与 WebUI 端口不同。',
    });
    f.smbMin = selectField({
      id: 'smb-min-protocol',
      label: '最低协议',
      options: PROTOCOLS.map((value) => ({ value, label: value })),
      value: smb.min_protocol,
      hint: '需要重启。',
    });
    f.smbMax = selectField({
      id: 'smb-max-protocol',
      label: '最高协议',
      options: PROTOCOLS.map((value) => ({ value, label: value })),
      value: smb.max_protocol,
      hint: '需要重启。',
    });
    f.smbSigning = selectField({
      id: 'smb-signing',
      label: '签名',
      options: SIGNING,
      value: smb.signing,
      hint: '需要重启。默认“关闭”表示不强制也不启用签名；需要更高安全性时改为“强制”。',
    });
    f.smbEncryption = selectField({
      id: 'smb-encryption',
      label: '加密',
      options: ENCRYPTION,
      value: smb.encryption,
      hint: '需要重启。“关闭”将禁用传输加密。',
    });
    f.smbNetbios = textField({
      id: 'smb-netbios',
      label: 'NetBIOS 名称',
      value: smb.netbios_name,
      hint: '1–15 个字母、数字或短横线。需要重启。',
    });
    f.smbNetbios.input.maxLength = 15;
    f.smbWorkgroup = textField({
      id: 'smb-workgroup',
      label: '工作组',
      value: smb.workgroup,
      hint: '最多 15 个字符。需要重启。',
    });
    f.smbWorkgroup.input.maxLength = 15;
    f.smbInterface = selectField({
      id: 'smb-interface',
      label: 'SMB 绑定接口',
      options: ifaceOptions,
      value: smb.interface || 'auto',
      hint: '需要重启。网络选择在“网络”页面配置。',
    });
    f.smbGuest = checkField({
      id: 'smb-guest',
      label: '访客访问',
      checked: smb.guest_access,
      hint: '局域网内任何人都可无需密码使用访客共享。',
    });
    f.smbAnon = checkField({
      id: 'smb-anonymous',
      label: '允许匿名访问',
      checked: smb.allow_anonymous,
      hint: '同时允许未认证访问。',
    });

    const smbSection = section(
      'SMB',
      'Samba 服务配置。标记为“需要重启”的设置在保存后会自动重启 smbd。',
      el(
        'div',
        { class: 'form-grid' },
        f.smbEnabled.wrap,
        f.smbPort.wrap,
        f.smbMin.wrap,
        f.smbMax.wrap,
        f.smbSigning.wrap,
        f.smbEncryption.wrap,
        f.smbNetbios.wrap,
        f.smbWorkgroup.wrap,
        f.smbInterface.wrap,
        f.smbGuest.wrap,
        f.smbAnon.wrap,
      ),
    );

    /* WebUI -------------------------------------------------------------- */
    f.webuiEnabled = checkField({
      id: 'webui-enabled',
      label: '启用 WebUI',
      checked: webui.enabled,
      hint: '停用后保存，此页面将无法访问。',
    });
    f.webuiPort = numberField({
      id: 'webui-port',
      label: '端口',
      value: webui.port,
      min: 1,
      max: 65535,
      hint: '必须与 SMB 端口不同。应用新端口会断开此页面。',
    });
    f.webuiBind = textField({
      id: 'webui-bind',
      label: '绑定地址',
      value: webui.bind,
      hint: '0.0.0.0 或 :: 监听所有接口；127.0.0.1 仅限本机。',
    });
    f.webuiAuth = checkField({
      id: 'webui-auth',
      label: '需要认证',
      checked: webui.auth,
      hint: '关闭后，任何能访问该端口的人都拥有完全控制权。',
    });
    f.webuiLanOnly = checkField({
      id: 'webui-lan-only',
      label: '仅限局域网',
      checked: webui.lan_only,
      hint: '拒绝非私有地址的客户端。',
    });
    f.webuiSession = numberField({
      id: 'webui-session-hours',
      label: '会话有效期（小时）',
      value: webui.session_hours,
      min: 1,
      max: 720,
      hint: '守护进程重启后生效。',
    });

    const webuiSection = section(
      'WebUI',
      '此界面的监听与会话设置。',
      el(
        'div',
        { class: 'form-grid' },
        f.webuiEnabled.wrap,
        f.webuiPort.wrap,
        f.webuiBind.wrap,
        f.webuiSession.wrap,
        f.webuiAuth.wrap,
        f.webuiLanOnly.wrap,
      ),
    );

    /* Startup ------------------------------------------------------------ */
    f.startOnBoot = checkField({
      id: 'startup-start-on-boot',
      label: '开机启动',
      checked: startup.start_on_boot,
      hint: '下次开机时由模块启动脚本使用。',
    });
    f.startWebUIOnBoot = checkField({
      id: 'startup-webui-on-boot',
      label: '开机启动 WebUI',
      checked: startup.start_webui_on_boot !== false,
      hint: '关闭时，守护进程启动后不会监听 HTTP，直到重新启用。',
    });
    f.waitNetwork = checkField({
      id: 'startup-wait-network',
      label: '等待网络',
      checked: startup.wait_for_network,
      hint: '在局域网接口就绪前不启动 SMB。',
    });
    f.waitStorage = checkField({
      id: 'startup-wait-storage',
      label: '等待存储',
      checked: startup.wait_for_storage,
      hint: '在存储根目录可用前不启动 SMB。',
    });
    f.watchdog = checkField({
      id: 'startup-watchdog',
      label: '看门狗',
      checked: startup.watchdog,
      hint: 'smbd 意外退出后自动重启。',
    });
    f.watchdogMax = numberField({
      id: 'startup-watchdog-max',
      label: '最大重启次数',
      value: startup.watchdog_max_restarts,
      min: 1,
      max: 100,
      hint: '看门狗窗口内允许的重启次数。',
    });
    f.watchdogWindow = numberField({
      id: 'startup-watchdog-window',
      label: '看门狗窗口（分钟）',
      value: startup.watchdog_window_minutes,
      min: 1,
      max: 1440,
      hint: '稳定运行超过该时长后，重启计数将重置。',
    });

    const startupSection = section(
      '启动',
      '开机、就绪检查与看门狗行为。',
      el(
        'div',
        { class: 'form-grid' },
        f.startOnBoot.wrap,
        f.startWebUIOnBoot.wrap,
        f.waitNetwork.wrap,
        f.waitStorage.wrap,
        f.watchdog.wrap,
        f.watchdogMax.wrap,
        f.watchdogWindow.wrap,
      ),
    );

    /* Storage ------------------------------------------------------------ */
    f.storageRoot = textField({
      id: 'storage-root',
      label: '存储根目录',
      value: storage.root,
      hint: '绝对路径。修改后会重新映射所有共享并重启 SMB。',
    });
    f.storageAutoCreate = checkField({
      id: 'storage-auto-create',
      label: '自动创建缺失目录',
      checked: storage.auto_create,
      hint: '存储根目录和共享目录不存在时自动创建。',
    });
    f.storageDataMedia = checkField({
      id: 'storage-use-data-media',
      label: '使用 /data/media/0 回退',
      checked: storage.use_data_media,
      hint: '当 root 无法使用 FUSE 视图时，改用原始挂载点。',
    });

    const storageSection = section(
      '存储',
      '共享在设备上的存放位置，仅支持共享存储根目录。',
      el(
        'div',
        { class: 'form-grid' },
        f.storageRoot.wrap,
        f.storageAutoCreate.wrap,
        f.storageDataMedia.wrap,
      ),
    );

    /* Log ---------------------------------------------------------------- */
    f.logLevel = selectField({
      id: 'log-level',
      label: '日志级别',
      options: LOG_LEVELS,
      value: log.level,
      hint: '守护进程重启后生效。',
    });
    f.logSize = numberField({
      id: 'log-max-size',
      label: '最大文件大小（KiB）',
      value: log.max_size_kb,
      min: 64,
      max: 65536,
      hint: '日志文件超过此大小时轮转。需要重启守护进程。',
    });
    f.logFiles = numberField({
      id: 'log-max-files',
      label: '保留的轮转文件数',
      value: log.max_files,
      min: 1,
      max: 10,
      hint: '需要重启守护进程。',
    });

    const logSection = section(
      '日志',
      'asrd 与 Samba 日志轮转。',
      el('div', { class: 'form-grid' }, f.logLevel.wrap, f.logSize.wrap, f.logFiles.wrap),
    );

    /* Password ----------------------------------------------------------- */
    const pwCurrent = el('input', {
      id: 'webui-password-current',
      type: 'password',
      autocomplete: 'current-password',
    });
    const pwNew = el('input', {
      id: 'webui-password-new',
      type: 'password',
      autocomplete: 'new-password',
      maxlength: 256,
    });
    const pwConfirm = el('input', {
      id: 'webui-password-confirm',
      type: 'password',
      autocomplete: 'new-password',
      maxlength: 256,
    });
    const pwError = el('div', { class: 'form-error', role: 'alert', hidden: true });
    const pwBtn = el('button', {
      class: 'btn btn-primary',
      type: 'button',
      text: '修改 WebUI 密码',
      onclick: () => changePassword(),
    });
    const pwForm = el(
      'form',
      { class: 'pw-form', novalidate: true },
      field({
        id: 'webui-password-current',
        label: '当前密码',
        control: pwCurrent,
        required: true,
      }),
      field({
        id: 'webui-password-new',
        label: '新密码',
        control: pwNew,
        required: true,
        hint: '至少 10 个字符，之后不再显示。',
      }),
      field({
        id: 'webui-password-confirm',
        label: '确认新密码',
        control: pwConfirm,
        required: true,
      }),
      pwError,
      el(
        'div',
        { class: 'form-actions' },
        pwBtn,
      ),
    );
    pwForm.addEventListener('submit', (event) => {
      event.preventDefault();
      changePassword();
    });
    const passwordCard = section(
      'WebUI 密码',
      '修改密码会使所有会话（包括当前会话）退出登录。系统会校验当前密码，新密码不会被回显。',
      pwForm,
    );

    formHost.replaceChildren(
      el(
        'div',
        { class: 'settings-grid' },
        smbSection,
        webuiSection,
        startupSection,
        storageSection,
        logSection,
      ),
      passwordCard,
    );

    async function changePassword() {
      setFormError(pwError, '');
      const current = pwCurrent.value;
      const next = pwNew.value;
      const confirmed = pwConfirm.value;
      if (!current) {
        setFormError(pwError, '请输入当前的 WebUI 密码。');
        return;
      }
      if (next.length < 10) {
        setFormError(pwError, '新密码至少需要 10 个字符。');
        return;
      }
      if (next.length > 256) {
        setFormError(pwError, '新密码最多 256 个字符。');
        return;
      }
      if (CONTROL_CHARS_RE.test(next)) {
        setFormError(pwError, '新密码包含无效字符。');
        return;
      }
      if (next !== confirmed) {
        setFormError(pwError, '两次输入的新密码不一致。');
        return;
      }
      setButtonBusy(pwBtn, true);
      try {
        await api.changePassword(current, next);
        pwCurrent.value = '';
        pwNew.value = '';
        pwConfirm.value = '';
        toast('WebUI 密码已修改，请重新登录。', 'success');
        if (ctx && typeof ctx.relogin === 'function') {
          ctx.relogin('WebUI 密码已修改，请重新登录。');
        } else {
          window.location.reload();
        }
      } catch (error) {
        if (error && error.status === 401) return;
        setFormError(pwError, errorMessage(error));
      } finally {
        setButtonBusy(pwBtn, false);
      }
    }

    dirty = false;
    updateWarnings();
  }

  /* --- Collect / validate ------------------------------------------------ */
  function collect() {
    return {
      smb: {
        enabled: f.smbEnabled.input.checked,
        port: readInt(f.smbPort.input),
        min_protocol: f.smbMin.input.value,
        max_protocol: f.smbMax.input.value,
        signing: f.smbSigning.input.value,
        encryption: f.smbEncryption.input.value,
        guest_access: f.smbGuest.input.checked,
        allow_anonymous: f.smbAnon.input.checked,
        netbios_name: f.smbNetbios.input.value.trim(),
        workgroup: f.smbWorkgroup.input.value.trim(),
        interface: f.smbInterface.input.value || 'auto',
        force_user: (cfg.smb && cfg.smb.force_user) || 'root',
      },
      webui: {
        enabled: f.webuiEnabled.input.checked,
        port: readInt(f.webuiPort.input),
        bind: f.webuiBind.input.value.trim(),
        auth: f.webuiAuth.input.checked,
        lan_only: f.webuiLanOnly.input.checked,
        session_hours: readInt(f.webuiSession.input),
      },
      network: {
        interface: (cfg.network && cfg.network.interface) || 'auto',
        allow_cellular: Boolean(cfg.network && cfg.network.allow_cellular),
        settle_seconds:
          cfg.network && Number.isFinite(Number(cfg.network.settle_seconds))
            ? Number(cfg.network.settle_seconds)
            : 5,
      },
      startup: {
        start_on_boot: f.startOnBoot.input.checked,
        start_webui_on_boot: f.startWebUIOnBoot.input.checked,
        wait_for_network: f.waitNetwork.input.checked,
        wait_for_storage: f.waitStorage.input.checked,
        watchdog: f.watchdog.input.checked,
        watchdog_max_restarts: readInt(f.watchdogMax.input),
        watchdog_window_minutes: readInt(f.watchdogWindow.input),
      },
      storage: {
        root: f.storageRoot.input.value.trim(),
        auto_create: f.storageAutoCreate.input.checked,
        use_data_media: f.storageDataMedia.input.checked,
      },
      log: {
        level: f.logLevel.input.value,
        max_size_kb: readInt(f.logSize.input),
        max_files: readInt(f.logFiles.input),
      },
    };
  }

  function validate(settings) {
    const errors = [];
    const intInRange = (value, min, max, label) => {
      if (!Number.isInteger(value) || value < min || value > max) {
        errors.push(`${label}必须是 ${min} 到 ${max} 之间的整数。`);
      }
    };
    intInRange(settings.smb.port, 1, 65535, 'SMB 端口');
    intInRange(settings.webui.port, 1, 65535, 'WebUI 端口');
    if (
      Number.isInteger(settings.smb.port) &&
      Number.isInteger(settings.webui.port) &&
      settings.smb.port === settings.webui.port
    ) {
      errors.push('SMB 与 WebUI 端口必须不同。');
    }
    const rank = (protocol) => PROTOCOLS.indexOf(protocol);
    if (rank(settings.smb.min_protocol) < 0) {
      errors.push('请选择有效的最低 SMB 协议。');
    } else if (rank(settings.smb.max_protocol) < 0) {
      errors.push('请选择有效的最高 SMB 协议。');
    } else if (rank(settings.smb.min_protocol) > rank(settings.smb.max_protocol)) {
      errors.push('最低 SMB 协议不能高于最高协议。');
    }
    if (
      settings.smb.netbios_name &&
      !NETBIOS_RE.test(settings.smb.netbios_name)
    ) {
      errors.push('NetBIOS 名称必须为 1–15 个字母、数字或短横线。');
    }
    if (settings.smb.workgroup.length > 15) {
      errors.push('工作组最多 15 个字符。');
    }
    if (CONTROL_CHARS_RE.test(settings.smb.workgroup)) {
      errors.push('工作组包含无效字符。');
    }
    if (!validBind(settings.webui.bind)) {
      errors.push('WebUI 绑定地址必须是 IP 地址（0.0.0.0、127.0.0.1 或具体地址）。');
    }
    intInRange(settings.webui.session_hours, 1, 720, '会话有效期');
    intInRange(settings.startup.watchdog_max_restarts, 1, 100, '最大看门狗重启次数');
    intInRange(settings.startup.watchdog_window_minutes, 1, 1440, '看门狗窗口');
    if (!settings.storage.root.startsWith('/')) {
      errors.push('存储根目录必须为绝对路径。');
    }
    if (CONTROL_CHARS_RE.test(settings.storage.root)) {
      errors.push('存储根目录包含无效字符。');
    }
    if (!LOG_LEVELS.some((level) => level.value === settings.log.level)) {
      errors.push('请选择有效的日志级别。');
    }
    intInRange(settings.log.max_size_kb, 64, 65536, '日志大小上限');
    intInRange(settings.log.max_files, 1, 10, '轮转日志文件数');
    return errors;
  }

  /* --- Live danger warnings --------------------------------------------- */
  function updateWarnings() {
    if (!warningsHost || !f.smbPort) return;
    const settings = collect();
    const warnings = [];
    if (!settings.smb.enabled) {
      warnings.push({
        kind: 'warn',
        text: 'SMB 已停用，共享将不会对外提供。',
      });
    }
    if (settings.smb.guest_access) {
      warnings.push({
        kind: 'error',
        text: '已启用访客访问：局域网内任何人都可无需密码访问共享。',
      });
    }
    if (settings.smb.allow_anonymous) {
      warnings.push({ kind: 'error', text: '已启用匿名访问。' });
    }
    if (!settings.webui.auth) {
      warnings.push({
        kind: 'error',
        text: 'WebUI 认证已关闭：任何能访问该端口的人都拥有完全控制权。',
      });
    }
    if (
      settings.webui.bind === '0.0.0.0' ||
      settings.webui.bind === '::'
    ) {
      if (settings.webui.lan_only) {
        warnings.push({
          kind: 'warn',
          text: 'WebUI 监听所有接口，已启用仅限局域网过滤。',
        });
      } else {
        warnings.push({
          kind: 'error',
          text: 'WebUI 监听所有接口，且未启用仅限局域网过滤。',
        });
      }
    }
    if (settings.smb.signing === 'off') {
      warnings.push({ kind: 'warn', text: 'SMB 签名已关闭。' });
    }
    if (settings.smb.encryption === 'off') {
      warnings.push({
        kind: 'warn',
        text: 'SMB 传输加密已关闭（仍可强制签名）。',
      });
    }
    if (!settings.webui.enabled) {
      warnings.push({
        kind: 'warn',
        text: 'WebUI 已停用：保存后此页面将无法访问。',
      });
    }
    warningsHost.replaceChildren(
      ...warnings.map((warning) =>
        el('div', {
          class: warning.kind === 'error' ? 'notice notice-error' : 'notice notice-warn',
          text: warning.text,
        }),
      ),
    );
  }

  /* --- Restart hints ---------------------------------------------------- */
  function diffHints(previous, next) {
    const smbRestart = [];
    const daemonRestart = [];
    const webuiLive = [];
    const smbFields = [
      ['port', 'SMB 端口'],
      ['min_protocol', '最低协议'],
      ['max_protocol', '最高协议'],
      ['signing', '签名'],
      ['encryption', '加密'],
      ['interface', 'SMB 接口'],
      ['force_user', '强制用户'],
      ['netbios_name', 'NetBIOS 名称'],
      ['workgroup', '工作组'],
      ['guest_access', '访客访问'],
      ['allow_anonymous', '匿名访问'],
    ];
    for (const [key, label] of smbFields) {
      if (previous.smb[key] !== next.smb[key]) smbRestart.push(label);
    }
    if (previous.network.interface !== next.network.interface) {
      smbRestart.push('网络接口');
    }
    if (previous.network.allow_cellular !== next.network.allow_cellular) {
      smbRestart.push('蜂窝网络策略');
    }
    if (previous.storage.root !== next.storage.root) {
      smbRestart.push('存储根目录');
    }
    if (previous.storage.use_data_media !== next.storage.use_data_media) {
      smbRestart.push('数据媒体映射');
    }

    if (previous.webui.session_hours !== next.webui.session_hours) {
      daemonRestart.push('WebUI 会话有效期');
    }
    if (previous.startup.start_on_boot !== next.startup.start_on_boot) {
      daemonRestart.push('开机启动');
    }
    if (previous.log.level !== next.log.level) daemonRestart.push('日志级别');
    if (previous.log.max_size_kb !== next.log.max_size_kb) {
      daemonRestart.push('日志大小上限');
    }
    if (previous.log.max_files !== next.log.max_files) {
      daemonRestart.push('日志文件数');
    }

    const webuiFields = [
      ['enabled', 'WebUI 启用状态'],
      ['port', 'WebUI 端口'],
      ['bind', 'WebUI 绑定地址'],
      ['auth', 'WebUI 认证'],
      ['lan_only', '仅限局域网过滤'],
    ];
    for (const [key, label] of webuiFields) {
      if (previous.webui[key] !== next.webui[key]) webuiLive.push(label);
    }

    return {
      smbRestart,
      daemonRestart,
      webuiLive,
      smbEnabledChanged: previous.smb.enabled !== next.smb.enabled,
    };
  }

  function showSaveSummary(hints, webuiChanged) {
    const content = el('div', { class: 'stack' });
    content.appendChild(
      el('p', {
        class: 'modal-message',
        text: '配置已保存，并已通过 asrd 校验。',
      }),
    );

    const addGroup = (title, items) => {
      if (items.length === 0) return;
      content.appendChild(el('p', { class: 'modal-subtitle', text: title }));
      content.appendChild(
        el(
          'ul',
          { class: 'modal-list' },
          items.map((item) => el('li', { text: item })),
        ),
      );
    };

    addGroup('已自动重启 SMB 以应用', hints.smbRestart);
    if (hints.smbEnabledChanged) {
      addGroup('SMB 服务状态已变更', ['服务将相应地启动或停止']);
    }
    addGroup('守护进程重启后生效', hints.daemonRestart);
    addGroup('WebUI 监听已变更', hints.webuiLive);

    if (webuiChanged) {
      content.appendChild(
        el('div', {
          class: 'notice notice-warn',
          text:
            'WebUI 监听已重新配置。如果此页面停止响应，请通过新地址或端口重新打开 WebUI。',
        }),
      );
    }
    if (hints.smbRestart.length === 0 && hints.daemonRestart.length === 0 && !webuiChanged) {
      content.appendChild(
        el('p', {
          class: 'hint',
          text: '这些更改无需重启。',
        }),
      );
    }

    openModal({
      title: '设置已保存',
      content,
      actions: [
        {
          label: '确定',
          kind: 'primary',
          onClick: (controls) => controls.close(),
        },
      ],
    });
  }

  /* --- Save / reload ---------------------------------------------------- */
  async function save() {
    if (saving || loading || !cfg) return;
    setFormError(pageError, '');
    const next = collect();
    const errors = validate(next);
    if (errors.length > 0) {
      setFormError(pageError, errors.join(' '));
      if (pageError.scrollIntoView) pageError.scrollIntoView({ block: 'center' });
      return;
    }
    const webuiChanged = ['enabled', 'port', 'bind', 'auth', 'lan_only'].some(
      (key) => cfg.webui[key] !== next.webui[key],
    );
    if (webuiChanged) {
      const confirmed = await confirmAction({
        title: '应用 WebUI 监听更改？',
        message:
          '修改 WebUI 监听或认证设置会立即生效，并可能断开此页面。' +
          '如果端口或绑定地址发生变化，请通过新地址重新打开 WebUI。',
        details: next.webui.enabled
          ? '认证更改会立即对新请求生效。'
          : 'WebUI 将被停用，此页面将停止响应。',
        confirmLabel: '保存并应用',
        danger: !next.webui.enabled || !next.webui.auth,
      });
      if (!confirmed) return;
    }

    const hints = diffHints(cfg, next);
    saving = true;
    setButtonBusy(saveTop, true);
    setButtonBusy(saveBottom, true);
    reloadBtn.disabled = true;
    try {
      await api.saveSettings(next);
      cfg = clone(next);
      dirty = false;
      statusText.textContent = '所有更改已保存。';
      toast('设置已保存。', 'success');
      updateWarnings();
      showSaveSummary(hints, webuiChanged);
    } catch (error) {
      if (error && error.status === 401) return;
      setFormError(pageError, errorMessage(error));
    } finally {
      saving = false;
      setButtonBusy(saveTop, false);
      setButtonBusy(saveBottom, false);
      reloadBtn.disabled = false;
    }
  }

  async function reload() {
    if (saving || loading) return;
    if (dirty) {
      const confirmed = await confirmAction({
        title: '放弃未保存的更改？',
        message: '要从守护进程重新加载上次保存的配置吗？',
        confirmLabel: '放弃并重新加载',
        danger: true,
      });
      if (!confirmed) return;
    }
    await loadSettings(true);
  }

  async function loadSettings(showLoading) {
    loading = true;
    if (showLoading) {
      renderLoading(formHost, '正在加载设置…');
      warningsHost.replaceChildren();
    }
    statusText.textContent = '正在加载设置…';
    try {
      const [settings, networkInfo] = await Promise.all([
        api.settings(),
        api.network().catch(() => null),
      ]);
      cfg = clone(settings);
      net = networkInfo;
      renderForm();
      statusText.textContent = '正在编辑已保存的配置。';
    } catch (error) {
      if (showLoading) {
        renderPageError(root, errorMessage(error), () => {
          if (ctx && typeof ctx.refreshPage === 'function') ctx.refreshPage();
          else window.location.reload();
        });
      } else {
        setFormError(pageError, errorMessage(error));
      }
    } finally {
      loading = false;
    }
  }

  function handleBeforeUnload(event) {
    if (!dirty) return undefined;
    event.preventDefault();
    event.returnValue = '';
    return '';
  }
  window.addEventListener('beforeunload', handleBeforeUnload);

  buildShell();
  await loadSettings(true);

  return () => {
    window.removeEventListener('beforeunload', handleBeforeUnload);
  };
}
