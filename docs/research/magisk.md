# Magisk 技术调研（Android 15/16/17 · arm64 · Root 模块）

- **调研日期**：2026-11-14
- **方法**：只读检索官方一手资料，逐条抓取并比对。主要来源为 Magisk 官方文档站（`topjohnwu.github.io/Magisk`）的 guides / details / tools / faq / install / changes 页面，以及 Magisk 官方仓库中 `native/src/sepolicy/rules.rs` 的原文（经 jsDelivr 镜像读取，内容与 GitHub 仓库同源）。
- **范围**：Magisk 模块规范、安装器行为、Boot Scripts、SELinux 策略、WebUI 有无、Android 15/16/17 兼容性、Magisk 识别与卸载清理。**不覆盖**：Magisk App 的 UI 开发、Zygisk 模块 API、boot image 打包细节（仅在与本模块相关处提及）。
- **可信度**：
  - 标 `[已由官方来源确认]` 的结论可在文末链接原文中直接读到，可直接作为实现依据。
  - 标 `[推断/待验证]` 的结论是官方文档未明说、由官方行为推导，或来自社区资料，**必须上机验证后才能写进实现**。
  - 本次调研期间 `github.com` 与 `raw.githubusercontent.com` 在本机网络不可达，仓库源码改由 jsDelivr 镜像读取；`source.android.com` 不可达，因此涉及 AOSP/CDD 的表述一律降级为推断。所有未能读到原文的点已汇入文末「证据不足」。

---

## 1. 结论摘要（先看这 10 条）

1. Magisk **没有内建的 WebUI 机制**。模块规范中的 UI 钩子只有 `action.sh`（App 内控制台执行），没有 `webroot`、没有端口、没有 HTTP 服务。`[已由官方来源确认]`
2. Magisk 的 `magisk` domain（`u:r:magisk:s0`）被官方定义为「unconstrained, permissive domain」，所有 root shell 都在其中运行；源码里显式写了 `permissive([proc])`。**这是 Magisk 下的最大便利，也是不可移植的假设。** `[已由官方来源确认]`
3. `post-fs-data` 阶段是**阻塞**的，且 Magisk 会把该阶段限制在 40 秒内（其中脚本阶段 35 秒）；`late_start service` 阶段**非阻塞**，官方推荐绝大多数逻辑放这里。`[已由官方来源确认]`
4. 模块内必须用 `MODDIR=${0%/*}` 定位自身目录，禁止硬编码路径；所有 Boot Script 跑在 BusyBox `ash` 的 Standalone 模式下，`PATH` 不影响 applet 解析。`[已由官方来源确认]`
5. 想看「开机完成」，官方给的做法是 `resetprop -w sys.boot_completed 0`；在 `post-fs-data` 里用 `setprop` 会**死锁开机**。`[已由官方来源确认]`
6. 模块文件默认由安装器套上权限与 `u:object_r:system_file:s0` 上下文；用 `SKIPUNZIP=1` 就**由你自己负全责**，漏设上下文是「模块文件无法访问」类故障的高发点。`[已由官方来源确认]`
7. `sepolicy.rule` 是**开机 pre-init 阶段**由 `magiskinit` 连同内建规则一起注入的，不是运行时改策略；模块的 `sepolicy.rule` 会被持久化到 pre-init 分区（cache/metadata），**擦除该分区会丢失模块策略直到重装**。`[已由官方来源确认]`
8. `uninstall.sh` 的语义只是「Magisk 移除你的模块时执行」，而移除动作由 `remove` 标记驱动、**发生在下一次重启**；因此它不适合承担「杀死本模块守护进程」这类运行时清理。`[已由官方来源确认]` + 时序 `[推断/待验证]`
9. Magisk 最新公开版本为 **v31.1（2026.10.24）**；官方 changelog 明确记录 v30.3/v30.7 适配 **Android 16 QPR2** 的 sepolicy 新二进制格式，v31.0 的 Zygisk 支持 **Android 17（API 37）**，v28.0 起支持 **16 KB page size**。最低支持版本已被抬到 Android 7.0。`[已由官方来源确认]`
10. KernelSU 官方 FAQ 明确：**KernelSU 的模块系统与 Magisk 的 magic mount 冲突**——KernelSU 里有任何模块启用时，Magisk 会完全失效；但只使用 KernelSU 的 `su` 时两者可以共存（KernelSU 改 kernel，Magisk 改 ramdisk）。`[已由官方来源确认]`

---

## 2. 模块目录结构

官方给出的目录树（`/data/adb/modules/$MODID`）`[已由官方来源确认]`：

