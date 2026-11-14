# 第三方组件与 GPL 合规

本模块发布的 zip 中**包含第三方编译产物**（Samba 及其依赖库）。本文件列出组件、版本、许可证、源码来源与我们对二进制的修改，并说明 GPLv3 合规方式。

> **重要**：本文件是发布包的一部分，应与二进制一起分发。`build/build.sh` 会把 `LICENSE`、`README.md` 与本文件（`docs/third-party.md`）复制进模块 zip 的根层；发布前请用 `unzip -l` 确认。详见 §6。

## 1. 总览

| 类别 | 内容 | 许可证 |
|---|---|---|
| 本项目自有代码 | `asrd`（Go）、WebUI、模块脚本、`smbctl` | **GPL-3.0**（本仓库 [LICENSE](../LICENSE)） |
| SMB 服务端 | Samba 4.25.1（Termux aarch64 构建） | **GPL-3.0-or-later** |
| Samba 依赖库 | 见 §2，绝大多数为 LGPL/BSD/MIT/Apache 等宽松或弱 copyleft 许可 | 见下表 |
| Termux 打包工程 | 交叉编译配方、补丁、依赖包 | 见 Termux packages 上游仓库/各包 |

- 我们不修改 Samba 源码；只对已编译 ELF 中内嵌的 Termux 前缀做**二进制字符串重定位**（见 §4）。
- 所有第三方向 Samba 提供源码的版本、补丁与构建方式均可公开获取；Samba 官方源码包与 Termux 构建配方见 §3。
- 本项目与 Samba 官方、Termux 官方、Magisk/KernelSU 官方无隶属关系；“Samba”“Termux”等名称仅用于说明来源与兼容性。

## 2. 随包分发的组件

### 2.1 核心

| 组件 | 版本 | 许可证 | 上游/源码 |
|---|---|---|---|
| Samba（`smbd`、`smbpasswd`、`smbcontrol`、`testparm`、`lib/samba/**`、`libsamba-*`、`libtalloc`、`libtdb`、`libtevent`、`libwbclient` 等） | 4.25.1-1（aarch64） | **GPL-3.0-or-later** | <https://www.samba.org/> · <https://download.samba.org/pub/samba/samba-4.25.1.tar.gz> · 配方 <https://github.com/termux/termux-packages/tree/master/packages/samba> |

### 2.2 Termux aarch64 依赖包

以下版本来自构建缓存中的 `build/cache/debs/aarch64/PACKAGES.txt`（构建工作区，不随模块发布；当前 41 条，含 `zstd`、`liblzma`）。许可证列是上游项目许可证的汇总，**最终以各包随包 `copyright`/`LICENSE` 与上游声明为准**（Termux 包经常裁剪 `share/doc`，本仓库的构建产物里部分包没有版权文件）。

