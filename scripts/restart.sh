#!/system/bin/sh
MODDIR=${0%/*}
MODDIR=${MODDIR%/scripts}
DATA_DIR=/data/adb/android-smb-relay

"$MODDIR/scripts/stop.sh"
sleep 1
"$MODDIR/scripts/start.sh"
