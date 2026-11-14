# SELinux 技术调研（Android 15–17 · Magisk / KernelSU · root SMB 模块）

- **调研日期**：2026-11-14
- **方法**：四类一手材料交叉核对，**不**给出任何"在真机上跑通"的未验证结论：
  1. **AOSP 官方文档**：`source.android.com` 的 SELinux 概览（默认拒绝、Enforcing/Permissive、denial 落盘位置）原文抓取后逐句比对。
  2. **AOSP 策略源码**：通过 `android.googlesource.com` 取 `system/sepolicy` 的 `private/file_contexts`（53 KB，base64 解码后逐条检索）、`private/su.te`、`private/adbd.te`、`public/app.te`。
  3. **Magisk 侧**：本仓库 `docs/research/magisk.md` 已确认的官方结论（`magisk`/`magisk_file` domain、`sepolicy.rule` 注入时机、`rules.rs` 的 `deny(... load_policy)`），以及 `topjohnwu.github.io/Magisk` 的 details/tools 页面。
  4. **KernelSU 侧**：`kernelsu.org/guide/module.html` 原文（模块目录树含 `sepolicy.rule`、`preinit/` 在策略加载之前执行、hot install 时"rules are only ever added"）。
- **项目实测（仅引用，不扩展）**：OnePlus PJA110 / Android 16 / KernelSU，`getenforce` 为 `Enforcing`；su 上下文为 `u:r:ksu:s0`；同一环境可执行 Termux Samba 4.25.1 的 `smbd --version`。
- **可信度**：`[已确认]` = 官方文档/源码原文或本次可复现的设备取证；`[推断/待验证]` = 由证据推导、尚未上机。
- **不覆盖**：SELinux 类型强制（TE）理论、MLS/MCS 完整语义、厂商（OEM）私有策略差异。Samba 侧见 `docs/research/samba.md`。

---

## 1. 结论摘要（先看这 9 条）

1. Android 的 SELinux 是**默认拒绝 + Enforcing**：AOSP 原文"Anything not explicitly allowed is denied"，并且 denial 会同时进 `dmesg` 与 `logcat`——这正是本项目排查问题的第一手线索来源。`[已确认]`
2. **per-domain permissive ≠ 全局 permissive**：AOSP 支持"单个 domain 放行、其余仍 Enforcing"。AOSP 自己的 `su` domain 在 `userdebug/eng` 版本里就是 `permissive su;`（源码注释："su is also permissive to permit setenforce"）。`[已确认]`
3. **Magisk 的 `magisk` domain 是官方定义的 "unconstrained, permissive domain"**：`magiskd` 与所有 root shell 都在里面。因此"root shell 里跑什么都行"是 Magisk 的特性，**不是 SELinux 被关了**。`[已确认]`
4. 本项目实测的 `u:r:ksu:s0` 只证明**标签**是 KernelSU 的 su domain；KernelSU 该 domain 是否也 permissive、放宽到什么程度，**未取证**。`[已确认·label]` + `[推断/待验证]`
5. **永远不要 `setenforce 0`**：Magisk 侧不需要（已在 permissive domain 内）、且在 Magisk 下其它 domain 被禁止 `load_policy`；更重要的是它会削弱整机安全边界并留下可观测痕迹。`[已确认]` + `[推断]`
6. 策略注入只有两条正路：Magisk 的 `sepolicy.rule`（**pre-init** 注入，持久化在 cache/metadata）与 KernelSU 的模块 `sepolicy.rule`（hot install 与开机加载，**规则只增不减**）。运行期乱改不是常规手段。`[已确认]`
7. 本项目真正需要的 SELinux 例外**可能极少甚至为零**：若守护进程由 root 启动、跑在 `magisk`/`ksu` domain，绝大多数文件访问不会产生 denial；风险恰恰在于"看起来没触发"——见 §5 的风险列。`[推断/待验证]`
8. WebUI 用 **127.0.0.1 的 HTTP** 而不是"App 直连 unix socket"，可以绕开一整套跨 domain 的 `unix_stream_socket`/`connectto` 策略问题，是更省事的架构选择。`[推断/待验证]`
9. 排查顺序固定为：`getenforce` → 复现 → `dmesg | grep avc` / `logcat` 抓 denial → 判断是"缺策略"还是"路径/权限/标签错" → 只补**最小** `allow`。绝不建议关 SELinux。`[已确认·机制]` + `[推断·流程]`

