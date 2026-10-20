#!/usr/bin/env python3
"""One-off migration from exclusive Local projects to optional laptop tools.

Run on a server after its owner authorizes the rollout. Back up each changed
manifest before writing it. Project/chat IDs, folder contents and sharing stay
unchanged. Remove this tool after every server has moved.
"""
import argparse
import json
from pathlib import Path
import shutil


def migrate(workspace_root, registry_path, backup_dir):
    root = workspace_root.resolve(strict=True)
    records = json.loads(registry_path.read_text())["projects"]
    changes = []
    for record in records.values():
        origin, folder, owner = (record.get(key) for key in ("product", "folder", "owner_id"))
        if origin not in ("work", "code"):
            continue
        relative = record.get("canonical_root") or (
            f"Crew/{folder}" if record.get("shared") else
            f"_users/{owner}/Chats/{'Work' if origin == 'work' else 'Code'}/projects/{folder}"
        )
        project_root = (root / relative).resolve(strict=True)
        project_root.relative_to(root)
        product_path = project_root / "product.json"
        product = json.loads(product_path.read_text())
        was_local = product.get("mode") == "local"
        old_connection = product.get("local_files") or product.get("laptop_connection")
        if not was_local and old_connection is None:
            continue
        runtime_path = project_root / "workflow.json"
        runtime = json.loads(runtime_path.read_text())
        if was_local:
            product["mode"] = "dev"
            runtime.setdefault("capabilities", {})["memory_enabled"] = True
        # Folder permissions now belong to server-private per-Crew settings.
        # The old binding has no local path or access level; the owner chooses them in Settings.
        product.pop("laptop_connection", None)
        product.pop("local_files", None)
        changes.append((project_root, product_path, product, runtime_path, runtime, was_local))
    if not changes:
        print(json.dumps({"migrated": 0, "message": "Nothing to migrate"}))
        return
    # All inputs are parsed before changing data; the backup is a plain copy.
    backup_dir.mkdir(parents=True, exist_ok=False)
    shutil.copy2(registry_path, backup_dir / "projects.json")
    for project_root, product_path, product, runtime_path, runtime, was_local in changes:
        destination = backup_dir / project_root.relative_to(root)
        destination.mkdir(parents=True)
        shutil.copy2(product_path, destination / "product.json")
        shutil.copy2(runtime_path, destination / "workflow.json")
        product_path.write_text(json.dumps(product, indent=2, ensure_ascii=False) + "\n")
        if was_local:
            runtime_path.write_text(json.dumps(runtime, indent=2, ensure_ascii=False) + "\n")
    print(json.dumps({"migrated": len(changes), "former_local": sum(row[-1] for row in changes),
                      "cleared_old_bindings": len(changes),
                      "backup": str(backup_dir)}))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workspace-root", type=Path, required=True)
    parser.add_argument("--registry", type=Path, required=True)
    parser.add_argument("--backup-dir", type=Path, required=True)
    args = parser.parse_args()
    migrate(args.workspace_root, args.registry, args.backup_dir)
