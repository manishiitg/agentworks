#!/usr/bin/env python3
"""List route-backed schedules with explicit extra full-workflow messages.

Read-only: prints manifest path, schedule ID and matching message positions as
JSON lines, never message content. Operators review the listed schedules, back
up their manifests, then remove obsolete messages through the normal owner API.
No automatic repair, workflow execution or runtime changes are performed.
"""
import argparse
import json
import os
from pathlib import Path
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workspace-root", required=True, help="Filesystem root of workspace documents")
    args = parser.parse_args()
    root = Path(args.workspace_root).resolve(strict=True)
    if not root.is_dir():
        parser.error("workspace root must be a directory")
    errors = 0
    for directory, folders, files in os.walk(root, followlinks=False):
        folders[:] = sorted(name for name in folders if name not in {".git", "node_modules", "__pycache__"})
        if "workflow.json" not in files:
            continue
        target = Path(directory) / "workflow.json"
        if target.is_symlink():
            continue
        try:
            manifest = json.loads(target.read_text())
            for schedule in manifest.get("schedules", []):
                if not schedule.get("route_selections"):
                    continue
                matches = [f"messages[{i}]" for i, message in enumerate(schedule.get("messages") or [])
                           if isinstance(message, str) and "run_full_workflow" in message]
                for item_index, item in enumerate(schedule.get("calendar_items") or []):
                    matches.extend(f"calendar_items[{item_index}].messages[{i}]"
                                   for i, message in enumerate(item.get("messages") or [])
                                   if isinstance(message, str) and "run_full_workflow" in message)
                if matches:
                    print(json.dumps({"manifest": str(target.relative_to(root)), "schedule_id": schedule.get("id"),
                                      "enabled": schedule.get("enabled", False), "message_positions": matches}))
        except (OSError, ValueError, AttributeError, TypeError):
            errors += 1
            print(f"Could not inspect {target.relative_to(root)}; listing is incomplete.", file=sys.stderr)
    return 2 if errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
