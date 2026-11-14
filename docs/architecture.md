# 架构说明

本文描述 `android-smb-relay` 0.1.0 的实际实现结构：进程、启动时序、数据流、状态机、watchdog、网络检测、存储映射、Samba 配置生成，以及模块升级/卸载时的数据保留策略。

> 说明：本文基于仓库源码（`src/asrd/*.go`、`scripts/*.sh`、`module.prop`、`customize.sh`、`service.sh`、`post-fs-data.sh`、`uninstall.sh`、`webui/*`）与一次真机验证记录编写。凡是“已验证”字样，仅指 OnePlus PJA110 / Android 16 / KernelSU 3.3.1 这一组合；其余行为是代码设计描述，不等于已在所有环境验证。

## 1. 设计目标与约束

| 目标 | 实现方式 |
|---|---|
| 不依赖 Android App / Termux | 模块自带 Samba 二进制与依赖库；WebUI 是模块自带的静态页面 |
| 不修改 `/system` | 不使用 `system/`、`replace`、`skip_mount`；Magisk 与 KernelSU 共用一套脚本 |
| 升级不丢状态 | 模块代码在 `/data/adb/modules/android-smb-relay`，运行状态在 `/data/adb/android-smb-relay` |
| 开机可用 | `post-fs-data.sh` 只做目录/权限；真正的启动在 `service.sh`（非阻塞） |
| 可控与可观测 | 单进程 supervisor + Unix 控制 socket + 结构化日志 + WebUI 状态页 |
| 可交叉编译 | `asrd` 只用 Go 标准库，`CGO_ENABLED=0`，无运行时依赖 |
| 默认模式 | 匿名整盘共享（`Internal` → `/storage/emulated/0`）、无 SMB 账号、SMB1 关闭、签名不强制、WebUI 认证保留（首次强制改密） |

明确的非目标：不提供 TLS、不做多用户 UNIX 隔离（SMB 统一 `force user = root`）、不做 HA/集群、不修改系统分区。

## 2. 组件与进程

```text
Magisk / KernelSU
  │
  ├─ post-fs-data.sh           阻塞阶段，极轻量
  ├─ service.sh                late_start，等待 boot_completed 后启动
  │    ├─ scripts/start.sh     启动 asrd serve，并等待控制 socket
  │    └─ scripts/watchdog.sh  外层 shell watchdog（常驻）
  │
  ├─ asrd serve                常驻 supervisor + WebUI + 控制 socket
  │    ├─ 配置加载/校验/原子保存
  │    ├─ 网络检测（5 s 轮询 + 去抖）
  │    ├─ 存储就绪检测、路径映射
  │    ├─ 状态机与 smbd 监督
  │    ├─ WebUI HTTP :8080
  │    └─ Unix socket run/asr.sock（0600，仅 uid 0）
  │
  ├─ smbd                      Samba 4.25.1，daemon 模式，TCP :445
  │    └─ 每客户端一个 smbd 子进程（max smbd processes = 64）
  │
  └─ 用户文件 /storage/emulated/0（Internal 共享整盘）
```

`asrd` 是唯一的长期业务进程；`smbd` 由它拉起并监控；`watchdog.sh` 是兜底，只在 `asrd` 整体死亡时工作。WebUI 不是独立进程，而是 `asrd` 内的 `net/http` 服务。

## 3. 启动时序

| 阶段 | 脚本/进程 | 行为 | 阻塞性 |
|---|---|---|---|
| `post-fs-data` | `post-fs-data.sh` | 建 `config/logs/run/samba/tmp`（0700）；`data/lib -> module/lib` 符号链接；配置不存在则复制默认配置（0600）；`boot.log` > 256 KB 时轮转 | 阻塞，保持极轻量 |
| `late_start service` | `service.sh` | 循环等待 `sys.boot_completed=1`，最多 120 s；执行 `start.sh --boot`；后台启动 `watchdog.sh` | 非阻塞 |
| 启动 daemon | `scripts/start.sh` | 清理 `run/watchdog.disabled`；检查 `run/asrd.pid` 是否仍存活；`service.out` > 1 MB 时轮转；`nohup asrd serve --module-dir --data-dir --webui --boot`；最多等 15 s 出现 `run/asr.sock` | 非阻塞（由 service.sh 调用） |
| 初始化 | `asrd` | 创建数据目录；写 `run/asrd.pid`；加载/校验配置；创建 logger；生成初始凭据；创建 `SambaManager`/会话存储/限速器/监控器；启动 WebUI、网络 watcher、`supervise` goroutine、控制 socket | — |
| 首次用户 | `asrd` | 若 `users` 为空且 SMB 启用：生成 `android` 用户（随机 20 位口令），写入 tdbsam 并追加到初始凭据文件 | — |
| 服务就绪 | `asrd` | `reconcile()` 等到网络与存储就绪后启动 `smbd`；WebUI 在启动时即监听 | — |
| 用户解锁 | Android/FUSE | `/storage/emulated/0` 出现后，`reconcile` 的下一次 10 s tick 或事件会重新评估；必要时重启 `smbd` | 由系统触发 |

关键点：

- `post-fs-data.sh` **不访问网络、不访问 `/storage`、不启动服务**，避免阻塞开机。
- `service.sh` 对 `sys.boot_completed` 最多等 120 s；即使超时也继续启动 daemon，由 daemon 自己的网络/存储门控决定何时启动 `smbd`。
- `start.sh` 使用 PID 文件防止重复启动；`asrd serve` 自身也会检查 PID。
- 手动执行 `start.sh` 时不带 `--boot`，此时即使 `start_webui_on_boot=false` 也会启动 WebUI（该开关只影响 boot 模式）。

## 4. 数据流

