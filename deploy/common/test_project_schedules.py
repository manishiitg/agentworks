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
            self.assertEqual(json.loads((goal / "workflow.json").read_text())["schedules"], [hook])
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


if __name__ == "__main__":
    unittest.main()
