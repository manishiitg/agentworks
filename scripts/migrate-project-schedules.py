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
import hashlib
import uuid
from pathlib import Path
import shutil
import sqlite3


def project_roots(root):
    bases = [root]
    bases.extend(p for p in (root / "_users").glob("*") if p.is_dir() and not p.is_symlink())
    for base in bases:
        for kind in ("Workflow", "Crew"):
            folder = base / kind
            if base == root and kind == "Crew" and (root / "_system/crew-data").is_dir():
                folder = root / "_system/crew-data"
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
    changes = entry_changes(root, changes, counts, local_user_id)
    counts["changed_files"] = len(changes)
    return changes, dict(counts)


def execution_input(source):
    return {new: copy.deepcopy(source[old]) for old, new in (
        ("messages", "messages"), ("group_names", "groups"), ("route_selections", "routes"),
        ("trigger_payload", "payload"), ("team_authorized_by", "team_authorized_by"),
        ("team_policy_revision", "team_policy_revision")) if source.get(old) not in (None, "", [], {})}


def input_mapping(config):
    return {new: copy.deepcopy(config[old]) for old, new in (
        ("input_mode", "mode"), ("allowed_variables", "variables"),
        ("payload_mappings", "payload")) if config.get(old) not in (None, "", [], {})}


def function_schema(source):
    spec = source.get("function") or {}
    schema = {"type": "object", "properties": {}}
    required = []
    for parameter in spec.get("inputs") or []:
        field = {"type": parameter.get("type") or "string"}
        for key in ("description", "enum"):
            if parameter.get(key):
                field[key] = copy.deepcopy(parameter[key])
        schema["properties"][parameter["name"]] = field
        if parameter.get("required"):
            required.append(parameter["name"])
    groups = source.get("group_names") or []
    if len(groups) > 1:
        schema["properties"]["group"] = {"type": "string", "enum": groups}
    if required:
        schema["required"] = required
    return schema


def function_record(kind, folder, source, binding=False):
    config = source.get("webhook") or {}
    spec = source.get("function") or {}
    if kind == "crew" and not binding:
        identity = str(uuid.uuid5(uuid.NAMESPACE_OID, folder + "\0" + source["name"]))
        action = {"type": "message"}
        if source.get("execution") == "fresh":
            action["conversation"] = "fresh"
        else:
            action["tab"] = source.get("agent") or source["name"]
        record = {"id": identity, "name": source["name"], "description": source.get("description") or "",
                  "enabled": True, "action": action, "input": {"messages": [source.get("instructions") or ""]}}
        for key in ("input_schema", "result_schema", "created_by", "created_at", "updated_at"):
            if key in source:
                record[key] = copy.deepcopy(source[key])
        return record
    action = {"type": "message" if kind == "crew" else "run"}
    if config.get("step_id"):
        action["step"] = config["step_id"]
    if kind == "crew" and source.get("run_destination", "isolated") == "isolated":
        action["conversation"] = "isolated"
    inputs = execution_input(source)
    if kind == "crew":
        inputs["messages"] = [source["message"]]
    name = "binding_" + source["id"].replace("-", "_") if binding else spec["name"]
    record = {"id": source["id"], "name": name, "description": spec.get("description") or source.get("name") or name,
              "enabled": bool(source.get("enabled")), "action": action, "input": inputs,
              "input_schema": {"type": "object"} if binding else function_schema(source)}
    if binding:
        record["input_mapping"] = input_mapping(config)
        if not source.get("caller"):
            raise ValueError("an explicit saved step binding has no caller")
        record["allowed_callers"] = [copy.deepcopy(source["caller"])]
    elif spec.get("allowed_callers"):
        record["allowed_callers"] = copy.deepcopy(spec["allowed_callers"])
    if not binding and kind != "relay":
        record["result_schema"] = {"type": "object", "properties": {"status": {"type": "string"}, "error": {"type": "string"}, "steps": {"type": "array"}}}
    return record


def release_hash(project, release, planned=None):
    files = release.get("files") or []
    if not files or len(set(files)) != len(files) or release.get("file_count") != len(files):
        return None
    runtime_prefixes = ("runs/", "relay_releases/", "chat/", "chats/", "chat_history/", "backup/", "publish/", "planning/revisions/", "planning/changelog/", "variables/changelog/", "knowledgebase/notes/", ".sandbox-cache/", "costs/", "db/", "config/", "webhooks/", "builder/", "session/", "sessions/", "logs/")
    runtime_names = {"release.json", "schedule-runs.json", ".webhook-sequence", "workflow.json.kb-lock"}
    for file in project.rglob("*"):
        if file.is_symlink():
            return None
        if file.is_file():
            relative = file.relative_to(project).as_posix()
            if relative not in files and relative not in runtime_names and not relative.startswith(runtime_prefixes):
                return None
    digest = hashlib.sha256()
    for relative in sorted(files):
        path = Path(relative)
        if path.is_absolute() or ".." in path.parts or path.as_posix() != relative:
            return None
        file = project / relative
        if planned and file in planned:
            content = (json.dumps(planned[file], indent=2, ensure_ascii=False) + "\n").encode()
        elif file.is_file() and not file.is_symlink():
            content = file.read_bytes()
        else:
            return None
        digest.update(relative.encode() + b"\0" + content + b"\0")
    return digest.hexdigest()


