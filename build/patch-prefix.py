#!/usr/bin/env python3
"""Rewrite the Termux prefix in a bundle of Samba ELF files.

Termux compiles Samba with MODULESDIR=/data/data/com.termux/files/usr/lib/samba
and no smb.conf option can override it. We relocate the binary to the module
data directory by editing the embedded C strings in place.
"""
import os
import sys

OLD = b"/data/data/com.termux/files/usr"


def patch_file(path, new_prefix):
    data = bytearray(open(path, "rb").read())
    count, pos = 0, 0
    while True:
        idx = data.find(OLD, pos)
        if idx < 0:
            break
        start = data.rfind(b"\x00", 0, idx)
        start = 0 if start < 0 else start + 1
        end = data.find(b"\x00", idx)
        if end < 0 or end - start > 8192:
            pos = idx + 1
            continue
        raw = bytes(data[start:end])
        if OLD not in raw:
            pos = idx + 1
            continue
        replaced = raw.replace(OLD, new_prefix)
        if len(replaced) > len(raw):
            raise SystemExit(f"{path}: replacement too long ({len(replaced)} > {len(raw)})")
        data[start:end] = replaced + b"\x00" * (len(raw) - len(replaced))
        count += 1
        pos = end
    if count:
        st = os.stat(path)
        with open(path, "wb") as fh:
            fh.write(data)
        os.chmod(path, st.st_mode)
    return count


def main():
    root = sys.argv[1]
    new_prefix = (sys.argv[2] if len(sys.argv) > 2 else "/data/adb/android-smb-relay").encode()
    if len(new_prefix) > len(OLD):
        raise SystemExit(f"new prefix too long: {len(new_prefix)} > {len(OLD)}")
    total = 0
    for dirpath, _, files in os.walk(root):
        for f in files:
            p = os.path.join(dirpath, f)
            if os.path.islink(p) or not os.path.isfile(p):
                continue
            with open(p, "rb") as fh:
                if fh.read(4) != b"\x7fELF":
                    continue
            n = patch_file(p, new_prefix)
            if n:
                print(f"patched {n:2d}: {os.path.relpath(p, root)}")
                total += n
    print(f"total strings patched: {total}")


if __name__ == "__main__":
    main()
