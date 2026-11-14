#!/system/bin/sh
# late_start service: non-blocking. Start the supervisor and its shell watchdog.

MODDIR=${0%/*}
DATA_DIR=/data/adb/android-smb-relay
LOG="$DATA_DIR/logs/boot.log"

mkdir -p "$DATA_DIR/logs" "$DATA_DIR/run" 2>/dev/null
{
  echo "--- service.sh $(date 2>/dev/null) ---"
  echo "MODDIR=$MODDIR"
} >> "$LOG" 2>/dev/null

# Wait for Android boot completion, but never longer than two minutes. The
# daemon has its own network/storage gates, so a slow boot is not fatal.
i=0
while [ "$(getprop sys.boot_completed 2>/dev/null)" != "1" ] && [ "$i" -lt 120 ]; do
  sleep 1
  i=$((i + 1))
done
echo "boot_completed wait=${i}s" >> "$LOG" 2>/dev/null

if [ -x "$MODDIR/scripts/start.sh" ]; then
  sh "$MODDIR/scripts/start.sh" --boot >> "$LOG" 2>&1
else
  echo "start.sh missing" >> "$LOG" 2>/dev/null
fi

if [ -x "$MODDIR/scripts/watchdog.sh" ]; then
  nohup sh "$MODDIR/scripts/watchdog.sh" >> "$DATA_DIR/logs/watchdog.log" 2>&1 &
fi

exit 0
