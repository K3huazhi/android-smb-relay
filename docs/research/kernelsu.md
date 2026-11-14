# KernelSU 技术调研（Android 15/16/17 · arm64 · Root 模块）

- **调研日期**：2026-11-14
- **方法**：只读检索官方一手资料。来源分三类：(1) KernelSU 官方文档站 `kernelsu.org/guide/`（逐页抓取原文）；(2) `tiann/KernelSU` 仓库源码（`userspace/ksud/src/{cli,module,defs,hot_install,ksu_uapi}.rs`、`uapi/supercall.h`、`kernel/selinux/{rules.c,selinux.h}`、`main.rs`），经 jsDelivr 镜像读取 `main` 与 **`v3.3.1` tag** 两个版本；(3) GitHub Releases API（版本与 asset 清单）。KernelSU 与 Magisk 的对照结论同时参考 `docs/research/magisk.md`（本仓库，同日调研）。
- **范围**：模块结构、与 Magisk 的差异、WebUI 机制与安全边界、模块配置系统、热安装（hot install）、`ksud` CLI、`su` 与 SELinux、Android 15/16/17 兼容性/late-load/x86_64/metamodule、卸载与升级、`uninstall.sh` 语义。
- **可信度**：
  - `[已确认]`：可在文末链接原文（文档页或仓库源码）中直接读到。
  - `[推断/待验证]`：官方未明说，由已确认机制推导，或来自版本间比对，**必须上机验证后才能写进实现**。
  - **版本敏感性（重要）**：官方文档站 `kernelsu.org` 描述的是仓库 `main`（写作时 `KERNEL_SU_UAPI_VERSION = 6`），而**本机是 v3.3.1 / uapi 5**。凡文档站描述、但 `v3.3.1` tag 源码里**不存在**的能力（hot install、模块 `preinit/`），本文都单独标注并给出降级结论。
  - **可达性**：`kernelsu.org`、`cdn.jsdelivr.net`（GitHub 镜像）、`api.github.com` 可达；`github.com`、`raw.githubusercontent.com` 未验证；`source.android.com`、`developer.android.com` 不可达，故涉及 AOSP 的表述一律降级。
- **本机事实（调研基线）**：OnePlus PJA110，Android 16 / API 36，arm64，KernelSU **3.3.1（uapi 5）**，`su` 上下文 `u:r:ksu:s0`，`ksud module install <zip>` 可用。

---

## 1. 结论摘要（先看这 10 条）

1. **v3.3.1 ≠ 文档站现状**。`v3.3.1` 的 `uapi/supercall.h` 里 `KERNEL_SU_UAPI_VERSION = 5`（注释：`5: add EVENT_SERVICES with a start/skip result`），与本机报告的 uapi 5 完全一致 —— 这是判定「文档是否适用于本机」的锚点。`[已确认]`
2. **v3.3.1 没有 hot install**。`v3.3.1` 的 `userspace/ksud/src/main.rs` 里**没有** `mod hot_install;`，`defs.rs` 里也没有 `HOT_INSTALL_*` 常量；而 `main` 分支两者都有。因此文档站的 `MODULE_HOT_INSTALL_REQUEST`/`MODULE_HOT_RUN_SCRIPT` **在本机不可用，升级只能靠重启**。`[已确认]`（tag 源码）＋ `[推断/待验证]`（运行期是否真的不存在，需上机试）
3. **本模块不需要 metamodule**。官方 FAQ/Module guide 明确：只有要修改 `/system` 文件（模块里放 `system/` 目录）才需要 metamodule；脚本、`sepolicy.rule`、`system.prop` 都不需要。我们的二进制与配置全在 `/data/adb/smbshare/`，不放 `system/`，**因此不依赖 metamodule**。`[已确认]`
4. KernelSU 的 `su` domain 是 **`u:r:ksu:s0`**（`KERNEL_SU_CONTEXT = "u:r:ksu:s0"`），且 `rules.c` 对其执行 `ksu_permissive()` + `ksu_allow(domain, ALL, ALL, ALL)`（源码注释 `// allow all!`）+ `netdomain`/`mlstrustedobject` 等 typeattribute。**它和 Magisk 的 `magisk` domain 是同一设计哲学：permissive + 无限制**。`[已确认]`
5. **KernelSU 的 WebUI 是「Manager 里的 WebView + `kernelsu` JS API」**，页面必须放 `webroot/index.html`；`exec`/`spawn` 的等价物是 JS 库的 `exec()`/`spawn()`。因此**页面代码等价于 root 代码**，安全边界由 Manager 的 WebView 承担，不由模块承担。`[已确认]`
6. 我们采用**独立 HTTP WebUI**，不依赖 `ksu.exec`：好处是 Magisk/未知 Root 方案下同样可用；代价是**认证与来源过滤必须自己实现**（Manager 不会替我们隔离）。同时仍要能被 KernelSU 正常安装（`module.prop` + 可选 `webroot/`）。`[推断/待验证]`
7. **模块配置系统与我们的 `config.json` 不冲突**：前者在 `/data/adb/ksu/module_configs/<id>/persist.config|tmp.config`（二进制，magic `KSUM`），后者是我们自己的 JSON。**但不要把 `override.description`、`manage.<feature>` 当成通用 KV 存储**（它们有 KernelSU 语义），也**不要双写两套配置**。`[已确认]`（路径/格式/限制）＋ `[推断/待验证]`（双写风险）
8. **`uninstall.sh` 不是在「卸载时立刻执行」**：`ksud module uninstall <id>` 只是创建 `remove` 标记；真正执行发生在**下一次开机的 `post-fs-data` 阶段**（`prune_modules()`）：先 metamodule 的 `metauninstall.sh`，再模块自己的 `uninstall.sh`，然后清模块配置，最后 `rm -rf` 模块目录。因此它适合清文件，**不适合停进程**。`[已确认]`
9. **升级路径**：`ksud module install <zip>` 把新版解压到 `/data/adb/modules_update/<id>`，下次开机由 `handle_updated_modules()/prune_modules()` 合并进 `/data/adb/modules`；**`uninstall.sh` 在升级时不会执行**（hot install 文档明确「`uninstall.sh` is not run on updates」）。`[已确认]`
10. **兼容性**：v3.3.1 发布于 2026-10-12（tag `v3.3.1`，Release APK 名含 `32680`）；v3.3.0（2026-09-08）起官方 LKM 资产覆盖 `android15-6.6` / `android16-6.12` / `android17-6.18`，并新增 x64 LKM 与 x86_64 syscall dispatcher 补丁能力。`[已确认]`（Releases API）