```text
SMB 客户端 ──TCP 445──► smbd ──VFS/FUSE──► /storage/emulated/0/SMB/...
                                    └────► /data/media/0/SMB/...（回退）

浏览器 ──HTTP 8080──► asrd
   ├─ 静态文件：module/webui/{index.html,css,js}
   ├─ 读 API：/api/status、/api/network、/api/system、/api/shares、/api/users、/api/logs、/api/settings
   ├─ 写 API：/api/shares、/api/users、/api/service/*、/api/settings、/api/auth/*
   └─ 认证：cookie `asr_session` + `X-CSRF-Token`

root CLI ──Unix socket──► asrd 控制服务
   ├─ GET /ctl/status、/ctl/network、/ctl/users、/ctl/config、/ctl/logs、/ctl/credentials
   └─ POST /ctl/{start,stop,restart,reload,passwd,webui-password}

asrd ──写 smb.conf + 进程控制──► smbd
asrd ──读 /proc、/sys、statfs──► 状态/指标
smbd ──写日志──► data/logs/smbd.log（+ smbd-console.log）
asrd ──写日志──► data/logs/asrd.log（轮转）
```

## 5. `asrd` 源码模块划分

| 文件 | 职责 |
|---|---|
| `main.go` | 命令入口（`serve`/`ctl`/`render`/`init`/`doctor`）、CLI 输出、doctor 自检 |
| `config.go` | `Config` 结构、默认值、`normalize`、`Validate`、原子读写、共享/用户查找 |
| `daemon.go` | `Daemon` 生命周期、`supervise`/`reconcile` 状态机、重启退避、`Status`、警告、配置更新、初始用户 |
| `samba.go` | `smb.conf` 渲染、`smbd` 启停/监控/重载、`smbpasswd` 用户管理、进程扫描 |
| `network.go` | 接口枚举/分类/排序/选择、路由/DNS、私网判定、`NetworkWatcher` 去抖 |
| `paths.go` | 受保护路径、允许根、路径校验、`/data/media/0` 映射、FUSE 检测、共享目录创建 |
| `auth.go` | PBKDF2 口令哈希、secrets 文件、初始凭据、会话、登录限速 |
| `api.go` | WebUI HTTP handler、中间件（安全头/LAN/认证/CSRF）、全部 REST 路由、JSON 编解码 |
| `webui.go` | WebUI 监听器生命周期、URL 生成 |
| `control.go` | Unix socket 控制服务（仅 uid 0）、控制客户端、CLI 使用 |
| `sysinfo.go` | CPU/内存/uptime/存储/吞吐/连接数采样 |
| `logging.go` | 轮转 writer、分级 logger、脱敏、日志尾部读取 |
| `platform_linux.go` | Linux/Android 的进程组、信号、存活检测、SO_PEERCRED |
| `platform_other.go` | 其他平台的最小实现（便于本地开发/测试编译） |

## 6. 服务状态机

`ServiceStatus.State` 的取值与含义：

| 状态 | 含义 |
|---|---|
| `stopped` | 未运行（用户停止、SMB 禁用、`start_on_boot=false` 且未手动启动） |
| `starting` | 正在写配置/启动 `smbd` |
| `running` | `smbd` 存活 |
| `waiting_network` | 没有可用的 LAN IPv4 接口 |
| `waiting_storage` | 存储（共享根）不可用 |
| `restarting` | `smbd` 异常退出后在退避等待 |
| `untrusted_network` | 当前 Wi-Fi 不在 `network.trusted_ssids` 且策略为 `stop`；换回可信网络后自动恢复 |
| `failed` | 达到重启上限或被禁用 watchdog 后放弃；不会自动重试。需要用户 Start/Restart/Reload（`Reload` 在未运行时等价 `Start`）或先恢复运行状态再改配置来清除。 |

```text
                    ┌──────────────┐
      初始/停止 ────►│   stopped    │
                    └──────┬───────┘
                           │ reconcile：SMB 启用、非用户停止
                           ▼
                    ┌──────────────┐
                    │  starting    │
                    └──────┬───────┘
                 成功      │      失败
              ┌────────────┴──────────┐
              ▼                       ▼
       ┌──────────────┐        ┌──────────────┐
       │   running    │        │  restarting  │
       └──────┬───────┘        └──────┬───────┘
              │ 崩溃/网络变化/存储映射变化 │ 退避结束
              └────────────┬────────────┘
                           ▼
                    失败次数达上限
                           │
                           ▼
                    ┌──────────────┐
                    │    failed    │   （需 Start/Reload/改配置触发）
                    └──────────────┘

  网络未就绪：任意状态 → waiting_network（并停止 smbd）
  存储未就绪：任意状态 → waiting_storage（并停止 smbd，若 wait_for_storage=true）
```

`reconcile()` 每次（10 s tick、网络事件、控制操作、启动/退出事件）执行：

1. `detectNetwork(cfg)` 更新快照。
2. 若 `!smb.enabled` 或 `userStopped`：停止 `smbd`，状态 `stopped`。
3. 若 `wait_for_network && !ready`：停止 `smbd`，状态 `waiting_network`。
4. 若 `!ready`：同样停止并进入 `waiting_network`（**没有 LAN IPv4 时始终不会启动**，该开关只影响分支文案）。
5. 若 `wait_for_storage && !storageUsable(root)`：停止 `smbd`，状态 `waiting_storage`。
6. `ensureShareTree`（若 `auto_create`）+ `EnsureSharePaths`。
7. 若 `smbd` 正在运行，但选中接口/IP 或存储映射指纹变化：停止并重新启动。
8. 若状态为 `failed`：不自动重试。
9. 否则 `startSMB()`。

`lastIface`/`lastIP`/`lastStorage` 在启动成功时记录；`StorageFingerprint()` 由所有启用共享的 `RuntimePath` 拼接而成，用于检测“解锁前 `/data/media/0`、解锁后 `/storage/emulated/0`”的映射切换。

## 7. 双层 watchdog

### 7.1 内层：`asrd` 监督 `smbd`