| 包 | 版本 | 许可证（上游） | 源码/上游 |
|---|---|---|---|
| samba | 4.25.1-1 | GPL-3.0-or-later | samba.org |
| zlib | 1.4 | Zlib | zlib.net |
| tdb-tools | 1.4.15-1 | GPL-3.0-or-later（Samba tdb 工具） | samba.org |
| libtdb | 1.4.15-1 | LGPL-3.0-or-later | samba.org |
| libbsd | 0.12.2 | BSD-3-Clause | libbsd |
| libmd | 1.3.0 | BSD-3-Clause / BSD-2-Clause | libmd |
| readline | 8.3.6 | GPL-3.0-or-later | GNU readline |
| ncurses | 6.6.20260307+really6.5.20250830 | X11 风格（MIT-like） | GNU ncurses |
| libandroid-support | 29-1 | Apache-2.0 | termux/termux-packages |
| openssl | 1:3.6.5 | Apache-2.0 | openssl.org |
| ca-certificates | 1:2026.11.05-1 | MPL-2.0（Debian 证书包惯例） | Debian ca-certificates / Mozilla CA |
| libtirpc | 1.3.8 | BSD-3-Clause | libtirpc |
| libtasn1 | 4.21.0 | LGPL-2.1-or-later | GNU libtasn1 |
| libtalloc | 2.5.0 | LGPL-3.0-or-later | samba.org |
| libpopt | 1.19-3 | MIT | rpm.org/popt |
| libandroid-glob | 0.6-3 | BSD（AOSP/BSD 风格） | termux/termux-packages |
| libicu | 79.1 | Unicode-3.0（ICU） | unicode-org/icu |
| libc++ | 30 | Apache-2.0 WITH LLVM-exception / MIT（上游双许可） | llvm.org |
| libiconv | 1.19.1 | LGPL-2.1-or-later | GNU libiconv |
| libgnutls | 3.8.13-1 | LGPL-2.1-or-later | gnutls.org |
| p11-kit | 0.26.6 | BSD-3-Clause | p11-kit |
| libffi | 3.8.0 | MIT | sourceware.org/libffi |
| libunistring | 1.5 | LGPL-3.0-or-later | GNU libunistring |
| libunbound | 1.26.2 | BSD-3-Clause | NLnet Labs Unbound |
| resolv-conf | 1.3 | 配置包装（见 Termux 包） | termux/termux-packages |
| libngtcp2 | 1.26.0 | MIT | ngtcp2 |
| libnghttp2 | 1.71.0 | MIT | nghttp2 |
| libevent | 2.1.13 | BSD-3-Clause | libevent |
| libidn2 | 2.3.8-1 | LGPL-3.0-or-later / GPL-2.0-or-later | GNU libidn2 |
| libnettle | 4.0+really3.10.2 | LGPL-3.0-or-later / GPL-2.0-or-later | nettle |
| libgmp | 6.3.0-2 | LGPL-3.0-or-later / GPL-2.0-or-later | gmplib.org |
| libcrypt | 0.2-6（crypt 兼容库） | BSD-2-Clause（随包 copyright 为 BSD 二条款） | Termux libcrypt |
| libcap | 2.78 | BSD-3-Clause OR GPL-2.0-only | kernel.org/libcap |
| attr | 2.6.0 | LGPL-2.1-or-later | GNU attr |
| libandroid-spawn | 0.3 | BSD（AOSP） | termux/termux-packages |
| libandroid-execinfo | 0.1-3 | BSD（AOSP） | termux/termux-packages |
| krb5 | 1.22.3 | MIT | MIT Kerberos |
| libdb | 18.1.40-6 | Sleepycat License | Oracle Berkeley DB |
| libresolv-wrapper | 1.1.8 | BSD-3-Clause | cwrap |
| liblzma | 5.8.4 | 0BSD（liblzma；工具部分另有条款） | tukaani.org/xz |
| zstd | 1.5.7-1 | BSD-3-Clause OR GPL-2.0 | facebook/zstd |

说明：

- 表中 `or later` 表示“该版本或更高版本”；具体以各项目 `COPYING`/`LICENSE` 原文为准。
- 随包实际包含的是上表的一个**子集**（由 `bundle.py` 的 NEEDED 闭包决定），并非每个包的所有文件都会进入模块。`0.1.0` 的 `lib/` 根目录有 117 个库、`lib/samba/` 有 153 个插件库。
- Android 系统库 `libc.so`、`libdl.so`、`libm.so` 由设备提供，不随包分发（`MISSING.txt` 中仅允许这三项）。
- GPL/LGPL 组件的源码通过其上游项目与 Termux 构建配方提供；宽松许可证组件的许可证文本随上游源码提供。

## 3. 源码与构建来源（Samba / Termux）

### 3.1 Samba

