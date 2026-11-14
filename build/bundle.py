#!/usr/bin/env python3
"""Assemble a self-contained Android bundle from cached Termux .deb files.

Only ELF closures of the SMB daemons are kept: smbd, smbpasswd, smbcontrol,
testparm, their shared libraries and Samba's plugin tree (pdb/vfs/...).
"""
import argparse
import os
import shutil
import subprocess
from pathlib import Path

BUILD = Path(__file__).resolve().parent
CACHE = BUILD / "cache"
BINS = ["smbd", "smbpasswd", "smbcontrol", "testparm"]
# Samba >= 4.15 serves every named pipe (IPC$: srvsvc, lsarpc, samr, winreg...)
# from samba-dcerpcd, which smbd spawns from its compiled-in LIBEXECDIR. Without
# it smbd cannot open any pipe: the spawn fails, the ready pipe hits EPIPE and
# the client sees NT_STATUS_CONNECTION_DISCONNECTED (0xC000020C). Clients such
# as MT Manager enumerate shares over srvsvc first, so the whole mount fails.
LIBEXEC = [
    "samba-dcerpcd",
    "rpcd_classic",
    "rpcd_epmapper",
    "rpcd_fsrvp",
    "rpcd_lsad",
    "rpcd_mdssvc",
    "rpcd_spoolss",
    "rpcd_winreg",
]


def run(cmd, **kw):
    return subprocess.run(cmd, check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, **kw).stdout


def elf_needed(path):
    res = subprocess.run(["readelf", "-d", path], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    return [line.split("[", 1)[1].split("]", 1)[0] for line in res.stdout.splitlines() if "(NEEDED)" in line]


def extract(arch):
    debs = CACHE / "debs" / arch
    root = CACHE / "extract" / arch
    termux = root / "termux"
    if root.exists():
        shutil.rmtree(root)
    termux.mkdir(parents=True)
    for deb in sorted(debs.glob("*.deb")):
        pkgdir = root / "pkgs" / deb.stem
        pkgdir.mkdir(parents=True, exist_ok=True)
        run(["dpkg-deb", "-x", str(deb), str(pkgdir)])
        src = pkgdir / "data" / "data" / "com.termux" / "files" / "usr"
        if not src.is_dir():
            continue
        for dirpath, dirs, files in os.walk(src):
            dirs[:] = [d for d in dirs if not os.path.islink(os.path.join(dirpath, d))]
            rel = Path(dirpath).relative_to(src)
            dst_dir = termux / rel
            dst_dir.mkdir(parents=True, exist_ok=True)
            for f in files:
                s = Path(dirpath) / f
                d = dst_dir / f
                if d.exists() or d.is_symlink():
                    if d.is_dir() and not d.is_symlink():
                        shutil.rmtree(d)
                    else:
                        d.unlink()
                if s.is_symlink():
                    os.symlink(os.readlink(s), d)
                else:
                    shutil.copy2(s, d)
    return termux


def bundle(arch, termux, out):
    libmap = {}
    for p in (termux / "lib").rglob("*"):
        if p.is_file() or p.is_symlink():
            libmap.setdefault(p.name, p)
    seeds = [termux / "bin" / b for b in BINS]
    seeds += [termux / "libexec" / "samba" / b for b in LIBEXEC]
    needed, seen, stack, missing = [], set(), seeds, set()
    while stack:
        p = stack.pop()
        rp = os.path.realpath(p)
        if rp in seen:
            continue
        seen.add(rp)
        needed.append(p)
        for n in elf_needed(p):
            if n in libmap:
                stack.append(libmap[n])
            else:
                missing.add(n)
    if out.exists():
        shutil.rmtree(out)
    (out / "bin").mkdir(parents=True)
    (out / "lib").mkdir(parents=True)
    (out / "libexec" / "samba").mkdir(parents=True)
    for p in needed:
        if p.parent == termux / "bin" and p.name in BINS:
            shutil.copy2(p, out / "bin" / p.name)
        elif p.parent == termux / "libexec" / "samba":
            shutil.copy2(p, out / "libexec" / "samba" / p.name)
    for p in needed:
        if "/lib/" in str(p).replace("\\", "/"):
            shutil.copy2(os.path.realpath(p), out / "lib" / p.name)
    src_samba = termux / "lib" / "samba"
    if src_samba.is_dir():
        shutil.copytree(src_samba, out / "lib" / "samba", symlinks=True)
    (out / "MISSING.txt").write_text("\n".join(sorted(missing)), encoding="utf-8")
    total = sum(p.stat().st_size for p in out.rglob("*") if p.is_file())
    print(f"{arch}: {len(needed)} ELF, {total/1048576:.1f} MiB, missing={sorted(missing)}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("arch", nargs="?", default="aarch64")
    ap.add_argument("--out", default=None)
    args = ap.parse_args()
    termux = extract(args.arch)
    out = Path(args.out) if args.out else CACHE / f"bundle-{args.arch}"
    bundle(args.arch, termux, out)


if __name__ == "__main__":
    main()
