# Samba / smbd 技术调研（Termux 4.25.1 · Android 15–17 · arm64 root 模块）

- **调研日期**：2026-11-14
- **方法**：四类一手证据交叉核对，**没有**在本机做任何"编译成功/运行成功"的声称：
  1. **本地静态取证**：用 WSL `dpkg-deb -x` 解包 Termux 官方 `samba_4.25.1-1_aarch64.deb`，对 `bin/smbd`、`bin/smbpasswd`、`bin/smbcontrol`、`lib/libsamba-*.so`、`lib/samba/**` 做 `readelf -d`（RUNPATH/NEEDED）、`readelf -W -l`（INTERP、LOAD 对齐）、`strings -a`（编译期路径字面量）。
  2. **上游 man page**：`samba.org` 的 `smb.conf(5)`、`smbd(8)`、`smbpasswd(8)`、`pdbedit(8)`、`smbcontrol(1)`、`testparm(1)`（current 版），并与本仓库 `.work/man/` 下同一份 `Samba 4.25.1-Debian-4.25.1+dfsg-1` 的 man 文本逐条比对。
  3. **打包配方原文**：Termux `packages/samba/build.sh`（4.25.1，revision 1）。
  4. **Google 先例**：`google/samba-documents-provider` 的 README 与 `sambapatch.diff` 的改动文件清单。
- **项目实测（仅引用，不扩展）**：Termux Samba 4.25.1 aarch64 二进制在 OnePlus PJA110 / Android 16 / KernelSU 上可执行 `smbd --version`；设备 SELinux 为 Enforcing，su 上下文 `u:r:ksu:s0`；`smbpasswd` 首次运行报 `No builtin nor plugin backend for tdbsam found`。
- **可信度**：`[已确认]` = 上游文档原文或本次静态取证可直接复现；`[推断/待验证]` = 由证据推导但未上机，写进实现前必须验证。
- **不覆盖**：SMB 协议线格式细节、Windows 客户端兼容矩阵、AD/DC 与 winbind（Termux 配方已 `--without-ad-dc --without-winbind`，本项目也不用）。SELinux 侧见 `docs/research/selinux.md`。

---

## 1. 结论摘要（先看这 10 条）

1. **Termux 的 `smbd` 是 bionic 动态二进制，且路径是"编译期写死"的**：`INTERP=/system/bin/linker64`，`RUNPATH` 里硬编码了四个 `/data/data/com.termux/files/usr/...` 路径。把它当成"复制过去就能跑"的静态程序是错的。`[已确认]`
2. 写死的还有 `CONFIGFILE`（`$PREFIX/etc/samba/smb.conf`）、`MODULESDIR`（`$PREFIX/lib/samba`）以及 `private/lock/state/cache/run/log` 全套 `$PREFIX/var/...` 目录。**能改的只有 `smb.conf` 里的目录类参数**，二进制里的字面量改不了。`[已确认]`
3. `tdbsam` 是**共享插件**（`lib/samba/pdb/tdbsam.so`），不是内建后端——这直接解释了项目实测的 `No builtin nor plugin backend for tdbsam found`：Samba 在 `MODULESDIR/pdb/` 找不到 `.so`。`[已确认]`
4. 该 aarch64 二进制的 `LOAD` 段对齐是 **`0x4000`（16 KB）**，因此可被 16 KB page size 内核加载；**自己用 NDK 默认参数编出来的 4 KB 对齐产物在 16 KB 设备上装不上**。`[已确认]`
5. `server min protocol` 默认已是 `SMB2_02`，即 SMB1 默认关闭；显式写同一行即可，**绝不要**写 `NT1`/`LANMAN1`。`[已确认]`
6. **SMB2/3 的签名关不掉**：`server signing = disabled` 在 SMB2 协商下被当作 `auto` 处理（man 原文），所以"要么默认、要么 `mandatory`"，不存在"关掉签名换性能"的自由。`[已确认]`
7. Termux 的 `cross-answers.txt` 把 `setreuid`/`setresuid`/`seteuid`/`setuidx` 全部标成 `NO`——**smbd 能否把连接切换到对应 UNIX 身份是本项目最大的未验证风险**，直接影响"多用户/只读共享"的设计。`[已确认·配方]` + `[推断]`
8. `smbpasswd -a` 的 man 原文要求"用户必须已存在于系统密码文件"，而 Android 没有 `useradd` 语义 → 现实路径是单用户 + `force user`，不要设计"多 SMB 用户各自映射不同 UNIX 用户"的方案。**但本项目在 OnePlus PJA110 / Android 16 上实测：配置 `force user = root` 后，`smbpasswd -s -a -c smb.conf testuser` 成功创建了并不存在于 `/etc/passwd` 的 SMB 用户，`smbd` 认证日志显示用户被映射为 `root` 并成功登录。** 因此"非 Unix 用户 + force user"可用；多用户各自映射不同 Unix UID 仍不可假设。`[已确认·man]` + `[项目实测：非 Unix 用户可创建/认证]`
9. 共享 `/storage/emulated/0` 实际走 MediaProvider 的 FUSE；AOSP 官方文档明确 FUSE 有性能代价（随机读写可达 2× 慢），这是吞吐天花板的主要来源之一。`[已确认·AOSP 文档]`
10. **自建 smbd 没有任何公开先例可抄**：Termux 配方是"给整个 Samba 打 bionic 适配"的产物，Google 的 `samba-documents-provider` 只编了 `libsmbclient.so`（客户端库），不编服务端。`[已确认·README]` + `[推断]`

---

## 2. Termux Samba 4.25.1 包事实

### 2.1 版本与内容 `[已确认]`

`dpkg-deb -I` 与解包结果：

