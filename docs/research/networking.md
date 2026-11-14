# Android 网络调研（Android 15/16/17 · Root 模块视角 · arm64）

- **调研日期**：2026-11-14
- **方法**：只读检索一手资料并逐条抓取原文。主要来源：Linux man-pages（`rtnetlink(7)`、`route(8)`、`ip-address(8)`，man7.org）、Linux 内核文档（`docs.kernel.org` 的 `networking/operstates`、`networking/proc_net_tcp`、`networking/fib_trie`）、Linux UAPI 头文件 `include/uapi/linux/rtnetlink.h` 与 `include/net/tcp_states.h`（经 git.kernel.org / jsDelivr 读原文）、IANA 特殊用途地址注册表（IPv4/IPv6）、Samba 官方 Wiki。KernelSU 侧 SELinux 结论取自 `tiann/KernelSU` 仓库 `kernel/selinux/rules.c`、`kernel/selinux/selinux.h` 原文。
- **范围**：Root 进程如何观测 Android 网络状态、如何判定「网络可用」、Samba/WebUI 的绑定策略、端口冲突检测、网络状态机。**不覆盖**：netd/ConnectivityService 内部实现、eBPF 流量统计、VPN 协议本身、Samba 协议细节。
- **可信度**：
  - `[已确认]`：可在文末链接原文中直接读到，可直接作为实现依据。
  - `[推断/待验证]`：官方文档未明说，由已确认机制推导，或来自工程经验，**必须上机验证后才能写进实现**。
  - **本轮可达性**：`man7.org`、`docs.kernel.org`、`rfc-editor.org`、`iana.org`、`wiki.samba.org`、`kernelsu.org`、`cdn.jsdelivr.net`（GitHub 镜像）、`api.github.com` 可达；**`developer.android.com`、`source.android.com`、`android.googlesource.com` 一律不可达**（fetch failed）。因此本文中所有「Android 框架层」表述（ConnectivityManager、netd、`cmd connectivity` 等）**没有原文支撑，全部降级为 `[推断/待验证]`**；文中已确认部分只涉及 Linux 内核与 IANA/Samba 这类与 Android 版本无关的接口。`github.com` 与 `raw.githubusercontent.com` 本轮仍未验证可达，仓库文件经 jsDelivr 读取（内容与仓库同源）。

---

## 1. 结论摘要（先看这 10 条）

1. **不要依赖 ConnectivityManager**：`NetworkCallback` 是应用进程（有 `Context`）的 Java API；纯 native root 守护进程没有合法的 binder 客户端与权限去注册它。Root 侧可行的事实标准是 **rtnetlink**（`NETLINK_ROUTE`）与 `/proc`、`/sys`。`[推断/待验证]`（Android 侧无原文）／rtnetlink 可用性 `[已确认]`
2. 判断链路是否可用，内核文档给的标准是 **`IFF_RUNNING` 或 `IFLA_OPERSTATE ∈ {UP, UNKNOWN}`**，并明确建议「routing daemon 或 dhcp client 应等 operstate 变为 UP/UNKNOWN 后再认为接口可用」。**只看 admin up（`IFF_UP`）是不够的**。`[已确认]`
3. 事件驱动的最优解是 **订阅 rtnetlink 多播组**：`RTNLGRP_LINK`（接口增删/flags 变化）、`RTNLGRP_IPV4_IFADDR`、`RTNLGRP_IPV6_IFADDR`（地址增删）。`RTMGRP_LINK=1`、`RTMGRP_IPV4_IFADDR=0x10`、`RTMGRP_IPV6_IFADDR=0x100`，均在 UAPI 头文件里写死。`[已确认]`
4. 私网判定有权威依据：IPv4 `10/8`、`172.16/12`、`192.168/16`（RFC1918）、`169.254/16` link-local（RFC3927）、`100.64/10` 共享地址/CGNAT（RFC6598）；IPv6 `fc00::/7` ULA（RFC4193）、`fe80::/10` link-local（RFC4291）。**移动数据（`rmnet_data*`）经常拿到 `100.64/10`**，不能被误判为 `192.168/16` 之外的「公网」。`[已确认]`（IANA 注册表逐行列出）
5. **不要用 ping 外网来判定网络可用**。理由：会引入 DNS/ICMP/防火墙依赖、唤醒射频耗电、在 VPN/VPC 下误判；而「本地有私网地址 + 接口 operstate up」已足以支撑「SMB 可被局域网访问」这个真正的目标。`[推断/待验证]`（工程判断，依据 §4 判定模型）
6. 默认路由**只用来排序候选接口**（首选有默认路由的物理接口），**不作为可用性前置条件**：只连局域网、路由器不提供默认路由的热点场景下，SMB 依然完全可用。`[推断/待验证]`
7. Samba 侧的正确组合是 `bind interfaces only = yes` + `interfaces = lo <iface>`，**`lo` 必须包含**（官方 Wiki 原文：「you should always enable Samba to listen on the `loopback` (`lo`) devices」）；`interfaces` 既接受设备名也接受 IP 地址。`[已确认]`
8. 在 KernelSU 上，`su` 进程运行在 **`u:r:ksu:s0`**，该 domain 在 `rules.c` 里被 `ksu_permissive()` 且 `ksu_allow(domain, ALL, ALL, ALL)`，并带 `netdomain` 属性 —— 即**绑定 445、读 `/proc/net`、开 netlink socket 在 SELinux 层不会成为阻碍**。这与 Magisk 的 `magisk` domain 是同一设计思路。`[已确认]`
9. 端口冲突检测不能只看 `bind()` 成功：**dual-stack 下绑定 `::`（`IPV6_V6ONLY=0`）会同时占用 IPv4 端口**，因此必须同时查 `/proc/net/tcp` 与 `/proc/net/tcp6`（或直接做双栈 bind 探测）。`[推断/待验证]`（/proc/net/tcp 格式 `[已确认]`，dual-stack 占用行为是 Linux 默认语义）
10. 网络状态机建议 **`NO_NETWORK → WAIT_STORAGE → READY`**，并在每次迁移上加「连续 N 次采样一致」的去抖窗与「重启冷却/指数退避」；本机 `wlan0=192.168.1.223` 之外还有 `vgate0`/`tun0`，必须把「候选接口白名单」显式写死，绝不能 `bind` 到 `all`。`[推断/待验证]`（本机接口存在为真机已知事实）