- `smbd` 以守护模式启动：`smbd -D -s <data>/samba/smb.conf`。
  - 选择 `-D` 而不是 `-F` 的原因：Termux/Android 上 `-F` 在前台模式下遇到第一个连接时可能因地址解析问题退出；daemon 模式 fork 出的 worker 能正常处理连接（源码注释记录）。
  - **不传 `-l`**：Samba 把 `-l` 当 log basename，会把日志写成 `<dir>/log.smbd` 并忽略 `log file`；不传时 smb.conf 里的 `log file = <data>/logs/smbd.log` 生效，WebUI 才能读到。
- 启动后最多 3 s 等待 `smbd.pid` 出现且进程存活；失败则扫描 `/proc/*/cmdline`（包含 `smbd` 与本配置文件路径）作为兜底。
- 每秒检查一次 master PID；`Wait()` 返回后向 `exitEvents` 发送退出事件。
- 非预期退出：记日志、清理残留、记录一次 restart、按退避重启：

  ```text
  delay = min(3 * 2^(attempt-1), 60) 秒
  attempt: 1→3s, 2→6s, 3→12s, 4→24s, 5→48s, ≥6→60s
  ```

- 默认 `watchdog_max_restarts=5`、`watchdog_window_minutes=10`；窗口内达到上限进入 `failed`。
- 进程稳定运行满一个窗口（默认 10 分钟）后，重启计数清零。
- 用户 `Stop`/`Restart` 会设置 `expectExit`，从而区分“计划内停止”和“崩溃”。
- 已验证：真机上 `kill smbd` 后 watchdog 自动重启（`restarts=1`）；把上限调到 2 后连续 kill 会进入 `failed`，`smbctl start` 可恢复。

### 7.2 外层：`scripts/watchdog.sh` 监督 `asrd`

- 由 `service.sh` 通过 `nohup ... &` 启动，日志 `data/logs/watchdog.log`。
- 单实例锁：`run/watchdog.lock/` 目录 + `pid` 文件；旧锁的 PID 已死则恢复。
- 每 30 s 检查 `run/asrd.pid`；若进程不在：
  - 记录崩溃时间到 `run/watchdog.crashes`，仅保留最近 600 s；
  - 10 分钟内 ≥5 次则放弃（`exit 1`）；
  - 否则 `start.sh --boot` 重新拉起，再等 10 s。
- `stop.sh`/`uninstall.sh` 会先创建 `run/watchdog.disabled`，watchdog 看到后退出；`start.sh` 会清除该标记。
- 外层 watchdog 不监控 `smbd`；`smbd` 由内层 `asrd` 负责。

### 7.3 失败边界

- `smbd` 连续失败 → `failed`，WebUI 显示错误与“已达重启上限”，需手动 Start/Restart 或改配置。
- `asrd` 连续失败 → shell watchdog 放弃，需重启手机或手动启动。
- 两者都不会无限重启（避免耗电与日志风暴）。

## 8. 网络检测

### 8.1 枚举与分类

- 通过 Go `net.Interfaces()` 枚举接口、地址、MAC；路由/默认路由解析 `/proc/net/route`；DNS 读 `/etc/resolv.conf`，失败时读 `getprop net.dns1/net.dns2`。
- 接口分类基于名称前缀 + sysfs 兜底：

| 类型 | 判定 |
|---|---|
| `loopback` | `lo` |
| `wifi` | `wlan*`、`swlan*`、`ap*`、`wifi*`，或 `/sys/class/net/<if>/wireless` 存在 |
| `cellular` | `rmnet*`、`ccmni*`、`pdp*` |
| `vpn` | `tun*`、`tap*`、`ppp*`、`wg*`、`vgate*`、`v4-*`、`clat*` |
| `usb` | `usb*`、`rndis*`，或 sysfs 链接包含 `usb` |
| `ethernet` | `eth*` |
| `other` | 其余 |

- 排序权重：wifi 10、ethernet 20、usb 30、vpn 40、other 50、cellular 60、loopback 70；down 或无 IPv4 的接口排到最后。

### 8.2 选择规则

- 显式配置 `network.interface=<name>`：接口必须存在、up、有 IPv4，否则返回错误文案并不启动。
- `auto`：
  - 排除 loopback、down、无 IPv4 的接口；
  - 排除 `cellular`（除非 `allow_cellular=true`）；
  - **永远不自动选择 VPN**（`tun*`/`wg*` 等），只能显式指定；
  - 要求 IPv4 不是 loopback/link-local；
  - 有默认路由的优先，其次按类型权重。
- `Ready` 的定义：选中接口存在且有 IPv4。默认路由只参与排序，不作为“可用”的前置条件。

### 8.3 变化检测与去抖

- `NetworkWatcher` 每 5 s 采样一次，指纹 = `selected|ipv4|ipv6列表`。
- 指纹变化后启动去抖定时器（`network.settle_seconds`，默认 5 s）；期间再次变化会重置定时器。
- 去抖结束后向 `Events` 发送事件（容量 1，丢弃重复），`supervise` 收到后执行 `reconcile`。
- 另外每 10 s 无条件 `reconcile` 一次作为兜底。
- `reconcile` 检测到选中接口或 IPv4 变化时重启 `smbd`：代码不假设 Samba 会自动跟随接口地址变化（Samba 在启动时解析 `interfaces` 并建监听；是否自动重建未在本项目单独验证）。
- 注意：去抖参数在 daemon 启动时读取并保存在 watcher 中，**运行时修改 `network.settle_seconds` 需要重启 `asrd` 才生效**。

### 8.4 可信 Wi-Fi 白名单（`network.trusted_ssids`）

