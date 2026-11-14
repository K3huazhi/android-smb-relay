# Android 15 / 16 / 17 平台调研（Root SMB 模块 · arm64）

- **调研日期**：2026-11-14
- **方法**：只读检索官方一手资料，逐条比对后按证据强度标注。优先顺序为 AOSP 官方文档（`source.android.com` / `android.googlesource.com`）、Android Developer（`developer.android.com`）、Magisk 官方文档站（`topjohnwu.github.io/Magisk`）、KernelSU 官方文档站（`kernelsu.org`），社区资料只在官方缺失时作为补充且单独标注。
- **范围**：Android 15/16/17 的发布状态与 API level；开机关键阶段（post-fs-data、late_start service、`sys.boot_completed`、用户解锁）；FBE 与 CE/DE 存储；Root 方案下的 mount namespace；`/data/adb` 布局；root 访问 `/storage/emulated/0` 的 FUSE/MediaProvider 机制概览；对 SMB 守护进程的系统限制（<1024 端口、SELinux、私有目录、`/proc`）；Android 15/16/17 兼容性风险与真机验证项。
- **不覆盖**：App 层 Storage Access Framework / MediaStore API 细节；Samba 协议本身；KernelSU 与 Magisk 的安装流程（见 [magisk.md](./magisk.md) 与后续 kernelsu.md）；本项目的 Go 实现。
- **可信度说明**：
  - `[已确认]`：能直接读到官方原文（官方文档页、AOSP 文档、官方 changelog、AOSP 提交说明），可直接作为实现依据。
  - `[推断/待验证]`：官方未明说，由已确认机制推导，或只能引用次级来源；**必须真机验证后才能写进实现**。
  - `[社区来源]`：论坛 / 问答 / 第三方整理，仅用于「现象描述」，不作依据。
  - **检索环境限制（重要）**：本次 `source.android.com`、`developer.android.com`、`android.googlesource.com`、`cs.android.com` 的直接 HTTP 抓取失败（TLS/连接被阻断）；可用的替代通道是 `source.android.google.cn` 镜像（正文会被截断）、搜索引擎索引快照、以及 `topjohnwu.github.io` 与 `kernelsu.org` 的原文页面。因此凡涉及 AOSP 文档但未读到正文的条目，均降级为 `[推断/待验证]` 并在文末「证据不足」中列出。

> 真机基线（用户提供的背景信息，**非本次调研实测**）：OnePlus PJA110，Android 16 / API 36，arm64-v8a，KernelSU 3.3.1，SELinux Enforcing，`su` 上下文 `u:r:ksu:s0`。下文凡写「真机」均指这台设备，凡写「验证方法」均是待执行项，不代表已得到结果。

---

## 1. 结论摘要（先看这 9 条）

1. **API level 对应关系已足够确定**：Android 15 = API 35、Android 16 = API 36、Android 17 = API 37。额外的坑是 **Android 16 QPR2 把 SDK 抬到了 API 36.1**（季度平台版本的「小数 API level」），判断设备版本时不能只比较整数。`[已确认]`
2. **Android 17 已正式发布**（2026 年），Magisk v31.0 的 changelog 明确写有「Support Android 17 (API 37)」；但 Android 17 的**具体推送日期**只在厂商/媒体页面看到，未抓到 Google 官方日期页。`[已确认]`（版本存在与 API 号）/`[推断/待验证]`（发布日期）
3. **post-fs-data 与 late_start service 语义**：post-fs-data 在 `/data` 已解密挂载后、Zygote 启动前执行且**阻塞**（Magisk 上限 40s、脚本 35s；KernelSU 上限 10s）；`late_start service` **非阻塞**、与开机并行，是放常驻服务的默认位置。`[已确认]`
4. **FBE 下 `/storage/emulated/0` 的可用性由「用户解锁」决定，而不是由「`/data` 挂载」决定**。AOSP vold 明确：FUSE 守护进程是 MediaProvider 进程，**必须等用户解锁才能启动**，所以 emulated 卷在此之前不会挂载。`[已确认]`
5. 因此 **root 守护进程绝不应该在 post-fs-data 访问 `/storage/emulated/0`**：那时既没有 FUSE 挂载、也没有 MediaProvider，而且 post-fs-data 是阻塞阶段，任何等 IO/等网络都可能拖死开机。`[已确认]`（机制）+`[推断/待验证]`（默认设备上的具体表现）
6. **`sys.boot_completed=1` 不等于用户已解锁**：无密码设备两者几乎同时；有 PIN/密码/图案的 FBE 设备，开机完成时 CE 存储仍不可用，`/storage/emulated/0` 也可能尚未挂载。正确做法是**同时等待 `sys.boot_completed` 与存储可用性**，而不是只等属性。`[已确认]`（时序）+`[推断/待验证]`（等待条件组合）
7. **mount namespace 不是主要矛盾，挂载时机才是**。Magisk/KernelSU 的模块脚本在全局 mount namespace 中执行；真正让 `/storage` 消失的是「FUSE 卷尚未挂载」以及「`su` 默认给 shell 一个隔离 namespace、之后的新挂载不会传播进去」。`[已确认]`（隔离 namespace 存在）/`[推断/待验证]`（传播行为对守护进程的影响）
8. **SMB 的 445/139 是特权端口**：root（uid 0，默认保有 `CAP_NET_BIND_SERVICE`）可以绑定；非 root 需要 `net.ipv4.ip_unprivileged_port_start=0`。Magisk v30.7 起明确「默认不再丢弃 capabilities」，这对 root 服务是利好。`[已确认]`（Linux 能力语义 + Magisk changelog）/`[推断/待验证]`（Android 默认 sysctl 值）
9. **Android 15/16/17 对本模块的三条硬兼容项**：16 KB page size（AOSP 自 Android 15 支持，Play 对 targetSdk≥35 的应用有 2027-02-01 起的强制期限）、Android 16 QPR2 的 **sepolicy 二进制格式变化**（Magisk 需 ≥ v30.3/v30.7 才支持）、以及解锁时机导致的存储可见性差异。`[已确认]`

