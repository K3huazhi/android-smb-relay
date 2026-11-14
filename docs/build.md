# 构建与发布

本文描述 `android-smb-relay` 0.1.0 的脚本化构建流程、依赖版本、产物结构、校验项目，以及可复现性风险（尤其是 Termux 镜像/哈希不一致）。

> 当前仓库已经包含正式的构建脚本（`build/`），不再依赖 `.work/` 下的开发期脚本。构建必须在 **WSL2 或 Linux** 上执行；Windows 原生环境无法可靠处理 `dpkg-deb`、ELF、符号链接和 LF 换行。

## 1. 产物与脚本

### 1.1 一键构建

```sh
cd /path/to/SMBShare
bash build/build.sh
```

`build.sh` 依次执行：

1. 检查 `ARCH=arm64`、Go、Python 3、`dpkg-deb`、`readelf`；
2. 若 `build/cache/debs/aarch64/samba_4.25.1-1_aarch64.deb` 不存在，调用 `fetch-termux.py` 下载依赖闭包；
3. `verify-lock.py` 校验缓存 `.deb` 的 SHA-256；
4. `bundle.py` 解包并计算 `smbd/smbpasswd/smbcontrol/testparm` 及 `libexec/samba/{samba-dcerpcd,rpcd_*}` 的 NEEDED 闭包；
5. `patch-prefix.py` 把编译期 Termux 前缀重写为 `/data/adb/android-smb-relay`；
6. 交叉编译 `asrd`；
7. 组装 `build/out/module/`（模块根），加入 `LICENSE`、`README.md`、`docs/third-party.md`；
8. `zip.py` 生成确定性 zip：`releases/android-smb-relay-<module.prop version>.zip`。

### 1.2 脚本职责

| 文件 | 作用 |
|---|---|
| `build/build.sh` | 总入口；检查环境、串起所有步骤、组装 staging、打包 |
| `build/fetch-termux.py` | 从 Termux 包索引解析 `samba`（以及 `zstd`）的依赖闭包，下载 `.deb`，把**实际下载文件**的 SHA-256 写入 `build/cache/debs/<arch>/PACKAGES.txt` |
| `build/verify-lock.py` | 逐行校验 `PACKAGES.txt` 中的 SHA-256；失败即退出非 0 |
| `build/bundle.py` | `dpkg-deb -x` 解包、按 `readelf -d` 的 NEEDED 递归收集库、复制 `lib/samba/**` 插件树与 `libexec/samba/**` 命名管道服务、写 `MISSING.txt` |
| `build/patch-prefix.py` | 在 ELF 内嵌 C 字符串中把 `/data/data/com.termux/files/usr` 替换为 `/data/adb/android-smb-relay`（新前缀不长于旧前缀才可原地替换，NUL 填充） |
| `build/zip.py` | 固定时间戳（2026-01-01）、排序条目、按可执行位设置 zip mode，输出字节级尽量可复现的 zip |
| `build/termux-aarch64.lock` | 人类可读的锁摘要（当前 41 行：包名/版本/pool 路径/SHA-256/OK），供审计参考；`build.sh` 实际使用的是 `build/cache/debs/aarch64/PACKAGES.txt`（同样 41 行） |

### 1.3 缓存与输出目录

```text
build/
├── build.sh
├── fetch-termux.py / verify-lock.py / bundle.py / patch-prefix.py / zip.py
├── termux-aarch64.lock
├── cache/
│   ├── debs/aarch64/*.deb + PACKAGES.txt        # 固定版本的 Termux 包与 SHA-256 锁
│   ├── extract/aarch64/                          # dpkg-deb -x 的临时解包
│   ├── bundle-aarch64/{bin,lib,MISSING.txt}      # 未打补丁的 Samba 集合
│   └── release-bundle-aarch64/{bin,lib,...}      # 已重写前缀的集合
└── out/
    ├── asrd-arm64                                # 交叉编译产物
    └── module/                                   # 模块 staging（zip 根层）
```

`build/out/module/` 的内容就是 zip 的根：`module.prop`、`bin/`、`lib/`、`libexec/`、`webroot/`、`scripts/`、`config/`、`webui/`、`*.sh`，以及（构建时加入的）`LICENSE`、`README.md`、`docs/third-party.md`。