---

## 2. 模块目录结构

官方给出的目录树（`kernelsu.org/guide/module.html`）`[已确认]`：

```
/data/adb/modules/$MODID
├── module.prop          # 元数据（必须有，没有则不认作模块）
├── system/              # 仅当安装了提供挂载能力的 metamodule 时才被挂载
├── skip_mount           # 存在则不挂载 system/
├── disable              # 存在则模块被禁用
├── remove               # 存在则下次开机被移除
├── post-fs-data.sh      # post-fs-data 阶段（阻塞）
├── post-mount.sh        # 挂载完成后（KernelSU 独有阶段）
├── service.sh           # late_start service（非阻塞，官方推荐默认放这里）
├── boot-completed.sh    # 开机完成（KernelSU 独有阶段）
├── uninstall.sh         # 移除模块时（下一次开机 prune 阶段）
├── action.sh            # 用户在 Manager 点「操作」按钮时
├── system.prop          # 以 resetprop -n 载入
├── sepolicy.rule        # 追加 sepolicy
├── initrc/              # 注入 init.rc 的 .rc 文件（模块级）
├── preinit/             # main 分支新增；v3.3.1 无此能力
├── vendor / product / system_ext   # 自动生成的符号链接，禁止手工创建
└── ...                  # 允许任意附加文件
```

`module.prop` 格式 `[已确认]`：

```
id=<string>            # ^[a-zA-Z][a-zA-Z0-9._-]+$，全局唯一，发布后不改
name=<string>
version=<string>
versionCode=<int>      # 必须是整数
author=<string>
description=<string>
updateJson=<url>       # 可选
actionIcon=<path>      # 可选（KernelSU 扩展）
webuiIcon=<path>       # 可选（KernelSU 扩展）
```

- 必须 **LF 换行**，不能 CRLF/CR——对在 Windows 上打包的项目是真实风险点。`[已确认]`
- `actionIcon`/`webuiIcon` 是相对模块根的路径，`ksud` 会拒绝绝对路径与 `..` 穿越。`[已确认]`（`module.rs::resolve_module_icon_path`）
- `description` 可被模块配置 `override.description` 动态覆盖。`[已确认]`

> 对本项目：`id` 用全小写（如 `smbshare`），`versionCode` 用单调递增整数；**不建 `system/`、不写 `skip_mount`**，从根上与 metamodule 解耦。

---

## 3. 与 Magisk 的差异总表

官方 `difference-with-magisk` 页面 + 源码比对 `[已确认]`：

| 维度 | Magisk | KernelSU | 对本项目影响 |
|---|---|---|---|
| 模块格式/安装目录 | ZIP / `/data/adb/modules` | 相同 | 无 |
| `post-fs-data.sh`、`service.sh`、`system.prop`、`sepolicy.rule` | 语义相同 | 语义相同 | 脚本可直接复用 |
| BusyBox | `/data/adb/magisk/busybox` | `/data/adb/ksu/bin/busybox`（官方注明是内部行为，可能变化） | 只用 `busybox sh`，不硬编码路径 |
| 识别方式 | — | 环境变量 `KSU=true`（所有模块脚本都有） | 判 Root 方案先看 `KSU` |
| `MAGISK_VER_CODE`/`MAGISK_VER` | 真实值 | **恒为 `25200` / `v25.2`** | 禁止用它们判断是不是 Magisk |
| `/system` 挂载 | 内建 magic mount | **委派给 metamodule**（如 `meta-overlayfs`，OverlayFS） | 我们不用 `system/`，不受影响 |
| 目录替换/删除 | `.replace`、`REMOVE`/`REPLACE` | **不支持 `.replace`**；用 `mknod NAME c 0 0` whiteout，`REMOVE`/`REPLACE` 变量可用（`REPLACE` 走 `trusted.overlay.opaque`） | 我们两者都不用 |
| 额外阶段 | — | `post-mount.sh`（挂载后）、`boot-completed.sh`（开机完成） | 可用 `boot-completed.sh` 等待网络就绪 |
| initrc 注入 | 无 | `initrc/*.rc` 注入 `init.rc` | 可选 |
| Recovery 安装 | 支持 | **不支持** | 文档里要写清楚 |
| Zygisk | 内建 | 无内建，需 ZygiskNext | 与本模块无关 |
| WebUI | 无内建 | `webroot/index.html` + Manager WebView | 我们自建 HTTP，但保留 `webroot/` |
| 卸载脚本执行时机 | `remove` → 下次重启 | `remove` → **下次开机 post-fs-data 的 prune 阶段** | 见 §8 |
| 共存 | 模块系统与 KernelSU 冲突 | 官方 FAQ：KernelSU 里有模块启用时 Magisk 完全失效 | 不要假设两者同时可用 |

---

## 4. `system/` 与 metamodule 机制（为什么我们不需要它）

- **核心事实**：`system/` 目录**只有在安装了提供挂载能力的 metamodule 时才会被挂载**；官方 FAQ 与 Module guide 都在显眼位置警示这一点，并称「Fresh KernelSU installations require installing a metamodule for modules to function」。`[已确认]`
- FAQ 的精确表述：**「most Magisk modules work on KernelSU. However, if your module needs to modify `/system` files, you need to install a metamodule. Other module features work without a metamodule.」** → 脚本 / `sepolicy.rule` / `system.prop` 全部不需要 metamodule。`[已确认]`
- 判定「这个模块是否需要挂载」的代码口径：`ksud module list` 输出里 `mount = system/ 存在 && skip_mount 不存在`。我们有 `system/` 时该字段才会为 true。`[已确认]`（`module.rs::list_module`）
- metamodule 关键约束（仅在被问及时需要知道）：`module.prop` 里 `metamodule=1`；**同一时间只能装一个**；挂载时必须把 source/dev 设为 `"KSU"`（否则 kernel umount / zygisk umount 不认）；钩子为 `metamount.sh`（挂载）、`metainstall.sh`（装普通模块时 source 执行）、`metauninstall.sh`（卸普通模块时）；`/data/adb/metamodule` 是指向活动 metamodule 的稳定符号链接。`[已确认]`
- 官方执行顺序（post-fs-data 内）：公共 `.d` 脚本 → prune/restorecon/`sepolicy.rule` → metamodule 的 `post-fs-data.sh` → 普通模块 `post-fs-data.sh` → `system.prop` → `metamount.sh` → `post-mount.d` → metamodule/模块 `post-mount.sh`。`[已确认]`