- `Package: samba`、`Version: 4.25.1-1`、`Architecture: aarch64`、`Installed-Size: 29164`、`Maintainer: @termux`。
- `bin/` 同时提供客户端与服务端：`smbd`、`nmbd`、`smbpasswd`、`pdbedit`、`smbcontrol`、`testparm`、`smbclient`、`smbstatus`、`net`、`rpcclient`、`wbinfo` 等；**没有 `sbin/`**（配方显式 `--sbindir=$TERMUX_PREFIX/bin`）。
- **不随包提供 `smb.conf`，连 `$PREFIX/etc/samba/` 目录都没有**；只给 `share/doc/samba/smb.conf.example`（由 Termux 的 `smb.conf.example.in` 经 `sed` 替换 `@TERMUX_PREFIX@`/`@TERMUX_HOME@` 生成，落盘版本里已是真实 `$PREFIX` 路径）与 `share/man/man5/smb.conf.5.gz`。
- `lib/samba/` 下是插件目录：`pdb/`、`vfs/`、`idmap/`、`ldb/`、`krb5/`、`nss_info/`，另有上百个 `*-private-samba.so` 内部库；`lib/` 根下是 `libsmbconf.so.0`、`libsamba-passdb.so.0` 等公共库。
- 归档内 `usr/`、`usr/bin/` 目录与 `bin/*` 二进制权限为 `0700`（`tar -tvf` 所见），归档外由 Termux 安装器决定实际落盘权限。**拷贝这些文件时必须自己 `chmod`，不要假设 0755。**

### 2.2 依赖 `[已确认]`

`control` 的 `Depends` 与配方 `TERMUX_PKG_DEPENDS` 完全一致：

```
krb5, libandroid-execinfo, libandroid-spawn, libbsd, libcap, libcrypt, libgnutls,
libiconv, libicu, libpopt, libtalloc, libtasn1, libtirpc, ncurses, openssl,
readline, tdb-tools, zlib
```

要点：

- **`libandroid-spawn` / `libandroid-execinfo` 是 Android 专属补丁库**：bionic 没有 `<execinfo.h>`（backtrace）与完整 `posix_spawn` 语义，Samba 需要它们才能链接。`[已确认·配方 LDFLAGS += -landroid-spawn]`
- **`tdb-tools` 是独立包**：配方用 `TERMUX_PKG_RM_AFTER_INSTALL` 把 samba 自带的 `tdbbackup/tdbdump/tdbrestore/tdbtool` 与 `man` 删掉，改由 `tdb-tools` 提供 → 运行时要用 `tdbdump` 检查 `passdb.tdb` 就得额外装它。`[已确认]`
- 传递依赖量很大（`.work/debs/aarch64/PACKAGES.txt` 共 39 个包含 `p11-kit`、`libunbound`、`libicu` 等）；**`libicu` 单包约 10 MB**，是模块体积的主要来源。`[已确认]`
- **`libcap` 在依赖里**，但 Android 上 capability 管理依赖内核 + SELinux，不等于"有了 libcap 就能降权"。`[推断/待验证]`

### 2.3 Termux 的 waf 编译配置（决定路径与能力）`[已确认]`

`build.sh` 的 `termux_step_configure` 关键项：

```
./buildtools/bin/waf configure --cross-compile --cross-answers=cross-answers.txt \
  --enable-fhs --prefix="$TERMUX_PREFIX" --sysconfdir="$TERMUX_PREFIX/etc" \
  --localstatedir="$TERMUX_PREFIX/var" --sbindir="$TERMUX_PREFIX/bin" \
  --disable-rpath --disable-rpath-install \
  --without-ad-dc --without-ads --without-ldap --without-pam --without-winbind \
  --without-acl-support --without-quotas --without-utmp --without-systemd \
  --without-regedit --without-json --without-libarchive --without-gpgme \
  --with-system-mitkrb5 --disable-avahi --disable-cups --disable-glusterfs \
  --disable-iprint --disable-python --nopyc --nopyo --disable-spotlight \
  --with-shared-modules="<vfs 列表>,pdb_tdbsam,pdb_smbpasswd,pdb_wbc_sam,<auth 列表>" \
  --with-static-modules='!auth_winbind' \
  --bundled-libraries='!asn1_compile,!compile_et'
```

以及 `termux_step_pre_configure`：

- `CPPFLAGS += -D_FILE_OFFSET_BITS=64`（Android 上 `off_t` 默认 32 位，必须显式开大文件支持）
- `LDFLAGS += -landroid-spawn`；`PYTHONHASHSEED=1`
- 交叉编译靠 `cross-answers.txt` 供答案，包括 `-Wl,--version-script support: NO`、`strptime: NO`、`strtoll: NO`、`Linux thread-specific credentials: NO`、**`setreuid/setresuid/seteuid/setuidx: NO`**、`fcntl hints: NO`、`gnutls fips: NO`。
- `termux_step_post_massage` 把 `var/lock/samba`、`var/lib/samba`、`var/cache/samba` `chmod 755`，注释写明"**700 会让 testparm 报警告**"。
- 源码用 `https://download.samba.org/pub/samba/samba-4.25.1.tar.gz`，`SHA256=7f367545bca69aa96897628f82f0f04d4b66b3bcb36cb978b9bb952c4b4c428d`（若要复刻，先校验这个值）。

> 对本项目的直接含义：**`setuid` 能力被配方"告知为不可用"**。Samba 会据此编掉/绕过依赖 `seteuid` 的用户切换路径。真实设备上 bionic 是否提供、SELinux 是否允许是另一回事——这正是必须上机验证的第 1 号实验。`[推断/待验证]`

---

## 3. 编译期路径、RUNPATH 与装载器（迁移的真正难点）

### 3.1 静态取证结果 `[已确认]`

`readelf` / `strings` 实测（aarch64 deb）：

