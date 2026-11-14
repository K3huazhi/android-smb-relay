# Android SMB Relay

把一台 **Android 15–17 / arm64 / Root** 手机变成局域网 SMB 文件服务器。模块自带 Samba `smbd`、Go 守护进程 `asrd`、静态中文 WebUI 和客户端快捷挂载脚本，**不需要安装 Android App，也不需要 Termux**。

> **当前默认是匿名 SMB + 整个内部存储可读写。** 这是按使用者要求提供的“局域网直接连接”模式：同一局域网内任何人都可以在不输入账号密码的情况下读写 `/storage/emulated/0`。**请只在可信局域网使用**，详见 [安全说明](#9-安全说明)。WebUI 默认不启用登录认证。

- 版本：0.1.3
- 已验证：OnePlus PJA110 / Android 16 (API 36) / arm64-v8a / KernelSU 3.3.1 / SELinux Enforcing
- 未验证：Android 15、Android 17、Magisk、其他机型、热点/VPN 实际转发、完整卸载/重装等；详见 [兼容性](#10-兼容性)

---

## 1. 功能

- Samba 4.25.1 `smbd`，SMB2 / SMB3，默认禁用 SMB1。
- 默认匿名共享整个内部存储：`\\<手机IP>\Internal`。
- SMB 默认匿名；Windows 11 24H2 需要的兼容账号为 `android` / `admin`。
- 多个共享、只读/读写、Guest/匿名、隐藏、启用/禁用。
- WebUI：Dashboard、Shares、Users、Network、**Connect**、Settings、Logs。
- `asrd` 守护进程：启动/停止/重启 SMB、生成 `smb.conf`、watchdog、网络变化重绑、日志轮转。
- 网络自动检测：Wi-Fi / Ethernet / USB，排除蜂窝和 VPN；Wi-Fi 断开进入 `waiting_network`，恢复后自动重启。
- 电脑端快捷挂载：
  - Windows：`tools/windows-mount.cmd` / `windows-mount.ps1`
  - Linux：`tools/linux-mount.sh`（CIFS）
  - macOS：`tools/macos-mount.sh`
- 完全静态 WebUI，无 CDN、无 Node/Python 运行时。
- 卸载不删除 `/storage/emulated/0` 中的用户文件。

---

## 2. 默认共享

| 名称 | 路径 | 权限 | Guest |
| --- | --- | --- | --- |
| `Internal` | `/storage/emulated/0`（整机内部存储） | 读写 | 匿名 |

默认 SMB 配置：

```json
{
  "smb": {
    "enabled": true,
    "port": 445,
    "min_protocol": "SMB2_02",
    "max_protocol": "SMB3_11",
    "signing": "default",
    "encryption": "off",
    "guest_access": true,
    "allow_anonymous": true
  },
  "shares": [
    {
      "name": "Internal",
      "path": "/storage/emulated/0",
      "read_only": false,
      "guest_ok": true
    }
  ],
  "users": []
}
```

地址：

```text
\\192.168.1.223\Internal
smb://192.168.1.223/Internal
```

> `192.168.1.223` 只是本机验证示例，不是硬编码；实际 IP 由 WebUI 的 Network / Dashboard 显示。

---

## 3. 安装

### 3.1 KernelSU

1. 下载 [releases/android-smb-relay-0.1.3.zip](releases/android-smb-relay-0.1.3.zip)。
2. KernelSU Manager → 模块 → 从本地安装 → 选择 zip。
3. 重启手机。
4. 重启后打开 KernelSU 管理器 → 模块 → Android SMB Relay，点 **WebUI** 就能进管理界面（不用记 IP）。

或命令行：

```sh
su -c 'ksud module install /sdcard/Download/android-smb-relay-0.1.3.zip'
```

### 3.2 Magisk

Magisk App → 模块 → 从本地安装 → 选择 zip，重启。

> Magisk 路径尚未真机验证；模块不使用 `system/`、不注入 sepolicy，理论上兼容。

### 3.3 安装后

- 模块目录：`/data/adb/modules/android-smb-relay`
- 持久数据：`/data/adb/android-smb-relay`
- WebUI：`http://<手机IP>:8080`
- WebUI：默认关闭登录认证，局域网内直接访问；如重新开启认证，账号/密码为 `admin` / `admin`。
- 凭据文件：

```sh
su -c 'cat /data/adb/android-smb-relay/config/initial-credentials.txt'
```

**没有初始 SMB 账号/密码**；默认直接匿名访问 `\\<IP>\Internal`。WebUI 仍需要管理员密码（与 SMB 匿名是两件事）。

---

## 4. WebUI

两种入口，内容相同（都是 `asrd` 提供的同一个管理界面）：

1. **KernelSU 内直接打开（推荐）**：KernelSU 管理器 → 模块 → Android SMB Relay → **WebUI**。模块的 `webroot/index.html` 是一个启动页，会用 root shell 判断 `asrd` 是否在跑，正常就直接跳进完整 WebUI；没在跑时给出“启动服务/启动 WebUI”按钮，不用回浏览器输地址。
2. **浏览器**：打开 `http://<手机IP>:8080`，默认没有登录密码，局域网内直接进入 WebUI。

| 页面 | 内容 |
| --- | --- |
| Dashboard | SMB/WebUI 状态、IP、端口、连接数、速率、CPU/RAM/存储、Connect 快捷卡 |
| Shares | 共享列表：名称、**SMB 地址**、路径、读写/只读、Anonymous/Password、Visible/Hidden、启用、编辑/删除 |
| Users | 可选的 SMB 账号（默认空）；启用口令认证后才需要 |
| Network | 接口、IPv4/IPv6、网关、MAC、自动/手动选口 |
| **Connect** | `\\IP\Internal`、WebUI 地址、Windows/Linux/macOS/Android 连接步骤、挂载脚本下载 |
| Settings | SMB 端口/协议/签名/加密/Guest/匿名/接口、**可信 Wi-Fi**、WebUI、Startup、Storage、Log |
| Logs | asrd/smbd/console 日志、级别过滤 |

Shared 地址和 Guest 状态来自后端真实状态；不是前端拼的假数据。

---

## 5. 客户端访问

### 5.1 Windows（推荐）

直接打开资源管理器，地址栏输入：

```text
\\<手机IP>\Internal
```

**Windows 11 24H2 Pro/Enterprise 默认强制 SMB 签名，匿名 guest 会话无法协商签名，会返回 1272 或 0xC05D0003。** 工具已内置 `android` / `admin` 账号，直接运行即可：

```powershell
powershell -ExecutionPolicy Bypass -File .\tools\windows-connect.ps1 -PhoneIp <手机IP> -User android
```

工具会自动使用 `android` / `admin` 账号映射；`-Anonymous` 可只尝试匿名。Windows 10/11 家庭版或旧版本若提示“组织安全策略阻止未经身份验证的来宾访问”，可先以**管理员**运行：

```powershell
powershell -ExecutionPolicy Bypass -File .\tools\windows-allow-guest.ps1
```

然后再次打开 `\\<手机IP>\Internal`，不需要账号密码。

或使用快捷挂载脚本：

```powershell
powershell -ExecutionPolicy Bypass -File .\tools\windows-connect.ps1 -PhoneIp <手机IP> -Share Internal -Drive Z
```

- 无需密码；脚本先检查 445，再映射 `Z:`。
- `windows-mount.cmd` 可双击，提示输入手机 IP。
- **生成桌面快捷方式**：双击 `tools\windows-create-shortcut.cmd`，桌面会出现 “Android SMB Relay” 图标；以后双击图标，输入手机 IP 即可挂载。
- 卸载：`... -Drive Z -Unmount`
- 工具默认自动使用 `android` / `admin`，无需输入密码；`-Anonymous` 可只尝试匿名。

`windows-allow-guest.ps1` 修改的是 PC 端 `AllowInsecureGuestAuth`，**只影响这台电脑**；不需要时可用 `-Disable` 关闭。

### 5.2 Linux

```sh
smbclient //<手机IP>/Internal -N
# 或
sudo mount -t cifs //<手机IP>/Internal /mnt/android-smb \
  -o guest,uid=$(id -u),gid=$(id -g),iocharset=utf8,vers=3.0
```

脚本：

```sh
sudo ./tools/linux-mount.sh --ip <手机IP>
```

需要 `cifs-utils` 与 root。

### 5.3 macOS

Finder → 前往 → 连接服务器（Cmd+K）：

```text
smb://<手机IP>/Internal
```

选择“访客”。或：

```sh
./tools/macos-mount.sh --ip <手机IP>
```

### 5.4 Android

文件管理器/ES/MT 等支持 SMB 的应用输入：

```text
smb://<手机IP>/Internal
```

选择匿名/访客。

---

## 6. CLI

```sh
su -c '/data/adb/modules/android-smb-relay/bin/smbctl status'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl start'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl stop'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl restart'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl url'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl shares'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl users'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl network'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl logs -n 100'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl config'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl credentials'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl webui-passwd'
```

`smbctl start/stop/restart` 只控制 SMB；`webui-start` / `webui-stop` 控制 HTTP 监听。

---

## 7. 目录与配置

```text
/data/adb/modules/android-smb-relay/   模块（升级时整体替换）
├── bin/         asrd, smbd, smbpasswd, smbcontrol, testparm, smbctl
├── lib/         Samba 运行库与插件
├── libexec/     samba-dcerpcd 与 rpcd_*（IPC$ 命名管道服务，缺了它安卓客户端挂载会报 0xC000020C）
├── webroot/     KernelSU 模块页入口（index.html：判断状态后跳转完整 WebUI）
├── scripts/     start/stop/restart/status/network/watchdog
├── webui/       静态 WebUI + downloads 挂载脚本
├── config/      默认配置
└── docs/        third-party 说明

/data/adb/android-smb-relay/           持久数据（升级保留）
├── config/config.json
├── config/initial-credentials.txt
├── logs/
├── run/asr.sock, asrd.pid
└── samba/       smb.conf, passdb.tdb 等
```

配置只能通过 WebUI / `smbctl` / 直接编辑 `config.json` 修改；不要手改生成的 `samba/smb.conf`。

默认端口：

- SMB：445（root）
- WebUI：8080

---

## 8. 构建

需要 Linux/WSL、Go 1.22+、Python 3、`dpkg-deb`、`readelf`、`zip`。

```sh
bash build/build.sh
# 输出 releases/android-smb-relay-0.1.3.zip
```

构建内容：

- Termux Samba 4.25.1 aarch64 包（41 个，SHA256 锁定）
- 把编译期前缀 `/data/data/com.termux/files/usr` patch 为 `/data/adb/android-smb-relay`
- `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '-s -w'`
- 静态 WebUI + 客户端挂载脚本
- 确定性 zip（固定时间戳、排序）

详见 [docs/build.md](docs/build.md)。第三方/许可证见 [docs/third-party.md](docs/third-party.md)。

---

## 9. 安全说明

**默认配置故意不安全的匿名整盘共享**，因为这是使用者的明确要求：

- 同一局域网内任何人都可以读写 `/storage/emulated/0`，包括照片、文档、应用数据目录。
- 没有 SMB 账号/密码，`guest_access=true`、`allow_anonymous=true`。
- SMB 签名默认不强制（`server signing = default`），Windows 11 24H2 客户端会按自己的策略签名；未签名流量仍有被中间人篡改/重放的风险，需要更高安全性可改为“强制”。
- WebUI 默认无登录密码；不要把 WebUI 暴露到公网。
- 不要在不信任的 Wi-Fi（咖啡厅、酒店、公司访客网络）开启。
- **可信 Wi-Fi 白名单**（Settings → 可信网络）：填入自家 SSID 后，离开这些网络时自动降级——默认「只读」，也可选「停止 SMB」。判定读不到 SSID 时按可信处理并在警告里提示；有线/USB 连接不参与判定。
- 建议只在家用/隔离 LAN 使用；不用时在 WebUI 关闭 SMB 或改只读。
- 若改回认证模式：在 Settings 关闭 Guest/匿名，在 Users 创建账号，客户端用 `smbclient -U user` / Windows 凭据。

模块**不关闭 SELinux**，不使用 `setenforce 0`。

---

## 10. 兼容性

| 项目 | 结论 | 说明 |
| --- | --- | --- |
| OnePlus PJA110 / Android 16 / KernelSU 3.3.1 / arm64 | **Verified** | 模块、匿名 SMB、WebUI、4 GiB+1、1000 文件、双客户端、Wi-Fi 恢复、watchdog |
| Windows 10 客户端 | **Verified** | 匿名 `\\IP\Internal` 读写；快捷脚本映射 Z: |
| Android 15 / 17 | **Untested** | 未实测 |
| Magisk | **Untested** | 未实测 |
| 其他 arm64 机型 | **Untested** | FUSE/SELinux/OEM 策略不同 |
| 热点 / VPN 实际转发 | **Untested** | 接口分类已实现 |
| Linux / macOS / Android 客户端 | **Untested** | 脚本已提供，标准 SMB2/3 |
| 16 KB page size | **Likely compatible** | 随包二进制 LOAD 对齐 0x4000 |
| 完整卸载/重装 | **Untested** | 手动执行 `uninstall.sh` 已验证保留用户文件 |

详见 [docs/android-compatibility.md](docs/android-compatibility.md)。

---

## 11. 故障排查

| 现象 | 处理 |
| --- | --- |
| WebUI 打不开 | 确认手机 IP、8080 端口、WebUI 未关闭；`smbctl status` |
| 安卓客户端挂载报 `SMB2_CREATE ... 0xC000020C` | 0.1.0 的打包漏了 `libexec/samba/samba-dcerpcd`，客户端枚举共享（IPC$/srvsvc）就会断连；升级到 0.1.1 重装并重启 |
| 445 被占用 | 关闭其他 SMB/Samba；改 `smb.port`（Windows 需 445 才能直接 `\\IP`） |
| Windows 拒绝匿名 | 管理员运行 `tools/windows-allow-guest.ps1`，再访问 |
| Windows 提示凭据 | 清除旧连接 `net use * /delete /y`，再打开 `\\IP\Internal` |
| Linux mount 失败 | 安装 `cifs-utils`；确认 `vers=3.0`；看 `dmesg` |
| SMB 启动失败 | `smbctl logs --source smbd`；检查 `samba/smb.conf`、库路径 |
| 存储未就绪 | Android 解锁后重试；`/data/media/0` 回退日志 |
| SELinux denial | 采集 `dmesg`/`logcat` avc；不要关闭 SELinux |
| 重新开启 WebUI 认证 | Settings → WebUI → 认证，账号/密码 `admin` / `admin` |

日志：

```sh
su -c '.../smbctl logs -n 200'
su -c '.../smbctl logs --source smbd -n 200'
```

---

## 12. 卸载

```sh
su -c 'ksud module uninstall android-smb-relay'
# 或 Magisk 中移除，然后重启
```

卸载会停止进程、清理 PID/socket/临时文件，**不会删除** `/storage/emulated/0` 下的用户文件。

配置与日志默认保留，便于重装恢复；需要彻底清理可手动删除 `/data/adb/android-smb-relay`。

---

## 13. 目录与文档

- [docs/research/README.md](docs/research/README.md)：Android/Magisk/KernelSU/Samba/SELinux/存储/网络调研
- [docs/architecture.md](docs/architecture.md)：进程、状态机、watchdog、存储映射
- [docs/security.md](docs/security.md)：威胁模型与匿名模式风险
- [docs/android-compatibility.md](docs/android-compatibility.md)：兼容性矩阵
- [docs/build.md](docs/build.md)：可复现构建
- [docs/troubleshooting.md](docs/troubleshooting.md)：排障
- [docs/third-party.md](docs/third-party.md)：Samba/Termux 许可证与来源

## 14. 许可证

本项目以 **GNU GPL v3.0** 发布，完整文本见 [LICENSE](LICENSE)。随包分发的 Samba 4.25.1 与 Termux 依赖库遵循各自上游许可证（Samba 为 GPL-3.0-or-later）；来源、版本、哈希与二进制前缀修改说明见 [docs/third-party.md](docs/third-party.md)。

---

> 本项目只在上述真机组合上做过较完整验证；**不承诺所有 Android 15–17 机型可用**，也不建议把匿名整盘共享用于不可信网络。