---

## 2. Android SELinux 基础（只写有原文的部分）

### 2.1 模式与"默认拒绝" `[已确认]`

AOSP 原文（SELinux 概览）：

- "SELinux operates on the principle of **default denial**: Anything not explicitly allowed is denied."
- "SELinux can operate in two global modes: **Permissive** mode, in which permission denials are logged but not enforced. **Enforcing** mode, in which permissions denials are both logged and enforced."
- "Android includes SELinux in enforcing mode and a corresponding security policy that works by default across AOSP. In enforcing mode, disallowed actions are prevented and all attempted violations are logged by the kernel to **dmesg and logcat**."
- "SELinux also supports a **per-domain permissive mode** in which specific domains (processes) can be made permissive while placing the rest of the system in global enforcing mode."
- 历史节点：Android 4.3 引入 SELinux；4.4 部分强制；**Android 5.0 起全面强制**（从 `installd`/`netd`/`vold`/`zygote` 扩展到 60+ domain）；Android 6.0 进一步收紧（含"extremely limited `/proc` access"）；7.0 继续锁 domain。

### 2.2 标签语法与几个必懂名词

- 进程标签形如 `u:r:ksu:s0`：`u`=SELinux user，`r`=role，`ksu`=**domain**（type 的一种），`s0`=MLS level（Android 里常见 `s0`，部分场景带 `:cX,cY` 分类）。`[已确认·格式]`
- **domain**：进程的 type；同 domain 的进程在策略里被一视同仁。`[已确认]`
- **type**：文件/套接字/节点等对象的标签，权限规则是 `allow <domain> <type>:<class> <perms>`。`[已确认]`
- **file_contexts**：路径 → 标签的映射表，`restorecon` 依据它重贴标签；新文件默认继承父目录标签。`[已确认·机制]`
- **attribute**：type 的分组（如 `file_type`、`domain`、`net_domain`），策略里常用它做批量授权——也是最容易写出"过宽规则"的地方。`[已确认]`

### 2.3 关键路径的官方标签（本次从 AOSP `private/file_contexts` 实测）`[已确认]`

| 路径（正则） | 标签 |
|---|---|
| `/data/adb(/.*)?` | `u:object_r:adb_data_file:s0` |
| `/data/media` | `u:object_r:media_userdir_file:s0` |
| `/data/media/.*` | `u:object_r:media_rw_data_file:s0` |
| `/storage(/.*)?` | `u:object_r:storage_file:s0` |
| `/mnt/media_rw(/.*)?` | `u:object_r:mnt_media_rw_file:s0` |
| `/dev/fuse` | `u:object_r:fuse_device:s0` |
| `/mnt`、`/debug_ramdisk`、`/second_stage_resources` | `u:object_r:tmpfs:s0` |

推论（重要）：`/data/adb/smbshare/**` 下我们自己创建的文件**天然继承 `adb_data_file`**（父目录正则覆盖子树），不需要 `restorecon` 也能有"看起来正确"的标签；但"标签正确"不等于"别的 domain 能访问"。`[已确认·file_contexts]` + `[推断]`

### 2.4 su / magisk / ksu：三种"root 上下文"

**AOSP 的 `su`（`private/su.te`，整段包在 `userdebug_or_eng(...)` 里）** `[已确认]`：

- 只有 `userdebug`/`eng` 构建才有；`domain_auto_trans(shell, su_exec, su)` 让 `shell` 执行 `/system/bin/su` 时切到 `su` domain。
- 源码里直接写：`# su is also permissive to permit setenforce.` 然后 `permissive su;`
- 还有 `app_domain(su)`、`net_domain(su)`、大量 `dontaudit su ...`（把噪音关掉）。
- **含义**：在 AOSP 自己的 debug 版上，"su 是 permissive"是官方设计；而我们设备上没有 AOSP `su`，是 Magisk/KernelSU 注入的等价物。

