# Android 共享存储调研（FUSE / /storage / /data/media · Root SMB 模块）

- **调研日期**：2026-11-14
- **方法**：只读检索官方一手资料并按证据强度标注。优先 AOSP 官方文档（`source.android.com` / `android.googlesource.com` 的 vold 提交说明）、Android Developer、Magisk 官方文档、KernelSU 官方文档；社区资料只在官方缺失时用于「现象描述」，且单独标注。
- **范围**：`/storage/emulated/0`、`/mnt/user/0/emulated/0`、`/data/media/0`、`/sdcard` 的关系；sdcardfs → FUSE 的历史；root 经 FUSE 访问共享存储的权限模型；直接访问 `/data/media/0` 的可行性；文件操作测试矩阵；ext4/f2fs、FUSE 性能、`O_DIRECT`/`fallocate`/稀疏文件/权限位；共享安全边界；卸载/升级不得删除用户数据的设计含义。
- **不覆盖**：MediaStore API 用法；Samba 协议实现细节；`/storage/emulated/0` 之外的外置 SD 卡 / USB-OTG（adoptable storage 仅少量提及）；本项目 Go 代码。
- **可信度说明**：
  - `[已确认]`：可直接读到官方原文（AOSP 文档、vold 提交说明、官方 changelog、官方文档页），可作为实现依据。
  - `[推断/待验证]`：官方未明说，由已确认机制推导，或只有次级来源；**必须真机验证后才能写进实现**。
  - `[社区来源]`：论坛/问答/第三方整理，只用于描述现象。
  - **检索环境限制**：本次 `source.android.com`、`developer.android.com`、`android.googlesource.com`、`cs.android.com` 直接抓取失败（TLS/连接被阻断）；可用通道为 `source.android.google.cn` 镜像（正文被截断）、搜索索引快照，以及 `topjohnwu.github.io`、`kernelsu.org` 原文。涉及 AOSP 文档正文的条目已相应降级为 `[推断/待验证]`。

> 真机基线（用户提供背景，非本次实测）：OnePlus PJA110，Android 16 / API 36，arm64-v8a，KernelSU 3.3.1，SELinux Enforcing，`su` 上下文 `u:r:ksu:s0`。本文出现的命令均为**待执行**的验证手段，不代表已得到结果。

---

## 1. 结论摘要（先看这 10 条）

1. **三层路径，两种视图**：`/data/media/0` 是**真实数据**（下层文件系统）；`/mnt/user/0/emulated/0` 是 **vold 挂载的 FUSE 视图**；`/storage/emulated/0` 是用户可见路径；`/sdcard` 是指向用户主存储的符号链接。root 直接读 `/data/media/0` 与读 `/storage/emulated/0` **不是同一件事**。`[已确认]`（各层存在与 FUSE 挂载点）+`[推断/待验证]`（链接链细节）
2. **Android 11 起用 FUSE 取代 sdcardfs**：官方明确「SDCardFS is deprecated on devices launching with Android 11 or higher and running kernel 5.4 or higher」；新 FUSE 实现主要服务于位置信息脱敏等策略。`[已确认]`
3. **FUSE 守护进程 = MediaProvider 进程**，**必须等用户解锁**才启动，因此 `/storage/emulated/0` 在解锁前可能根本不存在（不是「空目录」，而是未挂载）。`[已确认]`
4. **FUSE 挂载的职责是「策略网关」**：Scoped storage 官方说明 MediaProvider 在用户态审查文件操作，可 allow / deny / redact。也就是说，**root 经 `/storage` 访问时仍然受这层策略影响**（root 是否被放行需实测）。`[已确认]`（机制）+`[推断/待验证]`（root 行为）
5. **`/data/media/0` 属于 DE 域，解锁前 root 理论上可读写**；但直接写入会**绕过 MediaProvider 的策略、脱敏和 MediaStore 元数据**，并且 FUSE 守护进程创建文件时会把 uid/gid 设为自身与 `AID_MEDIA_RW`（默认 ACL 保证），直接写入的文件若权限/组不匹配，后续可能对 App 不可见或只读。`[已确认]`（DE 语义、ACL 提交说明）+`[推断/待验证]`（直接写入的后果）
6. **共享根的默认选择应是 `/storage/emulated/0`**（用户可见、策略一致、MediaStore 一致）；`/data/media/0` 只作为「解锁前可用/兼容」的可选路径，且**不能与 FUSE 视图混用同一缓存**。`[推断/待验证]`
7. **性能上 FUSE 有额外开销，但 Android 用 FUSE passthrough 缓解**：打开文件时把 read/write/mmap 转发给底层文件系统（用守护进程凭据）。实际吞吐必须实测，本文不预设数字。`[已确认]`（passthrough 存在）+`[推断/待验证]`（具体倍率）
8. **文件语义存在「类 FAT」限制**：符号链接、硬链接、`fallocate`、`O_DIRECT`、稀疏文件在 FUSE 视图下都可能不被支持或行为不同；必须按测试矩阵逐项验证，**不能假设 ext4 语义直通**。`[推断/待验证]`
9. **SMB 共享的安全边界必须显式实现**：路径穿越、符号链接逃逸、mount 逃逸、Unicode/大小写碰撞、非法 UTF-8；同时**禁止共享 `/data/data`、`/data/adb`、`/data/system`、`/data/misc`** 等目录。`[推断/待验证]`（设计结论）+`[已确认]`（这些目录的系统定位）
10. **卸载/升级与用户数据必须解耦**：Magisk 用 `modules_update` 暂存、重启合并；KernelSU 热装时旧模块目录被挪开、下次开机才删。用户数据只能放在模块目录之外（`/data/adb/smbshare/`），且 `uninstall.sh` **绝不能**删除用户选择的 share 根。`[已确认]`（KernelSU hot install 原文、Magisk 目录语义）+`[推断/待验证]`（Magisk 时序细节）

