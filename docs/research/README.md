# Android SMB Relay 技术调研索引

调研日期：2026-11-14。范围：Android 15–17、Magisk、KernelSU、Samba 4.25.1、Android 存储/FUSE、SELinux、Android 网络。方法：优先官方文档、AOSP 源码、Samba man page、Termux 包索引；逐条标注 `[已确认]` / `[推断/待验证]`。本文档不把未上机验证的结论当作测试结果。

| 文档 | 内容 | 证据强度 |
| --- | --- | --- |
| [android.md](android.md) | Android 15/16/17、启动阶段、FBE、mount namespace、/data/adb、SMB 系统限制、兼容风险 | 官方文档 + 部分推断（本机 Android 官方站抓取受限） |
| [storage.md](storage.md) | /storage/emulated/0、/data/media/0、FUSE 历史、root 经 FUSE 的权限模型、文件操作测试矩阵、安全边界 | AOSP/文档 + 真机 FUSE 行为实测（有限） |
| [magisk.md](magisk.md) | module.prop、安装器、boot scripts、sepolicy.rule、magisk domain、WebUI 有无、Android 15–17 | Magisk 官方文档/源码，较强 |
| [kernelsu.md](kernelsu.md) | KernelSU 模块、webroot/WebUI、module config、hot install、ksu domain、ksud CLI、卸载 | KernelSU 官方文档/源码，较强；真机 3.3.1 实测补充 |
| [samba.md](samba.md) | Termux Samba 4.25.1、编译期路径、tdbsam 插件、smb.conf 安全基线、用户管理、进程模型、性能 | 包实测/readelf + Samba man page，较强 |
| [selinux.md](selinux.md) | Android SELinux、Magisk/KernelSU 注入、root daemon 检查点、denial 采集、最小权限 | AOSP/Magisk/KernelSU 源码 + 推断；真机仅验证 Enforcing 下可运行 |
| [networking.md](networking.md) | 接口清单、root 信息源、可用性判定、绑定策略、端口冲突、状态机 | Linux/Android 文档 + 真机接口实测；netlink 方案未落地（实现用 5 s 轮询） |

## 与本项目实现直接相关的已验证结论

- OnePlus PJA110 / Android 16 / API 36 / arm64-v8a / KernelSU 3.3.1 / SELinux Enforcing 下，Termux Samba 4.25.1 aarch64 `smbd` 可运行；必须把编译期 `MODULESDIR`/`PRIVATE_DIR` 等 Termux 前缀原地 patch 到 `/data/adb/android-smb-relay`，并保留该目录下 `lib` 符号链接。
- Samba 在前台 `-F` 模式会在 Android 首次连接时因 `get_remote_hostname` 失败退出；使用 `-D` daemon 模式 + pid 文件/1 s 轮询监督可稳定工作。
- 必须设置 `use sendfile = no`、`use mmap = no`、`aio read/write size = 0`、`smb2 max read/write = 1 MiB`，否则 1 GiB 下载会在约 192 MiB 处连接重置；这是本项目实测结论。
- `/storage/emulated/0` 在用户解锁前只是空挂载点；必须用 FUSE magic 判断，失败时回退 `/data/media/0`，解锁后再按 storage fingerprint 重启 smbd。
- 445 属于 <1024 特权端口，root 可绑定；SMB1 通过 `server min protocol = SMB2_02` 禁止。
- `smbpasswd -a` 在 `force user = root` 下可为不存在于 `/etc/passwd` 的 SMB 用户创建 passdb 条目并认证（本项目真机实测），但不要假设不同 SMB 用户能映射不同 Unix UID。

## 未验证/证据不足

- Android 15、Android 17、Magisk、其他 arm64 机型、热点/VPN 实际转发、完整卸载/重装、Linux/macOS/Android 客户端、16 KB page size 设备的实际运行。
- 其他 ROM/OEM 的 SELinux 策略、FUSE/MediaProvider 行为、/proc 可见性、`ip_unprivileged_port_start` 默认值。
- 自建 Samba（NDK 交叉编译）未在本项目实际执行；随包二进制来自 Termux，见 [../third-party.md](../third-party.md)。

## 来源说明

各文档正文内附 markdown 链接。部分 Android 官方页面在调研网络环境下无法直接抓取，相关结论已降级为 `[推断/待验证]`，不以社区帖子替代官方结论。
