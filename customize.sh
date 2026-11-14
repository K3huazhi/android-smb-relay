#!/system/bin/sh
# Magisk / KernelSU installer customization.
# This script is sourced by the module installer. Do not exit early.

DATA_DIR=/data/adb/android-smb-relay

ui_print "- Android SMB Relay $MODID"

case "$ARCH" in
  arm64) ;;
  *)
    abort "! Unsupported architecture: $ARCH. This release ships arm64-v8a only."
    ;;
esac

# All module files are root-owned. Binaries are executable, data is not.
set_perm_recursive "$MODPATH" 0 0 0755 0644
set_perm_recursive "$MODPATH/bin" 0 0 0755 0755
set_perm_recursive "$MODPATH/scripts" 0 0 0755 0755
if [ -d "$MODPATH/libexec" ]; then
  set_perm_recursive "$MODPATH/libexec" 0 0 0755 0755
fi
set_perm "$MODPATH/module.prop" 0 0 0644
set_perm "$MODPATH/customize.sh" 0 0 0755
set_perm "$MODPATH/post-fs-data.sh" 0 0 0755
set_perm "$MODPATH/service.sh" 0 0 0755
set_perm "$MODPATH/uninstall.sh" 0 0 0755
set_perm "$MODPATH/action.sh" 0 0 0755

# Persistent state lives outside the module directory so a module upgrade
# (which replaces this directory) never loses configuration, users or logs.
mkdir -p "$DATA_DIR/config" "$DATA_DIR/logs" "$DATA_DIR/run" "$DATA_DIR/samba" "$DATA_DIR/tmp" 2>/dev/null
chmod 0700 "$DATA_DIR" "$DATA_DIR/config" "$DATA_DIR/logs" "$DATA_DIR/run" "$DATA_DIR/tmp" 2>/dev/null

if [ ! -f "$DATA_DIR/config/config.json" ]; then
  if [ -f "$MODPATH/config/default-config.json" ]; then
    cp -f "$MODPATH/config/default-config.json" "$DATA_DIR/config/config.json"
    chmod 0600 "$DATA_DIR/config/config.json"
    ui_print "- Installed default configuration"
  fi
fi

# Samba's compiled-in MODULESDIR and LIBEXECDIR are both patched to this data
# directory. Keep stable symlinks so Samba can load modules and start the
# samba-dcerpcd named-pipe helper from the module itself.
ln -sfn "$MODPATH/lib" "$DATA_DIR/lib"
ln -sfn "$MODPATH/libexec" "$DATA_DIR/libexec"
chmod 0700 "$DATA_DIR" 2>/dev/null

ui_print "- Data directory: $DATA_DIR"
ui_print "- Default share: Internal -> /storage/emulated/0 (anonymous read/write)"
ui_print "- After the next reboot open http://<phone-ip>:8080"
ui_print "- WebUI initial password: su -c 'cat $DATA_DIR/config/initial-credentials.txt'"
ui_print "- No SMB account or password is created; connect via \\\\<phone-ip>\\Internal"
ui_print "- CLI: su -c '/data/adb/modules/android-smb-relay/bin/smbctl credentials'"
