#!/usr/bin/env python3
"""Bundle the build host's tmux and non-system dylibs; no Homebrew at runtime."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

destination = Path(sys.argv[1]).resolve()
source = Path(shutil.which('tmux') or '')
if not source.is_file():
    sys.exit('tmux is needed on the build host (brew install tmux). End users do not need Homebrew.')
seen = {}
provenance = []

def bundle(source, target):
    source = source.resolve()
    if target in seen:
        if seen[target] != source:
            raise RuntimeError(f'Conflicting library names: {target.name}')
        return
    seen[target] = source
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source, target)
    target.chmod(0o755)
    provenance.append({'name': target.name, 'sha256_before_relocation': hashlib.sha256(source.read_bytes()).hexdigest()})
    # Homebrew formulae retain their upstream notices at the version root.
    # Ship them alongside the relocated libraries as well.
    for parent in source.parents:
        if (parent / 'INSTALL_RECEIPT.json').is_file():
            notices = destination / 'licenses' / parent.parent.name
            notices.mkdir(parents=True, exist_ok=True)
            for name in ['COPYING', 'LICENSE', 'LICENSE.md']:
                if (parent / name).is_file(): shutil.copy2(parent / name, notices / name)
            break
    lines = subprocess.check_output(['otool', '-L', str(source)], text=True).splitlines()[1:]
    for line in lines:
        dependency = line.strip().split(' (', 1)[0]
        if dependency.startswith(('/usr/lib/', '/System/Library/')):
            continue
        if not dependency.startswith('/'):
            raise RuntimeError(f'Unresolved build dependency: {dependency}')
        dependency_path = Path(dependency).resolve()
        if dependency_path == source:  # LC_ID_DYLIB, not a dependency
            continue
        local = destination / 'lib' / dependency_path.name
        bundle(dependency_path, local)
        relative = '@loader_path/' + os.path.relpath(local, target.parent)
        subprocess.run(['install_name_tool', '-change', dependency, relative, str(target)], check=True)
    if target.suffix == '.dylib':
        subprocess.run(['install_name_tool', '-id', '@loader_path/' + target.name, str(target)], check=True)
    subprocess.run(['codesign', '--force', '--sign', '-', str(target)], check=True)

bundle(source, destination / 'bin/tmux')
(destination / 'tmux-build.json').write_text(json.dumps(provenance, indent=2) + '\n')
subprocess.run([str(destination / 'bin/tmux'), '-V'], check=True)