---

## 2. Android 15/16/17 常见网络接口

### 2.1 接口名清单与确认度

| 接口名 | 典型场景 | 确认度 |
|---|---|---|
| `wlan0` | Wi-Fi STA（主用）。**本机确认存在且为 `192.168.1.223`** | `[已确认]`（真机） |
| `lo` | loopback，始终存在 | `[已确认]`（Linux 基础事实） |
| `eth0` | 有线/USB-Ethernet dongle；部分 TV/车机/Auto 设备主接口 | `[推断/待验证]` |
| `usb0`、`rndis0` | USB 网络共享（RNDIS/CDC-ECM），名字随 USB gadget 配置而异 | `[推断/待验证]` |
| `rmnet_data0`、`rmnet_data1`… | 移动数据（QMI/rmnet），常有多个 PDN；地址多为 `10.x` 或 `100.64/10` | `[推断/待验证]` |
| `tun0` | 第三方 VPN 的 TUN 隧道（WireGuard/OpenVPN 类）。**本机确认存在** | `[已确认]`（存在性）／用途 `[推断/待验证]` |
| `vgate0` | 某类 VPN/隧道应用自建的接口（非内核标准命名）。**本机确认存在** | `[已确认]`（存在性）／来源 `[推断/待验证]` |
| `ap0`、`softap0`、`wlan1` | Wi-Fi 热点（SoftAP）。**命名强依赖芯片/驱动（Broadcom 常见 `wlan1`，部分方案 `ap0`）** | `[推断/待验证]` |
| `ppp0` | 传统 PPP（少见；现代 Android 多用 rmnet） | `[推断/待验证]` |
| `dummy0`、`ip6tnl0`、`sit0` | 系统预置的哑接口/隧道占位，**没有可用 IPv4，必须排除** | `[推断/待验证]` |

> 结论：**接口名不可作为唯一真理**。可靠做法是「名字前缀启发式 + 地址族/私网判定 + operstate」三者叠加；名字只用于**白名单/黑名单**（例如明确排除 `tun*`、`vgate*`、`rmnet*`）。`[推断/待验证]`

### 2.2 枚举接口的三种口径

- `/sys/class/net/`：目录列表即当前 netns 的接口名，零解析成本。`[推断/待验证]`（路径为 Linux 惯例；内核文档确认 `IFLA_OPERSTATE` 可经 sysfs 读取 `[已确认]`）
- `ip link show` / `ip -4 addr show`：语义最全（flags、mtu、address），但要 fork 进程。`[已确认]`（`ip-address(8)` 语法）
- netlink `RTM_GETLINK` + `RTM_GETADDR` dump：无 fork、结构化、可增量。`[已确认]`（`rtnetlink(7)` 的消息与属性表）

---

## 3. Root 可用的信息源与开销比较

### 3.1 各信息源能提供什么

| 信息源 | 能拿到 | 关键限制 | 证据 |
|---|---|---|---|
| `/proc/net/route` | IPv4 路由表（含默认路由、flags、mask、iface） | **仅 IPv4**；无 IPv6；列语义需按 net-tools 口径理解 | `[已确认]`（`route(8)` FILES 同时列出 `/proc/net/ipv6_route`，说明 route 文件只覆盖 v4） |
| `/proc/net/ipv6_route` | IPv6 路由表 | 格式与 v4 完全不同，解析成本高 | `[已确认]`（同上，文件存在） |
| `/proc/net/fib_trie` | FIB trie 的人类可读 dump（含 `Main:`/`Local:` 段） | 输出是调试视图，**不是稳定 ABI**；无正式格式文档 | `[已确认]`（LC-trie 存在与 `fn_trie_dump()` 行为）／格式稳定性 `[推断/待验证]` |
| `/proc/net/tcp`、`/proc/net/tcp6` | 监听/已建立 socket、inode、uid、状态 | 十六进制小端地址；**内核文档已标注 deprecated，推荐 `tcp_diag`** | `[已确认]` |
| `/proc/net/dev` | 每接口收发计数 | 无地址、无状态语义 | `[推断/待验证]` |
| `/sys/class/net/<if>/operstate` | RFC2863 运行态 | 值为 `up/down/unknown/...`，`unknown` 对 TUN 类设备很常见 | `[已确认]`（`IFLA_OPERSTATE` 可经 sysfs 查询）／具体路径 `[推断/待验证]` |
| `ip addr` / `ip route` | 全部地址/路由/scope/flags | 需 fork；需确认 Android 镜像是否带 `ip` | `[已确认]`（语义）／Android 上是否存在 `[推断/待验证]` |
| `getprop` | `dhcp.*`、`wifi.*`、`net.*` 等属性 | **属性不是网络状态权威源**，不同 ROM/版本行为不一致，可能残留旧值 | `[推断/待验证]` |
| `dumpsys connectivity` / `dumpsys wifi` | LinkProperties、NetworkAgent 状态 | 单次开销极大（binder dump，百毫秒到秒级），输出格式随版本漂移 | `[推断/待验证]` |
| rtnetlink（`NETLINK_ROUTE`） | 接口/地址/路由全量 + **变更通知** | 需自己实现 netlink 编解码 | `[已确认]` |