**Magisk 的 `magisk` domain** `[已确认·官方文档/源码]`：

- 官方描述："An unconstrained, permissive domain that `magiskd` and all root shells run in"；`rules.rs` 里有 `permissive([proc])` 与 `allow([proc], [fs_type, dev_type, file_type, domain, ...], all, all)`。
- 另有 `magisk_file`：官方定义"An unrestricted file type accessible by all domains"，用于 Magisk 自己的 socket/二进制。
- 同时 `deny(all, ["kernel"], ["security"], ["load_policy"])`（注释："Prevent anything to change sepolicy except ourselves"）——**其它 domain 不许再加载策略**。
- 含义：在 Magisk 上，"root shell 里连 SELinux denial 都很少见"是因为 domain 本身宽松，不是 Enforcing 失效。

**KernelSU 的 `ksu` domain** `[已确认·label]` + `[推断/待验证]`：

- 项目实测 su 上下文为 `u:r:ksu:s0`；这是我们能确认的全部。
- KernelSU 的模块文档明确 `preinit/` 里由 `exec` 起的程序"run as root with full capabilities and **no SELinux restriction**"，说明其设计目标是让 root 侧尽量不撞策略。`[已确认]`
- 但 `ksu` domain 在策略中具体被放宽到什么程度（是否 permissive、放了哪些 type）**没有公开逐条文档**，必须以设备上 `ls -Z`/`dmesg` 实测为准。`[推断/待验证]`

---

## 3. 策略注入：Magisk 与 KernelSU 的正规路径

### 3.1 Magisk `sepolicy.rule` `[已确认]`

- 位置：模块根目录 `sepolicy.rule`，**每行一条 policy statement**（语法同 `magiskpolicy`）。
- 时机：**pre-init** 由 `magiskinit` 与内建规则一起注入已加载策略，早于 `/data` 挂载与所有模块脚本。
- 持久化：规则会被写到 pre-init 分区（`cache` 或 `metadata`），**擦除该分区会丢失模块策略直到重装**。
- 运行期工具：Magisk CLI 提供 `magiskpolicy`（可 `--live` 对当前策略打补丁）与 `magisk --restorecon`（重贴 Magisk 文件上下文）。高级/排障用，不作为模块常规机制。
- 风险：语法错误发生在极早的开机阶段；规则"只增不减"，删掉的规则要等重启后不再加载才失效。

### 3.2 KernelSU `sepolicy.rule` `[已确认]`

- 模块目录树里 `sepolicy.rule` 的注释是"Additional custom sepolicy rules"；`preinit/` 则是"preinit.conf and its files, **run before SELinux policy is loaded**"。
- hot install 流程（模块文档原文顺序）：替换模块目录 → **加载 `sepolicy.rule`（"Rules are only ever added, so rules dropped by the new version stay active until the next reboot"）** → 运行 `MODULE_HOT_RUN_SCRIPT`。
- 安装器语义：模块文件默认 `0644`，需要执行位的程序要在 `customize.sh` 里 `set_perm ... 0755`。
- 与 Magisk 的关键差异：KernelSU 有"策略加载之前"的 `preinit/` 窗口（连策略都还没有），所以那一步的程序"无 SELinux 限制"；但 preinit 环境极简陋（无 shell、无 property、无 ueventd）。

### 3.3 两套机制对照 `[已确认]`

| 维度 | Magisk | KernelSU |
|---|---|---|
| 规则文件位置 | 模块根目录 `sepolicy.rule` | 模块根目录 `sepolicy.rule` |
| 注入时机 | **pre-init**，由 `magiskinit` 与内建规则一起注入已加载策略 | 开机流程中加载；hot install 时也会加载（安装期即生效） |
| 持久化 | 写入 pre-init 分区（`cache`/`metadata`），擦除即丢失 | 随模块目录；"rules are only ever added"，删除的规则要等下次重启 |
| 语法 | 每行一条 policy statement，同 `magiskpolicy` 语法 | 同左（KernelSU 复用同族语法） |
| 运行期改策略 | `magiskpolicy --live`（排障用，非常规路径） | 文档未给出等价"live"入口；以重启为准 |
| 是否存在"策略加载前"窗口 | 有 pre-init，但那是**注入规则**的窗口，不是给模块跑任意程序 | 有 `preinit/`，`exec` 的程序**在策略加载之前**以 root + 全 capability 运行（"no SELinux restriction"） |
| 已知限制 | 其它 domain 被 `deny(... load_policy)`；规则只增不减 | preinit 环境极简陋（无 shell、无 property、无 ueventd、静态链接程序限定） |

