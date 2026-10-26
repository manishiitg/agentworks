"""PLAT-871-15 migration data/identity regression; no server or model calls."""
import hashlib
import importlib.util
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("project_schedules", Path(__file__).resolve().parents[2] / "scripts" / "migrate-project-schedules.py")
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


class ProjectScheduleMigrationTest(unittest.TestCase):
    def test_migration_preserves_messages_dependency_callers_hooks_and_receipts(self):
        with tempfile.TemporaryDirectory() as temp:
            root, backup = Path(temp) / "docs", Path(temp) / "backup"
            goal, crew, relay = root / "Workflow" / "example", root / "Crew" / "helper-id", root / "Workflow" / "relay"
            for folder in (goal, crew, relay, root / "_system"):
                folder.mkdir(parents=True)
            hook = {"id": "external", "name": "External", "schedule_type": "webhook", "enabled": True,
                    "webhook": {"auth_mode": "bearer", "encrypted_secret": "opaque-fixture"}}
            original = {"id": "example", "created_by": "owner", "access": {"owners": ["owner"]}, "schedules": [
                {"id": "daily", "name": "Daily", "enabled": False, "cron_expression": "0 9 * * *", "timezone": "UTC",
                 "messages": ["Collect.", "Check.", "Report."], "group_names": ["default"], "route_selections": {"router": "report"},
                 "after_schedule_ids": ["collect"], "after_terminal_status": "any_terminal", "after_delay_minutes": 5,
                 "dependency_deadline": "12:00", "max_start_delay_minutes": 30, "collision_policy": "skip"}, hook]}
            (goal / "workflow.json").write_text(json.dumps(original))
            (crew / "product.json").write_text(json.dumps({"id": "helper", "owner_id": "owner", "product": "work"}))
            (crew / "workflow.json").write_text(json.dumps({"id": "helper", "schedules": [{"id": "check", "name": "Check", "enabled": True,
                "cron_expression": "0 10 * * *", "timezone": "UTC", "messages": ["Check."], "chat_key": "helper:chat:check", "isolated": True}]}))
            (relay / "workflow.json").write_text(json.dumps({"id": "relay", "kind": "relay", "created_by": "owner", "schedules": [
                {"id": "date", "name": "Date", "enabled": True, "schedule_type": "calendar", "timezone": "UTC",
                 "calendar_items": [{"id": "once", "date": "2027-01-01", "time": "10:00", "trigger_payload": {"task": "check"}}]}]}))
            function = {"id": "typed-entry", "name": "Typed entry", "enabled": True, "schedule_type": "webhook", "kind": "function",
                        "group_names": ["default", "other"], "route_selections": {"router": "report"}, "webhook": {"step_id": "inspect"},
                        "function": {"name": "inspect", "allowed_callers": [{"type": "workflow", "id": "source"}],
                                     "inputs": [{"name": "query", "type": "string", "required": True, "enum": ["one", "two"]}]}}
            original["schedules"].append(function)
            (goal / "workflow.json").write_text(json.dumps(original))
            crew_runtime = json.loads((crew / "workflow.json").read_text())
            binding = {"id": "bound-step", "name": "Saved step", "enabled": True, "kind": "internal", "message": "Check the input.",
                       "caller": {"type": "workflow", "id": "source"}, "run_destination": "isolated"}
            crew_runtime["triggers"] = [binding, {**binding, "id": "generated-adapter", "workspace_function": True}]
            (crew / "workflow.json").write_text(json.dumps(crew_runtime))
            (crew / "functions.json").write_text(json.dumps({"version": 1, "functions": [{"name": "research", "instructions": "Research.", "execution": "fresh"}]}))
            release = root / "Workflow" / ".relay_releases" / "relay" / "v1"
            release.mkdir(parents=True)
            (release / "workflow.json").write_text(json.dumps({"id": "relay", "kind": "relay", "schedules": [function]}))
            original_hash = hashlib.sha256(b"workflow.json\0" + (release / "workflow.json").read_bytes() + b"\0").hexdigest()
            (release / "release.json").write_text(json.dumps({"version": "v1", "hash": original_hash, "files": ["workflow.json"], "file_count": 1}))
            corrupt = release.parent / "v2"
            corrupt.mkdir()
            (corrupt / "workflow.json").write_bytes((release / "workflow.json").read_bytes())
            (corrupt / "release.json").write_text(json.dumps({"version": "v2", "hash": "deliberately-wrong", "files": ["workflow.json"], "file_count": 1}))
            corrupt_before = {file.name: file.read_bytes() for file in corrupt.iterdir()}
            published = root / "_users" / "owner" / "crew-published" / "workspace" / "checks" / "revision" / "source"
            published.mkdir(parents=True)
            (published / "functions.json").write_text(json.dumps({"version": 1, "functions": [{"name": "check", "instructions": "Check the report."}]}))
            db = root / "_system" / "schedule-state.sqlite"
            with sqlite3.connect(db) as connection:
                connection.execute("CREATE TABLE receipts (id TEXT, status TEXT)")
                connection.execute("INSERT INTO receipts VALUES ('previous-call','completed')")
            state_path = root / "_users" / "owner" / "chat_history" / "product-schedules.json"
            state_path.parent.mkdir(parents=True)
            state_path.write_text(json.dumps({"work/helper/check": {"enabled": False, "last_status": "success", "last_run_at": "2026-01-01T00:00:00Z"}}))
            before_hash = hashlib.sha256(db.read_bytes()).hexdigest()
            counts = migration.migrate(root, check=True)
            self.assertEqual((counts["timers"], counts["goal_message_timers"], counts["after_dependencies"], counts["http_webhooks"]), (3, 1, 1, 1))
            self.assertFalse((goal / "schedules.json").exists())
            migration.migrate(root, backup)
            saved = json.loads((goal / "schedules.json").read_text())["schedules"][0]
            self.assertEqual(saved["input"]["messages"], ["Collect.", "Check.", "Report."])
            self.assertEqual(saved["input"]["routes"], {"router": "report"})
            self.assertEqual(saved["when"]["after"]["deadline"], "12:00")
            self.assertEqual(saved["run_as"], "owner")
            self.assertFalse(saved["enabled"])
            self.assertNotIn("max_start_delay_minutes", saved)
            self.assertEqual(json.loads((goal / "workflow.json").read_text())["schedules"], [])
            saved_hook = json.loads((goal / "webhooks.json").read_text())["webhooks"][0]
            self.assertEqual(saved_hook["auth"], {"mode": "bearer", "encrypted_secret": "opaque-fixture"})
            self.assertEqual(saved_hook["path"], "/api/hooks/external")
            saved_function = json.loads((goal / "functions.json").read_text())["functions"][0]
            self.assertEqual(saved_function["action"]["step"], "inspect")
            self.assertEqual(saved_function["input"]["routes"], {"router": "report"})
            self.assertEqual(saved_function["allowed_callers"], function["function"]["allowed_callers"])
            self.assertEqual(saved_function["input_schema"]["required"], ["query"])
            published_function = json.loads((published / "functions.json").read_text())["functions"][0]
            self.assertEqual(published_function["action"], {"type": "message", "tab": "check"})
            self.assertEqual(published_function["input"]["messages"], ["Check the report."])
            crew_functions = json.loads((crew / "functions.json").read_text())["functions"]
            self.assertEqual(len(crew_functions), 2)
            self.assertEqual(next(fn for fn in crew_functions if fn["name"] == "research")["action"], {"type": "message", "conversation": "fresh"})
            self.assertEqual(next(fn for fn in crew_functions if fn["id"] == "bound-step")["allowed_callers"], [binding["caller"]])
            self.assertNotIn("triggers", json.loads((crew / "workflow.json").read_text()))
            release_metadata = json.loads((release / "release.json").read_text())
            digest = hashlib.sha256()
            for relative in release_metadata["files"]:
                digest.update(relative.encode() + b"\0" + (release / relative).read_bytes() + b"\0")
            self.assertEqual(release_metadata["hash"], digest.hexdigest())
            self.assertIn("functions.json", release_metadata["files"])
            self.assertEqual(counts["flow_releases_skipped_hash_mismatch"], 1)
            self.assertEqual({file.name: file.read_bytes() for file in corrupt.iterdir()}, corrupt_before)
            self.assertIn(corrupt.relative_to(root).as_posix(), counts["flow_release_skip_paths"])
            self.assertEqual(json.loads((backup / "Workflow" / "example" / "workflow.json").read_text()), original)
            self.assertEqual(hashlib.sha256(db.read_bytes()).hexdigest(), before_hash)
            with sqlite3.connect(backup / "schedule-state.sqlite") as connection:
                self.assertEqual(connection.execute("SELECT * FROM receipts").fetchall(), [("previous-call", "completed")])
            crew_saved = json.loads((crew / "schedules.json").read_text())["schedules"][0]
            self.assertEqual(crew_saved["action"], {"type": "message", "tab": "helper:chat:check", "conversation": "isolated"})
            self.assertFalse(crew_saved["enabled"])
            state = json.loads(state_path.read_text())["work/helper/check"]
            self.assertNotIn("enabled", state)
            self.assertEqual(state["last_status"], "success")
            dates = json.loads((relay / "schedules.json").read_text())["schedules"][0]["when"]["dates"]
            self.assertEqual(dates[0]["input"]["payload"], {"task": "check"})
            self.assertEqual(migration.migrate(root, backup).get("changed_files"), 0)

    def test_local_caller_requires_explicit_single_user_conversion(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp)/"docs"
            for kind in ("Workflow","Crew"):
                project=root/kind/"local"
                project.mkdir(parents=True)
                (project/"workflow.json").write_text(json.dumps({"id":"local","schedules":[{"id":"daily","name":"Daily","cron_expression":"0 9 * * *","enabled":True}]}))
            with self.assertRaisesRegex(ValueError,"no recorded owner"):
                migration.migrate(root,check=True)
            migration.migrate(root,Path(temp)/"backup",local_user_id="local-person")
            for kind in ("Workflow","Crew"):
                saved=json.loads((root/kind/"local"/"schedules.json").read_text())["schedules"][0]
                self.assertEqual(saved["run_as"],"local-person")


if __name__ == "__main__":
    unittest.main()
