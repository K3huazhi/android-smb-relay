#!/usr/bin/env python3
"""Verify cached Termux debs against the project lock (SHA256)."""
import hashlib
import sys
from pathlib import Path

BUILD = Path(__file__).resolve().parent
ARCH = sys.argv[1] if len(sys.argv) > 1 else "aarch64"
DEBS = BUILD / "cache" / "debs" / ARCH
LOCK = DEBS / "PACKAGES.txt"
if not LOCK.exists():
    print("no lock at", LOCK)
    sys.exit(1)
bad = 0
for line in LOCK.read_text(encoding="utf-8").splitlines():
    if not line.strip():
        continue
    parts = line.split("\t")
    if len(parts) < 4:
        continue
    name, version, filename, expected = parts[0], parts[1], parts[2], parts[3]
    path = DEBS / filename.replace(":", "_")
    if not path.exists():
        print("MISSING", name, path)
        bad += 1
        continue
    actual = hashlib.sha256(path.read_bytes()).hexdigest()
    if actual != expected:
        print("HASH", name, actual, "!=", expected)
        bad += 1
print(f"{ARCH}: verified, failures={bad}")
sys.exit(1 if bad else 0)