### 1.4 0.1.3 产物观测值

用于核对（不是校验和）：

- `releases/android-smb-relay-0.1.3.zip`：**349 个条目**，约 **36 MB**（最终包大小以 `ls -l` 为准）。
- `lib/` 根目录 **124 个 `.so*`**；`lib/samba/` 下 **153 个插件库**（`lib/samba/` 含目录共 160 个条目）。
- `webroot/index.html` 为 KernelSU 模块页入口（仅 1 个文件，权限 0644）。
- `libexec/samba/` **8 个可执行文件**：`samba-dcerpcd`、`rpcd_classic`、`rpcd_epmapper`、`rpcd_fsrvp`、`rpcd_lsad`、`rpcd_mdssvc`、`rpcd_spoolss`、`rpcd_winreg`。缺这些文件时安卓客户端挂载会在 IPC$ 枚举阶段报 `0xC000020C`。
- `bin/` 6 个文件：`asrd`、`smbctl`、`smbd`、`smbpasswd`、`smbcontrol`、`testparm`。
- zip 内含 `LICENSE`、`README.md`、`docs/third-party.md`。
- `lib/lib/` **必须为 0**（见 §4.1 的阻断校验）。

## 2. 依赖与版本

### 2.1 构建主机

| 依赖 | 版本/说明 |
|---|---|
| OS | WSL2 Ubuntu（或原生 Linux） |
| Go | **1.22+**（`src/asrd/go.mod` 写 `go 1.22`；只用标准库，无需 `go mod download`） |
| Python | 3.10+ |
| binutils | `readelf` |
| dpkg | `dpkg-deb` |
| 压缩 | Python 标准库 `zipfile`（无需外部 `zip`） |
| 磁盘 | 至少 1 GB 临时空间 |

建议记录 `go version`、`python3 --version`、`dpkg-deb --version`、`readelf --version` 到构建日志。

### 2.2 Go 编译参数

`build.sh` 使用：

```sh
cd src/asrd
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -ldflags '-s -w -buildid=' -o ../../build/out/asrd-arm64 .
```

- `CGO_ENABLED=0`：纯静态、无 libc 依赖。
- `-trimpath`：去掉本机路径。
- `-buildid=`、`-s -w`：去掉 build ID 与符号/调试信息，提高可复现性。
- 产物校验：`file build/out/asrd-arm64` 应为 ARM aarch64 静态 ELF；`go version -m` 应显示 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0`。
- 仓库没有 Go 测试文件；`go test ./...` 只做编译，不能替代真机冒烟测试。

### 2.3 Termux 运行时组件

| 组件 | 版本 |
|---|---|
| Samba | 4.25.1-1（aarch64） |
| 依赖包 | 以 `build/cache/debs/aarch64/PACKAGES.txt` 为准（当前 **41 条**，含 `zstd`、`liblzma`；与缓存中的 41 个 `.deb` 一一对应） |
| 关键库 | GnuTLS 3.8.13、OpenSSL 3.6.5、krb5 1.22.3、libicu 79.1、readline 8.3.6、zlib 1.4、libcap 2.78、libc++ 30 等 |

### 2.4 Samba 源码（GPL 合规）

| 项 | 值 |
|---|---|
| 源码包 | `https://download.samba.org/pub/samba/samba-4.25.1.tar.gz` |
| SHA-256 | `7f367545bca69aa96897628f82f0f04d4b66b3bcb36cb978b9bb952c4b4c428d` |
| Termux 打包配方 | `https://github.com/termux/termux-packages/tree/master/packages/samba` |
| 我们的修改 | **不改源码**；只对已编译 ELF 内嵌的 Termux 前缀做字符串重定位（§3.4） |

## 3. 构建步骤（分步说明）

> 下面的命令与 `build.sh` 等价，便于理解/调试；正常构建只需 `bash build/build.sh`。

### 3.1 准备

```sh
cd /path/to/SMBShare
mkdir -p build/cache/debs/aarch64 build/out
```

### 3.2 获取并校验 Termux 包

```sh
python3 build/fetch-termux.py aarch64   # 缓存已存在且 samba deb 存在时 build.sh 会跳过
python3 build/verify-lock.py aarch64
```

