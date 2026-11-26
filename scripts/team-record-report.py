#!/usr/bin/env python3
"""Read-only Team/project duplicate inventory (#2809, part of #2796).

Run locally on each server as an authorized operator. JSON goes only to stdout;
keep it private. No network calls, database reads, credentials, repairs or deletes.
Directory names can be supplied using a sanitized users action=list JSON export,
never config/users.json (which contains authentication data).
"""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import re
import stat
import sys
import uuid

sys.dont_write_bytecode = True
LIMIT = 64 * 1024 * 1024


def stamp(value):
    try:
        parsed = dt.datetime.fromisoformat(str(value).replace("Z", "+00:00"))
        return parsed.astimezone(dt.timezone.utc).isoformat() if parsed.tzinfo and parsed.year > 1 else None
    except (ValueError, TypeError):
        return None


def leaf(value):
    return isinstance(value, str) and value not in ("", ".", "..") and not any(c in value for c in "/\\\0")


class Reader:
    def __init__(self):
        self.warnings = []

    def warn(self, subject, code):
        self.warnings.append({"record": subject, "reason": code})

    def safe(self, root, path):
        # Do not follow metadata or folder symlinks, including an ancestor.
        path = Path(path)
        try:
            relative = path.relative_to(root)
        except ValueError:
            raise ValueError("outside_root")
        current = root
        for part in ("", *relative.parts):
            current = current / part
            if current.is_symlink():
                raise ValueError("symlink")
        return path

    def read(self, root, path, subject, required=False):
        try:
            path = self.safe(root, path)
            fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
            with os.fdopen(fd, "rb") as stream:
                info = os.fstat(stream.fileno())
                if not stat.S_ISREG(info.st_mode) or info.st_size > LIMIT:
                    raise ValueError("not_bounded_metadata")
                raw = stream.read(LIMIT + 1)
                if len(raw) > LIMIT:
                    raise ValueError("metadata_too_large")
                value = json.loads(raw)
            if not isinstance(value, dict):
                raise ValueError("metadata_not_object")
            return value
        except FileNotFoundError:
            if required:
                self.warn(subject, "missing_metadata")
        except (OSError, ValueError, UnicodeError):
            self.warn(subject, "unreadable_or_unsafe_metadata")
        return None

    def directories(self, root, parent, subject):
        try:
            parent = self.safe(root, parent)
            return sorted((p for p in parent.iterdir() if not p.is_symlink() and p.is_dir()), key=lambda p: p.name)
        except FileNotFoundError:
            return []
        except (OSError, ValueError):
            self.warn(subject, "unreadable_directory")
            return []

    def files(self, root, path, max_files):
        result = {"count": None, "bytes": None, "complete": False, "latest_modified_at": None}
        try:
            path = self.safe(root, path)
            if not path.is_dir():
                return result
            count, size, newest = 0, 0, 0
            pending = [path]
            while pending:
                current = pending.pop()
                for item in current.iterdir():
                    info = item.lstat()
                    if stat.S_ISLNK(info.st_mode):
                        self.warn(path.name, "file_symlink_skipped")
                        continue
                    if stat.S_ISDIR(info.st_mode):
                        pending.append(item)
                    elif stat.S_ISREG(info.st_mode):
                        count += 1
                        size += info.st_size
                        newest = max(newest, info.st_mtime)
                    if count >= max_files:
                        self.warn(path.name, "file_scan_limit")
                        return {**result, "count": count, "bytes": size, "lower_bound": True}
            return {"count": count, "bytes": size, "complete": True, "latest_modified_at": dt.datetime.fromtimestamp(newest, dt.timezone.utc).isoformat() if newest else None}
        except (OSError, ValueError):
            self.warn(path.name, "unreadable_or_unsafe_files")
            return result