> **结论**：本模块不写 `system/`，**不需要 metamodule**，也不应在 `customize.sh` 里替用户检查 metamodule（只需在检测到有 `system/` 目录的旧版本时提示）。`[推断/待验证]`（「不需要」由官方两句原文直接支撑，属 `[已确认]`；「不必检查」是工程判断）

---

## 5. initrc 注入与 preinit（v3.3.1 有前者、无后者）

### 5.1 initrc 注入（v3.3.1 有）

- 机制：内核侧 hook init 的 `read()`/`fstat()`，当 init 读 `/system/etc/init/hw/init.rc` 时把自定义 RC 追加回去。`[已确认]`
- 用户态：`ksud` 把启用模块的 `.rc` **与 `/data/adb/initrc.d/*.rc`** 拼成 `modules.rc`，写入 **`/metadata` 分区**；模块状态变化时自动重建。`[已确认]`
- 目录级规则：`initrc/` 里要求 `.rc` 扩展名，**不要求可执行位**；按文件名、模块 ID 字典序处理。`/data/adb/initrc.d/` 里**必须可执行**，否则静默跳过。`[已确认]`
- 手动刷新：`ksud initrc refresh`（下次开机生效）。`[已确认]`
- **本机路径差异**：`main` 分支优先 `/metadata/vold/ksu/`（`PREINIT_VOLD_UAPI_VERSION = 6`），而 **uapi 5 不满足该条件**，只能落到 `/metadata/watchdog/ksu/` 或 `/metadata/ksu/`；`v3.3.1` 的 `defs.rs` 里确实只有这两个候选（注释原文 `// Prefer /metadata/watchdog/ when present, else /metadata`）。`[已确认]`
- 风险提醒：官方 rescue 文档给的手工救援命令就是删 `/metadata/ksu/modules.rc` 与 `/metadata/watchdog/ksu/modules.rc`；**任何人能写 `/metadata` 就能在极早期执行代码**（官方原话）。`[已确认]`

### 5.2 preinit（`main` 有，v3.3.1 无）

- `main` 文档描述：模块可放 `preinit/preinit.conf`（`insmod`/`exec`/`write` 三种动作），在 **SELinux 策略加载前、init.rc 解析前**执行；有 30 s 总时限、`/metadata` 空间限制、bootloop 保护（连续 3 次未完成开机则禁用）。`[已确认]`（main 文档）
- 但 `v3.3.1`：`main.rs` **没有** `mod preinit;`，`defs.rs` **没有** `MODULE_PREINIT_DIR`/`PREINIT_CONF`/`KSU_GET_INFO_FLAG_PREINIT`（该 flag 到 uapi 6 才加入，v3.3.1 的 `supercall.h` 注释只到 `5:`）。`[已确认]`
- **结论：本模块不要用 `preinit/`**。要改 sysctl/procfs，用 `post-fs-data.sh` + `ksu` 的 permissive domain；要更早，只能让用户刷 LKM 并升级 KernelSU。`[推断/待验证]`

---

## 6. Boot 阶段与脚本环境

官方 Boot scripts 一节 `[已确认]`：

- **`post-fs-data` 模式**：**阻塞**。官方原文「The boot process is paused before execution is done or after 10 seconds」；且「**使用 `setprop` 会死锁开机**，必须用 `resetprop -n`」；只在必要时用。
- **`late_start service` 模式**：**非阻塞**，与开机并行，**官方推荐的默认阶段**。
- `boot-completed.sh`：开机完成（`ACTION_BOOT_COMPLETED` 之后）执行；`post-mount.sh`：OverlayFS 挂载完成后执行。
- **环境变量**（所有模块脚本）：`ASH_STANDALONE=1`、`KSU=true`、`KSU_VER`、`KSU_VER_CODE`、`KSU_KERNEL_VER_CODE`、`KSU_UAPI_VER`、`KSU_RUNTIME_MODE`（`built-in`/`lkm`/`late-load`）、`KSU_LATE_LOAD`（仅 late-load 时设 `1`）、`KSU_MODULE`（当脚本位于 `/data/adb/modules/<id>/` 下时）。`[已确认]`（`module.rs::get_common_script_envs`）
- **脚本执行方式**：`busybox sh <script>`，`cwd` = 脚本所在目录，**新进程组**，并 `switch_cgroups()`；脚本超时 `BOOT_STAGE_TIMEOUT = 35 s`。`[已确认]`（源码）
- 定位自身目录一律 `MODDIR=${0%/*}`，禁止硬编码。`[已确认]`

> 注意一处**文档与源码的出入**：官方文档说 `post-fs-data` 阻塞上限 **10 s**，而 `defs.rs` 里 `BOOT_STAGE_TIMEOUT = 35 s`。两者可能描述不同阶段（通用 `.d` 脚本 vs 模块脚本）。工程上取**保守值**：`post-fs-data.sh` 内不做任何超过数秒的工作。`[已确认]`（两处原文）＋ `[推断/待验证]`（差异成因）

---

## 7. WebUI：机制与安全边界

### 7.1 官方机制

