#!/system/bin/sh
# Stop the supervisor and SMB server. User data is never touched.

MODDIR=${0%/*}
MODDIR=${MODDIR%/scripts}
DATA_DIR=/data/adb/android-smb-relay
mkdir -p "$DATA_DIR/run" 2>/dev/null
touch "$DATA_DIR/run/watchdog.disabled" 2>/dev/null

if [ -f "$DATA_DIR/run/asrd.pid" ]; then
  PID=$(cat "$DATA_DIR/run/asrd.pid" 2>/dev/null)
  case "$PID" in
    ''|*[!0-9]*) ;;
    *)
      kill -TERM "$PID" 2>/dev/null
      i=0
      while kill -0 "$PID" 2>/dev/null && [ "$i" -lt 20 ]; do
        sleep 0.5
        i=$((i + 1))
      done
      kill -KILL "$PID" 2>/dev/null
      ;;
  esac
fi

if [ -f "$DATA_DIR/samba/pid/smbd.pid" ]; then
  PID=$(cat "$DATA_DIR/samba/pid/smbd.pid" 2>/dev/null)
  case "$PID" in
    ''|*[!0-9]*) ;;
    *) kill -TERM "$PID" 2>/dev/null ;;
  esac
fi

rm -f "$DATA_DIR/run/asr.sock" "$DATA_DIR/run/asrd.pid" "$DATA_DIR/samba/pid/smbd.pid" 2>/dev/null
echo "stopped"
