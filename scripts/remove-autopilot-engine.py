#!/usr/bin/env python3
"""One-off PLAT-825-97 data change. Stop the app before running this script.

Use the workspace service's document root and a separate backup directory.
Keep this script only until every installation has moved to the new release.
"""

import argparse
import json
import re
from pathlib import Path
import shutil
import sqlite3


ENGINE_TABLES = (
    "goal_lead_builder_asks", "goal_lead_conversation", "goal_lead_messages",
    "goal_lead_qa_requests", "pulse_consolidation", "pulse_context_records",
    "pulse_decisions", "pulse_fast_request", "pulse_final_command_state",
    "pulse_finding_details", "pulse_finding_events", "pulse_finding_issue_aliases",
    "pulse_fix_attempt_findings", "pulse_fix_attempts", "pulse_fix_verifications",
    "pulse_goal_check_runs", "pulse_goal_checks", "pulse_goal_work",
    "pulse_impact_assessments", "pulse_intervention_effects",
    "pulse_intervention_sources", "pulse_interventions", "pulse_issues",
    "pulse_module_audit", "pulse_module_result_history", "pulse_module_state",
    "pulse_prompt_budget_metrics", "pulse_recommendations",
    "pulse_review_focus_history", "pulse_review_focus_state", "pulse_review_log",
    "pulse_review_notes", "pulse_review_recovery", "pulse_reviews",
    "pulse_run_mode", "pulse_schedule_state", "pulse_schema_migrations",
    "pulse_shadow_signal_observation",
)

KEYS = {"pulse_llm": "background_llm"}
RETIRED_NOTIFICATION_KEYS = {"daily_digest", "daily_digest_off"}

RETIRED_TOOLS = {
    "ask_pulse", "ask_goal_lead", "ask_builder", "manage_pulse",
    "schedule_message_wakeup", "builder_pulse_chat", "builder_pulse_status",
}


def rename_config(value):
    if isinstance(value, list):
        return [rename_config(item) for item in value]
    if not isinstance(value, dict):
        return value
    out = {}
    for key, item in value.items():
        if key.startswith(("pulse_summary", "goal_update")) or key in RETIRED_NOTIFICATION_KEYS:
            continue
        if key in ("selected_tools", "enabled_tools") and isinstance(item, list):
            item = [name for name in item if str(name).split(":")[-1] not in RETIRED_TOOLS]
        if key == "selected_skills" and isinstance(item, list):
            item = [name for name in item if name not in ("pulse", "panel-pulse")]
        out[KEYS.get(key, key)] = rename_config(item)
    return out


def migrate_manifest(document):
    if document.get("kind") == "relay":
        return document
    # Retiring a delivery reference must not turn its webhook credential into
    # an agent-visible selected secret (the existing backend-only boundary).
    caps = document.get("capabilities") or {}
    notifications = caps.get("notifications") or {}
    retired_webhooks = {name for key in ("pulse_summary_slack_webhook_secret_names", "goal_update_slack_webhook_secret_names")
                        for name in (notifications.get(key) or []) if isinstance(name, str)}
    for key in ("selected_secrets", "selected_global_secret_names"):
        if isinstance(caps.get(key), list):
            caps[key] = [name for name in caps[key] if name not in retired_webhooks]
    old = document.pop("pulse", {}) or {}
    old_post_run = document.pop("post_run_monitor", False)
    inherited = bool(old.get("enabled") or old_post_run)
    document.pop("post_run_monitor_mode", None)
    schedules = document.get("schedules", [])
    inherited = inherited or any(item.get("enabled") and
        (item.get("pulse_review_only") or str(item.get("pulse_mode", "")).strip().lower() == "full")
        for item in schedules)
    kept = []
    for schedule in schedules:
        if schedule.get("pulse_review_only"):
            continue
        if schedule.get("after_run") is None:
            mode = str(schedule.get("pulse_mode", "")).strip().lower()
            enabled = False if mode == "off" else True if mode in ("basic", "full") else inherited
            if str(schedule.get("schedule_type", "")).lower() == "webhook":
                enabled = False
            schedule["after_run"] = dict.fromkeys(("backup", "publish"), enabled)
        schedule["after_run"].pop("notify", None)
        for key in ("pulse_mode", "pulse_mode_reason", "pulse_review_only"):
            schedule.pop(key, None)
        kept.append(schedule)
    if "schedules" in document:
        document["schedules"] = kept
    if isinstance(document.get("after_manual_run"), dict):
        document["after_manual_run"].pop("notify", None)
    publish = document.get("publish")
    if isinstance(publish, dict) and isinstance(publish.get("targets"), list):
        targets = publish["targets"]
        def retired(target):
            if isinstance(target, str):
                return target.strip().lower() == "pulse"
            return isinstance(target, dict) and any(str(target.get(key, "")).strip().lower() == "pulse"
                for key in ("id", "name", "type", "kind"))
        publish["targets"] = [target for target in targets if not retired(target)]
        if targets and not publish["targets"]:
            publish["enabled"] = False
    return rename_config(document)


def quote(name):
    return '"' + name.replace('"', '""') + '"'


