#!/usr/bin/env python3
"""PLAT-825-113: remove retired settings while the app is stopped.

Keep until the final installation moves. Back up changed manifests, never
secret stores, user notes, run evidence or generic-chat configuration.
"""
import argparse
import json
from pathlib import Path
import shutil

RETIRED_NOTIFICATIONS = {
    "slack_webhook_secret_name", "run_summary_slack_webhook_secret_names",
    "run_summary_gmail_connection_ids", "block_recipients", "instructions",
}


def simplify(manifest):
    result = json.loads(json.dumps(manifest))
    for key in ("run_retention_count", "execution_defaults"):
        result.pop(key, None)
    caps = result.get("capabilities")
    if not isinstance(caps, dict):
        return result
    caps.pop("cdp_ports", None)
    notifications = caps.get("notifications")
    if not isinstance(notifications, dict):
        return result
    # The old setter already stripped these backend-only names from selections.
    # Also cover older/manual saved references before dropping their evidence.
    names = {notifications.get("slack_webhook_secret_name")}
    names.update(notifications.get("run_summary_slack_webhook_secret_names") or [])
    names.discard(None)
    names = {name.strip() for name in names if isinstance(name, str) and name.strip()}
    for key in ("selected_secrets", "selected_global_secret_names"):
        if isinstance(caps.get(key), list):
            caps[key] = [name for name in caps[key] if not isinstance(name, str) or name.strip() not in names]
    for key in RETIRED_NOTIFICATIONS:
        notifications.pop(key, None)
    if not notifications:
        caps.pop("notifications")
    return result


def migrate(root, backups):
    crew = root / "_system/crew-data"
    folders = [root / "Workflow", crew if crew.is_dir() else root / "Crew"]
    for user in (root / "_users").glob("*"):
        if user.is_dir() and not user.is_symlink():
            folders.extend([user / "Workflow", user / "Crew"])
    changes = []
    for folder in folders:
        if folder.is_symlink():
            continue
        for project in sorted(folder.glob("*")):
            path = project / "workflow.json"
            if project.is_symlink() or not path.is_file() or path.is_symlink():
                continue
            before = json.loads(path.read_text())
            after = simplify(before)
            if before != after:
                changes.append((path, after))
    for path, after in changes:
        backup = backups / path.relative_to(root)
        backup.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, backup)
        path.write_text(json.dumps(after, indent=2, ensure_ascii=False) + "\n")
        print(json.dumps({"event": "workflow_settings_simplified", "manifest": str(path.relative_to(root))}))
    print(json.dumps({"event": "workflow_settings_simplification", "changed_files": len(changes)}))
    return len(changes)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workspace-dir", type=Path, required=True)
    parser.add_argument("--backup-dir", type=Path, required=True)
    args = parser.parse_args()
    migrate(args.workspace_dir, args.backup_dir)
