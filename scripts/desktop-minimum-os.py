#!/usr/bin/env python3
"""Derive the packaged app's OS floor from its native resources and Electron 33."""
from pathlib import Path
import subprocess
import sys

# Electron 33 requires macOS 11; native helper/dylib load commands may raise it.
minimum = (11, 0, 0)
source = 'Electron 33'
magics = {bytes.fromhex(x) for x in (
    'feedface', 'cefaedfe', 'feedfacf', 'cffaedfe',
    'cafebabe', 'bebafeca', 'cafebabf', 'bfbafeca',
)}
for binary in Path(sys.argv[1]).rglob('*'):
    if not binary.is_file():
        continue
    with binary.open('rb') as stream:
        if stream.read(4) not in magics:
            continue
    legacy = False
    for line in subprocess.check_output(['otool', '-l', str(binary)], text=True).splitlines():
        parts = line.split()
        if len(parts) < 2:
            continue
        if parts[0] == 'cmd':
            legacy = parts[1] == 'LC_VERSION_MIN_MACOSX'
        if parts[0] == 'minos' or (legacy and parts[0] == 'version'):
            version = tuple(int(n) for n in parts[1].split('.'))
            version = (version + (0, 0, 0))[:3]
            if version > minimum:
                minimum, source = version, str(binary)
print(f'Native minimum macOS {".".join(map(str, minimum))}: {source}', file=sys.stderr)
print('.'.join(map(str, minimum)))