| 项 | 值 |
|---|---|
| 版本 | 4.25.1（Termux 包版本 4.25.1-1） |
| 官方源码 | <https://download.samba.org/pub/samba/samba-4.25.1.tar.gz> |
| 源码 SHA-256 | `7f367545bca69aa96897628f82f0f04d4b66b3bcb36cb978b9bb952c4b4c428d` |
| Termux 构建配方 | <https://github.com/termux/termux-packages/tree/master/packages/samba>（`build.sh`、`cross-answers.txt`、`smb.conf.example` 等） |
| Termux 包索引 | <https://packages.termux.dev/apt/termux-main/> |
| 本模块使用的 deb | `samba_4.25.1-1_aarch64.deb` |
| deb SHA-256 | `e77bb94e7fd7de75f271a41c546a8c245891bcc5717a1db92d322ff79d27aa8f` |

Termux 的 Samba 配方关键点（用于复现）：`--enable-fhs`、`--without-ad-dc --without-ads --without-ldap --without-pam --without-winbind`、`--without-acl-support --without-quotas --without-systemd`、`--with-shared-modules=...pdb_tdbsam,pdb_smbpasswd...`、`CPPFLAGS=-D_FILE_OFFSET_BITS=64`、`LDFLAGS=-landroid-spawn`、`cross-answers.txt` 指定 `setreuid/setresuid/seteuid/setuidx: NO` 等。完整的构建细节见 `docs/research/samba.md`（仓库内调研文档）与上游配方。

### 3.2 依赖库

每个依赖的来源与版本可以从 `build/cache/debs/aarch64/PACKAGES.txt` 的 `filename` 字段推导，例如：

```text
libgnutls	3.8.13-1	libgnutls_3.8.13-1_aarch64.deb	eb12f2cf...
```

对应下载 URL 为 `https://packages.termux.dev/apt/termux-main/pool/main/libg/libgnutls/libgnutls_3.8.13-1_aarch64.deb`；上游源码项目见上表。构建时使用 `build/cache/debs/aarch64/PACKAGES.txt` 中的 SHA-256 校验（见 [build.md](build.md) §5）。

## 4. 我们对二进制的修改

### 4.1 修改内容：Termux 前缀重定位

Termux 在编译期把 `/data/data/com.termux/files/usr` 写入 Samba 二进制的多个字符串常量（`RUNPATH`、`CONFIGFILE`、`MODULESDIR`、`private/lock/state/cache/pid` 等）。本模块安装到 `/data/adb`，必须把这些路径改到稳定数据目录。

- 工具：`build/patch-prefix.py`（构建期，不随设备运行）。
- 规则：
  - 旧前缀：`/data/data/com.termux/files/usr`（31 字节）
  - 新前缀：`/data/adb/android-smb-relay`（28 字节）
  - 对每个 ELF 文件，找到包含旧前缀的 C 字符串，整串替换；新前缀更短，剩余字节用 `\0` 填充，字符串长度不变，ELF 布局不变。
- 范围：`bin/{smbd,smbpasswd,smbcontrol,testparm}` 与所有随包库（包括 `lib/samba/**` 插件）中实际包含该前缀的 ELF。
- 未修改：Samba 源码、编译选项、协议实现、插件逻辑；未改 `INTERP`（仍为 `/system/bin/linker64`）；未加壳/加密。
- 结果路径（编译期常量被重写后）：
  - `RUNPATH` → `/data/adb/android-smb-relay/lib`（及 `lib/samba`）
  - `CONFIGFILE` → `/data/adb/android-smb-relay/etc/samba/smb.conf`（实际运行由 `smbd -s <生成的 smb.conf>` 覆盖）
  - `MODULESDIR` → `/data/adb/android-smb-relay/lib/samba`
  - `private/lock/state/cache/pid` → `/data/adb/android-smb-relay/samba/...`
- 因此安装后依赖 `data/lib -> module/lib` 的符号链接（由 `post-fs-data.sh`/`customize.sh`/`asrd` 创建）：Samba 从 `/data/adb/android-smb-relay/lib/samba` 加载 `pdb/tdbsam.so`、vfs 插件等。

### 4.2 其他随包改动

