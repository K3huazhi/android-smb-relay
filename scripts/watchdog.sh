#!/system/bin/sh
# Background watchdog for the supervisor process. The daemon itself supervises
# smbd; this script only covers "the daemon died" (crash, OOM killer, etc.).

MODDIR=${0%/*}
MODDIR=${MODDIR%/scripts}
DATA_DIR=/data/adb/android-smb-relay
RUN="$DATA_DIR/run"
LOG="$DATA_DIR/logs/watchdog.log"
mkdir -p "$RUN" "$DATA_DIR/logs" 2>/dev/null

# One watchdog only. Recover a lock left behind by a killed watchdog.
if ! mkdir "$RUN/watchdog.lock" 2>/dev/null; then
  OLD=$(cat "$RUN/watchdog.lock/pid" 2>/dev/null)
  if [ -n "$OLD" ] && kill -0 "$OLD" 2>/dev/null; then
    exit 0
  fi
  rm -rf "$RUN/watchdog.lock" 2>/dev/null
  mkdir "$RUN/watchdog.lock" 2>/dev/null || exit 0
fi
echo $$ > "$RUN/watchdog.lock/pid"
trap 'rm -rf "$RUN/watchdog.lock" 2>/dev/null' EXIT INT TERM

record_crash() {
  NOW=$(date +%s 2>/dev/null || echo 0)
  echo "$NOW" >> "$RUN/watchdog.crashes"
  CUT=$((NOW - 600))
  TMP="$RUN/watchdog.crashes.tmp"
  : > "$TMP"
  while read -r t; do
    case "$t" in
      ''|*[!0-9]*) continue ;;
    esac
    [ "$t" -ge "$CUT" ] && echo "$t" >> "$TMP"
  done < "$RUN/watchdog.crashes"
  mv -f "$TMP" "$RUN/watchdog.crashes"
  wc -l < "$RUN/watchdog.crashes" | tr -d ' '
}

while true; do
  if [ -f "$RUN/watchdog.disabled" ]; then
    exit 0
  fi
  PID=$(cat "$RUN/asrd.pid" 2>/dev/null)
  if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
    sleep 30
    continue
  fi
  COUNT=$(record_crash)
  echo "$(date 2>/dev/null) asrd is not running (crashes in 10min: $COUNT)" >> "$LOG"
  if [ "$COUNT" -ge 5 ]; then
    echo "$(date 2>/dev/null) watchdog giving up after repeated failures" >> "$LOG"
    exit 1
  fi
  sh "$MODDIR/scripts/start.sh" --boot >> "$LOG" 2>&1
  sleep 10
done
