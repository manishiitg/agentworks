#!/usr/bin/env python3
"""PLAT-746-885: pin Crew message automations to the existing physical Main.

One-off operator tool, not a startup migration. Stop the app before applying.
Use the Crew's private data folder and its owner's conversation registry JSON.
No database, transcript, secret, function tab, or project identity is rewritten.
Run separately for each Crew on each authorized server/laptop. Remove this tool
when the rollout is complete. Explicit executable roots also convert saved chat_id destinations.
"""
import argparse
import copy
import json
from pathlib import Path
import shutil


def read_json(file):
    if file.is_symlink():
        raise ValueError("migration inputs must not be symlinks")
    return json.loads(file.read_text())


def pin_document(document, collection, project, main):
    result = copy.deepcopy(document)
    for record in result.get(collection, []):
        if record.get("project") != {"kind": "crew", "id": project}:
            raise ValueError("automation belongs to another project")
        action = record.get("action", {})
        condition = record.get("input", {}).get("condition")
        if condition:
            # Watchers route through target.chat_id, not action.tab. The suffix
            # is the physical tab identity used by the Watcher dispatcher.
            target = condition.get("target", {})
            if target.get("kind") == "agent" and not condition.get("github_pr"):
                if target.get("chat_id", "").strip() in ("", "main"):
                    target["chat_id"] = main.removeprefix(project + ":chat:")
            continue
        if action.get("type") != "message" or action.get("conversation") in ("isolated", "new"):
            continue
        if action.get("tab", "").strip() in ("", "main", project):
            action["tab"] = main
    return result


def inspect(crew, registry_file, registry_workspace=None, executable_roots=()):
    manifest = read_json(crew / "product.json")
    project = manifest.get("id")
    if not project or manifest.get("product") != "work":
        raise ValueError("not a Crew project manifest")
    main = manifest.get("main_chat_key", "")
    prefix = project + ":chat:"
    suffix = main.removeprefix(prefix)
    if not main.startswith(prefix) or not suffix or ":" in suffix or suffix.startswith(("fn.", "agent-")):
        raise ValueError("Crew has no saved physical Main; do not guess or create one")
    registry = read_json(registry_file)
    record = registry.get("entries", {}).get("work/" + main, {})
    if (record.get("conversation_key") != main or record.get("profile_id") != "work"
            or not record.get("session_id")):
        raise ValueError("saved Main is absent from the supplied owner's registry")
    # The operator supplies the authorized owner's registry. Also check the
    # saved workspace against this exact Crew, never a similarly named Crew.
    expected_workspace = registry_workspace or str(crew.resolve())
    if record.get("workspace_path") != expected_workspace:
        raise ValueError("registry workspace does not match this Crew; supply its exact saved workspace path")
    changes = []
    for collection in ("schedules", "webhooks"):
        file = crew / (collection + ".json")
        if not file.exists():
            continue
        before = read_json(file)
        after = pin_document(before, collection, project, main)
        if before != after:
            changes.append((file, after))
    for executable in executable_roots:
        file = executable / "workflow.json"
        before = read_json(file)
        after = copy.deepcopy(before)
        for schedule in after.get("schedules", []):
            # Blank script destinations no longer need a conversation. Only
            # an explicitly saved Main role is pinned to its physical agent.
            if schedule.get("chat_id", "").strip() in ("main", project):
                schedule["chat_id"] = main
        if before != after:
            changes.append((file, after))
    return changes


def apply(changes, backup):
    if not changes:
        return
    backup.mkdir(mode=0o700, parents=True, exist_ok=False)
    # Back up every changed file before changing any of them. Existing backups
    # are never overwritten. Nothing is printed from an automation's payload.
    names = {}
    for index, (file, _) in enumerate(changes):
        name = file.name if sum(p.name == file.name for p, _ in changes) == 1 else f"{index}-{file.name}"
        shutil.copy2(file, backup / name)
        names[str(file)] = name
    (backup / "files.json").write_text(json.dumps(names, indent=2) + "\n")
    for file, document in changes:
        file.write_text(json.dumps(document, indent=2, ensure_ascii=False) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--crew-root", type=Path, required=True)
    parser.add_argument("--owner-registry", type=Path, required=True)
    parser.add_argument("--registry-workspace", help="exact logical Crew path if the registry does not store an absolute path")
    parser.add_argument("--executable-root", type=Path, action="append", default=[], help="explicit executable root belonging to this Crew; repeat as needed")
    parser.add_argument("--apply", action="store_true")
    parser.add_argument("--backup", type=Path)
    args = parser.parse_args()
    if args.apply and not args.backup:
        parser.error("--apply requires a new --backup directory")
    changes = inspect(args.crew_root, args.owner_registry, args.registry_workspace, args.executable_root)
    if args.apply:
        apply(changes, args.backup)
    print(f"{'Updated' if args.apply else 'Would update'} {len(changes)} automation files.")


if __name__ == "__main__":
    main()