### 3.2 开销量级（工程估算，必须实测）

下表是量级判断，**不是实测值**：`[推断/待验证]`

| 方案 | 单次开销量级 | 适用频率 |
|---|---|---|
| netlink 事件订阅（idle） | ≈0（阻塞在 `recv`） | 常驻 |
| netlink `RTM_GETLINK`+`GETADDR` dump | 亚毫秒~几毫秒 CPU，几 KB 内存 | 事件触发时 / 5–15 s |
| 读 `/proc/net/route` + `/sys/class/net/*/operstate` | <1 ms，无 fork | 5–15 s |
| `ip -4 addr show` | 一次 fork + exec，5–15 ms | ≥15 s 或仅调试 |
| `getprop`（整表） | fork + 属性服务读，~10 ms，输出大 | 仅启动时一次 |
| `dumpsys connectivity` | 100 ms~1 s+ | 仅用户手动诊断 |

### 3.3 建议（低开销优先）

1. **常驻事件驱动**：起一个 netlink socket，`bind` 到 `RTMGRP_LINK | RTMGRP_IPV4_IFADDR | RTMGRP_IPV6_IFADDR`，`recv` 到事件后再做一次 dump 校验。这是内核文档描述的标准做法（802.1X supplicant 的交互流程即「subscribe RTNLGRP_LINK → 一次 RTM_GETLINK 取初值 → 等多播」）。`[已确认]`
2. **兜底轮询 5–15 s**：netlink 事件可能丢（buffer 溢出、进程重启窗口），加一个低频采样做对账。5–15 s 对 SMB 场景完全够用（用户感知的是「手机换 Wi-Fi 后 10 秒内可访问」）。
3. **绝不在轮询里跑 `dumpsys`/`ping`/`nslookup`**。`[推断/待验证]`
4. 只在**状态迁移**时做重活（重启 smbd、重算绑定地址），稳态下只做一次 `/proc/net/route` + operstate 的轻量采样。

---

## 4. 「网络可用」的判定模型

### 4.1 判定顺序（从便宜到昂贵）

```
候选接口 = /sys/class/net 下所有接口
  ├─ 排除：lo、dummy*、*tnl*、sit*、黑名单前缀（tun*、vgate*、rmnet_data* 默认排除）
  ├─ 排除：无 IPv4 地址
  ├─ 要求：operstate ∈ {up, unknown} 且 IFF_UP  ← 内核文档口径
  ├─ 要求：IPv4 属于私网（RFC1918）且 **不是** 169.254/16 link-local
  └─ 排序：有默认路由 > 无默认路由；物理接口（wlan0/eth0/usb0/rndis0）> 其它
结果：取排序后第一个 → READY；否则 NO_NETWORK
```

证据：operstate 口径 `[已确认]`；私网/链路本地地址段 `[已确认]`（IANA + RFC1918）；黑名单与排序策略 `[推断/待验证]`。

### 4.2 为什么是「私网 + 非 link-local」

- Android 设备在**没有 DHCP 的临时直连**场景会拿到 `169.254/16`；IANA 注册表把它标为 `Forwardable=False`（路由器不转发），意味着「只能同链路通信」。对 SMB 来说它**可能可用但不该被当作正常 LAN 服务地址**，默认排除更安全。`[已确认]`（注册表字段）
- 移动数据常拿到 `100.64/10`（RFC6598 共享地址）。它**不是 RFC1918 私网**，但也不是公网可路由地址；把它当「公网」而阻止，比当「私网」而放行更安全 —— 我们默认**不**在其上提供服务。`[已确认]`（地址段）／策略 `[推断/待验证]`
- IPv6 侧：`fc00::/7`（ULA）与 `fe80::/10`（link-local）都可能是局域网可达地址；`fe80::` 需要 scope id（`%wlan0`）才能在应用层使用，**Samba 通常不用它**，建议只把 ULA 与 GUAs 纳入考虑。`[已确认]`（地址段）／Samba 行为 `[推断/待验证]`

### 4.3 地址「存在 ≠ 可用」：必须排除 tentative / dadfailed

