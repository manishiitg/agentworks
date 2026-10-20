#!/usr/bin/env python3
"""One-off Code/Work -> Crew metadata conversion. Run before the Crew-only release.

Pass --docs and --state for one deployment. Without --apply, report the planned
changes. Applying first saves the exact metadata, databases and link inventory
under the private state directory. Chat text, secrets and native IDs are not
rewritten. Retire this tool after the last deployment has converted.
"""

import argparse
import collections
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import tarfile
from urllib.parse import urlsplit


PROFILE_FIELDS = {"product", "profile_id", "agent_profile_id", "caller_profile_id",
                  "target_profile_id", "crew_profile", "profile", "surface"}
PATH_FIELDS = {"workspace_path", "workspace", "selected_folder", "working_folder",
               "working_dir", "project_dir_id", "caller_path", "target_path",
               "conversation_path", "code_root", "root", "path"}
PRIVATE_FIELDS = {"binding_root", "store_root", "session_id", "external_session_id",
                  "conversation_id", "agent_profile_key", "chat_policy_key",
                  "chat_policy_role_key", "project_identity", "content", "messages",
                  "events", "query", "response", "prompt", "tool_calls", "data"}
SKILLS = {"code-" + name: "work-" + name for name in
          ("mcp", "integrations", "workflow-files", "skills", "schedules-and-bots", "dashboard", "ui-control")}

SKILLS["code-background-work"] = "background-work"


def scopes(values):
    result = []
    for value in values:
        replacements = ["crews:review"] if value == "code:review" else (
            ["crews:read", "crews:run", "crews:write"] if value == "code:run" else [value])
        result.extend(x for x in replacements if x not in result)
    return result