| 项 | 实测值 |
|---|---|
| ELF INTERP | `/system/bin/linker64`（Android 动态链接器，非 glibc 的 `ld-linux`） |
| `smbd` RUNPATH | `/data/data/com.termux/files/usr/lib:/data/data/com.termux/files/usr/lib/samba:/data/data/com.termux/files/usr/lib:/data/data/com.termux/files/usr/lib`（**4 段，全部写死**） |
| `LOAD` 段 Align | `0x4000`（16 KB），三个 `LOAD` 全一致 |
| `smbd` 直接 `NEEDED` | 28 个（`libsmbd-base-private-samba.so`、`libsamba-passdb.so.0`、`libsmbconf.so.0`、`libpopt.so`、`libtalloc.so.2`、`libbsd.so`、`libc.so` …） |
| 整棵 `lib` 树 `NEEDED` 去重 | 约 117 个库名（含近百个 `*-private-samba.so`） |
| 内嵌 `CONFIGFILE` 字面量 | `/data/data/com.termux/files/usr/etc/samba/smb.conf`（`libsamba-util.so.0.0.1`，另有同目录 `lmhosts`） |
| 内嵌目录字面量 | `.../usr/var/lib/samba`（+`/private`、`/private/smbpasswd`、`/bind-dns`、`/ntp_signd`）、`.../usr/var/lock/samba`、`.../usr/var/run/samba`（+`/ncalrpc`、`/nmbd`、`/winbindd`）、`.../usr/var/cache/samba`、`.../usr/var/log/samba` |
| 插件装载实现 | `lib/samba/libsamba-modules-private-samba.so` 内含 `modules_path`、`load_module`、`smb_probe_module`、`opendir/readdir/dlopen` 符号 |

> 说明：配方里明明传了 `--disable-rpath --disable-rpath-install`，产物里却有 RUNPATH——推断是 Termux 工具链在链接期统一注入的 `$TERMUX_PREFIX/lib` rpath，而非 Samba 自身 rpath 逻辑。`[推断/待验证]`

### 3.2 三种"让它在模块里跑"的路线（按风险从低到高）

**A. 依赖宿主 Termux 安装的 Samba（推荐先做）** `[推断/待验证]`

- 模块只做"调用方"：检测 `$PREFIX/bin/smbd`、校验版本、用宿主二进制启动。
- 优点：RUNPATH、`MODULESDIR`、`CONFIGFILE`、`var` 目录全部自洽，无需任何二进制改造；升级也由 `apt` 负责。
- 代价：用户必须先装 Termux 与 `samba`；版本漂移（用户 `apt upgrade` 后行为变化）；`/data/data/com.termux/...` 是 Termux 的私有目录，root 进程访问它需要 SELinux 允许（见 `selinux.md`）。

**B. 自带二进制但保留 `$PREFIX` 绝对布局** `[推断/待验证]`

- 把 deb 解包后的 `data/data/com.termux/files/usr/**` 原样搬到真实 `/data/data/com.termux/files/usr`（或把模块的 rootfs 用 bind mount 贴到该路径）。
- 优点：RUNPATH 与 `MODULESDIR` 字面量全部命中。
- 代价：**与 Termux 包体系直接冲突**（互相覆盖文件、apt 会覆盖你的补丁版）；卸载残留难清理；`/data/data/com.termux` 由 Termux app UID 拥有，root 目录权限/SELinux 都要额外处理。**不建议**作为默认方案。

**C. `patchelf` 改 RUNPATH + 全参数覆盖（不推荐当主路线）** `[推断/待验证]`

- `RUNPATH` 可以用 `patchelf --set-rpath` 改，这一步没问题。
- 但 `MODULESDIR`、`CONFIGFILE` 是**普通字符串常量**，不是动态段字段；`patchelf` 改不了。等长字符串替换有极小概率成功，跨目录长度不同就必须重编译。
- `LD_LIBRARY_PATH` 只能救共享库，救不了插件目录；`smbd -b`（`--build-options`）能打印这些编译期值，可用来在设备上核对。`[已确认·smbd(8)]`
- 结论：除非愿意维护"二进制补丁 + 每次升级重打"，否则不要选 C。

---

## 4. 目录、`smb.conf` 生成与 tdbsam 插件加载

### 4.1 默认目录（man 文档 vs 本包实测）

| 参数 | man page 默认 | 本包实际（`$PREFIX` = `/data/data/com.termux/files/usr`） | 证据 |
|---|---|---|---|
| `config file` | 编译期决定 | `$PREFIX/etc/samba/smb.conf` | `[已确认·strings]` |
| `private dir` | `/var/lib/samba/private` | `$PREFIX/var/lib/samba/private`（`passdb.tdb`、`secrets.tdb`、`smbpasswd` 在此） | `[已确认·strings]` |
| `state directory` | `/var/lib/samba` | `$PREFIX/var/lib/samba`（含 `bind-dns`、`ntp_signd`） | `[已确认]` |
| `lock directory` | `/run/samba` | `$PREFIX/var/lock/samba` | `[已确认]` |
| `pid directory` | `/run/samba` | `$PREFIX/var/run/samba` | `[已确认]` |
| `ncalrpc dir` | `/run/samba/ncalrpc` | `$PREFIX/var/run/samba/ncalrpc` | `[已确认]` |
| `cache directory` | `/var/cache/samba` | `$PREFIX/var/cache/samba` | `[已确认]` |
| 日志目录 | `log file` 无默认 | `$PREFIX/var/log/samba`（示例配置再用 `log.%m` 细化） | `[已确认]` |
| `modules dir` | **不是 `smb.conf` 参数** | `$PREFIX/lib/samba`（编译期 `MODULESDIR`） | `[已确认·man 无此参数头 + strings]` |

> 注意两点：① man page 里的 `/run/samba` 是"默认构建"的现代路径，Termux 走 `--enable-fhs + --localstatedir=$PREFIX/var` 得到的是 `var/run/samba`，**以实测为准**；② `modules dir` 在 4.25.1 的 `smb.conf(5)` 里**没有**同名参数，它是 waf 的编译期设置，运行期只能通过保持路径或重编译来影响。