```
/data/adb/modules/$MODID
├── module.prop          # 元数据（必须有）
├── system/              # 无 skip_mount 时被挂载（magic mount）
├── zygisk/              # Zygisk native 库
├── skip_mount           # 存在则不挂载 system/
├── disable              # 存在则模块被禁用
├── remove               # 存在则下次重启被移除
├── post-fs-data.sh      # post-fs-data 阶段执行
├── service.sh           # late_start service 阶段执行
├── uninstall.sh         # Magisk 移除模块时执行
├── action.sh            # 用户点 App 内「操作」按钮时执行
├── system.prop          # 以 resetprop 载入为系统属性
├── sepolicy.rule        # 追加自定义 sepolicy 规则
├── vendor / product / system_ext   # 自动生成的符号链接，禁止手工创建
└── ...                  # 允许任意附加文件
```

关键点：

- `system/` 会被**递归合并**进真实 `/system`：同名文件被覆盖、新文件被添加。目录里放 `.replace` 则整个目录替换；要删除文件/目录，放一个 `mknod <name> c 0 0` 的哑字符设备，或在 `customize.sh` 里用 `REMOVE` 声明。`[已由官方来源确认]`
- `/vendor`、`/product`、`/system_ext` 的替换分别放进 `system/vendor` 等路径，Magisk 会透明处理独立分区与否。`[已由官方来源确认]`
- `vendor`、`product`、`system_ext` 三个符号链接是**自动生成**的，手工创建会干扰 Magisk 逻辑。`[已由官方来源确认]`

### 2.1 module.prop 严格格式

`[已由官方来源确认]`

```
id=<string>
name=<string>
version=<string>
versionCode=<int>
author=<string>
description=<string>
updateJson=<url> (optional)
```

- `id` 必须匹配 `^[a-zA-Z][a-zA-Z0-9._-]+$`：不能有空格、不能数字开头、不能 `-` 开头；`id` 是唯一标识，发布后不应更改。
- `versionCode` 必须是**整数**，用于版本比较。
- 其余未列出的字段可以是任意**单行**字符串。
- 必须用 **UNIX (LF)** 换行，不能是 CR+LF / CR——这一点对在 Windows 上打 zip 的项目是真实风险点。
- `updateJson` 指向的 JSON 格式为：`version`(string)、`versionCode`(int)、`zipUrl`(url)、`changelog`(url)。

> 对本项目：`id` 建议与仓库名一致且全小写（如 `smbshare`），`versionCode` 用单调递增整数（如 `2026111401`），避免出现「Magisk 认为版本没变所以不更新」的问题。

### 2.2 各文件执行时机与限制（总表）

| 文件 | 执行者/阶段 | 阻塞性 | 限制与注意事项 | 证据 |
|---|---|---|---|---|
| `customize.sh` | 安装器 `source`（不是执行），在所有文件解压并套用默认权限/上下文**之后** | 阻塞安装 | 环境是 BusyBox ash standalone；有 `MAGISK_VER`/`MAGISK_VER_CODE`/`BOOTMODE`/`MODPATH`/`TMPDIR`/`ZIPFILE`/`ARCH`/`IS64BIT`/`API`；可用 `ui_print`/`abort`/`set_perm`/`set_perm_recursive`；**结尾不能 `exit`**（安装器还要清理） | `[已由官方来源确认]` |
| `post-fs-data.sh` | post-fs-data，`/data` 解密挂载后、Zygote 启动前、模块挂载前 | **阻塞**，Magisk 整体上限 40s，脚本阶段上限 35s | `setprop` 会死锁，必须 `resetprop -n`；官方建议「除非必要不要用」 | `[已由官方来源确认]` |
| `service.sh` | late_start service | **非阻塞**，与开机并行 | 官方推荐作为默认选择 | `[已由官方来源确认]` |
| `uninstall.sh` | Magisk 移除该模块时 | 未文档化 | 触发条件是 `remove` 标记 → 下次重启移除；不覆盖「用户手删目录」「factory reset」 | `[已由官方来源确认]` + 时序 `[推断/待验证]` |
| `action.sh` | 用户在 Magisk App 模块卡片点「操作」 | 阻塞该控制台 | 在 **App 内建控制台**执行，BusyBox standalone，`BOOTMODE=true`；是 Magisk 唯一的官方 UI 钩子 | `[已由官方来源确认]` |
| `system.prop` | post-fs-data 阶段被处理，语义等同 `resetprop -n` | 随 post-fs-data | 格式同 `build.prop`，每行 `key=value` | `[已由官方来源确认]` |
| `sepolicy.rule` | **pre-init**，由 `magiskinit` 与内建规则一起注入已加载策略 | 随 pre-init | 每行一条 policy statement 语法；持久化在 pre-init 分区 | `[已由官方来源确认]` |

