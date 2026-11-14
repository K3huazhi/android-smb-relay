# Android 兼容性

本文用 **Verified / Likely compatible / Untested** 三档描述兼容性，只依据仓库内容与真机验证记录，**不虚构测试结果**。

> 结论先行：本项目只在 **OnePlus PJA110 / Android 16 (API 36) / arm64-v8a / KernelSU 3.3.1 (uapi 5) / SELinux Enforcing** 上完成较完整验证。Android 15、Android 17、Magisk、其他 arm64 机型、热点/VPN 实际转发、完整卸载/重装仍是未验证项。**不要把它理解为“全机型可用”。**

## 1. 已验证环境

| 项目 | 值 |
| --- | --- |
| 设备 | OnePlus PJA110 |
| 系统 | Android 16 / API 36 |
| ABI | arm64-v8a |
| Root | KernelSU 3.3.1（uapi 5） |
| SELinux | Enforcing；`su` 上下文 `u:r:ksu:s0` |
| 网络 | wlan0，192.168.1.223/24 |
| Samba | Termux Samba 4.25.1 aarch64 |
| 模块 | android-smb-relay 0.1.0（本仓库 zip） |

## 2. 兼容性矩阵

| 组合/能力 | 结论 | 依据或限制 |
| --- | --- | --- |
| OnePlus PJA110 · Android 16 · KernelSU 3.3.1 · arm64 | **Verified** | 模块安装、重启启动、SMB2/3、WebUI、watchdog 均已跑通 |
| Android 15 (API 35) · arm64 | **Untested** | 机制上只依赖 root shell、`/proc`、FUSE、445 绑定；未实测 |
| Android 17 (API 37) · arm64 | **Untested** | 同上；未实测 |
| Magisk（Android 15–17） | **Untested** | 不使用 `system/`、不注入 sepolicy；Magisk 路径未实测 |
| 其他 arm64 机型（Android 15–17） | **Untested** | 取决于 ROM 的 FUSE/MediaProvider、SELinux、OEM 省电策略 |
| 16 KB page size 设备 | **Likely compatible** | 随包 Termux 二进制的 LOAD 对齐为 0x4000；未在 16 KB 设备实测 |
| Windows 10 SMB 客户端 | **Verified** | 匿名 `\\192.168.1.223\Internal` 读写；快捷脚本映射 Z: |
| Linux / macOS / Android SMB 客户端 | **Untested** | 标准 SMB2/3；本环境只有 WSL smbclient 与 Windows |
| 4 GiB+ 单文件 | **Verified** | 4 GiB+1 字节上传/下载，SHA256 一致 |
| 1000 个小文件 | **Verified** | 1000 个文件批量上传成功 |
| 多客户端并发 | **Verified** | 两个客户端各 200 MB 并发下载成功 |
| Wi-Fi 断开/恢复 | **Verified** | 断开后 `waiting_network`，恢复后自动重启 SMB |
| watchdog 失败与恢复 | **Verified** | `watchdog_max_restarts=2` 连续 kill 进入 `failed`，`smbctl start` 恢复 |
| 热点（SoftAP）/ VPN 实际转发 | **Untested** | 接口分类与排除逻辑已实现，未做真实转发测试 |
| SMB1 | **Verified（拒绝）** | `server min protocol = SMB2_02`；`-m NT1` 被拒绝 |
| 匿名整盘共享 | **Verified** | `Internal` → `/storage/emulated/0`，无账号；Linux `smbclient -N` 与 Windows 直接访问均成功 |
| 只读共享 | **Verified** | 写入被 `NT_STATUS_ACCESS_DENIED` |
| 卸载/重装 | **Untested（完整流程）** | 手动执行 `uninstall.sh` 已验证会停进程、保留 `/storage/emulated/0/SMB`；KernelSU 移除+重启未完整走一遍 |
| SELinux 零 denial 的普遍性 | **Untested** | 仅单台 Enforcing 设备验证可运行；其他 ROM 策略不同 |
| WebUI 反向代理 / IPv6-only / 多用户 | **Untested** | 当前按 0.0.0.0/私网来源设计 |

## 3. 机制层面的兼容性说明

