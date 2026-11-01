#!/usr/bin/env python3
"""One-off PLAT-856-442 migration of private Muse sync descriptors.

Run with the workspace docs directory after stopping the old sync process.
Native journal bytes, account files and conversation archives are untouched.
"""
import argparse
import json
from pathlib import Path
import re
import shutil
from datetime import datetime, timezone

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("docs", type=Path)
args = parser.parse_args()
root = args.docs.resolve() / "_system" / "crew-data"
stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
count = 0

def descriptor(value):
    if isinstance(value, dict) and "session_id" in value and "files" in value and "provider" not in value:
        value.update(provider="muse-cli", format="muse-jsonl", transport="structured")
        return True
    return False

for crew in root.iterdir() if root.exists() else []:
    if crew.is_symlink() or not crew.is_dir():
        continue
    mirror = crew / "native-mirror.json"
    manifests = list((crew / "native-sync").glob("*/manifest.json"))
    for path in [mirror, *manifests]:
        if not path.is_file() or path.is_symlink() or any((crew / p).is_symlink() for p in path.relative_to(crew).parents if str(p) != "."):
            continue
        value = json.loads(path.read_text())
        changed = False
        if path == mirror:
            conversation = value.get("conversation", {})
            if "muse" in conversation and "native" not in conversation:
                conversation["native"] = conversation.pop("muse")
                changed = descriptor(conversation["native"])
        else:
            changed = descriptor(value)
        if not changed:
            continue
        backup = path.with_name(path.name + ".before-native-format-" + stamp)
        shutil.copy2(path, backup)
        backup.chmod(0o600)
        path.write_text(json.dumps(value, separators=(",", ":")))
        path.chmod(0o600)
        if path != mirror and re.fullmatch(r"[0-9a-f-]{36}", path.parent.name):
            destination = path.parent.with_name("muse-cli-" + path.parent.name)
            if destination.exists():
                raise RuntimeError("native staging target already exists")
            path.parent.rename(destination)
        count += 1
print(f"Updated {count} private native sync descriptors; each prior record was backed up.")
