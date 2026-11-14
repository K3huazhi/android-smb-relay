#!/usr/bin/env bash
# Android SMB Relay - Linux quick mount (anonymous by default).
#   sudo ./linux-mount.sh --ip 192.168.1.223
#   sudo ./linux-mount.sh --ip 192.168.1.223 --auth android
#   sudo ./linux-mount.sh --unmount
set -eu

IP="${ASR_PHONE_IP:-}"
SHARE="${ASR_SHARE:-Internal}"
MOUNT="${ASR_MOUNT:-/mnt/android-smb}"
USER="${ASR_SMB_USER:-}"
UNMOUNT=0

usage() {
  cat <<'EOF'
Usage: linux-mount.sh [--ip IP] [--share NAME] [--mount DIR] [--user USER] [--auth USER] [--unmount]
Default is anonymous (guest). --auth USER switches to username/password mode.
Environment: ASR_PHONE_IP ASR_SHARE ASR_MOUNT ASR_SMB_USER ASR_SMB_PASSWORD
EOF
}

AUTH=0
while [ $# -gt 0 ]; do
  case "$1" in
    --ip) IP="${2:-}"; shift 2 ;;
    --share) SHARE="${2:-}"; shift 2 ;;
    --mount) MOUNT="${2:-}"; shift 2 ;;
    --user|--auth) USER="${2:-}"; AUTH=1; shift 2 ;;
    --unmount) UNMOUNT=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

if [ "$(id -u)" != "0" ]; then
  echo "This script needs root (mount.cifs): sudo $0 ..." >&2
  exit 1
fi

if [ "$UNMOUNT" = "1" ]; then
  umount "$MOUNT" 2>/dev/null || true
  echo "Unmounted $MOUNT"
  exit 0
fi

if [ -z "$IP" ]; then
  read -r -p "Phone IP address: " IP
fi
if ! command -v mount.cifs >/dev/null 2>&1; then
  echo "mount.cifs is missing. Install cifs-utils." >&2
  exit 1
fi

mkdir -p "$MOUNT"
COMMON="uid=$(id -u),gid=$(id -g),iocharset=utf8,vers=3.0"
if [ "$AUTH" = "0" ] && [ -n "$USER" ]; then
  AUTH=1
fi

if [ "$AUTH" = "0" ]; then
  mount -t cifs "//$IP/$SHARE" "$MOUNT" -o "guest,$COMMON"
  echo "Mounted //$IP/$SHARE at $MOUNT (anonymous)"
else
  if [ -n "${ASR_SMB_PASSWORD:-}" ]; then
    PASSWORD="$ASR_SMB_PASSWORD"
  else
    read -r -s -p "SMB password for $USER: " PASSWORD
    echo
  fi
  CRED="$(mktemp)"
  trap 'rm -f "$CRED"' EXIT INT TERM
  chmod 600 "$CRED"
  printf 'username=%s\npassword=%s\n' "$USER" "$PASSWORD" > "$CRED"
  unset PASSWORD
  mount -t cifs "//$IP/$SHARE" "$MOUNT" -o "credentials=$CRED,$COMMON"
  echo "Mounted //$IP/$SHARE at $MOUNT"
fi
echo "Unmount: sudo $0 --mount $MOUNT --unmount"