### 4.2 tdbsam 为什么会"找不到后端" `[已确认]`

- 配方 `--with-shared-modules=...pdb_tdbsam,pdb_smbpasswd,pdb_wbc_sam...` → 这三个是**共享模块**，编出来的产物是 `lib/samba/pdb/*.so`。实测该目录只有两个文件：`tdbsam.so`、`smbpasswd.so`（`pdb_wbc_sam` 因 `--without-winbind` 未生成）。
- `passdb backend` 默认就是 `tdbsam`（man 原文），因此**任何** `smbpasswd`/`smbd` 启动都会走插件加载：`load_module(MODULESDIR "/pdb/tdbsam.so")`。
- 如果 `MODULESDIR` 指向的位置没有 `pdb/tdbsam.so`，Samba 报的正是项目实测那句话：`No builtin nor plugin backend for tdbsam found`。所以这条报错的根因是**插件目录不可达**，不是密码库损坏、也不是权限问题。
- 对策（按可靠性排序）：① 保持 `$PREFIX/lib/samba` 布局；② 用 `smbd -b` 确认 `MODULESDIR` 后按实际路径摆文件；③ 重编译。**注意 `MODULESDIR` 不是 `smb.conf` 参数，`--option` 覆盖不了它**（见 4.1 注②）。

### 4.3 `smb.conf` 生成策略（本项目）

原则：**由守护进程生成"完全显式"的 `smb.conf`，不依赖任何编译期默认值**；生成后立即用 `testparm` 校验。

- 模板里显式写：`config file` 不用写（用 `--configfile`/`-s` 启动参数指定更直接）；`private dir`、`lock directory`、`state directory`、`cache directory`、`pid directory`、`ncalrpc dir`、`log file` 全部指向 `/data/adb/smbshare/var/...`。
- 生成后用 `testparm -s --configfile=<path>` 解析；`testparm --show-all-parameters` 可打印所有参数类型与合法值，`--parameter-name=<name>` 可只查一项（都是 4.25.1 `testparm(1)` 的正式选项）。`[已确认·man]`
- 目录权限：`private dir` 必须 `0700`（内含 `passdb.tdb`/`secrets.tdb`），对应 Android 侧由守护进程 `umask 077` + `chmod` 保证；`var/lock`、`var/lib`、`var/cache` 用 `0755`（Termux 配方注释：700 会让 `testparm` 警告）。
- **不要**把 `private dir` 放在 `/data/media/0` 或任何用户可写的 FUSE 路径上：`passdb.tdb` 就是密码库，放共享盘等于公开。`[推断/待验证]`

---

## 5. 自建 / 交叉编译 Samba 的可行性（只做风险判断）

结论先行：**技术上有路，但没有"照做就能出 smbd"的公开配方；投入产出比很差，应作为最后选项。** `[推断/待验证]`

已知的硬约束（全部有出处）：

1. **必须交叉编译 + 必须给 `cross-answers`**：waf 在交叉编译时无法跑运行时探测，Termux 用一份约 30 项的答案表绕过。缺表就 configure 失败；**答案错则静默降级**（例如把 `setreuid` 答成 `NO`，能力被编掉）。`[已确认·配方]`
2. **bionic 缺 API**：`execinfo.h`（backtrace）、`posix_spawn` 家族、`getifaddrs`/netlink 接口枚举、`passwd/group` 查询语义都与 glibc 有差异。Termux 用 `libandroid-execinfo`、`libandroid-spawn` 兜底。`[已确认·配方依赖]`
3. **Google 的 `samba-documents-provider` 只证明"客户端库可编"**：README 明确 Samba **4.5.1** + NDK r15b+，第 8 步是"`compile.sh` 编译 `libsmbclient.so`"，并只要求把 `jniLibs/<ABI>` 打包进 App。它**没有**编译 `smbd`。`[已确认·README]`
4. 其 `sambapatch.diff` 的改动文件清单（`+++` 列表）说明了 Android 适配的真实工作量：`lib/replace/getifaddrs.c`（改）+ 新增 `lib/replace/netlink_ifaddrs.c`、`lib/socket/interfaces.c`、`lib/util/util_pw.c`（密码/用户查询）、`librpc/ndr/util.c`、`nsswitch/libwbclient/{wbc_pwd,wbc_sid}.c`、`source3/lib/dumpcore.c`（core dump 路径）、`lib/replace/{replace.c,replace.h,wscript}`、`lib/param/loadparm.h`、`buildtools/wafsamba/wafsamba.py`，外加自建 `build_answers`、`cc_shim.py`、`configure.sh`、`compile.sh`、`install.sh`。`[已确认·diff]`
5. **NDK r27 与 waf 的组合没有公开验证**：Samba 的 waf 需要 host `python3`；`asn1_compile`/`compile_et` 需要系统包（Termux 直接用 `/usr/bin/asn1_compile`、`/usr/bin/compile_et`，并以 `--bundled-libraries='!asn1_compile,!compile_et'` 排除自带构建）。这些在纯 NDK 工具链下都要额外准备。`[已确认·配方]` + `[推断]`
6. **16 KB page size 对齐**：见 §3.1，Termux 产物已是 `0x4000`。自建时若链接器按 4 KB 对齐，Android 15+ 的部分设备（16 KB 内核）会直接 `exec` 失败。**这条必须作为验收项**，而 NDK r27 默认是否 16 KB 对齐要按实际产物 `readelf -l` 验证，本调研不做断言。`[推断/待验证]`

**若仍要自建，最小风险路径**（不保证成功）：完全复刻 Termux 配方（同一份 `samba-4.25.1.tar.gz` + 同 `SHA256` + 同一套 `cross-answers.txt` 与 `--with-shared-modules` 列表），只替换编译器为 NDK r27 的 clang + `--target=aarch64-linux-android26`，逐步对齐 `-D_FILE_OFFSET_BITS=64`、`-landroid-spawn`、`-landroid-execinfo`；先用 `smbd -b` 验证编译期路径与插件目录，再谈运行。**不要**从零写一套 configure。