`ip-address(8)` 定义了地址 flags：`tentative`（尚未通过 DAD）、`dadfailed`（DAD 失败）、`deprecated`（不用于新连接）、`dynamic`、`secondary`、`temporary`。IPv6 SLAAC 地址在 DAD 期间处于 `tentative`，此时绑定会失败或行为异常；IPv4 也有 DAD（`arping` 类冲突检测）窗口。`[已确认]`（flags 语义）

> 实现要求：netlink `RTM_GETADDR` 解析 `IFA_FLAGS`，跳过 `IFA_F_TENTATIVE | IFA_F_DADFAILED` 的地址；**去抖窗口本身就顺带覆盖了 DAD 窗口**，但显式检查更稳。`[已确认]`（flags 常量属于 UAPI `if_addr.h`）／实现细节 `[推断/待验证]`

### 4.4 默认路由的正确用途

默认路由只用于**排序**：同时有 `wlan0`（有默认路由）与 `usb0`（无默认路由）时优先 `wlan0`。**不能用「有默认路由」作为可用性前置条件** —— 手机开热点给 LAN、或纯局域网无上行时，SMB 仍然完全可用。`[推断/待验证]`

---

## 5. 没有 ConnectivityManager 回调怎么办

### 5.1 事实

- `ConnectivityManager`/`NetworkCallback` 属于 Android 应用框架（Java/Kotlin，需要 `Context` 与 binder 会话），回调经由 binder 从 `system_server` 推送到**已注册的应用进程**。`[推断/待验证]`（`developer.android.com` 不可达，无原文）
- 因此 root 守护进程拿不到「Wi-Fi 连上/断开」的系统回调，除非自己起 App 或伪造 binder 客户端（后者需要系统权限，不建议）。`[推断/待验证]`
- 可靠的替代品就是 Linux 原生接口（§3）。内核/驱动/网络栈在 Android 15/16/17 之间**没有改变 rtnetlink 与 procfs 的语义**；同一套 `NETLINK_ROUTE` 代码在 15–17 通用。`[推断/待验证]`（版本差异无原文，但 `rtnetlink(7)` 描述的接口是内核 ABI）

### 5.2 事件集与推荐订阅

| 事件 | 多播组 / 消息 | 用途 | 证据 |
|---|---|---|---|
| 接口增删、flags（含 carrier/operstate）变化 | `RTNLGRP_LINK`(`1`) / `RTM_NEWLINK`、`RTM_DELLINK`、`RTM_SETLINK` | 接口 up/down、插拔 USB 网卡 | `[已确认]` |
| IPv4 地址新增/删除 | `RTNLGRP_IPV4_IFADDR`(`0x10`) / `RTM_NEWADDR`、`RTM_DELADDR` | DHCP 续租换 IP、静态 IP 改动 | `[已确认]` |
| IPv6 地址新增/删除 | `RTNLGRP_IPV6_IFADDR`(`0x100`) / `RTM_NEWADDR`、`RTM_DELADDR` | ULA/SLAAC 地址 | `[已确认]` |
| 路由变化 | `RTNLGRP_IPV4_ROUTE`(`0x40`)、`RTNLGRP_IPV6_ROUTE` | 默认路由切换（排序变化） | `[已确认]` |

### 5.3 去抖（debounce）

Android 上网络状态是**高频抖动**的：Wi-Fi 省电唤醒、AP 漫游（`wlan0` 短暂 down）、DHCP renew、VPN 建立/拆除（`tun0` 出现必然触发 `RTNLGRP_LINK`）、热点启停。

建议规则 `[推断/待验证]`：

1. 事件到达后**不要立刻重启服务**，先标脏并进入 1.5–3 s 观察窗。
2. 观察窗结束后做一次全量采样，若「关键字段」（接口名 + IPv4 地址 + 路由 iface）与当前运行态**一致**则丢弃本次事件。
3. 采样需连续 2 次一致（间隔 ~1 s）才认为稳定。
4. 若决定重启：`stop → 等端口释放 → start`，并记录 `last_restart`；冷却 5 s，失败退避 5→10→30→60 s，连续 N 次失败转 `ERROR` 状态并停止重试（避免无限重启拉爆电量）。
5. 保护「同一 IP 下的短暂抖动」：如果 60 s 内 IP 没变，只重启一次（很多事件风暴最终收敛到同一 IP）。

---

## 6. Samba 绑定策略（`interfaces` / `bind interfaces only`）

### 6.1 参数语义

- `bind interfaces only = yes` + `interfaces = lo eth0`：把 Samba 服务绑定到指定接口。官方 Wiki 明确这是「多网口机器上只对特定接口提供服务」的标准做法，并强调**必须一并监听 `lo`**，否则本机工具（不带主机名时走 loopback）会连不上。`[已确认]`
- `interfaces` 的值**可以是设备名，也可以是 IP 地址**（Wiki 原文：「enables you to use alternative values, such as IP addresses instead of device names」，细节指向 `smb.conf(5)`）。`[已确认]`

### 6.2 绑接口 vs 绑 IP（本项目必须显式选一个）

| 维度 | 绑定接口名（`wlan0`） | 绑定 IP（`192.168.1.223`） |
|---|---|---|
| IP 变化（DHCP 续租换址） | 语义随接口走，**但 smbd 是否自动跟随新地址未验证** | 必然失效，必须重启 smbd |
| 多地址接口（IPv4+IPv6、secondary） | 自动覆盖该接口所有地址（可能包括不想暴露的） | 精确控制 |
| 热点/VPN 出现新接口 | 不受影响（白名单里没有它） | 不受影响 |
| 配置生成复杂度 | 低（只需接口名） | 高（要 dump 地址） |