---

## 2. Android 15 / 16 / 17 的发布状态与 API level

### 2.1 版本总表

| 版本 | API level | 状态与关键事实 | 证据 |
|---|---|---|---|
| Android 15 | 35 | 2024 年 9 月发布到 AOSP，随后向 Pixel 推送；AOSP 自 Android 15 起支持 **16 KB page size** 设备。 | [Android 15 版本页](https://developer.android.com/about/versions/15)、[16 KB page size 文档](https://developer.android.com/guide/practices/page-sizes) `[已确认]`（API 35、16KB 支持）/`[推断/待验证]`（Pixel 推送的具体日期 2024-10-15） |
| Android 16 | 36 | AOSP `android-16.0.0_r1` 的构建 `BP2A.250605.031.A2` 标注日期为 **2025-06-05**；`Android16 QPR2` 对应 **API level 36.1**（发布名 25Q4）。 | [构建号与 API level 参考](https://source.android.com/docs/setup/reference/build-numbers) `[已确认]` |
| Android 17 | 37 | 已正式发布，源码进入 AOSP；Magisk v31.0（2026-09-04）changelog 写有「[Zygisk] Support Android 17 (API 37)」。 | [Android 17 版本页](https://developer.android.com/about/versions/17)、[Magisk Changelog](https://topjohnwu.github.io/Magisk/changes.html) `[已确认]`（存在性 + API 37）/`[推断/待验证]`（发布日期；媒体与厂商页面记为 2026-06-16，未核到 Google 官方日期页） |

### 2.2 读取设备版本的正确姿势（待真机验证）

- `getprop ro.build.version.sdk` 给出**主 API level**（36）；季度版本另看 `ro.build.version.release`、`ro.build.version.security_patch`，以及可能的「小数 SDK」属性（属性名待确认，见上）。`[推断/待验证]`（属性名来自 AOSP 版本化的惯例，未在本次检索中逐字核到）
- `getprop ro.build.version.release` 可能是「16」而不是「16 QPR2」；**不要用 release 字符串做兼容判断**，用 `getprop ro.build.version.sdk` + 可能的 minor 属性（属性名待确认）+ `ro.build.version.codename`。`[推断/待验证]`
- 需要跨 Android 15/16/17 统一判断时，建议以 `ro.build.version.sdk` 的整数区间为准（35/36/36.1/37），并把 36.1 视作「≥36」。`[推断/待验证]`

> 工程含义：本项目若要声明「支持 Android 15/16/17」，判定条件应是 **`sdk >= 35`**，而不是字符串匹配；遇到 36.1 这类季度版本时按 ≥36 处理。

---

## 3. 启动流程：从 init 到用户解锁

### 3.1 关键阶段与语义

下表把 AOSP init 的阶段与 Root 方案暴露的钩子对齐。阶段顺序以 KernelSU 官方文档给出的开机流程图为准；Magisk 官方文档对同名阶段的描述与之一致。

| 阶段 / 触发点 | 发生位置与阻塞性 | 与存储的关系 | 证据 |
|---|---|---|---|
| `early-init` → `init` → `late_init` | init 早期，SELinux 策略已加载 | `/data` 尚未挂载 | [KernelSU Module guide](https://kernelsu.org/guide/module.html) `[已确认]` |
| `early-fs` / `fs` | 启动 vold，挂载 `/vendor`、`/system`、`/data` 等 | **`/data` 在此后可用**（FBE 下为 DE 部分） | `[已确认]` |
| **`post-fs-data`** | `/data` 解密挂载后、Zygote 启动前；**阻塞** | 只有 `/data`，**没有 `/storage/emulated/0`** | [Magisk Internal Details](https://topjohnwu.github.io/Magisk/details.html)、Magisk changelog v21.0/v21.2 `[已确认]` |
| `zygote-start` → `class_start main` | Zygote、system_server、MediaProvider 等系统服务启动 | vold 具备启动 FUSE 守护进程的前提 | `[已确认]` |
| **`late_start service`** | 与开机并行，**非阻塞** | FUSE 可能在几秒后才挂载 | `[已确认]` |
| **boot completed**（广播 `ACTION_BOOT_COMPLETED`） | 系统服务就绪，`sys.boot_completed=1` | **仍可能未解锁**，`/storage/emulated/0` 仍可能不存在 | `[已确认]` |
| **用户解锁**（`ACTION_USER_UNLOCKED`，输入 PIN/密码/图案/指纹） | 解锁后 CE 存储可用 | FUSE 守护进程此时才启动，`/storage/emulated/0` 才出现 | `[已确认]` |

关键引用原文：

- Magisk：「This triggers on `post-fs-data` when `/data` is decrypted and mounted… `late_start` … Magisk "service" mode will be started … When `sys.boot_completed=1` is triggered, Magisk resets the bootloop counter…」`[已确认]`
- KernelSU：post-fs-data 为 BLOCKING，脚本「after 10 seconds」被终止；late_start service 为 NON-BLOCKING，官方推荐「This is the recommended stage to run most scripts」。`[已确认]`

### 3.2 FBE 解锁与 `/data/media` 可用性的关系

FBE（File-Based Encryption）把数据分成两套密钥与两个可见域：

- **DE（Device Encrypted）**：设备启动并通过校验后即可解密，不需要用户凭据。
- **CE（Credential Encrypted）**：**必须等用户解锁**后才可用。

AOSP Direct Boot 文档原文：「Credential encrypted storage, which is the default storage location and only available after the user has unlocked the device」「Device encrypted storage, which is a storage location available both during Direct Boot mode and after the user has unlocked the device」。`[已确认]`

Compatibility Definition（CDD）把这两条写成强制要求：设备必须实现 Direct Boot API；启动时不得要求用户提供凭据即可访问 DE；**只有用户提供凭据并广播 `ACTION_USER_UNLOCKED` 后，才允许访问 CE 存储**。`[已确认]`，引用 [Android 14 CDD 9.9](https://source.android.com/docs/compatibility/14/android-14-cdd#9_9_data_storage_encryption)。

对本模块最重要的推论（分层标注）：

1. `/data/media/0` 属于 **DE** 域（`/data` 分区，FBE 下 `/data/media` 在设备启动后即可读），所以 root 在解锁前理论上能读到媒体文件。`[推断/待验证]`（DE 的划分在 AOSP 文档里是「app 专属数据 = CE，媒体/共享存储 = DE」这一层，具体到 `/data/media` 的标签与可读性需真机 `ls /data/media/0` 验证）
2. 但用户可见的 `/storage/emulated/0` **不是** `/data/media/0` 的直通，它由 FUSE 守护进程提供，而守护进程要等用户解锁。所以「root 能读 `/data/media/0`」与「root 能读 `/storage/emulated/0`」是**两个时间点**。`[已确认]`（FUSE 守护进程时序）+`[推断/待验证]`（两者时间差的具体秒级表现）
3. **CE 存储（`/data/data`、`/data/user/0`）在解锁前完全不可用**，不要在开机早期把配置/凭据写进 CE 路径；模块自己的持久化应放 `/data/adb`（DE，见 §5）。`[已确认]`（CE/DE 语义）+`[推断/待验证]`（`/data/adb` 的具体加密域归属，Magisk 文档只说它是 device encrypted storage）

### 3.3 为什么 root daemon 不该在 post-fs-data 访问 `/storage/emulated/0`

按证据强度排列：

1. **FUSE 守护进程还不存在**。vold 提交说明原文：「we depend on the user to become unlocked before we can start the FUSE daemon (which is the MediaProvider application process)」。post-fs-data 时 Zygote 都没起来，MediaProvider 更不会存在。注意该提交确立了「FUSE 依赖解锁」的设计意图，Android 15/16/17 是否严格保持需真机验证；但「post-fs-data 早于 Zygote」这一点与版本无关。`[已确认]`（时序）/`[推断/待验证]`（新版本仍严格如此）
2. **挂载还没建立**。emulated 卷的 `/dev/fuse` 挂载点由 vold 在用户可用后才创建（提交原文：「Mount /dev/fuse on /mnt/user/<userid>/<volumeid>」），此时 `/storage/emulated/0` 通常是 `ENOENT`。`[已确认]`（机制）+`[推断/待验证]`（默认设备上的 errno 表现）
3. **post-fs-data 是阻塞阶段且有超时**。Magisk：整体 40s、脚本 35s（changelog v21.0/v21.2）；KernelSU：10s。任何「等网络」「等文件系统」的逻辑都可能让开机超时、模块被杀，严重时进 Safe Mode。`[已确认]`
4. **最坏情况是挂起而不是报错**。如果挂载点已存在但 FUSE 守护进程未运行，对该路径的访问会进入 D 状态等待，可能导致开机看门狗/卡死。这是 FUSE 的通用行为推导，**必须真机验证**。`[推断/待验证]`
5. **设计上不可依赖**：模块被禁用、安全模式、late-load 模式下 post-fs-data 脚本根本不会执行；正确的启动位置是 `service.sh`（或 KernelSU 的 `boot-completed.sh`）+ 就绪轮询。`[已确认]`（脚本执行条件）+`[推断/待验证]`（轮询策略）

### 3.4 推荐的等待策略（语义，不含实现）

```
等 sys.boot_completed=1  →  再轮询存储就绪  →  再启动服务（或先启动仅监听、存储就绪后再挂 share）
```

- 属性等待：Magisk 官方给的是 `resetprop -w sys.boot_completed 0`；KernelSU 可用 `boot-completed.sh`（官方文档：boot completed 时执行）。`[已确认]`
- 存储就绪判定优先级（建议）：
  1. `/storage/emulated/0` 存在且可 `stat`；
  2. 能列出用户目录（`/storage/emulated/0/Android` 或用户指定的 share 根）；
  3. 反复 `ENOENT`/`EACCES` 时指数退避重试，并记录日志（不要死循环阻塞开机）。`[推断/待验证]`
- **不要**把「用户已解锁」写成硬等待条件：无密码设备不会广播解锁（或马上广播），硬等 `ACTION_USER_UNLOCKED` 可能永远等不到。`[推断/待验证]`
- `sys.user.0.ce_available` 这类属性被部分资料当作「CE 可用」标志，但本次未在官方文档中核实其存在与语义，**不可直接依赖**。`[推断/待验证]`

---

## 4. Mount namespace、`/storage` 可见性与延迟启动

### 4.1 已确认的事实

- **Magisk 的 mount 是 magic mount（bind mount 组合）**，在 post-fs-data 阶段把模块文件挂进系统；`su` shell 默认拿到**隔离的 mount namespace**（changelog v21.0：「Isolated mount namespace will now first inherit from parent process, then isolate itself from the world」）。`[已确认]`
- **KernelSU 的 system 挂载委托给 metamodule（如 meta-overlayfs，OverlayFS）**；hot install 的 `MODULE_HOT_RUN_SCRIPT` 明确「runs … in the global mount namespace」。`[已确认]`
- KernelSU 的 boot 脚本由同一执行器运行；官方文档未逐句写「在全局 namespace」，但既然热装脚本被特别说明为 global，推断 boot 脚本同属全局。`[推断/待验证]`
- `/storage/emulated/0` 是一个 **FUSE 挂载**，与模块系统无关：它是否可见只取决于「vold 有没有挂、FUSE 守护进程有没有起」。`[已确认]`

### 4.2 由此得到的三个工程结论

1. **模块脚本自己看到的 `/storage` 与 App 看到的 `/storage` 不是同一个「时刻状态」**：模块脚本在全局 namespace、开机早期执行；App 在用户解锁后执行。脚本里看不到不代表 App 看不到。`[推断/待验证]`
2. **从 `su` shell 手动启动的守护进程会继承该 shell 的隔离 namespace**：如果启动时 `/storage/emulated/0` 还没挂载，之后 vold 在全局 namespace 里新挂的 FUSE **不保证**出现在这个旧 namespace 中，进程可能永久看不到 `/storage`。这是「开机后手动 `su -c start` 能跑、重启后 `service.sh` 起不来」类问题的常见根因。`[推断/待验证]`
3. **延迟启动与重试是必需项，不是优化项**。即便 `service.sh` 在全局 namespace，它也可能在 FUSE 挂载之前几秒执行；必须重试。`[已确认]`（时序）+`[推断/待验证]`（重试策略）

### 4.3 真机验证方法（只读命令）

```sh
# 1) 当前进程与 init 是否同一 mount namespace（输出 inode 相同即同 namespace）
ls -l /proc/self/ns/mnt /proc/1/ns/mnt

# 2) /storage 的实际挂载情况与类型
grep -E ' /(storage|mnt/user|data/media)' /proc/mounts

# 3) 符号链接链
readlink -f /sdcard ; readlink -f /storage/emulated/0

# 4) 是否为 FUSE 且挂载参数（如 default_permissions / allow_other）
mount | grep -i fuse
```

以上命令的**预期输出**需上机记录；本文不预设结果。`[推断/待验证]`

---

## 5. `/data/adb` 及其子目录

`/data/adb` 是 Magisk 与 KernelSU **共用**的持久化根目录，官方描述（Magisk）为：权限 `700`、属主 `root`、上下文 `u:object_r:adb_data_file:s0`，且位于 Device Encrypted storage，因此「`/data` 挂载后即可用」。`[已确认]`

| 路径 | 归属 | 用途 | 证据 |
|---|---|---|---|
| `/data/adb` | Magisk / KernelSU | 持久化根；`700 root:root`，`adb_data_file` | [Magisk Internal Details](https://topjohnwu.github.io/Magisk/details.html) `[已确认]` |
| `/data/adb/modules` | 两者 | 已启用模块；Magisk 下以只读 bind mount 挂到 `$MAGISKTMP/.magisk/modules`（因 `/data` 有 `nosuid`） | `[已确认]` |
| `/data/adb/modules_update` | 两者 | 升级暂存区；**下次重启**才合并进 `modules` | `[已确认]` |
| `/data/adb/post-fs-data.d`、`/data/adb/service.d` | Magisk / KernelSU | 全局通用脚本（非模块）；KernelSU 另有 `post-mount.d`、`boot-completed.d`、`late-load.d`、`initrc.d` | [KernelSU Module guide](https://kernelsu.org/guide/module.html) `[已确认]` |
| `/data/adb/magisk`（`$DATABIN`） | Magisk | busybox、脚本、magisk 二进制；`/data/adb/magisk.db` 存设置与授权 | `[已确认]` |
| `/data/adb/ksu` | KernelSU | busybox 在 `/data/adb/ksu/bin/busybox`（官方注明为内部路径，未来可能变化） | `[已确认]` |
| `/data/adb/ksu` 其余内容（`module_configs/`、`ksud` 等） | KernelSU | 内部实现细节 | `[推断/待验证]` |
| `/data/adb/metamodule` | KernelSU | metamodule 容器（安装 meta-overlayfs 后存在） | `[推断/待验证]` |
| `/data/adb/smbshare`（本项目自建） | 本项目 | 配置、二进制、日志、PID、socket | `[推断/待验证]`（设计建议，见 §9） |

补充：

- KernelSU 的 `initrc/`、`preinit/`、`modules.rc` 落盘在 `/metadata` 分区，官方明确 `/metadata` 很小（约 10 MB）且承载加密密钥与 OTA 状态。**不要把本模块的运行时数据放 `/metadata`**。`[已确认]`
- `/data/adb` 是 **DE** 存储，解锁前可写；但它是 root 专属目录，其他 App 访问需要 SELinux 例外，跨进程通信不要指望 App 直接读该目录下的文件。`[已确认]`（权限/上下文）+`[推断/待验证]`（跨 App 访问）

---

## 6. root 访问共享存储的 FUSE / MediaProvider 机制概览

> 本节只做机制层概览；路径拓扑、性能与测试矩阵见 [storage.md](./storage.md)。

### 6.1 已确认的骨架

1. **Android 11 起用 FUSE 取代 sdcardfs**：官方在 SDCardFS 弃用页写明「SDCardFS is deprecated on devices launching with Android 11 or higher and running kernel 5.4 or higher」，替代实现是 FUSE；新 FUSE 实现主要用于支持「位置信息脱敏（location redaction）」等策略。`[已确认]`，引用 [SDCardFS 弃用](https://source.android.com/docs/core/storage/sdcardfs-deprecate)
2. **FUSE 守护进程在 MediaProvider 进程里**：vold 提交明确 FUSE daemon 即「the MediaProvider application process」，并要等用户解锁。`[已确认]`
3. **FUSE 让 MediaProvider 在用户态审查文件操作**：Scoped storage 官方页写「Android 11 or higher supports Filesystem in Userspace (FUSE), which enables the MediaProvider module to examine file operations in user space and to gate access to files based on the policy to allow, deny, or redact access」。`[已确认]`，引用 [Scoped storage](https://source.android.com/docs/core/storage/scoped)
4. **底层是 `/data/media/0`**；vold 提交把 `/mnt/user/0/emulated/0/Android` 与 `/mnt/user/10/emulated/0/Android` 都指到同一个 `lower filesystem` 路径 `/data/media/0/Android`。`[已确认]`
5. **FUSE 有 passthrough**：LPC 2021 的 FUSE 议题材料展示了 Android 的 FUSE passthrough——打开文件时 `FUSE_PASSTHROUGH_OPEN` 把 read/write/mmap 直接转发给底层文件系统，用守护进程凭据。`[已确认]`（会议材料），引用见文末。

### 6.2 对 root 守护进程意味着什么（分层）

- **文件访问仍然经过 FUSE**：root 进程打开 `/storage/emulated/0/...` 时，VFS 把请求交给 FUSE，FUSE 守护进程（MediaProvider，在 CE 解锁后运行）决定放行/拒绝/脱敏。`[已确认]`（机制）
- **root 是否被「特权放行」取决于 MediaProvider 的策略实现**：它按调用方 uid 做判断，root（uid 0）通常不受普通 App 的 scoped storage 限制，但**没有官方文档保证**；`/storage/emulated/0/Android/data`、`/Android/obb` 这类受保护目录的行为尤其需要实测。`[推断/待验证]`
- **SELinux 上下文不是「一个 fuse 类型就完事」**：`/storage/emulated/0` 下文件的 label 由 FUSE 守护进程与 vold 的 `genfs_contexts` 共同决定，不同 Android 版本/ROM 可能有差异；root 域（`u:r:ksu:s0` / `u:r:magisk:s0`）通常能绕过 App 侧限制，但**必须以真机 `ls -Z` 为准**。`[推断/待验证]`
- **`su` 域是否 permissive**：Magisk 的 `magisk` 域官方定义为 permissive（[magisk.md](./magisk.md) 已记录）；KernelSU 的 `u:r:ksu:s0` 是否 permissive 或只是被授予宽规则，官方文档未写，真机可用 `cat /sys/fs/selinux/enforce` 与 `dmesg | grep avc` 验证。`[推断/待验证]`

### 6.3 与 SMB 服务直接相关的三个「不要」

1. **不要在解锁前用 `/storage/emulated/0` 判断「存储不可用」**——那是正常的；应改用 `/data/media/0`（若业务允许）或等待。`[已确认]`（时序）+`[推断/待验证]`（业务取舍）
2. **不要把「能读 `/data/media/0`」等同于「FUSE 视图一致」**：绕过 FUSE 会丢掉 MediaProvider 的策略、脱敏、MediaStore 元数据。`[推断/待验证]`
3. **不要让 SMB 共享根目录默认落在 `/storage/emulated/0/Android/data` 等受保护目录**：那是 Android 明确要隔离的区域。`[已确认]`（scoped storage 设计意图）+`[推断/待验证]`（root 下行为）

---

## 7. 对 SMB 服务的系统限制

### 7.1 端口 < 1024

- Linux 把 1–1023 视为特权端口（`PROT_SOCK`），绑定需要 `CAP_NET_BIND_SERVICE`。root（uid 0）默认保有全部 capability，因此 **root 守护进程绑定 445/139 在能力层面没问题**。`[已确认]`，内核来源：[ip_unprivileged_port_start 引入提交](https://android.googlesource.com/kernel/common/+/4548b683b78137f8eadeb312b94e20bb0d4a7141)
- Magisk v30.7 changelog：「[MagiskSU] Do not drop capabilities by default, even when switching to non-root UIDs」，即 root 进程默认保留 capabilities；若曾用 `--drop-cap` 之类方式启动，需显式保留 `cap_net_bind_service`。`[已确认]`，引用 [Magisk Changelog](https://topjohnwu.github.io/Magisk/changes.html)
- 非 root 进程要绑低端口需把 `net.ipv4.ip_unprivileged_port_start` 设为 0；该 sysctl 是 per-network-namespace 的。**Android 默认值本次未核到**，不要假设为 0。`[已确认]`（sysctl 存在与语义）/`[推断/待验证]`（Android 默认值）
- 工程建议：默认用 445，提供「高位端口回退」（如 1445/4445），避免用户网络/策略限制时无法启动；端口选择应写进配置而不是硬编码。`[推断/待验证]`

### 7.2 SELinux

- Magisk 侧：`magisk` 域 permissive、`magisk_file` 对所有域可访问、`magisk_log_file` 仅 zygote/magisk 可读；策略由 `magiskinit` 在 pre-init 注入（[magisk.md](./magisk.md) §5）。`[已确认]`
- KernelSU 侧：模块文档的 `initrc` 示例用 `seclabel u:r:ksu:s0` 注册服务，说明 `u:r:ksu:s0` 是模块服务的预期上下文；但该域的具体权限集合官方未列。`[已确认]`（示例）/`[推断/待验证]`（权限集合）
- 需要新增策略时写 `sepolicy.rule`（仅增不减、pre-init 注入、勿擦 cache/metadata），不要 `setenforce 0`。`[已确认]`（Magisk 文档，[magisk.md](./magisk.md) §5.3）
- **常见失败点**：SMB 二进制/配置/socket 放 `/data/adb/smbshare/`，若文件上下文仍是 `adb_data_file`，其他域的客户端（如 App 内的 WebUI 通过本地代理访问）会被拒；socket 的 label 与访问方 domain 必须同时正确。`[推断/待验证]`

### 7.3 私有目录与文件权限

- `/data/adb` 是 `700 root:root`，普通 App 无法进入；因此**不要把 WebUI 静态文件或 socket 放 `/data/adb/smbshare/` 让 App 直接打开**——除非给 App 侧做 SELinux 例外或用 TCP loopback。`[已确认]`（权限）+`[推断/待验证]`（方案取舍）
- 模块文件默认被安装器设为 `0644`（KernelSU 官方在 preinit 限制里写明；Magisk 同源推断），可执行程序需要显式 `set_perm ... 0755`。`[已确认]`（KernelSU）/`[推断/待验证]`（Magisk）
- 卸载/升级会替换模块目录（Magisk `modules_update`；KernelSU 热装把旧目录挪走、下次开机删除），**任何用户数据都不能放模块目录内**。`[已确认]`（KernelSU hot install 原文）+`[推断/待验证]`（Magisk 时序）

### 7.4 `/proc`

- 普通 App 读 `/proc/net/tcp*`、`/proc/<pid>/…` 受 SELinux 限制；root 域通常可读，但 `hidepid` 挂载参数与厂商差异需要实测。`[推断/待验证]`
- 端口占用检测应走 `/proc/net/tcp`、`/proc/net/tcp6`（并可选用 `ss`，但 Android 的 toybox `ss` 能力有限），注意**network namespace**：root shell 默认与 init 同 netns，但若被容器/隔离环境改动，`/proc/net` 内容会不同。`[推断/待验证]`
- 判断「端口是否被占用」不能只看 bind 是否成功：SMB 服务要显式处理 `EADDRINUSE` 并给出可读日志，而不是静默退出。`[推断/待验证]`

### 7.5 网络

- 绑定地址建议同时考虑 `0.0.0.0`、`::` 与仅 LAN 地址；IPv6 双栈在不同 ROM 上行为不同，需真机验证。`[推断/待验证]`
- Android 的 netd/iptables 规则可能阻止来自其他接口的入站连接（尤其热点/AP 模式），需要真机在 Wi-Fi 与热点两种场景下分别验证。`[推断/待验证]`
- 屏幕关闭/Doze 不影响 root 守护进程的 socket 监听，但 Wi-Fi 省电可能导致连接中断；这是网络层行为，与本模块的存储问题相互独立。`[推断/待验证]`

---

## 8. Android 15 / 16 / 17 兼容性风险清单与真机验证项

| # | 风险项 | 影响 | 证据 | 真机验证方法（只读/低风险） |
|---|---|---|---|---|
| 1 | **16 KB page size** | 本模块若带 native 二进制（如 `smbd`），需按 16 KB 对齐编译，否则在新设备上无法加载/崩溃 | AOSP 自 Android 15 支持 16 KB 设备；Play 对 targetSdk≥35 的应用要求 64 位设备支持 16 KB，2027-02-01 起不支持的更新无法发布（[page-sizes](https://developer.android.com/guide/practices/page-sizes)）`[已确认]` | `getconf PAGE_SIZE`；`zipalign -c -P 16 <apk/zip>`（若有 APK）；自建二进制用 `readelf -l` 看 LOAD 对齐 `[推断/待验证]` |
| 2 | **Android 16 QPR2 的 sepolicy 二进制格式变化** | 依赖自定义 sepolicy 的模块在旧 Magisk 上可能无法注入规则；Magisk 在 v30.3（MagiskPolicy）与 v30.7（MagiskInit）才支持 | Magisk changelog v30.3/v30.7 `[已确认]` | 读取 minor SDK 属性（属性名待确认）；若提供 `sepolicy.rule`，确认 Magisk ≥ v30.3 `[推断/待验证]` |
| 3 | **API 36.1（Android 16 QPR2）** | 版本判断若只比较整数 36 会误判；SDK 级别出现小数 | [build-numbers](https://source.android.com/docs/setup/reference/build-numbers) `[已确认]` | `getprop ro.build.version.sdk` + `ro.build.version.release` 与 minor 属性（属性名待确认） |
| 4 | **FUSE 挂载晚于 `sys.boot_completed`** | 服务启动时 share 根不存在；日志出现 `no such file or directory` | vold 提交（FUSE daemon 需解锁）`[已确认]` | 重启后立刻 `ls /storage/emulated/0`，对比解锁前后 |
| 5 | **`su` 隔离 mount namespace** | 从终端手动启动的服务看不到后续挂载的 `/storage`；重启后由 `service.sh` 启动则正常 | Magisk changelog v21.0 `[已确认]`；对守护进程的影响 `[推断/待验证]` | 比较 `/proc/self/ns/mnt` 与 `/proc/1/ns/mnt`；对比手动启动与开机启动 |
| 6 | **HSUM（Headless System User Mode）设备** | 多用户/系统用户模型变化可能影响「用户 0」假设；Magisk v31.1 才正式支持 HSUM 设备 | Magisk changelog v31.1 `[已确认]` | 手机一般不涉及；若面向车机/特殊设备需单独验证 `[推断/待验证]` |
| 7 | **Android 17 生态滞后** | 厂商 ROM 适配、Root 方案适配可能晚于 AOSP；Magisk v31.0 才支持 Android 17 的 Zygisk | Magisk changelog v31.0 `[已确认]` | 确认设备实际 `sdk` 与 KernelSU/Magisk 版本；Android 17 机上跑全量测试 |
| 8 | **存储策略差异（`/storage` 视图）** | 同一路径在解锁前后、不同用户下的可见性不同；多用户时 `/storage/emulated/<userId>` | [多用户测试文档](https://source.android.com/docs/devices/admin/multi-user-testing) `[已确认]`（路径表） | 创建第二用户后验证 `ls /storage/emulated/10` 行为 |
| 9 | **`/proc` 可读性差异** | 端口/进程检测在 root 域与 App 域结果不同 | `[推断/待验证]` | `ls -l /proc/net/tcp` 与 `ss -lntp` 对比 |
| 10 | **SELinux 域能力差异（KernelSU vs Magisk）** | 同一服务在两种 Root 方案下权限表现可能不同 | KernelSU 只给示例上下文 `[已确认]`，域权限未列 `[推断/待验证]` | 用 `dmesg` 过滤 `avc`；`ls -Z` 检查关键文件 |

**真机验证优先级（建议）**：4（FUSE 时序）→ 5（namespace）→ 1（page size）→ 2（sepolicy）→ 10（SELinux 域）→ 其余。

---

## 9. 对本项目的直接含义（Android 侧）

1. **服务启动位置**：`service.sh`（Magisk）/ `service.sh` 或 `boot-completed.sh`（KernelSU），配合「等 `sys.boot_completed` + 轮询存储就绪」；`post-fs-data.sh` 只做极轻量的目录/权限修复。`[已确认]`（脚本语义）+`[推断/待验证]`（等待策略）
2. **持久化位置**：配置/日志/PID/socket 放 `/data/adb/smbshare/`（DE，解锁前可用，权限 0700 或 0750）；**用户数据（share 根）绝不放在模块目录或 `/metadata`**。`[推断/待验证]`
3. **共享根选择**：默认 `/storage/emulated/0`（用户可见、MediaProvider 一致），并提供 `/data/media/0` 作为「解锁前/兼容」可选路径；两者不可混用同一份缓存。`[推断/待验证]`
4. **边界**：不共享 `/data/data`、`/data/adb`、`/data/system`、`/data/misc`；路径必须 canonicalize 后校验在根内。`[推断/待验证]`
5. **端口**：默认 445，允许回退高位端口；不要假设非 root 能绑低端口。`[推断/待验证]`
6. **不要写 `setenforce 0`**；要权限就 `sepolicy.rule`。`[已确认]`（[magisk.md](./magisk.md) §5.2）

---

## 10. 证据不足 / 待上机验证

1. **Android 17 的官方发布日期与 AOSP 标签**：只核到「已正式发布 + API 37」与 Magisk changelog；发布日期（媒体记 2026-06-16）未在 Google 官方页核实。`[推断/待验证]`
2. **`/data/media/0` 在解锁前的真实可读性**：CDD/AOSP 只到「DE 可用」这一层，具体到 `/data/media` 的 SELinux label 与 root 可读性未逐字核到。
3. **FUSE 挂载相对 `sys.boot_completed` 的实际延迟**：vold 提交说明了「依赖用户解锁」，但没有给出典型设备上的时间差；不同 OEM 的解锁时机（人脸/指纹）差异大。
4. **root（uid 0）经 FUSE 访问 `/storage/emulated/0/Android/data` 的行为**：无官方文档；需实测（见 storage.md 测试矩阵）。
5. **`/proc` 的 hidepid 设置与 `/proc/net` 可见性**：本次未取得 AOSP/CDD 原文。
6. **`net.ipv4.ip_unprivileged_port_start` 在 Android 的默认值**：只确认 sysctl 存在与语义，默认值未核到。
7. **KernelSU `u:r:ksu:s0` 的域权限与是否 permissive**：官方模块文档只给出示例上下文。
8. **Android 16 QPR2 的 sepolicy 格式变化细节与 KernelSU 侧适配**：只确认 Magisk 的适配版本，KernelSU 侧未取证。
9. **本次网络无法直接抓取 `source.android.com` / `developer.android.com` / `android.googlesource.com` / `cs.android.com`**：AOSP 文档正文均通过 `.google.cn` 镜像（截断）或搜索快照获得；需要行号级证据的结论应在可访问环境复核。

---

## 11. 来源

**Android 平台官方**

- Android 15 版本概览：https://developer.android.com/about/versions/15
- Android 16 版本概览：https://developer.android.com/about/versions/16
- Android 17 版本概览：https://developer.android.com/about/versions/17
- SDK 构建号与 API level（含 Android 16 QPR2 = API 36.1）：https://source.android.com/docs/setup/reference/build-numbers
- 16 KB page size：https://developer.android.com/guide/practices/page-sizes
- Direct Boot / CE 与 DE 存储（AOSP 文档源文件）：https://android.googlesource.com/platform/frameworks/base/+/682c24e/docs/html/preview/features/direct-boot.jd
- Android CDD 9.9 数据存储加密（DE/CE 访问时机）：https://source.android.com/docs/compatibility/14/android-14-cdd#9_9_data_storage_encryption
- Scoped storage 与 FUSE：https://source.android.com/docs/core/storage/scoped
- SDCardFS 弃用：https://source.android.com/docs/core/storage/sdcardfs-deprecate
- 存储总览：https://source.android.com/docs/core/storage
- 多用户存储路径表：https://source.android.com/docs/devices/admin/multi-user-testing
- AOSP init 文档：https://android.googlesource.com/platform/system/core/+/refs/heads/main/init/README.md

**AOSP vold 提交（FUSE 时序与 `/data/media` 关系）**

- FUSE 守护进程需用户解锁：https://android.googlesource.com/platform/system/vold/+/cf5916f3fa9e5eabc143dfb3669198e9ba3c3634
- `/dev/fuse` 挂载到 `/mnt/user/<userid>/<volumeid>`：https://android.googlesource.com/platform/system/vold/+/3623a212e305f022a2fe279a4c13ec2a6513d6e7
- 多路径指向同一 `/data/media`：https://android.googlesource.com/platform/system/vold/+/73e3010a252e22224282a7de6ba204487c543481
- `/data/media/userId` 的 AID_MEDIA_RW 与默认 ACL：https://android.googlesource.com/platform/system/vold/+/8688eb4f47bd589feb2e885836b34c5991158845

**Root 方案官方文档**

- Magisk Internal Details（booting 流程、`/data/adb`、SELinux 域）：https://topjohnwu.github.io/Magisk/details.html
- Magisk Changelog（v31.1 / v31.0 / v30.7 / v30.3 / v28.0 / v21.2 / v21.0）：https://topjohnwu.github.io/Magisk/changes.html
- Magisk Developer Guides（模块脚本语义）：https://topjohnwu.github.io/Magisk/guides.html
- KernelSU Module guide（boot scripts、initrc、preinit、hot install、busybox、`u:r:ksu:s0` 示例）：https://kernelsu.org/guide/module.html

**其他**

- 内核 sysctl `net.ipv4.ip_unprivileged_port_start` 引入提交：https://android.googlesource.com/kernel/common/+/4548b683b78137f8eadeb312b94e20bb0d4a7141
- LPC 2021「FS stacking with FUSE: performance issues and mitigations」（Android FUSE/passthrough）：https://lpc.events/event/11/contributions/1048/attachments/838/1662/2021%20LPC_%20FS%20stacking%20with%20FUSE_%20performance%20issues%20and%20mitigations.pdf

**社区来源（仅现象描述，不作依据）**

- `/sdcard -> /storage/self/primary -> /mnt/user/0/primary -> /storage/emulated/0` 的链接链观察（含 `init.rc` 引用）：https://stackoverflow.com/feeds/question/61700922
- Android 上低端口与 `ip_unprivileged_port_start` 的实践帖：http://bbs.c3.wuyou.net/forum.php?mod=viewthread&action=printable&tid=438193