`fetch-termux.py`：

- 请求 `https://packages.termux.dev/apt/termux-main/dists/stable/main/binary-aarch64/Packages`；
- 解析 `samba`（以及 `zstd`）的 `Depends` 闭包（含虚拟依赖的第一个候选）；
- 按 `Size` 断点续传/校验完整性；
- 写入 `PACKAGES.txt`：`name<TAB>version<TAB>filename<TAB>sha256`。

`verify-lock.py` 对 `PACKAGES.txt` 每一行计算缓存文件 SHA-256；**不匹配/缺失即失败**。

### 3.3 解包、计算 NEEDED 闭包

```sh
python3 build/bundle.py aarch64 --out build/cache/bundle-aarch64
```

- 解包到 `build/cache/extract/aarch64/`；
- 从 `bin/{smbd,smbpasswd,smbcontrol,testparm}` 与 `libexec/samba/{samba-dcerpcd,rpcd_*}` 出发，沿 `readelf -d` 的 `NEEDED` 递归收集库；
- 复制 `lib/samba/**` 插件树（`pdb/tdbsam.so`、`vfs/*.so` 等）；
- 写 `MISSING.txt`。

预期 `MISSING.txt` 只包含 Android 系统库：

```text
libc.so
libdl.so
libm.so
```

其他缺失（例如 `libicuuc.so.*`）说明依赖闭包/缓存不完整，**必须解决**。

### 3.4 重写编译期前缀

```sh
rm -rf build/cache/release-bundle-aarch64
cp -a build/cache/bundle-aarch64 build/cache/release-bundle-aarch64
python3 build/patch-prefix.py build/cache/release-bundle-aarch64 /data/adb/android-smb-relay
```

原理：

- Termux 把 `/data/data/com.termux/files/usr` 写进 `RUNPATH`、`CONFIGFILE`、`MODULESDIR`、`private/lock/state/cache/pid` 等字符串常量；
- 脚本遍历所有 ELF，找到包含旧前缀的 C 字符串，整串替换为新前缀；
- 要求新前缀**不长于**旧前缀（旧 31 字节、新 28 字节），剩余字节用 `\0` 填充，ELF 布局与字符串长度不变；
- 不做源码级修改（见 [third-party.md](third-party.md) 的 GPL 说明）。

### 3.5 编译 `asrd`

```sh
cd src/asrd
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -ldflags '-s -w -buildid=' -o ../../build/out/asrd-arm64 .
cd ../..
```

### 3.6 组装 staging

`build.sh` 会：

1. 复制模块元数据与脚本（`module.prop`、`customize.sh`、`post-fs-data.sh`、`service.sh`、`uninstall.sh`、`action.sh`）；
2. 复制 `config/default-config.json`、`scripts/`、`webui/`；
3. 放入 `bin/asrd`、`bin/smbctl` 与 release-bundle 的 `bin/*`；
4. **先删除 staging 的 `lib/` 再拷贝**（`rm -rf "$STAGE/lib"; mkdir -p "$STAGE/lib"; cp -a "$RELEASE_BUNDLE/lib/." "$STAGE/lib/"`），避免出现 `lib/lib/` 嵌套；同样方式拷贝 `libexec/` 并 `chmod 0755`；
5. 设置可执行位、统一 LF；
6. 复制 `LICENSE`、`README.md`、`docs/third-party.md` 到模块 staging（GPL 合规）。

### 3.7 打包

```sh
python3 build/zip.py build/out/module releases/android-smb-relay-0.1.0.zip
```

要求：

- zip 根层就是模块根（`module.prop` 在第一层），不要再套目录；
- 不需要 `META-INF/`（Manager 安装不依赖 `update-binary`；本模块不支持 recovery 刷入）；
- 不包含 `build/cache`、`build/out`、`.work`、日志、`.git`；
- `zip.py` 固定时间戳/排序，便于比较内容；GPL 文档在 zip 内。

## 4. 校验（发布阻断项）

### 4.1 包结构与换行