结论 `[推断/待验证]`：**默认用接口名**（`interfaces = lo wlan0`），把「IP 变化」当作**重启触发器**而不是配置字段；只有用户显式要求「只在这一个 IP 上监听」时才绑 IP。

> 无论选哪种，**IP 变化后必须重启 smbd**：Samba 在启动时解析 `interfaces` 并建 socket，之后地址变更不会自动重建监听。`[推断/待验证]` —— 这是本文最需要上机验证的一条（用 `ip addr add/ del` 在 `wlan0` 上加删 secondary 地址，观察 445 监听是否跟随）。

### 6.3 风险清单

1. **`interfaces = all`（默认）绝对不要用**：会绑定到 `rmnet_data0`、`tun0`、`vgate0` 等所有接口，等于把 SMB 服务暴露给运营商网络或 VPN 对端。`[推断/待验证]`
2. **VPN 存在 ≠ 阻断 LAN**：`tun0`/`vgate0` 的默认路由会抢走**出向**流量，但**入向**（LAN 客户端连 445）仍走 `wlan0`。真正的风险是 VPN App 的 iptables/nftables 规则（`allow LAN` 开关、kill-switch 的 INPUT DROP）—— 这类规则会静默吃掉 445 入向包，表现为「能 ping 通但 SMB 连不上」。诊断要看 `iptables -S`/`nft list ruleset`。`[推断/待验证]`
3. **热点模式**：SoftAP 接口通常是 `wlan1`/`ap0`/`softap0`；若用户想「开热点给平板访问手机上的 SMB」，候选接口集合与地址段（常见 `192.168.43.x`、`192.168.232.x` 等 ROM/驱动相关网段）都会变，必须重新采样而不是硬编码。`[推断/待验证]`
4. **多网卡同时在线**：Wi-Fi + USB 网卡 + VPN → 允许多个监听，但要在 UI 上明确「当前在哪些地址上提供服务」，否则用户不知道自己暴露在哪。`[推断/待验证]`
5. **`smb ports`**：只开 `445` 可以显著减少监听面；`139`（NetBIOS over TCP）在 Android 上无 WINS 支持、且被 SMB1 依赖，建议默认关闭。`[推断/待验证]`

---

## 7. WebUI 的绑定与来源限制

### 7.1 三种绑定方式

| 方式 | 谁能访问 | 适用 |
|---|---|---|
| `127.0.0.1:<port>` | 只有本机进程（需 `adb forward`/`adb reverse` 或同设备代理） | 最安全；调试与「配合 VPN/反代」首选 |
| `<wlan0 IP>:<port>` | 该 LAN 内到该地址的客户端 | 平衡方案；IP 变了要重建 listener（同 §6.2） |
| `0.0.0.0:<port>` | 所有接口，含 `rmnet_data*`/`tun*`/`vgate*` | 最方便也最危险，**必须配合来源过滤 + 认证** |

`[推断/待验证]`（HTTP 监听行为是通用工程事实，非 Android 特有）

### 7.2 私网来源判定（按 peer address 过滤）

允许的客户端来源（`accept`）：

- IPv4：`10/8`、`172.16/12`、`192.168/16`（RFC1918）—— `[已确认]`
- IPv4：`169.254/16`（link-local）—— 可选放行（USB 直连/临时直连），默认**拒绝**更保守 `[已确认]`（地址段）
- IPv6：`fc00::/7`（ULA）、`fe80::/10`（link-local，注意需 `scope_id`）—— `[已确认]`

必须拒绝：

- `0.0.0.0/8`、`127/8`（loopback 仅本机健康检查，且要单独路径）、`240/4`（Reserved）、`255.255.255.255/32`（Limited Broadcast）—— `[已确认]`（IANA IPv4 特殊用途注册表逐项列出）；`224/4` 多播地址段不在该注册表内，单独按「非单播、不可能是 SMB 客户端地址」排除 `[推断/待验证]`
- 任何**非**上述私网段的全局单播地址（即公网来源）—— 这是「不把管理面暴露到公网」的核心防线 `[推断/待验证]`
- `100.64/10`：既非 RFC1918 也非全球可路由（RFC6598）。默认**拒绝**（避免运营商侧可达）`[已确认]`（地址段）／策略 `[推断/待验证]`

要点：**过滤必须在 accept 之后立刻做**（先 `getpeername` 再决定是否继续），并且**不能只依赖 `Host`/`X-Forwarded-For` 头**——头可伪造。`[推断/待验证]`

### 7.3 反向代理注意

- 让反代（如设备上的 nginx/Caddy，或路由器上的反代）连 `127.0.0.1:<port>`，**WebUI 本体不要监听 `0.0.0.0`**。这样即使 WebUI 自身认证薄弱，暴露面也只是 loopback。`[推断/待验证]`
- 反代若把 `X-Forwarded-For` 透传进来，只能用于**日志**，不能用于访问控制（客户端可伪造）。访问控制必须基于 TCP peer 地址。`[推断/待验证]`
- TLS 建议在反代层终止；Android 上自签证书的信任问题比 PC 更麻烦。`[推断/待验证]`
- 若反代在**路由器**上，注意它会把 WebUI 暴露给整个 LAN，等于绕过了 §7.2 的 peer 过滤（peer 变成路由器地址）—— 此时应改用 Basic Auth + 仅 LAN 可达 + 强口令。`[推断/待验证]`

