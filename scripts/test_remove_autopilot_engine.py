"""PLAT-825-97: retiring the engine must preserve actual Goal work and routing."""
import importlib.util
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("removal", Path(__file__).with_name("remove-autopilot-engine.py"))
removal = importlib.util.module_from_spec(spec)
spec.loader.exec_module(removal)


class RetireEngineTest(unittest.TestCase):
    def test_backed_up_removal_preserves_measurements_decisions_and_notifications(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "workspace"
            backup = Path(directory) / "backup"
            goal = root / "Workflow" / "example"
            (goal / "db").mkdir(parents=True)
            manifest = {"id": "example", "pulse": {"enabled": True}, "post_run_monitor": True, "capabilities": {
                "selected_secrets": ["RETIRED_WEBHOOK", "APP_TOKEN"], "selected_global_secret_names": ["RETIRED_WEBHOOK", "TEAM_TOKEN"],
                "llm_config": {"pulse_llm": {"provider": "example", "model_id": "selected"}},
                "notifications": {"pulse_summary_slack_webhook_secret_names": ["RETIRED_WEBHOOK"], "daily_digest_off": True, "goal_update_channels": ["gmail"], "run_summary_recipients": ["team@example.com"], "pulse_summary_recipients": ["owner@example.com"], "wake_tab": {"crew_id": "crew", "chat_id": "tab"}}},
                "publish": {"enabled": True, "targets": ["report", "pulse"]},
                "schedules": [{"id": "daily", "after_run": {"backup": True, "notify": True}},
                    {"id": "off", "pulse_mode": "off"}, {"id": "basic", "pulse_mode": "basic"},
                    {"id": "engine-clock", "pulse_review_only": True},
                    {"id": "hook", "schedule_type": "webhook", "pulse_mode": "full"}]}
            (goal / "workflow.json").write_text(json.dumps(manifest))
            notes = root / "Brain" / "Shared" / "notes.md"
            notes.parent.mkdir(parents=True)
            notes.write_text("My own notes")
            with sqlite3.connect(goal / "db" / "db.sqlite") as db:
                db.execute("CREATE TABLE pulse_goal_observations (id INTEGER PRIMARY KEY, value REAL)")
                db.execute("INSERT INTO pulse_goal_observations VALUES (1, 42)")
                for table in ("workflow_goal_metrics", "report_human_input_events", "goal_run_facts", "run_concerns", "background_agent_log"):
                    db.execute(f"CREATE TABLE {table} (record TEXT)")
                    db.execute(f"INSERT INTO {table} VALUES ('keep')")
                db.execute("CREATE TABLE report_human_inputs (record TEXT, source TEXT)")
                db.execute("INSERT INTO report_human_inputs VALUES ('keep', 'pulse')")
                db.execute("CREATE TABLE org_dashboard_notifications (id TEXT PRIMARY KEY, notification_kind TEXT CHECK(notification_kind IN ('run_summary','pulse_summary')), message TEXT)")
                db.execute("CREATE INDEX notice_kind ON org_dashboard_notifications(notification_kind)")
                db.execute("INSERT INTO org_dashboard_notifications VALUES ('notice','pulse_summary','Existing notice')")
                db.execute("INSERT INTO org_dashboard_notifications VALUES ('run','run_summary','Run outcome')")
                for table in removal.ENGINE_TABLES:
                    db.execute(f"CREATE TABLE {table} (record TEXT)")
            scheduler = root / "_system" / "schedule-state.sqlite"
            scheduler.parent.mkdir()
            with sqlite3.connect(scheduler) as db:
                db.execute("CREATE TABLE schedule_runs (run_id TEXT, state TEXT, completed_at TEXT, updated_at TEXT, error_message TEXT)")
                db.execute("INSERT INTO schedule_runs VALUES ('review', 'pulse_running', NULL, '', '')")
                db.execute("INSERT INTO schedule_runs VALUES ('work', 'workflow_running', NULL, '', '')")
            first = removal.migrate(root, backup)
            self.assertEqual(first["retired_scheduler_runs"], 1)
            with sqlite3.connect(scheduler) as db:
                self.assertEqual(db.execute("SELECT state FROM schedule_runs WHERE run_id='review'").fetchone(), ("interrupted",))
                self.assertEqual(db.execute("SELECT state FROM schedule_runs WHERE run_id='work'").fetchone(), ("workflow_running",))
            saved = json.loads((goal / "workflow.json").read_text())
            self.assertEqual(saved["capabilities"]["selected_secrets"], ["APP_TOKEN"])
            self.assertEqual(saved["capabilities"]["selected_global_secret_names"], ["TEAM_TOKEN"])
            self.assertNotIn("pulse", saved)
            self.assertNotIn("post_run_monitor", saved)
            self.assertEqual(saved["schedules"][0], {"id": "daily", "after_run": {"backup": True}})
            self.assertEqual([item["id"] for item in saved["schedules"]], ["daily", "off", "basic", "hook"])
            self.assertEqual(saved["schedules"][1]["after_run"], dict.fromkeys(("backup", "publish"), False))
            self.assertEqual(saved["schedules"][2]["after_run"], dict.fromkeys(("backup", "publish"), True))
            self.assertFalse(saved["schedules"][3]["after_run"]["backup"])
            self.assertEqual(saved["publish"]["targets"], ["report"])
            self.assertEqual(saved["capabilities"]["notifications"]["run_summary_recipients"], ["team@example.com"])
            self.assertFalse(any(key.startswith(("pulse_summary", "goal_update", "daily_digest")) for key in saved["capabilities"]["notifications"]))
            self.assertEqual(saved["capabilities"]["notifications"]["wake_tab"], manifest["capabilities"]["notifications"]["wake_tab"])
            self.assertEqual(saved["capabilities"]["llm_config"]["background_llm"]["model_id"], "selected")
            self.assertEqual(notes.read_text(), "My own notes")
            with sqlite3.connect(goal / "db" / "db.sqlite") as db:
                self.assertEqual(db.execute("SELECT value FROM goal_observations").fetchone(), (42,))
                self.assertEqual(db.execute("SELECT record,source FROM report_human_inputs").fetchone(), ("keep", "goal"))
                self.assertEqual(db.execute("SELECT notification_kind,message FROM org_dashboard_notifications").fetchone(), ("run_summary", "Run outcome"))
                self.assertFalse({row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}.intersection(removal.ENGINE_TABLES))
                db.execute("INSERT INTO org_dashboard_notifications VALUES ('new','run_summary','Still works')")
            with sqlite3.connect(backup / "Workflow" / "example" / "db" / "db.sqlite") as db:
                self.assertEqual(db.execute("SELECT value FROM pulse_goal_observations").fetchone(), (42,))
                self.assertEqual(db.execute("SELECT message FROM org_dashboard_notifications WHERE notification_kind='pulse_summary'").fetchone(), ("Existing notice",))
            second = removal.migrate(root, Path(directory) / "second-backup")
            self.assertEqual(second["retired_scheduler_runs"], 0)
            self.assertEqual(second["workflow_configs"], 0)
            self.assertEqual(second["databases"], 0)
            self.assertFalse((Path(directory) / "second-backup").exists())


if __name__ == "__main__":
    unittest.main()