- 页面：`webroot/` 子目录，**必须有 `index.html`**；安装时 KernelSU 会自动套权限与 SELinux context（官方警告：**不要自己设该目录权限**）。`[已确认]`
- 宿主：**KernelSU Manager 的 WebView**，通过 JS 库 `kernelsu`（npm）暴露系统 API。`[已确认]`
- JS API 面（`js/index.d.ts` 原文）：`exec(cmd[, opts]) -> {errno, stdout, stderr}`、`spawn(cmd[, args][, opts]) -> ChildProcess`（`stdout.on('data')`、`on('exit')`）、`toast()`、`fullScreen()`、`enableEdgeToEdge()`、`moduleInfo()`、`exit()`、`pm.listPackages()`/`pm.getPackagesInfo()`、`io.*`（`File`/`FileInputStream`/`FileOutputStream`/`RandomAccessFile`，等价 Java 文件 API）。`[已确认]`
- 注意：`exec` 返回 `Promise`；`listPackages`/`getPackagesInfo` 已 deprecated，改用 `pm.*`。`[已确认]`
- 存储：页面可用 `localStorage`，**但 Manager 卸载后会丢**；持久数据要自己落盘。`[已确认]`

### 7.2 安全边界（必须写清楚）

- `exec`/`spawn` 在 **`su`/`u:r:ksu:s0`** 上下文执行 → **页面里的任何脚本注入 = 设备 root 代码执行**。因此：
  - 页面不得加载远程 JS/CDN（无 SRI 的远程脚本等于把 root 交出去）。`[推断/待验证]`
  - 若页面把用户输入拼进命令，必须做**严格白名单**（只允许枚举值/数字），不要靠转义。`[推断/待验证]`
  - `webroot/` 里的文件一旦被改写（模块升级包、第三方 App 写入），等价于 root 持久化。`[推断/待验证]`
- Manager 侧的 WebView 隔离（同源、`javascript:` 拦截等）由 Manager 实现，模块无法假定具体细节。`[推断/待验证]`

### 7.3 我们的取舍：独立 HTTP WebUI，但兼容 KernelSU 安装

- **不依赖 `ksu.exec`**：我们的 WebUI 是模块自带的 HTTP 服务（见 `networking.md` §7），浏览器直连；API 走我们自己的 JSON 接口。原因：Magisk/未知 Root 下没有 `kernelsu` JS 库，功能不能分叉。`[推断/待验证]`
- **仍然要被 KernelSU 认作合法模块**：`module.prop` 必须严格合法（LF、`id` 正则、整数 `versionCode`）。`[已确认]`
- **`webroot/` 定位**：可作为**可选的第二入口**（用户在 Manager 里点开时打开一个只读状态页/快捷跳转页），但**页面不得依赖 `kernelsu` JS 库**才能工作——若检测不到 `exec`，就展示「请用浏览器访问 `http://<ip>:<port>`」。`[推断/待验证]`
- **认证必须自带**：Manager 的 WebView 不在链路里，没人替我们挡 CSRF/公网来源（详见 `networking.md` §7.2）。`[推断/待验证]`
- **`action.sh` 是免费的可达入口**：用户可在 Manager 里点「操作」触发 `action.sh`（阻塞执行，`ScriptWait::Forever`），适合做「重启 SMB 服务 / 打开 WebUI 提示 / 打印当前状态」。`[已确认]`（`module.rs::run_action`）

---

## 8. 模块配置系统 vs 我们的 `config.json`

### 8.1 官方配置系统

- 位置：`/data/adb/ksu/module_configs/<module_id>/`（`defs.rs`: `WORKING_DIR + "module_configs/"`，`WORKING_DIR = /data/adb/ksu/`）。`[已确认]`
- 两类：`persist.config`（持久）、`tmp.config`（**每次开机 post-fs-data 阶段清空**）；读取时 tmp 覆盖 persist。`[已确认]`
- 格式：二进制，magic `0x4b53554d`（"KSUM"）+ 版本校验；带长度前缀，可存任意 UTF-8（含换行）。`[已确认]`
- 限制：key ≤ 256 B、value ≤ 1 MB、**每模块最多 32 条**；key 必须匹配 `^[a-zA-Z][a-zA-Z0-9._-]+$`（≥2 字符）。`[已确认]`
- 访问方式：模块脚本里 `KSU_MODULE=<id>` 已设好，直接 `ksud module config get/set/list/delete/clear [--temp]`；也支持从 stdin 写入。`[已确认]`
- **特殊 key（有 KernelSU 语义，不能乱用）**：`override.description`（覆盖 Manager 里显示的描述）、`manage.<feature>`（声明本模块管理某 KernelSU 功能，**只认 `su_compat` 与 `kernel_umount`**）。`[已确认]`
- 生命周期：卸载模块时配置自动清除（`prune_modules()` 里 `clear_module_configs`）。`[已确认]`

### 8.2 与本项目 `config.json` 的关系

| 维度 | KernelSU 配置系统 | 我们的 `config.json` |
|---|---|---|
| 路径 | `/data/adb/ksu/module_configs/<id>/` | `/data/adb/smbshare/config.json`（自定） |
| 写入者 | `ksud`（须在模块上下文里，或 `--internal`） | 我们自己的二进制/脚本 |
| 结构 | 扁平 KV，最多 32 条 | 任意嵌套 JSON |
| 与 Root 方案耦合 | 强（只在 KernelSU 存在） | 无（Magisk/未知方案同用） |

**结论** `[推断/待验证]`：

1. **不冲突**，两者物理隔离，互不覆盖。
2. **单一真源**：`config.json` 是唯一真源；**不要**把同一份设置同时写进模块配置，双写必然出现「谁赢」问题。
3. **不要占用 `override.description` 与 `manage.*`**：这两个有 KernelSU 专有语义，占用会改变 Manager 行为或与其他模块冲突。
4. 若将来想「在 Manager 描述里显示运行状态」，可以用 `override.description`（一次性、短文本），但**必须确保模块卸载/禁用时清理**，否则描述会残留。`[已确认]`（语义）／清理必要性 `[推断/待验证]`

---

## 9. hot install（文档有、**本机 v3.3.1 没有**）

### 9.1 上游（`main`）语义 `[已确认]`