---

## 6. `smb.conf` 安全基线（逐项，含默认值与建议）

> 所有"默认值"列均出自 4.25.1 man page 原文；"建议"列是本项目取舍。项目是**手机上的个人 SMB 服务**，威胁模型是"同一 Wi-Fi 下的其他设备"。

| 参数 | 4.25.1 默认 | 本项目建议 | 依据/理由 |
|---|---|---|---|
| `server min protocol` | `SMB2_02` | 显式写 `SMB2_02` | 默认即禁用 SMB1；显式写是给 reviewer 的证据，也防未来默认变化 `[已确认]` |
| `server max protocol` | `SMB3` | `SMB3_11` | 合法值到 `SMB3_11`；明确上限避免协商到更老变体 `[已确认]` |
| `server signing` | `default`（AD DC 才强制，其余不强制） | `mandatory` | **SMB2 下 `disabled` 会被当成 `auto`**，关不掉；不如强制签名 `[已确认]` |
| `server smb encrypt` | `default` = `if_required`（取值 `off/if_required/desired/required`） | 公网/热点 `desired` 或 `required`，家网可 `if_required` | 仅当 `server max protocol` ≥ `SMB3` 才有 SMB3 原生加密；加密降低吞吐 `[已确认]` |
| `map to guest` | `Never` | `Never` | 保持；`Bad User`/`Bad Password` 会静默把打错密码的人变 guest `[已确认]` |
| `guest ok` | `no` | `no` | 与 `restrict anonymous = 2` 搭配；man 原文：任何共享 `guest ok = yes` 都会抵消 `restrict anonymous=2` 的收益 `[已确认]` |
| `guest account` | `nobody`（可编译期改） | 不启用 guest，则无关 | Android 的 `/etc/passwd` 里通常没有 `nobody` `[推断/待验证]` |
| `valid users` | 空 = 任何已认证用户 | 显式列出（本项目通常是 `root`） | 空值等于放行所有能通过 passdb 的账号 `[已确认]` |
| `read only` | `yes` | 默认 `yes`；需要写入才设 `no` | 反向同义词是 `writeable`；默认只读是安全的起点 `[已确认]` |
| `browseable` | `yes` | 可设 `no` 降低被发现面 | 只是"是否出现在共享列表"，不是访问控制 `[已确认]` |
| `interfaces` | 空 = 内核枚举所有非 loopback 广播接口 | `wlan0`（或热点接口名） | Android 接口名随连接方式变化（`wlan0`/`ap0`/`rmnet*`），需运行时探测 `[已确认·man]` + `[推断]` |
| `bind interfaces only` | `no` | `yes` | 只监听 `interfaces` 列出的接口；man 提醒：`127.0.0.1` 不加入列表时 `smbpasswd`（默认连 localhost）可能表现异常 `[已确认]` |
| `smb ports` | 同义词 `server smb transports`，默认 `tcp, nbt`（445、139） | 显式 `445` 或 `tcp:445`；不要 139 | 见下方说明；`server smb transports` 语法支持 `tcp:1445`、`nbt:1139`、`quic:1443` `[已确认]` |
| `passdb backend` | `tdbsam` | 显式 `passdb backend = tdbsam`（可选带路径） | 显式写可见性更好；路径默认落在 `private dir` `[已确认]` |
| `wide links` | `no` | `no` | 允许跟随**指向共享树外**的符号链接，等于越权读全盘 `[已确认]` |
| `follow symlinks` | `yes` | `no`（若共享根下不需要符号链接） | 关掉可阻止客户端用 `ln -s /data/...` 逃逸；代价是查名略慢 `[已确认]` |
| `smb1 unix extensions` | `yes` | `no` | SMB1 已禁用，此扩展无正面价值；且开着会**自动禁用 `wide links`**（man 明说耦合） `[已确认]` |
| `smb3 unix extensions` | `yes` | `no` | SMB3.1.1 Unix Extensions 会带来 POSIX 语义（含符号链接），对 Android/Windows 混合客户端无必要 `[已确认]` |
| `log level` | `0` | `1`（排障临时 `3`，排障后回落） | 级别 >1 日志量暴涨 `[已确认]` |
| `log file` | 无默认（用编译期目录 + `log.%m`） | `/data/adb/smbshare/logs/log.%m` | `%m` 按客户端名分文件，便于定位 `[已确认]` |
| `max log size` | `5000`（**单位 KB**） | `50`（约 50 KB 滚动） | 超出会改名加 `.old`；Android 分区空间宝贵 `[已确认]` |
| `deadtime` | `10080`（分钟，= 7 天） | `15`（Termux 示例值） | 无打开文件时回收空闲连接，避免手机被几十个休眠会话占住 `[已确认]` |
| `socket options` | 空 | **不设**（或仅 `TCP_NODELAY`） | man 明确警告：设了 socket buffer 会关掉 Linux 自动调优，可能"严重损害"协议栈 `[已确认]` |
| `restrict anonymous` | `0` | `2` | 禁匿名 SAMR / 匿名 `IPC$`；与 `guest ok` 有耦合（见上） `[已确认]` |
| `ntlm auth` | `ntlmv2-only` | 保持默认，绝不 `ntlmv1-permitted` | 现代客户端默认 NTLMv2 `[已确认]` |
| `disable netbios` | `no` | `yes`（若 `server smb transports` 已不含 `nbt`） | 关掉 NBT 噪音与 137/138 端口暴露 `[已确认]` |
| `use sendfile` | `no` | `yes`（Android 内核支持 `sendfile`） | 仅对"独占 oplock 的文件"生效；Android FUSE 上是否受益需实测 `[已确认·man]` + `[推断]` |
| `oplocks` / `kernel oplocks` | `yes` / `no` | `yes` / `no` | 内核 oplock 关掉才能保留 Level II oplock；Android FUSE 后端行为未知 `[已确认]` + `[推断/待验证]` |
| `max smbd processes` | `0`（不限） | 设一个小值（如 `8`） | 手机内存有限，防 fork 风暴 `[已确认]` |
| `aio read size` | `1`（总是异步） | 保持，或配合 `vfs objects = aio_pthread` | Termux 示例共享段用 `vfs objects = aio_pthread` + `aio_pthread:aio open = yes` `[已确认]` |

