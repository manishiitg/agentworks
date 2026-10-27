#!/usr/bin/env python3
"""PLAT-825-113 one-off saved native selections. Run while the app is stopped.

Remove after the last installation moves. Qualified selectors are left alone
unless the operator identifies their namespace as native, never by leaf alone.
"""
import argparse
import json
from pathlib import Path
import shutil


def migrate(root, backups, catalogs, native_namespaces=()):
    mapping = {}
    for catalog in catalogs:
        for action in json.loads(catalog.read_text()):
            identity = action["family"] + "." + action["name"]
            sources = [action["member"], *action.get("aliases", [])]
            if action.get("source") and action["source"] != action["family"]:
                sources.append(action["source"])
            for member in sources:
                if identity not in mapping.setdefault(member, []):
                    mapping[member].append(identity)

    def selectors(value):
        if not isinstance(value, str):
            return [value]
        namespace, sep, member = value.rpartition(":")
        if sep and namespace not in native_namespaces:
            return [value]
        return [namespace + sep + replacement for replacement in mapping.get(member, [member])]

    def rewrite(value, parent=""):
        if isinstance(value, list):
            return [rewrite(item, parent) for item in value]
        if not isinstance(value, dict):
            return value
        result = {}
        for key, item in value.items():
            selection = key in {"selected_tools", "enabled_tools", "enabled_custom_tools", "selected_custom_tools"} or (parent == "tool_policy" and key in {"enabled", "disabled"})
            if selection and isinstance(item, list):
                # Keep explicit empty selections and order; collapse only IDs
                # that represented the same existing action.
                item = list(dict.fromkeys(identity for entry in item for identity in selectors(entry)))
            result[key] = rewrite(item, key)
        return result

    crew = root / "_system/crew-data"
    folders = [root / "Workflow", crew if crew.is_dir() else root / "Crew"]
    for user in (root / "_users").glob("*"):
        folders += [user / "Workflow", user / "Crew"]
    changes = []
    for folder in folders:
        for project in sorted(folder.glob("*")):
            if not project.is_dir() or project.is_symlink():
                continue
            for relative in ("workflow.json", "product.json", "planning/step_config.json"):
                path = project / relative
                if not path.is_file() or path.is_symlink():
                    continue
                before = json.loads(path.read_text())
                after = rewrite(before)
                if before != after:
                    changes.append((path, after))
    # Parse every candidate before changing the first file.
    for path, after in changes:
        backup = backups / path.relative_to(root)
        backup.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, backup)
        path.write_text(json.dumps(after, indent=2, ensure_ascii=False) + "\n")
        print(json.dumps({"event": "platform_tool_selections_grouped", "config": str(path.relative_to(root))}))
    print(json.dumps({"event": "platform_tool_selection_migration", "changed_files": len(changes)}))
    return len(changes)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workspace-dir", type=Path, required=True)
    parser.add_argument("--backup-dir", type=Path, required=True)
    parser.add_argument("--catalog", type=Path, action="append", required=True)
    parser.add_argument("--native-namespace", action="append", default=[])
    args = parser.parse_args()
    migrate(args.workspace_dir, args.backup_dir, args.catalog, args.native_namespace)
