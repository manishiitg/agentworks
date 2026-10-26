import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("grouping", Path(__file__).parents[2] / "scripts/group-platform-tool-selections.py")
grouping = importlib.util.module_from_spec(spec)
spec.loader.exec_module(grouping)
catalog = Path(__file__).parents[2] / "agent_go/pkg/platformtools/catalog/dashboard-actions.json"
agent_catalog = Path(__file__).parents[2] / "agent_go/pkg/platformtools/catalog/agent-actions.json"
experience_catalog = Path(__file__).parents[2] / "agent_go/pkg/platformtools/catalog/experience-actions.json"
data_catalog = Path(__file__).parents[2] / "agent_go/pkg/platformtools/catalog/data-actions.json"


class PlatformSelections(unittest.TestCase):
    def test_partial_native_selection_preserves_foreign_tools_and_user_notes(self):
        # PLAT-825-113: migrating get must never grant the dashboard family.
        with tempfile.TemporaryDirectory() as temp:
            root, backup = Path(temp) / "docs", Path(temp) / "backup"
            project = root / "Crew" / "team"
            project.mkdir(parents=True)
            path = project / "product.json"
            original = {"tool_policy": {"enabled": ["get_dashboard"], "disabled": ["create_dashboard"]},
                        "selected_tools": ["foreign:get_dashboard", "dashboard:get_dashboard", "workspace_advanced:diff_patch_workspace_file", "foreign:diff_patch_workspace_file", "workspace_browser:agent_browser", "foreign:agent_browser", "human_tools:create_human_input_request", "foreign:create_human_input_request"],
                        "selected_custom_tools": ["query_workflow_db"],
                        "execution_defaults": {"enabled_custom_tools": ["get_dashboard_share", "share_dashboard"]},
                        "enabled_tools": [], "enabled_custom_tools": ["manage_vault_secret_access", "foreign:manage_vault_secret_access"], "note": "get_dashboard is in my own notes"}
            path.write_text(json.dumps(original))
            catalogs = [catalog, data_catalog, agent_catalog, experience_catalog]
            namespaces = ["dashboard", "workspace_advanced", "workspace_browser", "human_tools"]
            self.assertEqual(grouping.migrate(root, backup, catalogs, namespaces), 1)
            saved = json.loads(path.read_text())
            self.assertEqual(saved["tool_policy"], {"enabled": ["dashboard.get"], "disabled": ["dashboard.create"]})
            self.assertEqual(saved["selected_tools"], ["foreign:get_dashboard", "dashboard:dashboard.get", "workspace_advanced:files.patch", "foreign:diff_patch_workspace_file", "workspace_browser:browser.execute", "foreign:agent_browser", "human_tools:needs_you.create", "foreign:create_human_input_request"])
            self.assertEqual(saved["selected_custom_tools"], ["database.query", "database.describe", "database.integrity_check"])
            self.assertEqual(saved["execution_defaults"]["enabled_custom_tools"], ["share.get", "share.set"])
            self.assertEqual(saved["enabled_tools"], [])
            self.assertEqual(saved["enabled_custom_tools"], ["vault.shared_secrets", "vault.grant_secret", "vault.set_value", "vault.share_secret", "vault.delete_shared_secret", "foreign:manage_vault_secret_access"])
            self.assertEqual(saved["note"], original["note"])
            self.assertEqual(json.loads((backup / "Crew/team/product.json").read_text()), original)
            self.assertEqual(grouping.migrate(root, backup, catalogs, namespaces), 0)


if __name__ == "__main__":
    unittest.main()