**SMB1 到底怎么关**（最容易被写错的一条）：

- 正确写法：`server min protocol = SMB2_02`（这已是默认，写上等于声明意图）。`[已确认]`
- 错误写法：`server min protocol = NT1`（man 的"示例"里确实有这行，那是**开启 SMB1** 的示例，照抄会把 SMB1 打开）。`[已确认]`
- 不需要写 `server max protocol = NT1` 之类；`server max protocol` 只控制上限。
- 4.25.1 用 `smb1 unix extensions` / `smb3 unix extensions` 两个参数取代了旧的 `unix extensions`；Termux 的 `smb.conf.example` 里仍在用旧名 `unix extensions = no`，**是否仍被接受要以设备上 `testparm` 输出为准**（本调研未验证）。`[推断/待验证]`

**端口与 root**：

- 445/139 都 < 1024，**非 root 无法 bind**；本项目由 root 守护进程拉起 `smbd`，因此这不是限制，但意味着"降到普通用户跑 smbd"不可行。`[已确认·man 常识 + 推断]`
- Windows 客户端只认 445（Termux 示例注释原话："Windows can only see SMB servers on port 445"），若改用 4445 之类端口，需要 `iptables` REDIRECT 才能互通。`[已确认·Termux 示例注释]`
- `server smb transports` 支持 `quic`（UDP 443，需内核 `quic.ko`，Linux 6.14 测试过）——Android 内核不会有，**不要启用**。`[已确认·man]`

---

## 7. 用户与密码

`smbpasswd(8)` 实测/原文要点 `[已确认]`：

- `-a <user>` 添加账号，**仅 root 可用**，且 man 原文：默认 passdb 后端**要求用户已存在于系统密码文件**（通常 `/etc/passwd`），否则添加失败。
- `-x` 删除、`-d` 禁用（写入 `D` 标志）、`-e` 启用、`-c <path>` 指定 `smb.conf` 路径（"`${prefix}/etc/smb.conf`"之外的情况）。
- 密码交互：默认从 tty 读；非交互场景由程序通过管道喂 stdin，**不要**把密码拼进命令行参数（`/proc/<pid>/cmdline` 对同 UID 进程可读，日志里也容易残留）。
- `pdbedit` 提供 `-L`（列出）、`-a`（新增）、`-x`（删除）、`-u`（指定用户）、`-v`（详细，配合 `-w` 显示哈希）——比 `smbpasswd` 更适合"程序化读写 passdb"。`[已确认·pdbedit(8)]`

对本项目的推论 `[推断/待验证]`：

1. Android 没有 `useradd`，`/etc/passwd` 只有系统固定账号（`root`、`system`、`shell` 等，bionic 从 `/system/etc/passwd` 读）。因此**唯一确定存在的账号是 `root`**。
2. 现实设计：单 SMB 账号 = `root`，共享段用 `force user = root`（或干脆依赖 root 身份）+ `valid users = root`；不承诺"多用户各自映射不同 UNIX 用户"。
3. `guest` 路径不可靠：`nobody` 账号大概率不存在，`map to guest = Never` 已是默认，保持即可。
4. 密码库是 `private dir/passdb.tdb`（默认即 `<private dir>/passdb.tdb`，man 原文）→ 权限 `0600`、目录 `0700`，并纳入"卸载清理"清单。
5. 避免密码进 argv/日志的具体做法：`smbpasswd`/`pdbedit` 用 stdin 喂两行密码；守护进程日志里对含 `password`/`passwd` 的行做整行丢弃；**不要**用 `--option=...` 传密码。

---

## 8. 进程模型、信号与运行期控制

`smbd(8)` 原文要点 `[已确认]`：

- **进程模型**：`smbd` 是"每客户端一份服务进程"——"A session is created whenever a client requests one. Each client gets a copy of the server for each session... When all connections from its client are closed, the copy of the server for that client terminates." 父进程负责监听，子进程服务各自客户端；`max smbd processes` 可设上限（默认 `0` = 不限）。
- **配置重载**：配置文件"每三分钟自动检查一次，变化则重载"；也可 `SIGHUP` 强制；也可用 `smbcontrol` 发送重载请求。**重载不影响已建立的连接**——想生效必须断开/重连。
- **`smbcontrol`**：`smbcontrol <destination> <message-type> [params]`；`destination` 可为 `nmbd`/`smbd`/`winbindd`/PID/`all`，`smbd` 目标读 `smbd.pid`；消息类型见其 `MESSAGE-TYPES` 段（`reload-config` 即"重载配置"）。`[已确认·smbcontrol(1)]`
- **PID 文件**：落在 `pid directory`（本包 `$PREFIX/var/run/samba/smbd.pid`）。`[已确认·man + strings]`
- **CLI 选项**（`smbd -?` 文档原文）：`-D|--daemon`、`-i|--interactive`（只接一个连接、前台、打到 stdout）、`-F|--foreground`（不 daemonize，适合 supervisor）、`--no-process-group`、`-b|--build-options`（打印编译期路径/特性，**排查插件目录问题的第一工具**）、`-p|--port <ports>`、`-P <profiling>`、`-d <0-10>`、`--debug-stdout`、`--configfile=<file>`、`--option=<name>=<value>`、`-l|--log-basename <dir>`、`-V|--version`。`[已确认]`
- 推荐启动方式：`-F --configfile=<我们生成的 conf> -l <我们的日志目录>`，由守护进程托管并记录 pid；**用 `-F` 而不是 `-D`**，避免"父进程 daemonize 后我们丢失 pid 对应关系"。`[推断/待验证]`