- 目的：默认共享是「匿名 + 整盘可写」，出门连上陌生 Wi-Fi 时会直接暴露 `/storage/emulated/0`。
- 配置：`trusted_ssids`（SSID 精确匹配、区分大小写、最多 32 条）+ `untrusted_action`（`readonly` 默认 / `stop` / `allow`）。
- SSID 读取：`cmd wifi status` 的 `Wifi is connected to "X"` 或 `WifiInfo: SSID: "X"`，由 root 身份执行，结果缓存 10 s，只对选中接口为 `wifi` 时读取。
- 判定顺序：白名单为空 → 不启用；非 Wi-Fi（以太网/USB）→ 允许并注明跳过；读不到 SSID → 允许（fail-open）并写入 warnings；SSID 不在名单 → 按 `untrusted_action` 处理。
- `readonly` 的实现是不改存储的配置克隆：`effectiveConfig()` 把启用共享全部置为 `read_only = yes` 再渲染 `smb.conf`，所以离开不可信网络会恢复读写。
- 策略变化**重启** `smbd` 而不是 SIGHUP：Samba 的共享参数在客户端连接时读取，重载不会影响已建立会话。
- SSID 变化会进入 `NetworkWatcher` 的指纹（`selected|ipv4|ipv6|ssid|policy`），因此切换网络会自动触发 `reconcile`。

### 8.5 WebUI 的 LAN-only

- `lanGuard` 对**所有**路径（静态页也包含）检查 TCP 对端地址；`webui.lan_only=true` 时只允许私有地址：
  - IPv4：`10/8`、`172.16/12`、`192.168/16`、loopback、link-local；
  - IPv6：ULA `fc00::/7`、loopback、link-local。
- 不做 `X-Forwarded-For` 解析（头可伪造）；反向代理场景下看到的对端是代理地址。

## 9. 存储就绪与路径映射

### 9.1 FUSE 检测

- Android 11+ 的 `/storage/emulated/0` 是 FUSE 视图；用户解锁前可能不存在或不可用。
- `isFuseMount(path)` 用 `statfs` 的 `f_type == 0x65735546`（FUSE super magic）判断。
- `storageUsable(path)`：
  - 若路径在 `/storage/emulated/0` 下，必须先通过 FUSE 检测（避免把空挂载点当成可用存储）；
  - 必须是目录且可 `open` + `Readdirnames(1)`（空目录也允许）。
- `wait_for_storage=true` 时，不可用则进入 `waiting_storage`，不启动 `smbd`。

### 9.2 `/data/media/0` 回退

- 配置 `storage.use_data_media=true`（默认）时，`RuntimePath()` 对 `/storage/emulated/0` 及其子路径做映射：
  - 如果 `/storage/emulated/0` 可用（FUSE 已挂载），原样使用；
  - 否则尝试 `/data/media/0/<剩余路径>`；两者都不可用则仍返回配置路径。
- 映射结果会写入 `smb.conf` 的共享 `path`，所以同一份配置在解锁前后可能指向不同真实路径。
- `StorageFingerprint()` 记录映射结果；变化时自动重启 `smbd`，避免旧进程继续使用失效路径。
- 注意：直接走 `/data/media/0` 会绕过 MediaProvider 的策略与媒体库索引；这属于回退行为，不是默认首选。

### 9.3 共享目录创建

- `ensureShareTree`：默认 `auto_create=false`；只有显式开启时才创建 `storage.root` 及默认子目录。默认共享 `Internal` 直接指向 `/storage/emulated/0`。
- `EnsureSharePaths`：为每个启用的共享创建缺失目录（不删除任何东西）。
- 启动后 `Status` 会检查每个共享路径是否存在（`exists` 字段）。

## 10. Samba 配置生成

`SambaManager` 永远从 `Config` **重新渲染** `smb.conf`，不允许手改（文件头写明）。渲染输入：配置 + 当前选中接口 + 数据目录；输出：

- `[global]`：
  - 身份：`server role = standalone server`、`security = user`、`passdb backend = tdbsam`、`ntlm auth = ntlmv2-only`；
  - 匿名：默认 `restrict anonymous = 0`、`map to guest = Bad User`、`guest account = nobody`；关闭 Guest/匿名后才使用 `Never` / `restrict anonymous = 2`；
  - 协议：`server min protocol`、`server max protocol`、`server signing`、`server smb encrypt`、`server multi channel support = no`；
  - 端口：`smb ports = <port>`；
  - 接口：仅当选中接口不是 `auto` 时写 `interfaces = <iface>` + `bind interfaces only = yes`；
  - 路径：`private dir`、`lock directory`、`state directory`、`cache directory`、`pid directory`、`ncalrpc dir`、`log file` 全部指向 `/data/adb/android-smb-relay/...`；
  - 日志：`max log size`、`log level`（`debug→4`、`info→1`、`warn/error→0`）、时间戳/PID；
  - 兼容/安全：`load printers = no`、`disable spoolss = yes`、`dns proxy = no`、`name resolve order = host`、`hostname lookups = no`、`unix extensions = no`、`wide links = no`、`follow symlinks = no`、`kernel oplocks = no`；
  - 性能/语义：`use sendfile = no`、`use mmap = no`、`aio read/write size = 0`、`smb2 max read/write = 1048576`、`deadtime = 15`、`keepalive = 300`、`max smbd processes = 64`；
  - 权限/属性：`nt acl support = no`、`store dos attributes = no`、`map archive/hidden/system = no`、`ea support = no`、`create mask = 0666`、`directory mask = 0777`、`force create/directory mode = 0000`、`dos filemode = no`、`hide dot files = no`、`delete readonly = no`。
- 每个启用的共享：`[Name]`、`path`（`RuntimePath`）、`comment`、`browseable`、`read only`、`guest ok`、`valid users`（仅非 guest 共享；所有启用用户；无用户时写 `nobody`）、`force user`（默认 `root`，此时同时 `force group = root`）、`inherit permissions = no`、`writeable`。
- 文件以 `0600` 原子写入 `data/samba/smb.conf`。

配置变更策略：

