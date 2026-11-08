#!/usr/bin/env python3
"""Build dependency-free distribution archives; never include caches or test data."""
import io
import hashlib
import json
import os
from pathlib import Path
import sys
import tarfile
import urllib.request
import zipfile

root = Path(__file__).resolve().parents[1]
out = Path(sys.argv[1])
out.mkdir(parents=True, exist_ok=True)
node = root / 'packages/playwright'
with tarfile.open(out / 'agentworks-playwright.tgz', 'w:gz') as archive:
    for name in ['package.json', 'index.cjs', 'index.mjs', 'index.d.ts', 'README.md']:
        data = (node / name).read_bytes()
        entry = tarfile.TarInfo('package/' + name)
        entry.size, entry.mode, entry.mtime = len(data), 0o644, 0
        archive.addfile(entry, io.BytesIO(data))
python = root / 'packages/playwright-python'
# Bundle the one pure-Python dependency of our own live-view helper. Ordinary
# script dependencies still belong to the script's requirements.txt.
# Release metadata: https://pypi.org/pypi/websocket-client/1.9.2/json
wheel_name = 'websocket_client-1.9.2-py3-none-any.whl'
wheel_hash = 'e1a673830a9c7bfa47b1cd3d5e4178f4c9651d80a4eab02c9c23a1c3ec6250ce'
wheel_url = 'https://files.pythonhosted.org/packages/d5/d2/cc4dc1271e464942db7ee278baae2daa99ee77cb2af744025c04da585a3e/' + wheel_name
cache = Path(os.environ.get('XDG_CACHE_HOME', Path.home() / '.cache')) / 'agentworks/python-wheels'
cache.mkdir(parents=True, exist_ok=True)
wheel_file = cache / wheel_name
wheel = wheel_file.read_bytes() if wheel_file.exists() else b''
if hashlib.sha256(wheel).hexdigest() != wheel_hash:
    with urllib.request.urlopen(wheel_url, timeout=30) as response:
        wheel = response.read()
    if hashlib.sha256(wheel).hexdigest() != wheel_hash:
        raise ValueError('The browser helper dependency download failed its checksum.')
    wheel_file.write_bytes(wheel)
with zipfile.ZipFile(out / 'agentworks-playwright-python.zip', 'w', zipfile.ZIP_DEFLATED) as archive:
    for file in [python / 'pyproject.toml', python / 'README.md', *sorted((python / 'agentworks_playwright').glob('*.py'))]:
        archive.writestr('agentworks-playwright-python/' + str(file.relative_to(python)), file.read_bytes())
    with zipfile.ZipFile(io.BytesIO(wheel)) as dependency:
        for name in dependency.namelist():
            if name.startswith('websocket/') and '/tests/' not in name and not name.endswith('/'):
                archive.writestr('agentworks-playwright-python/runtime-dependencies/' + name, dependency.read(name))
        archive.writestr('agentworks-playwright-python/runtime-dependencies/websocket/LICENSE', dependency.read('websocket_client-1.9.2.dist-info/licenses/LICENSE'))
print(json.dumps({'packages': sorted(file.name for file in out.iterdir())}))