```sh
unzip -t releases/android-smb-relay-0.1.0.zip
unzip -l releases/android-smb-relay-0.1.0.zip | head -40
sha256sum releases/android-smb-relay-0.1.0.zip

# 关键文件必须存在
unzip -l releases/android-smb-relay-0.1.0.zip | grep -E \
  'module.prop|bin/asrd|bin/smbd|lib/samba/pdb/tdbsam.so|webui/index.html|LICENSE|README.md|docs/third-party.md'

# 阻断项：不允许 lib/lib/ 嵌套
if unzip -l releases/android-smb-relay-0.1.0.zip | grep -q 'lib/lib/'; then
  echo 'FAIL: lib/lib nested'; fi

# module.prop 必须是 LF
unzip -p releases/android-smb-relay-0.1.0.zip module.prop | od -c | head
```

### 4.2 ELF / 动态链接

```sh
rm -rf /tmp/asr-check && mkdir -p /tmp/asr-check && cd /tmp/asr-check
unzip -q /path/to/releases/android-smb-relay-0.1.0.zip

file bin/smbd
readelf -h bin/smbd | grep -E 'Class|Machine'
readelf -lW bin/smbd | grep LOAD          # 期望 Align 0x4000
readelf -d bin/smbd | grep -E 'RUNPATH|NEEDED' | head
strings -a bin/smbd | grep -c '/data/data/com.termux'   # 期望 0
strings -a bin/smbd | grep '/data/adb/android-smb-relay' | head

# 递归检查所有随包 ELF（bin/ 与 lib/；不要把 text 文档算进去）
if find bin lib -type f -exec strings -a {} + | grep -q '/data/data/com.termux/files/usr'; then
  echo 'FAIL: termux prefix remains'; fi
```

期望：

- `Class: ELF64`、`Machine: AArch64`；
- 所有 `LOAD` 的 `Align` 为 `0x4000`（16 KB，兼容 16 KB page size 设备）；
- `RUNPATH` 指向 `/data/adb/android-smb-relay/lib`（及 `lib/samba`）；
- 无 Termux 前缀残留；
- 模块 `lib/` 根有约 117 个 `.so*`、`lib/samba/` 有约 153 个插件库。

### 4.3 设备侧冒烟测试

```sh
su -c 'ksud module install /sdcard/Download/android-smb-relay-0.1.0.zip'
# 重启后
su -c '/data/adb/modules/android-smb-relay/bin/smbctl status'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl doctor'
su -c '/data/adb/modules/android-smb-relay/bin/smbctl url'
su -c 'grep -m1 "smbd version" /data/adb/android-smb-relay/logs/smbd.log'
su -c 'curl -s http://127.0.0.1:8080/api/health'
```

再从 PC 侧做 SMB 冒烟（列目录、10 MB/1 GiB/4 GiB+ 读写哈希、只读拒绝、`-m NT1` 拒绝、WebUI 登录/CSRF、stop/start、kill `smbd` 恢复；有条件的再做并发/小文件）。

> 不要在构建主机上直接运行 `bin/smbd`：它是 Android/bionic 动态链接的可执行文件，依赖 `/system/bin/linker64`。

## 5. Termux 镜像与哈希风险

Termux 的 apt 仓库持续重建，存在：

1. **同版本号被重建为不同字节**：`4.25.1-1` 可能重新打包；依赖/SONAME 可能变化。
2. **镜像同步延迟**：`packages.termux.dev` 与各地镜像不同步，同名包可能哈希不同。
3. **SONAME 漂移**：`libicuuc.so.79` 等升级会改变 `smbd` 的 NEEDED，旧 bundle/MISSING 结果失效。
4. **无 apt 签名校验链**：本项目直接下载 `.deb`，只能靠 SHA-256 锁。
5. **锁覆盖**：`fetch-termux.py` 只把解析出的包写入 `PACKAGES.txt`；如果缓存目录里手工加入了额外 `.deb`，`bundle.py` 仍会打包它们，但 `verify-lock.py` 不会校验。**发布前必须确认 `PACKAGES.txt` 覆盖了 `build/cache/debs/aarch64/` 中的每一个 `.deb`**（当前 41 条锁 / 41 个 deb，已一致）。

构建要求：

- 以 `PACKAGES.txt` 的 SHA-256 为唯一信任锚；任一行不符立即中止；
- 固定使用 `packages.termux.dev`，不要在构建中途 `apt upgrade`/换源；
- 每次发布保存 `PACKAGES.txt`、`sha256sum`、`unzip -l` 清单、Go/Python 版本；
- 上游重建导致哈希变化时，视为供应链变更：记录新哈希、重做真机冒烟测试、考虑升版本号。