class Conversion:
    def __init__(self, docs, state):
        self.docs, self.state = docs.resolve(), state.resolve()
        self.registry_path = self.state / "ownership/projects.json"
        self.registry = json.loads(self.registry_path.read_text())
        self.original = self.registry["projects"]
        self.paths, self.owners, self.ids, self.links, self.moves = {}, collections.defaultdict(list), {}, [], []
        self.files = {}
        self.project_owners = {}
        self.context_owner = None
        self.unresolved_contexts = []
        self.state_moves = []
        self.retired_states = []
        self.schedule_state_keys = 0
        self.skipped_history_metadata = 0
        records = {}
        for record in self.original.values():
            if record["product"] not in ("work", "code"):
                raise ValueError("Unknown registered project type")
            owner, folder = record["owner_id"], record["folder"]
            if any(not x or x in (".", "..") or "/" in x or "\\" in x for x in (owner, folder)):
                raise ValueError("Invalid registered project location")
            legacy = f"_users/{owner}/Chats/{'Code' if record['product'] == 'code' else 'Work'}/projects/{folder}"
            canonical = "Crew/" + folder
            self.project_owners[canonical] = owner
            source = record.get("canonical_root") or (canonical if record.get("shared") else legacy)
            if source not in (legacy, canonical):
                raise ValueError("Unexpected registered storage location")
            manifest_path = self.docs / source / "product.json"
            manifest = json.loads(manifest_path.read_text())
            if manifest.get("product") != record["product"] or manifest.get("id") != record.get("project_id"):
                raise ValueError("Manifest and trusted registry identity disagree")
            self.ids[manifest["id"]] = manifest
            if record["product"] == "code":
                self.owners[owner].append(manifest["id"])
            new = dict(record, product="work", canonical_root=canonical, shared=True)
            if record["product"] == "code":
                new["admin_inspection"] = True
            # A zero-value legacy Code audience was never a company-wide grant.
            if record["product"] == "code" and not record.get("audience_configured"):
                new["private"], new["audience_configured"] = True, True
            if source != canonical:
                if (self.docs / canonical).exists():
                    raise ValueError("Both source and destination exist")
                new.setdefault("binding_root", source)
                self.moves.append((source, canonical))
            aliases = set(record.get("aliases", [])) | {legacy, source,
                f"_users/{owner}/Chats/Code/projects/{folder}",
                f"_users/{owner}/Chats/Work/projects/{folder}"}
            for alias in aliases:
                if alias == canonical:
                    continue
                self.paths[alias] = canonical
                logical = alias.split("/", 2)[-1] if alias.startswith("_users/") else alias
                if logical in self.paths and self.paths[logical] != canonical:
                    raise ValueError("Ambiguous logical project path")
                self.paths[logical] = canonical
                link = self.docs / alias
                if link.is_symlink():
                    if link.resolve() != (self.docs / canonical).resolve():
                        raise ValueError("Old link points to a different project")
                    self.links.append((alias, os.readlink(link)))
            new.pop("aliases", None)
            key = "work/" + folder
            if key in records:
                raise ValueError("Crew folder identities collide")
            records[key] = new
        self.registry["projects"] = records
        self.plan(self.registry_path, self.registry)

    def path(self, value):
        if not value.startswith(("Chats/", "_users/", str(self.docs) + "/")):
            return value
        for old, new in sorted(self.paths.items(), key=lambda pair: -len(pair[0])):
            if value == old or value.startswith(old + "/"):
                return new + value[len(old):]
            absolute = str(self.docs / old)
            if value == absolute or value.startswith(absolute + "/"):
                return str(self.docs / new) + value[len(absolute):]
        return value

    def transform(self, value, field=""):
        if field in PRIVATE_FIELDS:
            return value
        if isinstance(value, list):
            if field == "workflow_context_paths":
                result = []
                for item in value:
                    converted = self.path(item) if isinstance(item, str) else item
                    logical = converted.removeprefix(str(self.docs) + "/") if isinstance(converted, str) else ""
                    owner = self.project_owners.get(logical)
                    if owner and owner == self.context_owner:
                        result.append(converted)
                    else:
                        result.append(item)
                        if isinstance(item, str) and (item.startswith(("Chats/", "_users/")) or "/Chats/" in item):
                            self.unresolved_contexts.append({"conversation_owner": self.context_owner, "path": item})
                return result
            if field == "scopes":
                return scopes(value)
            if field in ("products", "create_products", "paused_products", "enabled_product_surfaces"):
                return list(dict.fromkeys("work" if x == "code" else x for x in value))
            if field in ("selected_skills", "skills") and all(isinstance(x, str) for x in value):
                return list(dict.fromkeys(SKILLS.get(x, x) for x in value if x != "make-a-crew"))
            return [self.transform(x) for x in value]
        if isinstance(value, dict):
            out = {}
            for key, item in value.items():
                source_key = key
                if key == "work" and field in ("create_products", "can_create_products") and isinstance(item, bool):
                    item = item or value.get("code") is True
                if key == "code_reviewer":
                    key = "crew_reviewer"
                if key.startswith("code/") and field in ("entries", "previous", "agents", "schedule_states"):
                    key = "work/" + key[5:]
                    # A running canonical schedule owns its latest state. Never
                    # reset it by replacing it with the pre-migration twin.
                    if field == "schedule_states" and key in value:
                        continue
                if key == "code" and field in ("create_products", "product_defaults", "can_create_products"):
                    if "work" in value:
                        continue
                    key = "work"
                # Map private-store attachment keys and history index keys;
                # StoreRoot/BindingRoot values stay pinned to their old namespace.
                key = self.path(key)
                if field == "target" and key == "name" and isinstance(item, str):
                    item = self.path(item)
                converted = self.transform(item, key)
                if key in out:
                    prior = out[key]
                    # Old and canonical cache entries may index the same exact
                    # history file. Keep its canonical entry; no chat is removed.
                    prior_session = prior.get("session", {}).get("session_id") if isinstance(prior, dict) else None
                    current_session = converted.get("session", {}).get("session_id") if isinstance(converted, dict) else None
                    if field == "entries" and prior_session and prior_session == current_session:
                        if source_key == key:
                            out[key] = converted
                        continue
                    if prior == converted:
                        continue
                    raise ValueError("Converted metadata keys collide in " + field)
                out[key] = converted
            if out.get("profile_id") == "work" and "profile_version" in out:
                out["profile_version"] = 3
            return out
        if isinstance(value, str):
            if field in PROFILE_FIELDS and value == "code":
                return "work"
            if field in PATH_FIELDS:
                return self.path(value)
            if field in ("target", "agent_id") and value.startswith("workspace:code:"):
                return "workspace:work:" + value[len("workspace:code:"):]
        return value

    def plan(self, path, value):
        raw = json.dumps(value, ensure_ascii=False, indent=2) + "\n"
        if json.loads(path.read_text()) != value:
            self.files[path] = raw

    def collect(self):
        candidates = {self.docs / "config" / name for name in ("users.json", "provider-account-settings.json", "slack-targets.json", "slack-config.json", "scheduler.json")}
        for pattern in ("Crew/*/product.json", "Crew/*/workflow.json", "Workflow/*/workflow.json",
                        "_users/*/chat_history/**/*.json", "_users/**/product-conversations.json",
                        "Crew/*/builder/conversation/**/*.json", "_users/*/preferences/workspace-state.json"):
            candidates.update(self.docs.glob(pattern))
        for source, _ in self.moves:
            candidates.update((self.docs / source).glob("*.json"))
        for parent in ("mcp", "personal-mcp", "code-session-pins", "crew-publications"):
            base = self.state / parent
            if parent == "crew-publications":
                candidates.update(base.glob("**/manifest.json"))
            else:
                candidates.update(base.glob("**/attachments.json") if "mcp" in parent else base.glob("*.json"))
        for path in candidates:
            if not path.is_file() or path.is_symlink() or path.stat().st_size > 64 << 20:
                continue
            try:
                value = json.loads(path.read_text())
            except (json.JSONDecodeError, UnicodeDecodeError):
                if "chat_history" not in path.parts and "conversation" not in path.parts:
                    raise ValueError("Unreadable current metadata: " + str(path.relative_to(self.docs)))
                self.skipped_history_metadata += 1
                continue
            try:
                parts = path.relative_to(self.docs).parts if path.is_relative_to(self.docs) else ()
                self.context_owner = (parts[1] if len(parts) > 1 and parts[0] == "_users" else
                    self.project_owners.get("/".join(parts[:2])))
                if path.name == "product-schedules.json":
                    owner = path.parent.parent.name
                    for key, row in value.items():
                        parts = key.split("/")
                        if parts[0] != "code" or len(parts) not in (2, 3):
                            continue
                        self.schedule_state_keys += 1
                        prefix = "product-project:" if len(parts) == 3 else "product:"
                        before = prefix + ":".join(parts)
                        after = prefix + "work:" + ":".join(parts[1:])
                        old_file = self.docs / "_system/condition_callers" / (hashlib.sha256((owner+"/"+before).encode()).hexdigest()+".json")
                        new_file = old_file.with_name(hashlib.sha256((owner+"/"+after).encode()).hexdigest()+".json")
                        if old_file.is_symlink():
                            raise ValueError("Watcher ledger must be a regular file")
                        if old_file.is_file():
                            if new_file.exists():
                                if not new_file.is_file() or new_file.is_symlink():
                                    raise ValueError("Canonical Watcher ledger must be a regular file")
                                self.retired_states.append(old_file)
                            else:
                                self.state_moves.append((old_file, new_file))
                        source = (row.get("watcher") or {}).get("source", {})
                        project = source.get("project_id")
                        if source.get("profile_id") == "code" and project:
                            folder = self.docs / "_users" / owner / "watchers/code" / hashlib.sha256(project.encode()).hexdigest()
                            destination = folder.parent.parent / "work" / folder.name
                            if folder.exists():
                                if folder.is_symlink() or destination.exists():
                                    raise ValueError("Watcher source location needs repair")
                                self.state_moves.append((folder, destination))
                    self.plan(path, self.transform(value, "schedule_states"))
                else:
                    self.plan(path, self.transform(value))
            except ValueError as error:
                raise ValueError(str(error) + " in " + str(path)) from None

        self.state_moves = list(dict.fromkeys(self.state_moves))
        self.retired_states = list(dict.fromkeys(self.retired_states))

    def databases(self, apply=False, backup=None):
        counts = collections.Counter()
        for file in (self.state / "auth").glob("*.sqlite"):
            uri = "file:" + str(file) + ("?mode=rw" if apply else "?mode=ro")
            db = sqlite3.connect(uri, uri=True)
            tables = {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
            jobs = []
            for table in ("access_tokens", "tokens", "codes", "requests", "cli_devices"):
                if table not in tables:
                    continue
                cols = {row[1] for row in db.execute(f'PRAGMA table_info("{table}")')}
                names = [x for x in ("scopes", "user_id", "crew_ids", "all_crews", "workflow_ids", "resource", "project_identity") if x in cols]
                for row in db.execute(f'SELECT rowid,{",".join(names)} FROM "{table}"'):
                    values = dict(zip(names, row[1:]))
                    updates = {}
                    old = json.loads(values["scopes"])
                    new = scopes(old)
                    if "code:run" in old and table != "access_tokens" and json.loads(values.get("workflow_ids") or "[]"):
                        # A machine grant keeps its existing workflow bounds;
                        # retiring a Code scope never adds new Crew authority.
                        new = ["crews:review" if x == "code:review" else x for x in old if x != "code:run"]
                    if old != new:
                        updates["scopes"] = json.dumps(new)
                    resource = values.get("resource", "")
                    if "/mcp/project/code/" in resource:
                        updated = resource.replace("/mcp/project/code/", "/mcp/project/work/")
                        project_id = urlsplit(updated).path.split("/project/work/", 1)[1].split("/", 1)[0]
                        manifest = self.ids[project_id]
                        old_identity = hashlib.sha256(json.dumps(["code", project_id, "", manifest["created_at"]], separators=(",", ":")).encode()).hexdigest()
                        if values.get("project_identity") and values["project_identity"] != old_identity:
                            raise ValueError("Project OAuth creation identity does not match")
                        updates["resource"] = updated
                        if "project_identity" in cols and values.get("project_identity"):
                            updates["project_identity"] = hashlib.sha256(json.dumps(["work", project_id, "", manifest["created_at"]], separators=(",", ":")).encode()).hexdigest()
                    if updates:
                        jobs.append((table, row[0], updates))
            if jobs and apply:
                saved = sqlite3.connect(backup / file.name)
                db.backup(saved)
                saved.close()
                with db:
                    for table, rowid, updates in jobs:
                        assignments = ",".join(f'"{key}"=?' for key in updates)
                        db.execute(f'UPDATE "{table}" SET {assignments} WHERE rowid=?', [*updates.values(), rowid])
            counts["database_rows"] += len(jobs)
            db.close()
        return counts

    def apply(self):
        stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        backup = self.state / "operator-backups" / ("crew-product-" + stamp)
        backup.mkdir(parents=True, mode=0o700)
        with tarfile.open(backup / "metadata.tar.gz", "w:gz", dereference=False) as archive:
            for file in self.files:
                label = "docs/" + str(file.relative_to(self.docs)) if file.is_relative_to(self.docs) else "state/" + str(file.relative_to(self.state))
                archive.add(file, arcname=label)
            for source, _ in self.state_moves:
                archive.add(source, arcname="docs/" + str(source.relative_to(self.docs)))
            for source in self.retired_states:
                archive.add(source, arcname="docs/" + str(source.relative_to(self.docs)))
            for source, _ in self.moves:
                archive.add(self.docs / source, arcname="docs/" + source)
        (backup / "links.json").write_text(json.dumps(self.links, indent=2) + "\n")
        (backup / "state-moves.json").write_text(json.dumps([[str(a.relative_to(self.docs)), str(b.relative_to(self.docs))] for a,b in self.state_moves], indent=2) + "\n")
        self.databases(True, backup)
        for file, raw in self.files.items():
            file.write_text(raw)
        for source, destination in self.moves:
            (self.docs / destination).parent.mkdir(parents=True, exist_ok=True)
            (self.docs / source).rename(self.docs / destination)
        for source, destination in self.state_moves:
            destination.parent.mkdir(parents=True, exist_ok=True)
            source.rename(destination)
        for source in self.retired_states:
            source.unlink()
        for alias, _ in self.links:
            (self.docs / alias).unlink()
        old_audit, audit = self.docs / "config/code-admin-audit", self.docs / "config/crew-admin-audit"
        if old_audit.exists():
            if audit.exists():
                raise ValueError("Both review audit directories exist")
            shutil.copytree(old_audit, backup / "code-admin-audit")
            old_audit.rename(audit)
        old_shares = self.docs / "config/code-shares.json"
        if old_shares.exists():
            shutil.copy2(old_shares, backup / "code-shares.json")
            old_shares.unlink()
        return backup


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--docs", type=Path, required=True)
    parser.add_argument("--state", type=Path, required=True)
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    conversion = Conversion(args.docs, args.state)
    conversion.collect()
    report = {"projects": len(conversion.original), "converted_code_projects": sum(x["product"] == "code" for x in conversion.original.values()),
              "metadata_files": len(conversion.files), "folder_moves": len(conversion.moves), "old_links": len(conversion.links),
              "watcher_state_moves": len(conversion.state_moves),
              "retired_state_twins": len(conversion.retired_states),
              "unresolved_context_paths": conversion.unresolved_contexts,
              "converted_schedule_state_keys": conversion.schedule_state_keys,
              "skipped_history_metadata": conversion.skipped_history_metadata,
              **conversion.databases()}
    if args.apply:
        report["backup"] = str(conversion.apply())
        report["applied"] = True
    print(json.dumps(report))


if __name__ == "__main__":
    main()
