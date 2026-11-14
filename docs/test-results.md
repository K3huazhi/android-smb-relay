# 验证记录 / Test Results

> 这是本仓库当前版本的实测摘要，不是安全认证。  
> 环境：OnePlus PJA110 / Android 16 (API 36) / arm64-v8a / KernelSU 3.3.1 / SELinux Enforcing。

## 最终构建

| 项目 | 值 |
| --- | --- |
| 版本 | 0.1.0 |
| 模块包 | `releases/android-smb-relay-0.1.0.zip` |
| 大小 | 38,061,921 字节 |
| SHA-256 | `D592972C3592EE27015813BF10A5027E1D5732A97A93BD101ED260FD4BB4ED9A` |
| Samba | Termux Samba 4.25.1 aarch64 |
| Termux 依赖锁 | `build/termux-aarch64.lock`，41 个包 |
| asrd | Go，`CGO_ENABLED=0 GOOS=linux GOARCH=arm64` |

## 已验证 / Verified

- 模块安装、重启后自动启动 `asrd` / `smbd` / WebUI。
- 修复 stale PID 后，重启不会再把复用的 PID 误判为 asrd；已实测自动启动。
- SMB2/SMB3 连接，SMB1 拒绝。
- 匿名读写 `Internal` → `/storage/emulated/0`。
- Windows 11 24H2 兼容账号 `android` / `admin` 可认证连接。
- WebUI 中文界面；默认关闭登录认证，局域网直接访问。
- WebUI Shares 页面显示 SMB 地址、读写/只读、匿名/密码、可见/隐藏。
- WebUI Connect 页面和 `/downloads/` 客户端脚本下载。
- Windows 工具 `.cmd` 使用 CRLF；参数 `-Share Internal` 正确传递，不再出现 `'Windows' is not recognized` 或 `\\IP\` 空共享名。
- Windows 工具默认使用 `android` / `admin`，无需交互输入密码。
- 服务端签名 `default`（不强制签名），Windows 24H2 客户端可按需签名。
- 早期版本的大文件/多文件验证：1 GiB、4 GiB+1 哈希一致；1000 文件；双客户端。
- watchdog 重启、Wi-Fi 断开恢复、API/CSRF/认证（认证关闭前）等已测试。

## 未验证 / Untested

- Android 15、Android 17、Magisk、其他 arm64 机型。
- Linux/macOS 真机客户端（脚本仅做语法检查，WSL `smbclient` 已测）。
- 热点/VPN 实际转发。
- 完整卸载后重装的全流程。
- 16 KB page size 真机验证（二进制 LOAD 对齐 0x4000，标为 Likely）。
- Windows 11 24H2 的匿名 guest 连接（Windows 强制 SMB 签名，guest 无法协商；必须使用账号 `android` / `admin`）。

## Windows 连接说明

Windows 11 24H2 Pro/Enterprise 默认强制 SMB 签名，匿名 guest 会被系统策略阻止，常见错误：

- `System error 1272`
- `0xC05D0003`（STATUS_SMB_NO_SIGNING_ALGORITHM_OVERLAP）
- Android 端 `0xC000020C`（STATUS_CONNECTION_DISCONNECTED）

处理方式：使用 `tools/windows-connect.cmd`，默认账号 `android` / `admin`。

## 关键数据

- 发布包大小与 SHA-256 见上表；`releases/SHA256SUMS` 提供校验文件。
- Termux 包锁：`build/termux-aarch64.lock`（41 行）。
- Samba 二进制前缀：`/data/adb/android-smb-relay`。
- 模块数据：`/data/adb/android-smb-relay`；用户文件：`/storage/emulated/0`。

## 安全声明

本仓库按使用者要求提供**可信局域网**配置：匿名整盘读写、WebUI 默认无认证、Windows 兼容账号为弱密码 `admin`。  
**不要用于不可信网络或公网。** 详见 [security.md](security.md)。