---

## 9. 性能（只写有依据的部分）

- **协议版本**：SMB1 早已不是性能选项（本项目禁用）；SMB2/3 的差异主要来自签名/加密与 oplock 策略，而非"版本号本身更快"。`[已确认·man 语义]` + `[推断]`
- **大文件**：`use sendfile = yes` 只对"独占 oplock 的文件"生效；`min receivefile size`（默认 `0`）调大可让较大的写请求走 receivefile 路径（Termux 示例用 `16384`）。两者都属"可测不可猜"的项。`[已确认·man]`
- **多客户端**：每客户端一个 `smbd` 进程 + 每连接打开文件句柄；手机内存/文件描述符是上限，`max smbd processes`、`max connections`（示例 `6`）、`deadtime` 是三个直接相关的旋钮。`[已确认]`
- **异步 I/O**：`aio read size` 默认 `1`（总异步）；`vfs objects = aio_pthread` + `aio_pthread:aio open = yes` 是 Termux 示例的写法，属于"有依据的默认值"，但**未必适合 FUSE 后端**。`[已确认·示例]` + `[推断/待验证]`
- **FUSE 后端是主要变量**：AOSP 官方文档对 MediaProvider FUSE 的表述是"顺序写略差，随机读写可能慢到 2×"，并建议重度 I/O 场景用 bulk API 绕开 FUSE。SMB 服务端只能接受 FUSE 的代价，除非改共享 `/data/media/0` 直读（绕过 MediaProvider，但会与媒体库不一致）。`[已确认·AOSP]` + `[推断]`
- **不要迷信 `socket options`**：man 明确说 Linux 有自动调优，硬设 buffer 反而可能"cripple"。`[已确认]`
- 本项目**未做**任何吞吐实测；以上只是"调参顺序"，不是结论。

---

## 10. Android 特有问题

1. **FUSE 语义不是"真文件系统"**：Android 11+ 的 `/storage/emulated/0` 由 MediaProvider 的 FUSE 承载，官方描述是"在用户空间检查文件操作并按策略 allow/deny/redact"。因此 `open`/`rename`/`lock` 的最终行为取决于 MediaProvider，而不是 ext4。`rename` 跨目录、`fcntl` 记录锁、硬链接等边缘操作**必须实测**。`[已确认·AOSP 文档]` + `[推断]`
2. **`/data/media/0` 与 `/storage/emulated/0` 不是同一条路径**：前者是底层真实目录（标签 `media_rw_data_file`），后者是 FUSE 视图。root 进程可以走前者绕过 MediaProvider 的门禁与性能开销，但代价是"相册/媒体库可能看不到新文件"。`[已确认·file_contexts]` + `[推断]`
3. **大小写敏感性**：`/storage/emulated`（FUSE）实际底层是 ext4/f2fs，区分大小写；外置 SD 常见 exFAT/FAT32 则不区分。Samba 侧 `case sensitive` 默认 `auto`（依共享路径探测），跨设备名冲突要靠 `case sensitive`/`preserve case`/`short preserve case` 处理。`[已确认·man 默认值]` + `[推断]`
4. **权限与属主映射**：Android 的 UID/GID 语义（如媒体文件属主为 app UID、`media_rw` 组）与 Samba 期望的 `user/group` 模型不同。Termux 示例把这些行注释掉了并注明"Android 上似乎默认就是这样"：`force user = root`、`force group = everybody`、`create mask = 0660`、`directory mask = 0770`、`vfs objects = fake_perms`。**这是社区经验，不是官方保证**，必须按设备实测。`[已确认·示例注释]` + `[推断/待验证]`
5. **xattr / DOS 属性在 FUSE、FAT 上不可靠**：Termux 示例为此关掉了一整组参数（`nt acl support = no`、`ea support = no`、`store dos attributes = no`、`map archive/hidden/readonly/system = no`），并注释"turn off things that cannot be set on sdcardfs and vfat filesystems"。`[已确认·示例]`
6. **`/proc` 与 `/dev/urandom`**：`smbd` 启动需要随机源（`/dev/urandom`）与进程信息（`/proc`）。Android 上 `/proc/net` 对非系统域有限制（本项目守护进程在 kernel root 域，通常不受影响）；`getifaddrs()` 依赖 netlink，被 SELinux 拦时的表现是"枚举不到接口"而不是明确报错。`[已确认·file_contexts/label]` + `[推断/待验证]`
7. **16 KB page size**：见 §3.1；Termux 产物已是 16 KB 对齐，自建时这是硬验收项。`[已确认]`
8. **SELinux**：把文件、socket、端口"放对位置"只是第一步，域间访问要单独看；见 `docs/research/selinux.md`。`[已确认]`

---

## 11. 落地检查清单（Samba 侧）

- [ ] 选定运行模式（A 依赖宿主 Termux / B 自带 + 原布局 / C 二进制改造），并删掉其它两条的代码路径。
- [ ] 用 `smbd -b` 在设备上打印编译期 `MODULESDIR`/`CONFIGFILE`/`PRIVATE_DIR`，与 §4.1 表核对。
- [ ] `lib/samba/pdb/tdbsam.so` 存在且可 `dlopen`；`smbpasswd -a root` 或 `pdbedit -a` 能成功（这同时验证了 `passdb backend` 与插件目录两件事）。
- [ ] 生成的 `smb.conf` 经 `testparm` 零错误；`server min protocol` 为 `SMB2_02`。
- [ ] 密码/密钥类文件 `0600`、`private dir` `0700`；日志目录可写且 `max log size` 有限。
- [ ] 445 端口 bind 成功（root），只监听目标接口；`interfaces`/`bind interfaces only` 与 `smbpasswd` 的 localhost 行为不冲突。
- [ ] 断开重连验证 `SIGHUP` / `smbcontrol smbd reload-config` 生效；pid 文件与真实进程一致。
- [ ] 记录一次"大文件顺序读写 + 4 客户端并发"的基线数字，再决定是否调 `aio`/`sendfile`/`socket options`。

