> **0.1.0 默认模式：匿名、可写、整盘共享（`Internal` → `/storage/emulated/0`），没有 SMB 账号。**  
> 本文中带 `\<IP>\Shared`、`-U android`、`/user:android` 的命令是“认证模式示例”；默认模式下请改用 `\<IP>\Internal`，不需要账号密码。  
> Windows 匿名访问需要 PC 端允许 guest SMB：`tools/windows-allow-guest.ps1`。

# 故障排查

本文按症状组织，命令默认在 Android 手机的 root shell 中执行。`smbctl` 必须是 root，并建议用完整路径：

```sh
SMBCTL=/data/adb/modules/android-smb-relay/bin/smbctl
su -c "$SMBCTL status"
```

快速三连：

```sh
su -c '/data/adb/modules/android-smb-relay/bin/smbctl status'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl logs -n 100 --source asrd'
su -c 'tail -n 100 /data/adb/android-smb-relay/logs/boot.log'
```

`status` 中的关键字段：

| 字段 | 含义 |
|---|---|
| `SMB` | `running` / `starting` / `stopped` / `waiting_network` / `waiting_storage` / `restarting` / `failed` |
| `WebUI` | 是否监听、URL、错误 |
| `Network` | 选中接口与 IPv4；`Ready` 表示有可用地址 |
| `Restarts` | 当前窗口内的 `smbd` 重启次数 |
| `LastError` | 最近一次失败原因 |
| `Warnings` | guest/匿名/无用户/无网络等配置提醒 |

---

## 1. WebUI 打不开

### 1.1 先判断 daemon 是否在运行

```sh
su -c '/data/adb/modules/android-smb-relay/bin/smbctl status'
# 或者
su -c 'cat /data/adb/android-smb-relay/run/asrd.pid'
su -c 'ps -A | grep asrd'
```

- 如果提示 “daemon is not running”：`smbctl` 会尝试自动执行 `scripts/start.sh`。仍失败时：

  ```sh
  su -c 'sh /data/adb/modules/android-smb-relay/scripts/start.sh'
  su -c 'tail -n 100 /data/adb/android-smb-relay/logs/service.out'
  su -c 'tail -n 100 /data/adb/android-smb-relay/logs/asrd.log'
  ```

- `start.sh` 最多等 15 秒控制 socket（`run/asr.sock`）。失败会打印日志尾部并退出 1。

### 1.2 检查端口监听

```sh
su -c 'ss -ltnp 2>/dev/null | grep 8080'
# 没有 ss 时：
su -c 'cat /proc/net/tcp /proc/net/tcp6 | grep -i ":1F90"'   # 8080 = 0x1F90
su -c '/data/adb/modules/android-smb-relay/bin/smbctl url'
```

- `ctl url` 会打印 WebUI URL。若显示 `(disabled)`，说明 `webui.enabled=false` 或启动失败。
- 若端口被占用，日志会出现 `WebUI bind ...: address already in use`；改 `webui.port`（或用 WebUI 前的旧地址访问）。
- 如果只是 WebUI 监听被停止（`smbctl webui-stop` 或在早期版本中手动停过），可用 `smbctl webui-start` 重新启动监听，不影响 `smbd`。
- 若 `webui.bind=127.0.0.1`，只能本机访问；局域网需要 `0.0.0.0` 或具体 LAN IP。

### 1.3 网络与来源过滤

| 检查 | 命令/判断 |
|---|---|
| 手机与电脑是否同网段 | `ip addr` / `smbctl network`；跨 VLAN/访客 Wi-Fi 通常不通 |
| AP 隔离 | 路由器是否开启“AP 隔离/客户端隔离”；开启后设备互访被禁 |
| 手机 IP | `smbctl network` 的 `IPv4`；IP 可能已变化 |
| LAN-only 拒绝 | 若从公网/非私有来源访问，会返回 403 `lan_only`（预期）；在手机本地 `curl http://127.0.0.1:8080/api/health` 应返回 `{"ok":true}` |
| 手机侧 VPN | VPN 可能抢路由或由 VPN App 的防火墙拦截入向；先关 VPN 测试 |
| 电脑侧防火墙 | Windows 防火墙/第三方安全软件是否允许出站到 8080 |
| 反向代理 | 代理可能改变来源地址；LAN-only 看到的是代理地址 |

