#!/system/bin/sh
# Uninstall: stop our processes and remove runtime state.
# User files under /storage/emulated/0/SMB are NEVER touched.

MODDIR=${0%/*}
DATA_DIR=/data/adb/android-smb-relay

mkdir -p "$DATA_DIR/run" 2>/dev/null
touch "$DATA_DIR/run/watchdog.disabled" 2>/dev/null

if [ -f "$DATA_DIR/run/asrd.pid" ]; then
  PID=$(cat "$DATA_DIR/run/asrd.pid" 2>/dev/null)
  case "$PID" in
    ''|*[!0-9]*) ;;
    *) kill -TERM "$PID" 2>/dev/null; sleep 2; kill -KILL "$PID" 2>/dev/null ;;
  esac
fi

# Samba master pid (if the daemon could not stop it).
if [ -f "$DATA_DIR/samba/pid/smbd.pid" ]; then
  PID=$(cat "$DATA_DIR/samba/pid/smbd.pid" 2>/dev/null)
  case "$PID" in
    ''|*[!0-9]*) ;;
    *) kill -TERM "$PID" 2>/dev/null ;;
  esac
fi

rm -f "$DATA_DIR/run/asr.sock" "$DATA_DIR/run/asrd.pid" "$DATA_DIR/run/watchdog.disabled" 2>/dev/null
rm -rf "$DATA_DIR/samba/pid" "$DATA_DIR/samba/lock" "$DATA_DIR/samba/cache" "$DATA_DIR/samba/ncalrpc" "$DATA_DIR/tmp" 2>/dev/null
rm -f "$DATA_DIR/lib" 2>/dev/null

# Configuration, logs and the initial credentials are intentionally kept so a
# reinstall can recover them. They contain no user share data.
exit 0