---

## 8. Android 权限 / SELinux 视角

### 8.1 KernelSU 下（本机：`u:r:ksu:s0`）

- `KERNEL_SU_DOMAIN` 定义为 `"ksu"`，完整上下文 `u:r:ksu:s0`；`KERNEL_SU_FILE` 为 `u:object_r:ksu_file:s0`。`[已确认]`（`kernel/selinux/selinux.h`）
- `apply_kernelsu_rules()` 里对该 domain：`ksu_type(db, KERNEL_SU_DOMAIN, "domain")`、**`ksu_permissive(db, KERNEL_SU_DOMAIN)`**、`ksu_typeattribute(..., "netdomain")`、`mlstrustedsubject`、`bluetoothdomain`，并且 **`ksu_allow(db, KERNEL_SU_DOMAIN, ALL, ALL, ALL)`**（源码注释原文 `// allow all!`），另有对 `blk_file/fifo_file/chr_file/file` 的 `allowxperm`。`[已确认]`（`kernel/selinux/rules.c`）
- 结论：**在 KernelSU 的 `su` 上下文里，SELinux 不是约束**。绑定 445、读 `/proc/net/*`、开 `NETLINK_ROUTE` socket 都不会被 deny（即使有规则也会因 permissive 只审计）。`[已确认]`（机制）／具体操作是否触发 audit `[推断/待验证]`
- 但这**不等于可以不做权限检查**：permissive 意味着「错了也不会报错」，会把真正的问题（比如 Android 未来版本的 neverallow 或 LSM 之外的限制）隐藏到上线之后。`[推断/待验证]`

### 8.2 端口与 capability

- `445` **小于 1024，按 Linux `capabilities(7)`/`ip_unprivileged_port_start` 语义属于特权端口**，root（uid 0，默认保有 `CAP_NET_BIND_SERVICE`）可以绑定；非 root 需要 `net.ipv4.ip_unprivileged_port_start=0`。WebUI 的 `8080` 等 ≥1024 端口不需要该 capability。本项目已在 OnePlus PJA110 / Android 16 / KernelSU 的 `u:r:ksu:s0` root 下实测绑定 445 成功。`[已确认·Linux 语义；真机绑定已验证]`
- `uid 0` 会绕过普通 DAC；`NETLINK_ROUTE` 的**读**操作不需要 `CAP_NET_ADMIN`，**写**（改地址/路由）需要 `CAP_NET_ADMIN`。本模块只读，故无需写权限。`[推断/待验证]`（`rtnetlink(7)` 只说「可读可改」，未逐条列 capability）
- Android 的「网络访问」控制（`AID_INET` group / `INTERNET` 权限）针对**应用 UID**；`uid 0` 或 `su` 进程不受该限制。`[推断/待验证]`

### 8.3 需要注意的 Android 特有坑

- `/proc/net` 在 Android 上可能被 SELinux 针对某些 domain 限制；在 `u:r:ksu:s0` 下不是问题，但**如果本模块哪天改成以 `shell`/`system` 身份跑**，结论会失效。`[推断/待验证]`
- 分享目录若用 `/sdcard`，会经过 FUSE（`/storage/emulated`），**root 直接读 `/sdcard` 可能遇到 FUSE 权限与性能问题**；更稳的是 `/data/media/0`（`/sdcard` 的真实落点）或应用私有目录。`[推断/待验证]`（需真机实测吞吐与权限）
- Doze/省电策略对 root 进程不适用（root 进程不是 App），但**内核 WiFi 省电**可能让 `wlan0` 在高负载下抖；监听由 smbd 负责，不额外处理。`[推断/待验证]`

---

## 9. 端口冲突检测与 dual-stack

### 9.1 `/proc/net/tcp` 口径

- 格式：`sl local_address rem_address st ...`，地址与端口是**十六进制**，IPv4 地址按**小端**存放（内核文档示例 `010310AC:9C4C`）。`[已确认]`
- **`st` 是 TCP 状态码的十六进制**：`0A` = `TCP_LISTEN`（`TCP_LISTEN = 10`），`01` = `TCP_ESTABLISHED`，`06` = `TCP_TIME_WAIT`。`[已确认]`（`include/net/tcp_states.h` 的枚举顺序 + 内核文档的 state 字段说明）
- 内核文档已标注这两个 proc 接口 **deprecated，推荐 `tcp_diag`**；但读取仍是 0 fork 的最低成本方式。`[已确认]`

### 9.2 检测顺序（推荐）

