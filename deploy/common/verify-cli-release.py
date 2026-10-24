#!/usr/bin/env python3
"""Refuse activation until the build's pinned CLI release finished publishing."""
import pathlib
import re
import sys
import urllib.error
import urllib.request

downloads = pathlib.Path(sys.argv[1])
base = (downloads / "cli-download-base.txt").read_text().strip()
if not re.fullmatch(r"https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/releases/download/cli-[0-9a-f]{8}(?:-arm64)?-\d{14}", base):
    sys.exit("Invalid pinned CLI release URL")
expected = (downloads / "cli-archives.sha256").read_bytes()
try:
    with urllib.request.urlopen(base + "/SHA256SUMS", timeout=60) as response:
        actual = response.read(4096)
except (urllib.error.URLError, OSError):
    sys.exit("CLI release is not published; publish this build before activating it")
if not expected or actual != expected:
    sys.exit("Published CLI release differs from this build; publish it before activating")
print("CLI release published: " + base)
