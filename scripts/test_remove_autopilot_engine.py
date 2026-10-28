"""PLAT-825-97: retiring the engine must preserve actual Goal work and routing."""
import importlib.util
import json
import pathlib
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
            manifest = {"id": "example", "version": "1.0.51", "oversight_mode": "supervised",
                "knowledgebase_migration": {"done": True}, "contract_upgrade_history": [{"version": "1.0.51"}],
                "workflow_skills_migration": {"version": "1.0.51", "folder": "workflow-how-to"}, "pulse": {"enabled": True}, "post_run_monitor": True, "capabilities": {
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
            # PLAT-825-103: an in-progress skills receipt survives for a Flow
            # still owing the upgrade; a completed receipt and unused fields go.
            for version in ("1.0.49", "1.0.51"):
                relay = root / "Workflow" / ("flow-" + version)
                relay.mkdir()
                (relay / "workflow.json").write_text(json.dumps({"kind": "relay", "version": version,
                    "code_layout_version": 1, "oversight_mode": "manual", "contract_upgrade_history": [],
                    "knowledgebase_migration": {}, "workflow_skills_migration": {"keep": "unfinished"}}))
            first = removal.migrate(root, backup)
            for version in ("1.0.49", "1.0.51"):
                path = Path("Workflow") / ("flow-" + version) / "workflow.json"
                flow = json.loads((root / path).read_text())
                self.assertEqual(flow["code_layout_version"], 1)
                self.assertEqual("workflow_skills_migration" in flow, version == "1.0.49")
                self.assertFalse({"oversight_mode", "knowledgebase_migration", "contract_upgrade_history"}.intersection(flow))
                self.assertIn("oversight_mode", json.loads((backup / path).read_text()))
            self.assertEqual(first["retired_scheduler_runs"], 1)
            with sqlite3.connect(scheduler) as db:
                self.assertEqual(db.execute("SELECT state FROM schedule_runs WHERE run_id='review'").fetchone(), ("interrupted",))
                self.assertEqual(db.execute("SELECT state FROM schedule_runs WHERE run_id='work'").fetchone(), ("workflow_running",))
            saved = json.loads((goal / "workflow.json").read_text())
            self.assertEqual(saved["capabilities"]["selected_secrets"], ["APP_TOKEN"])
            self.assertEqual(saved["capabilities"]["selected_global_secret_names"], ["TEAM_TOKEN"])
            self.assertFalse({"oversight_mode", "knowledgebase_migration", "contract_upgrade_history", "workflow_skills_migration"}.intersection(saved))
            self.assertNotIn("pulse", saved)
            self.assertNotIn("post_run_monitor", saved)
            self.assertEqual(saved["schedules"][0], {"id": "daily"})
            self.assertEqual([item["id"] for item in saved["schedules"]], ["daily", "off", "basic", "hook"])
            self.assertTrue(all("after_run" not in item for item in saved["schedules"]))
            self.assertFalse(saved["backup"]["after_runs"])
            self.assertFalse(saved["publish"]["after_runs"])
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

    def test_rerun_after_rollback_merges_recreated_old_observation_table(self):
        # A failed activation renamed the table, then the older release that kept running recreated it.
        with tempfile.TemporaryDirectory() as tmp:
            database = pathlib.Path(tmp) / "db.sqlite"
            with sqlite3.connect(database) as db:
                db.execute("CREATE TABLE goal_observations (id INTEGER PRIMARY KEY, value REAL)")
                db.execute("INSERT INTO goal_observations VALUES (1, 42)")
                db.execute("CREATE TABLE pulse_goal_observations (id INTEGER PRIMARY KEY, value REAL)")
                db.execute("INSERT INTO pulse_goal_observations VALUES (1, 42), (2, 7)")
            removal.migrate_database(database, pathlib.Path(tmp) / "backup" / "db.sqlite")
            with sqlite3.connect(database) as db:
                self.assertEqual(db.execute("SELECT id, value FROM goal_observations ORDER BY id").fetchall(), [(1, 42), (2, 7)])
                self.assertIsNone(db.execute("SELECT 1 FROM sqlite_master WHERE name='pulse_goal_observations'").fetchone())

    def test_consolidates_after_run_choices_once_with_backup_and_flow_exclusion(self):
        # PLAT-825-112: paused schedules do not turn automation on; current
        # explicit false survives stale fields, and Flow never gains housekeeping.
        with tempfile.TemporaryDirectory() as directory:
            root, backup = Path(directory) / "workspace", Path(directory) / "backup"
            cases = {
                "scheduled": {"schedules": [{"id": "on", "enabled": True, "after_run": {"backup": True}}, {"id": "paused", "enabled": False, "after_run": {"publish": True}}]},
                "manual": {"after_manual_run": {"publish": True}, "backup": {"enabled": True, "triggers": {"after_manual_run": True}}, "schedules": []},
                "trigger": {"backup": {"enabled": True, "triggers": {"after_manual_run": True}}, "schedules": []},
                "current": {"backup": {"after_runs": False}, "schedules": [{"enabled": True, "after_run": {"backup": True}}]},
                "flow": {"kind": "relay", "backup": {"enabled": True, "destinations": [{"id": "keep"}], "triggers": {"after_manual_run": True}}, "after_manual_run": {"backup": True}, "schedules": [{"enabled": True, "after_run": {"publish": True}}]},
            }
            for name, config in cases.items():
                path = root / "Workflow" / name / "workflow.json"
                path.parent.mkdir(parents=True); path.write_text(json.dumps(config))
            removal.migrate(root, backup)
            for name, expected in {"scheduled": (True, False), "manual": (False, True), "trigger": (True, False), "current": (False, False), "flow": (False, False)}.items():
                path = Path("Workflow") / name / "workflow.json"
                saved = json.loads((root/path).read_text())
                self.assertEqual((saved["backup"]["after_runs"], saved["publish"]["after_runs"]), expected, name)
                self.assertNotIn("after_manual_run", saved)
                self.assertTrue(all("after_run" not in row for row in saved.get("schedules", [])))
                self.assertTrue(all("triggers" not in saved[option] for option in ("backup", "publish")))
                self.assertEqual(json.loads((backup/path).read_text()), cases[name])
            self.assertEqual(json.loads((root/"Workflow/flow/workflow.json").read_text())["backup"]["destinations"], [{"id": "keep"}])
            rerun = removal.migrate(root, Path(directory)/"again")
            self.assertEqual(rerun["workflow_configs"], 0)
            self.assertFalse((Path(directory)/"again").exists())


if __name__ == "__main__":
    unittest.main()