1. **先查 PID 文件 + `/proc/<pid>/cmdline`**：如果本模块自己的 smbd 还活着，说明是「自己占着」，走重启流程而不是报冲突。`[推断/待验证]`
2. **查 `/proc/net/tcp` 与 `/proc/net/tcp6`**：找 `st == 0A` 且 `local_port == 445` 的条目；同时把 IPv6 侧也算进来（见 9.3）。`[推断/待验证]`
3. **再做 bind 探测**：`socket(AF_INET, SOCK_STREAM)`，**不设 `SO_REUSEPORT`**，`bind(待绑定的确切地址:445)`。`SO_REUSEADDR` 的语义是允许覆盖 `TIME_WAIT`，**不会**让已被 `LISTEN` 占用的端口绑定成功；`SO_REUSEPORT` 才会静默「共享」，用它探测会得到假阴性且可能造成两个进程同时监听，**禁止**。`[推断/待验证]`
4. 探测成功后立即 `close()`，并把「探测 → 真启动」窗口压到最小，避免 TOCTOU。`[推断/待验证]`

### 9.3 dual-stack 陷阱

- Linux 默认 `net.ipv6.bindv6only = 0`：绑定 `::` 的 socket 会**同时**接受 IPv4 连接（`v4-mapped`），即把 IPv6 端口也占了。因此「只查 `/proc/net/tcp`」会漏判。`[推断/待验证]`（Linux 默认语义；`ipv6(7)` 未抓取，标注待验证）
- 对策：**要么**明确只绑 IPv4（`bind` 到 IPv4 地址，不开 `AF_INET6` listener），**要么**显式设 `IPV6_V6ONLY=1` 后再另开 IPv4 listener，两者不要混。Samba 的 `smb ports`/地址族行为由 Samba 决定，与本模块自己的 HTTP listener 分开考虑。`[推断/待验证]`
- 若 `smbd` 已监听 `::`，本模块再 `bind` IPv4 `0.0.0.0:445` 会 `EADDRINUSE`；反过来也一样。这正是需要查 `tcp6` 的原因。`[推断/待验证]`

---

## 10. 网络状态机

```
        ┌──────────────┐
        │  NO_NETWORK  │◄──────────── 采样失败 / 接口消失 / 地址非私网
        └──────┬───────┘
               │ 候选接口 operstate up + 私网 IPv4（连续 2 次采样一致，≥1.5s）
               ▼
        ┌──────────────┐
        │ WAIT_STORAGE │  /data/adb 可写 + 分享根目录可读（可选：/sdcard 挂载完成）
        └──────┬───────┘
               │ 目录自检通过 + 端口无冲突（§9）
               ▼
        ┌──────────────┐
        │    READY     │  启动 smbd（绑定 §6 生成的 interfaces 列表）
        └──────┬───────┘
               │ netlink 事件：IP 变化 / 接口消失 / 端口被抢
               ▼
        回 NO_NETWORK（先 stop，进入冷却与退避）
```

要点 `[推断/待验证]`：

- **`WAIT_STORAGE` 不是摆设**：`service.sh` 阶段 `/data` 已解密挂载，但 `/sdcard`（FUSE）与用户解锁态可能晚于 `service`。若分享目录在用户存储上，用一个「目录可读 + 子项可 stat」的探针代替固定 sleep。
- **迁移必须幂等**：同一状态重复进入不得重复启动进程；用「期望配置指纹（interfaces + port + share list）→ 与运行态比对」决定是否重启。
- **冷却与退避**：重启失败后 5→10→30→60 s 退避，连续 5 次失败进入 `ERROR`（不再自动重试，UI 显示原因），避免无限重启导致的耗电与日志爆量。
- **状态变化要落日志**：写 `/data/adb/smbshare/logs/net.log`（时间戳、事件、决策），排障时比 `dumpsys` 有用得多。

---

## 11. 对本项目的约束清单

- 网络观测走 **netlink 事件 + 低频轮询兜底**，不引入 `dumpsys`/`ping`/`getprop` 作为判定依据。
- 候选接口用**白名单/黑名单**：默认 `[lo, wlan0, eth0, usb0, rndis0]`，排除 `tun*`、`vgate*`、`rmnet_data*`、`dummy*`；允许用户在配置里覆盖。
- 判定条件固定为「operstate up/unknown + 私网 IPv4 + 非 link-local + 非 tentative/dadfailed」；默认路由只参与排序。
- Samba 配置生成固定写 `bind interfaces only = yes` + `interfaces = lo <选中的接口>`；`smb ports = 445`（可选关 139）。
- **IP 变化必须触发 smbd 重启**（这是当前唯一的确定性做法，§6.2 待验证）。
- WebUI：默认绑 `0.0.0.0` 太危险 → 提供三档（`127.0.0.1` / 指定 LAN IP / `0.0.0.0`+来源过滤），默认「指定 LAN IP」或「127.0.0.1」。来源过滤按 §7.2 实现。
- 端口冲突检测必须同时看 `tcp` 与 `tcp6`，bind 探测不用 `SO_REUSEPORT`。
- 状态机与去抖参数（1.5–3 s 窗口、2 次一致、5 s 冷却、指数退避）先按 §5.3/§10 实现，**参数必须做成可配置并实测调整**。

---

## 12. 证据不足 / 待上机验证

