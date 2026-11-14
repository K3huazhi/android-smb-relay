#!/system/bin/sh
# Start the asrd supervisor. Safe to call repeatedly.
# Usage: start.sh [--boot]

MODDIR=${0%/*}
MODDIR=${MODDIR%/scripts}
DATA_DIR=/data/adb/android-smb-relay
BOOT_FLAG=""
[ "$1" = "--boot" ] && BOOT_FLAG="--boot"

mkdir -p "$DATA_DIR/logs" "$DATA_DIR/run" "$DATA_DIR/config" "$DATA_DIR/samba" "$DATA_DIR/tmp" 2>/dev/null
chmod 0700 "$DATA_DIR" "$DATA_DIR/logs" "$DATA_DIR/run" 2>/dev/null
export ASR_DATA_DIR="$DATA_DIR"
export ASR_MODULE_DIR="$MODDIR"
export ASR_WEBUI_DIR="$MODDIR/webui"

rm -f "$DATA_DIR/run/watchdog.disabled" 2>/dev/null

if [ -f "$DATA_DIR/run/asrd.pid" ]; then
  PID=$(cat "$DATA_DIR/run/asrd.pid" 2>/dev/null)
  case "$PID" in
    ''|*[!0-9]*) rm -f "$DATA_DIR/run/asrd.pid" ;;
    *)
      # A stale PID can be reused by an unrelated process after reboot.
      # Only trust the pid file if /proc/$PID is actually our asrd.
      CMDLINE=$(tr '\0' ' ' < "/proc/$PID/cmdline" 2>/dev/null)
      case "$CMDLINE" in
        *asrd*) echo "asrd already running (pid $PID)"; exit 0 ;;
        *) rm -f "$DATA_DIR/run/asrd.pid" ;;
      esac
      ;;
  esac
fi

OUT="$DATA_DIR/logs/service.out"
if [ -f "$OUT" ] && [ "$(wc -c < "$OUT" 2>/dev/null || echo 0)" -gt 1048576 ]; then
  mv -f "$OUT" "$OUT.1" 2>/dev/null
fi

nohup "$MODDIR/bin/asrd" serve \
  --module-dir "$MODDIR" \
  --data-dir "$DATA_DIR" \
  --webui "$MODDIR/webui" \
  $BOOT_FLAG </dev/null >>"$OUT" 2>&1 &

i=0
while [ "$i" -lt 30 ]; do
  if [ -S "$DATA_DIR/run/asr.sock" ]; then
    echo "asrd started"
    exit 0
  fi
  sleep 0.5
  i=$((i + 1))
done

echo "asrd did not create its control socket; see $OUT and $DATA_DIR/logs/asrd.log" >&2
tail -20 "$DATA_DIR/logs/asrd.log" 2>/dev/null
exit 1
