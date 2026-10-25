import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("grouping", Path(__file__).parents[2] / "scripts/group-dashboard-tool-selections.py")
grouping = importlib.util.module_from_spec(spec)
spec.loader.exec_module(grouping)
catalog = Path(__file__).parents[2] / "agent_go/pkg/platformtools/dashboard-actions.json"


class DashboardSelections(unittest.TestCase):
    def test_partial_native_selection_preserves_foreign_tools_and_user_notes(self):
        # PLAT-825-113: migrating get must never grant the dashboard family.
        with tempfile.TemporaryDirectory() as temp:
            root, backup = Path(temp) / "docs", Path(temp) / "backup"
            project = root / "Crew" / "team"
            project.mkdir(parents=True)
            path = project / "product.json"
            original = {"tool_policy": {"enabled": ["get_dashboard"], "disabled": ["create_dashboard"]},
                        "selected_tools": ["foreign:get_dashboard", "dashboard:get_dashboard"],
                        "execution_defaults": {"enabled_custom_tools": ["get_dashboard_share", "share_dashboard"]},
                        "enabled_tools": [], "note": "get_dashboard is in my own notes"}
            path.write_text(json.dumps(original))
            self.assertEqual(grouping.migrate(root, backup, catalog, ["dashboard"]), 1)
            saved = json.loads(path.read_text())
            self.assertEqual(saved["tool_policy"], {"enabled": ["dashboard.get"], "disabled": ["dashboard.create"]})
            self.assertEqual(saved["selected_tools"], ["foreign:get_dashboard", "dashboard:dashboard.get"])
            self.assertEqual(saved["execution_defaults"]["enabled_custom_tools"], ["share.get", "share.set"])
            self.assertEqual(saved["enabled_tools"], [])
            self.assertEqual(saved["note"], original["note"])
            self.assertEqual(json.loads((backup / "Crew/team/product.json").read_text()), original)
            self.assertEqual(grouping.migrate(root, backup, catalog, ["dashboard"]), 0)


if __name__ == "__main__":
    unittest.main()
