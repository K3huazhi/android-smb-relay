#!/usr/bin/env bash
# Reproducible Android SMB Relay build (Linux / WSL).
#
# Inputs are pinned in build/cache/debs/aarch64/PACKAGES.txt (SHA256).
# No Android device is needed to build; only to test.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD="$ROOT/build"
CACHE="$BUILD/cache"
ARCH="${ARCH:-arm64}"
TERMUX_ARCH="${TERMUX_ARCH:-aarch64}"
GOARCH="${GOARCH:-arm64}"
DEBS="$CACHE/debs/$TERMUX_ARCH"
BUNDLE="$CACHE/bundle-$TERMUX_ARCH"
RELEASE_BUNDLE="$CACHE/release-bundle-$TERMUX_ARCH"
STAGE="$BUILD/out/module"
VERSION="$(sed -n 's/^version=//p' "$ROOT/module.prop" | head -1)"
DATA_PREFIX="/data/adb/android-smb-relay"

if [ "$ARCH" != "arm64" ]; then
  echo "This release supports arm64 only (ARCH=$ARCH)" >&2
  exit 1
fi
command -v go >/dev/null || { echo "go is required (1.22+)" >&2; exit 1; }
command -v python3 >/dev/null || { echo "python3 is required" >&2; exit 1; }
command -v dpkg-deb >/dev/null || { echo "dpkg-deb is required (run in WSL/Linux)" >&2; exit 1; }
command -v readelf >/dev/null || { echo "binutils (readelf) is required" >&2; exit 1; }

mkdir -p "$CACHE" "$BUILD/out" "$ROOT/releases"

if [ ! -f "$DEBS/samba_4.25.1-1_${TERMUX_ARCH}.deb" ]; then
  echo "== fetching Termux packages =="
  python3 "$BUILD/fetch-termux.py" "$TERMUX_ARCH"
fi
if [ ! -f "$DEBS/PACKAGES.txt" ] && [ -f "$BUILD/termux-$TERMUX_ARCH.lock" ]; then
  cp "$BUILD/termux-$TERMUX_ARCH.lock" "$DEBS/PACKAGES.txt"
fi

echo "== verifying cached packages =="
python3 "$BUILD/verify-lock.py" "$TERMUX_ARCH"

echo "== extracting and bundling Samba =="
python3 "$BUILD/bundle.py" "$TERMUX_ARCH" --out "$BUNDLE"

echo "== patching compiled-in prefix -> $DATA_PREFIX =="
rm -rf "$RELEASE_BUNDLE"
cp -a "$BUNDLE" "$RELEASE_BUNDLE"
python3 "$BUILD/patch-prefix.py" "$RELEASE_BUNDLE" "$DATA_PREFIX"

echo "== building asrd ($GOARCH) =="
(
  cd "$ROOT/src/asrd"
  CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build -trimpath \
    -ldflags '-s -w -buildid=' -o "$BUILD/out/asrd-$GOARCH" .
)

echo "== staging module =="
rm -rf "$STAGE"
mkdir -p "$STAGE"/{bin,lib,scripts,config,webui}
cp "$ROOT/module.prop" "$ROOT/customize.sh" "$ROOT/post-fs-data.sh" \
   "$ROOT/service.sh" "$ROOT/uninstall.sh" "$ROOT/action.sh" "$STAGE"/
cp "$ROOT/config/default-config.json" "$STAGE/config/"
cp -a "$ROOT/scripts/." "$STAGE/scripts/"
cp -a "$ROOT/webui/." "$STAGE/webui/"
mkdir -p "$STAGE/webui/downloads"
cp -a "$ROOT/tools/." "$STAGE/webui/downloads/"
cp "$ROOT/bin/smbctl" "$STAGE/bin/smbctl"
chmod 0755 "$STAGE/bin/smbctl"
cp "$BUILD/out/asrd-$GOARCH" "$STAGE/bin/asrd"
chmod 0755 "$STAGE/bin/asrd"
cp -a "$RELEASE_BUNDLE/bin/." "$STAGE/bin/"
rm -rf "$STAGE/lib"
mkdir -p "$STAGE/lib"
cp -a "$RELEASE_BUNDLE/lib/." "$STAGE/lib/"
chmod 0755 "$STAGE"/bin/smbd "$STAGE"/bin/smbpasswd "$STAGE"/bin/smbcontrol "$STAGE"/bin/testparm
chmod 0755 "$STAGE"/*.sh "$STAGE"/scripts/*.sh
find "$STAGE/lib" -type f -name '*.so*' -exec chmod 0755 {} \;
find "$STAGE/webui" -type f -exec chmod 0644 {} \;

# Optional documentation in the zip; the repository is the authoritative copy.
for f in README.md LICENSE; do
  [ -f "$ROOT/$f" ] && cp "$ROOT/$f" "$STAGE/$f"
done
if [ -f "$ROOT/docs/third-party.md" ]; then
  mkdir -p "$STAGE/docs"
  cp "$ROOT/docs/third-party.md" "$STAGE/docs/third-party.md"
fi

# Normalize line endings and file modes for a cross-platform zip.
python3 - "$STAGE" <<'PY'
import os, sys
root = sys.argv[1]
for dirpath, _, files in os.walk(root):
    for f in files:
        if f.endswith(('.sh', '.prop', '.json', '.md', '.html', '.css', '.js')):
            p = os.path.join(dirpath, f)
            data = open(p, 'rb').read()
            if b'\r\n' in data:
                open(p, 'wb').write(data.replace(b'\r\n', b'\n'))
PY

OUT="$ROOT/releases/android-smb-relay-$VERSION.zip"
python3 "$BUILD/zip.py" "$STAGE" "$OUT"
echo "== built $OUT =="
ls -lh "$OUT"
