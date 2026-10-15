#!/usr/bin/env python3
"""Provision the pinned FFprobe used by core dashboard video exports."""
import base64
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import sys
import tarfile
import tempfile
import urllib.request

# Fixed platform archives; no npm scripts are executed by this installer.
PACKAGES = {
    "x86_64": ("linux-x64", "D3UeqTLYPNs7pBWPLUYGehPdRVqU8eACox4OZy3pZUZatxye2YKlvBwEfaLdL1v2Z4FOAlLUhms0kY8m8kqSRA=="),
    "aarch64": ("linux-arm64", "X1VvWtlLs6ScP73biVLuHD5ohKJKsMTa0vafCESOen4mOoNeLAYbxOVxDWAdFz9cpZgRiloFj5QD6nDj8E28yQ=="),
}


def install(root):
    package, integrity = PACKAGES[platform.machine()]
    destination = root / "tools/bin/ffprobe"
    marker = root / "tools/ffprobe.json"
    if destination.is_file() and marker.is_file():
        try:
            saved = json.loads(marker.read_text())
        except (ValueError, OSError):
            saved = {}
        if saved.get("integrity") == integrity and saved.get("binary_sha256") == hashlib.sha256(destination.read_bytes()).hexdigest():
            subprocess.run([str(destination), "-version"], check=True, stdout=subprocess.DEVNULL)
            print("ffprobe: pinned tool unchanged")
            return
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=root / "tools") as name:
        stage = Path(name)
        archive = stage / "ffprobe.tgz"
        url = f"https://registry.npmjs.org/@ffprobe-installer/{package}/-/{package}-5.2.0.tgz"
        with urllib.request.urlopen(url, timeout=120) as response, archive.open("wb") as output:
            while chunk := response.read(1024 * 1024):
                output.write(chunk)
        digest = hashlib.sha512(archive.read_bytes()).digest()
        if digest != base64.b64decode(integrity):
            raise RuntimeError("FFprobe archive checksum mismatch")
        candidate = stage / "ffprobe"
        with tarfile.open(archive) as package_archive:
            binary = package_archive.extractfile("package/ffprobe")
            if binary is None:
                raise RuntimeError("The verified archive has no FFprobe binary")
            with binary:
                candidate.write_bytes(binary.read())
        candidate.chmod(0o755)
        subprocess.run([str(candidate), "-version"], check=True, stdout=subprocess.DEVNULL)
        checksum = hashlib.sha256(candidate.read_bytes()).hexdigest()
        os.replace(candidate, destination)
        record = stage / "ffprobe.json"
        record.write_text(json.dumps({"version": "5.2.0", "package": package, "integrity": integrity, "binary_sha256": checksum}) + "\n")
        os.replace(record, marker)
    print("ffprobe: installed pinned tool")


if __name__ == "__main__":
    try:
        install(Path(os.environ["REMOTE_APP"]))
    except Exception as error:
        print(f"Video export setup: could not prepare FFprobe: {error}", file=sys.stderr)
        sys.exit(1)
