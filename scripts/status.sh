#!/system/bin/sh
MODDIR=${0%/*}
MODDIR=${MODDIR%/scripts}
DATA_DIR=/data/adb/android-smb-relay
export ASR_DATA_DIR="$DATA_DIR"
exec "$MODDIR/bin/smbctl" "$@"