---

## 2. 路径关系：`/storage/emulated/0`、`/mnt/user/0/emulated/0`、`/data/media/0`、`/sdcard`

### 2.1 拓扑（按证据强度分层的描述）

```
下层文件系统（真实数据）
  /data/media/0                     ← 用户 0 的媒体/共享数据；ext4 或 f2fs；DE 加密
        ▲
        │  FUSE 守护进程（MediaProvider 进程）把下层文件「投射」成用户视图
        │  挂载点：/mnt/user/0/emulated（/dev/fuse）
  /mnt/user/0/emulated/0/...        ← FUSE 视图（每用户一层）
        ▲
        │  用户可见路径（具体是 bind mount 还是符号链接，见 2.3）
  /storage/emulated/0/...           ← App / shell / SMB 服务最常用的路径
        ▲
  /sdcard → /storage/self/primary → …/storage/emulated/0
```

官方可确认的锚点：

- emulated 卷的 FUSE 挂载点是 `/mnt/user/<userid>/<volumeid>`：vold 提交原文「Mount /dev/fuse on /mnt/user/<userid>/<volumeid>」。`[已确认]`
- `/mnt/user/0/emulated/0/Android` 与 `/mnt/user/10/emulated/0/Android` 指向同一 `lower filesystem` 路径 `/data/media/0/Android`：vold 提交说明。`[已确认]`
- 多用户存储路径表把 `/storage/emulated/{userId}` 标为「共享内部存储空间」、`/data/media/{userId}` 标为「用户媒体数据」。`[已确认]`，引用 [多用户测试文档](https://source.android.com/docs/devices/admin/multi-user-testing)
- `/sdcard` 在 Android 上是 `/storage/self/primary` 的链接、而 `/storage/self/primary` 再指向 `/mnt/user/0/primary`（该链在某次观察中继续指向 `/storage/emulated/0`）。`[社区来源]`，链接链需真机 `readlink -f` 复核；本文标 `[推断/待验证]`。

### 2.2 为什么会有 `primary` / `self` 这些别名

- `/storage/self/primary` 是**按进程**解析的「当前用户主存储」别名，供 App 在不知道 userId 的情况下使用；`/storage/emulated/<userId>` 则是按用户固定路径。`[推断/待验证]`（AOSP 的 `StorageManager` 语义，本次未取到原文）
- 旧版本遗留的 `primary` 路径在 Android 10/11 的重构中逐步被 FUSE 视图替代；不同 OEM 仍可能保留差异。`[推断/待验证]`

### 2.3 真机确认方法（只读命令）

```sh
# 各路径实际身份
readlink /sdcard ; readlink /storage/self/primary ; readlink -f /storage/emulated/0
# 挂载关系与类型（fuse? bind?）
grep -E ' /(storage|mnt/user|data/media)' /proc/mounts
mount | grep -iE 'fuse|emulated'
# 下层数据与视图是否同一 inode（同 inode 说明是同一文件）
stat -c '%d:%i %n' /data/media/0/Android /storage/emulated/0/Android 2>/dev/null
```

预期输出需上机记录，本文不预设。`[推断/待验证]`

### 2.4 sdcardfs → FUSE 的历史

| 时期 | 机制 | 说明 |
|---|---|---|
| Android 4.4 – 10 | sdcardfs（部分设备）/ FUSE（早期 sdcard 守护进程） | 官方 storage 文档记录了传统模拟存储与 fuse_sdcard 服务；sdcardfs 提供大小写不敏感层与存储追踪。`[已确认]`（历史文档描述）/`[推断/待验证]`（各版本默认实现） |
| Android 11+（kernel 5.4+） | **FUSE** | 官方 SDCardFS 弃用页：「SDCardFS is deprecated on devices launching with Android 11 or higher and running kernel 5.4 or higher」；VTS 不再允许 SDCardFS 挂载。`[已确认]` |
| Android 11+ 但 kernel 4.19 及以下 | 仍可继续用 sdcardfs | 官方：「may continue to use SDCardFS, but additional support is not provided」。`[已确认]` |
| FUSE 时代的大小写不敏感 | 交给文件系统层处理 | 官方原文：Linux 文件系统特性被用来达到与 SDCardFS 类似的效果，「case-insensitive handling is now done directly in the filesystem」。`[已确认]`（表述）/`[推断/待验证]`（在 emulated 卷上的实际表现） |

---

## 3. root 经 FUSE 访问共享存储的权限模型

### 3.1 三层权限叠加

| 层 | 作用 | 证据 |
|---|---|---|
| Linux VFS / FUSE 内核层 | 把路径操作转发给 FUSE 守护进程；挂载参数（`default_permissions`、`allow_other` 等）决定内核是否提前做权限检查 | `[推断/待验证]`（本次未取到 MediaProvider 的实际挂载参数） |
| FUSE 守护进程（MediaProvider） | 按调用方 uid/gid 与策略决定 allow / deny / redact；Scoped storage 官方页明确该机制 | [Scoped storage](https://source.android.com/docs/core/storage/scoped) `[已确认]` |
| 下层文件系统 + SELinux | `/data/media` 上真实的 fs 权限、ACL、SELinux label | vold 提交（AID_MEDIA_RW 默认 ACL）`[已确认]`；label 细节 `[推断/待验证]` |

### 3.2 已确认的关键细节

- FUSE 守护进程创建文件时：**文件的 uid/gid 会被设为守护进程的 uid，gid 通过默认 ACL 保持为 `AID_MEDIA_RW`**，以保证持有 `AID_MEDIA_RW` 的系统应用仍可写。原文：「files created by the FUSE daemon will have their uid/gid set to the uid of the FUSE daemon; to ensure these files stay writable to other system applications that have AID_MEDIA_RW, use a default ACL to make sure the gid stays AID_MEDIA_RW」。`[已确认]`，引用 [vold 提交](https://android.googlesource.com/platform/system/vold/+/8688eb4f47bd589feb2e885836b34c5991158845)
- 同一提交还说明：在没有 sdcardfs 的设备上，**FUSE 守护进程看到的是原始文件系统及其权限**。`[已确认]`
- FUSE passthrough：打开文件时把 `FUSE_PASSTHROUGH_OPEN` 与下层 fd 交给内核，之后的 read/write/mmap 直接走下层文件系统（使用守护进程凭据）。`[已确认]`（LPC 2021 材料）

### 3.3 root（uid 0）经 FUSE 的行为：需要实测的清单

MediaProvider 的 FUSE 策略按 uid 判定，root 是否被当作特权用户放行**没有官方文档**。以下行为都必须实测：

| 场景 | 待验证问题 | 测试方法 |
|---|---|---|
| 读普通文件 | root 经 `/storage/emulated/0/...` 能否读任意用户文件 | `su -c 'cat /storage/emulated/0/DCIM/...'` |
| 写普通文件 | 是否允许创建/修改 | `su -c 'touch /storage/emulated/0/xxx'` |
| `/Android/data` | root 是否被拒（App 是被拒的） | `su -c 'ls /storage/emulated/0/Android/data'` |
| `/Android/obb` | 同上 | `su -c 'ls /storage/emulated/0/Android/obb'` |
| 跨用户 | root 访问 `/storage/emulated/10` | 建第二用户后执行 |
| 脱敏 | 照片 EXIF（GPS）是否被 FUSE 改写 | 对同一文件分别读 `/storage/emulated/0` 与 `/data/media/0`，比对大小与 EXIF |

结论必须以真机输出为准，`[推断/待验证]`。

### 3.4 SELinux 上下文

- **真实文件与 FUSE 视图的 label 很可能不同**：真实文件很可能是 `media_rw_data_file`，FUSE 挂载点/视图带 FUSE 相关类型；**本次未取到逐字原文**，故具体类型名不写死，需真机 `ls -Z` 记录。`[推断/待验证]`
- Magisk 的 `magisk` 域为 permissive（[magisk.md](./magisk.md) §5.1），KernelSU 的 `u:r:ksu:s0` 权限集合官方未列。`[已确认]`（Magisk）/`[推断/待验证]`（KernelSU）
- 需要额外权限时写 `sepolicy.rule`（仅增不减、pre-init 注入），不要 `setenforce 0`。`[已确认]`

---

## 4. 直接访问 `/data/media/0`：可行性与限制

### 4.1 可行性

- `/data/media/0` 在 `/data` 挂载后即可访问（DE 域），**不需要用户解锁**；CE 存储（`/data/data`、`/data/user/0`）则必须解锁。`[已确认]`（DE/CE 语义，见 [android.md](./android.md) §3.2）+`[推断/待验证]`（root 在解锁前读 `/data/media/0` 的实际结果）
- 多用户下真实路径是 `/data/media/<userId>`；用户 0 是 `/data/media/0`，其他用户 `>/data/media/10` 等。`[已确认]`（多用户路径表）

### 4.2 限制与风险（按严重程度）

1. **绕过策略**：Scoped storage 与 FUSE 的 allow/deny/redact 都被绕过。对照片类文件，**GPS 等元数据不会被脱敏**，SMB 客户端可拿到原图。这是安全问题，不只是功能问题。`[推断/待验证]`
2. **绕过 MediaStore**：直接写入的文件不会自动进 MediaStore，媒体扫描可能延迟或需要手动触发；依赖 MediaStore 的客户端（相册、MTP）可能看不到。`[推断/待验证]`
3. **权限/属主/ACL 不一致**：FUSE 创建文件时 uid 为守护进程、gid 为 `AID_MEDIA_RW`（默认 ACL）；root 自己写入的文件若属主/组/ACL 不同，可能对 App 侧不可见或只读，也可能被 MediaProvider 拒绝索引。`[已确认]`（FUSE 侧行为）+`[推断/待验证]`（root 直接写入的后果）
4. **SELinux label**：直接写入需要正确的 label；label 不对会让 MediaProvider/其他进程无法读取。`[推断/待验证]`
5. **`Android/data` 语义差异**：`/data/media/0/Android/data/<pkg>` 是 App 专属数据，root 直接读写会破坏「卸载 App 时清理」等语义。`[推断/待验证]`
6. **并发一致性**：下层文件系统与 FUSE 视图同时读写同一文件时，缓存/直通行为可能导致「看不到对方刚写的文件」（FUSE 属性和数据缓存）。`[推断/待验证]`

### 4.3 设计建议

- 若必须支持「解锁前可用」，提供**显式的** `share_root` 选项，二选一：
  - `emulated`：`/storage/emulated/0`（推荐默认；策略一致、MediaStore 一致）
  - `media_raw`：`/data/media/0`（兼容解锁前；需在文档中说明脱敏/MediaStore 风险）
- **两种视图不要混用同一缓存/索引**：同一文件在两个路径下的 inode/大小/时间戳可能不同，缓存键要带上「访问路径类型」。`[推断/待验证]`
- 写入时尽量保持 `AID_MEDIA_RW` 组与默认 ACL；必要时对新建文件 `chown root:media_rw` + 复制 ACL（需先确认设备上 `media_rw` 的 GID 数值）。`[推断/待验证]`

---

## 5. 文件操作测试矩阵

> 下表是**待执行**的测试计划，不含结果。所有行都必须在真机上跑两遍：一遍走 `/storage/emulated/0`（FUSE 视图），一遍走 `/data/media/0`（直连），再对比。判定标准一栏写「通过/失败」的可观测条件。

### 5.1 基础操作

| # | 操作 | 路径/参数 | 关注点 | 判定标准 |
|---|---|---|---|---|
| 1 | create | 新建文件与目录 | 权限位、属主、SELinux label、是否触发媒体扫描 | 操作返回 0 且 `stat` 显示预期属主/模式 |
| 2 | read | `cat` / `dd` 读取 | 内容完整、无 0 字节截断 | 与源文件 sha256 一致 |
| 3 | write | 覆盖写、追加写 | 是否允许、是否原子、`fsync` 行为 | 重新读取内容一致 |
| 4 | delete | 删除文件/目录 | 递归删除、`EBUSY`（被 SMB 客户端打开时） | 删除后路径不存在，服务不崩溃 |
| 5 | rename | 同目录/跨目录 | `rename(2)` 是否原子；跨 share 是否 `EXDEV` | 名称变化且内容不变 |
| 6 | move | 跨目录/跨文件系统 | `/storage` ↔ `/data` 应为 `EXDEV`，服务需回退为 copy+delete | 不丢数据、无部分文件残留 |
| 7 | traverse | 深目录（>32 层）/大量 `..` | 路径长度、`ELOOP`、栈溢出 | 正常返回或明确错误，不崩溃 |
| 8 | stat | 文件/目录 | 大小、mtime、mode、uid/gid | 与 `ls -Z` 一致 |

### 5.2 规模

| # | 规模 | 关注点 | 判定标准 |
|---|---|---|---|
| 9 | 1 MB | 小文件往返、`fsync` 开销 | 校验和一致；时延可接受 |
| 10 | 100 MB | 持续吞吐、连接中断恢复 | 校验和一致；可续传/重传 |
| 11 | 1 GB | 大文件内存占用（流式读写，不整块加载） | 进程 RSS 不随文件线性增长 |
| 12 | **4 GB+** | 32 位偏移问题、SMB 客户端协商、底层 fs 上限 | 完整读写；大小与 sha256 一致 |
| 13 | 1000+ 小文件 | 目录枚举、inode 数、SMB 列目录性能 | 全部创建成功；列目录不超时 |
| 14 | 并发 | 多客户端同时读写同一/不同文件 | 无数据损坏；锁语义符合预期 |

> 说明：4 GB+ 在 ext4/f2fs 上均受支持（上限远大于此），风险主要来自 **32 位 `off_t`**、SMB 协议的 offset 宽度、以及客户端缓存。`[推断/待验证]`

### 5.3 特殊语义

| # | 场景 | 关注点 | 判定标准 |
|---|---|---|---|
| 15 | 符号链接 | `/storage` 是否允许 `symlink(2)`；若允许，指向根外的链接必须被 SMB 服务拒绝 | 越界链接不可下载/上传 |
| 16 | 硬链接 | FUSE 视图是否支持 `link(2)` | 明确成功/失败，不静默降级 |
| 17 | 大小写 | `A` 与 `a` 是否共存；重命名大小写 | 与设备实际策略一致，服务不误删 |
| 18 | UTF-8 / 非法字符 | 中文、emoji、非法 UTF-8 字节、NUL、`/` | 非法名拒绝；合法 UTF-8 往返一致 |
| 19 | Windows 非法名 | `CON`、`NUL`、`:`、`\`、`*`、`?` 等 | 服务器明确拒绝并返回可读错误 |
| 20 | 磁盘满 | `ENOSPC` 时的写入、临时文件清理 | 返回 `ENOSPC`，不产生半截文件（或能清理） |
| 21 | 只读 | share 配置为只读、目录 `chmod` 为只读 | 写操作明确失败；不破坏原文件 |
| 22 | 稀疏文件 | `truncate` / `seek` 写入 | 大小/内容正确（若支持） |
| 23 | `fallocate` | `fallocate -l` 是否 `EOPNOTSUPP` | 明确结果；服务不因此崩溃 |
| 24 | `O_DIRECT` | `dd iflag=direct oflag=direct` | 明确结果；失败时正确回退 |

> 「磁盘满」的**安全测试法**：不要直接填满整机 `/data`（可能导致系统异常）。优先在 `/data/adb/smbshare/test/` 下用 loop 设备挂载一个小的 ext4 镜像做隔离测试；loop 设备在 Android 上是否可用需先验证。`[推断/待验证]`

---

## 6. 文件系统、FUSE 性能与语义

### 6.1 `/data` 的文件系统

- Android 支持 ext4 与 f2fs 作为 `/data` 文件系统；具体设备用哪个需真机确认：`mount | grep ' /data '` 或 `grep ' /data ' /proc/mounts`。`[推断/待验证]`
- 两者都支持大文件、稀疏文件、`fallocate`、`O_DIRECT`（下层能力），但**经 FUSE 后这些能力是否透传，取决于守护进程是否实现对应操作**。`[推断/待验证]`
- 权限位：`/data/media` 目录由 vold 设置默认 ACL + `AID_MEDIA_RW` 组；直接 `chmod` 可能被 FUSE 侧合成视图掩盖。`[已确认]`（ACL 提交）+`[推断/待验证]`（FUSE 合成视图）

### 6.2 FUSE 性能

- FUSE 的固有成本：每次文件操作要从内核切到用户态守护进程；Android 用 **passthrough**（read/write/mmap 直接转发下层 fd）缓解。`[已确认]`（机制）
- 额外开销点：FUSE 属性缓存超时（`attr_timeout`/`entry_timeout`）、`fsync`、目录枚举（`readdir` 每个条目一次调用）、并发线程数。`[推断/待验证]`
- LPC 2021 的 FUSE 材料讨论了 Android 上「FS stacking with FUSE」的性能问题与缓解（passthrough、FUSE BPF 等方向）；具体数字以材料原文为准，**本文不复制未经核实的数值**。`[已确认]`（议题存在）
- 测量建议（不预设结果）：用 `dd`/`fio`（若有）分别在 `/data/media/0` 与 `/storage/emulated/0` 上测顺序读写、随机小 IO、元数据密集操作（create/unlink/stat），记录差异。`[推断/待验证]`

### 6.3 `O_DIRECT`、`fallocate`、稀疏文件

| 项 | FUSE 视图 | 直连 `/data/media/0` |
|---|---|---|
| `O_DIRECT` | 取决于 FUSE 挂载/守护进程；可能退化为 buffered 或 `EINVAL` | 由 ext4/f2fs 决定，通常支持 `[推断/待验证]` |
| `fallocate` | 守护进程可能不实现 → `EOPNOTSUPP` | 下层 fs 通常支持 `[推断/待验证]` |
| 稀疏文件 | 可能被物化（全零写入） | 下层 fs 支持稀疏 `[推断/待验证]` |
| 权限位/属主 | FUSE 合成（守护进程决定） | 真实 fs 权限/ACL |
| 大小写 | 取决于文件系统层策略 | 同左 |

---

## 7. 共享安全边界

### 7.1 必须实现的边界

1. **路径穿越**：对客户端路径做规范化（`realpath` 之后）与前缀校验；拒绝 `..`、绝对路径、NUL。`[推断/待验证]`（设计结论）
2. **符号链接逃逸**：打开文件用 `O_NOFOLLOW` 或 `openat2(RESOLVE_BENEATH)`（内核支持时）；对每级目录 `O_PATH|O_NOFOLLOW` 校验；拒绝指向 share 根之外的链接。`[推断/待验证]`
3. **mount 逃逸**：在 share 根内检测子挂载点（`/proc/self/mountinfo`），默认不跟随/不共享。`[推断/待验证]`
4. **`/proc` 边界**：`/proc/self/root`、`/proc/<pid>/cwd` 可以穿越到任意位置，任何「用户可指定路径」的入口必须拒绝 `/proc`、`/sys`、`/dev`。`[推断/待验证]`
5. **Unicode / 大小写碰撞**：NFC/NFD 归一化后同名、仅大小写不同的文件名，可能导致覆盖；需明确策略（拒绝或按字节比较）。`[推断/待验证]`

### 7.2 禁止共享的目录（黑名单）

| 目录 | 原因 |
|---|---|
| `/data/data`、`/data/user/0` | App 私有数据（CE），包含凭据、token、数据库；共享 = 全机 App 数据泄露 |
| `/data/adb` | Root 模块/授权数据库（`magisk.db` / `ksu`），共享等于把 root 交出去 |
| `/data/system` | 系统配置、`packages.xml`、锁屏信息 |
| `/data/misc`、`/data/local`、`/data/vendor` | 系统/厂商私有数据（Wi-Fi、keystore 等） |
| `/proc`、`/sys`、`/dev` | 内核接口，可导致权限提升/信息泄露 |
| `/metadata` | 加密密钥、OTA 状态（KernelSU 也在用） |

以上目录的系统定位属于常识性事实，`[推断/待验证]` 标注的是「黑名单必须由实现强制」这一设计结论。

### 7.3 建议的安全策略

- **默认只读**，写操作需要显式开启；
- **share 根白名单**，只允许在 `/storage/emulated/0` 与 `/data/media/0` 之下；
- 对 `/Android/data`、`/Android/obb` 默认排除；
- 认证：SMB 用户/密码 + 只读/读写权限分离；日志记录每次拒绝。

---

## 8. 卸载/升级不得删除用户数据的设计含义

### 8.1 已确认的模块生命周期

- **Magisk**：模块升级先落到 `/data/adb/modules_update`，**下次重启**才合并进 `/data/adb/modules`（官方理由：模块被挂载时其文件不安全被修改）；移除由 `remove` 标记驱动，重启时删除。`[已确认]`
- **KernelSU**：默认同样 staged 到 `modules_update`、重启生效；启用 hot install 时，`/data/adb/modules/<id>` 会被**替换**，旧目录被挪开、**下次开机才删除**，因此仍在用旧版本文件的进程能继续工作；`uninstall.sh` **不会**在更新时执行。`[已确认]`，引用 [KernelSU Module guide](https://kernelsu.org/guide/module.html)

### 8.2 三层数据分离（设计含义）

| 层 | 位置 | 生命周期 | 升级/卸载时的处置 |
|---|---|---|---|
| 代码/资源 | 模块目录（`/data/adb/modules/<id>`） | 随模块 | 会被整体替换/删除；**不能放用户数据** |
| 本模块状态 | `/data/adb/smbshare/`（config、日志、PID、socket） | 独立于模块文件 | 升级保留；卸载时可提示清理，但**不得静默删除** |
| 用户内容 | 用户选择的 share 根（`/storage/emulated/0/...` 或 `/data/media/0/...`） | 用户所有 | **永不删除**；`uninstall.sh` 只允许删除自己创建的文件 |

由此得到硬约束：

1. `uninstall.sh` 不得 `rm -rf` 任何用户路径；若必须清理，只能删除 `/data/adb/smbshare/` 下由本模块创建的已知文件，并且**先做路径规范化与白名单校验**（防止 `share_root` 被篡改成 `/data`）。`[推断/待验证]`
2. 升级流程必须假设「服务会被重启打断」：状态持久化、启动时自愈（清理残留 socket/PID），不要在升级脚本里做长时间操作。`[推断/待验证]`
3. 若提供「卸载时删除配置」选项，默认关闭，并在 UI 上明确提示。`[推断/待验证]`
4. KernelSU 热装替换模块目录时，**旧目录仍在**，不要在新版本里假设旧目录已消失；反之，Magisk 在重启合并时会有短暂窗口。`[已确认]`（KernelSU）/`[推断/待验证]`（Magisk 时序）

---

## 9. 真机验证清单（按优先级，均为待执行）

```sh
# P0 路径与挂载
grep -E ' /(storage|mnt/user|data)' /proc/mounts
mount | grep -i fuse
readlink -f /sdcard ; readlink -f /storage/emulated/0
getconf PAGESIZE ; getprop ro.build.version.sdk

# P0 解锁前/后的可见性（重启后立即执行，记录时间与输出）
su -c 'ls -la /storage/emulated/0'      # 预期解锁前 ENOENT/空
su -c 'ls -la /data/media/0'            # 预期解锁前可读

# P1 root 经 FUSE 的写权限与保护目录
su -c 'touch /storage/emulated/0/.smb_probe && rm /storage/emulated/0/.smb_probe'
su -c 'ls /storage/emulated/0/Android/data'

# P1 SELinux 与属主
su -c 'ls -Z /storage/emulated/0 | head' ; su -c 'ls -Z /data/media/0 | head'
su -c 'id ; getenforce ; dmesg | grep avc | tail'

# P2 语义
su -c 'ln -s /data/media/0 /storage/emulated/0/.lnk_probe'   # 是否允许 symlink
su -c 'fallocate -l 1G /storage/emulated/0/.falloc_probe'    # 是否 EOPNOTSUPP
su -c 'dd if=/dev/zero of=/storage/emulated/0/.dd_probe bs=1M count=8 oflag=direct'
```

每项都要记录：命令、时间点（解锁前/后）、返回码、输出、`dmesg` 中是否有 `avc denied`。本文不预设任何结果。

---

## 10. 证据不足 / 待上机验证

1. **`/sdcard` / `/storage/self/primary` / `/mnt/user/0/primary` 的精确链接链**：只掌握一次社区观察与 `init.rc` 片段，且随版本变化；需真机 `readlink`。
2. **`/storage/emulated/0` 与 `/mnt/user/0/emulated/0` 之间是 bind mount 还是符号链接**：vold 提交只确认 FUSE 挂在 `/mnt/user/<user>/<volume>`，用户可见路径的构造方式未逐字核到。
3. **root 经 FUSE 的放行规则**（尤其 `/Android/data`、`/Android/obb`）：无官方文档，必须实测。
4. **MediaProvider FUSE 的挂载参数**（`default_permissions`、`allow_other`、缓存超时）：未取到原文。
5. **FUSE 视图下 `fallocate`/`O_DIRECT`/稀疏文件/硬链接/符号链接的实际行为**：未知，必须逐项实测。
6. **大小写不敏感在 emulated 卷上的实际表现**：官方只说「移到文件系统层」，未说明 `/data` 是否启用 casefold。
7. **`/data` 的 ext4/f2fs 选择与挂载参数**：需要真机 `mount` 输出。
8. **FUSE 性能的具体倍率**：本次未取得可引用的官方基准；LPC 材料只讨论问题与缓解方向。
9. **`/data/media/0` 的 SELinux 类型名**：未取到逐字原文，未写死。
10. **本次无法直接抓取 `source.android.com` / `developer.android.com` / `android.googlesource.com` / `cs.android.com`**：AOSP 文档正文经镜像/快照获得，行号级证据需在可访问环境复核。

---

## 11. 来源

**AOSP / Android 官方**

- Scoped storage 与 FUSE（MediaProvider 在用户态审查文件操作）：https://source.android.com/docs/core/storage/scoped
- SDCardFS 弃用（Android 11+ / kernel 5.4+；FUSE 替代；大小写与追踪）：https://source.android.com/docs/core/storage/sdcardfs-deprecate
- 存储总览（模拟存储、外部存储历史）：https://source.android.com/docs/core/storage
- 存储配置示例（`fuse_sdcard` 服务、`EMULATED_STORAGE_SOURCE/TARGET`，历史参考）：https://source.android.com/docs/core/storage/config-example
- 多用户存储路径表（`/storage/emulated/{userId}`、`/data/media/{userId}`）：https://source.android.com/docs/devices/admin/multi-user-testing
- Direct Boot / CE 与 DE（AOSP 文档源文件）：https://android.googlesource.com/platform/frameworks/base/+/682c24e/docs/html/preview/features/direct-boot.jd
- Android CDD 9.9 数据存储加密（CE 需解锁）：https://source.android.com/docs/compatibility/14/android-14-cdd#9_9_data_storage_encryption
- AOSP init / rootdir（`symlink /storage/self/primary /sdcard` 的出处，需在可访问环境核对）：https://android.googlesource.com/platform/system/core/+/refs/heads/main/rootdir/init.rc

**AOSP vold 提交（FUSE 与 `/data/media` 的一手证据）**

- FUSE 守护进程需用户解锁：https://android.googlesource.com/platform/system/vold/+/cf5916f3fa9e5eabc143dfb3669198e9ba3c3634
- `/dev/fuse` 挂载到 `/mnt/user/<userid>/<volumeid>`：https://android.googlesource.com/platform/system/vold/+/3623a212e305f022a2fe279a4c13ec2a6513d6e7
- `/mnt/user/*/emulated/0/*` 与 `/data/media/0/*` 同源：https://android.googlesource.com/platform/system/vold/+/73e3010a252e22224282a7de6ba204487c543481
- `/data/media` 的 `AID_MEDIA_RW` 默认 ACL 与 FUSE 创建文件的 uid/gid：https://android.googlesource.com/platform/system/vold/+/8688eb4f47bd589feb2e885836b34c5991158845

**Root 方案官方文档**

- Magisk Internal Details（`/data/adb`、booting、SELinux 域）：https://topjohnwu.github.io/Magisk/details.html
- Magisk Changelog（`modules_update` 语义、模块行为变更）：https://topjohnwu.github.io/Magisk/changes.html
- KernelSU Module guide（hot install、旧模块目录延后删除、`uninstall.sh` 不在更新时执行、boot scripts、`/metadata`）：https://kernelsu.org/guide/module.html

**其他**

- LPC 2021「FS stacking with FUSE: performance issues and mitigations」（Android FUSE、passthrough、FUSE BPF）：https://lpc.events/event/11/contributions/1048/attachments/838/1662/2021%20LPC_%20FS%20stacking%20with%20FUSE_%20performance%20issues%20and%20mitigations.pdf

**社区来源（仅现象描述）**

- `/sdcard -> /storage/self/primary -> /mnt/user/0/primary -> /storage/emulated/0` 的链接链观察：https://stackoverflow.com/feeds/question/61700922
- 「模拟存储上符号链接不生效、需 bind mount」的实践帖：https://android.stackexchange.com/revisions/bfa54e5c-98b2-4d0c-9667-cd93a54463f5/view-source