| 变更类型 | 处理 |
|---|---|
| 共享/用户增删改 | 写配置 → `SIGHUP` 重载；失败则重启 |
| `smb.*` 关键字段、`network.interface`、`allow_cellular`、`storage.root`、`use_data_media` | 重启 `smbd` |
| WebUI 字段 | 立即重建 HTTP 监听（不重启 SMB） |
| 其他（如日志） | 写配置并尝试重载 |

> `smb.interface` 会触发重启判断，但实际绑定接口来自网络检测（`network.interface`）；这是 0.1.0 的已知不一致。

## 11. `smbd` 生命周期

| 操作 | 实现 |
|---|---|
| 启动 | 写配置 → 删除旧 pid → 打开 `smbd-console.log` 轮转 writer → `exec.Command(bin/smbd, "-D", "-s", conf)`，设置 `SysProcAttr{Setpgid:true}`、工作目录=数据目录、受限环境变量 → `cmd.Run()`（daemon 立即返回）→ 等 pid 文件/扫描 `/proc` |
| 环境 | `PATH=<module>/bin:/system/bin:/system/xbin`；`LD_LIBRARY_PATH=<module>/lib:<module>/lib/samba`；`TMPDIR=<data>/tmp`；`HOME=<data>`；`ANDROID_DATA=/data`；`ANDROID_ROOT=/system`；`LANG=C.UTF-8` |
| 监控 | 1 s 轮询 master PID；退出后发送事件 |
| 重载 | `SIGHUP`（Samba 会重新读配置，不丢弃已建立会话） |
| 停止 | `SIGTERM` → 等最多 8 s → `SIGKILL` → 对启动前记录的 child PID 再补 `SIGKILL` → 关日志 writer → 删 pid 文件 |
| 用户管理 | `smbpasswd -s -a <name>`（新增）、`smbpasswd -s <name>`（改密）、`smbpasswd -x/-e/-d`（删除/启用/禁用），口令通过 stdin 传两行，绝不进入 argv；超时 20 s |
| 配置自检 | `testparm -s -c <conf>`（`CheckConfig`；当前代码定义了但未在启动/更新流程中调用） |

进程模型：`smbd` 是“每客户端一个子进程”的模型；`max smbd processes = 64` 限制并发。`Stop` 会清理 master 与启动时捕获的 children。

## 12. WebUI / HTTP 层

除浏览器的 `http://<ip>:8080` 外，模块还提供 KernelSU 模块页入口：`webroot/index.html`（KernelSU 用 `WebViewAssetLoader` 加载，并注入 `ksu.exec()` root shell）。该页面本身不是完整前端，只通过 `ksu.exec` 判断 `run/asr.sock` 是否存在、`/proc/net/tcp{,6}` 是否监听 `1F90`（8080），据此显示“启动服务/启动 WebUI”按钮；服务正常时 `location.replace` 到 `http://127.0.0.1:8080/`，在同一个 WebView 里加载真正的 WebUI（同源，cookie/CSRF 与浏览器访问完全一致）。

