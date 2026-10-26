#!/usr/bin/env python3
"""PLAT-871-15: move project timers to the common shape while the app is stopped.

--check is read-only and prints counts only. Applying requires a plain backup;
historical run receipts and webhook URLs are never rewritten by this migration.
"""

import argparse
from collections import Counter
import copy
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import shutil
import sqlite3


def project_roots(root):
    bases = [root]
    bases.extend(p for p in (root / "_users").glob("*") if p.is_dir() and not p.is_symlink())
    for base in bases:
        for kind in ("Workflow", "Crew"):
            folder = base / kind
            if folder.is_symlink():
                continue
            for project in sorted(folder.glob("*")):
                if project.is_dir() and not project.is_symlink() and not project.name.startswith("."):
                    yield kind, project


def read_json(path, default):
    if not path.is_file():
        return copy.deepcopy(default)
    if path.is_symlink():
        raise ValueError("project action source must not be a symlink")
    return json.loads(path.read_text())


def caller_for(kind, manifest, metadata, local_user_id=None):
    if kind == "crew":
        return metadata.get("owner_id") or manifest.get("owner_id") or local_user_id or ""
    owners = (manifest.get("access") or {}).get("owners") or []
    creator = manifest.get("created_by") or ""
    # Match the saved ownership, never replace a missing caller with an admin.
    if creator and (not owners or creator in owners):
        return creator
    return next((owner for owner in owners if isinstance(owner, str) and owner.strip()), local_user_id or "")


def timer_record(kind, identity, caller, schedule, anchor):
    if not caller:
        raise ValueError("a saved project timer has no recorded owner; assign its real caller before migrating")
    when = {"timezone": schedule.get("timezone") or "UTC"}
    if schedule.get("cron_expression"):
        when["cron"] = schedule["cron_expression"]
    if schedule.get("created_at") or anchor:
        when["anchor"] = schedule.get("created_at") or anchor
    action = {"type": "message" if kind == "crew" or schedule.get("messages") else "run"}
    if schedule.get("chat_key"):
        action["tab"] = schedule["chat_key"]
    if schedule.get("isolated"):
        action["conversation"] = "isolated"
    inputs = {}
    for old, new in (("messages", "messages"), ("group_names", "groups"), ("route_selections", "routes"),
                     ("trigger_payload", "payload"), ("condition", "condition"),
                     ("team_authorized_by", "team_authorized_by"), ("team_policy_revision", "team_policy_revision")):
        if schedule.get(old) is not None and schedule.get(old) not in ("", [], {}):
            inputs[new] = schedule[old]
    dates = []
    for item in schedule.get("calendar_items") or []:
        dated = {k: item[k] for k in ("id", "date", "time") if k in item}
        payload = {new: item[old] for old, new in (("messages", "messages"), ("trigger_payload", "payload")) if old in item}
        if payload:
            dated["input"] = payload
        dates.append(dated)
    if schedule.get("run_at"):
        at = datetime.fromisoformat(schedule["run_at"].replace("Z", "+00:00"))
        # Preserve an absolute one-time Crew call using the common calendar form.
        utc = at.astimezone(timezone.utc)
        when["timezone"] = "UTC"
        dates.append({"date": utc.strftime("%Y-%m-%d"), "time": utc.strftime("%H:%M")})
    if dates:
        when["dates"] = dates
    ids = list(dict.fromkeys(([schedule["after_schedule_id"]] if schedule.get("after_schedule_id") else []) + (schedule.get("after_schedule_ids") or [])))
    if ids:
        when["after"] = {"schedules": ids}
        for old, new in (("after_terminal_status", "outcome"), ("after_delay_minutes", "delay_minutes"), ("dependency_deadline", "deadline")):
            if schedule.get(old):
                when["after"][new] = schedule[old]
    event_only = (schedule.get("condition") or {}).get("check_mode") in ("event", "inbox")
    if not when.get("cron") and not dates and not event_only:
        raise ValueError("project timer has no supported cron time or calendar date")
    if bool(when.get("cron")) and dates:
        raise ValueError("project timer has conflicting clock forms")
    return {"id": schedule["id"], "name": schedule["name"], "project": {"kind": kind, "id": identity},
            "when": when, "action": action, "input": inputs, "run_as": caller, "enabled": bool(schedule.get("enabled"))}