> 对我们最重要的差异：KernelSU 的 `preinit/` 能在"策略还不存在"时做极早期动作（例如准备目录、加载内核模块），但它**不能**当作常规启动脚本用；模块的正常启动逻辑仍应放在 `service.sh` / `post-fs-data` 一类阶段。`[推断/待验证]`

### 3.4 为什么不能 `setenforce 0`

分层给理由，强度从硬到软：

1. **不需要**：Magisk 下 root shell 已在 permissive `magisk` domain；`setenforce 0` 不带来任何额外权限。`[已确认]`
2. **改策略的正门被封了**：Magisk 明确 `deny(all, kernel, security, load_policy)`——只有 Magisk 自己能改策略；要例外就写 `sepolicy.rule`。`[已确认]`
3. **时序不对**：`sepolicy.rule` 在 pre-init 就加载完了；`post-fs-data`/`service.sh` 阶段已经太晚，此时"临时关一下"只会让问题在重启后重现。`[已确认·magisk.md]`
4. **副作用**：全局 Permissive 会被完整性/风控类检查看到（`/sys/fs/selinux/enforce` 可读），且等于把整机所有 domain 的边界一起拆掉——为了一个 SMB 服务这样做是明显的过度授权。`[推断/待验证]`
5. **可移植性**：代码里一旦写了 `setenforce 0`，就等于把"依赖 permissive"这个脏假设固化进实现，换 Root 方案/换设备必炸。`[推断]`

> 工程结论：**任何**模块脚本、守护进程、WebUI 后端里都不允许出现 `setenforce 0` / 写 `/sys/fs/selinux/enforce`。这条应当写进 code review checklist。

---

## 4. root daemon 的访问检查点

> 下表的"是否可能不触发"列是重点：**没有 denial 不代表没有风险**，只代表当前 domain 的宽松程度掩盖了它。

| # | 访问点 | 相关标签/对象 | 需要的 SELinux 权限（概念） | 本机是否可能默认不触发 | 证据/备注 |
|---|---|---|---|---|---|
| 1 | 共享 `/storage/emulated/0` | FUSE 挂载 + `media_rw_data_file` / `storage_file` | `fuse_device` 使用；对挂载内 `file`/`dir` 的 `open/read/write/getattr/search`；`filesystem` 相关 | 可能不触发（root domain 宽松） | `[已确认·file_contexts]`；FUSE 由 MediaProvider 承载 `[已确认]` |
| 2 | 直接读写 `/data/media/0` | `media_rw_data_file` | `dir: search`、`file: { read write open getattr setattr }`、`dir: { add_name remove_name }` | 可能不触发 | 同上；绕过 MediaProvider 有媒体库不一致风险 `[推断]` |
| 3 | 读写自己的目录 `/data/adb/smbshare` | `adb_data_file`（继承） | `dir/file` 常规读写创建 | 可能不触发 | 目录本身默认 `700 root:root` `[已确认·magisk.md]` |
| 4 | 读模块目录 `/data/adb/modules/<id>` | `adb_data_file` | `dir: search`、`file: read` | 可能不触发 | Module 文件上下文由安装器设为 `system_file`/`magisk_file` 之一，需实测 `ls -Z` `[推断/待验证]` |
| 5 | bind 445 | `node_type:node`、`self:capability` | `capability net_bind_service`；`node: { bind }`（视策略） | 可能不触发 | root + <1024 端口 `[已确认·Samba man]` |
| 6 | Unix socket（若采用） | 自己的 socket 文件（`adb_data_file`） | `unix_stream_socket: { create bind listen accept }`；**对端** domain 需 `connectto` | **很可能触发** | 跨 domain（App → daemon）才是难点 `[推断/待验证]` |
| 7 | `/dev/urandom` | `dev_type:chr_file` | `read` | 可能不触发 | smbd 启动需要随机源 `[推断]` |
| 8 | `/proc`（进程信息、`/proc/net`） | `proc_type` / `proc_net_type` | `dir: search`、`file: read` | 可能不触发 | Android 6.0 起收紧 `/proc` `[已确认·AOSP 文档]` |
| 9 | `getifaddrs()` 枚举接口 | netlink socket（`netlink_route_socket` 等） | `{ create bind getattr read write }` | 可能不触发，但失败表现是"枚举不到接口"而非报错 | Termux 交叉编译把接口枚举改写过 `[已确认·samba.md §5]` |
| 10 | 客户端（PC）连接进来 | `node_type:tcp_socket`、`netif` | 域内 `tcp_socket` 相关权限 | 可能不触发 | 同上 `[推断]` |