手机本地自检：

```sh
su -c 'curl -s http://127.0.0.1:8080/api/health'
su -c 'curl -s -o /dev/null -w "%{http_code}\n" http://192.168.1.223:8080/'
```

> WebUI 是 HTTP 明文；如果浏览器强制 HTTPS/有代理，访问 `http://` 地址即可。

### 1.4 登录/CSRF 相关问题

| 现象 | 原因 | 处理 |
|---|---|---|
| 登录后刷新页面变成只读 | CSRF token 只存内存，刷新即丢失（设计） | 点 “Unlock changes” 重新登录 |
| 写操作返回 403 `csrf` | 缺少/错误 `X-CSRF-Token` | 重新登录获取 token；不要用旧页面标签页 |
| 写操作返回 403 `origin` | `Origin` 与请求 Host 不一致 | 用同一地址访问（不要混用 IP/主机名/端口） |
| 登录返回 429 | 失败次数过多，IP 被限速 5 分钟 | 等待 `Retry-After`，或重启 daemon 清空内存限速 |
| 登录报凭据错误 | 口令错/被改/初始文件丢失 | 见 [§6 密码重置](#6-密码重置) |
| 页面 404/空白 | `webui/` 文件缺失或 `--webui` 路径错误 | 检查模块目录 `webui/index.html` |

---

## 2. 445 端口被占用

### 2.1 判断谁占用了 445

```sh
su -c 'ss -ltnp 2>/dev/null | grep ":445"'
# 没有 ss 时（445 = 0x01BD）：
su -c 'cat /proc/net/tcp /proc/net/tcp6 | grep -i ":01BD"'
```

- 如果占用者就是本模块的 `smbd`（PID 与 `samba/pid/smbd.pid` 一致）：说明服务正常，不需要处理。
- 如果占用者是另一个 Samba/Termux Samba/其他模块：先停掉或卸载冲突服务，再重启本模块：

  ```sh
  su -c '/data/adb/modules/android-smb-relay/bin/smbctl restart'
  ```

- 本模块自身的残留进程：

  ```sh
  su -c 'cat /data/adb/android-smb-relay/samba/pid/smbd.pid'
  su -c 'sh /data/adb/modules/android-smb-relay/scripts/stop.sh'
  su -c 'sh /data/adb/modules/android-smb-relay/scripts/start.sh'
  ```

### 2.2 改端口

- 在 WebUI → Settings → SMB Port 修改，保存后 `asrd` 会自动重启 `smbd`。
- 也可以用 `smbctl config` 查看当前端口；直接编辑 `config.json` 需要重启 daemon。
- **注意**：Windows 客户端只使用 TCP 445。改到 `1445` 等端口后，Windows 的 `net use` 不会自动连新端口，需要端口转发（如 `iptables`/`socat`）或客户端支持指定端口；不推荐作为常规方案。
- 不要使用 139（本项目只监听 445，且不启用 NetBIOS）。

### 2.3 端口绑定失败

日志关键字：`start smbd`、`address already in use`、`smbd daemon did not stay running`。处理顺序：

1. 停冲突服务；
2. `smbctl stop` → 确认 445 已释放 → `smbctl start`；
3. 仍失败则改端口；
4. 查看 `smbd-console.log` 与 `asrd.log`。

---

## 3. SMB 登录失败

### 3.1 先确认服务与用户

```sh
su -c '/data/adb/modules/android-smb-relay/bin/smbctl status'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl users'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl shares'
```

- `status` 必须是 `running`；`waiting_storage`/`waiting_network` 时客户端连不上。
- `users` 应为空或列出 `android` 等启用用户。没有启用用户时，非 guest 共享会写 `valid users = nobody`，无法登录（这是有意的“不可达”设计）。
- 确认目标共享存在且 `enabled`。

### 3.2 常见原因

| 现象/错误 | 可能原因 | 处理 |
|---|---|---|
| 用户名/密码错误 | 初始口令没保存、被改、用户被禁用 | 重设口令（§6）；WebUI Users 页确认启用 |
| Windows 反复弹凭据 | 缓存了旧凭据 | `net use * /delete /y`、`cmdkey /list` 清理 |
| `系统错误 53`（找不到网络路径） | IP/共享名错误、SMB1-only 客户端、端口不对 | 用 IP + 正确共享名；确认客户端支持 SMB2/3；确认 445 |
| `系统错误 64/67` | 网络名无效/共享不存在 | `net view \\<ip>` 或 `smbclient -L` 列共享；核对名称大小写 |
| `系统错误 1219`（凭据冲突） | 已用另一组凭据连接同一服务器 | `net use * /delete /y` 后重试 |
| `系统错误 1326`（登录失败） | 用户名/口令错误、用户被禁用 | 核对用户与口令；`smbctl users` 确认启用 |
| 连接成功但列目录为空/拒绝 | `valid users` 不含该用户、共享禁用、路径不存在 | `smbctl shares`、`smbctl config`；检查共享路径是否存在 |
| 写入被拒绝 | 共享只读（如 `Download`） | 这是预期；改用 `Shared`/`Upload` |
| 旧设备/旧客户端连不上 | 服务器最低 SMB2_02、签名强制、NTLMv2-only | 升级客户端；不要用 `-m NT1` |
| `mount error(13): Permission denied`（Linux） | 口令/域/协议/签名不匹配 | 显式 `-U android`，`vers=3.1.1`；先 `smbclient` 验证 |

### 3.3 客户端协议与签名

- 本项目默认 `server min protocol = SMB2_02`、`server signing = mandatory`、`ntlm auth = ntlmv2-only`。
- 仅支持 SMB1 的客户端（或 `smbclient -m NT1`）会被拒绝（已验证）。
- 某些 Android SMB 客户端只支持 SMB1 或不支持签名，会连接失败；换客户端或确认其 SMB2/3 支持。
- 若必须兼容老客户端，也不要通过开启 SMB1 解决（会显著降低安全性）；本项目不提供 SMB1 开关。

### 3.4 服务端日志

```sh
su -c 'tail -n 200 /data/adb/android-smb-relay/logs/smbd.log'
su -c 'tail -n 200 /data/adb/android-smb-relay/logs/asrd.log'
```

日志中常见但**通常无害**的行（来自真机日志观察）：

- `db_open_ex: mutexes requested but not available`
- `tdb_wrap_open: mutexes requested but not available`
- `idmap range not specified for domain '*'`

它们不影响基本认证/读写；如果伴随连接失败，再继续查其他原因。

---

## 4. SELinux denial

### 4.1 采集

```sh
su -c 'getenforce'                                  # 期望 Enforcing
su -c 'dmesg | grep -i "avc:" | tail -50'
su -c 'logcat -d -b all | grep -iE "avc:|selinux" | tail -50'
su -c 'ls -Z /data/adb/android-smb-relay'
su -c 'ls -Z /data/adb/modules/android-smb-relay'
su -c 'ps -A -o PID,NAME,LABEL 2>/dev/null | grep -E "asrd|smbd"'
su -c 'cat /proc/$(pidof smbd 2>/dev/null | awk "{print \$1}")/attr/current 2>/dev/null'
```

关键字段：`scontext`（进程域）、`tcontext`（目标类型）、`tclass`、`permissive=`。

- `permissive=1` 表示该 domain 当前是 permissive：**denial 只记录不拦截**，不能据此认为策略已正确；在 Enforcing 的严格 domain 上可能失败。
- 本项目在 KernelSU 下运行于 `u:r:ksu:s0`（permissive 风格 domain），在 Magisk 下运行于 `magisk` domain，通常不应出现影响功能的 denial；但“零 denial 的普遍性”未验证。

### 4.2 处理原则

1. **不要 `setenforce 0`**，不要写 `/sys/fs/selinux/enforce`；模块本身也不会这么做。
2. 先排除非 SELinux 原因：文件权限（是否 0700/0600 被改）、路径是否存在、FUSE 是否挂载、端口是否被占。
3. 若是文件上下文问题：确认 `/data/adb/**` 为 `u:object_r:adb_data_file:s0`、`/storage/**` 为 `storage_file` 等；不要随意 `chcon`。
4. 确有 denial 且确认是策略缺失：在测试机上用最小规则验证（模块当前**不随包提供 `sepolicy.rule`**）。新增规则属于设备级变更，升级/重启后可能失效；不要照抄网上的 `allow magisk * * *`。
5. 换 Root 方案/机型后重新采集基线，不要假设“本机能跑 = 别的机型也能跑”。

### 4.3 常见误判

- `EACCES` 不一定来自 SELinux：看同一时间是否有 `avc: denied`。
- `dontaudit` 会让部分 denial 不打印；没有 `avc` 不等于没被拦。
- FUSE 的拒绝（MediaProvider 用户态策略）**不会**产生 `avc`；表现为普通 `EACCES/EPERM`。

---

## 5. 存储未解锁 / FUSE 问题

### 5.1 “存储未解锁”的表现

- `status` 显示 `waiting_storage`，`LastError` 类似 `storage not ready: /storage/emulated/0/SMB`。
- `smbd` 不启动；WebUI 可以打开。
- 日志：`storage not ready`、`created missing share directory`、`share ... path is not a directory`。

原因：Android FBE 下 `/storage/emulated/0` 由 MediaProvider 的 FUSE 提供，**必须等用户解锁**（输入 PIN/密码/图案/指纹）才可用；`sys.boot_completed=1` 不代表已解锁。

### 5.2 检查

```sh
su -c 'getprop sys.boot_completed'
su -c 'ls -ld /storage/emulated/0'
su -c 'ls /storage/emulated/0 | head'
su -c 'mount | grep -iE "fuse|emulated"'
su -c 'ls -ld /data/media/0'
su -c 'ls /data/media/0 | head'
```

- `/storage/emulated/0` 不存在或不可列目录 → 正常等待解锁。
- `/data/media/0` 可读 → 可用于回退。
- `wait_for_storage=true`（默认）时不会带病启动；解锁后 `reconcile` 的下一次 tick（≤10 s）会重新评估并启动/重启 `smbd`。

### 5.3 仍然不通时的处理

1. 解锁屏幕并保持解锁，等 10–30 秒再看 `status`。
2. 确认共享根路径没有被改成不存在/不可写的目录：`smbctl config`。
3. 如果 `/storage/emulated/0` 长期不可用：可临时使用 `/data/media/0` 作为共享根（`storage.root`），或在 WebUI 关闭 `wait_for_storage` 后自行承担风险。
4. 若解锁后仍 `waiting_storage`，检查 FUSE 挂载是否正常（部分 ROM 的 MediaProvider 异常会导致 `/storage` 不可用）；重启手机是常见恢复手段。
5. 多用户/工作资料：默认只考虑用户 0；其他用户路径未支持。

### 5.4 FUSE 相关限制

| 现象 | 原因 | 处理 |
|---|---|---|
| 文件已写入但相册/媒体库看不到 | 直接绕过 MediaProvider 或媒体扫描延迟 | 用 `/storage/emulated/0` 路径；触发媒体扫描/重启 MediaProvider |
| `Android/data`、`Android/obb` 访问被拒 | Android scoped storage 限制 | 不要把共享根指向这些目录 |
| 写入慢、随机 IO 慢 | FUSE 开销 | 属预期；可考虑 `/data/media/0` 回退（牺牲媒体库一致性） |
| 符号链接/硬链接/`fallocate`/`O_DIRECT` 行为异常 | FUSE 语义差异 | 未逐项验证；不要依赖这些语义 |
| 大小写/文件名冲突 | 下层文件系统与客户端差异 | 避免仅大小写不同的文件名 |
| 共享内文件属主/权限异常 | `force user=root`、FUSE 映射 | 属预期；不要依赖 UNIX 权限做隔离 |

---

## 6. 密码重置

### 6.1 WebUI 管理员口令

以 root 执行（交互式输入两次；不需要旧口令）：

```sh
su -c '/data/adb/modules/android-smb-relay/bin/smbctl webui-passwd'
```

- 新口令至少 10 字符；成功后所有 WebUI 会话被注销，需要用新口令重新登录。
- `initial-credentials.txt` 会被删除。
- 如果当前没有交互终端，请在 `su` shell 里执行，不要用管道喂两次输入（`readPassword` 每次新建 reader，管道输入可能只被第一行消费）。

### 6.2 SMB 用户口令

```sh
su -c '/data/adb/modules/android-smb-relay/bin/smbctl passwd android'
```

- 新口令至少 8 字符；用户必须已存在（先 `smbctl users` 确认）。
- 若用户不存在或 passdb 与配置不同步：在 WebUI → Users 删除该用户后重新创建（会执行 `smbpasswd -a`），或重建 passdb 后再创建。

### 6.3 初始凭据文件

- 路径：`/data/adb/android-smb-relay/config/initial-credentials.txt`（0600）。
- 读取：`su -c 'cat ...'` 或 `smbctl credentials`。
- 文件可能在修改 WebUI 口令后被删除；此时用上面的 CLI 重设。
- 极端情况下（WebUI/SMB 口令都不可用且 CLI 不可用）：停止 daemon，删除 `/data/adb/android-smb-relay/secrets.json` 后重启 daemon，`ensureSecrets` 会生成新的随机 WebUI 口令并写入新的初始凭据文件。**这会丢失旧的 WebUI 口令与会话**；SMB passdb 不受影响，但初始 SMB 口令需要另行重设。操作前先备份 `config/`。

### 6.4 Windows 缓存

```bat
net use * /delete /y
cmdkey /list
```

删除与目标 IP 相关的 Windows 凭据后重试，或 `net use \\<ip>\Shared /user:android *`。

---

## 7. 日志位置

### 7.1 文件

| 文件 | 内容 |
|---|---|
| `/data/adb/android-smb-relay/logs/asrd.log` | supervisor/WebUI/配置/认证/网络/状态机日志（默认 512 KB × 3 轮转） |
| `/data/adb/android-smb-relay/logs/smbd.log` | Samba 服务日志 |
| `/data/adb/android-smb-relay/logs/smbd-console.log` | `smbd` 的 stdout/stderr |
| `/data/adb/android-smb-relay/logs/boot.log` | `service.sh` 的启动记录（超过 256 KB 轮转 `.1`） |
| `/data/adb/android-smb-relay/logs/service.out` | `asrd` 进程的 stdout/stderr（超过 1 MB 轮转 `.1`） |
| `/data/adb/android-smb-relay/logs/watchdog.log` | 外层 watchdog 的崩溃/重启记录 |
| `/data/adb/android-smb-relay/run/watchdog.crashes` | 10 分钟窗口内的 asrd 崩溃时间戳 |
| `/data/adb/android-smb-relay/run/watchdog.disabled` | 存在表示 watchdog 被主动停止 |
| `/data/adb/android-smb-relay/samba/pid/smbd.pid` | `smbd` master PID |

### 7.2 读取方式

```sh
# CLI（root）
su -c '/data/adb/modules/android-smb-relay/bin/smbctl logs -n 100 --source asrd'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl logs -n 100 --source smbd --level warn'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl logs -n 100 --source console'

# 直接 tail
su -c 'tail -n 100 /data/adb/android-smb-relay/logs/asrd.log'
su -c 'tail -F /data/adb/android-smb-relay/logs/asrd.log'

# WebUI：Logs 页面
```

### 7.3 系统日志

```sh
su -c 'getenforce'
su -c 'dmesg | grep -i "avc:" | tail -50'
su -c 'logcat -d -b all | grep -iE "avc:|selinux|smbd|asrd" | tail -100'
```

模块管理器（KernelSU/Magisk）自身的安装日志也可以帮助定位“安装失败/脚本未执行”。

### 7.4 日志中的常见无害告警

- `db_open_ex: mutexes requested but not available` / `tdb_wrap_open: mutexes requested but not available`
- `idmap range not specified for domain '*'`
- `smbd version 4.25.1 started`（正常启动）
- `created missing share directory ...`（首次创建共享目录，正常）

---

## 8. 其他常见问题

| 问题 | 检查 |
|---|---|
| 重启后服务没起来 | `boot.log`、`service.out`、`sys.boot_completed`、模块是否被禁用、架构是否 arm64 |
| `smbd` 反复重启后 `failed` | 看 `smbd-console.log`/`smbd.log`；常见是插件目录/库缺失、端口占用、配置无效；修复后 `smbctl start` 清计数 |
| `smbctl` 报 “daemon is not running” | 先 `scripts/start.sh`；看 `service.out` 与 `asrd.log`；检查 `run/asr.sock` 是否存在 |
| `asrd` 启动即退出 | `config.json` 校验失败、`secrets.json` 损坏、数据目录无权限（应为 root 0700） |
| WebUI 状态 `failed` | 重启次数达上限；修原因后用 `smbctl start`（或 `restart`/未运行时的 `reload`）清计数 |
| `smbctl` 非 root | 必须 `su -c`；socket 只接受 uid 0 |
| 修改配置后不生效 | WebUI 保存会按需生效；直接改文件需重启 daemon；日志级别/去抖参数需重启 |
| 手机换 IP 后 WebUI 打不开 | 用 `smbctl url` 获取新地址；SMB 客户端也要用新 IP |
| 升级后异常 | 重启一次让新模块目录生效；检查 `data/lib` 是否指向新模块；看 `boot.log` |
| 模块装不上 | 架构必须 arm64；`ksud module install` 需 `sys.boot_completed=1`；看管理器日志 |
| 想临时停用 | `smbctl stop`（WebUI 与 asrd 仍在）或管理器中禁用模块后重启 |
| 只想关 WebUI | `smbctl webui-stop` 只停 HTTP 监听，SMB 继续运行；`smbctl webui-start` 恢复 |
| Wi-Fi 关闭后 SMB 停止 | 正常：进入 `waiting_network`；打开 Wi-Fi 并拿到 LAN IPv4 后自动恢复（已验证） |
| 连续 kill 后 `failed` | 达到 `watchdog_max_restarts`；修复原因后 `smbctl start` 会清计数并恢复（已验证 max=2 场景） |

### 已修复的严重问题（开发记录）

- **`smbd` monitor / Stop 并发 panic**：早期版本在停止/重启 `smbd` 时可能因关闭/等待同一个 nil channel 导致 `asrd` panic；已改为每次启动创建局部 `done` channel（`monitor(pid, done)`）。真机反复 stop/restart/kill 不再 panic。
- **Wi-Fi 关闭时误选 `vgate0` 等隧道接口**：早期自动选择可能把 VPN/隧道当成 LAN；已把 `vgate*`、`v4-*`、`clat*` 归为 `vpn`，`auto` 永不选中。关闭 Wi-Fi 后进入 `waiting_network`，重新开启后恢复（已验证）。
- **WebUI 对相同 bind/port 重复监听**：早期每次保存设置都会尝试重新绑定同一地址并记录 `address already in use`；最终版本 `Apply` 对相同配置 no-op，只有 `enabled/port/bind` 变化才重建。
- **构建 `lib/lib/` 嵌套**：早期构建 staging 时把库放进 `lib/lib/`；已修复并在 [build.md](build.md) §4.1 加入阻断校验。
- **`/storage` 未解锁时误判为空挂载点可用**：早期直接 `stat`/列目录可能把空挂载点当可用存储；已改为用 `statfs` 的 FUSE magic 判断，不可用时回退 `/data/media/0`，解锁后按 storage fingerprint 重启 `smbd`。

### 恢复建议

- 服务异常但手机正常：`sh scripts/stop.sh` 后用 `sh scripts/start.sh` 重启；或直接用 WebUI/`smbctl restart`。
- 开机异常/循环：在 KernelSU/Magisk 管理器中禁用或卸载模块，或使用安全模式（KernelSU 官方救援方式为开机画面后连按音量下 3 次进入安全模式；Magisk 连续启动失败会自动进入安全模式）。模块不修改 `/system`，禁用后删除 `/data/adb/modules/android-smb-relay` 即可恢复。
- 不要通过关闭 SELinux、删除 `/data/adb` 或格式化用户存储来“修复”问题；先备份日志与配置。
