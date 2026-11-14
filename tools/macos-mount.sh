#!/usr/bin/env bash
# Android SMB Relay - macOS quick mount (anonymous by default).
#   ./macos-mount.sh --ip 192.168.1.223
#   ./macos-mount.sh --unmount
set -eu

IP="${ASR_PHONE_IP:-}"
SHARE="${ASR_SHARE:-Internal}"
MOUNT="${ASR_MOUNT:-$HOME/android-smb}"
USER="${ASR_SMB_USER:-guest}"
UNMOUNT=0

usage() {
  cat <<'EOF'
Usage: macos-mount.sh [--ip IP] [--share NAME] [--mount DIR] [--user USER] [--unmount]
Default uses the macOS guest account. mount_smbfs may prompt if a user is set.
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --ip) IP="${2:-}"; shift 2 ;;
    --share) SHARE="${2:-}"; shift 2 ;;
    --mount) MOUNT="${2:-}"; shift 2 ;;
    --user) USER="${2:-}"; shift 2 ;;
    --unmount) UNMOUNT=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

if [ "$UNMOUNT" = "1" ]; then
  umount "$MOUNT" 2>/dev/null || true
  echo "Unmounted $MOUNT"
  exit 0
fi

if [ -z "$IP" ]; then
  read -r -p "Phone IP address: " IP
fi
mkdir -p "$MOUNT"
mount_smbfs "//$USER@$IP/$SHARE" "$MOUNT"
echo "Mounted //$IP/$SHARE at $MOUNT"
echo "Unmount: $0 --mount $MOUNT --unmount"