def entry_changes(root, timer_changes, counts, local_user_id=None):
    planned = dict(timer_changes)
    skipped_releases = []
    projects = list(project_roots(root))
    for base in [root] + [p for p in (root / "_users").glob("*") if p.is_dir() and not p.is_symlink()]:
        releases = base / "Workflow" / ".relay_releases"
        if not releases.is_symlink():
            projects.extend(("Workflow", p.parent) for p in sorted(releases.glob("*/v*/workflow.json")) if not p.is_symlink())
    for folder_kind, project in projects:
        release_path = project / "release.json"
        if release_path.exists():
            original_release = read_json(release_path, {})
            if not original_release.get("hash") or release_hash(project, original_release) != original_release["hash"]:
                counts["flow_releases_skipped_hash_mismatch"] += 1
                skipped_releases.append(project.relative_to(root).as_posix())
                continue
        metadata_path, manifest_path = project / "product.json", project / "workflow.json"
        metadata = read_json(metadata_path, {})
        manifest = copy.deepcopy(planned.get(manifest_path, read_json(manifest_path, metadata if folder_kind == "Crew" else {})))
        if not manifest:
            continue
        kind = "crew" if folder_kind == "Crew" else ("relay" if manifest.get("kind") == "relay" else "goal")
        identity = manifest.get("id") or metadata.get("id")
        root_key = project.relative_to(root).as_posix()
        functions_path, hooks_path = project / "functions.json", project / "webhooks.json"
        old_functions = read_json(functions_path, {})
        functions = copy.deepcopy(old_functions.get("functions") or [])
        changed_functions = bool(old_functions) and ("version" in old_functions or any("action" not in fn for fn in functions))
        if changed_functions:
            counts[kind + "_function_files_converted"] += len(functions)
            functions = [function_record(kind, root_key, fn) for fn in functions]
        source_entries = manifest.get("triggers") or [] if kind == "crew" else [s for s in manifest.get("schedules") or [] if s.get("schedule_type") == "webhook"]
        if source_entries and hooks_path.exists():
            raise ValueError("common webhooks and retired entries coexist")
        hooks = []
        for source in source_entries:
            if source.get("workspace_function"):
                counts["function_adapters_removed"] += 1
                continue
            if source.get("kind") in ("internal", "function"):
                functions.append(function_record(kind, root_key, source, source.get("kind") == "internal"))
                changed_functions = True
                counts["explicit_bindings" if source.get("kind") == "internal" else "declared_functions"] += 1
                continue
            caller = caller_for(kind, manifest, metadata, local_user_id)
            if not caller:
                raise ValueError("a webhook has no recorded caller")
            config = source.get("webhook") or {}
            action = {"type": "message" if kind == "crew" or source.get("messages") else "run"}
            if config.get("step_id"):
                action["step"] = config["step_id"]
            if kind == "crew" and source.get("run_destination", "crew_chat") == "isolated":
                action["conversation"] = "isolated"
            inputs = execution_input(source)
            if kind == "crew":
                inputs["messages"] = [source["message"]]
            hooks.append({"id": source["id"], "name": source["name"], "project": {"kind": kind, "id": identity},
                          "enabled": bool(source.get("enabled")), "path": source.get("path") or "/api/hooks/" + source["id"],
                          "action": action, "input": inputs, "run_as": caller,
                          "auth": {"mode": config.get("auth_mode") or "", "encrypted_secret": config.get("encrypted_secret") or ""},
                          "input_mapping": input_mapping(config)})
        if changed_functions:
            ids = [fn["id"] for fn in functions]
            if len(ids) != len(set(ids)):
                raise ValueError("function identities collide during conversion")
            planned[functions_path] = {"functions": sorted(functions, key=lambda fn: fn["name"])}
        if source_entries:
            if hooks:
                planned[hooks_path] = {"webhooks": hooks}
            if kind == "crew":
                manifest.pop("triggers", None)
            else:
                manifest["schedules"] = [s for s in manifest.get("schedules") or [] if s.get("schedule_type") != "webhook"]
            planned[manifest_path if manifest_path.exists() else metadata_path] = manifest
        release_path = project / "release.json"
        if release_path.exists() and (changed_functions or source_entries):
            release = read_json(release_path, {})
            files = set(release.get("files") or [])
            for item in (functions_path, hooks_path):
                if item in planned:
                    files.add(item.name)
            release.update(files=sorted(files), file_count=len(files))
            digest = hashlib.sha256()
            for relative in sorted(files):
                file = project / relative
                content = (json.dumps(planned[file], indent=2, ensure_ascii=False) + "\n").encode() if file in planned else file.read_bytes()
                digest.update(relative.encode() + b"\0" + content + b"\0")
            release["hash"] = digest.hexdigest()
            planned[release_path] = release
            counts["flow_releases_converted"] += 1
    # Published agents keep an immutable source copy outside the Crew tree.
    # Convert those definitions too; there is no runtime legacy reader.
    for user in (root / "_users").glob("*"):
        published = user / "crew-published"
        if user.is_symlink() or published.is_symlink():
            continue
        for file in published.glob("*/*/*/source/functions.json"):
            relative = file.relative_to(root)
            if any(root.joinpath(*relative.parts[:i]).is_symlink() for i in range(1, len(relative.parts) + 1)):
                continue
            old = read_json(file, {})
            functions = old.get("functions") or []
            if "version" not in old and all("action" in fn for fn in functions):
                continue
            folder = file.parent.relative_to(root).as_posix()
            planned[file] = {"functions": [function_record("crew", folder, fn) for fn in functions]}
            counts["published_function_files_converted"] += 1
    if skipped_releases:
        counts["flow_release_skip_paths"] = skipped_releases
    return list(planned.items())


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
