#!/system/bin/sh
# Magisk/KernelSU "Action" button: print a compact status and URLs.

MODDIR=${0%/*}
DATA_DIR=/data/adb/android-smb-relay
export ASR_DATA_DIR="$DATA_DIR"

echo "Android SMB Relay"
echo "================="
if [ -x "$MODDIR/bin/asrd" ]; then
  "$MODDIR/bin/asrd" ctl status 2>/dev/null || echo "daemon is not running"
  echo
  "$MODDIR/bin/asrd" ctl url 2>/dev/null
else
  echo "asrd binary missing"
fi

if [ -f "$DATA_DIR/config/initial-credentials.txt" ]; then
  echo
  echo "Initial credentials (root-only):"
  cat "$DATA_DIR/config/initial-credentials.txt" 2>/dev/null
fi