### 3.1 启动时序

- 模块脚本在 `post-fs-data.sh`（阻塞）只做目录/符号链接/配置初始化，不等待网络。
- `service.sh` 在 late_start 等待 `sys.boot_completed`（最多 120 s），再启动 `asrd`。
- `asrd` 自己再等待 LAN 接口与存储可用；因此开机后到 SMB 可用的延迟通常为 **10–30 s**，取决于 Wi-Fi 与用户解锁。
- `/storage/emulated/0` 在用户解锁前只是空挂载点；代码用 FUSE magic 判断，不可用时回退 `/data/media/0`，解锁后按 storage fingerprint 重启 `smbd`。

### 3.2 SELinux

- 模块**不关闭 SELinux、不执行 `setenforce 0`、不注入 `sepolicy.rule`**。
- 在本机 `u:r:ksu:s0` 下，执行模块文件、绑定 445、访问 `/storage/emulated/0`、`/data/media/0` 与 `/data/adb` 均可工作。
- 这不代表其他 ROM 的 `magisk`/`ksu` domain、file context 或 OEM 策略也同样放行；出现 denial 时应采集 `dmesg`/`logcat` 的 avc 并写最小规则，而不是关闭 SELinux。

### 3.3 Samba 二进制

- 随包的是 Termux Samba 4.25.1 aarch64，运行时依赖 bionic、`/system/bin/linker64` 和若干 `.so`。
- 二进制内的 Termux 前缀被原地 patch 为 `/data/adb/android-smb-relay`；该目录下 `lib` 符号链接指向模块的 `lib`。
- Termux 包升级可能改变 soname 或依赖；本项目锁定 4.25.1-1 与 SHA256，见 `build/termux-aarch64.lock`。

### 3.4 网络接口

- `wlan0`、`eth0`、`usb0`、`rndis0` 等按名称/类型分类；`rmnet*`（蜂窝）默认排除；`tun*`、`ppp*`、`wg*`、`vgate*`、`v4-*`、`clat` 视为 VPN，不自动选择。
- 网络变化用 5 s 轮询 + settle 去抖，不直接使用 netlink；IP 变化会重启 `smbd`，不会让服务永久失效。
- 热点接口名依 OEM 而异，未实测；如自动选择不到，可在 Settings 手动指定接口。

## 4. 仍需在其他设备上验证的项目

1. Android 15 / 17 的 FUSE 挂载时机与 `/data/media/0` 可访问性。
2. Magisk 的 `magisk` domain、模块安装/卸载时序与 KernelSU 的差异。
3. 热点（SoftAP）接口名、VPN 防火墙、OEM 省电策略对 445 入向连接的影响。
4. 16 KB page size 设备的实际执行。
5. 不同 ROM 的 MediaProvider/Scoped storage 对 root 经 FUSE 访问的限制。
6. Linux/macOS/Android 客户端的 SMB3 协商、签名与文件名编码。
7. 完整卸载/重装及模块升级后的数据保留。
8. 长时间运行（数天）后的 fd/内存/日志增长。

## 5. 验证记录（2026-11-14，单台设备）

- `ksud module install` 成功；重启后 `asrd`/`smbd`/WebUI 启动。
- SMB2/SMB3：10 MB、1 GiB、4 GiB+1 上传/下载，SHA256 一致。
- 1000 个小文件批量上传成功；双客户端各 200 MB 并发成功。
- Windows 10 `net use` 读写删除；只读共享拒绝写入；`-m NT1` 拒绝。
- WebUI 登录、状态、共享/用户/设置 API、CSRF 403、stop/start 验证。
- `kill smbd` 后 watchdog 重启（`restarts=1`）；`watchdog_max_restarts=2` 时连续 kill 进入 `failed`，`smbctl start` 恢复。
- Wi-Fi 关闭进入 `waiting_network`，恢复后 SMB 重启。
- 手动执行 `uninstall.sh`：进程停止、`lib` 符号链接移除、`/storage/emulated/0/uninstall-probe.txt` 保留；重启服务后恢复。

> 以上结果只对应表中设备与构建；不能外推为其他 Android 版本、Root 实现或机型的保证。
