#!/usr/bin/env python3
"""Package the reviewed extension source, deterministically, for the Go server."""
from pathlib import Path
from zipfile import ZipFile, ZipInfo, ZIP_DEFLATED
root = Path(__file__).resolve().parent.parent
with ZipFile(root / 'agent_go/pkg/browserrelay/extension.zip', 'w', compression=ZIP_DEFLATED) as archive:
    for file in sorted((root / 'extensions/agentworks-chrome').iterdir()):
        if file.suffix not in ('.json', '.js', '.html', '.css'):
            continue
        info = ZipInfo(file.name, date_time=(2026, 10, 5, 0, 0, 0))
        info.compress_type = ZIP_DEFLATED
        info.external_attr = 0o644 << 16
        archive.writestr(info, file.read_bytes())