- 触发：`customize.sh` 里 `export MODULE_HOT_INSTALL_REQUEST=true`，可选 `export MODULE_HOT_RUN_SCRIPT=hotinstall.sh`（相对模块根的路径；符号链接指向模块外会被拒）。安装器把它写进**模块目录里的标记文件**（`.ksu_hot_install`），只有 `installer.sh` 能写（zip 里自带的同名文件会被删掉）。
- 执行顺序（安装脚本成功之后）：① 把 `/data/adb/modules/<id>` 换成新版（旧目录先移到 `/data/adb/ksu/hot_install_trash/`，**下次开机才删**，这样旧进程/旧挂载仍可用）；② 加载新版的 `sepolicy.rule`（**规则只增不减**，被删掉的规则要到下次重启才失效）；③ 运行 `MODULE_HOT_RUN_SCRIPT`（**最多等 30 s**，stdin `/dev/null`，忽略 `SIGPIPE`，后台进程不会被安装器退出带走，**装在 init 的 mount namespace 里**）；④ 不留 `update` 标记（Manager 不会提示重启）。
- **立刻生效**：模块文件本身、WebUI、`action.sh`、`module.prop` 信息。
- **仍需重启**：`system/` 等分区文件、`system.prop`、`initrc`、`preinit`、以及 `post-fs-data.sh`/`post-mount.sh`/`service.sh`/`boot-completed.sh` 这些阶段。
- **旧实例不会被停**，且**升级不执行 `uninstall.sh`**——官方原话：「If your module runs a daemon, `MODULE_HOT_RUN_SCRIPT` should stop the old instance before starting the new one.」
- 回退到「下次重启生效」的情形：装的是 metamodule、设备处于安全模式、`MODULE_HOT_RUN_SCRIPT` 不是模块内相对路径、模块目录无法替换。

### 9.2 本机判定（v3.3.1）

- `v3.3.1/userspace/ksud/src/main.rs` 的 `mod` 列表：有 `module`、`module_config`、`metamodule`、`sepolicy`、`su`、`sulog`、`late_load`、`magica`… **没有 `hot_install`、没有 `preinit`**。`[已确认]`
- `v3.3.1/userspace/ksud/src/defs.rs`：**没有** `HOT_INSTALL_MARKER`、没有 `HOT_INSTALL_TRASH_DIR`、没有 `HOT_INSTALL_SCRIPT_TIMEOUT`、没有 `MODULE_PREINIT_DIR`。`[已确认]`
- 结论：**本机（3.3.1）不支持 hot install → 升级必须重启**。文档站的 hot install 章节只适用于更新的版本（uapi 6 / `main`）。`[已确认]`（源码）／`[推断/待验证]`（运行期行为需实测）

### 9.3 对本模块的升级/重启设计（不依赖 hot install）

`[推断/待验证]`：

1. 升级 = 新版进 `modules_update` → 用户重启 → 开机合并 → `service.sh` 启动新版。**服务被重启打断是常态**，状态必须持久化、启动时自恢复。
2. 若将来 KernelSU 版本 ≥ 引入 hot install，可在 `customize.sh` 里请求 hot install，并在 `MODULE_HOT_RUN_SCRIPT` 里**先停旧实例再启新实例**（用 PID 文件 + `/proc/<pid>/cmdline` 校验，禁止 `pkill` 名字）。
3. **不要在安装器里假设服务在跑**：安装器运行在 Manager 上下文，与 `service.sh` 的 init mount namespace 不同（官方 hot install 实现里专门 `switch_mnt_ns(1)` 就是这个原因）。`[已确认]`（`hot_install.rs` 注释与调用）

---

## 10. `ksud` CLI 命令面

来自 `cli.rs` 原文（`main`，与 v3.3.1 基本一致，差异见 §9.2）`[已确认]`：

| 命令 | 说明 |
|---|---|
| `ksud module install <zip>` | 安装模块；**要求 `sys.boot_completed == 1`**（否则 `Android is Booting!` 直接失败） |
| `ksud module list` | 输出 JSON（`main` 含 `enabled/update/remove/web/action/mount/managedFeatures`；`preinit` 字段是新版才有，v3.3.1 预期无此字段 `[推断/待验证]`） |
| `ksud module uninstall <id>` | **只是打 `remove` 标记**，下次开机才真正删除 |
| `ksud module undo-uninstall <id>` | 撤销 `remove` 标记 |
| `ksud module enable/disable <id>` | 增删 `disable` 文件 |
| `ksud module action <id>` | 执行 `action.sh`（阻塞等待） |
| `ksud module config get/set/list/delete/clear [--temp] [--internal <name>]` | 模块配置读写 |
| `ksud module preinit status/reset` | **仅新版**（v3.3.1 无） |
| `ksud initrc refresh` | 重建 `modules.rc` + preinit 目录（下次开机生效） |
| `ksud late-load [--kmi <kmi>]` | late-load 模式加载 `kernelsu.ko` 并跑对应阶段脚本 |
| `ksud sepolicy patch/apply/check` | 运行时打策略（`--live` 类能力） |
| `ksud debug su [-g]` | root shell（可切全局 mount namespace） |
| `ksud debug info` | 打印 `version/flags/uapi_version/features/lkm/bundled/late_load/runtime_mode/pr_build` |
| `ksud --version` | 打印 `FULL_VERSION = "<VERSION_NAME> (uapi: <N>)"`；本机预期形如 `3.3.1 (uapi: 5)`（`VERSION_NAME` 是否带 `v` 前缀未实测）`[推断/待验证]` |

关键实现细节 `[已确认]`：

- **`Module` 子命令统一先 `utils::switch_mnt_ns(1)?`**（切到 init 的挂载命名空间）再执行，所以 `ksud module ...` 看到的 `/data/adb/modules` 与 `service.sh` 看到的一致。
- `install_module` 与 `run_action` 会先 `ksucalls::ensure_uapi_version_matched()` —— **`ksud` 与内核 uapi 不匹配会直接报错**，这也是「升级 KernelSU 要整体升级」的原因。
- 安全模式（safe mode）下模块全部禁用：连续多次未完成开机后由内核判定；官方救援文档给出「第一次开机画面后连按音量下 3 次」进入。`[已确认]`

---

## 11. `su` 与 SELinux