---

## 12. 证据不足 / 待上机验证

1. **`setreuid/seteuid` 能力**：Termux 的 `cross-answers` 标 `NO`，但设备上 bionic 是否提供、SELinux 是否允许未被验证。这决定"多 SMB 账号 → 不同 UNIX 身份"是否可行。
2. **`smbpasswd -a` 在 Android 的真实行为**：man 说需要系统密码文件里有该用户；`root` 应存在，但 Android 的 `getpwnam` 数据源（`/system/etc/passwd` vs `/etc/passwd`）与 Samba 的绑定关系未实测。
3. **`unix extensions`（旧名）是否仍被 4.25.1 接受**：man 已拆成 `smb1/smb3 unix extensions`，Termux 示例仍用旧名；需 `testparm` 输出确认。
4. **FUSE 下的 oplock / fcntl 锁 / rename 语义**：AOSP 只说"用户空间可拦截"，未给出 POSIX 兼容矩阵；本项目需用 SMB 客户端做专门的锁与改名测试。
5. **`/data/media/0` 直读 vs `/storage/emulated/0` 的吞吐差**：AOSP 给了 FUSE 的粗粒度结论，但手机 eMMC/UFS 上的实际差距、以及媒体库不一致的后果都要实测。
6. **`server smb encrypt = required` 在 Android 客户端的可用性**：取决于客户端是否支持 SMB3 加密（Windows 8+/Samba 4.1+），移动端第三方客户端不一定支持。
7. **`vfs objects = fake_perms`、`aio_pthread` 在 FUSE 上的实际效果**：Termux 示例只是"作者在手机上试过"，无版本/机型上下文。
8. **本调研的网络限制**：`git.samba.org` 返回 429（限流），未能取到 Samba 源码行号级证据；`raw.githubusercontent.com`、`source.android.com`、`android.googlesource.com` 可达（已用于取证）。凡涉及"源码某行"的推断，请在可访问 Samba git 的环境复核。

---

## 13. 来源

**上游（Samba 官方 man page / 下载站）**

- `smb.conf(5)`（参数、默认值、`server smb transports`、`smb1/smb3 unix extensions`）：https://www.samba.org/samba/docs/current/man-html/smb.conf.5.html
- `smbd(8)`（进程模型、`SIGHUP`、`-b`、`-F`、`-p`、`--configfile`、TDB 清单）：https://www.samba.org/samba/docs/current/man-html/smbd.8.html
- `smbpasswd(8)`（`-a/-x/-d/-e/-c`、"用户必须已存在"）：https://www.samba.org/samba/docs/current/man-html/smbpasswd.8.html
- `pdbedit(8)`（`-L/-a/-x/-u/-v/-w`）：https://www.samba.org/samba/docs/current/man-html/pdbedit.8.html
- `smbcontrol(1)`（destination / message-type）：https://www.samba.org/samba/docs/current/man-html/smbcontrol.1.html
- `testparm(1)`（`--show-all-parameters`、`--parameter-name`）：https://www.samba.org/samba/docs/current/man-html/testparm.1.html
- 源码发布包（4.25.1）：https://download.samba.org/pub/samba/samba-4.25.1.tar.gz
- Samba git（本次 429，仅登记）：https://git.samba.org/?p=samba.git

**Termux 打包**

- `packages/samba/build.sh`（waf 参数、`cross-answers.txt`、`post_massage` 权限、`SHA256`）：https://github.com/termux/termux-packages/blob/master/packages/samba/build.sh
- 包索引（`PACKAGES.txt` 中 39 个包的来源）：https://packages.termux.dev/apt/termux-main/dists/stable/main/binary-aarch64/Packages
- 包主页：https://github.com/termux/termux-packages/tree/master/packages/samba

**Google 先例（只覆盖客户端库）**

- `samba-documents-provider` README（Samba 4.5.1 / NDK r15b / 只编 `libsmbclient.so`）：https://github.com/google/samba-documents-provider
- 同一仓库的 `sambapatch.diff`（改 `lib/replace`、`lib/socket/interfaces.c`、`lib/util/util_pw.c`、`source3/lib/dumpcore.c` 等）

**Android（AOSP 官方）**

- Scoped storage / MediaProvider FUSE（Android 11+、性能代价）：https://source.android.com/docs/core/storage/scoped
- SELinux 概览（default denial、Enforcing/Permissive、denial 落到 dmesg+logcat）：https://source.android.com/docs/security/features/selinux
- `private/file_contexts`（`/data/adb` = `adb_data_file`、`/data/media/.*` = `media_rw_data_file`、`/storage(/.*)` = `storage_file`、`/dev/fuse` = `fuse_device`）：https://android.googlesource.com/platform/system/sepolicy/+/refs/heads/main/private/file_contexts

**本仓库内制品（本次取证对象）**

- `.work/debs/aarch64/samba_4.25.1-1_aarch64.deb`、`.work/debs/aarch64/PACKAGES.txt`
- `.work/man/smb.conf.5.txt`、`smbd.8.txt`、`smbcontrol.1.txt`、`testparm.1.txt`（`Samba 4.25.1-Debian-4.25.1+dfsg-1`）
- `.work/gsp/{README.md,sambapatch.diff}`
- 解包产物 `.work/rootfs_aarch64/**`（`readelf`/`strings` 取证用）
