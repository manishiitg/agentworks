#!/usr/bin/env python3
"""Package the native uv executable for the existing immutable helper runtime."""
import hashlib
import io
import os
from pathlib import Path
import platform
import sys
import tarfile
import urllib.request
import zipfile

# Official release asset SHA-256 values, not a runtime latest-version lookup:
# https://github.com/astral-sh/uv/releases/tag/0.13.7
VERSION = '0.13.7'
ASSETS = {
    ('Linux', 'x86_64'): ('x86_64-unknown-linux-gnu', 'c5221ff41b7892bfc223543883600125f6a47fe845093e0f5bdff4f359196d83'),
    ('Linux', 'aarch64'): ('aarch64-unknown-linux-gnu', '71279f8d3c67241e807b9d8db872d1bf541579fb9f08b7d1eb1682196d1e57dc'),
    ('Darwin', 'arm64'): ('aarch64-apple-darwin', '81b811b57e4624df7ed3aac148e0359100d9962f3d9dce00aec533dc2e086731'),
    ('Darwin', 'x86_64'): ('x86_64-apple-darwin', '20c47e4c1f630b8abaff8c291fa9b9aa300fef92c4fbff95adf198b55287cda7'),
    ('Windows', 'AMD64'): ('x86_64-pc-windows-msvc', '0cdc5b38fe5a8168ed6a25814f5ba43c21d313dd3187314fccb37b7cec46e021'),
}


def build(output):
    selected = ASSETS.get((platform.system(), platform.machine()))
    if selected is None:
        return  # Other targets retain the declared pip fallback.
    target, checksum = selected
    windows = platform.system() == 'Windows'
    filename = f'uv-{target}.' + ('zip' if windows else 'tar.gz')
    cache = Path(os.environ.get('XDG_CACHE_HOME', Path.home() / '.cache')) / 'agentworks/python-installers' / VERSION
    cache.mkdir(parents=True, exist_ok=True)
    archive = cache / filename
    data = archive.read_bytes() if archive.is_file() else b''
    if hashlib.sha256(data).hexdigest() != checksum:
        request = urllib.request.Request(f'https://github.com/astral-sh/uv/releases/download/{VERSION}/{filename}', headers={'User-Agent': 'AgentWorks-runtime-build'})
        with urllib.request.urlopen(request, timeout=60) as response:
            data = response.read()
        if hashlib.sha256(data).hexdigest() != checksum:
            raise ValueError('The Python package installer download failed its checksum.')
        archive.write_bytes(data)
    if windows:
        with zipfile.ZipFile(io.BytesIO(data)) as package:
            binary = package.read('uv.exe')
    else:
        with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as package:
            binary = package.extractfile(f'uv-{target}/uv').read()
    destination = output / ('uv.exe' if windows else 'uv')
    output.mkdir(parents=True, exist_ok=True)
    (output / 'uv-LICENSE-MIT').write_bytes((Path(__file__).resolve().parents[1] / 'packages/python-installer/LICENSE-MIT').read_bytes())
    if not destination.is_file() or hashlib.sha256(destination.read_bytes()).digest() != hashlib.sha256(binary).digest():
        temporary = destination.with_suffix('.tmp')
        temporary.write_bytes(binary)
        temporary.chmod(0o555)
        temporary.replace(destination)


if __name__ == '__main__':
    build(Path(sys.argv[1]))