### 11.1 `su` 的实现路径

- `cli.rs` 开头：若 `argv[0] == "su"` 或以 `/su` 结尾，直接进 `crate::su::root_shell()` —— 即 `su` 是 `ksud` 的一个入口别名。`[已确认]`
- `su` 进程的 SELinux 上下文由内核侧设置：`KERNEL_SU_CONTEXT = "u:r:" KERNEL_SU_DOMAIN ":s0"`，`KERNEL_SU_DOMAIN = "ksu"` → **`u:r:ksu:s0`**（与本机 `su` 上下文一致）。`[已确认]`
- App Profile（Root Profile）可在**内核层**改写 `su` 的 `uid`/`gid`/`groups`/`capabilities`/SELinux 规则；官方还提示 `NO_NEW_PRIVS` 可阻止二次提权逃逸，并给出「把 su 的 uid 设为 2000 后，再加一个 2000 的授权即可二次 su 逃逸」的具体例子。`[已确认]`
- 与 Magisk 的差异：Magisk 的 `magisk` domain 是 permissive + unconstrained（`rules.rs`）；KernelSU 的 `ksu` domain 同样是 **permissive + allow-all**，但**由内核在启动时 patch sepolicy 实现**，并且额外显式声明了 `netdomain`、`bluetoothdomain`、`mlstrustedsubject` 等属性，还对 `chr_file/fifo_file/blk_file/file` 加了 `allowxperm`（任意 ioctl）。`[已确认]`（`rules.c`）／Magisk 侧见 `magisk.md` §5

### 11.2 对本项目的直接含义

- 绑定 445、读 `/proc/net/*`、开 `NETLINK_ROUTE`、写 `/data/adb/`、`chown/chmod` 全部**不会被 SELinux 拦**（permissive）。`[已确认]`（机制）／具体操作的 audit 行为 `[推断/待验证]`
- **不要写 `setenforce 0`**：既无必要（已在 permissive domain），又不可在运行时改策略（KernelSU 通过 `KSU_IOCTL_SET_SEPOLICY` 自己管），且有可观测痕迹。`[已确认]`＋`[推断/待验证]`
- 若需要给**别的 domain**（例如桌面 App、`untrusted_app`）访问我们的 socket/文件：在 KernelSU 上写 `sepolicy.rule`（`ksud` 在 post-fs-data 阶段 `apply_file`），**规则只增不减**；也可以用 `ksud sepolicy patch` 做运行时试验（不持久）。`[已确认]`
- **不要依赖 App Profile 的宽松默认**：用户可能给我们的 `su` 用户配了「受限 profile」（去掉 `inet` group 或限制 capability），此时网络能力会消失。启动自检应该**实测**（`socket()` + `bind()` 探测）而不是假定成功。`[推断/待验证]`

---

## 12. Android 15 / 16 / 17 兼容性、late-load、x86_64

### 12.1 版本与资产（Releases API 原文）`[已确认]`

| 项 | 事实 |
|---|---|
| 最新 release | **v3.3.1**，`published_at 2026-10-12T22:52:06Z`；APK 资产名 `KernelSU_v3.3.1_32680-release.apk`（`32680` 即 versionCode） |
| 上一版 | v3.3.0，`2026-09-08`，Release notes 含 `feat: add x64 lkm`、`kernel: add syscall dispatcher patching for x64` |
| LKM 资产覆盖面（v3.3.0 清单） | `aarch64` + `x86_64` × `android12-5.10`、`android13-5.10`、`android13-5.15`、`android14-5.15`、`android14-6.1`、**`android15-6.6`**、**`android16-6.12`**、**`android17-6.18`** |
| 其他资产 | `ksud-*`（android/musl/darwin/windows）、`ksuinit-{aarch64,x86_64,riscv64}` |

> 对本项目：**Android 15/16/17 都有官方 LKM 资产**，说明内核层已覆盖；我们的模块只依赖「root shell + procfs/netlink + 起进程」，不依赖任何 Android 框架 API，所以版本风险主要在**打包/权限**而不是 API。`[推断/待验证]`

### 12.2 设备支持范围（FAQ）`[已确认]`

- 官方支持 **GKI kernel 5.10+**（实务上等于「出厂 Android 12 及以上」的设备）；更老内核需要自行集成/移植（已 backport 到 4.14）。
- 官方 FAQ 强调：**兼容性取决于内核版本而不是 Android 版本**。`[已确认]`
- 只支持解锁 bootloader 的设备；`/system` 可写不建议（走 systemless）。

### 12.3 Runtime mode 与 late-load `[已确认]`

- `KSU_RUNTIME_MODE`：`built-in`（GKI 模式，编译进内核）/ `lkm`（开机时 insmod）/ `late-load`（开机后才加载）。
- `ksud late-load` 会检测 KMI、加载对应 `kernelsu.ko`，再执行初始化；**late-load 下不可用**的能力包括：initrc 注入、preinit、kprobe hooks（`execve/read/fstat/input`）、安全模式检测（恒禁用）、boot log 捕获、**Magisk coexistence check**、`post-fs-data` 事件上报（改用 `late-load` 阶段脚本）。仍可用：`system.prop`、OverlayFS 挂载、`post-mount`/`service`/`boot-completed` 脚本。
- 若 `KSU_LATE_LOAD=1`，脚本里应跳过「只在早期才有意义」的操作。**本模块的 `service.sh` 启动逻辑在 late-load 下仍会执行**，这是它的一个优势。`[推断/待验证]`

### 12.4 x86_64 `[已确认]`

- KernelSU 完整支持 x86_64，但新内核把 syscall 表的间接跳转硬化了，KernelSU 的 `syscall_hook` 需要额外处理，否则会**干净地放弃初始化以避免 kernel panic**。
- 两条路（**只能选一条**）：编译期打开 `KSU_X86_PATCH_SYSCALL_DISPATCHER`（3.3.0 引入，推荐），或继续用原来的内核源码补丁（`X86_FEATURE_INDIRECT_SAFE` + cmdline `syscall_hardening=off`）。
- 官方有安全警示：这两种做法都会重新打开间接分支攻击面，**不要用在生产服务器**。
- 对本项目：arm64 手机不受影响；若未来出 x86_64 平板/模拟器版本，需注意 KMI 与 LKM 选择。