def migrate_notifications(db):
    row = db.execute("SELECT sql FROM sqlite_master WHERE type='table' AND name='org_dashboard_notifications'").fetchone()
    if not row or not any(kind in row[0] for kind in ("pulse_summary", "goal_update")):
        return
    schema = re.sub(r"CHECK\s*\(\s*notification_kind\s+IN\s*\([^)]*\)\s*\)",
                    "CHECK(notification_kind IN ('run_summary'))", row[0], flags=re.I)
    if schema == row[0]:
        raise RuntimeError("unrecognized notification-kind constraint; database is backed up")
    objects = [row[0] for row in db.execute(
        "SELECT sql FROM sqlite_master WHERE tbl_name='org_dashboard_notifications' AND type IN ('index','trigger') AND sql IS NOT NULL"
    )]
    columns = [row[1] for row in db.execute("PRAGMA table_info(org_dashboard_notifications)")]
    db.execute("ALTER TABLE org_dashboard_notifications RENAME TO retiring_autopilot_notifications")
    db.execute(schema)
    names = ','.join(map(quote, columns))
    db.execute(f"INSERT INTO org_dashboard_notifications ({names}) SELECT {names} FROM retiring_autopilot_notifications WHERE notification_kind='run_summary'")
    db.execute("DROP TABLE retiring_autopilot_notifications")
    for sql in objects:
        if not any(kind in sql for kind in ("pulse_summary", "goal_update")):
            db.execute(sql)


def migrate_database(path, backup):
    with sqlite3.connect(path) as db:
        tables = {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
        notification = db.execute("SELECT sql FROM sqlite_master WHERE type='table' AND name='org_dashboard_notifications'").fetchone()
        has_source = "report_human_inputs" in tables and "source" in {
            row[1] for row in db.execute("PRAGMA table_info(report_human_inputs)")}
        old_decisions = has_source and db.execute("SELECT 1 FROM report_human_inputs WHERE source='pulse' LIMIT 1").fetchone()
        changed = bool(old_decisions or tables.intersection(ENGINE_TABLES) or "pulse_goal_observations" in tables or
                       notification and any(kind in notification[0] for kind in ("pulse_summary", "goal_update")))
        if not changed:
            return 0
        backup.parent.mkdir(parents=True, exist_ok=True)
        with sqlite3.connect(backup) as copy:
            db.backup(copy)
        db.execute("PRAGMA foreign_keys=OFF")
        with db:
            db.execute("BEGIN")
            if "pulse_goal_observations" in tables:
                db.execute("ALTER TABLE pulse_goal_observations RENAME TO goal_observations")
            if has_source:
                db.execute("UPDATE report_human_inputs SET source='goal' WHERE source='pulse'")
            migrate_notifications(db)
            for name in ENGINE_TABLES:
                db.execute(f"DROP TABLE IF EXISTS {quote(name)}")
        if db.execute("PRAGMA integrity_check").fetchone()[0] != "ok":
            raise RuntimeError(f"database integrity check failed: {path}")
    return len(tables.intersection(ENGINE_TABLES))


def retire_scheduler_runs(root, backups):
    path = root / "_system" / "schedule-state.sqlite"
    if not path.exists():
        return 0
    with sqlite3.connect(path) as db:
        if not db.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='schedule_runs'").fetchone():
            return 0
        retired = "('pulse_running','pulse_gate','pulse_modules','pulse_finalizing')"
        count = db.execute("SELECT count(*) FROM schedule_runs WHERE state IN " + retired).fetchone()[0]
        if not count:
            return 0
        backup = backups / path.relative_to(root)
        backup.parent.mkdir(parents=True, exist_ok=True)
        with sqlite3.connect(backup) as copy:
            db.backup(copy)
        with db:
            db.execute("UPDATE schedule_runs SET state='interrupted', completed_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'), updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'), error_message='Built-in review engine removed' WHERE state IN " + retired)
        return count


def migrate(root, backups):
    root, backups = root.resolve(), backups.resolve()
    workflows = tables = databases = 0
    # Only Goal/Flow manifests are changed. Crew files, chat histories and user
    # notes (including Brain) are outside this migration.
    # Goal roots are shared Workflow/<name>; include old per-user roots that
    # may still exist without walking releases, backups or run artifacts.
    folders = [root / "Workflow"] + list((root / "_users").glob("*/Workflow"))
    manifests = sorted(path for folder in folders for path in folder.glob("*/workflow.json"))
    for path in manifests:
        before = path.read_text()
        document = json.loads(before)
        document = migrate_manifest(document)
        if document != json.loads(before):
            destination = backups / path.relative_to(root)
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, destination)
            path.write_text(json.dumps(document, indent=2, ensure_ascii=False) + "\n")
            workflows += 1
        database = path.parent / "db" / "db.sqlite"
        if database.exists():
            count = migrate_database(database, backups / database.relative_to(root))
            tables += count
            if (backups / database.relative_to(root)).exists():
                databases += 1
    retired_runs = retire_scheduler_runs(root, backups)
    result = {"retired_scheduler_runs": retired_runs, "workflow_configs": workflows, "databases": databases, "engine_tables_removed": tables,
              "backup_directory": str(backups)}
    print(json.dumps(result))
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workspace-dir", type=Path, required=True)
    parser.add_argument("--backup-dir", type=Path, required=True)
    args = parser.parse_args()
    migrate(args.workspace_dir, args.backup_dir)