### 2.3 Boot Scripts 的运行时环境

- **BusyBox standalone**：Magisk 自带功能完整的 BusyBox（含完整 SELinux 支持），路径 `/data/adb/magisk/busybox`。在 `ash` 的 standalone 模式下，`ls`/`rm`/`chmod` 等**不看 `PATH`**，直接走 BusyBox 内建 applet；要让某个命令不走 BusyBox，必须给全路径。所有 boot script 与模块安装脚本都在此模式下运行。`ASH_STANDALONE=1` 是官方推荐的外部启用方式（环境变量会被子进程继承）。`[已由官方来源确认]`
- **PATH**：官方 changelog 记录 v21.1「Add Magisk binaries to `PATH` when executing boot scripts」、v14.6「Busybox is added into `PATH` for all boot scripts」。所以 boot script 里 `magisk`、`resetprop`、`busybox` 可直接调用；但**不要依赖** `/system/bin` 的某个具体实现（可能被模块或 ROM 影响）。`[已由官方来源确认]`
- **定位自身目录**：所有模块脚本用 `MODDIR=${0%/*}`，**不要硬编码**模块路径。`[已由官方来源确认]`
- **等待开机完成**：官方明确「若需要等待 boot completed，可用 `resetprop -w sys.boot_completed 0`」。`[已由官方来源确认]`
- **Zygisk 状态**：启用时环境变量 `ZYGISK_ENABLED=1`。`[已由官方来源确认]`
- **日志**：
  - 官方没有给出「模块脚本 stdout 会写到哪个文件」的明确路径；能确认的是 Magisk 有独立日志管道与 `magisk_log_file` FIFO（`u:object_r:magisk_log_file:s0`，仅 `zygote` 与 `magisk` 可读），changelog v21.2 记录「Directly log to file to prevent logcat weirdness」。`[已由官方来源确认]`（管道/类型）／`[推断/待验证]`（具体落盘路径）
  - **工程建议**：不要赌 Magisk 的日志落盘位置。本模块应自己写日志到 `/data/adb/smbshare/logs/`（见 §9），并在 `action.sh` 或 WebUI 里提供查看入口。`[推断/待验证]`

---

## 3. 安装器：权限、上下文、SKIPUNZIP、REPLACE/REMOVE

### 3.1 安装器做了什么

Magisk 模块安装包 = 一个 zip，至少包含模块文件；若要在 recovery 里刷，额外需要 `META-INF/.../update-binary`（即官方 `scripts/module_installer.sh`）与内容为 `#MAGISK` 的 `updater-script`。`[已由官方来源确认]`

安装器流程（官方描述）：`setup environment → 解压模块文件到正确位置 → finalize（含默认权限与 secontext）→ source customize.sh`。`[已由官方来源确认]`

- **默认权限/上下文**：`set_perm`/`set_perm_recursive` 的 `[context]` 缺省值是 `u:object_r:system_file:s0`，实现即 `chown` + `chmod` + `chcon`。`[已由官方来源确认]`
- **默认文件权限位**：Magisk 文档未写死；KernelSU 官方文档在 preinit 限制里明确写「installer sets module files to `0644`，所以 `exec` 的程序需要在 `customize.sh` 里 `set_perm ... 0755`」。两者安装器同源，故 **Magisk 侧推断同为 0644（脚本本身不需要可执行位，因为由 `busybox sh` 调用）**。`[推断/待验证]`
- **`update-binary` 会被强制替换**：从 Magisk App 下载/安装模块时，zip 里的 `update-binary` 会被最新官方脚本覆盖——**不要在其中加自定义逻辑**；另外**不要**放名为 `install.sh` 的文件（历史原因）。`[已由官方来源确认]`

### 3.2 SKIPUNZIP

`SKIPUNZIP=1` 声明于 `customize.sh`，作用是**跳过全部默认安装步骤**，此时「你的 `customize.sh` 负责自己装好一切」。`[已由官方来源确认]`

风险（工程判断）：默认的权限/上下文套用也被跳过，因此凡是需要被其他 domain 访问的文件（socket、配置、可执行文件）都必须显式 `set_perm`；漏掉会产生「root shell 能读、其他进程 EACCES/SELinux denied」的隐蔽故障。`[推断/待验证]`

修复手段：Magisk CLI 提供 `magisk --restorecon`（restore selinux context on Magisk files）与 `--clone-attr SRC DEST`（克隆权限/属主/上下文）。`[已由官方来源确认]`

### 3.3 REPLACE / REMOVE

