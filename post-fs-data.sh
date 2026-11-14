#!/system/bin/sh
# post-fs-data: blocking stage. Keep this short (no network, no storage I/O).

MODDIR=${0%/*}
DATA_DIR=/data/adb/android-smb-relay

mkdir -p "$DATA_DIR/config" "$DATA_DIR/logs" "$DATA_DIR/run" "$DATA_DIR/samba" "$DATA_DIR/tmp" 2>/dev/null
chmod 0700 "$DATA_DIR" "$DATA_DIR/config" "$DATA_DIR/logs" "$DATA_DIR/run" 2>/dev/null

# Stable path for Samba's patched MODULESDIR.
ln -sfn "$MODDIR/lib" "$DATA_DIR/lib"

if [ ! -f "$DATA_DIR/config/config.json" ] && [ -f "$MODDIR/config/default-config.json" ]; then
  cp -f "$MODDIR/config/default-config.json" "$DATA_DIR/config/config.json"
  chmod 0600 "$DATA_DIR/config/config.json"
fi

# Rotate the small boot log so an unbootable module cannot fill /data.
BOOT_LOG="$DATA_DIR/logs/boot.log"
if [ -f "$BOOT_LOG" ] && [ "$(wc -c < "$BOOT_LOG" 2>/dev/null || echo 0)" -gt 262144 ]; then
  mv -f "$BOOT_LOG" "$BOOT_LOG.1" 2>/dev/null
fi

exit 0
