#!/usr/bin/env python3
"""Create a deterministic module zip (fixed timestamps, sorted entries)."""
import os
import stat
import sys
import zipfile
from pathlib import Path

FIXED = (2026, 1, 1, 0, 0, 0)


def main():
    stage = Path(sys.argv[1])
    out = Path(sys.argv[2])
    out.parent.mkdir(parents=True, exist_ok=True)
    if out.exists():
        out.unlink()
    entries = []
    for p in stage.rglob("*"):
        if p.is_symlink():
            continue
        rel = p.relative_to(stage).as_posix()
        entries.append((rel, p))
    entries.sort(key=lambda x: x[0])
    with zipfile.ZipFile(out, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as z:
        for rel, p in entries:
            if p.is_dir():
                info = zipfile.ZipInfo(rel + "/", FIXED)
                info.external_attr = (stat.S_IFDIR | 0o755) << 16
                z.writestr(info, b"")
                continue
            info = zipfile.ZipInfo(rel, FIXED)
            mode = 0o755 if os.access(p, os.X_OK) or p.suffix == ".sh" else 0o644
            info.external_attr = (stat.S_IFREG | mode) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            z.writestr(info, p.read_bytes())
    print(out, out.stat().st_size)


if __name__ == "__main__":
    main()