**为什么"不触发"是风险而不是好消息**：`magisk` 是 permissive、KernelSU 的 `ksu` 也可能大幅放宽，一旦你把服务迁到更"正规"的 domain（例如未来改成由 `init` 拉起、或用户换成别的 Root 方案），同一套代码会突然全部 deny。**结论：不要以"当前没报错"作为 SELinux 需求已满足的证据。** `[推断/待验证]`

---

## 5. 最小权限实践（与 SELinux 互补，不是替代）

1. **属主/权限**：`/data/adb/smbshare` 及其子目录 `0700`/`0750`，属主 `root:root`；配置 `0640`；`passdb.tdb`、`secrets.tdb`、私钥类 `0600`；可执行二进制 `0755`。`/data/adb` 本身就是 `700 root`，别去改它。`[已确认·magisk.md]` + `[推断]`
2. **umask**：守护进程启动即 `umask 077`，避免任何临时文件默认 `0644`。`[推断]`
3. **socket 权限**：如果保留 Unix socket，用 `0600`（仅 root）比 `0660`+特殊组更安全，因为 Android 的 App 无法加入 root 组；**跨 App 访问本来就该走 WebUI 的 HTTP + token**。`[推断/待验证]`
4. **WebUI 认证**：默认只监听 `127.0.0.1`；一次性随机 token（≥128 bit），比对用常量时间；不把 token 写进日志；不提供"无认证"模式。若必须对外监听，等同把 root 控制面暴露到局域网。`[推断]`
5. **不新增宽规则**：禁止 `allow <domain> * * *`、禁止对 `file_type`/`domain` 这类 attribute 一次性放行、禁止 `permissive` 整个 App domain。`[已确认·Magisk 的 --magisk 语义]` + `[推断]`
6. **文件不要放 FUSE 共享盘**：密码库/日志/pid 放 `/data/adb/smbshare`，杜绝"用户能通过 SMB 读到 `passdb.tdb`"。`[推断]`
7. **日志脱敏**：对含 `passwd`/`password`/`token` 的行整行丢弃；`smb.conf` 里不放明文凭据。`[推断]`

---

## 6. 采集 denial 与验证方法

### 6.1 采集 `[已确认·AOSP 原文说明 denial 去 dmesg 与 logcat]`

```bash
# 1. 先确认模式（Enforcing 才是预期状态）
getenforce

# 2. 复现问题（启动 smbd / 访问共享 / 打开 WebUI）

# 3. 抓 denial：dmesg（root）
dmesg | grep -i 'avc:' | tail -50

# 4. 抓 denial：logcat（SELinux 相关事件）
logcat -d -b all | grep -iE 'avc:|selinux' | tail -50

# 5. 相关对象/进程的标签
ls -Z /data/adb/smbshare /data/adb/modules/<id> 2>/dev/null
ps -A -o PID,NAME,LABEL | grep -E 'smbd|asrd'
cat /proc/$(pidof smbd | awk '{print $1}')/attr/current

# 6. 挂载点与 FUSE 视图
mount | grep -E 'emulated|fuse'
```