### 12.5 16 KB page size

- Magisk 侧自 v28.0 支持 16k page size（见 `magisk.md` §6）；**KernelSU 文档站没有对应的「16k page size」条目**，本轮无法确认其对第三方 native 二进制的具体要求。`[推断/待验证]`
- 工程建议：我们的 Samba 二进制按 **16 KB 对齐**编译并上机 `getconf PAGESIZE` 验证，不要假定 4 KB。

---

## 13. 卸载 / 升级流程与 `uninstall.sh` 行为

### 13.1 从源码看到的精确时序 `[已确认]`

`ksud module uninstall <id>`：

1. `validate_module_id` → 校验 `/data/adb/modules/<id>` 存在 → **`File::create(remove)`**（打标记）→ `regenerate_preinit()`（重建 `modules.rc`）。**此时什么都没删。**

下一次开机（`post-fs-data` 阶段）：

2. `prune_modules()` 遍历 `/data/adb/modules`，对存在 `remove` 的模块依次：
   - 若该模块是 metamodule → 删 `/data/adb/metamodule` 符号链接；否则 → 执行 **metamodule 的 `metauninstall.sh`**（若存在）
   - 执行 **模块自己的 `uninstall.sh`**（若存在，`ScriptWait::Forever`，失败只 warn）
   - `module_config::clear_module_configs(module_id)`（清模块配置）
   - `remove_dir_all(module)`
3. 因此 `uninstall.sh` 执行时：**旧进程早已随重启消失**、模块目录**还在**（脚本能读到自己的文件）、`/data/adb/ksu/module_configs/<id>/` **还没删**、metamodule 钩子**已经跑过**。

`ksud module undo-uninstall <id>`：删 `remove` 标记（升级/误操作的救援口）。

### 13.2 升级流程 `[已确认]`

- `ksud module install <zip>`：解压到 `/data/adb/modules_update/<id>`，运行安装脚本；成功后在 `/data/adb/modules/<id>` 建目录、复制 `module.prop`、写 `update` 标记（**除非**走 hot install，v3.3.1 没有）。
- 下次开机由 `handle_updated_modules()` 把 `modules_update/<id>` 搬进 `modules/<id>`（旧目录 `remove_dir_all`），并**继承旧的 `disable`/`remove` 标记**。
- **升级不执行 `uninstall.sh`**。`[已确认]`（hot install 文档明确）＋ `[推断/待验证]`（普通升级路径未明文，但代码里只有 `prune_modules` 会调 `uninstall.sh`）
- 模块目录被整体替换 → **运行期状态不能放模块目录里**（PID、socket、日志放 `/data/adb/smbshare/`）。`[推断/待验证]`

### 13.3 对本模块的 `uninstall.sh` 写法

`uninstall.sh` 只做**文件与状态清理**，且要在「目录还存在」的前提下写：`[推断/待验证]`

1. 停进程不做（那一刻没有我们的进程）；**不要 `pkill`/`kill -9`**，会误伤同 PID 的其他进程。
2. 删 `/data/adb/smbshare/` 下的 socket、pid、日志、临时文件；**默认保留 `config.json`**（用户重装后可复用），只在 `--purge` 场景删。
3. 不要删 `/data/adb/ksu/**`（那是 KernelSU 的地盘）。
4. 幂等：任何一步失败都 `continue`，不要 `exit 1` 中断（失败只影响清理彻底度）。

---

## 14. 对本项目的约束清单（KernelSU 侧）

- `module.prop` 严格合法（LF 换行、`id` 正则、整数 `versionCode`）；`id` 与仓库名一致。
- **不放 `system/`、不依赖 metamodule**；安装器不因缺少 metamodule 而 abort。
- 服务启动放 `service.sh`（非阻塞、官方推荐）；需要「开机完成」时加 `boot-completed.sh` 或轮询 `sys.boot_completed`；`post-fs-data.sh` 只做极轻量动作（目录/权限修复），**禁止 `setprop`**（用 `resetprop -n`）。
- **不用 hot install**（3.3.1 不支持）；升级假设「重启生效」，状态持久化在 `/data/adb/smbshare/`。
- **不用 `preinit/`**（3.3.1 不支持）。
- **不写 `setenforce 0`**；需要给其他 domain 权限时写 `sepolicy.rule`（只增不减）。
- `config.json` 是唯一配置真源；**不双写** KernelSU 模块配置；**不占用** `override.description`/`manage.*`。
- WebUI：主入口为独立 HTTP 服务（自带认证 + 私网来源过滤）；`webroot/index.html` 可选，**不依赖 `kernelsu` JS 库**。
- `action.sh` 提供「重启服务 / 显示状态 / WebUI 地址」三件套（Manager 里唯一官方可达入口）。
- `uninstall.sh` 只清文件、不 kill 进程、幂等。
- 版本门槛：本机 3.3.1/uapi 5 为基线；若用到 `ksud` 新子命令，先 `ksud --version`/`ksud debug info` 探测，不满足就降级。`[推断/待验证]`

---

## 15. 证据不足 / 待上机验证

1. **本机是否真的没有 hot install**：已由 `v3.3.1` tag 的 `main.rs`/`defs.rs` 缺失证明，但**未在真机跑 `MODULE_HOT_INSTALL_REQUEST=true` 验证**（预期会被忽略）。
2. **`post-fs-data` 阻塞上限是 10 s 还是 35 s** —— 文档 10 s、源码常量 35 s，成因未确认；实现取保守值。
3. **`uninstall.sh` 在「普通升级」路径是否执行** —— 文档只说 hot install 时**不**执行；普通升级路径代码上只有 `prune_modules()` 会调用，故推断不执行。
4. **`ksud module install` 的「必须 boot completed」在实践中是否有绕过** —— 源码是硬 `bail!("Android is Booting!")`，但 Manager 是否有其它入口未确认。
5. **`webroot/` 页面在 Manager WebView 中的隔离细节**（同源策略、远程加载是否被拦）—— 未读 Manager 的 `WebUIActivity`/`WebViewInterface` 源码，属推断。
6. **KernelSU 对 16 KB page size 的要求** —— 文档站无对应条目，未取证。
7. **late-load 模式下 `service.sh` 的确切行为** —— 文档表格说「可用」，但与本模块的启动时机交互未实测。
8. **`override.description` 的清理责任** —— 语义已确认，但「模块卸载/禁用后是否自动重置」未见文档说明。
9. **KMI 与本机内核**：`android16-6.12` 只是资产命名，本机实际 KMI 需 `ksud boot-info current-kmi` 确认。
10. **`KSU_VER` 的精确取值**（是否带 `v` 前缀、是否等于 tag 名）—— 文档示例是 `v0.4.0`，本机未实测。