- `WebUIServer` 由 `Daemon` 持有；`Apply(cfg)` 在配置未变化时直接返回（避免对同一 bind/port 重复 `net.Listen`）；需要重建时先尝试新监听，失败则记录 `lastErr` 并返回错误（旧 listener 保持运行）；成功后切换并关闭旧 listener。
- `Daemon.StartWebUI()` / `StopWebUI()` 可在不影响 `smbd` 的情况下启停 HTTP 监听（`ctl webui-start|webui-stop`）。
- `http.Server` 超时：读头 10 s、读 30 s、写 60 s、空闲 120 s，最大头 64 KB。
- 中间件链：

  ```text
  securityHeaders → lanGuard → mux
      └─ 每个路由：authed( ... )，写路由再包 csrf(...)
  ```

  - `securityHeaders`：`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、`Referrer-Policy: no-referrer`、`Permissions-Policy`、严格 CSP（`default-src 'self'`、`script-src 'self'`、`connect-src 'self'`、`frame-ancestors 'none'`、`base-uri 'none'`、`form-action 'self'`）。
  - `authed`：`webui.auth=false` 时直接放行；否则要求有效会话 cookie。
  - `csrf`：同样在 auth 关闭时放行；否则要求 `X-CSRF-Token == session.CSRF`，并在浏览器带 `Origin` 时校验同源。
- 路由使用 Go 1.22 的 `"METHOD /path/{param}"` 模式；未知 `/api/*` 返回 JSON 404；静态文件仅允许 `.html/.css/.js/.svg/.png/.ico/.woff2`，`pathWithin` 防止目录穿越，无目录列表。
- JSON 解码限制：`MaxBytesReader`（4 KB/8 KB/16 KB/64 KB 视端点）、`DisallowUnknownFields`、拒绝尾随 JSON、Content-Type 必须为 JSON（若提供）。
- 登录：校验用户名 `admin` + 口令；失败固定延迟 300 ms 并计入限速；成功后轮换旧会话，下发 cookie（`HttpOnly`、`SameSite=Strict`、`Path=/`，无 `Secure`）。
- 修改口令：校验新口令策略；成功后删除 `initial-credentials.txt`、注销所有会话。
- 控制类端点会调用 `Daemon` 的 `StartSMB/StopSMB/RestartSMB/ReloadSMB`；设置端点调用 `UpdateConfig`。

## 13. 控制 socket 与 CLI

- 路径：`/data/adb/android-smb-relay/run/asr.sock`，`chmod 0600`；HTTP server 监听 Unix socket。
- 鉴权：`SO_PEERCRED` 读取对端 uid，**仅 uid 0 允许**；非 root 返回 403。控制接口不需要 WebUI 认证/CSRF（本地 root socket 即信任边界）。
- 端点：

  | 方法 | 路径 | 说明 |
  |---|---|---|
  | GET | `/ctl/status` | 完整状态 |
  | GET | `/ctl/network` | 网络信息 |
  | GET | `/ctl/users` | 用户列表 |
  | GET | `/ctl/credentials` | 初始凭据文件内容（root-only） |
  | GET | `/ctl/config` | 配置路径与内容 |
  | GET | `/ctl/logs` | 日志尾部 |
  | POST | `/ctl/start` / `/ctl/stop` / `/ctl/restart` / `/ctl/reload` | 服务控制 |
  | POST | `/ctl/passwd` | 修改 SMB 口令（需用户存在） |
  | POST | `/ctl/webui-password` | 修改 WebUI 口令（无旧口令，依赖 root socket） |
  | POST | `/ctl/webui-start` / `/ctl/webui-stop` | 只启停 WebUI 监听，不影响 `smbd` |

- `smbctl` 是 shell 包装：拒绝非 root；socket 不存在时尝试 `scripts/start.sh`；然后 `exec asrd ctl "$@"`。
- `asrd ctl` 客户端通过自定义 `http.Transport` 的 `DialContext` 连接 Unix socket，超时 30 s。
- `asrd` 顶层还提供 `render`（打印渲染后的 smb.conf）、`init`（初始化数据目录/配置/凭据）、`doctor`（自检）。

## 14. 配置模型与更新

- `Config` 在内存中以指针 + `sync.RWMutex` 保护；读取返回克隆（`clone()` 通过 JSON 往返）。
- `loadConfig`：读文件 → `Unmarshal` → `normalize`（补默认值）→ `Validate`；任何错误都使 daemon 启动失败（不会静默使用默认值，除非文件不存在）。
- `saveConfig`：`normalize` → `Validate` → `MarshalIndent` → `atomicWriteFile`（同目录临时文件、`Chmod(0600)`、`Sync`、`Rename`、目录 `Sync`）。
- `UpdateConfig(mutate)`：
  1. 加 `opMu` 互斥；
  2. 克隆旧配置 → `mutate` → `normalize` → `Validate`；
  3. 写盘成功后才替换内存配置；
  4. `samba.cfg` 更新；仅当 `webui.enabled`/`webui.port`/`webui.bind` 变化时调用 `web.Apply`，失败会返回给 API（设置已保存但监听未切换）；
  5. 根据 `smbNeedsRestart` 选择重启 `smbd` / 写配置并 `SIGHUP` / 触发 `reconcile`。
- WebUI 的 `PUT /api/settings` 是**整段替换**（`smb`/`webui`/`network`/`startup`/`storage`/`log` 任一提供则整体替换该段），UI 会提交完整段。
- WebUI 的 `auth`/`lan_only`/`session_hours` 不触发监听重建：`auth`/`lan_only` 在请求处理时读取当前配置；`session_hours` 在 `SessionStore` 创建时确定，修改后需重启 daemon 才影响新会话。
- 校验覆盖所有外部可控字段（端口、协议、签名、加密、NetBIOS 名、workgroup、接口名、force user、bind、session、watchdog、storage、日志、共享、用户）。
- 日志级别在 logger 创建时确定，运行时修改 `log.level` 不会重建 logger，需重启 daemon 生效（已知限制）。

## 15. 数据目录布局

```text
/data/adb/modules/android-smb-relay/       # 模块目录（升级时整体替换）
├── module.prop
├── customize.sh / post-fs-data.sh / service.sh / uninstall.sh / action.sh
├── bin/        asrd, smbctl, smbd, smbpasswd, smbcontrol, testparm
├── lib/        Samba 依赖库（117 个根库 + lib/samba 下 153 个插件）
├── libexec/    samba-dcerpcd 与 rpcd_* 命名管道服务（IPC$ 必需）
├── webroot/    KernelSU 模块页入口（index.html）
├── scripts/    start.sh stop.sh restart.sh status.sh network.sh watchdog.sh
├── config/     default-config.json
└── webui/      index.html css/ js/

/data/adb/android-smb-relay/               # 持久数据（升级保留）
├── config/
│   ├── config.json               0600
│   └── initial-credentials.txt   0600（修改 WebUI 口令后删除）
├── secrets.json                  0600（PBKDF2 口令哈希）
├── logs/
│   ├── asrd.log                  轮转（默认 512 KB × 3）
│   ├── smbd.log
│   ├── smbd-console.log
│   ├── boot.log (+ boot.log.1)
│   ├── service.out (+ service.out.1)
│   └── watchdog.log
├── run/
│   ├── asrd.pid                 0600
│   ├── asr.sock                 0600
│   ├── watchdog.lock/pid
│   ├── watchdog.crashes
│   └── watchdog.disabled        （停止/卸载时存在）
├── samba/
│   ├── smb.conf                 0600
│   ├── private/                 0700（passdb.tdb、secrets.tdb）
│   ├── lock/ state/ cache/ pid/ ncalrpc/
│   └── ...
├── tmp/
├── var/log/samba/
├── lib -> /data/adb/modules/android-smb-relay/lib          # 稳定符号链接
└── libexec -> /data/adb/modules/android-smb-relay/libexec  # 稳定符号链接
```

- 模块文件由安装器/`customize.sh` 设为 `0755`/`0644`；数据目录 `0700`；配置与凭据 `0600`。
- `data/lib` 与 `data/libexec` 两个符号链接分别对应 Samba 编译期的 `MODULESDIR` 和 `LIBEXECDIR`（都被 patch 为 `/data/adb/android-smb-relay`）；升级替换模块目录后会重新指向新目录。

## 16. 模块升级与卸载

### 16.1 升级

1. Magisk/KernelSU 把新 zip 安装到暂存区（Magisk `/data/adb/modules_update`；KernelSU 默认同样 staged）。
2. 下一次开机合并/替换 `/data/adb/modules/android-smb-relay`。
3. 升级**不会执行 `uninstall.sh`**。
4. 开机后 `service.sh` 用新代码启动 `asrd`；持久数据目录不变，配置/用户/日志保留。
5. 旧 PID/socket 在重启后已失效；`start.sh` 会覆盖 `asrd.pid`，控制 socket 会重建。
6. KernelSU 3.3.1 无 hot install，因此升级必须重启。

### 16.2 卸载

1. `ksud module uninstall android-smb-relay` 只创建 `remove` 标记；Magisk 同理（App 移除）。
2. 下一次开机 `post-fs-data` 阶段执行模块 `uninstall.sh`（KernelSU 在 `prune_modules()`；Magisk 语义类似）：
   - 创建 `watchdog.disabled`；
   - 若还能读到 PID，尝试 `TERM`/`KILL` `asrd` 与 `smbd`（重启后通常已不存在）；
   - 删除 `run/asr.sock`、`run/asrd.pid`、`run/watchdog.disabled`；
   - 删除 `samba/{pid,lock,cache,ncalrpc}`、`tmp`、`data/lib` 符号链接；
   - **保留** `config/`、`logs/`、`secrets.json`、初始凭据文件。
3. 模块目录随后被删除。
4. 用户文件 `/storage/emulated/0/SMB/**` 永不被删除。

> 卸载/重装流程尚未在真机上完整验证；以上是脚本当前行为。

### 16.3 数据保留边界

| 数据 | 位置 | 升级 | 卸载 |
|---|---|---|---|
| 模块代码/二进制/WebUI | 模块目录 | 替换 | 删除 |
| 配置/用户/日志/凭据哈希 | `/data/adb/android-smb-relay` | 保留 | 保留（需手动 `rm -rf` 彻底清理） |
| 用户文件 | `/storage/emulated/0/SMB` | 保留 | **永不删除** |

## 17. 日志与指标

- 日志格式：`2026-11-14T22:00:00+08:00 [INFO] component: message`。
- `RotatingWriter` 按大小轮转（`max_size_kb`、`max_files`），写前检查，`path.1` 最新；不会原地截断导致半行。
- 脱敏：`redactSecrets` 对 `password/passwd/secret/token` 后跟 `=` 或 `:` 的键值做星号替换（兜底，调用方本就不应记录口令）。
- 日志读取：`/api/logs` 与 `/ctl/logs` 支持 `source=asrd|smbd|console`、`level`、`limit`；只读文件尾部最多 512 KB，limit 超过 1000 时回落到 200。
- `Status` 采集：CPU `/proc/stat`、内存 `/proc/meminfo`、uptime `/proc/uptime`、存储 `statfs`、吞吐 `/proc/net/dev`（RX 记为 upload，TX 记为 download）、SMB 连接数 `/proc/net/tcp{,6}` 的 ESTABLISHED、`smbd` 进程数 `/proc/*/comm`。
- `warnings` 会在以下情况提示：SMB 禁用、手动停止、guest/anonymous 开启（默认会提示）、WebUI 认证关闭、全接口监听且关闭 LAN-only、无可用 LAN、SMB 加密关闭。

## 18. 并发与故障处理

- `Daemon.mu` 保护运行快照（cfg、smb、web、userStopped、expectExit、restarts 等）；`samba` 自身字段由 supervisor 单 goroutine + `opMu` 串行化。
- `reconcile` 由单一 `supervise` goroutine 执行；控制 API 的 Start/Stop/Restart 通过 `opMu` 与状态机串行。
- `exitEvents` 容量 4，避免 `smbd` 退出事件阻塞。
- 网络 watcher 与 WebUI 各自独立 goroutine；`Stop()` 通过 `stopCh` 关闭。
- 失败处理：启动失败与运行中崩溃走同一 restart 计数；达上限进入 `failed`；`failed` 不会被 `reconcile` 自动拉起（避免死循环）。`StartSMB`/`RestartSMB` 会清空计数；未运行时 `ReloadSMB` 等价于 `StartSMB`。配置更新是否清除计数取决于是否走到重启路径，不保证一定清除。
- 配置损坏/缺失：缺失时写默认配置；损坏时 daemon 启动失败并记日志，不会静默重置。
- 凭据损坏：`secrets.json` 解析失败会报错退出，不会自动生成新口令（避免静默锁死/重置）。

## 19. 设计取舍与已知限制

1. **单 supervisor、无外部依赖**：牺牲了细粒度插件化，换取交叉编译与部署简单。
2. **`smbd -D` + PID 监督**：Samba daemon 化后无法直接用 `cmd.Wait()` 等待 master，所以每秒轮询 PID；进程 PID 复用理论上有误判窗口，但 pid 文件 + 命令行扫描降低了风险。
3. **`force user = root`**：Android 没有 `useradd` 语义，Samba 多用户映射到不同 UNIX 用户不可靠；代价是 SMB 侧没有文件级用户隔离。
4. **`/storage` 与 `/data/media/0` 双视图**：回退可解决解锁前的可用性，但会绕过 MediaProvider，并可能造成媒体库不一致。
5. **`wait_for_network` 语义**：没有可用 LAN IPv4 时始终等待；该开关目前只影响分支文案，不改变“是否要求网络”。
6. **`smb.interface` 与 `network.interface` 并存**：实际生效的是后者；前者仅显示/触发重启，属于 0.1.0 的接口遗留。
7. **无 TLS**：WebUI 是局域网管理面，默认 LAN-only；公网暴露需要额外的反向代理 + TLS。
8. **日志级别、网络去抖参数、WebUI session TTL 需重启 daemon 生效**（在启动时读取/创建）。
9. **WebUI 静态文件与 session 都在内存/模块目录**：daemon 重启会丢失会话（需重新登录），但配置与用户持久化。
10. **模块目录写入**：`SambaManager.ensureDirs` 会创建 `<module>/var/log/samba`，属于实现细节；升级替换模块目录时该目录会被丢弃，不影响功能。

## 20. 开发记录：已修复的严重问题

以下问题在 0.1.0 的真机测试阶段被发现并修复，记录在此以避免回归：

1. **`smbd` monitor 与 Stop 并发导致 `asrd` panic**
   - 现象：停止/重启 `smbd` 时，`Daemon` 的 `Wait()` 与 monitor goroutine 可能同时关闭/等待同一个 `waitDone` channel；对已关闭或为 nil 的 channel 执行 close 会 panic，导致 supervisor 崩溃。
   - 修复：monitor 改为接收一个**本次启动创建**的局部 `done` channel（`monitor(pid, done)`），`SambaManager` 在启动时保存它；Stop 只等待该 channel，不再从共享字段重建/关闭。
   - 验证：真机反复 stop/restart/kill 不再触发 panic。

2. **Wi-Fi 关闭时误选 VPN/隧道接口**
   - 现象：Wi-Fi 关闭后，设备上的 `vgate0` 等隧道接口仍处于 up 且有私网地址，自动选择逻辑可能把它当成可用 LAN 接口，导致 SMB 绑定到错误的接口。
   - 修复：接口分类把 `vgate*`、`v4-*`、`clat*` 归为 `vpn`；`auto` 选择**永不**选中 `vpn` 类型（只能通过显式 `network.interface` 指定）。
   - 验证：关闭 Wi-Fi 后 SMB 进入 `waiting_network`；重新开启并拿到 LAN 地址后自动恢复。

3. **WebUI 对相同 bind/port 重复监听**
   - 现象：早期版本每次保存配置都调用 `web.Apply`，即使 WebUI 的 bind/port 未变化，也会对同一地址 `net.Listen` 失败并记录 `address already in use`。
   - 修复：`WebUIServer.Apply` 对相同配置直接 no-op；`UpdateConfig` 仅在 `enabled`/`port`/`bind` 变化时调用 `Apply`；失败会把错误返回给 API（设置已保存但监听未切换）。
   - 说明：该问题属于 0.1.0 早期测试版行为，最终构建已修复。

4. **构建 staging 的 `lib/lib/` 嵌套**
   - 现象：`build.sh` 先创建 `$STAGE/lib`，再用 `cp -a "$RELEASE_BUNDLE/lib" "$STAGE/lib"`，导致库被放到 `lib/lib/`，运行时 `smbd` 找不到 `libsamba-*` 与 `lib/samba/pdb/tdbsam.so`。
   - 修复：先 `rm -rf "$STAGE/lib"`、再 `mkdir -p "$STAGE/lib"`、再用 `cp -a "$RELEASE_BUNDLE/lib/." "$STAGE/lib/"`；发布前用 `unzip -l ... | grep lib/lib/` 做阻断校验（见 [build.md](build.md) §4.1）。

5. **`/storage` 未解锁时误把空挂载点当可用存储**
   - 现象：用户解锁前 `/storage/emulated/0` 可能已存在但只是空挂载点；直接 `stat`/列目录会判断“可用”，导致 Samba 在错误路径上启动或共享为空。
   - 修复：`isFuseMount()` 用 `statfs` 的 `f_type == FUSE super magic (0x65735546)` 判断 FUSE 是否真正挂载；不可用时 `RuntimePath()` 回退到 `/data/media/0`，解锁后再由 `StorageFingerprint` 变化触发 `smbd` 重启。

6. **打包漏掉 `libexec/samba/`，安卓客户端挂载报 `0xC000020C`**
   - 现象：MT 管理器等客户端挂载 `\\<IP>\Internal` 时报 `SMB2_CREATE ... STATUS_CONNECTION_DISCONNECTED (0xC000020C)`；Samba 日志对应 `smbd_smb2_request_error_ex: ... status[NT_STATUS_CONNECTION_DISCONNECTED] || at smb2_create.c:393`。`smbclient` 等只走数据共享的客户端不受影响，能正常列目录。
   - 原因：Samba ≥ 4.15 的命名管道全部由 `samba-dcerpcd` 提供，`smbd` 从编译期 `LIBEXECDIR`（patch 后为 `<data>/libexec/samba`）`posix_spawn` 它。`bundle.py` 只打包了 `bin/` 与 `lib/`，该目录不存在；bionic 的 `posix_spawn` 不向父进程报告 exec 失败，子进程直接退出，`smbd` 读 ready 管道得到 `EPIPE`，而 `EPIPE` 在 `libcli/util/errmap_unix.c` 里映射成 `NT_STATUS_CONNECTION_DISCONNECTED`。
   - 影响面：客户端只要先通过 `srvsvc`（IPC$）枚举共享就会挂掉——包括 MT 管理器、资源管理器一类“先列共享再挂载”的实现。
   - 修复：`bundle.py` 的 NEEDED 闭包加入 `samba-dcerpcd` + `rpcd_*`，输出 `libexec/samba/`；`build.sh` staging 复制并 `0755`；`post-fs-data.sh`/`customize.sh` 建立 `data/libexec` 稳定符号链接；`uninstall.sh` 清理该链接。用同一份 Termux 4.25.1 二进制在本地验证：移走 `libexec` 复现 `0xC000020C @ smb2_create.c:393`，放回后 `srvsvc` 管道打开成功。

7. **`smbd -l` 让日志写到 `log.smbd`，WebUI 永远读不到**
   - 现象：`/api/logs?source=smbd` 一直返回 `open .../logs/smbd.log: no such file or directory`，即使已有真实连接；实际文件是 `<logs>/log.smbd`。
   - 原因：Samba 的 `-l` 是 log basename，传目录后日志写成 `<dir>/log.smbd`，并忽略 smb.conf 里的绝对 `log file`。
   - 修复：`SambaManager.Start` 不再传 `-l`（`log file = <data>/logs/smbd.log` 生效）；`logTail` 在 `smbd.log` 不存在时回退读 `log.smbd`，兼容旧版本产生的日志。