补充要点：

- `dmesg` 在部分设备受 `kernel.dmesg_restrict` 限制；本项目有 root，通常可直接读。`[推断/待验证]`
- denial 里的 `scontext`/`tcontext`/`tclass`/`permissive=` 四个字段必须完整记录：`permissive=1` 表示"该 domain 当前是 permissive，只是记日志"，这类记录**不能**用来证明策略已正确。`[已确认·AOSP 机制]`
- 用 `audit2allow` 生成候选规则可以做参考，但**必须人工裁剪到最小**——它会把 `file_type` 之类 attribute 一起放行。`[推断]`

### 6.2 最小 `sepolicy.rule` 的思路（示例形态，未验证）

只有在 §5 的常规手段都用尽后，才考虑写规则。写法上要满足三条：**只给需要的 domain、只给需要的 type、只给需要的 class/perm**。

```text
# 形态示例（不是本项目已确认的最终规则，必须按实测 denial 逐条生成）
# 1) 给 root daemon 域访问我们自己的目录（若 deny 真的出现）
allow <daemon_domain> adb_data_file:dir { search open read write add_name remove_name };
allow <daemon_domain> adb_data_file:file { open read write getattr setattr create unlink };

# 2) 允许绑定 445（若 node_bind 被拦）
allow <daemon_domain> self:capability net_bind_service;
```

注意事项 `[已确认·Magisk/KernelSU 文档]`：

- 规则在 **pre-init** 注入（Magisk）或安装/开机加载（KernelSU），语法错误可能导致开机异常；**先在测试机、保留可用 `uninstall.zip` 的情况下验证**。
- 规则"只增不减"：删掉的规则要等重启；KernelSU hot install 下更是如此。
- 不要抄网上"`allow magisk * * *`"这类万能规则；那是把 Magisk 自身 domain 的宽松复制给所有人。

### 6.3 验证矩阵

| 场景 | 期望 | 检查命令 | 通过标准 |
|---|---|---|---|
| 开机后模式 | Enforcing | `getenforce` | 输出 `Enforcing`（任何 `Permissive` 都视为失败） |
| 守护进程身份 | 预期 domain | `cat /proc/<pid>/attr/current` | 与设计一致（如 `u:r:ksu:s0`），且与 `ps -Z` 一致 |
| smbd 启动 | 无 avc denial | `dmesg \| grep avc`（启动前后对比） | 无新增 `avc: denied` |
| 共享可列举 | 无 denial | PC 侧 `net use`/`smbclient -L` | 能列出共享，且日志无 deny |
| 大文件写入 `/storage/emulated/0` | 无 denial 且数据落地 | 传文件 + `ls -Z` 目标文件 | 内容校验一致，标签为 `media_rw_data_file` |
| 删除/改名 | 无 denial | 重命名目录、删除文件 | 成功且无 deny |
| 445 监听 | 绑定成功 | `ss -ltnp \| grep 445`（或 `netstat`） | 监听在预期接口，非 `0.0.0.0` 意外暴露 |
| WebUI | 仅本机可访问 | 从 PC 访问手机 IP:port | 拒绝或需要 token；`127.0.0.1` 正常 |
| 卸载/升级后 | 无残留策略副作用 | 重装模块 → 重启 → 重跑上表 | 行为一致（注意规则"只增不减"） |

---

### 6.4 六个常见误判