def report(state, workspace, server, directory=None, max_files=100000):
    state, workspace = Path(state).absolute(), Path(workspace).absolute()
    reader = Reader()
    # No application startup: ordinary server initializers can repair/create records.
    owners = reader.read(state, state / "ownership/projects.json", "ownership", True)
    projects = {}
    extra_records = []
    owner_records = (owners or {}).get("projects", {})
    if not isinstance(owner_records, dict):
        raise ValueError("Invalid ownership metadata")
    for key, record in owner_records.items():
        if not isinstance(record, dict):
            reader.warn(key, "invalid_project_record")
            continue
        if record.get("product") != "work":
            continue
        pid, owner, folder = record.get("project_id"), record.get("owner_id"), record.get("folder")
        if not all(leaf(v) for v in (pid, owner, folder)):
            reader.warn(key, "invalid_project_identity")
            continue
        row = {"id": pid, "registry_key": key, "owner_id": owner, "folder": folder, "created_at": record.get("created_at"), "registry_conflict": False, "registered": True}
        if pid in projects:
            reader.warn(pid, "duplicate_project_id")
            projects[pid]["registry_conflict"] = row["registry_conflict"] = True
            extra_records.append(row)
        else:
            projects[pid] = row

    teams = {}
    for folder in reader.directories(state, state / "team-folders", "teams"):
        team = reader.read(state, folder / "team.json", folder.name, True)
        if team and team.get("project_id") == folder.name:
            teams[folder.name] = team
        else:
            reader.warn(folder.name, "unavailable_team")
    for pid, team in teams.items():
        if pid not in projects and leaf(team.get("owner_id")):
            reader.warn(pid, "team_record_without_registered_project")
            projects[pid] = {"id": pid, "owner_id": team["owner_id"], "folder": None, "registered": False, "registry_conflict": True}
    all_rows = list(projects.values()) + extra_records
    users = {}
    directory_loaded = False
    if directory:
        directory = Path(directory).absolute()
        doc = reader.read(directory.parent, directory, "directory", True)
        directory_loaded = doc is not None
        # Refuse the authentication store instead of silently treating it as an export.
        records = (doc or {}).get("users", [])
        if not isinstance(records, list) or any(not isinstance(u, dict) for u in records):
            raise ValueError("Invalid directory export")
        if any(any(k in u for k in ("password_hash", "sso", "access_token", "refresh_token")) for u in records):
            raise ValueError("Use a sanitized users list export, never authentication storage")
        users = {u.get("user_id", u.get("id")): u.get("username", "") for u in records}

    registries = []
    for user in reader.directories(workspace, workspace / "_users", "agent_registries"):
        doc = reader.read(workspace, user / "chat_history/product-conversations.json", user.name)
        if doc:
            registries.append((user.name, doc))
    groups = []
    for row in all_rows:
        pid = row["id"]
        team = teams.get(pid, {})
        row["owner"] = {"id": row.pop("owner_id"), "name": None}
        row["owner"]["name"] = users.get(row["owner"]["id"])
        row["people"] = [{"id": p.get("user_id"), "name": users.get(p.get("user_id")), "invited": bool(p.get("invited"))} for p in team.get("people", [])]
        row["team_on"] = team.get("on")
        row["source_team"] = team.get("roster_source")
        row["online_home"] = team.get("online_home")
        row["team_owner_matches"] = not team or team.get("owner_id") == row["owner"]["id"]
        root = workspace / "Crew" / row["folder"] if row["folder"] else None
        manifest = reader.read(workspace, root / "product.json", pid) or {} if root else {}
        row["name"] = team.get("name") or manifest.get("title") or row["folder"]
        row["files"] = reader.files(workspace, root, max_files) if root else {"count": None, "bytes": None, "complete": False, "latest_modified_at": None}
        sync = reader.read(state, state / "crew-sync" / (pid + ".json"), pid)
        row["mac_source"] = None
        if sync and sync.get("side") == "server" and sync.get("pair_id") and sync.get("peer_project_id"):
            row["mac_source"] = {k: sync.get(k) for k in ("mac_id", "pair_id", "peer_project_id", "archive_only", "last_completed_at")}
        row["agents"] = []
        for user, doc in registries:
            for key, record in doc.get("entries", {}).items():
                if not isinstance(record, dict) or record.get("profile_id") != "work":
                    continue
                chat = record.get("conversation_key", "")
                if chat != pid and not (chat.startswith(pid + ":chat:") and ":" not in chat[len(pid + ":chat:"):]):
                    continue
                identity = doc.get("agents", {}).get(key, {})
                row["agents"].append({"id": record.get("conversation_id"), "key": record.get("conversation_key"), "owner_id": user, "name": identity.get("name") or record.get("title"), "updated_at": stamp(record.get("updated_at"))})
        known = {(a["owner_id"], a["key"]) for a in row["agents"]}
        for aid, agent in team.get("agents", {}).items():
            if (agent.get("owner_id"), agent.get("chat_key")) not in known:
                row["agents"].append({"id": aid, "key": agent.get("chat_key"), "owner_id": agent.get("owner_id"), "name": agent.get("name"), "updated_at": None, "basis": "team_agent_registry_only"})
        row["agent_count"] = len(row["agents"])
        updates = [a["updated_at"] for a in row["agents"] if a["updated_at"]]
        row["last_activity"] = {"at": max(updates) if updates else None, "basis": "conversation_registry_updated_at", "note": "Metadata update, not proof of last completed AI turn"}
        row["canonical"] = {"project_id": None, "reason": "not_established"}
        row["findings"] = []
        if team and not row["team_owner_matches"]:
            row["findings"].append({"kind": "team_owner_conflict"})
        for agent in row["agents"]:
            if (agent.get("name") or "").endswith("· online"):
                row["findings"].append({"kind": "online_agent_name_candidate", "agent_key": agent["key"], "reason": "name_only_not_proof_of_duplicate"})

    origins = {}
    for row in all_rows:
        source = row["mac_source"]
        if source:
            origin = (row["owner"]["id"], source.get("mac_id") or "pair:" + source["pair_id"], source["peer_project_id"])
            origins.setdefault(origin, []).append(row)
    for origin, rows in origins.items():
        if len(rows) < 2:
            continue
        active = [r for r in rows if not r["mac_source"].get("archive_only") and not r["registry_conflict"] and r["team_owner_matches"]]
        canonical = active[0]["id"] if len(active) == 1 else None
        reason = "unique_nonarchived_source_binding" if canonical else "conflicting_live_bindings_require_owner_review"
        groups.append({"kind": "crew_twins", "owner_id": origin[0], "mac_source": origin[1:], "project_ids": [r["id"] for r in rows], "canonical_project_id": canonical, "reason": reason})
        for row in rows:
            row["canonical"] = {"project_id": canonical, "reason": reason}
            row["findings"].append({"kind": "crew_twin"})

    for row in all_rows:
        source = projects.get(row["source_team"])
        expected = str(uuid.uuid5(uuid.NAMESPACE_OID, "agentworks/team-home/" + (row["source_team"] or "")))
        if row["source_team"]:
            if source and source["online_home"] == row["id"] and row["id"] == expected and row["owner"]["id"] == "team-" + source["id"] and source["team_owner_matches"] and not source["registry_conflict"] and not row["registry_conflict"]:
                row["canonical"] = {"project_id": source["id"], "online_home_id": row["id"], "reason": "reciprocal_source_home_and_deterministic_id"}
            else:
                row["findings"].append({"kind": "orphan_or_conflicting_home", "reason": "missing_or_conflicting_source_link"})
        elif row["owner"]["id"].startswith("team-"):
            row["findings"].append({"kind": "suspected_home_without_source", "reason": "execution_owner_shape_only_requires_review"})

    # Mark linked sources independently of display-name and file-count heuristics.
    for row in all_rows:
        home = projects.get(row["online_home"])
        if home and home["canonical"].get("project_id") == row["id"] and not row["registry_conflict"] and row["canonical"]["reason"] == "not_established":
            row["canonical"] = {"project_id": row["id"], "online_home_id": home["id"], "reason": "reciprocal_source_home_link"}

    account_groups = {}
    for uid, name in users.items():
        if isinstance(name, str) and name.startswith("Team: "):
            base = re.sub(r" \((?:[2-9]|[1-9][0-9]+)\)$", "", name)
            account_groups.setdefault(base, []).append({"id": uid, "name": name, "project_ids": [r["id"] for r in projects.values() if r["owner"]["id"] == uid]})
    for name, accounts in account_groups.items():
        if len(accounts) > 1 or any(a["name"] != name for a in accounts):
            groups.append({"kind": "numbered_team_accounts", "display_name": name, "accounts": accounts, "canonical_account_id": None, "canonical_by_source": [{"source_project_id": r["source_team"], "account_id": r["owner"]["id"], "home_project_id": r["id"]} for r in all_rows if r["owner"]["id"] in {a["id"] for a in accounts} and r["canonical"]["reason"] == "reciprocal_source_home_and_deterministic_id"], "reason": "display_name_not_identity_use_each_project_source_evidence"})

    registered_folders = {r["folder"] for r in all_rows}
    unregistered_folders = []
    for folder in reader.directories(workspace, workspace / "Crew", "project_folders"):
        if folder.name not in registered_folders:
            unregistered_folders.append({"folder": folder.name, "owner": None, "canonical": None, "reason": "no_authoritative_ownership_record", "files": reader.files(workspace, folder, max_files)})
    return {"server": server, "read_only": True, "generated_at": dt.datetime.now(dt.timezone.utc).isoformat(), "coverage": {"ownership_loaded": owners is not None, "directory_export_supplied": directory is not None, "directory_loaded": directory_loaded, "agent_registries_read": len(registries), "files": "regular-file counts only; contents, databases and symlinks never read", "live_snapshot": "nontransactional; rerun if metadata changes during inspection"}, "projects": sorted(all_rows, key=lambda r: (r["id"], r["folder"] or "")), "groups": groups, "unregistered_folders": unregistered_folders, "warnings": reader.warnings}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state-root", required=True, type=Path)
    parser.add_argument("--workspace-root", required=True, type=Path)
    parser.add_argument("--server", required=True, help="Private operator label, never a connection or credential")
    parser.add_argument("--directory-json", type=Path, help="Sanitized users action=list export for account names")
    parser.add_argument("--max-files", type=int, default=100000, help="Per-project count cap; truncated counts marked lower bounds")
    args = parser.parse_args()
    if args.max_files < 1:
        parser.error("max-files must be positive")
    try:
        result = report(args.state_root, args.workspace_root, args.server, args.directory_json, args.max_files)
        json.dump(result, sys.stdout, indent=2, sort_keys=True)
        print()
        print(f"[TEAM_RECORD_REPORT] event=reported projects={len(result['projects'])} groups={len(result['groups'])} warnings={len(result['warnings'])}", file=sys.stderr)
    except (ValueError, TypeError, OSError):
        parser.exit(2, "Report unavailable: check metadata shape, read access and safe directory export. No changes made.\n")


if __name__ == "__main__":
    main()
