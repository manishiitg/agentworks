#!/usr/bin/env python3
"""PLAT-746-1024: retire private standby staging after all binaries are updated.

Stop the backend first. Preview is the default. --apply requires a NEW private
backup directory. This moves no project files or native conversation histories.
Saved Run-online account choices move out of the retired tree; all retired
payloads and writer marks are backed up before removal. Run per private state
root, never on a client's server without its owner's rollout approval.
"""
import argparse
import json
import os
from pathlib import Path
import shutil


def regular_tree(path):
    if path.is_symlink():
        raise ValueError("Refusing a symlink in retired private state")
    if path.is_dir():
        for entry in path.iterdir():
            regular_tree(entry)
    elif not path.is_file():
        raise ValueError("Refusing non-file private state")


def retire(root, backup=None):
    root = root.resolve(strict=True)
    sync = root / "crew-sync"
    if sync.is_symlink():
        raise ValueError("Refusing a symlink in private sync state")
    retired = sorted(sync.glob("*-always-on")) if sync.exists() else []
    retired += [p for p in (sync / "standby-sessions.json", root / "chat-terminal-standby") if p.exists()]
    accounts = []
    for source in retired:
        regular_tree(source)
        if source.is_dir() and source.name.endswith("-always-on"):
            project = source.name.removesuffix("-always-on")
            if project in ("", ".", ".."):
                raise ValueError("Invalid retired project ID")
            for old in sorted((source / "people").glob("*/server-account.json")):
                value = json.loads(old.read_text())
                if not isinstance(value, dict) or any(not isinstance(k, str) or not isinstance(v, str) for k, v in value.items()):
                    raise ValueError("Invalid saved server account choices")
                dest = sync / "server-accounts" / project / (old.parent.name + ".json")
                if any(p.is_symlink() for p in [dest, *dest.parents]):
                    raise ValueError("Refusing a symlink in account destination")
                if dest.exists() and json.loads(dest.read_text()) != value:
                    raise ValueError("Saved server account choices conflict; keep both and resolve before cleanup")
                accounts.append((old, dest))
    print(f"[TEAM_ONLINE] event=retirement_plan retired={len(retired)} account_choices={len(accounts)}")
    if backup is None or not retired:
        return
    backup = backup.resolve()
    if backup.is_relative_to(root) or root.is_relative_to(backup):
        raise ValueError("Keep the private backup separate from the state root")
    backup.mkdir(mode=0o700, parents=True, exist_ok=False)
    for source in retired:
        dest = backup / source.relative_to(root)
        dest.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        if source.is_dir():
            shutil.copytree(source, dest)
        else:
            shutil.copy2(source, dest)
    # A full private backup exists before the first write or removal.
    for old, dest in accounts:
        if not dest.exists():
            dest.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            with dest.open("xb") as out:
                os.chmod(dest, 0o600)
                out.write(old.read_bytes())
    for source in retired:
        if source.is_dir():
            shutil.rmtree(source)
        else:
            source.unlink()
    print(f"[TEAM_ONLINE] event=retirement_done retired={len(retired)} account_choices={len(accounts)}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("state_root", type=Path)
    parser.add_argument("--apply", action="store_true")
    parser.add_argument("--backup", type=Path)
    args = parser.parse_args()
    if args.apply and not args.backup:
        parser.error("--apply requires --backup")
    retire(args.state_root, args.backup if args.apply else None)