1. **`permissive=1` 的 denial 被当成"策略已修好"**：该字段原文含义是"当前 domain 是 permissive，只记录不拦截"。看到它说明**你根本没在 Enforcing 下验证过**。`[已确认·AOSP 机制]`
2. **标签正确却访问失败**：先看是不是非 SELinux 因素——UNIX 权限位、`mount` 的 `nosuid/nodev/noexec`、路径本身不存在、FUSE 后端的策略拒绝（MediaProvider 的 allow/deny/redact 是**用户态**决策，不产生 avc）。`[已确认·AOSP 文档]` + `[推断]`
3. **`dontaudit` 把 denial 藏了**：AOSP 与 Magisk 都大量使用 `dontaudit`（例如 AOSP `su.te` 里成片的 `dontaudit su ...`），所以"`dmesg` 里没有 avc"≠"没被拦"。要么用 `semodule`/重编策略临时关 `dontaudit`（工程上不现实），要么用行为差异（返回值/errno）判断。`[已确认·su.te]` + `[推断]`
4. **denial 被内核限流/环形缓冲冲掉**：重复 deny 会触发 `printk` 限流，`dmesg` 也可能被后续日志滚掉。抓取时应在**复现瞬间**立刻取，并同时看 `logcat`。`[推断/待验证]`
5. **把 `EACCES` 一律归因 SELinux**：判别法很简单——同一次失败在 `dmesg`/`logcat` 里**有对应 `avc: denied`** 才是 SELinux；没有就先查 UNIX 权限与路径。`[推断]`
6. **在宽松 domain 下"测试通过"**：`magisk`/`ksu` 的宽松掩盖了真实需求；迁移到任何更严格的 domain（或换 Root 方案）都会集体爆雷。**验收标准应该是"策略上说得清为什么能访问"，而不是"这台机器上恰好能跑"。** `[推断/待验证]`

---

## 7. 本项目的 sepolicy 需求清单（结论：先零规则，按 denial 增量补）

**阶段 0（默认假设）**：守护进程由模块脚本以 root 拉起，运行在 `magisk`/`ksu` domain → **不写任何 `sepolicy.rule`**，先跑通功能并录制 denial 基线。`[推断/待验证]`

**阶段 1（若出现 denial，按类型补充）**：

1. 文件访问类：`adb_data_file`（自己的目录）、`media_rw_data_file`（`/data/media/0`，仅在绕过 FUSE 时）。
2. 能力类：`self:capability net_bind_service`（445）。
3. 网络类：`node`/`netif`/`tcp_socket`（通常 root domain 已有）。
4. IPC 类：**优先改架构（HTTP over localhost）而不是写 `unix_stream_socket connectto` 规则**。

**明确不做的事**（写进规范）：

- 不 `setenforce 0`、不写 `/sys/fs/selinux/enforce`。
- 不使用 `magiskpolicy --magisk` 式全量放宽、不 `permissive` 整个 App/系统 domain。
- 不为了"少一条规则"把服务塞进 `untrusted_app`/`shell` 等错误 domain。
- 不假设"Magisk 能跑 = KernelSU 能跑"；两者策略注入机制不同（§3），必须双栈实测。

---

## 8. 为什么绝不建议关闭 SELinux

1. **不必要**：root 侧本来就有 `magisk`（permissive）/`ksu` 的宽松域；关全局 SELinux 换不到任何权限。`[已确认]`
2. **不可逆的信任损失**：Android 的 SELinux 是整机隔离模型（App 沙箱、`installd`/`netd`/`vold`/`zygote` 边界）的基础；关掉等于把"一个 App 被攻破"变成"整机被攻破"。`[已确认·AOSP 文档]`
3. **会破坏"最小惊讶"**：用户装一个 SMB 模块，不应该得到一台 SELinux 关闭的手机；这类副作用应当在模块说明里被明确拒绝。
4. **合规层面**：CDD 对 SELinux 有强制要求（本调研未能取到 CDD 原文，见 §9），因此"靠关 SELinux 实现的方案"在合规与上架审查上都有风险。`[推断/待验证]`

---

## 9. 证据不足 / 待上机验证