1. **`smbd` 在 `interfaces` 绑定的接口 IP 变化后是否自动重建监听** —— 未找到 Samba 文档明确说明；本文按「不会」处理。验证：改 `wlan0` 地址后 `ss -ltnp | grep 445` / `curl` 探测。
2. **Android 15/16/17 之间是否存在网络相关行为差异** —— `developer.android.com`、`source.android.com`、`android.googlesource.com` 全部不可达，本轮**没有任何 Android 框架层原文**。所有涉及 Android API 的表述均为推断。
3. **`ip` 命令在 Android 系统镜像中是否存在**（AOSP `external/iproute2` 构建产物；`aosp-mirror` 镜像路径未取到）—— 需真机 `which ip`、`ip -4 addr show` 验证；若缺失，退回 `/proc/net/*` + `toybox netstat`。
4. **`/sys/class/net/<if>/operstate` 的精确路径** —— 内核文档只说该 TLV「可经 sysfs 查询」，具体文件名未在本文档中逐字确认。
5. **`/proc/net/route` 的列序** —— `route(8)` 给出了 `route` 命令输出的列语义与 `/proc/net/route` 的存在，但**未逐字给出 proc 文件的列序**；本文未把它当作解析依据（推荐改用 netlink/`ip`）。
6. **`/proc/net/fib_trie` 是否可作为稳定接口** —— 有 trie 与 dump 机制的文档，但无格式稳定性承诺，视为调试用途。
7. **445 端口与特权端口的定义（`capabilities(7)`）** —— 445 < 1024，属于特权端口；root 可绑定（真机已用 root 绑定 445 验证），非 root 需 `ip_unprivileged_port_start=0`。`[已确认·Linux 语义；真机绑定已验证]`
8. **`SO_REUSEADDR` 与 `LISTEN` 冲突的确切语义** —— 依据 Linux 通用语义推断，未抓 `socket(7)` 原文。
9. **`vgate0` 的具体来源**（哪个 VPN/隧道应用）—— 只知道本机存在该接口，未确认归属。
10. **开销量级表（§3.2）全部是估算** —— 必须在本机用 `time`/`/proc/<pid>/stat` 实测后再写进文档或代码注释。

---

## 13. 来源

**Linux 内核 / man-pages（官方一手）**

- `rtnetlink(7)`（`NETLINK_ROUTE`、`ifinfomsg`/`ifaddrmsg`/`rtmsg`、`RTM_GETLINK/GETADDR/GETROUTE`、`IFA_*`/`RTA_*` 属性表）：https://man7.org/linux/man-pages/man7/rtnetlink.7.html
- Operational States（`IFF_UP`/`IFF_RUNNING`/`IFF_LOWER_UP`/`IFF_DORMANT`、`IF_OPER_*`、`RTNLGRP_LINK`、sysfs 查询、dhcp client 建议）：https://docs.kernel.org/networking/operstates.html
- The proc/net/tcp and proc/net/tcp6 variables（格式、hex 小端、deprecated → `tcp_diag`）：https://docs.kernel.org/networking/proc_net_tcp.html
- LC-trie implementation notes（`fn_trie_dump()`）：https://docs.kernel.org/networking/fib_trie.html
- UAPI `include/uapi/linux/rtnetlink.h`（`RTMGRP_*`、`RTNLGRP_*`、`RTM_NEWADDR/RTM_DELADDR`、`RTM_NEWROUTE`）：https://cdn.jsdelivr.net/gh/torvalds/linux@master/include/uapi/linux/rtnetlink.h （同源：https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/tree/include/uapi/linux/rtnetlink.h）
- `include/net/tcp_states.h`（`TCP_LISTEN = 10` 等状态枚举）：https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/plain/include/net/tcp_states.h
- `route(8)`（`/proc/net/route`、`/proc/net/ipv6_route`、flags 语义）：https://man7.org/linux/man-pages/man8/route.8.html
- `ip-address(8)`（`tentative`/`dadfailed`/`deprecated`/`scope` 语义）：https://man7.org/linux/man-pages/man8/ip-address.8.html

**地址空间（IANA / RFC，官方）**

- IPv4 Special-Purpose Address Registry（`10/8`、`172.16/12`、`192.168/16`、`169.254/16`、`100.64/10`、`127/8`、`240/4`）：https://www.iana.org/assignments/iana-ipv4-special-registry
- IPv6 Special-Purpose Address Space（`fc00::/7`、`fe80::/10`）：https://www.iana.org/assignments/iana-ipv6-special-registry
- RFC 1918（Private-Use 三段）：https://www.rfc-editor.org/rfc/rfc1918.txt

**Samba（官方 Wiki）**

- Configure Samba to Bind to Specific Interfaces（`bind interfaces only`、`interfaces` 支持接口名与 IP、必须含 `lo`）：https://wiki.samba.org/index.php/Configure_Samba_to_Bind_to_Specific_Interfaces

**KernelSU（官方仓库源码，用于 §8 SELinux 结论）**

- `kernel/selinux/selinux.h`（`KERNEL_SU_DOMAIN "ksu"`、`u:r:ksu:s0`）：https://cdn.jsdelivr.net/gh/tiann/KernelSU@main/kernel/selinux/selinux.h
- `kernel/selinux/rules.c`（`ksu_permissive` + `ksu_allow(domain, ALL, ALL, ALL)` + `netdomain`）：https://cdn.jsdelivr.net/gh/tiann/KernelSU@main/kernel/selinux/rules.c

**引用但本轮不可取证（已降级为推断）**

- Android 开发者文档 `ConnectivityManager` / `NetworkCallback`：https://developer.android.com/reference/android/net/ConnectivityManager （fetch failed）
- AOSP 网络相关实现（netd / ConnectivityService）：https://source.android.com/ （fetch failed）