---

## 16. 来源

**KernelSU 官方文档站（`kernelsu.org`）**

- Module guide（目录树、`module.prop`、`system/` 与 metamodule 关系、initrc、preinit、hot install、`customize.sh` 变量/函数、boot scripts、late-load）：https://kernelsu.org/guide/module.html
- Difference with Magisk（`KSU` 变量、BusyBox 路径、不支持 `.replace`、`.replace`/`REMOVE`/`REPLACE`、额外阶段、Recovery 不支持）：https://kernelsu.org/guide/difference-with-magisk.html
- FAQ（仅需 metamodule 才能改 `/system`、与 Magisk 共存、GKI 5.10+、内核版本决定兼容性）：https://kernelsu.org/guide/faq.html
- Metamodule（`metamodule=1`、单实例、`metamount.sh`/`metainstall.sh`/`metauninstall.sh`、`source=KSU`、`/data/adb/metamodule`、执行顺序）：https://kernelsu.org/guide/metamodule.html
- Module WebUI（`webroot/index.html`、`kernelsu` JS 库、`exec` 示例、权限由安装器设置）：https://kernelsu.org/guide/module-webui.html
- Module Configuration（路径、persist/tmp、magic、限制、`override.description`、`manage.*`）：https://kernelsu.org/guide/module-config.html
- App Profile（uid/gid/groups/capabilities/SELinux、`u:r:ksu:s0`、`NO_NEW_PRIVS`、escalation 例子）：https://kernelsu.org/guide/app-profile.html
- Rescue from bootloop（安全模式、`ksud module list/disable/uninstall`、删 `/metadata/**/modules.rc`）：https://kernelsu.org/guide/rescue-from-bootloop.html
- x86_64 support（`KSU_X86_PATCH_SYSCALL_DISPATCHER`、`X86_FEATURE_INDIRECT_SAFE`、安全警示）：https://kernelsu.org/guide/x86_64-support.html
- Hidden features（`/data/adb/ksu/.ksurc`）：https://kernelsu.org/guide/hidden-features.html

**KernelSU 仓库源码（`main` 与 `v3.3.1`）**

- `userspace/ksud/src/cli.rs`（命令面、`argv[0]=="su"` 入口、`switch_mnt_ns(1)`）：https://cdn.jsdelivr.net/gh/tiann/KernelSU@main/userspace/ksud/src/cli.rs
- `userspace/ksud/src/module.rs`（`prune_modules`/`handle_updated_modules`/`exec_script`/`run_action`/`get_common_script_envs`/`list_module`）：https://cdn.jsdelivr.net/gh/tiann/KernelSU@main/userspace/ksud/src/module.rs
- `userspace/ksud/src/defs.rs`（`MODULE_DIR`/`MODULE_UPDATE_DIR`/`HOT_INSTALL_*`/`PREINIT_*`/`BOOT_STAGE_TIMEOUT=35s`）：https://cdn.jsdelivr.net/gh/tiann/KernelSU@main/userspace/ksud/src/defs.rs
- `userspace/ksud/src/hot_install.rs`（hot install 实现：marker、trash、30 s、additive sepolicy）：https://cdn.jsdelivr.net/gh/tiann/KernelSU@main/userspace/ksud/src/hot_install.rs
- `uapi/supercall.h`（`main`: `KERNEL_SU_UAPI_VERSION = 6`）：https://cdn.jsdelivr.net/gh/tiann/KernelSU@main/uapi/supercall.h
- `kernel/selinux/rules.c`（`ksu_permissive`、`allow all`、`allowxperm`、`netdomain`）：https://cdn.jsdelivr.net/gh/tiann/KernelSU@main/kernel/selinux/rules.c
- `kernel/selinux/selinux.h`（`KERNEL_SU_DOMAIN "ksu"`、`u:r:ksu:s0`）：https://cdn.jsdelivr.net/gh/tiann/KernelSU@main/kernel/selinux/selinux.h
- `js/index.d.ts`（WebUI JS API 面）：https://cdn.jsdelivr.net/gh/tiann/KernelSU@main/js/index.d.ts
- **v3.3.1 对照**：`main.rs`（无 `mod hot_install;`/`mod preinit;`）https://cdn.jsdelivr.net/gh/tiann/KernelSU@v3.3.1/userspace/ksud/src/main.rs ；`defs.rs`（无 hot install / preinit 常量）https://cdn.jsdelivr.net/gh/tiann/KernelSU@v3.3.1/userspace/ksud/src/defs.rs ；`uapi/supercall.h`（`KERNEL_SU_UAPI_VERSION = 5`）https://cdn.jsdelivr.net/gh/tiann/KernelSU@v3.3.1/uapi/supercall.h

**Releases**

- GitHub Releases API（v3.3.1 `2026-10-12`、APK `32680`、v3.3.0 的 LKM 资产矩阵）：https://api.github.com/repos/tiann/KernelSU/releases
- v3.3.1 tag：https://github.com/tiann/KernelSU/releases/tag/v3.3.1

**引用但本轮不可取证（已降级）**

- AOSP / Android 开发者文档（`source.android.com`、`developer.android.com`，fetch failed）
- `github.com`、`raw.githubusercontent.com`（本轮未验证；仓库内容经 jsDelivr 读取，同源）
- Magisk 对照结论：见本仓库 `docs/research/magisk.md`（同日调研）