1. **`u:r:ksu:s0` 的实际宽松程度**：只确认了 label，未确认该 domain 在设备策略里是否有 `permissive` 或宽 `allow`。需要 `ls -Z`/`dmesg`/（若可得）`sesearch` 交叉验证。
2. **KernelSU 侧 `sepolicy.rule` 的加载细节**：文档给出"安装时加载、规则只增不减"，但 pre-init 路径下模块规则的加载时机与 Magisk 是否完全等同，未逐条取证。
3. **`/data/adb/modules/<id>` 下文件的实际标签**：`file_contexts` 只保证 `/data/adb(/.*)?` → `adb_data_file`，Magisk/KernelSU 安装器是否对模块文件重贴 `system_file`/`magisk_file` 需实测。
4. **FUSE 挂载的文件标签与权限**：`/storage/emulated/0` 下的文件在 `ls -Z` 里是否为 `media_rw_data_file`、FUSE 挂载参数是否影响访问，未在真机确认。
5. **445 端口 bind 是否真的需要额外策略**：取决于设备策略对 `node_type:node bind` 的处理，未实测。
6. **CDD 关于 SELinux 强制/不可关闭的原文**：仅见二手摘要，未打开 CDD 正文（`android-git.linaro.org` 未取），故 §8 第 4 点整体标待验证。
7. **`dmesg` 可读性**：不同 OEM 的 `kernel.dmesg_restrict` 默认值不同，未逐一验证。
8. **未做任何真机 SELinux 实验**：本文件所有"检查点"均来自策略源码/官方文档推导，**必须**在 OnePlus PJA110 上按 §6.3 跑一遍才算确认。

---

## 10. 来源

**AOSP 官方文档**

- SELinux 概览（default denial、Enforcing/Permissive、per-domain permissive、denial 去 dmesg+logcat、版本历史）：https://source.android.com/docs/security/features/selinux
- SELinux 实现指南：https://source.android.com/docs/security/features/selinux/implement
- Scoped storage / MediaProvider FUSE（Android 11+，用户空间拦截文件操作）：https://source.android.com/docs/core/storage/scoped

**AOSP 策略源码（android.googlesource.com，本次以 `?format=TEXT` 抓取并 base64 解码）**

- `system/sepolicy/private/file_contexts`（`/data/adb`、`/data/media/.*`、`/storage(/.*)`、`/dev/fuse`、`/mnt/media_rw` 的标签）：https://android.googlesource.com/platform/system/sepolicy/+/refs/heads/main/private/file_contexts
- `system/sepolicy/private/su.te`（`userdebug_or_eng`、`domain_auto_trans(shell, su_exec, su)`、`permissive su;` 及其注释）：https://android.googlesource.com/platform/system/sepolicy/+/refs/heads/main/private/su.te
- `system/sepolicy/private/adbd.te`（`/data/adb` 相关域）：https://android.googlesource.com/platform/system/sepolicy/+/refs/heads/main/private/adbd.te
- `system/sepolicy/public/app.te`（App domain 模板）：https://android.googlesource.com/platform/system/sepolicy/+/refs/heads/main/public/app.te

**Magisk（官方）**

- Details（`magisk` domain = unconstrained permissive、`magisk_file`、`/data/adb` 700、sepolicy.rule 持久化）：https://topjohnwu.github.io/Magisk/details.html
- Tools（`magiskpolicy`、`--live`、`magisk --restorecon`）：https://topjohnwu.github.io/Magisk/tools.html
- 安装文档（sepolicy.rule 存于 cache/metadata，勿擦除）：https://topjohnwu.github.io/Magisk/install.html
- 源码 `native/src/sepolicy/rules.rs`（`deny(... load_policy)`、`permissive([proc])`、`magisk_file` 全放行）：https://cdn.jsdelivr.net/gh/topjohnwu/Magisk@master/native/src/sepolicy/rules.rs

**KernelSU（官方）**

- 模块指南（目录树含 `sepolicy.rule`；`preinit/` 在策略加载前执行；hot install"rules are only ever added"；安装器设 `0644`）：https://kernelsu.org/guide/module.html
- 与 Magisk 的差异（模块系统冲突、`KSU` 环境变量）：https://kernelsu.org/guide/difference-with-magisk.html
- FAQ：https://kernelsu.org/guide/faq.html

**本仓库内交叉引用**

- `docs/research/magisk.md`（Magisk domain/`sepolicy.rule`/pre-init/规则只增不减的已确认结论）
- `docs/research/samba.md`（Samba 侧目录、445 端口、FUSE 性能与 Android 特性）
- `.work/fetched/aosp-su-te`、`aosp-fc-private`、`aosp-adbd-te`（本次解码后的 AOSP 源码文本）