- `REPLACE="..."`（`customize.sh` 中的变量，列出目录）→ 安装器在对应目录创建 `.replace`，实现「整目录替换」。`[已由官方来源确认]`
- `REMOVE="..."`（列出文件/目录）→ 安装器在 `$MODPATH` 下创建对应的哑字符设备（`mknod c 0 0`），实现 systemless 删除。`[已由官方来源确认]`
- 也支持直接在 `system/` 里放 `.replace` 文件做目录替换。`[已由官方来源确认]`

> 对本项目：我们**不注入 `system/`**（Samba 二进制与配置放在 `/data/adb/smbshare/`），因此完全不使用 `REPLACE`/`REMOVE`/`.replace`，可避开 magic mount 与 metamodule 的差异（见 kernelsu.md §3）。

### 3.4 升级与卸载流程

- **升级**：通过 Magisk App 安装的模块会被暂存到 `/data/adb/modules_update`，**下一次重启**时才合并进 `/data/adb/modules`。官方给出的原因是：模块被挂载时其文件不安全被修改。`[已由官方来源确认]`
- **移除**：`remove` 标记存在 → 「module will be removed next reboot」。`[已由官方来源确认]`
- **`magisk --remove-modules [-n]`**：移除所有模块，不带 `-n` 则立即重启；changelog v20.1 记录「Make sure `--remove-modules` will execute `uninstall.sh` after removal」。`[已由官方来源确认]`
- **`uninstall.sh` 能否保证清理？** 不能。可确认的只有「移除模块时会被执行」；不可保证的情形包括：用户直接 `rm -rf /data/adb/modules/<id>`、factory reset、以及模块因安全模式/异常被 `disable` 后长期不启用。另外「移除发生在下次重启」意味着**执行 `uninstall.sh` 时上一轮开机启动的进程已经随重启消失**，所以它适合清理**文件与状态**，不适合承担「优雅停止守护进程」。`[已由官方来源确认]` + `[推断/待验证]`
- **安全模式**：连续两次未完成 `boot_completed`，Magisk 会自动进入 Safe Mode，给每个模块创建 `disable` 文件并禁用 Zygisk。`[已由官方来源确认]`
- **另一种卸载路径**：把 Magisk APK 改名为 `uninstall.zip` 当普通 zip 刷入（官方 installation 文档的 Uninstallation 段）。`[已由官方来源确认]`

---

## 4. Magisk 有没有内建 WebUI？

**结论：没有。** `[已由官方来源确认]`

支撑依据（均为官方材料，属「规范中不存在该机制」+「唯一 UI 钩子是别的机制」）：