| 改动 | 说明 |
|---|---|
| 依赖裁剪 | 只打包 `smbd/smbpasswd/smbcontrol/testparm` 的 NEEDED 闭包 + `lib/samba` 插件，避免携带完整的 Termux 用户空间 |
| 权限/换行 | `build/build.sh` 设置可执行位、统一 LF；不修改二进制内容 |
| 新增 | `asrd`、`smbctl`、模块脚本、`webui/`、`config/` 是本项目新增，不属于 Samba/Termux |
| 未修改 | Samba 源代码、Termux 的包签名/元数据、Android 系统库 |

### 4.3 为什么不做源码级改动

- Termux 的 Samba 构建依赖其交叉编译补丁（`libandroid-spawn`、`cross-answers.txt` 等）；从零重建的成本高、风险大。
- 仅需重定位编译期路径，二进制字符串替换已足够且可验证（`strings`/`readelf`）。
- GPL 合规不要求“必须改源码”；分发修改后的二进制时，需要提供对应源码与修改说明，本项目通过公开上游源码 + 构建配方 + `build/patch-prefix.py` 满足。

## 5. 许可证文本与归属

- **本项目**：GNU GPL-3.0，完整文本见 [LICENSE](../LICENSE)（674 行，来自 GNU 官方 GPL-3.0 文本，与 Debian `common-licenses/GPL-3` 一致）。
- **Samba**：GPL-3.0-or-later；上游 `COPYING` 见 <https://github.com/samba-team/samba/blob/master/COPYING>。Samba 的部分库（talloc/tdb/tevent 等）为 LGPL-3.0-or-later，具体见其源码目录的 `COPYING`。
- **其他依赖**：各自许可证见上表与上游项目；Termux 包可能裁剪了 `share/doc` 内的版权文件，因此发布时应同时提供本清单与各项目许可证链接。
- **Android 系统库**：`libc.so`、`libm.so`、`libdl.so`、`/system/bin/linker64` 由设备系统提供，不属于本模块分发内容。
- 各名称与商标归其各自所有者；本项目不声称与上述项目存在官方关联。

## 6. GPLv3 合规说明

Samba 以 **GPL-3.0-or-later** 分发。本模块分发的是 Samba 的**修改后二进制**（前缀重定位）以及若干依赖库，因此：

1. **提供对应源码**
   - Samba 4.25.1 官方源码：<https://download.samba.org/pub/samba/samba-4.25.1.tar.gz>（SHA-256 见 §3.1）。
   - Termux 的构建配方与补丁：<https://github.com/termux/termux-packages/tree/master/packages/samba>。
   - 我们的修改与构建脚本：`build/patch-prefix.py`、`build/bundle.py`、`build/build.sh`、`build/fetch-termux.py`、`build/zip.py`（`.work/` 下的早期脚本已被 `build/` 取代），以及本文件与 [build.md](build.md)。
   - 依赖库源码：见 §2.2 的上游项目与 Termux 包索引。
2. **说明修改**
   - 修改内容仅限 §4 的 ELF 字符串前缀替换；没有修改 Samba 逻辑。
   - 修改由本项目维护者在构建期执行，日期与新版本发布同步。
3. **许可文本**
   - `LICENSE` 含 GPL-3.0 全文；`build/build.sh` 会把 `LICENSE`、`README.md`、`docs/third-party.md` 复制进模块 zip 根层。
   - 发布前用 `unzip -l` 确认三者存在；当前重建的 `0.1.0` 包已包含它们。
   - 各第三方依赖的许可证文本/链接见 §2 表格；如有需要，可在 release 附件中补充完整许可证集合。
4. **动态链接与 LGPL**
   - LGPL 库以动态链接（`.so`）形式随包分发；源码可获取；用户/下游可以在 root 权限下替换 `lib/` 中的库并重新运行（模块不锁定签名/不校验库哈希），为 LGPL 的重新链接提供了条件。
   - GPL 的 Samba 二进制与 LGPL 库的组合按 GPL/LGPL 兼容方式分发；本项目的 `asrd` 是独立进程（通过文件/进程控制交互，不链接 Samba 库），但本项目整体仍选择 GPL-3.0 以简化合规。
