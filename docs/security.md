# 安全说明

> **重要：0.1.0 的默认配置是匿名、可写、整盘共享。**  
> 这是按项目使用者要求提供的“局域网直接填写 IP 就能连接”模式，不是安全默认值。  
> 只要手机和客户端在同一局域网，任何人不输入账号密码即可读写 `/storage/emulated/0`。  
> WebUI 仍然需要管理员密码；WebUI 与 SMB 是两套独立入口。  
> **请只在可信家庭/隔离局域网使用。** 不要把 445 或 WebUI 暴露到公网。

## 1. 默认暴露面

| 项目 | 默认值 | 风险 |
| --- | --- | --- |
| SMB enabled | true | 445 对局域网开放 |
| SMB min protocol | SMB2_02 | SMB1 关闭 |
| SMB signing | default（不强制） | 不强制签名；客户端可协商签名，未签名流量仍可被篡改/重放 |
| SMB encryption | off | 局域网可嗅探文件内容 |
| Guest access | true | 无需 SMB 账号 |
| Allow anonymous | true | 允许匿名会话 |
| Shares | `Internal` → `/storage/emulated/0` | 整盘可读写，包括照片/文档/应用数据 |
| WebUI auth | false | 按使用者要求关闭登录认证，局域网直接访问 |
| WebUI bind | 0.0.0.0 | 默认所有接口，但 LAN-only 过滤公网来源 |

## 2. 为什么这样设计

使用者明确要求：

- 手机端共享开放匿名；
- 不做 SMB 账户密码；
- 其他客户端在局域网直接填 `192.168.1.XXX` 连接；
- 默认共享整个内部存储。

因此默认 `guest_access=true`、`allow_anonymous=true`、共享 `guest_ok=true`，并且不创建默认 SMB 用户。  
这不是“安全最佳实践”，而是“可信 LAN 内零配置”的取舍；安全模式仍然可以在 WebUI 中恢复（见 §6）。

## 3. 仍然保留的安全控制

- **SMB1 关闭**：`server min protocol = SMB2_02`。
- **签名默认不强制**：`server signing = default`，按使用者要求关闭强制签名；未签名流量降低了对中间人篡改/重放的防护，需要时在 Settings 改回“强制”。
- **WebUI 认证默认关闭**：按使用者要求局域网内直接访问；重新开启时仍使用 PBKDF2 会话/CSRF。
- **LAN-only**：WebUI 拒绝非私网来源；不依赖公网。
- **路径白名单**：共享路径必须位于允许的存储根内；拒绝 `..`、控制字符、`/data`、`/system` 等敏感目录；存在路径会解析符号链接并检查逃逸。
- **无 shell 拼接**：API 不把用户输入拼成 shell；Samba 用户名/共享名有严格正则；密码通过 stdin 传给 `smbpasswd`，不进入 argv/日志。
- **日志脱敏**：`password/passwd/secret/token` 形式的键值会被替换。
- **SELinux**：不关闭、不执行 `setenforce 0`、不注入 sepolicy.rule；在 `u:r:ksu:s0` 下运行。
- **卸载不删用户数据**：`uninstall.sh` 只清理 PID/socket/临时文件，绝不删除 `/storage/emulated/0`。

## 4. Windows 匿名访问的特殊说明

Windows 10/11 默认阻止“不安全的来宾登录”。要让 `\\<手机IP>\Internal` 免密码工作，需要在这台 PC 上以管理员运行：

```powershell
powershell -ExecutionPolicy Bypass -File .\tools\windows-allow-guest.ps1
```

它设置：

```text
HKLM\SYSTEM\CurrentControlSet\Services\LanmanWorkstation\Parameters
  AllowInsecureGuestAuth = 1
```

这是 **PC 全局设置**：开启后，这台 PC 也会接受其他 SMB 服务器的 guest 访问。不需要时用 `-Disable` 关闭。

## 5. 其他风险

- **整盘共享**：`Internal` 指向 `/storage/emulated/0`，包含 `Android/data`、应用缓存、照片、下载、文档等；任何 LAN 用户可读写/删除。
- **Force user = root**：Samba 以 root 身份读写共享；共享级 `read only` 是唯一的写保护，没有 per-user/per-share ACL。
- **SMB 加密关闭**：文件内容在局域网明文传输；签名不等于加密。
- **热点/VPN**：手机开启热点时，连接热点的设备可能进入同一 LAN；不要把热点给不可信设备。
- **WebUI HTTP**：局域网内 HTTP 明文；admin 密码和会话可能被同网段嗅探。不要使用公网/不可信 Wi-Fi。
- **无 SMB 账号**：不存在“按用户隔离”；所有客户端看到同一份整盘内容。

## 6. 加固步骤（推荐在不可信环境使用）

1. WebUI → Settings：关闭 `Guest access` 与 `Allow anonymous`。
2. WebUI → Users：创建 SMB 用户（至少 10 位密码）。
3. WebUI → Shares：把 `Internal` 改成只读，或删除它，只共享专门目录（如 `/storage/emulated/0/SMB/Shared`）。
4. WebUI → Settings：绑定到具体 LAN IP 或保持 `0.0.0.0` + `LAN only`；不要公网端口转发。
5. 如不需要 Windows 匿名，运行 `windows-allow-guest.ps1 -Disable`。
6. 不使用 SMB 时在 Dashboard 点 Stop。
7. 只从可信网络访问 WebUI；不要使用公共 Wi-Fi。

## 7. 日志与凭据

- WebUI 初始密码：`/data/adb/android-smb-relay/config/initial-credentials.txt`（0600，root-only）。
- WebUI 密码只存 PBKDF2 哈希；修改后初始文件被删除。
- 默认没有 SMB 账号，因此没有 SMB 密码文件；如果以后创建用户，密码只存 Samba `tdbsam`。
- 日志不记录密码；`smbctl logs` 可查看 asrd/smbd 日志。

## 8. 事件响应

如果发现异常访问：

1. Dashboard 立即 Stop SMB，或 `smbctl stop`。
2. 查看 WebUI Logs、`/data/adb/android-smb-relay/logs/asrd.log`、`log.smbd`。
3. 检查是否有未知 SMB 用户：`smbctl users`。
4. 关闭匿名（Settings），创建新账号，修改 WebUI 密码（Settings）。
5. 如怀疑凭据泄露，运行 `smbctl webui-passwd`，并重启设备。
6. 收集 `dmesg | grep avc`、`logcat`，不要关闭 SELinux。

## 9. 已知限制

- 无 per-user 文件 ACL。
- WebUI 仅 HTTP（无 HTTPS）。
- 匿名模式没有审计到具体客户端身份的可靠机制（连接数来自 `/proc/net/tcp`）。
- 其他 ROM/OEM 的 SELinux 策略和 FUSE 行为可能不同；见 [android-compatibility.md](android-compatibility.md)。