1. 官方模块目录树把「Optional Files」穷举为：`post-fs-data.sh`、`service.sh`、`uninstall.sh`、`action.sh`、`system.prop`、`sepolicy.rule`——**没有 `webroot`、没有 `index.html`、没有端口/HTTP 相关文件**。`[已由官方来源确认]`
2. Magisk App 与模块的唯一交互点是：模块卡片上的 **Action 按钮 → 执行 `action.sh`**（v28.0 加入该能力：「Add `action.sh` support to allow modules to define an action triggered from UI」；v31.0 把执行环境升级为 App 内建终端模拟器）。`[已由官方来源确认]`
3. Magisk 无「WebUI 宿主」概念，因此模块的网页界面在 Magisk 生态里普遍依靠**第三方宿主 App**（如 KsuWebUI、WebUI X）来加载模块 `webroot/` 里的静态页面。`[社区来源，非官方]`（见 [WebUI X 文档](https://mmrl.dev/guide/webuix/)、[KsuWebUI 对比说明](https://github.com/alipymanbu/Android/blob/master/%E5%AE%98%E7%BD%91%E7%89%88APP%E4%B8%8B%E8%BD%BD/KsuWebUI/WebUI%20X%20%E4%B8%8E%20KsuWebUI%20%E6%80%8E%E4%B9%88%E9%80%89.md)）

**对本项目的直接含义**：在 Magisk 设备上，「WebUI」只能是

- (a) 我们自带的**独立 HTTP 服务 + 用户浏览器**访问（本项目的方案），或
- (b) 用户安装第三方宿主 App 读取 `webroot/`。

(a) 不依赖任何 App，是本项目的主要路线；(b) 只能作为可选增益，且不能假设用户装了宿主。

---

## 5. SELinux：magisk domain、magisk_file、sepolicy.rule

### 5.1 官方定义的策略结构

`[已由官方来源确认]`

- **`magisk`（`u:r:magisk:s0`）**：官方原文「An unconstrained, permissive domain that `magiskd` and all root shells run in」。
- **`magisk_file`（`u:object_r:magisk_file:s0`）**：官方原文「An unrestricted file type accessible by all domains」，用于标注 Magisk 的 socket、二进制、内部节点。注意：它**不是**通用的 `/data` 文件标签；`/data/adb` 本身是 `u:object_r:adb_data_file:s0`。
- **`magisk_log_file`**：专用 FIFO 类型，所有 domain 可写，仅 `zygote`（和 `magisk`）可打开与读取。
- **客户端 domain 白名单**：`zygote`、`shell`、`platform_app`、`system_app`、`priv_app`、`untrusted_app`、`untrusted_app_all` 被允许连接 `magisk` 的 Unix stream socket（用于 `su`、`magisk`、Zygisk IPC），并被允许共享 root shell pty 的文件描述符。
- **`/data/adb` 自带保护**：默认权限 `700`、属主 `root`、上下文 `u:object_r:adb_data_file:s0`，且位于 Device Encrypted storage（FBE 设备 `data` 挂载后即可用）。

官方源码 `native/src/sepolicy/rules.rs` 进一步佐证 `[已由官方来源确认]`：

- `deny(all, ["kernel"], ["security"], ["load_policy"]);` —— 注释原文「Prevent anything to change sepolicy except ourselves」，即除 Magisk 自身外**任何 domain 都不允许**再加载策略。
- `permissive([proc]);` —— 注释原文「Just in case, make the domain permissive」。
- `allow([proc], [fs_type, dev_type, file_type, domain, service_manager_type, ...], all, all);` —— 注释原文「Make our root domain unconstrained」。
- `allow(["domain"], [file], [...], all);` —— 把 `magisk_file` 做成对所有 domain 无限制的文件类型。

### 5.2 为什么不能依赖 `setenforce 0`

按证据强度分层：

1. **不需要**：Magisk 下 root shell 已经在 permissive 的 `magisk` domain 里（§5.1 已确认），把全局 SELinux 设为 Permissive 不带来任何额外权限。`[已由官方来源确认]`
2. **不可在运行时改「策略」**：Magisk 明令禁止其他 domain `load_policy`；策略修改必须走 `sepolicy.rule`（pre-init 注入）或 `magiskpolicy --live`（高级工具）。`[已由官方来源确认]`
3. **时间窗不对**：策略在 pre-init、`/data` 挂载之前就已注入并加载（`magiskinit` 用 `LD_PRELOAD` 钩 `security_load_policy` 等三种策略之一）。`post-fs-data`/`service.sh` 阶段已经太晚。`[已由官方来源确认]`
4. **全局 permissive 会削弱系统安全边界并产生可观测痕迹**：`/sys/fs/selinux/enforce` 可被读取，很多完整性/风控检查会看它；同时 Android 的 SELinux 是开机即 enforce 的强制机制。**CDD 层面**的表述（SELinux「SHOULD NOT be user or developer configurable」、必须实现 SELinux）我只通过检索摘要看到，未能打开原文，故整体标 `[推断/待验证]`，引用见文末 CDD 链接。
5. **可移植性错误**：如果代码里写了 `setenforce 0`，在 KernelSU 上未必生效、且会把「我们依赖 permissive」这个脏假设固化进实现。`[推断/待验证]`

> 工程结论：**永远不要**在模块脚本里调用 `setenforce 0`。要权限就写 `sepolicy.rule`；要「先跑起来」就利用 Magisk 的 permissive `magisk` domain；在 KernelSU 上则必须实测（见 kernelsu.md §7）。

### 5.3 sepolicy.rule 的用法与风险

用法 `[已由官方来源确认]`：

- 放在模块根目录，**每行一条 policy statement**，语法等同于 `magiskpolicy` 的语句格式（支持 `allow`/`deny`/`auditallow`/`dontaudit`、`allowxperm`、`permissive`/`enforce`、`typeattribute`、`type`、`type_transition`、`genfscon` 等）。
- 这些行由 `magiskinit` 在 pre-init 读取并**与内建规则一起**注入策略；持久化在 pre-init 分区，以便下次开机在 `/data` 可用之前就能读到。

风险 `[已由官方来源确认]` / `[推断/待验证]`：

- **擦除 pre-init 分区会丢策略**：官方 install 文档明确警告「模块的 `sepolicy.rule` 可能存放在 `cache` 或 `metadata` 分区，**不要擦除** cache/metadata，否则活动模块的 `sepolicy.rule` 会丢失直到重装模块」。`[已由官方来源确认]`
- **规则只增不减**：KernelSU 侧文档明确指出 hot install 时「Rules are only ever added」，被删掉的规则要等重启才失效；Magisk 同样是 pre-init 阶段整批加载。`[已由官方来源确认]`（KernelSU 文档表述）/ `[推断/待验证]`（Magisk 同构）
- **语法错误可能导致开机异常**：策略在 pre-init 注入，出错位置极早。`[推断/待验证]`
- **过度放宽是安全问题**：`allow magisk * * *` 式的写法（即 `magiskpolicy --magisk` 的效果）只应留在 Magisk 自己的 domain，不要为自己的服务新增宽规则。`[已由官方来源确认]`（`--magisk` 语义）+ `[推断/待验证]`（风险评估）

---

## 6. Android 15 / 16 / 17 兼容性

可引用的官方事实 `[已由官方来源确认]`：

| 版本/条目 | 官方记录 |
|---|---|
| 最新发布 | v31.1（2026.10.24）；v31.0（2026.9.4） |
| Android 17 | v31.0：「[Zygisk] Support Android 17 (API 37)」 |
| Android 16 QPR2 | v30.7：「[MagiskInit] Support Android 16 QPR2 sepolicy format」；v30.3：「[MagiskPolicy] Support new sepolicy binary format introduced in Android 16 QPR2」 |
| 16 KB page size | v28.0：「[General] Support 16k page size」 |
| 最低 Android | v31.1：「Drop support for Android 6.0；Android 7.0 is now the minimum」 |
| 分区/镜像 | v30.3：「Support installing Magisk into `vendor_boot` partition」；v29.0：sepolicy 注入逻辑重写；v30.0：模块实现迁到 Rust |

工程注意点（区分证据强度）：

- **sepolicy 二进制格式随 Android 大版本变化**：Android 16 QPR2 引入了新格式，Magisk 在 v30.3+ 才支持。**不要把 Magisk 版本要求写得太低**；若模块依赖 `sepolicy.rule`，建议要求 v30.3 以上。`[已由官方来源确认]`（格式变化与支持版本）+ `[推断/待验证]`（「必须 ≥ v30.3」的阈值判断）
- **16 KB page size**（Android 15 起在部分设备上启用）与 arm64 native 二进制直接相关：官方只说 Magisk 自 v28.0 支持 16k page size，**没有**说第三方模块的 `smbd`/`.so` 需要怎样编译。`[已由官方来源确认]`（Magisk 侧支持）/ **AOSP 侧要求未能取证** `[推断/待验证]`——上机前必须在真机 `getconf PAGESIZE` 确认，并按 16 KB 对齐编译。
- **Android 15 无逐条声明**：官方 changelog 没有「Support Android 15」这样的条目（Android 15 周期内的条目是 v27.x/v28.0）。不能声称「官方宣布支持 Android 15」；能说的是「v28.0+ 在 Android 15 时代的 ABI 变化（16 KB page size）上有显式适配」。`[已由官方来源确认]`（无该条目）+ `[推断/待验证]`（结论）
- **模块脚本 API 稳定**：`MODDIR`、`post-fs-data.sh`/`service.sh` 语义、`module.prop` 格式在 v31.1 文档里与旧版一致，未见破坏性变更。`[已由官方来源确认]`（文档现状）

---

## 7. 如何判断「装了 Magisk / 版本多少 / 与 KernelSU 区分」

### 7.1 判断 Magisk 存在与版本

官方 CLI（`tools.html`）`[已由官方来源确认]`：

- `magisk -c` → 当前二进制版本字符串
- `magisk -v` → 正在运行的 daemon 版本
- `magisk -V` → 正在运行的 daemon version code（整数，便于比较）
- `magisk --path` → 当前 Magisk tmpfs 目录（`/sbin` 或 `/debug_ramdisk`），用于取 `MAGISKTMP`
- `magisk --list` → 可用 applet 列表

安装器环境变量（仅在 `customize.sh` 可用）：`MAGISK_VER`（如 `v20.0`）、`MAGISK_VER_CODE`（如 `20000`）。`[已由官方来源确认]`

文件系统特征 `[已由官方来源确认]`：

- `/data/adb/magisk/`（`DATABIN`：busybox、脚本、magisk 二进制）
- `/data/adb/magisk.db`（设置与 root 授权数据库）
- `/data/adb/modules/`、`/data/adb/modules_update/`
- `/data/adb/post-fs-data.d/`、`/data/adb/service.d/`
- `$MAGISKTMP/.magisk/`（`config`、`modules`、`busybox`、`device`、`rootdir`、`worker`、`preinit`）
- `$MAGISKTMP/.magisk/busybox/busybox`

### 7.2 与 KernelSU 区分

`[已由官方来源确认]`（KernelSU 官方文档）：

- **环境变量 `KSU`**：KernelSU 中所有「能跑模块脚本的地方」（`customize.sh`、`post-fs-data.sh`、`service.sh`）都会把 `KSU` 设为 `true`；Magisk 下不设置。
- **BusyBox 路径不同**：Magisk `/data/adb/magisk/busybox`，KernelSU `/data/adb/ksu/bin/busybox`（KernelSU 官方注明这是内部行为，未来可能变化）。
- **坑**：KernelSU 中 `MAGISK_VER_CODE` 恒为 `25200`、`MAGISK_VER` 恒为 `v25.2`，**不能用它们判断是不是 Magisk**。
- **共存**：KernelSU 官方 FAQ——KernelSU 的模块系统与 Magisk magic mount 冲突；KernelSU 里有模块启用时 Magisk 完全失效；只使用 KernelSU 的 `su` 时可共存（KernelSU 改 kernel、Magisk 改 ramdisk）。KernelSU 在标准开机的 late-load 对照表中还有「Magisk coexistence check」一项，说明其会检测 Magisk 存在。
- `/data/adb` 布局差异：KernelSU 侧有 `/data/adb/ksu/`（busybox、`module_configs/`）、`/data/adb/metamodule`（符号链接）等，详见 kernelsu.md。

> 判定建议（不含实现代码）：**先判 `KSU`，再判 `magisk` 二进制/`/data/adb/magisk`，两者都无则视为「未知 Root 方案」，功能降级而不是直接失败**。`[推断/待验证]`

---

## 8. 卸载/升级时的进程与 PID/socket 清理（最佳实践）

官方没有针对「模块自带守护进程」的清理协议，以下是**基于已确认机制推导的工程实践**，全部标 `[推断/待验证]`：

1. **把运行时状态放在模块目录之外**。模块目录在升级时会被整体替换（Magisk：`modules_update` 合并；KernelSU：旧目录被挪开、下次开机才删除）。PID 文件、socket、日志放 `/data/adb/smbshare/`（`/data/adb` 本身是 `700 root:root`、`u:object_r:adb_data_file:s0`），不要放 `$MAGISKTMP`（tmpfs，重启即失）。
2. **socket 路径不要放 `/dev` 或 `/sbin`**：`/sbin` 在 Android 11+ 可能不存在，Magisk 改用 `/debug_ramdisk`；tmpfs 内容不保证跨阶段可见。
3. **PID 文件 + 身份校验**：写入 PID 后，停止时先校验 `/proc/<pid>/cmdline` 或 `exe` 是否确实指向我们的二进制，避免 PID 复用误杀（Magisk 下 `magisk` domain 是 permissive，signal 不会被拦，`kill -9` 很容易误伤）。
4. **不要按进程名全局 `pkill`**：`smbd` 名字可能与其他实现（第三方 Samba 模块）冲突。
5. **优先 `service.sh` 做「启动前清理」**：每次开机 `service.sh` 都会跑（模块启用时），在这里检测「残留 PID 已死 / socket 文件残留」并清理，比依赖 `uninstall.sh` 可靠。
6. **升级时靠「重启生效」而不是「热重启」**：Magisk 的模块更新本来就是 `modules_update` → 重启合并（已确认），因此升级路径应假设「服务会被重启打断」，把状态持久化做好、启动时自动恢复。
7. **`uninstall.sh` 只做文件清理**：删除 `/data/adb/smbshare/` 下的 socket/pid/log、清理 `system.prop` 之外的持久化开关。**不要**在 `uninstall.sh` 里 `kill` 进程——那一刻旧进程已经随重启消失，而新进程尚未启动，杀了只会误伤同 PID 的无关进程。`[推断/待验证]`
8. **`disable` 场景要单独处理**：模块被禁用/安全模式时脚本不执行，此时残留 pid/socket 只能是上次开机遗留（实际已死），只需 `service.sh` 里的残留检测覆盖。
9. **日志兜底**：`post-fs-data.sh` 阻塞且可能被杀（35s 上限），守护进程启动逻辑放 `service.sh` + 自写日志，便于事后定位。

---

## 9. 对本项目的约束清单（Magisk 侧）

- 不注入 `system/`、不使用 `REPLACE`/`REMOVE`/`.replace` → 与 magic mount 解耦，最大化跨 Root 方案兼容。`[已由官方来源确认]`（机制）+ `[推断/待验证]`（可行性）
- `customize.sh` 里对 `/data/adb/smbshare/` 下的二进制显式 `set_perm ... 0755`，对目录 `0755`，对配置 `0644`；若设 `SKIPUNZIP=1`，所有上下文都要自己补。`[已由官方来源确认]`（set_perm 语义）/`[推断/待验证]`（权限值选择）
- 服务启动放 `service.sh`；需要「开机完成」再用 `resetprop -w sys.boot_completed 0`；`post-fs-data.sh` 最多做极轻量的目录/权限修复。`[已由官方来源确认]`
- 需要 SELinux 例外时写 `sepolicy.rule`（pre-init 注入，规则只增不减，勿擦 cache/metadata）。`[已由官方来源确认]`
- 不写 `setenforce 0`。`[推断/待验证]`
- 版本门槛建议：若依赖新 sepolicy 格式处理，要求 Magisk ≥ v30.3（Android 16 QPR2）；否则 ≥ v28.0（16 KB page size）。`[推断/待验证]`

---

## 10. 证据不足 / 待上机验证

1. `uninstall.sh` 的**精确执行时机与顺序**（相对模块目录删除、相对 `service.sh`）——官方只有「移除模块时执行」一句；`--remove-modules` 会执行它（changelog v20.1）是唯一强证据。
2. `uninstall.sh` 在**模块升级**时是否执行——Magisk 文档未写；KernelSU 文档明确「不执行」，Magisk 侧仅 `[推断/待验证]`。
3. Magisk Boot Script 的 **stdout 落盘位置**（`/cache/magisk.log` 之类）——未在官方文档中找到现行路径，不作断言。
4. **默认文件权限位 0644**——由 KernelSU 官方文档（安装器同源）反推，Magisk 文档未明写。
5. **Android 15 的官方支持声明**——changelog 无对应条目；仅能从 16 KB page size（v28.0）作关联推断。AOSP 侧 16 KB 要求的原文未能取证（`developer.android.com` 不可达）。
6. **CDD 关于 SELinux 必须 enforce / 不可由开发者配置**——仅见检索摘要，未打开 AOSP/CDD 原文，故 §5.2 第 4 点整体标待验证。
7. **`sepolicy.rule` 在 Magisk 上「只增不减」**——由 KernelSU 文档同构推断，Magisk 侧未逐字确认。
8. 本机网络无法访问 `github.com` / `raw.githubusercontent.com` / `source.android.com` / `developer.android.com` / `npmjs.com`（403 或 fetch failed）。若需要引用仓库源码行号级证据，需在可访问 GitHub 的环境复核。

---

## 11. 来源

**官方（Magisk 文档站）**

- Developer Guides（模块结构、module.prop、customize.sh、REPLACE/REMOVE、Boot Scripts、overlay.d）：https://topjohnwu.github.io/Magisk/guides.html
- Internal Details（`/data/adb` 布局、booting 流程、Resetprop、SELinux Policies）：https://topjohnwu.github.io/Magisk/details.html
- Magisk Tools（`magisk` CLI、`su`、`resetprop`、`magiskpolicy`、`magiskboot`）：https://topjohnwu.github.io/Magisk/tools.html
- Frequently Asked Questions（安全模式、bootloop 恢复）：https://topjohnwu.github.io/Magisk/faq.html
- Installation（卸载方式、sepolicy.rule 与 cache/metadata 警告）：https://topjohnwu.github.io/Magisk/install.html
- Magisk Changelog（v31.1 / v31.0 / v30.7 / v30.3 / v28.0 / v21.1 / v20.1 等）：https://topjohnwu.github.io/Magisk/changes.html
- 文档索引：https://topjohnwu.github.io/Magisk/

**官方（Magisk 仓库源码）**

- `native/src/sepolicy/rules.rs`（`deny(... load_policy)`、`permissive([proc])`、`magisk_file` 无限制）：https://cdn.jsdelivr.net/gh/topjohnwu/Magisk@master/native/src/sepolicy/rules.rs
- 仓库主页（本机网络不可达，仅登记）：https://github.com/topjohnwu/Magisk

**官方（KernelSU，用于对比与交叉确认）**

- Difference with Magisk：https://kernelsu.org/guide/difference-with-magisk.html
- Module guide（`KSU` 变量、`MAGISK_VER_CODE` 恒为 25200、preinit 中「installer sets module files to 0644」）：https://kernelsu.org/guide/module.html
- FAQ（与 Magisk 共存的官方结论）：https://kernelsu.org/guide/faq.html

**社区来源（仅用于「生态里有第三方宿主」这类事实，非权威）**

- WebUI X 官方文档：https://mmrl.dev/guide/webuix/
- KsuWebUI / WebUI X 对比（第三方整理）：https://github.com/alipymanbu/Android/blob/master/%E5%AE%98%E7%BD%91%E7%89%88APP%E4%B8%8B%E8%BD%BD/KsuWebUI/WebUI%20X%20%E4%B8%8E%20KsuWebUI%20%E6%80%8E%E4%B9%88%E9%80%89.md

**引用但未能取证（标待验证）**

- Android CDD 9.7 Kernel Security Features（SELinux 相关 MUST/SHOULD NOT 表述）：https://android-git.linaro.org/platform/compatibility/cdd.git/tree/9_security-model/9_7_kernel-security-features.md
- Android 16 KB page size 开发者文档：https://developer.android.com/guide/practices/page-sizes
