import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("simplification", Path(__file__).parents[2] / "scripts/simplify-workflow-settings.py")
simplification = importlib.util.module_from_spec(spec)
spec.loader.exec_module(simplification)


class WorkflowSettings(unittest.TestCase):
    def test_retired_webhook_names_leave_selections_without_deleting_secrets_or_active_settings(self):
        # PLAT-825-113: removing a backend-only credential reference must not
        # turn its old selected name into an agent-visible environment secret.
        with tempfile.TemporaryDirectory() as temp:
            root, backup = Path(temp) / "docs", Path(temp) / "backup"
            project = root / "_users" / "owner" / "Workflow" / "team"
            project.mkdir(parents=True)
            path = project / "workflow.json"
            notifications = {"slack_webhook_secret_name": "OLD_HOOK", "run_summary_slack_webhook_secret_names": ["OTHER_HOOK"], "block_recipients": ["old@example.com"], "instructions": "old", "run_summary_gmail_connection_ids": ["old"], "run_summary_recipients": ["team@example.com"], "gmail_connection_id": "selected-account", "exclude_channels": ["whatsapp"], "break_alerts": False}
            original = {"run_retention_count": 10, "execution_defaults": {"disable_learning": False}, "capabilities": {"cdp_ports": [], "selected_secrets": ["APP_TOKEN", "OLD_HOOK"], "selected_global_secret_names": ["OTHER_HOOK", "GLOBAL_APP"], "notifications": notifications, "knowledgebase_sources": [{"path": "Brain/team", "access": "read"}]}, "note": "execution_defaults in user prose stays"}
            path.write_text(json.dumps(original))
            secret = project / "encrypted-secrets.json"
            secret.write_text("opaque encrypted data")
            generic = root / "_users/owner/multiagent-config.json"
            generic.write_text(json.dumps({"capabilities": {"notifications": {"slack_webhook_secret_name": "GENERIC_HOOK"}}}))
            self.assertEqual(simplification.migrate(root, backup), 1)
            result = json.loads(path.read_text())
            self.assertNotIn("execution_defaults", result)
            self.assertNotIn("run_retention_count", result)
            caps = result["capabilities"]
            self.assertNotIn("cdp_ports", caps)
            self.assertEqual(caps["selected_secrets"], ["APP_TOKEN"])
            self.assertEqual(caps["selected_global_secret_names"], ["GLOBAL_APP"])
            self.assertEqual(caps["notifications"], {k: v for k, v in notifications.items() if k not in simplification.RETIRED_NOTIFICATIONS})
            self.assertEqual(caps["knowledgebase_sources"], original["capabilities"]["knowledgebase_sources"])
            self.assertEqual(result["note"], original["note"])
            self.assertEqual(json.loads((backup / path.relative_to(root)).read_text()), original)
            self.assertEqual(secret.read_text(), "opaque encrypted data")
            self.assertIn("GENERIC_HOOK", generic.read_text())
            self.assertEqual(simplification.migrate(root, backup), 0)


if __name__ == "__main__":
    unittest.main()
