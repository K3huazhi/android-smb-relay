#!/usr/bin/env python3
"""Download the pinned Termux arm64 package set required by Samba.

The public mirror occasionally serves a pool file whose hash differs from its
index. We therefore record the *actual* downloaded SHA256 in the lock file and
refuse to build later if the cached file changes.
"""
import concurrent.futures
import hashlib
import os
import re
import shutil
import sys
import time
import urllib.request
from pathlib import Path

BASE = "https://packages.termux.dev/apt/termux-main"
BUILD = Path(__file__).resolve().parent
CACHE = BUILD / "cache"


def parse_packages(path):
    text = Path(path).read_text(encoding="utf-8", errors="replace")
    pkgs = {}
    for stanza in text.split("\n\n"):
        if not stanza.strip():
            continue
        fields = {}
        key = None
        for line in stanza.splitlines():
            if line.startswith(" ") and key:
                fields[key] += "\n" + line.strip()
                continue
            if ":" in line:
                key, val = line.split(":", 1)
                fields[key.strip()] = val.strip()
        if "Package" in fields:
            pkgs[fields["Package"]] = fields
    return pkgs


def parse_depends(value):
    out = []
    if not value:
        return out
    for group in value.split(","):
        group = group.strip()
        if not group:
            continue
        alt = group.split("|")[0].strip()
        name = re.split(r"[\s(]", alt, maxsplit=1)[0].strip()
        if name:
            out.append(name)
    return out


def resolve(pkgs, roots):
    seen, order, stack, missing = set(), [], list(roots), set()
    while stack:
        name = stack.pop()
        if name in seen:
            continue
        seen.add(name)
        pkg = pkgs.get(name)
        if not pkg:
            missing.add(name)
            continue
        order.append(pkg)
        for dep in parse_depends(pkg.get("Depends", "")):
            if dep not in seen:
                stack.append(dep)
    return order, missing


def local_name(pkg):
    return os.path.basename(pkg["Filename"]).replace(":", "_")


def fetch(url, dest, expected):
    part = dest + ".part"
    if os.path.exists(dest) and os.path.getsize(dest) == expected:
        return
    if os.path.exists(dest) and os.path.getsize(dest) != expected:
        os.replace(dest, part) if not os.path.exists(part) else os.remove(dest)
    last = None
    for attempt in range(12):
        got = os.path.getsize(part) if os.path.exists(part) else 0
        if got == expected:
            break
        headers = {"User-Agent": "asr-build/0.1"}
        if got:
            headers["Range"] = f"bytes={got}-"
        try:
            req = urllib.request.Request(url, headers=headers)
            with urllib.request.urlopen(req, timeout=60) as resp, open(part, "ab") as fh:
                while True:
                    chunk = resp.read(1 << 20)
                    if not chunk:
                        break
                    fh.write(chunk)
        except Exception as exc:  # noqa: BLE001
            last = exc
        if os.path.exists(part) and os.path.getsize(part) == expected:
            break
        time.sleep(1 + attempt)
    got = os.path.getsize(part) if os.path.exists(part) else 0
    if got != expected:
        raise SystemExit(f"incomplete {url}: got {got}, expected {expected}, last={last}")
    os.replace(part, dest)


def ensure_index(arch):
    CACHE.mkdir(parents=True, exist_ok=True)
    path = CACHE / f"Packages-{arch}"
    if path.exists() and path.stat().st_size > 1000:
        return path
    bundled = BUILD / f"Packages-{arch}"
    if bundled.exists() and bundled.stat().st_size > 1000:
        CACHE.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(bundled, path)
        return path
    url = f"{BASE}/dists/stable/main/binary-{arch}/Packages"
    print("fetch index", url)
    req = urllib.request.Request(url, headers={"User-Agent": "asr-build/0.1"})
    with urllib.request.urlopen(req, timeout=120) as resp:
        path.write_bytes(resp.read())
    return path


def main(arch):
    index = ensure_index(arch)
    pkgs = parse_packages(index)
    order, missing = resolve(pkgs, ["samba", "zstd"])
    outdir = CACHE / "debs" / arch
    outdir.mkdir(parents=True, exist_ok=True)
    pending = []
    for pkg in order:
        dest = outdir / local_name(pkg)
        expected = int(pkg["Size"])
        if dest.exists() and dest.stat().st_size == expected:
            print(f"cached   {pkg['Package']:<22} {pkg['Version']}")
            continue
        pending.append((pkg, f"{BASE}/{pkg['Filename']}", str(dest), expected))

    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        futures = {pool.submit(fetch, url, dest, exp): (pkg, dest) for pkg, url, dest, exp in pending}
        for fut in concurrent.futures.as_completed(futures):
            pkg, dest = futures[fut]
            fut.result()
            print(f"got      {pkg['Package']:<22} {pkg['Version']}")

    lines = []
    for pkg in order:
        dest = outdir / local_name(pkg)
        digest = hashlib.sha256(dest.read_bytes()).hexdigest()
        lines.append(f"{pkg['Package']}\t{pkg['Version']}\t{local_name(pkg)}\t{digest}")
    lock = outdir / "PACKAGES.txt"
    lock.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"{arch}: {len(order)} packages, lock {lock}")
    if missing:
        print("unresolved/virtual deps:", ", ".join(sorted(missing)))


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else "aarch64")