5. **无担保**
   - 按 GPL-3.0 第 15/16 条，二进制与源码均不附带任何担保。
6. **源码获取方式**
   - 上游 URL 公开可访问；本项目的构建脚本与说明随仓库发布。若你需要某一版本的完整构建输入（deb 哈希清单、Go 工具链版本、zip 内容清单），请从发布页/仓库获取对应版本的 `PACKAGES.txt` 与构建记录。
   - 建议每个 release 保存：`PACKAGES.txt`、`sha256sum` 清单、Go 版本、`build/patch-prefix.py` 输出、zip 的 `unzip -l` 清单，保存期至少覆盖该版本的整个分发周期。

### 6.1 发布检查清单（GPL）

- [ ] zip 根目录包含 `LICENSE`（GPL-3.0 全文）。
- [ ] zip 或 release 附件包含 `THIRD_PARTY_NOTICES` / `docs/third-party.md`。
- [ ] 发布说明写明 Samba 版本、源码 URL、SHA-256、Termux 包清单与哈希。
- [ ] 说明二进制修改（prefix patch）与获取修改脚本的方式。
- [ ] 不删除、不隐藏源码链接；不添加额外限制（GPL 不允许对下游施加更严格条款）。
- [ ] 若分发预编译 deb 的再打包版本，保留其许可证与版权声明。

## 7. 版本与哈希（构建记录）

| 组件 | 版本 | SHA-256（deb/源） |
|---|---|---|
| samba | 4.25.1-1 | `e77bb94e7fd7de75f271a41c546a8c245891bcc5717a1db92d322ff79d27aa8f` |
| samba 源码 | 4.25.1 | `7f367545bca69aa96897628f82f0f04d4b66b3bcb36cb978b9bb952c4b4c428d` |
| zstd | 1.5.7-1 | `e1b4a5113648da8de189620ba1fce74c48b2d0833d9043391b9a1c91fb606fd3` |
| liblzma | 5.8.4 | `44e95e6e60dddb3705e60a344a4f009a1085a210797342b721eaff41b44033c0` |
| libgnutls | 3.8.13-1 | `eb12f2cf82923caf288edd99bc4c19a9dd0cf1de4a03855ff4457a1bbdae146d` |
| openssl | 1:3.6.5 | `a2fb7856875e63ef6b90442752e05974813372c3ca86d2513a78ea43c67a0f8b` |
| krb5 | 1.22.3 | `2f8b1831df2d00f821d71b44da2babada49097bc71a1ffcd25bcdbeff466564a` |
| libicu | 79.1 | `83c980a010e957738e3567127801d66a1b9bd7e404a4fda98788048ee57d606c` |
| readline | 8.3.6 | `616cc73009bc0223f61caed8dd09455714455aa5ea5e180126e2f69c4c775136` |
| zlib | 1.4 | `e3027565ae50bcd8ba5113c988b589754ae192c9cc3e6afbe3e4ed7c06b2598e` |

完整清单见 `build/cache/debs/aarch64/PACKAGES.txt`（当前 **41 行**，与缓存中的 41 个 `.deb` 一一对应；`zstd`、`liblzma` 已包含在锁内）。`verify-lock.py` 已通过；重建后仍应确认锁覆盖每个被成包的文件，参见 [build.md](build.md) §5。

## 8. 安全与完整性

- 模块 zip、Samba 二进制、依赖库均未做数字签名；请从可信发布渠道获取，并核对发布方提供的 SHA-256。
- Termux 包只通过 SHA-256 校验（未使用 apt 签名）；镜像/代理存在被替换风险，构建时必须逐包校验（见 [build.md](build.md) §5）。
- 设备上模块目录/数据目录仅 root 可写；升级会整体替换模块目录。若设备已 root，任何本地 root 进程都能替换 `smbd`/库——这是 root 模块的固有信任边界。
- 发现可疑二进制时：比对 zip 的 SHA-256、检查 `strings` 中的 Termux 前缀是否已清除、用 `readelf -d` 检查 `RUNPATH`、核对本文件版本与哈希。