def inspect(root, local_user_id=None):
    changes, counts = [], Counter()
    states = {}
    for state_path in (root / "_users").glob("*/chat_history/product-schedules.json"):
        if not state_path.is_symlink() and not state_path.parents[1].is_symlink():
            original = read_json(state_path, {})
            states[state_path.parents[1].name] = (state_path, original, copy.deepcopy(original))
    for folder_kind, project in project_roots(root):
        metadata_path, manifest_path = project / "product.json", project / "workflow.json"
        metadata = read_json(metadata_path, {})
        manifest = read_json(manifest_path, metadata if folder_kind == "Crew" else {})
        if not manifest:
            continue
        kind = "crew" if folder_kind == "Crew" else ("relay" if manifest.get("kind") == "relay" else "goal")
        identity = manifest.get("id") or metadata.get("id")
        timers, hooks = [], []
        for schedule in manifest.get("schedules") or []:
            if kind != "crew" and schedule.get("schedule_type") == "webhook":
                hooks.append(schedule)
                if schedule.get("kind") not in ("internal", "function"):
                    counts["http_webhooks"] += 1
                continue
            timers.append(schedule)
        if kind == "crew":
            counts["http_webhooks"] += sum(1 for hook in manifest.get("triggers") or [] if not hook.get("workspace_function") and hook.get("kind") != "internal")
        if not timers:
            continue
        counts["timers"] += len(timers)
        counts[kind + "_timers"] += len(timers)
        counts["goal_message_timers"] += sum(1 for s in timers if kind == "goal" and s.get("messages"))
        counts["after_dependencies"] += sum(1 for s in timers if s.get("after_schedule_id") or s.get("after_schedule_ids"))
        records = [timer_record(kind, identity, caller_for(kind, manifest, metadata, local_user_id), s,
                                manifest.get("created_at") or metadata.get("created_at") or manifest.get("updated_at")) for s in timers]
        if kind == "crew" and records:
            state = states.get(records[0]["run_as"])
            if state:
                profile = metadata.get("product") or "work"
                for record in records:
                    override = state[2].get(f"{profile}/{identity}/{record['id']}")
                    if not isinstance(override, dict):
                        continue
                    if isinstance(override.get("enabled"), bool):
                        record["enabled"] = override.pop("enabled")
                        counts["state_overrides_folded"] += 1
                    if override.get("messages"):
                        record["input"]["messages"] = override.pop("messages")
                        counts["state_overrides_folded"] += 1
        target = project / "schedules.json"
        if target.exists():
            raise ValueError("common schedules and retired timer arrays coexist; resolve before migration")
        updated = copy.deepcopy(manifest)
        updated["schedules"] = hooks if kind != "crew" else []
        if kind == "crew":
            updated.pop("schedules", None)
        changes.append((target, {"schedules": records}))
        if manifest_path.exists():
            changes.append((manifest_path, updated))
        else:
            changes.append((metadata_path, updated))
    for state_path, before, after in states.values():
        if before != after:
            changes.append((state_path, after))
    counts["changed_files"] = len(changes)
    return changes, dict(counts)


def migrate(root, backup=None, check=False, local_user_id=None):
    changes, counts = inspect(root, local_user_id)
    if check:
        return counts
    if not backup:
        raise ValueError("backup directory is required")
    if not changes:
        return counts
    backup.mkdir(parents=True, mode=0o700, exist_ok=True)
    # Finish the plain backup before changing any definition. Never print it.
    for source, _ in changes:
        if source.exists():
            target = backup / source.relative_to(root)
            target.parent.mkdir(parents=True, exist_ok=True)
            if target.exists():
                raise ValueError("backup already exists; choose a fresh backup directory")
            shutil.copy2(source, target)
            target.chmod(0o600)
    db = root / "_system" / "schedule-state.sqlite"
    if db.exists():
        destination = backup / "schedule-state.sqlite"
        if destination.exists():
            raise ValueError("scheduler backup already exists")
        with sqlite3.connect(f"file:{db}?mode=ro", uri=True) as source, sqlite3.connect(destination) as target:
            source.backup(target)
        destination.chmod(0o600)
    for target, content in changes:
        target.write_text(json.dumps(content, indent=2, ensure_ascii=False) + "\n")
    return counts


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workspace-dir", type=Path, required=True)
    parser.add_argument("--backup-dir", type=Path)
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--single-user", action="store_true", help="Explicit local deployment mode; never use on a company server")
    parser.add_argument("--local-user-id", help="The local server's DEFAULT_USER_ID (default when unset)")
    args = parser.parse_args()
    if args.single_user != bool(args.local_user_id) or (args.single_user and os.environ.get("MULTI_USER_MODE", "").lower() == "true"):
        parser.error("local caller conversion requires --single-user and --local-user-id together, outside multi-user mode")
    print(json.dumps({"event": "project_schedule_migration", "check": args.check,
                      **migrate(args.workspace_dir, args.backup_dir, args.check, args.local_user_id)}, sort_keys=True))
