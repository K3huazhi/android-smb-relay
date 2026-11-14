// Connect: copy-ready SMB addresses and client-specific quick mount steps.

import { api, errorMessage } from '../api.js';
import { el, badge, spinner, toast } from '../ui.js';

export async function mount(root) {
  const host = el('div', { class: 'stack' });
  root.append(host);
  host.replaceChildren(
    el('div', { class: 'loading-block' }, spinner(), el('span', { text: '正在加载连接信息…' })),
  );

  let status;
  try {
    status = await api.status();
  } catch (error) {
    host.replaceChildren(
      el('p', { class: 'notice notice-error', text: errorMessage(error) }),
    );
    return;
  }

  const network = status.network || {};
  const smb = status.smb || {};
  const shares = (status.shares || []).filter((share) => share.enabled);
  const ip = network.ipv4 || smb.ip || '';
  const webuiUrls = (status.webui && status.webui.urls) || [];
  const webuiUrl = webuiUrls[0] || (ip ? `http://${ip}:${status.webui?.port || 8080}` : '—');

  function copy(text) {
    navigator.clipboard
      .writeText(text)
      .then(() => toast('已复制。', 'success'))
      .catch(() => toast('无法访问剪贴板，请手动选中文本复制。', 'error'));
  }

  function codeRow(label, value, extra) {
    return el(
      'div',
      { class: 'connect-row' },
      el('span', { class: 'connect-label', text: label }),
      el('code', { class: 'mono address-code', text: value }),
      extra || null,
      el('button', {
        class: 'btn btn-sm btn-ghost',
        type: 'button',
        text: '复制',
        onclick: () => copy(value),
      }),
    );
  }

  function shareCard(share) {
    const unc = share.unc || (ip ? `\\\\${ip}\\${share.name}` : '—');
    return el(
      'section',
      { class: 'card connect-share' },
      el(
        'div',
        { class: 'card-head' },
        el('h2', { class: 'card-title', text: share.name }),
        el('span', { class: 'row' }, share.read_only ? badge('只读', 'warn') : badge('读写', 'ok'), share.guest_ok ? badge('匿名', 'danger') : badge('需要密码', 'info')),
      ),
      el('p', { class: 'hint', text: share.comment || share.path || '' }),
      codeRow('SMB', unc),
    );
  }

  const platform = (name, title, ...children) =>
    el(
      'section',
      { class: 'card' },
      el('div', { class: 'card-head' }, el('h2', { class: 'card-title', text: title })),
      ...children,
    );

  const download = (href, text) =>
    el('a', { class: 'btn btn-sm', href, download: '', text });

  host.replaceChildren(
    el(
      'div',
      { class: 'notice notice-info' },
      el('strong', { text: '匿名访问已启用。 ' }),
      el('span', { text: '局域网内的客户端无需 SMB 账户即可打开下面的地址。这是有意为之的高风险配置：局域网内任何人都可以读写共享存储。' }),
    ),
    el(
      'div',
      { class: 'card' },
      el('div', { class: 'card-head' }, el('h2', { class: 'card-title', text: 'WebUI' })),
      codeRow('地址', webuiUrl),
      el('p', { class: 'hint', text: 'WebUI 本身仍需管理员密码，与匿名 SMB 访问相互独立。' }),
    ),
    el(
      'div',
      { class: 'grid' },
      ...(shares.length ? shares.map(shareCard) : [el('p', { class: 'hint', text: '没有已启用的共享，请在“共享”页面创建。' })]),
    ),
    el('h2', { class: 'section-title', text: '连接电脑' }),
    el(
      'div',
      { class: 'grid' },
      platform(
        'windows',
        'Windows',
        el('ol', { class: 'steps' },
          el('li', { text: '下载 windows-connect.cmd 并双击运行。' }),
          el('li', { text: '输入手机 IP；工具会先尝试匿名，再根据 Windows 版本提示输入 SMB 账号。' }),
          el('li', { text: 'Windows 11 24H2 Pro/Enterprise 默认强制 SMB 签名，匿名 guest 无法协商，必须先在 WebUI 的“用户”页创建 SMB 账号。' }),
        ),
        el('div', { class: 'card-actions' },
          download('/downloads/windows-connect.cmd', 'Windows 连接工具'),
          download('/downloads/windows-connect.ps1', '连接工具 PowerShell'),
          download('/downloads/windows-create-shortcut.cmd', '创建桌面快捷方式'),
          download('/downloads/windows-allow-guest.ps1', '启用访客 SMB'),
        ),
        el('p', { class: 'hint', text: '把 windows-connect.cmd / windows-connect.ps1 / windows-create-shortcut.cmd 下载到同一个文件夹，双击 windows-create-shortcut.cmd，桌面会出现 “Android SMB Relay” 图标。' }),
        el('p', { class: 'hint', text: 'Windows 11 24H2 的 SMB 客户端强制签名；如果代理端关闭签名，连接会被拒绝。工具优先使用 SMB 账号，不会修改系统签名策略或停止 Workstation 服务。' }),
        el('p', { class: 'hint', text: '手动方式：按 Win+R，输入上面的 SMB 地址，然后按回车。' }),
      ),
      platform(
        'linux',
        'Linux',
        el('p', { class: 'hint', text: '需要 cifs-utils 和 root 权限。' }),
        el('pre', { class: 'code-block', text: `sudo mount -t cifs //${ip || 'PHONE_IP'}/${shares[0]?.name || 'Internal'} /mnt/android-smb -o guest,uid=$(id -u),gid=$(id -g),iocharset=utf8,vers=3.0` }),
        el('div', { class: 'card-actions' }, download('/downloads/linux-mount.sh', '下载 Linux 脚本')),
        el('p', { class: 'hint', text: '也可运行：sudo ./linux-mount.sh --ip <phone-ip>' }),
      ),
      platform(
        'macos',
        'macOS',
        el('p', { class: 'hint', text: 'Finder > 前往 > 连接服务器（Cmd+K），输入 SMB 地址，选择“访客”。' }),
        el('pre', { class: 'code-block', text: `mount_smbfs //guest@${ip || 'PHONE_IP'}/${shares[0]?.name || 'Internal'} ~/android-smb` }),
        el('div', { class: 'card-actions' }, download('/downloads/macos-mount.sh', '下载 macOS 脚本')),
      ),
      platform(
        'android',
        'Android',
        el('p', { class: 'hint', text: '大多数文件管理器支持 SMB URL。输入下面的共享地址，如提示则选择匿名/访客。' }),
        el('pre', { class: 'code-block', text: shares.map((s) => `smb://${ip || 'PHONE_IP'}/${s.name}`).join('\n') || '—' }),
      ),
    ),
  );
}