## 6. 可复现性

| 来源 | 影响 | 缓解 |
|---|---|---|
| Go 工具链/flags | 二进制不同 | 固定 Go 1.22.x 与 `-trimpath -ldflags '-s -w -buildid='` |
| Termux `.deb` | 字节不同 | SHA-256 锁；每次发布保存清单 |
| zip 时间戳/顺序 | zip 哈希不同 | `zip.py` 固定时间戳与排序 |
| 文件系统元数据 | zip mode 不同 | `zip.py` 按可执行位/后缀归一化 mode |
| Android 运行环境 | 不影响 zip | 真机冒烟测试单独记录 |

结论：**内容可复现**，zip 也尽量字节级稳定；发布时仍应同时给 `sha256sum` 与内容清单。

## 7. 常见构建问题

| 问题 | 原因 | 处理 |
|---|---|---|
| `MISSING.txt` 出现预期三项之外的库 | 依赖闭包不全/缓存缺包 | 补包；检查 `bundle.py` 是否遍历 `lib/`；查看 `build/cache/extract` |
| `replacement too long` | 新前缀 >31 字节 | 新前缀必须是 `/data/adb/android-smb-relay` 或更短 |
| patch 后仍有 Termux 路径 | 漏遍历 `lib/samba/**` 或非字符串区 | 用 `find bin lib -type f -exec strings -a {} + \| grep` 全量复查 |
| zip 里出现 `lib/lib/` | staging 的 `lib` 已存在时 `cp -a src/lib dest/lib` 嵌套 | `build.sh` 已改为先 `rm -rf "$STAGE/lib"` 再 `cp -a src/lib/. dest/lib/`；构建后用 §4.1 阻断检查 |
| zip 缺 `LICENSE`/`README`/`docs/third-party.md` | 构建版本早于文档加入 staging | 重新运行当前 `build.sh`；`unzip -l` 验证 |
| `smbd` 启动报 `No builtin nor plugin backend for tdbsam found` | `MODULESDIR`/`lib/samba/pdb/tdbsam.so` 不在预期路径 | 确认 zip 中 `lib/samba/pdb/tdbsam.so` 存在；prefix patch 后 `MODULESDIR=/data/adb/android-smb-relay/lib/samba` |
| 新版本安装后启动失败 | `module.prop` CRLF、架构不对、lib 布局错误 | 检查 LF；`ARCH=arm64`；§4.2 布局检查 |
| `build.sh` 提示缺 `go`/`dpkg-deb`/`readelf` | WSL 环境未装依赖 | 安装 Go 1.22+、`dpkg`、`binutils`、Python3 |
| `verify-lock.py` 失败 | 镜像/缓存被替换或下载不完整 | 删除对应 `.deb` 重新 fetch；不要绕过校验 |

## 8. 版本号与发布检查清单

- [ ] `module.prop` 的 `version` 与 `versionCode` 更新（`versionCode` 单调递增）。
- [ ] `src/asrd/daemon.go` 的 `appVersion` 同步。
- [ ] `README.md`、`docs/android-compatibility.md`、`docs/third-party.md` 更新版本/证据。
- [ ] 清理 `build/cache` 中未被 `PACKAGES.txt` 覆盖的 `.deb`（当前 41/41 已一致，重建后仍要复查）。
- [ ] 运行 `bash build/build.sh` 重建 zip。
- [ ] 通过 §4.1/§4.2 全部阻断校验（尤其 `lib/lib/` 与文档包含）。
- [ ] 在真机跑完整冒烟（安装、重启、SMB2/3、10 MB/1 GiB/4 GiB+、只读拒绝、NT1 拒绝、WebUI/CSRF、stop/start、kill smbd 恢复、watchdog failed 后 `smbctl start`）。
- [ ] 记录 `sha256sum`、构建主机/工具链版本、`PACKAGES.txt`。
- [ ] 明确标注未验证项（Android 15/17、Magisk、其他机型、卸载/重装、热点/VPN 实际转发），不要写成已验证。
